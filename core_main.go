package main

import (
	"archive/zip"
	"bot-vps-control/internal/tg"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ==============================================================================
// ⚙️ KONFIGURASI DEFAULT (semua diisi lewat .env atau Environment Variable)
// ==============================================================================
//
// Tidak ada kredensial yang ditanam di source:
//   - BOT_TOKEN  : wajib dari .env / environment (tidak ada fallback)
//   - ADMIN_IDS  : wajib dari .env / environment (tidak ada fallback)
//
// Ini disengaja. Token bot Telegram setara akses shell ke VPS, jadi tidak
// boleh ikut ke repository.

var (
	BotToken string
	ApiUrl   string
	AdminIDs map[int64]bool
)

// LoadConfig memuat konfigurasi dari .env atau Environment Variables
func loadConfig() {
	AdminIDs = make(map[int64]bool)

	// Baca file .env jika ada di direktori kerja
	loadDotEnv(".env")

	// Ambil Token Bot
	BotToken = strings.TrimSpace(os.Getenv("BOT_TOKEN"))
	if BotToken == "" {
		BotToken = strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	}
	if BotToken == "" {
		fmt.Println("❌ BOT_TOKEN tidak diset. Isi file .env atau export BOT_TOKEN=<token> lalu jalankan ulang.")
		os.Exit(1)
	}
	// Alamat API. Bisa diarahkan ke Local Bot API Server untuk melewati
	// batas unduh 20MB (batas naik jadi 2GB). Lihat /help → 📦 Upload Besar.
	if base := strings.TrimSpace(os.Getenv("API_URL")); base != "" {
		// Normalisasi: buang garis miring di ujung dulu, baru periksa suffix.
		// Urutan ini penting — kalau "/" ditambah lebih dulu, "/bot" tidak
		// lagi terdeteksi dan hasilnya jadi "/bot/bot".
		base = strings.TrimRight(base, "/")

		// Pengguna boleh memberi salah satu dari bentuk berikut:
		//   http://127.0.0.1:8081
		//   http://127.0.0.1:8081/
		//   http://127.0.0.1:8081/bot
		if strings.HasSuffix(base, "/bot") {
			ApiUrl = base + BotToken
		} else {
			ApiUrl = base + "/bot" + BotToken
		}
		// Tampilkan tanpa token — jangan pernah cetak kredensial ke log.
		tampil := strings.TrimSuffix(base, "/bot") + "/bot<TOKEN>"
		fmt.Printf("🌐 Memakai Bot API kustom: %s\n", tampil)
	} else {
		ApiUrl = "https://api.telegram.org/bot" + BotToken
	}

	// Klien Telegram di internal/tg memakai nilai yang sama, supaya tidak
	// ada dua sumber kebenaran untuk endpoint dan token.
	tg.BotToken = BotToken
	tg.ApiUrl = ApiUrl

	// Repositori sumber rilis untuk fitur /update jalur unduh.
	// Bisa diganti bila memakai fork.
	if repo := strings.TrimSpace(os.Getenv("UPDATE_REPO")); repo != "" {
		repoTuanRumah = strings.TrimPrefix(repo, "https://github.com/")
		repoTuanRumah = strings.TrimSuffix(repoTuanRumah, ".git")
		repoTuanRumah = strings.Trim(repoTuanRumah, "/")
	}

	// Ambil Admin IDs
	adminStr := strings.TrimSpace(os.Getenv("ADMIN_IDS"))
	if adminStr == "" {
		adminStr = strings.TrimSpace(os.Getenv("ADMIN_ID"))
	}

	if adminStr != "" {
		for _, p := range strings.Split(adminStr, ",") {
			p = strings.TrimSpace(p)
			if id, err := strconv.ParseInt(p, 10, 64); err == nil && id > 0 {
				AdminIDs[id] = true
			} else if p != "" {
				fmt.Printf("⚠️  ADMIN_IDS tidak valid, dilewati: %q\n", p)
			}
		}
	}

	// Tanpa admin, bot tidak bisa dipakai siapa pun. Lebih baik gagal jelas
	// di awal daripada berjalan tapi menolak semua orang tanpa penjelasan.
	if len(AdminIDs) == 0 {
		fmt.Println("❌ ADMIN_IDS tidak diset atau tidak valid.")
		fmt.Println("   Isi file .env dengan Telegram User ID kamu, contoh:")
		fmt.Println("   ADMIN_IDS=123456789")
		fmt.Println("   Tip: kirim /id ke @userinfobot untuk melihat User ID kamu.")
		os.Exit(1)
	}
}

// loadDotEnv parser sederhana untuk membaca format KEY=VALUE dari .env
func loadDotEnv(filepath string) {
	file, err := os.Open(filepath)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
	}
}

func isAdmin(id int64) bool {
	return AdminIDs[id]
}

// ==============================================================================
// 🌐 HELPER & SYSTEM MONITORING
// ==============================================================================
func getPublicIP() string {
	urls := []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://icanhazip.com",
	}

	client := &http.Client{Timeout: 5 * time.Second}
	for _, u := range urls {
		resp, err := client.Get(u)
		if err == nil && resp.StatusCode == 200 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			ip := strings.TrimSpace(string(body))
			if len(ip) > 0 && len(ip) <= 45 {
				return ip
			}
		}
	}
	return "Unknown IP"
}

func getLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

func getTailscaleSocket() string {
	candidates := []string{
		"/tmp/tailscale.sock",
		"/var/run/tailscale/tailscaled.sock",
	}
	usr, _ := user.Current()
	if usr != nil && usr.HomeDir != "" {
		candidates = append(candidates, filepath.Join(usr.HomeDir, ".tailscale", "tailscaled.sock"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "/var/run/tailscale/tailscaled.sock"
}

func getTailscaleCmd(args string) string {
	sock := getTailscaleSocket()
	if sock != "" {
		return fmt.Sprintf("tailscale --socket=%s %s", sock, args)
	}
	return fmt.Sprintf("tailscale %s", args)
}

func isTailscaleInstalled() bool {
	_, err := exec.LookPath("tailscale")
	return err == nil
}

func getTailscaleIP() string {
	if !isTailscaleInstalled() {
		return ""
	}
	_, out, _ := runBashCommand(getTailscaleCmd("ip -4"), 3)
	return strings.TrimSpace(out)
}

func ensureTailscaledRunning() {
	code, _, _ := runBashCommand("pgrep -x tailscaled", 3)
	if code == 0 {
		return
	}

	_, binPath, _ := runBashCommand("which tailscaled || echo /usr/sbin/tailscaled", 3)
	binPath = strings.TrimSpace(binPath)
	if binPath == "" {
		binPath = "/usr/sbin/tailscaled"
	}

	hasTun := false
	if _, err := os.Stat("/dev/net/tun"); err == nil {
		hasTun = true
	}

	usr, _ := user.Current()
	homeDir := "/root"
	if usr != nil && usr.HomeDir != "" {
		homeDir = usr.HomeDir
	}

	if hasTun {
		runBashCommand("systemctl enable --now tailscaled 2>/dev/null", 3)
		time.Sleep(1 * time.Second)
		if c, _, _ := runBashCommand("pgrep -x tailscaled", 2); c == 0 {
			return
		}

		startCmd := fmt.Sprintf("mkdir -p /var/run/tailscale /var/lib/tailscale /var/log && nohup %s --state=/var/lib/tailscale/tailscaled.state --socket=/var/run/tailscale/tailscaled.sock > /var/log/tailscaled.log 2>&1 &", binPath)
		runBashCommand(startCmd, 5)
		time.Sleep(2 * time.Second)
		if c, _, _ := runBashCommand("pgrep -x tailscaled", 2); c == 0 {
			return
		}
	}

	userspaceCmd := fmt.Sprintf("mkdir -p %s/.tailscale /tmp && nohup %s --tun=userspace-networking --socks5-server=localhost:1055 --outbound-http-proxy-listen=localhost:1055 --state=%s/.tailscale/tailscaled.state --socket=/tmp/tailscale.sock > %s/.tailscale/tailscaled.log 2>&1 &", homeDir, binPath, homeDir, homeDir)
	runBashCommand(userspaceCmd, 5)
	time.Sleep(2 * time.Second)
}

func getUptime() string {
	data, err := os.ReadFile("/proc/uptime")
	if err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			if secs, err := strconv.ParseFloat(fields[0], 64); err == nil {
				d := time.Duration(secs) * time.Second
				days := int(d.Hours()) / 24
				hours := int(d.Hours()) % 24
				mins := int(d.Minutes()) % 60
				if days > 0 {
					return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
				}
				return fmt.Sprintf("%dh %dm", hours, mins)
			}
		}
	}

	_, out, _ := runBashCommand("uptime -p", 5)
	if out != "" {
		return out
	}
	return "Unknown"
}

func getLoadAverage() string {
	data, err := os.ReadFile("/proc/loadavg")
	if err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 3 {
			return fmt.Sprintf("%s, %s, %s", fields[0], fields[1], fields[2])
		}
	}
	return "N/A"
}

func getMemoryStats() string {
	_, out, _ := runBashCommand("free -m | awk 'NR==2{printf \"Used: %sMB / Total: %sMB (%.1f%%)\", $3, $2, $3*100/$2}'", 5)
	if out != "" {
		return out
	}
	return "N/A"
}

func getDiskStats() string {
	_, out, _ := runBashCommand("df -h / | awk 'NR==2{printf \"Used: %s / Total: %s (%s)\", $3, $2, $5}'", 5)
	if out != "" {
		return out
	}
	return "N/A"
}

func getCPUModel() string {
	_, out, _ := runBashCommand("grep -m1 'model name' /proc/cpuinfo | awk -F: '{print $2}' | xargs", 5)
	if out != "" {
		return out
	}
	return "Standard CPU"
}

func getCPUCores() string {
	_, out, _ := runBashCommand("nproc", 5)
	if out != "" {
		return out
	}
	return "1"
}

func getOSInfo() string {
	_, out, _ := runBashCommand("cat /etc/os-release | grep PRETTY_NAME | cut -d= -f2 | tr -d '\"'", 5)
	if out != "" {
		return out
	}
	_, out2, _ := runBashCommand("uname -s -r", 5)
	if out2 != "" {
		return out2
	}
	return "Linux"
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// runBashCommand mengeksekusi shell command dengan timeout dan isolasi process group
func runBashCommand(cmdStr string, timeoutSec int) (int, string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Start()
	if err != nil {
		return -1, "", fmt.Sprintf("Gagal menjalankan proses: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return -1, "", "⛔ Perintah dibatalkan: Timeout (melebihi batas waktu)."
	case err = <-done:
		exitCode := 0
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			} else {
				exitCode = -1
			}
		}
		return exitCode, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String())
	}
}

// ==============================================================================
// 🪟 PANEL TUNGGAL — MENCEGAH CHAT PENUH
// ==============================================================================
//
// Masalah: setiap perintah /term sebelumnya mengirim pesan BARU, sehingga chat
// penuh setelah beberapa perintah. Solusinya sama seperti file manager: satu
// pesan "panel" per admin yang isinya ditulis ulang (editMessageText).
//
// Aturan:
//   - Output panjang (hasil perintah, panel terminal) → SELALU ke panel.
//   - Pesan singkat (konfirmasi, error) → kirim sekali, dan hapus pesan
//     panel lama supaya tidak menumpuk.
//   - Notifikasi penting (startup, reboot) → tetap pesan baru.

type panelState struct {
	MessageID int64
	ChatID    int64
}

var (
	panelMu     sync.Mutex
	panelByUser = make(map[int64]*panelState)
)

// setPanelMessage mencatat pesan mana yang dipakai sebagai panel untuk admin.
func setPanelMessage(userID, chatID, msgID int64) {
	panelMu.Lock()
	panelByUser[userID] = &panelState{MessageID: msgID, ChatID: chatID}
	panelMu.Unlock()
}

// getPanelMessage mengambil panel aktif milik admin.
func getPanelMessage(userID int64) (int64, int64, bool) {
	panelMu.Lock()
	defer panelMu.Unlock()
	p, ok := panelByUser[userID]
	if !ok {
		return 0, 0, false
	}
	return p.MessageID, p.ChatID, true
}

// sendPanel mengirim panel BARU dan mengingatnya. Dipakai saat belum ada panel.
func sendPanel(userID, chatID int64, text string, kb *InlineKeyboardMarkup) {
	msgID := sendMessageReturningID(chatID, text, kb)
	if msgID > 0 {
		setPanelMessage(userID, chatID, msgID)
	}
}

// updatePanel menulis ulang panel yang ada. Bila belum ada panel, buat baru.
// Ini yang membuat chat tidak penuh: satu pesan, ditulis ulang terus.
func updatePanel(userID, chatID int64, text string, kb *InlineKeyboardMarkup) {
	msgID, _, ok := getPanelMessage(userID)
	if !ok {
		sendPanel(userID, chatID, text, kb)
		return
	}
	if !editTelegramMessage(chatID, msgID, text, kb) {
		// Pesan sudah dihapus / terlalu lama → kirim panel baru.
		clearPanel(userID)
		sendPanel(userID, chatID, text, kb)
	}
}

// clearPanel melupakan panel (mis. setelah dihapus atau sesi ditutup).
func clearPanel(userID int64) {
	panelMu.Lock()
	delete(panelByUser, userID)
	panelMu.Unlock()
}

// deletePanel menghapus pesan panel dan melupakannya. Dipakai sebelum
// mengirim pesan singkat supaya chat tidak menumpuk pesan lama.
func deletePanel(userID int64) {
	msgID, chatID, ok := getPanelMessage(userID)
	if !ok {
		return
	}
	deleteTelegramMessage(chatID, msgID)
	clearPanel(userID)
}

// sendOnce mengirim pesan singkat: hapus panel lama, kirim pesan ini sekali.
// Hasilnya chat tetap bersih — hanya pesan terbaru yang terlihat.
func sendOnce(userID, chatID int64, text string) {
	deletePanel(userID)
	sendTelegram(chatID, text)
}

// ==============================================================================
// 🖥️ TERMINAL UI RENDERER (Level 3 — PTY Interaktif)
// ==============================================================================

// renderTerminal menggambar panel terminal dengan tombol kontrol.
func renderTerminal(userID int64) (string, *InlineKeyboardMarkup) {
	var b strings.Builder
	b.WriteString("🖥️ <b>Interactive Terminal</b>\n")
	b.WriteString(fmt.Sprintf("<i>%s</i>\n", html.EscapeString(SessionInfo(userID))))
	b.WriteString("━━━━━━━━━━━━━━━━━━━━\n")

	out, err := ReadScreen(userID)
	if err != nil {
		b.WriteString(fmt.Sprintf("⚠️ <i>%s</i>\n", html.EscapeString(err.Error())))
		b.WriteString("\n💡 Kirim <code>/term</code> untuk membuat sesi baru.")
	} else if out == "" {
		b.WriteString("<i>(belum ada output)</i>")
	} else {
		// Telegram membatasi 4096 karakter. Untuk output panjang seperti
		// `apt upgrade`, bagian AWAL (daftar paket) dan AKHIR (hasil/error)
		// sama pentingnya — jadi keduanya ditampilkan, bagian tengah dipotong.
		const maxShown = 3400
		shown := out
		truncated := 0
		if len(out) > maxShown {
			headLen := maxShown * 2 / 5 // 40% untuk bagian awal
			tailLen := maxShown - headLen
			truncated = len(out) - headLen - tailLen
			shown = out[:headLen] + "\n\n... [dipotong " + strconv.Itoa(truncated) + " karakter] ...\n\n" + out[len(out)-tailLen:]
		}
		b.WriteString(fmt.Sprintf("<pre>%s</pre>", html.EscapeString(shown)))
		if truncated > 0 {
			b.WriteString(fmt.Sprintf("\n<i>⚠️ Output panjang (%d karakter). Kirim <code>/term log</code> untuk log penuh.</i>", len(out)))
		}
	}

	kb := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "🔄 Refresh", CallbackData: "tm:r"},
				{Text: "🛑 Ctrl+C", CallbackData: "tm:c"},
			},
			{
				{Text: "📋 Info Sesi", CallbackData: "tm:i"},
				{Text: "💀 Tutup Sesi", CallbackData: "tm:k"},
			},
			{
				{Text: "🏠 Menu Utama", CallbackData: "tm:h"},
			},
		},
	}
	return b.String(), kb
}

// ==============================================================================
// 🗂️ FILE MANAGER ENGINE (INLINE GUI EXPLORER)
// ==============================================================================

// Path ID Mapper untuk mengatasi limit 64-byte callback_data di Telegram API
var (
	pathMapLock sync.Mutex
	pathToID    = make(map[string]string)
	idToPath    = make(map[string]string)
	pathSeq     int64
)

func getPathID(p string) string {
	pathMapLock.Lock()
	defer pathMapLock.Unlock()
	cleaned := filepath.Clean(p)
	if id, ok := pathToID[cleaned]; ok {
		return id
	}
	pathSeq++
	id := strconv.FormatInt(pathSeq, 36)
	pathToID[cleaned] = id
	idToPath[id] = cleaned
	return id
}

func getPathByID(id string) string {
	pathMapLock.Lock()
	defer pathMapLock.Unlock()
	if p, ok := idToPath[id]; ok {
		return p
	}
	return "/root"
}

type FileItem struct {
	Name    string
	Path    string
	IsDir   bool
	Size    int64
	ModTime time.Time
	Mode    os.FileMode
	Kosong  bool // khusus folder: tidak punya isi
}

func listDirectory(targetDir string) ([]FileItem, error) {
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return nil, err
	}

	var dirs []FileItem
	var files []FileItem

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		item := FileItem{
			Name:    entry.Name(),
			Path:    filepath.Join(targetDir, entry.Name()),
			IsDir:   entry.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Mode:    info.Mode(),
		}
		if entry.IsDir() {
			dirs = append(dirs, item)
		} else {
			files = append(files, item)
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name) })

	return append(dirs, files...), nil
}

const itemsPerPage = 8

func getHomeDir() string {
	usr, err := user.Current()
	if err == nil && usr.HomeDir != "" {
		return usr.HomeDir
	}
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return "/root"
}

func renderFileManager(currentPath string, page int) (string, *InlineKeyboardMarkup) {
	absPath, err := filepath.Abs(currentPath)
	if err != nil {
		absPath = currentPath
	}
	absPath = filepath.Clean(absPath)
	currID := getPathID(absPath)
	homeDir := getHomeDir()

	items, err := listDirectory(absPath)
	if err != nil {
		return renderDirError(absPath, err, homeDir)
	}

	// Pisahkan folder dan file supaya daftar mudah dibaca.
	// Sebelumnya keduanya tercampur dalam satu urutan abjad.
	var folders, files []FileItem
	for _, it := range items {
		if it.IsDir {
			folders = append(folders, it)
		} else {
			files = append(files, it)
		}
	}

	// Folder ditampilkan lebih dulu, lalu file — masing-masing tetap 2 kolom.
	urut := make([]FileItem, 0, len(items))
	urut = append(urut, folders...)
	urut = append(urut, files...)

	totalItems := len(urut)
	totalPages := (totalItems + itemsPerPage - 1) / itemsPerPage
	if totalPages == 0 {
		totalPages = 1
	}
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	start := page * itemsPerPage
	end := start + itemsPerPage
	if end > totalItems {
		end = totalItems
	}
	var pageItems []FileItem
	if totalItems > 0 && start < totalItems {
		pageItems = urut[start:end]
	}

	// ---- Header ----
	var b strings.Builder
	b.WriteString("📂 <b>File Manager</b>\n")

	// Path panjang dipotong dari depan supaya bagian akhir (nama folder
	// terpenting) tetap terlihat.
	lokasi := absPath
	if len([]rune(lokasi)) > 60 {
		r := []rune(lokasi)
		lokasi = "…" + string(r[len(r)-58:])
	}
	b.WriteString("📍 <code>" + html.EscapeString(lokasi) + "</code>\n")

	// Ringkasan yang berguna: jumlah folder & file, bukan "8 item(s)".
	ringkas := fmt.Sprintf("🗂 <b>%d</b> folder · 📄 <b>%d</b> file", len(folders), len(files))
	if totalItems == 0 {
		ringkas = "📭 <i>Folder kosong</i>"
	}
	b.WriteString(ringkas)
	if totalPages > 1 {
		b.WriteString(fmt.Sprintf(" · hal <b>%d/%d</b>", page+1, totalPages))
	}
	b.WriteString("\n")

	// Indikator hak tulis — penting untuk upload.
	if totalItems > 0 && !isDirWritable(absPath) {
		b.WriteString("\n⚠️ <i>Folder tidak bisa ditulis (upload akan gagal)</i>\n")
	}

	// Tandai folder kosong di halaman yang sedang tampil, supaya penanda "∅"
	// hanya muncul saat informasinya berguna. Dihitung dari pageItems — bukan
	// dari seluruh daftar — karena keputusan "apakah semua kosong" hanya
	// bermakna untuk item yang benar-benar terlihat.
	tandaiFolderKosong(urut, pageItems, start)

	text := b.String()
	text += "\n━━━━━━━━━━━━━━━━━━━━"

	var keyboard [][]InlineKeyboardButton

	// ---- Daftar isi ----
	// Ukuran label: Telegram memotong tombol yang terlalu panjang. Folder tanpa
	// ukuran bisa memakai nama lebih panjang; file perlu ruang untuk ukuran.
	for i := 0; i < len(pageItems); i += 2 {
		var row []InlineKeyboardButton
		row = append(row, fmItemButton(pageItems[i]))
		if i+1 < len(pageItems) {
			row = append(row, fmItemButton(pageItems[i+1]))
		}
		keyboard = append(keyboard, row)
	}

	// ---- Navigasi halaman ----
	if totalPages > 1 {
		var navRow []InlineKeyboardButton
		if page > 0 {
			navRow = append(navRow, InlineKeyboardButton{
				Text:         "⬅️",
				CallbackData: fmt.Sprintf("fm:o:%s:%d", currID, page-1),
			})
		}
		navRow = append(navRow, InlineKeyboardButton{
			Text:         fmt.Sprintf("· %d/%d ·", page+1, totalPages),
			CallbackData: fmt.Sprintf("fm:rf:%s:%d", currID, page),
		})
		if page < totalPages-1 {
			navRow = append(navRow, InlineKeyboardButton{
				Text:         "➡️",
				CallbackData: fmt.Sprintf("fm:o:%s:%d", currID, page+1),
			})
		}
		keyboard = append(keyboard, navRow)
	}

	// ---- Baris aksi ----
	// Ditata 3-2-2 supaya label tidak terpotong dan layar tidak terlalu tinggi.
	parentID := getPathID(filepath.Dir(absPath))
	keyboard = append(keyboard,
		[]InlineKeyboardButton{
			{Text: "⬆️ Atas", CallbackData: "fm:o:" + parentID + ":0"},
			{Text: "🏠 Home", CallbackData: "fm:o:" + getPathID(homeDir) + ":0"},
			{Text: "🌐 Root", CallbackData: "fm:o:" + getPathID("/") + ":0"},
		},
		[]InlineKeyboardButton{
			{Text: "📤 Upload", CallbackData: "fm:up:" + currID},
			{Text: "📁 Folder Baru", CallbackData: "fm:mk:" + currID},
		},
		[]InlineKeyboardButton{
			{Text: "📦 Zip", CallbackData: "fm:zip:" + currID},
			{Text: "🔄 Refresh", CallbackData: fmt.Sprintf("fm:rf:%s:%d", currID, page)},
			{Text: "❌ Tutup", CallbackData: "fm:cls"},
		},
	)

	return text, &InlineKeyboardMarkup{InlineKeyboard: keyboard}
}

// fmItemButton menyusun tombol untuk satu item di file manager.
//
// Panjang label disesuaikan: folder tidak menampilkan ukuran sehingga bisa
// memakai nama lebih panjang, sedangkan file perlu ruang untuk ukurannya.
func fmItemButton(it FileItem) InlineKeyboardButton {
	if it.IsDir {
		// Folder: nama + garis miring penutup, tanpa ukuran.
		// Folder kosong ditandai "∅" supaya tidak perlu dibuka hanya untuk cek.
		// Penanda dilewatkan sebagai argumen karena renderFileManager sudah
		// menghitungnya sekali untuk seluruh halaman.
		nama := truncateMid(it.Name, 22)
		if it.Kosong {
			nama = "∅ " + nama
		}
		return InlineKeyboardButton{
			Text:         "📁 " + nama + "/",
			CallbackData: "fm:o:" + getPathID(it.Path) + ":0",
		}
	}

	// File: nama dipotong di tengah supaya ekstensi tetap terlihat.
	// Contoh: "konfigurasi_produksi_server.yaml" → "konfigurasi…er.yaml"
	ukuran := formatBytes(it.Size)
	nama := truncateMid(it.Name, 24-len(ukuran))

	ikon := "📄"
	switch {
	case strings.HasSuffix(it.Name, ".sh"), strings.HasSuffix(it.Name, ".bash"):
		ikon = "⚙️"
	case strings.HasSuffix(it.Name, ".zip"), strings.HasSuffix(it.Name, ".gz"),
		strings.HasSuffix(it.Name, ".tar"), strings.HasSuffix(it.Name, ".xz"),
		strings.HasSuffix(it.Name, ".zst"), strings.HasSuffix(it.Name, ".7z"):
		ikon = "📦"
	case strings.HasSuffix(it.Name, ".json"), strings.HasSuffix(it.Name, ".yaml"),
		strings.HasSuffix(it.Name, ".yml"), strings.HasSuffix(it.Name, ".toml"),
		strings.HasSuffix(it.Name, ".ini"), strings.HasSuffix(it.Name, ".conf"):
		ikon = "🔧"
	case strings.HasSuffix(it.Name, ".log"):
		ikon = "📜"
	case strings.HasSuffix(it.Name, ".png"), strings.HasSuffix(it.Name, ".jpg"),
		strings.HasSuffix(it.Name, ".jpeg"), strings.HasSuffix(it.Name, ".gif"),
		strings.HasSuffix(it.Name, ".webp"), strings.HasSuffix(it.Name, ".svg"):
		ikon = "🖼"
	}

	return InlineKeyboardButton{
		Text:         fmt.Sprintf("%s %s (%s)", ikon, nama, ukuran),
		CallbackData: "fm:f:" + getPathID(it.Path),
	}
}

// renderDirError menyusun tampilan saat folder tidak bisa dibaca.
func renderDirError(absPath string, err error, homeDir string) (string, *InlineKeyboardMarkup) {
	text := "📂 <b>File Manager</b>\n" +
		"📍 <code>" + html.EscapeString(absPath) + "</code>\n" +
		"━━━━━━━━━━━━━━━━━━━━\n\n" +
		"❌ <b>Tidak bisa membaca folder</b>\n" +
		"<pre>" + html.EscapeString(err.Error()) + "</pre>\n\n" +
		"<i>Kemungkinan penyebab:</i>\n" +
		"• Bot tidak punya izin membaca\n" +
		"• Folder sudah dihapus atau dipindah\n" +
		"• Bukan sebuah direktori"

	parentID := getPathID(filepath.Dir(absPath))
	kb := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "⬆️ Atas", CallbackData: "fm:o:" + parentID + ":0"},
				{Text: "🏠 Home", CallbackData: "fm:o:" + getPathID(homeDir) + ":0"},
				{Text: "🌐 Root", CallbackData: "fm:o:" + getPathID("/") + ":0"},
			},
			{
				{Text: "🔄 Coba Lagi", CallbackData: "fm:rf:" + getPathID(absPath) + ":0"},
				{Text: "❌ Tutup", CallbackData: "fm:cls"},
			},
		},
	}
	return text, kb
}

// tandaiFolderKosong mengisi field Kosong pada item folder.
//
// Penanda "∅" hanya dipakai bila di halaman ini MEMANG ada item berisi.
// Kalau semua folder di halaman itu kosong, penanda di setiap tombol justru
// jadi bising — jadi dilewati.
//
// irisan = bagian dari items yang sedang tampil; mulai = indeks awalnya.
func tandaiFolderKosong(items []FileItem, irisan []FileItem, mulai int) {
	if len(irisan) == 0 {
		return
	}

	// Periksa apakah ada item NON-folder, atau folder yang berisi.
	adaIsi := false
	for _, it := range irisan {
		if !it.IsDir {
			adaIsi = true
			break
		}
		if !isDirEmpty(it.Path) {
			adaIsi = true
			break
		}
	}
	if !adaIsi {
		return // semua kosong di halaman ini: penanda tidak menambah informasi
	}

	for i := mulai; i < mulai+len(irisan) && i < len(items); i++ {
		if items[i].IsDir {
			items[i].Kosong = isDirEmpty(items[i].Path)
		}
	}
}

// isDirEmpty melaporkan apakah folder tidak punya isi.
// Dipakai untuk memberi penanda visual pada folder kosong.
func isDirEmpty(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	return err != nil // EOF = kosong
}

// truncateMid memotong teks di TENGAH, bukan di ujung.
//
// Untuk nama file, bagian akhir (ekstensi) jauh lebih penting daripada
// bagian tengah. Sebelumnya "konfigurasi_produksi_server.yaml" menjadi
// "konfiguras.." sehingga jenis filenya tidak terlihat. Dengan potong-tengah
// hasilnya "konfiguras…er.yaml" — ekstensi tetap terbaca.
func truncateMid(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen || maxLen < 8 {
		return s
	}

	// Tentukan panjang ekor: usahakan SELURUH ekstensi terlihat.
	// Contoh ".env.production.old" → ekor ".old" (4), bukan ".old"[0:4].
	ekor := 4
	if i := strings.LastIndex(s, "."); i > 0 && i < len(r)-1 {
		panjangEkstensi := len(r) - i // termasuk titik
		if panjangEkstensi <= maxLen/2 {
			ekor = panjangEkstensi
		}
	}

	kepala := maxLen - ekor - 1 // 1 untuk karakter "…"
	if kepala < 1 {
		kepala = 1
	}
	if kepala+ekor >= len(r) {
		return s
	}
	return string(r[:kepala]) + "…" + string(r[len(r)-ekor:])
}

func renderFileDetails(filePath string) (string, *InlineKeyboardMarkup) {
	absPath, _ := filepath.Abs(filePath)
	absPath = filepath.Clean(absPath)
	info, err := os.Stat(absPath)
	fileID := getPathID(absPath)
	parentID := getPathID(filepath.Dir(absPath))
	homeDir := getHomeDir()

	if err != nil {
		text := fmt.Sprintf("📄 <b>Informasi File</b>\n\n❌ <i>File tidak ditemukan:</i> <code>%s</code>", html.EscapeString(absPath))
		kb := &InlineKeyboardMarkup{
			InlineKeyboard: [][]InlineKeyboardButton{
				{
					{Text: "🔙 Kembali ke Folder", CallbackData: "fm:o:" + parentID + ":0"},
					{Text: "🏠 Home", CallbackData: "fm:o:" + getPathID(homeDir) + ":0"},
				},
			},
		}
		return text, kb
	}

	text := fmt.Sprintf(
		"📄 <b>Detail File:</b>\n\n"+
			"• <b>Nama:</b> <code>%s</code>\n"+
			"• <b>Path:</b> <code>%s</code>\n"+
			"• <b>Ukuran:</b> <code>%s</code> (%d bytes)\n"+
			"• <b>Izin (*Permissions*):</b> <code>%s</code>\n"+
			"• <b>Terakhir Diubah:</b> <code>%s</code>\n\n"+
			"<i>Pilih aksi yang ingin dilakukan:</i>",
		html.EscapeString(info.Name()),
		html.EscapeString(absPath),
		formatBytes(info.Size()),
		info.Size(),
		info.Mode().String(),
		info.ModTime().Format("2006-01-02 15:04:05"),
	)

	kb := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "⬇️ Download File", CallbackData: "fm:dl:" + fileID},
				{Text: "👁️ Preview Isi (Teks)", CallbackData: "fm:vw:" + fileID},
			},
			{
				{Text: "🗑️ Hapus File", CallbackData: "fm:del:" + fileID},
				{Text: "🔙 Kembali ke Folder", CallbackData: "fm:o:" + parentID + ":0"},
			},
			{
				{Text: "🏠 Home", CallbackData: "fm:o:" + getPathID(homeDir) + ":0"},
				{Text: "❌ Tutup", CallbackData: "fm:cls"},
			},
		},
	}
	return text, kb
}

func renderFilePreview(filePath string, maxLines int) (string, *InlineKeyboardMarkup) {
	absPath, _ := filepath.Abs(filePath)
	absPath = filepath.Clean(absPath)
	fileID := getPathID(absPath)

	file, err := os.Open(absPath)
	if err != nil {
		text := fmt.Sprintf("❌ <i>Gagal membuka file:</i> %v", err)
		kb := &InlineKeyboardMarkup{
			InlineKeyboard: [][]InlineKeyboardButton{
				{{Text: "🔙 Kembali ke Detail", CallbackData: "fm:f:" + fileID}},
			},
		}
		return text, kb
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var lines []string
	count := 0
	for scanner.Scan() && count < maxLines {
		lines = append(lines, scanner.Text())
		count++
	}

	content := strings.Join(lines, "\n")
	if len(content) > 3000 {
		content = content[:3000] + "\n... (Konten dipotong)"
	}
	if content == "" {
		content = "(File kosong atau tidak berisi teks)"
	}

	text := fmt.Sprintf(
		"👁️ <b>Preview:</b> <code>%s</code> (Maks %d baris)\n\n<pre>%s</pre>",
		html.EscapeString(filepath.Base(absPath)),
		maxLines,
		html.EscapeString(content),
	)

	homeDir := getHomeDir()
	kb := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "⬇️ Download File", CallbackData: "fm:dl:" + fileID},
				{Text: "🔙 Kembali", CallbackData: "fm:f:" + fileID},
			},
			{
				{Text: "🏠 Home", CallbackData: "fm:o:" + getPathID(homeDir) + ":0"},
				{Text: "❌ Tutup", CallbackData: "fm:cls"},
			},
		},
	}
	return text, kb
}

func renderDeleteConfirmation(targetPath string) (string, *InlineKeyboardMarkup) {
	absPath, _ := filepath.Abs(targetPath)
	absPath = filepath.Clean(absPath)
	id := getPathID(absPath)
	parentID := getPathID(filepath.Dir(absPath))

	text := fmt.Sprintf(
		"⚠️ <b>KONFIRMASI HAPUS PERMANEN</b>\n\n"+
			"Apakah Anda yakin ingin menghapus item ini dari VPS?\n"+
			"📍 <code>%s</code>\n\n"+
			"<i>⚠️ Tindakan ini tidak dapat dibatalkan!</i>",
		html.EscapeString(absPath),
	)

	kb := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "🗑️ YA, Hapus Sekarang!", CallbackData: "fm:delc:" + id},
				{Text: "❌ Batal", CallbackData: "fm:o:" + parentID + ":0"},
			},
		},
	}
	return text, kb
}

func zipDirectoryToBytes(sourceDir string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relPath)
		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}

		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = io.Copy(writer, file)
			if err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + ".."
}

// terminalHelpText mengembalikan panduan penggunaan terminal.
func terminalHelpText() string {
	return "🖥️ <b>Interactive Terminal (PTY)</b>\n" +
		"━━━━━━━━━━━━━━━━━━━━\n" +
		"Shell <b>persisten</b> dengan TTY asli. <code>cd</code>, <code>export</code>, " +
		"history, dan program yang butuh terminal (<code>sudo</code>, <code>top</code>, <code>apt</code>) berjalan normal.\n\n" +
		"<b>Perintah:</b>\n" +
		"• <code>/term</code> — buka / buat sesi terminal\n" +
		"• <code>/term &lt;perintah&gt;</code> — jalankan satu perintah\n" +
		"• <code>/term log</code> — log penuh (dikirim sebagai file)\n" +
		"• <code>/term kill</code> — matikan sesi\n" +
		"• <code>/term info</code> — status sesi\n\n" +
		"<b>Mode input langsung:</b> setelah sesi dibuka, kirim teks apa pun " +
		"(tanpa garis miring) ke chat dan teks itu masuk ke stdin shell.\n" +
		"Aktifkan dengan <code>/term mode on</code>, matikan dengan <code>/term mode off</code>.\n\n" +
		"<b>Pintasan Ctrl:</b>\n" +
		"• <code>/term ^c</code> — kirim Ctrl+C (SIGINT)\n" +
		"• <code>/term ^d</code> — kirim Ctrl+D (EOF / keluar)\n" +
		"• <code>/term ^z</code> — kirim Ctrl+Z (SIGTSTP)\n\n" +
		"<b>Paket (apt):</b>\n" +
		"• <code>/term apt check</code> — cek lock & hak root (jalankan dulu)\n" +
		"• <code>/term apt update</code> — perbarui daftar paket\n" +
		"• <code>/term apt upgrade</code> — upgrade (prompt bisa dijawab)\n" +
		"• <code>/term apt safe</code> — upgrade non-interaktif\n" +
		"• <code>/term apt full</code> — full-upgrade\n" +
		"• <code>/term clear</code> — bersihkan layar\n\n" +
		"⚠️ <i>Sesi otomatis ditutup setelah 30 menit menganggur.</i>"
}

// ==============================================================================
// 🖥️ TERMINAL COMMAND HANDLER (Level 3)
// ==============================================================================

// handleTerminalCommand menangani perintah /term dan turunannya.
func handleTerminalCommand(chatID int64, userID int64, rawText string) {
	fields := strings.Fields(rawText)

	// /term tanpa argumen -> buka atau tampilkan panel terminal
	if len(fields) < 2 {
		if _, err := getOrCreateSession(userID); err != nil {
			sendOnce(userID, chatID, fmt.Sprintf("❌ <b>Gagal membuka terminal:</b>\n<pre>%s</pre>\n\n"+
				"<i>Pastikan /dev/pts ter-mount dan bot berjalan sebagai user yang berhak.</i>",
				html.EscapeString(err.Error())))
			return
		}
		// Panel yang sama dipakai ulang — /term berulang tidak menumpuk pesan.
		text, kb := renderTerminal(userID)
		updatePanel(userID, chatID, text, kb)
		return
	}

	sub := strings.ToLower(fields[1])
	// Argumen perintah asli (case dipertahankan)
	argRaw := strings.TrimSpace(strings.TrimPrefix(rawText, fields[0]))
	argRaw = strings.TrimSpace(strings.TrimPrefix(argRaw, fields[1]))

	switch sub {
	case "help", "?":
		// Bantuan terminal TIDAK lagi jadi pesan baru — masuk ke panel.
		updatePanel(userID, chatID, terminalHelpText(), backToTerminalKeyboard())

	case "info", "status":
		if _, err := getOrCreateSession(userID); err != nil {
			updatePanel(userID, chatID, "❌ <i>Tidak ada sesi terminal:</i> "+
				html.EscapeString(err.Error()), backToTerminalKeyboard())
			return
		}
		updatePanel(userID, chatID,
			"📋 <b>Status Sesi:</b>\n<code>"+html.EscapeString(SessionInfo(userID))+"</code>\n\n"+
				"<i>Ketik /term untuk kembali ke panel terminal.</i>",
			backToTerminalKeyboard())

	case "kill", "close", "exit":
		if KillSession(userID) {
			updatePanel(userID, chatID,
				"💀 <b>Sesi terminal ditutup.</b>\n\nKetik <code>/term</code> untuk memulai sesi baru.", nil)
			clearPanel(userID)
		} else {
			updatePanel(userID, chatID,
				"⚠️ <i>Tidak ada sesi terminal yang aktif.</i>\n\nKetik <code>/term</code> untuk membuat sesi.",
				backToTerminalKeyboard())
		}

	case "log":
		out, err := ReadScreen(userID)
		if err != nil {
			updatePanel(userID, chatID, "❌ "+html.EscapeString(err.Error()), backToTerminalKeyboard())
			return
		}
		if out == "" {
			updatePanel(userID, chatID, "📄 <i>Buffer terminal kosong.</i>", backToTerminalKeyboard())
			return
		}
		// Log dikirim sebagai FILE — tidak mengotori chat dengan teks raksasa.
		fname := fmt.Sprintf("terminal_log_%d.txt", time.Now().Unix())
		_ = sendTelegramDocument(chatID, fname, []byte(out), "📄 <i>Log penuh sesi terminal</i>")

	case "apt":
		termHandleApt(chatID, userID, argRaw)

	case "clear", "cls":
		if err := ClearScreen(userID); err != nil {
			updatePanel(userID, chatID, "⚠️ "+html.EscapeString(err.Error()), backToTerminalKeyboard())
		} else {
			// Sungguhan membersihkan layar PTY, bukan cuma buffer bot.
			_ = SendToTerminal(userID, "clear\n")
			text, kb := renderTerminal(userID)
			updatePanel(userID, chatID, text, kb)
		}

	case "mode":
		mode := ""
		if len(fields) >= 3 {
			mode = strings.ToLower(fields[2])
		}
		switch mode {
		case "on":
			termDirectMu.Lock()
			termDirectMode[userID] = true
			termDirectMu.Unlock()
			updatePanel(userID, chatID, "✅ <b>Mode input langsung AKTIF.</b>\n\n"+
				"Kirim teks apa pun ke chat dan teks itu masuk ke stdin shell.\n"+
				"Matikan dengan <code>/term mode off</code>.\n\n"+
				"⚠️ <i>Perintah yang diawali / tetap diproses sebagai perintah bot.</i>",
				backToTerminalKeyboard())
		case "off":
			termDirectMu.Lock()
			delete(termDirectMode, userID)
			termDirectMu.Unlock()
			updatePanel(userID, chatID, "🔕 <b>Mode input langsung NONAKTIF.</b>\n\n"+
				"Gunakan <code>/term &lt;perintah&gt;</code> untuk mengirim perintah.",
				backToTerminalKeyboard())
		default:
			status := "nonaktif"
			termDirectMu.Lock()
			if termDirectMode[userID] {
				status = "aktif"
			}
			termDirectMu.Unlock()
			updatePanel(userID, chatID,
				fmt.Sprintf("ℹ️ Mode input langsung: <b>%s</b>\n\nGunakan <code>/term mode on|off</code>.", status),
				backToTerminalKeyboard())
		}

	case "^c", "ctrl-c", "ctrlc":
		termHandleSignal(chatID, userID, syscall.SIGINT, "Ctrl+C (SIGINT)")

	case "^d", "ctrl-d", "ctrld":
		termHandleSignal(chatID, userID, syscall.SIGHUP, "Ctrl+D / EOF")

	case "^z", "ctrl-z", "ctrlz":
		termHandleSignal(chatID, userID, syscall.SIGTSTP, "Ctrl+Z (SIGTSTP)")

	default:
		// Sisa argumen diperlakukan sebagai perintah shell.
		cmdToSend := argRaw
		if cmdToSend == "" {
			cmdToSend = sub
		}
		termExecAndShow(chatID, userID, cmdToSend)
	}
}

// backToTerminalKeyboard menyediakan tombol kembali ke panel terminal.
func backToTerminalKeyboard() *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "⬅️ Kembali ke Terminal", CallbackData: "tm:r"},
			},
		},
	}
}

// termHandleSignal mengirim sinyal ke proses foreground sesi lalu me-refresh panel.
func termHandleSignal(chatID int64, userID int64, sig syscall.Signal, label string) {
	if err := SendSignal(userID, sig, label); err != nil {
		updatePanel(userID, chatID, "⚠️ "+html.EscapeString(err.Error()), backToTerminalKeyboard())
		return
	}
	text, kb := renderTerminal(userID)
	updatePanel(userID, chatID, text, kb)
}

// termExecAndShow menyuntik satu perintah ke shell lalu menampilkan hasilnya.
func termExecAndShow(chatID int64, userID int64, cmd string) {
	if _, err := getOrCreateSession(userID); err != nil {
		updatePanel(userID, chatID,
			fmt.Sprintf("❌ <b>Gagal membuka terminal:</b>\n<pre>%s</pre>", html.EscapeString(err.Error())),
			backToTerminalKeyboard())
		return
	}

	// Kirim perintah + newline. Shell yang mengeksekusinya, bukan bot.
	if err := SendToTerminal(userID, cmd+"\n"); err != nil {
		updatePanel(userID, chatID, "❌ "+html.EscapeString(err.Error()), backToTerminalKeyboard())
		return
	}

	// Hasil masuk ke panel yang SAMA — chat tidak bertambah.
	text, kb := renderTerminal(userID)
	updatePanel(userID, chatID, text, kb)
}

// ==============================================================================
// ⌨️ MODE INPUT LANGSUNG
// ==============================================================================

var (
	termDirectMu   sync.Mutex
	termDirectMode = make(map[int64]bool)
)

// HasTerminalSession melaporkan apakah admin punya sesi terminal yang masih hidup.
func HasTerminalSession(userID int64) bool {
	termSessionsMu.Lock()
	defer termSessionsMu.Unlock()
	s, ok := termSessions[userID]
	if !ok {
		return false
	}
	s.Mu.Lock()
	alive := s.Alive
	s.Mu.Unlock()
	return alive
}

// IsTerminalDirectMode melaporkan apakah admin memakai mode input langsung.
func IsTerminalDirectMode(userID int64) bool {
	termDirectMu.Lock()
	defer termDirectMu.Unlock()
	return termDirectMode[userID]
}

// SendRawToTerminal mengirim teks mentah dari chat ke stdin shell.
func SendRawToTerminal(chatID int64, userID int64, text string) {
	if _, err := getOrCreateSession(userID); err != nil {
		updatePanel(userID, chatID, "❌ "+html.EscapeString(err.Error()), backToTerminalKeyboard())
		return
	}
	if err := SendToTerminal(userID, text+"\n"); err != nil {
		updatePanel(userID, chatID, "❌ "+html.EscapeString(err.Error()), backToTerminalKeyboard())
		return
	}
	// Tetap satu panel: setiap perintah menulis ulang pesan yang sama.
	text2, kb := renderTerminal(userID)
	updatePanel(userID, chatID, text2, kb)
}

// ==============================================================================
// 📊 PEMBANGUN PESAN (dipakai perintah DAN tombol bantuan)
// ==============================================================================
// Fungsi-fungsi ini dipakai bersama oleh handler perintah dan tombol inline,
// sehingga format pesan hanya didefinisikan di satu tempat.

// buildStatsMessage menyusun laporan resource server.
func buildStatsMessage() string {
	tsInfo := ""
	if ts := getTailscaleIP(); ts != "" {
		tsInfo = fmt.Sprintf("🦎 <b>Tailscale:</b> <code>%s</code>\n", html.EscapeString(ts))
	}

	return fmt.Sprintf(
		"📊 <b>System Resource Monitor:</b>\n\n"+
			"⏱ <b>Uptime:</b> <code>%s</code>\n"+
			"📈 <b>Load Avg:</b> <code>%s</code>\n"+
			"🧠 <b>Memory:</b> <code>%s</code>\n"+
			"💾 <b>Storage /:</b> <code>%s</code>\n"+
			"🖥 <b>CPU:</b> <code>%s (%s Cores)</code>\n"+
			"%s"+
			"🐧 <b>OS:</b> <code>%s</code>",
		html.EscapeString(getUptime()),
		html.EscapeString(getLoadAverage()),
		html.EscapeString(getMemoryStats()),
		html.EscapeString(getDiskStats()),
		html.EscapeString(getCPUModel()),
		html.EscapeString(getCPUCores()),
		tsInfo,
		html.EscapeString(getOSInfo()),
	)
}

// buildSysInfoMessage menyusun laporan spesifikasi sistem.
func buildSysInfoMessage() string {
	hostname, _ := os.Hostname()
	usr, _ := user.Current()
	username := "root"
	if usr != nil {
		username = usr.Username
	}

	return fmt.Sprintf(
		"🖥 <b>Spesifikasi Sistem Lengkap:</b>\n\n"+
			"🐧 <b>OS Distro:</b> <code>%s</code>\n"+
			"⚙️ <b>CPU Model:</b> <code>%s</code>\n"+
			"🔢 <b>Jumlah Cores:</b> <code>%s Core(s)</code>\n"+
			"🏠 <b>Hostname:</b> <code>%s</code>\n"+
			"👤 <b>Current User:</b> <code>%s</code>\n"+
			"⏱ <b>Uptime:</b> <code>%s</code>",
		html.EscapeString(getOSInfo()),
		html.EscapeString(getCPUModel()),
		html.EscapeString(getCPUCores()),
		html.EscapeString(hostname),
		html.EscapeString(username),
		html.EscapeString(getUptime()),
	)
}

// backToHelpKeyboard menyediakan tombol kembali ke bantuan utama.
func backToHelpKeyboard() *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "⬅️ Kembali ke Bantuan", CallbackData: "hp:b"},
			},
		},
	}
}

// ==============================================================================
// 🦎 TAILSCALE HANDLER
// ==============================================================================
// handleTailscaleCommand membangun respons berdasarkan sub-command /ts.
func handleTailscaleCommand(chatID int64, userID int64, rawText string) {
	fields := strings.Fields(rawText)
	subcmd := ""
	if len(fields) > 1 {
		subcmd = strings.ToLower(fields[1])
	}

	switch subcmd {
	case "install":
		// Proses panjang: beri tahu dulu, lalu panel menampilkan hasil akhir.
		if isTailscaleInstalled() {
			ensureTailscaledRunning()
			sendPanel(userID, chatID,
				"✅ <b>Tailscale sudah terinstall.</b>\n\n"+tsStatusRingkas(), tsMenuKeyboard())
			return
		}
		sendPanel(userID, chatID,
			"⏳ <b>Menginstall Tailscale…</b>\n\n"+
				"<i>Proses ini 15-45 detik. Pesan ini akan diperbarui.</i>",
			tsMenuKeyboard())
		sendPanel(userID, chatID, tsCallbackText(userID, chatID, "i"), tsMenuKeyboard())

	case "up", "login", "connect":
		if !isTailscaleInstalled() {
			sendPanel(userID, chatID, "❌ <b>Tailscale belum terinstall.</b>\n\n"+
				"Tekan tombol ⬇️ Install di bawah.", tsMenuKeyboard())
			return
		}

		// CATATAN KEAMANAN: auth key yang diketik sebagai argumen tersimpan
		// di riwayat chat Telegram. Bila pengguna memberi key, kita pakai
		// (untuk kompatibilitas) tapi sarankan jalur yang lebih aman.
		var authKey string
		if len(fields) >= 3 {
			authKey = strings.TrimSpace(fields[2])
		}

		ensureTailscaledRunning()

		var cmdStr string
		if authKey != "" {
			cmdStr = getTailscaleCmd(fmt.Sprintf(
				"up --auth-key=%s --accept-routes --ssh --reset", authKey))
		} else {
			cmdStr = getTailscaleCmd("up --accept-routes --ssh --reset")
		}

		code, out, errStr := runBashCommand(cmdStr, 35)
		combined := out + "\n" + errStr

		if loginURL := tsExtractLoginURL(combined); loginURL != "" {
			// Link login aman ditampilkan — hanya berlaku sesaat dan butuh
			// persetujuan akun Tailscale kamu.
			sendPanel(userID, chatID, fmt.Sprintf(
				"🔗 <b>Autentikasi Tailscale</b>\n━━━━━━━━━━━━━━━━━━━━\n\n"+
					"Buka tautan ini di browser untuk mengotorisasi VPS ini:\n\n"+
					"👉 <a href=\"%s\"><b>Login ke Tailscale</b></a>\n\n"+
					"<i>Setelah disetujui, tekan tombol 🔄 Refresh.</i>",
				html.EscapeString(loginURL)), tsMenuKeyboard())
			return
		}

		if code == 0 {
			sendPanel(userID, chatID,
				"✅ <b>Tailscale berhasil terhubung!</b>\n\n"+tsStatusRingkas(), tsMenuKeyboard())
			return
		}

		sendPanel(userID, chatID,
			"⚠️ <b>Respons Tailscale:</b>\n<pre>"+
				html.EscapeString(truncateStr(combined, 1000))+"</pre>",
			tsMenuKeyboard())

	case "down", "disconnect", "off":
		// Memutus Tailscale bisa menghilangkan akses remote ke VPS.
		// Karena itu butuh konfirmasi, bukan langsung eksekusi.
		if !isTailscaleInstalled() {
			sendPanel(userID, chatID, "❌ <b>Tailscale belum terinstall.</b>", tsMenuKeyboard())
			return
		}
		confirmed := len(fields) >= 3 &&
			(strings.ToLower(fields[2]) == "confirm" || strings.ToLower(fields[2]) == "yes")
		if !confirmed {
			sendPanel(userID, chatID,
				"🛑 <b>Putuskan Tailscale?</b>\n"+
					"━━━━━━━━━━━━━━━━━━━━\n\n"+
					"⚠️ Jika kamu mengakses VPS ini <b>hanya</b> lewat Tailscale, "+
					"memutuskan koneksi berarti kamu <b>kehilangan akses</b>.\n\n"+
					"Pastikan ada jalur SSH publik sebagai cadangan.\n\n"+
					"Ketik <code>/ts down confirm</code> untuk melanjutkan.",
				tsMenuKeyboard())
			return
		}
		sendPanel(userID, chatID, tsCallbackText(userID, chatID, "dy"), tsMenuKeyboard())

	case "status", "info":
		// "raw" mengirim output penuh sebagai file — berguna untuk banyak peer.
		if len(fields) >= 3 && strings.ToLower(fields[2]) == "raw" {
			detail, err := tsDetailStatus()
			if err != nil {
				sendTelegram(chatID, "❌ "+html.EscapeString(err.Error()))
				return
			}
			fname := fmt.Sprintf("tailscale_status_%d.txt", time.Now().Unix())
			_ = sendTelegramDocument(chatID, fname, []byte(detail), "📋 <i>Status Tailscale lengkap</i>")
			return
		}
		// Tampilan ringkas: peer dihitung, bukan dump mentah puluhan baris.
		sendPanel(userID, chatID, tsStatusRingkas(), tsMenuKeyboard())

	case "ip":
		sendPanel(userID, chatID, tsCallbackText(userID, chatID, "p"), tsMenuKeyboard())

	case "ssh":
		// /ts ssh [on|off] — tanpa argumen berarti aktifkan.
		on := true
		if len(fields) >= 3 {
			switch strings.ToLower(fields[2]) {
			case "off", "disable", "false", "0":
				on = false
			}
		}
		act := "hoff"
		if on {
			act = "hon"
		}
		sendPanel(userID, chatID, tsCallbackText(userID, chatID, act), tsMenuKeyboard())

	default:
		// Tanpa sub-perintah: tampilkan MENU TOMBOL, bukan panduan teks.
		text := tsStatusRingkas() + "\n\n" + tsMenuFooter()
		sendPanel(userID, chatID, text, tsMenuKeyboard())
	}
}

// ==============================================================================
// 🤖 TELEGRAM BOT API — bridge ke package internal/tg
// ==============================================================================
// Klien Telegram (tipe payload, pengiriman, unduhan, pemotongan) kini hidup
// di internal/tg supaya fitur bisa mengimpornya tanpa menarik loop polling.
// package main memakai alias agar file fitur tidak harus diubah satu per satu;
// alias ini dihapus tahap demi tahap saat tiap fitur dipindah ke subpackage-nya.
type (
	InlineKeyboardButton  = tg.InlineKeyboardButton
	InlineKeyboardMarkup  = tg.InlineKeyboardMarkup
	SendMessagePayload    = tg.SendMessagePayload
	EditMessagePayload    = tg.EditMessagePayload
	AnswerCallbackPayload = tg.AnswerCallbackPayload
	UpdateResponse        = tg.UpdateResponse
	Update                = tg.Update
	Document              = tg.Document
	Message               = tg.Message
	CallbackQuery         = tg.CallbackQuery
	User                  = tg.User
	Chat                  = tg.Chat
)

var (
	sendTelegram             = tg.SendTelegram
	sendTelegramWithKeyboard = tg.SendTelegramWithKeyboard
	sendSingleMessage        = tg.SendSingleMessage
	editTelegramMessage      = tg.EditTelegramMessage
	deleteTelegramMessage    = tg.DeleteTelegramMessage
	sendMessageReturningID   = tg.SendMessageReturningID
	extractMessageID         = tg.ExtractMessageID
	truncateHTMLSafe         = tg.TruncateHTMLSafe
	answerCallbackQuery      = tg.AnswerCallbackQuery
	needsUserAttention       = tg.NeedsUserAttention
	sendTelegramDocument     = tg.SendTelegramDocument
	downloadTelegramFile     = tg.DownloadTelegramFile
	splitMessage             = tg.SplitMessage
)

// ==============================================================================
// 🎯 MAIN ENGINE
// ==============================================================================
func main() {
	// Pemeriksaan mandiri: memastikan binary ini benar-benar bisa jalan.
	//
	// Dipakai fitur update sebelum memasang binary baru — binary yang tidak
	// bisa dijalankan akan tertangkap di sini, bukan setelah dipasang dan
	// bot mati. Sengaja dijalankan SEBELUM loadConfig() supaya tidak
	// memerlukan .env.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--cek", "-cek":
			fmt.Println("ok")
			return
		case "--versi", "-versi", "--version", "-version":
			fmt.Printf("bot-vps-control (%s/%s, %s)\n",
				runtime.GOOS, runtime.GOARCH, runtime.Version())
			return
		}
	}

	loadConfig()

	fmt.Println("==============================================================")
	fmt.Println("🤖 Bot VPS Engine v3.0 (Interactive File Manager Edition)...")
	// Jangan pernah cetak token, bahkan sebagian: prefix token cukup untuk
	// memverifikasi dugaan token orang lain. Yang berguna untuk diagnosis
	// hanyalah panjangnya — itu menunjukkan token terpotong atau tidak.
	fmt.Printf("🔑 Bot Token: terisi (%d karakter)\n", len(BotToken))
	fmt.Printf("👑 Authorized Admins: %d user(s)\n", len(AdminIDs))
	fmt.Println("==============================================================")

	// Reset Webhook
	_, _ = http.Get(ApiUrl + "/deleteWebhook")

	// Info Server
	pubIP := getPublicIP()
	locIP := getLocalIP()
	tsIP := getTailscaleIP()
	hostname, _ := os.Hostname()
	usr, _ := user.Current()
	username := "root"
	if usr != nil {
		username = usr.Username
	}

	tsLine := ""
	if tsIP != "" {
		tsLine = fmt.Sprintf("🦎 <b>Tailscale IP:</b> <code>%s</code>\n", html.EscapeString(tsIP))
	}

	// Kirim Notifikasi Startup ke Semua Admin Terdaftar
	startupMsg := fmt.Sprintf(
		"🚀 <b>Bot VPS Control Engine v3.0 Aktif!</b>\n\n"+
			"🌐 <b>Public IP:</b> <code>%s</code>\n"+
			"🏠 <b>Local IP:</b> <code>%s</code>\n"+
			"%s"+
			"🖥 <b>Hostname:</b> <code>%s</code>\n"+
			"👤 <b>Username:</b> <code>%s</code>\n"+
			"🕒 <b>Waktu Start:</b> <code>%s</code>\n\n"+
			"💻 <b>SSH Akses:</b>\n<code>ssh %s@%s</code>\n\n"+
			"📂 <i>Ketik <code>/fm</code> untuk membuka Interactive File Manager.</i>\n"+
			"💡 Ketik <code>/help</code> untuk melihat seluruh perintah.",
		html.EscapeString(pubIP),
		html.EscapeString(locIP),
		tsLine,
		html.EscapeString(hostname),
		html.EscapeString(username),
		time.Now().Format("2006-01-02 15:04:05"),
		html.EscapeString(username),
		html.EscapeString(pubIP),
	)

	// Verifikasi token SEBELUM mengklaim apa pun. Tanpa ini, bot akan bilang
	// "notifikasi terkirim" padahal Telegram sudah menolak tokennya.
	_, _, err := telegramGetMe()
	if err != nil {
		fmt.Printf("❌ Token bot ditolak Telegram: %v\n\n", err)
		fmt.Println("   Periksa BOT_TOKEN di file .env.")
		fmt.Println("   Bila token sudah di-revoke, buat baru di @BotFather.")
		os.Exit(1)
	}
	// Nama akun bot tidak perlu masuk log. Log sering diteruskan ke file,
	// journald, atau layanan pengumpul log pihak ketiga — identitas akun
	// tidak ada gunanya di sana dan hanya menambah permukaan bocor.
	fmt.Println("🤖 Terhubung ke Telegram.")

	// Kunci instance SEBELUM notifikasi apa pun dikirim.
	//
	// Dua proses dengan token sama akan berebut pesan: Telegram long polling
	// hanya memberi satu update ke satu pemanggil, jadi perintah bisa mendarat
	// di instance yang salah. Gejalanya menipu — /sysinfo menjawab spesifikasi
	// mesin lain, atau bot kadang diam.
	//
	// Urutan ini penting: kalau notifikasi dikirim lebih dulu, instance kedua
	// sudah terlanjur mengirim pesan startup sebelum ditolak. Pengguna melihat
	// dua notifikasi dari satu bot dan bingung mana yang benar.
	if err := kunciInstance(); err != nil {
		fmt.Printf("❌ %v\n\n", err)
		fmt.Println("   Bot lain dengan token yang sama sudah berjalan.")
		fmt.Println("   Hentikan dulu, lalu jalankan yang ini:")
		fmt.Println("       pkill -x core_engine")
		fmt.Println("       systemctl restart bot-vps")
		os.Exit(1)
	}

	for adminID := range AdminIDs {
		sendTelegram(adminID, startupMsg)
	}

	fmt.Println("✅ Notifikasi startup terkirim ke admin.")
	fmt.Println("🟢 Polling Telegram aktif. Mendengarkan perintah & callback...")

	// Reaper sesi terminal nganggur: mencegah shell hidup selamanya di RAM VPS.
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			reapIdleSessions()
		}
	}()

	var offset int64 = 0
	client := &http.Client{Timeout: 30 * time.Second}

	for {
		url := fmt.Sprintf("%s/getUpdates?offset=%d&timeout=15", ApiUrl, offset)
		resp, err := client.Get(url)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		var updateResp UpdateResponse
		if err := json.Unmarshal(body, &updateResp); err != nil || !updateResp.OK {
			time.Sleep(1 * time.Second)
			continue
		}

		for _, u := range updateResp.Result {
			offset = u.UpdateID + 1

			// ==================================================================
			// 🔘 HANDLE INLINE KEYBOARD CALLBACK QUERIES (FILE MANAGER INTERACTION)
			// ==================================================================
			if u.CallbackQuery != nil {
				cb := u.CallbackQuery
				userID := cb.From.ID
				chatID := cb.Message.Chat.ID
				msgID := cb.Message.MessageID
				data := cb.Data

				if !isAdmin(userID) {
					answerCallbackQuery(cb.ID, "⛔ Akses Ditolak.")
					continue
				}

				// Parser format callback: fm:<action>:<id>:<extra>
				if strings.HasPrefix(data, "dp:") {
					if DeployTanganiCallback(userID, chatID, msgID, data) {
						answerCallbackQuery(cb.ID, "")
					}
					continue
				}

				if strings.HasPrefix(data, "ub:") {
					action := strings.TrimPrefix(data, "ub:")
					setPanelMessage(userID, chatID, msgID)
					answerCallbackQuery(cb.ID, "")
					text, kb := handleUploadBesarCallback(userID, chatID, action)
					updatePanel(userID, chatID, text, kb)
					continue
				}

				if strings.HasPrefix(data, "ts:") {
					action := strings.TrimPrefix(data, "ts:")
					// Panel Tailscale dicatat supaya menu berikutnya menulis ke pesan yang sama.
					setPanelMessage(userID, chatID, msgID)
					answerCallbackQuery(cb.ID, "")
					text, kb := handleTailscaleCallback(userID, chatID, action)
					updatePanel(userID, chatID, text, kb)
					continue
				}

				if strings.HasPrefix(data, "hp:") {
					action := strings.TrimPrefix(data, "hp:")
					switch action {
					case "t": // Buka terminal
						answerCallbackQuery(cb.ID, "")
						if _, err := getOrCreateSession(userID); err != nil {
							sendTelegram(chatID, "❌ <b>Gagal membuka terminal:</b>\n<pre>"+
								html.EscapeString(err.Error())+"</pre>")
							continue
						}
						text, kb := renderTerminal(userID)
						// Pesan bantuan ini menjadi panel terminal ke depan.
						setPanelMessage(userID, chatID, msgID)
						editTelegramMessage(chatID, msgID, text, kb)

					case "f": // Buka file manager
						answerCallbackQuery(cb.ID, "")
						text, kb := renderFileManager(getHomeDir(), 0)
						editTelegramMessage(chatID, msgID, text, kb)

					case "s": // Statistik
						answerCallbackQuery(cb.ID, "")
						// Ganti isi panel bantuan dengan statistik, plus tombol kembali.
						editTelegramMessage(chatID, msgID, buildStatsMessage(), backToHelpKeyboard())

					case "y": // Sysinfo
						answerCallbackQuery(cb.ID, "")
						editTelegramMessage(chatID, msgID, buildSysInfoMessage(), backToHelpKeyboard())

					case "x": // Menu Tailscale
						answerCallbackQuery(cb.ID, "")
						setPanelMessage(userID, chatID, msgID)
						updatePanel(userID, chatID,
							tsStatusRingkas()+"\n\n"+tsMenuFooter(), tsMenuKeyboard())

					case "u": // Panduan upload file besar
						answerCallbackQuery(cb.ID, "")
						setPanelMessage(userID, chatID, msgID)
						text, kb := renderBantuanUploadBesar("")
						updatePanel(userID, chatID, text, kb)

					case "d": // Menu Deploy Bot
						answerCallbackQuery(cb.ID, "")
						setPanelMessage(userID, chatID, msgID)
						teksDeploy, kbDeploy := menuDeployUtama(userID)
						updatePanel(userID, chatID, teksDeploy, kbDeploy)

					case "a": // Semua perintah
						answerCallbackQuery(cb.ID, "")
						editTelegramMessage(chatID, msgID, commandsPlainText(), backToHelpKeyboard())

					case "b": // Kembali ke bantuan utama
						answerCallbackQuery(cb.ID, "")
						editTelegramMessage(chatID, msgID, helpText(), helpKeyboard())

					default:
						answerCallbackQuery(cb.ID, "")
					}
					continue
				}

				if strings.HasPrefix(data, "tm:") {
					action := strings.TrimPrefix(data, "tm:")
					// Pesan yang dipakai tombol ini adalah panel aktif untuk admin.
					setPanelMessage(userID, chatID, msgID)
					switch action {
					case "r": // Refresh layar
						text, kb := renderTerminal(userID)
						editTelegramMessage(chatID, msgID, text, kb)
						answerCallbackQuery(cb.ID, "")

					case "c": // Ctrl+C ke foreground process
						if err := SendSignal(userID, syscall.SIGINT, "SIGINT"); err != nil {
							answerCallbackQuery(cb.ID, "⚠️ "+err.Error())
						} else {
							answerCallbackQuery(cb.ID, "🛑 Ctrl+C terkirim")
							text, kb := renderTerminal(userID)
							editTelegramMessage(chatID, msgID, text, kb)
						}

					case "i": // Info sesi
						answerCallbackQuery(cb.ID, "📋 "+SessionInfo(userID))

					case "k": // Tutup & bunuh sesi
						if KillSession(userID) {
							answerCallbackQuery(cb.ID, "💀 Sesi ditutup")
							editTelegramMessage(chatID, msgID,
								"💀 <b>Sesi terminal ditutup.</b>\n\nKetik <code>/term</code> untuk memulai sesi baru.", nil)
						} else {
							answerCallbackQuery(cb.ID, "⚠️ Tidak ada sesi aktif")
						}

					case "h": // Kembali ke menu utama
						answerCallbackQuery(cb.ID, "")
						editTelegramMessage(chatID, msgID, terminalHelpText(), nil)

					default:
						answerCallbackQuery(cb.ID, "")
					}
					continue
				}

				// Handler tombol mode upload (up:o = timpa, up:c = batal).
				if strings.HasPrefix(data, "up:") {
					action := strings.TrimPrefix(data, "up:")
					switch action {
					case "o": // Setujui penimpaan
						AllowOverwrite(userID)
						answerCallbackQuery(cb.ID, "✅ Silakan kirim ulang filenya")
						editTelegramMessage(chatID, msgID,
							"✅ <b>Penimpaan disetujui.</b>\n\n"+
								"Kirim <b>ulang</b> file yang sama ke chat ini. "+
								"File lama akan ditimpa.", nil)

					case "c": // Batalkan
						ClearUploadTarget(userID)
						answerCallbackQuery(cb.ID, "")
						editTelegramMessage(chatID, msgID,
							"🚫 <b>Dibatalkan.</b>\n\nMode upload dimatikan.",
							&InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
								{{Text: "📁 Buka File Manager", CallbackData: "hp:f"}},
							}})

					default:
						answerCallbackQuery(cb.ID, "")
					}
					continue
				}

				if strings.HasPrefix(data, "fm:") {
					parts := strings.Split(data, ":")
					action := parts[1]

					switch action {
					case "o", "rf": // Open / Refresh Directory
						pathID := parts[2]
						page := 0
						if len(parts) >= 4 {
							page, _ = strconv.Atoi(parts[3])
						}
						targetDir := getPathByID(pathID)
						text, kb := renderFileManager(targetDir, page)
						editTelegramMessage(chatID, msgID, text, kb)
						answerCallbackQuery(cb.ID, "")

					case "f": // Open File Menu
						fileID := parts[2]
						targetFile := getPathByID(fileID)
						text, kb := renderFileDetails(targetFile)
						editTelegramMessage(chatID, msgID, text, kb)
						answerCallbackQuery(cb.ID, "")

					case "vw": // Preview File Text
						fileID := parts[2]
						targetFile := getPathByID(fileID)
						text, kb := renderFilePreview(targetFile, 40)
						editTelegramMessage(chatID, msgID, text, kb)
						answerCallbackQuery(cb.ID, "")

					case "dl": // Download File Directly
						fileID := parts[2]
						targetFile := getPathByID(fileID)
						answerCallbackQuery(cb.ID, "")
						fileData, err := os.ReadFile(targetFile)
						if err != nil {
							sendTelegram(chatID, fmt.Sprintf("❌ Gagal membaca file: %v", err))
						} else {
							caption := fmt.Sprintf("📄 <b>File:</b> <code>%s</code> (%s)", html.EscapeString(filepath.Base(targetFile)), formatBytes(int64(len(fileData))))
							_ = sendTelegramDocument(chatID, filepath.Base(targetFile), fileData, caption)
						}

					case "del": // Ask Delete Confirmation
						id := parts[2]
						targetPath := getPathByID(id)
						text, kb := renderDeleteConfirmation(targetPath)
						editTelegramMessage(chatID, msgID, text, kb)
						answerCallbackQuery(cb.ID, "")

					case "delc": // Confirm Delete
						id := parts[2]
						targetPath := getPathByID(id)
						parentDir := filepath.Dir(targetPath)
						err := os.RemoveAll(targetPath)
						if err != nil {
							answerCallbackQuery(cb.ID, "❌ Gagal menghapus: "+err.Error())
						} else {
							answerCallbackQuery(cb.ID, "✅ Berhasil dihapus!")
							text, kb := renderFileManager(parentDir, 0)
							editTelegramMessage(chatID, msgID, text, kb)
						}

					case "zip": // Zip Folder & Download
						dirID := parts[2]
						targetDir := getPathByID(dirID)
						answerCallbackQuery(cb.ID, "")
						sendTelegram(chatID, fmt.Sprintf("⏳ <i>Mengompresi folder</i> <code>%s</code> <i>menjadi file zip...</i>", html.EscapeString(targetDir)))

						zipBytes, err := zipDirectoryToBytes(targetDir)
						if err != nil {
							sendTelegram(chatID, fmt.Sprintf("❌ Gagal membuat zip: %v", err))
						} else {
							zipName := filepath.Base(targetDir)
							if zipName == "/" || zipName == "." || zipName == "" {
								zipName = "root_folder"
							}
							zipFileName := fmt.Sprintf("%s_%d.zip", zipName, time.Now().Unix())
							caption := fmt.Sprintf("📦 <b>Archive:</b> <code>%s</code> (%s)", html.EscapeString(zipFileName), formatBytes(int64(len(zipBytes))))
							_ = sendTelegramDocument(chatID, zipFileName, zipBytes, caption)
						}

					case "up": // Aktifkan mode upload ke folder ini
						dirID := parts[2]
						targetDir := getPathByID(dirID)
						SetUploadTarget(userID, targetDir)
						text, kb := renderUploadPrompt(targetDir)
						editTelegramMessage(chatID, msgID, text, kb)
						answerCallbackQuery(cb.ID, "")

					case "mk": // Buat folder baru
						dirID := parts[2]
						baseDir := getPathByID(dirID)
						// Nama folder default supaya tidak perlu mengetik.
						newName := fmt.Sprintf("folder_%s", time.Now().Format("20060102_150405"))
						newPath := filepath.Join(baseDir, newName)
						if err := os.MkdirAll(newPath, 0755); err != nil {
							answerCallbackQuery(cb.ID, "❌ Gagal membuat folder: "+err.Error())
						} else {
							answerCallbackQuery(cb.ID, "📁 Folder dibuat: "+newName)
							text, kb := renderFileManager(baseDir, 0)
							editTelegramMessage(chatID, msgID, text, kb)
						}

					case "cls": // Close File Manager
						editTelegramMessage(chatID, msgID, "📁 <i>File Manager ditutup. Ketik <code>/fm</code> untuk membuka kembali.</i>", nil)
						answerCallbackQuery(cb.ID, "File Manager ditutup.")
					}
				}
				continue
			}

			// ==================================================================
			// 📩 HANDLE MESSAGE UPDATES
			// ==================================================================
			if u.Message == nil || u.Message.From == nil {
				continue
			}

			userID := u.Message.From.ID
			chatID := u.Message.Chat.ID
			rawText := strings.TrimSpace(u.Message.Text)
			caption := strings.TrimSpace(u.Message.Caption)

			// Cek Otorisasi Admin
			if !isAdmin(userID) {
				sendTelegram(chatID, "⛔ <b>Akses Ditolak:</b> Anda tidak memiliki izin untuk mengontrol VPS ini.")
				continue
			}

			// ==================================================================
			// 📁 HANDLE UPLOAD DOKUMEN / FILE DARI USER TELEGRAM KE VPS
			// ==================================================================
			if u.Message.Document != nil {
				// Panel deploy menerima berkas lebih dulu saat sedang menunggu.
				// Tanpa pemeriksaan ini, berkas deploy akan jatuh ke file manager
				// biasa dan proyek tidak pernah terbentuk.
				if DeployMenerimaDokumen(chatID, userID, u.Message.Document) {
					continue
				}
				handleDocumentUpload(chatID, userID, u.Message.Document, caption)
				continue
			}

			// Abaikan pesan jika tidak ada teks
			if rawText == "" {
				continue
			}

			// Panel deploy memakai teks untuk URL, alamat GitHub, dan nama entry
			// point. Diperiksa sebelum perintah agar alamat berisi "/" tidak
			// ditafsirkan sebagai slash command.
			if DeployMenerimaTeks(chatID, userID, rawText) {
				continue
			}

			fmt.Printf("[%s] 📩 [User %d] %s\n", time.Now().Format("15:04:05"), userID, rawText)

			fields := strings.Fields(rawText)
			if len(fields) == 0 {
				continue
			}
			cmd := fields[0]
			if atIdx := strings.Index(cmd, "@"); atIdx != -1 {
				cmd = cmd[:atIdx]
			}
			cmdLower := strings.ToLower(cmd)

			switch cmdLower {
			case "/start", "/help":
				sendTelegramWithKeyboard(chatID, helpText(), helpKeyboard())

			case "/deploy", "/panel", "/apps":
				setPanelMessage(userID, chatID, 0)
				teks, kb := menuDeployUtama(userID)
				sendTelegramWithKeyboard(chatID, teks, kb)

			case "/commands":
				sendTelegram(chatID, commandsPlainText())

			case "/fm", "/filemanager", "/ls":
				targetPath := "."
				if len(fields) >= 2 {
					targetPath = strings.TrimSpace(strings.Join(fields[1:], " "))
				}
				absPath, err := filepath.Abs(targetPath)
				if err != nil {
					absPath = targetPath
				}
				text, kb := renderFileManager(absPath, 0)
				updatePanel(userID, chatID, text, kb)

			case "/cat", "/view":
				if len(fields) < 2 {
					sendTelegram(chatID, "❌ Gunakan format: <code>/cat /path/ke/file</code>")
					continue
				}
				targetPath := strings.TrimSpace(fields[1])
				text, kb := renderFilePreview(targetPath, 50)
				updatePanel(userID, chatID, text, kb)

			case "/mkdir":
				if len(fields) < 2 {
					sendTelegram(chatID, "❌ Gunakan format: <code>/mkdir /path/folder_baru</code>")
					continue
				}
				newDir := strings.TrimSpace(fields[1])
				err := os.MkdirAll(newDir, 0755)
				if err != nil {
					updatePanel(userID, chatID, fmt.Sprintf("❌ Gagal membuat folder: %v", err),
						backToTerminalKeyboard())
				} else {
					absDir, _ := filepath.Abs(newDir)
					text, kb := renderFileManager(absDir, 0)
					// Konfirmasi digabung ke panel — tidak lagi dua pesan terpisah.
					updatePanel(userID, chatID,
						fmt.Sprintf("✅ <i>Folder dibuat: <code>%s</code></i>\n\n", html.EscapeString(absDir))+text, kb)
				}

			case "/rm":
				if len(fields) < 2 {
					sendTelegram(chatID, "❌ Gunakan format: <code>/rm /path/file_atau_folder</code>")
					continue
				}
				targetPath := strings.TrimSpace(fields[1])
				text, kb := renderDeleteConfirmation(targetPath)
				updatePanel(userID, chatID, text, kb)

			case "/tailscale", "/ts":
				handleTailscaleCommand(chatID, userID, rawText)

			case "/upload":
				uploadGuide := "📤 <b>Cara Mengunggah File ke VPS:</b>\n\n" +
					"1. Kirim file/dokumen apapun langsung ke bot Telegram ini.\n" +
					"2. Berikan <b>Caption</b> pada file sesuai path tujuan penyimpanan di VPS.\n\n" +
					"<i>Contoh Caption yang bisa digunakan:</i>\n" +
					"• <code>/root/</code> <i>(Menyimpan dengan nama file asli di /root)</i>\n" +
					"• <code>/var/www/html/index.html</code> <i>(Menyimpan dengan nama kustom)</i>\n" +
					"• <code>/etc/nginx/sites-available/</code>\n" +
					"• <i>(Jika caption kosong, file disimpan di folder kerja saat ini)</i>"
				sendTelegram(chatID, uploadGuide)

			case "/ping":
				sendTelegram(chatID, fmt.Sprintf("🏓 <b>Pong!</b> Bot aktif dan berjalan normal.\n⏱ <i>Server Time:</i> <code>%s</code>", time.Now().Format("2006-01-02 15:04:05")))

			case "/ip":
				currPubIP := getPublicIP()
				currLocIP := getLocalIP()
				currTsIP := getTailscaleIP()
				hname, _ := os.Hostname()

				tsInfo := ""
				if currTsIP != "" {
					tsInfo = fmt.Sprintf("• <b>Tailscale IP:</b> <code>%s</code>\n", html.EscapeString(currTsIP))
				}

				msg := fmt.Sprintf(
					"🌐 <b>Informasi Jaringan VPS:</b>\n\n"+
						"• <b>Public IP:</b> <code>%s</code>\n"+
						"• <b>Local IP:</b> <code>%s</code>\n"+
						"%s"+
						"• <b>Hostname:</b> <code>%s</code>\n"+
						"• <b>SSH Command:</b>\n<code>ssh %s@%s</code>",
					html.EscapeString(currPubIP),
					html.EscapeString(currLocIP),
					tsInfo,
					html.EscapeString(hname),
					html.EscapeString(username),
					html.EscapeString(currPubIP),
				)
				sendTelegram(chatID, msg)

			case "/status", "/stats":
				sendTelegram(chatID, buildStatsMessage())

			case "/sysinfo":
				sendTelegram(chatID, buildSysInfoMessage())

			case "/top", "/ps":
				_, out, _ := runBashCommand("ps aux --sort=-%cpu | head -n 11 | awk '{printf \"%-8s %-5s %-5s %-5s %s\\n\", $1, $2, $3, $4, $11}'", 8)
				if out == "" {
					sendTelegram(chatID, "❌ Gagal mengambil daftar proses.")
				} else {
					sendTelegram(chatID, fmt.Sprintf("🔝 <b>Top 10 Proses Tertinggi:</b>\n<pre>%s</pre>", html.EscapeString(out)))
				}

			case "/net", "/ports":
				_, out, _ := runBashCommand("ss -tulpn | head -n 25 || netstat -tulpn | head -n 25", 8)
				if out == "" {
					sendTelegram(chatID, "❌ Tidak ada info port aktif / perintah tidak didukung.")
				} else {
					sendTelegram(chatID, fmt.Sprintf("🔌 <b>Listening Ports & Sockets:</b>\n<pre>%s</pre>", html.EscapeString(out)))
				}

			case "/getfile", "/download":
				parts := strings.SplitN(rawText, " ", 2)
				if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
					sendTelegram(chatID, "❌ Format salah. Gunakan: <code>/getfile /path/to/file</code>\nContoh: <code>/getfile /var/log/syslog</code>")
					continue
				}
				targetPath := strings.TrimSpace(parts[1])
				fileInfo, err := os.Stat(targetPath)
				if err != nil {
					sendTelegram(chatID, fmt.Sprintf("❌ File tidak ditemukan atau tidak dapat diakses: <code>%s</code>", html.EscapeString(targetPath)))
					continue
				}

				if fileInfo.IsDir() {
					sendTelegram(chatID, "❌ Target adalah direktori, bukan file. Silakan gunakan <code>/fm</code> untuk zip folder tersebut.")
					continue
				}

				if fileInfo.Size() > 50*1024*1024 {
					sendTelegram(chatID, fmt.Sprintf("❌ Ukuran file terlalu besar (%s). Maksimal 50MB.", formatBytes(fileInfo.Size())))
					continue
				}

				sendTelegram(chatID, fmt.Sprintf("⏳ Mengunggah file <code>%s</code>...", html.EscapeString(filepath.Base(targetPath))))

				data, err := os.ReadFile(targetPath)
				if err != nil {
					sendTelegram(chatID, fmt.Sprintf("❌ Gagal membaca file: %v", err))
					continue
				}

				caption := fmt.Sprintf("📄 <b>File:</b> <code>%s</code> (%s)", html.EscapeString(filepath.Base(targetPath)), formatBytes(int64(len(data))))
				err = sendTelegramDocument(chatID, filepath.Base(targetPath), data, caption)
				if err != nil {
					sendTelegram(chatID, fmt.Sprintf("❌ Gagal mengirim dokumen ke Telegram: %v", err))
				}

			case "/reboot":
				parts := strings.SplitN(rawText, " ", 2)
				if len(parts) < 2 || strings.ToLower(strings.TrimSpace(parts[1])) != "confirm" {
					sendTelegram(chatID, "⚠️ <b>Konfirmasi Diperlukan!</b>\n\nUntuk me-restart VPS, ketik:\n<code>/reboot confirm</code>")
					continue
				}

				sendTelegram(chatID, "🔄 <b>Memulai proses Reboot VPS sekarang...</b>\nBot akan kembali online setelah server selesai restart.")
				time.Sleep(1 * time.Second)
				go func() {
					runBashCommand("reboot || shutdown -r now", 10)
				}()

			case "/term", "/t", "/shell":
				handleTerminalCommand(chatID, userID, rawText)

			case "/update", "/upgrade":
				handleUpdateCommand(chatID, userID, rawText)

			case "/unduh", "/wget", "/download-url":
				handleUnduhURL(chatID, userID, rawText)

			case "/chunk", "/bagian":
				handleChunk(chatID, userID, rawText)

			case "/pecah", "/split":
				handlePecahFile(chatID, userID, rawText)

			case "/gabung", "/join":
				handleGabungFile(chatID, userID, rawText)

			case "/exec", "/cmd":
				parts := strings.SplitN(rawText, " ", 2)
				if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
					sendTelegram(chatID, "❌ Format salah. Gunakan: <code>/exec &lt;perintah_bash&gt;</code>\nContoh: <code>/exec uname -a</code>")
					continue
				}

				cmdToRun := strings.TrimSpace(parts[1])
				fmt.Printf("⚡ Menjalankan command: %s\n", cmdToRun)
				sendTelegram(chatID, fmt.Sprintf("⏳ <i>Menjalankan:</i> <code>%s</code>...", html.EscapeString(cmdToRun)))

				code, stdout, stderr := runBashCommand(cmdToRun, 60)

				fullOutput := fmt.Sprintf("COMMAND: %s\nEXIT CODE: %d\n\n--- STDOUT ---\n%s\n\n--- STDERR ---\n%s\n", cmdToRun, code, stdout, stderr)

				reply := fmt.Sprintf("💻 <b>Perintah:</b> <code>%s</code>\n⚙️ <b>Exit Code:</b> <code>%d</code>\n\n", html.EscapeString(cmdToRun), code)

				if stdout != "" {
					safeOut := stdout
					if len(safeOut) > 2500 {
						safeOut = safeOut[:2500] + "\n\n... (Output dipotong, file lengkap terlampir)"
					}
					reply += fmt.Sprintf("<b>Output:</b>\n<pre>%s</pre>\n", html.EscapeString(safeOut))
				}
				if stderr != "" {
					safeErr := stderr
					if len(safeErr) > 800 {
						safeErr = safeErr[:800] + "\n... (Error dipotong)"
					}
					reply += fmt.Sprintf("<b>Error:</b>\n<pre>%s</pre>\n", html.EscapeString(safeErr))
				}
				if stdout == "" && stderr == "" {
					reply += "<i>(Perintah selesai dieksekusi tanpa output teks)</i>"
				}

				sendTelegram(chatID, reply)

				if len(stdout) > 2500 || len(stderr) > 800 {
					filename := fmt.Sprintf("exec_output_%d.txt", time.Now().Unix())
					_ = sendTelegramDocument(chatID, filename, []byte(fullOutput), "📄 <i>Full Execution Log</i>")
				}

			default:
				// Perintah tanpa garis miring (mis. "cd /var", "ls -la") diteruskan ke
				// shell terminal bila sesi aktif. Ini membuat chat terasa seperti
				// terminal sungguhan tanpa perlu menyalakan mode khusus.
				// Perintah bot selalu diawali "/" sehingga tidak akan bentrok.
				if rawText != "" && !strings.HasPrefix(rawText, "/") && HasTerminalSession(userID) {
					SendRawToTerminal(chatID, userID, rawText)
				} else if rawText != "" && !strings.HasPrefix(rawText, "/") && IsTerminalDirectMode(userID) {
					SendRawToTerminal(chatID, userID, rawText)
				}
				// Selain itu: abaikan pesan non-command yang tidak dikenal.
			}
		}
	}
}

// telegramGetMe memverifikasi token dan mengambil identitas bot.
// Dipakai saat startup supaya bot tidak mengklaim berhasil dengan token
// yang ditolak Telegram.
func telegramGetMe() (nama string, username string, err error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(ApiUrl + "/getMe")
	if err != nil {
		return "", "", fmt.Errorf("tidak bisa menghubungi Telegram: %w", err)
	}
	defer resp.Body.Close()

	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", fmt.Errorf("respons Telegram tidak bisa dibaca: %w", err)
	}
	if !out.OK {
		if out.Description == "" {
			out.Description = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return "", "", fmt.Errorf("%s", out.Description)
	}
	return out.Result.FirstName, out.Result.Username, nil
}
