package deploy

// ==============================================================================
// 🚀 DEPLOY BOT — PANEL DEPLOY MANDIRI
// ==============================================================================
// Menu deploy bot per-proyek di dalam bot-vps-control, mendukung Python,
// Node.js, Go, binary jadi, dan shell.
//
// Kenapa fitur ini cocok ada di bot ini:
//
// Bot ini biasanya berjalan sebagai PID 1 di container, sehingga ia tidak bisa
// menggantikan dirinya sendiri. Tapi ia bisa melahirkan, menghentikan, dan
// menjalankan ulang proses LAIN. Jadi panel deploy di sini justru lebih andal
// daripada updater mandiri: yang direstart adalah proses terpisah, bukan bot
// yang sedang melayani perintah.
//
// Prinsip keamanan yang dipegang:
//
//  1. Nama proyek dan URL selalu lewat pembersihan. Satu nama proyek berisi
//     `;` atau `../` sudah cukup untuk keluar dari direktori atau menjalankan
//     perintah sembarang.
//  2. Setiap perintah eksternal punya batas waktu. `npm install` yang macet
//     akan membekukan seluruh bot karena update Telegram diproses berurutan.
//  3. Proses dijalankan dengan setsid + nohup supaya terlepas dari induknya.
//     Tanpa itu, menutup bot akan ikut mematikan semua proyek yang dikelola.

import (
	"bot-vps-control/internal/update"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ==============================================================================
// KONFIGURASI
// ==============================================================================

// DirDeploy adalah induk semua proyek yang dikelola panel deploy.
//
// Urutan pemilihan lokasi disusun dari yang paling aman:
//
//  1. DEPLOY_DIR — untuk yang ingin menentukan sendiri, misalnya di VPS
//     tempat bot berjalan sebagai root dan /opt memang bisa ditulis.
//  2. ~/.local/share/deploy-bot — lokasi standar data aplikasi pengguna.
//     Dipilih sebagai cadangan karena /opt TIDAK bisa ditulis oleh pengguna
//     biasa, dan bot sering dijalankan tanpa root.
//  3. /tmp/deploy-bot — pilihan terakhir bila HOME pun tidak bisa ditulis.
//
// Direktori kerja bot dipakai lebih dulu bila bisa ditulis, karena itu tempat
// paling mudah ditemukan pengguna. Tetapi tidak dipakai secara default: bot
// bisa dipasang di direktori yang hanya bisa dibaca.
var DirDeploy = func() string {
	if d := strings.TrimSpace(os.Getenv("DEPLOY_DIR")); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "deploy-bot")
	}
	return filepath.Join(os.TempDir(), "deploy-bot")
}()

// Batas ukuran unduhan. 256 MB cukup untuk hampir semua proyek Python/Node/Go,
// dan mencegah satu unduhan besar mengisi disk VPS sampai penuh.
const maksUnduhDeploy = 256 << 20

// Batas waktu tiap tahap. Semua perintah eksternal WAJIB punya batas.
const (
	batasInstalasiDeploy = 900 * time.Second
	batasUnduhDeploy     = 300 * time.Second
	batasGitDeploy       = 300 * time.Second
	batasSingkatDeploy   = 20 * time.Second
)

// Jumlah proyek per halaman daftar.
const perHalamanDeploy = 5

// Kode bahasa yang dikenali.
const (
	bahasaPython = "python"
	bahasaNode   = "node"
	bahasaGo     = "go"
	bahasaBiner  = "biner"
	bahasaShell  = "shell"
)

func labelBahasa(b string) string {
	switch b {
	case bahasaPython:
		return "🐍 Python"
	case bahasaNode:
		return "🟢 Node.js"
	case bahasaGo:
		return "🔵 Go"
	case bahasaBiner:
		return "⚙️ Binary"
	case bahasaShell:
		return "📜 Shell"
	}
	return "❔ Tidak dikenal"
}

// Berkas penanda di dalam direktori proyek. Pilihan bertahan walau bot
// di-restart karena disimpan di disk, bukan di memori.
const (
	penandaBahasaDeploy = ".deploy.lang"
	penandaEntryDeploy  = ".deploy.entry"
)

// dirDeployPengguna mengembalikan direktori induk proyek satu pengguna.
//
// Galat pembuatan direktori sengaja dibiarkan naik ke pemanggil. Sebelumnya
// galat diabaikan di sini, dan akibatnya pengguna hanya melihat pesan
// "direktori proyek tidak ditemukan" jauh kemudian — tanpa tahu bahwa
// sebenarnya induknya tidak bisa dibuat sejak awal.
func dirDeployPengguna(userID int64) (string, error) {
	path := filepath.Join(DirDeploy, strconv.FormatInt(userID, 10))
	if err := os.MkdirAll(path, 0o755); err != nil {
		return path, fmt.Errorf("tidak bisa membuat %s: %w", path, err)
	}
	return path, nil
}

// dirDeployPenggunaWajib sama dengan di atas, tapi mengembalikan jalur cadangan
// tanpa galat untuk pemakaian yang hanya membaca.
func dirDeployPenggunaWajib(userID int64) string {
	path, err := dirDeployPengguna(userID)
	if err != nil {
		// Pemanggil hanya membaca. Jalur tetap dikembalikan supaya daftar
		// kosong ditampilkan, bukan kegagalan yang menghentikan panel.
		return path
	}
	return path
}

// siapkanDirDeploy memastikan direktori induk bisa dipakai, dan mengembalikan
// pesan yang bisa dibaca pengguna bila tidak.
//
// Dipanggil sebelum setiap deploy supaya kegagalan izin muncul di awal dengan
// penjelasan yang jelas, bukan sebagai galat samar di tengah proses.
func siapkanDirDeploy(userID int64) (string, string) {
	dir, err := dirDeployPengguna(userID)
	if err != nil {
		pesan := "❌ <b>Tidak bisa menyiapkan direktori proyek.</b>\n\n" +
			"<code>" + update.HtmlEscapeRingkas(err.Error(), 300) + "</code>\n\n" +
			"Lokasi saat ini: <code>" + update.HtmlEscapeRingkas(DirDeploy, 200) + "</code>\n\n" +
			"<i>Setel lokasi lain yang bisa ditulis lewat variabel</i> " +
			"<code>DEPLOY_DIR</code>."
		return dir, pesan
	}

	// Periksa kemampuan menulis secara nyata. Memeriksa izin saja tidak cukup:
	// direktori bisa ada tapi berada di sistem berkas hanya-baca.
	uji := filepath.Join(dir, ".uji-tulis")
	if err := os.WriteFile(uji, []byte("uji"), 0o644); err != nil {
		pesan := "❌ <b>Direktori tidak bisa ditulis.</b>\n\n" +
			"<code>" + update.HtmlEscapeRingkas(dir, 300) + "</code>\n\n" +
			"<i>Setel lokasi lain lewat variabel</i> <code>DEPLOY_DIR</code>."
		return dir, pesan
	}
	os.Remove(uji)

	return dir, ""
}

// ==============================================================================
// PEMBERSIHAN NILAI DARI PENGGUNA
// ==============================================================================

var polaNamaAman = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

// namaProyekAman membersihkan nama proyek agar aman jadi nama direktori.
//
// Hanya huruf, angka, garis bawah, titik, dan tanda hubung yang lolos.
// Titik di awal dibuang supaya proyek tidak menjadi direktori tersembunyi
// atau, lebih buruk, "..".
func namaProyekAman(nama string) string {
	nama = filepath.Base(strings.TrimSpace(nama))
	nama = polaNamaAman.ReplaceAllString(nama, "_")
	nama = strings.TrimLeft(nama, ".")
	if len(nama) > 64 {
		nama = nama[:64]
	}
	if nama == "" {
		return "proyek"
	}
	return nama
}

// validasiURL memastikan URL yang diberikan masuk akal sebelum diunduh.
//
// Hanya http dan https yang diizinkan. Tanpa pemeriksaan skema, URL seperti
// `file:///etc/passwd` bisa dipakai membaca berkas lokal.
func validasiURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("URL kosong")
	}
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return "", fmt.Errorf("hanya http:// dan https:// yang didukung")
	}
	if len(raw) > 2000 {
		return "", fmt.Errorf("URL terlalu panjang")
	}
	return raw, nil
}

// validasiRepoGit menerima bentuk pemilik/repo atau URL penuh.
func validasiRepoGit(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("alamat repository kosong")
	}

	// Bentuk ringkas: pemilik/repo
	if !strings.Contains(raw, "://") {
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(raw) {
			return "", fmt.Errorf("format tidak dikenali, pakai pemilik/repo")
		}
		return "https://github.com/" + strings.TrimSuffix(raw, ".git") + ".git", nil
	}

	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, "https://") {
		return "", fmt.Errorf("repository harus memakai https://")
	}
	if !strings.HasSuffix(raw, ".git") {
		raw = strings.TrimSuffix(raw, "/") + ".git"
	}
	return raw, nil
}

// ==============================================================================
// DETEKSI BAHASA
// ==============================================================================

// bacaPenanda membaca isi berkas penanda di direktori proyek.
func bacaPenanda(dir string, nama string) string {
	data, err := os.ReadFile(filepath.Join(dir, nama))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// tulisPenanda menyimpan penanda. Kegagalan tulis tidak menghentikan deploy —
// penanda hanya memperbaiki tebakan, bukan syarat agar proyek bisa jalan.
func tulisPenanda(dir string, nama string, isi string) {
	os.WriteFile(filepath.Join(dir, nama), []byte(isi), 0o644)
}

// deteksiBahasa menebak bahasa proyek dari isi direktorinya.
//
// Pilihan manual selalu menang, supaya tebakan yang salah bisa dikoreksi
// tanpa memindahkan berkas. Binary diperiksa lebih dulu daripada berkas
// sumber, karena proyek binary sering menyertakan beberapa skrip bantu.
func deteksiBahasa(dir string) string {
	if p := bacaPenanda(dir, penandaBahasaDeploy); p != "" {
		switch p {
		case bahasaPython, bahasaNode, bahasaGo, bahasaBiner, bahasaShell:
			return p
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	var nama []string
	for _, e := range entries {
		nama = append(nama, e.Name())
	}

	// ---- Binary jadi ----
	for _, n := range nama {
		path := filepath.Join(dir, n)
		fi, err := os.Stat(path)
		if err != nil || fi.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(n), ".exe") {
			return bahasaBiner
		}
		if apakahELF(path) {
			return bahasaBiner
		}
	}

	// ---- Go ----
	for _, n := range nama {
		if n == "go.mod" || n == "go.sum" || strings.HasSuffix(n, ".go") {
			return bahasaGo
		}
	}

	// ---- Node.js ----
	for _, n := range nama {
		if n == "package.json" {
			return bahasaNode
		}
	}

	// ---- Python ----
	for _, n := range nama {
		if n == "requirements.txt" || n == "pyproject.toml" || strings.HasSuffix(n, ".py") {
			return bahasaPython
		}
	}

	// ---- Shell ----
	for _, n := range nama {
		if strings.HasSuffix(n, ".sh") {
			return bahasaShell
		}
	}

	// ---- Node tanpa package.json (cadangan terakhir) ----
	for _, n := range nama {
		if strings.HasSuffix(n, ".js") {
			return bahasaNode
		}
	}

	return ""
}

// apakahELF memeriksa 4 byte pertama berkas.
//
// Dipakai untuk mengenali binary jadi dari isinya, bukan hanya dari namanya.
// Binary hasil kompilasi Go/Rust/C sering tidak punya ekstensi apa pun.
func apakahELF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	var kepala [4]byte
	if _, err := io.ReadFull(f, kepala[:]); err != nil {
		return false
	}
	// ELF, Mach-O, dan PE.
	if kepala[0] == 0x7f && kepala[1] == 'E' && kepala[2] == 'L' && kepala[3] == 'F' {
		return true
	}
	if bytes.Equal(kepala[:], []byte{0xfe, 0xed, 0xfa, 0xce}) ||
		bytes.Equal(kepala[:], []byte{0xfe, 0xed, 0xfa, 0xcf}) {
		return true
	}
	if kepala[0] == 'M' && kepala[1] == 'Z' {
		return true
	}
	return false
}

// cariEntrypoint menentukan berkas utama yang akan dijalankan.
func cariEntrypoint(dir string, bahasa string) string {
	// Pilihan manual lebih dulu untuk binary dan shell: tidak ada cara pasti
	// menebak mana berkas yang dimaksud pengguna.
	if p := bacaPenanda(dir, penandaEntryDeploy); p != "" {
		if fi, err := os.Stat(filepath.Join(dir, p)); err == nil && !fi.IsDir() {
			return p
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	var berkas []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		berkas = append(berkas, e.Name())
	}
	sort.Strings(berkas)

	switch bahasa {
	case bahasaBiner:
		// Berkas dengan bit eksekusi adalah kandidat terkuat.
		for _, n := range berkas {
			fi, err := os.Stat(filepath.Join(dir, n))
			if err == nil && fi.Mode()&0o111 != 0 {
				return n
			}
		}

		// Binary yang baru diunduh sering belum punya bit eksekusi. Tanpa
		// cadangan ini, proyek tidak akan pernah bisa dijalankan: entry point
		// tidak ketemu karena tidak ada yang eksekutabel, dan tidak ada yang
		// eksekutabel karena entry point-nya tidak pernah ditentukan.
		//
		// Hanya ELF dan .exe yang dianggap kandidat, supaya berkas data atau
		// dokumentasi tidak salah dipilih sebagai program.
		for _, n := range berkas {
			if strings.HasSuffix(strings.ToLower(n), ".exe") {
				return n
			}
			if apakahELF(filepath.Join(dir, n)) {
				return n
			}
		}
		return ""

	case bahasaGo:
		// Paket main dicari dari isi berkasnya.
		for _, n := range berkas {
			if !strings.HasSuffix(n, ".go") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, n))
			if err == nil && strings.Contains(string(data), "func main(") {
				return n
			}
		}
		return ""

	case bahasaShell:
		for _, kandidat := range []string{"run.sh", "start.sh", "main.sh", "start"} {
			for _, n := range berkas {
				if n == kandidat {
					return n
				}
			}
		}
		for _, n := range berkas {
			if strings.HasSuffix(n, ".sh") {
				return n
			}
		}
		return ""

	default:
		kandidat := []string{
			"main.py", "bot.py", "app.py", "server.py", "run.py", "start.py",
			"main.js", "bot.js", "app.js", "server.js", "index.js",
		}
		for _, k := range kandidat {
			for _, n := range berkas {
				if n == k {
					return n
				}
			}
		}
		return ""
	}
}

// ==============================================================================
// PROSES — JALAN, HENTI, STATUS
// ==============================================================================

// namaProsesDeploy membangun nama proses unik per pengguna dan proyek.
//
// Awalan yang berbeda dari fitur lain mencegah satu panel mematikan proses
// milik panel lain saat nama proyeknya kebetulan sama.
func namaProsesDeploy(userID int64, proyek string) string {
	return fmt.Sprintf("deploy_%d_%s", userID, proyek)
}

func berkasPIDDeploy(scr string) string   { return "/tmp/" + scr + ".pid" }
func berkasWaktuDeploy(scr string) string { return "/tmp/" + scr + ".start" }
func berkasLogDeploy(scr string) string   { return "/tmp/" + scr + ".log" }

// pidHidupDeploy mengembalikan PID bila proses masih hidup, atau 0.
//
// Berkas PID yang tertinggal dibersihkan di sini supaya status tidak terus
// melaporkan "Running" palsu setelah proses mati mendadak.
func pidHidupDeploy(scr string) int {
	data, err := os.ReadFile(berkasPIDDeploy(scr))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		bersihkanBerkasProsesDeploy(scr)
		return 0
	}
	if _, err := os.Stat("/proc/" + strconv.Itoa(pid)); err != nil {
		bersihkanBerkasProsesDeploy(scr)
		return 0
	}
	return pid
}

func bersihkanBerkasProsesDeploy(scr string) {
	os.Remove(berkasPIDDeploy(scr))
	os.Remove(berkasWaktuDeploy(scr))
}

// prosesAktifDeploy mengembalikan nama proyek pengguna yang prosesnya hidup.
func prosesAktifDeploy(userID int64) map[string]bool {
	aktif := map[string]bool{}
	for _, nama := range daftarProyekDeploy(userID) {
		if pidHidupDeploy(namaProsesDeploy(userID, nama)) != 0 {
			aktif[nama] = true
		}
	}
	return aktif
}

// matikanProsesDeploy menghentikan proses beserta anak-anaknya.
//
// SIGTERM lebih dulu supaya program sempat menutup koneksi dan menyimpan data,
// lalu SIGKILL bila masih membandel. Membunuh anak proses penting karena bot
// Python/Node sering melahirkan subprocess: membunuh induknya saja akan
// meninggalkan anak yatim yang tetap memegang port.
func matikanProsesDeploy(scr string) {
	pid := pidHidupDeploy(scr)
	if pid > 0 {
		spid := strconv.Itoa(pid)
		jalankanShell(fmt.Sprintf("kill -TERM %s 2>/dev/null", spid), "", 10*time.Second)
		jalankanShell(fmt.Sprintf("pkill -TERM -P %s 2>/dev/null", spid), "", 10*time.Second)
		time.Sleep(700 * time.Millisecond)
		jalankanShell(fmt.Sprintf("kill -KILL %s 2>/dev/null", spid), "", 10*time.Second)
		jalankanShell(fmt.Sprintf("pkill -KILL -P %s 2>/dev/null", spid), "", 10*time.Second)
	}
	bersihkanBerkasProsesDeploy(scr)
}

// jalankanShell menjalankan perintah lewat sh dengan batas waktu.
func jalankanShell(perintah string, dir string, batas time.Duration) (bool, string) {
	cmd := exec.Command("sh", "-c", perintah)
	cmd.Dir = dir

	// Go di lingkungan terbatas butuh lokasi cache yang bisa ditulis.
	// Tanpa ini, perintah go bisa gagal hanya karena HOME tidak bisa ditulis.
	env := os.Environ()
	env = append(env,
		"GOCACHE=/tmp/gocache-deploy",
		"GOPATH=/tmp/gopath-deploy",
		"GOFLAGS=-mod=mod",
		"PYTHONUNBUFFERED=1",
	)
	cmd.Env = env

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Start(); err != nil {
		return false, err.Error()
	}

	selesai := make(chan error, 1)
	go func() { selesai <- cmd.Wait() }()

	select {
	case err := <-selesai:
		if err != nil {
			return false, buf.String()
		}
		return true, buf.String()
	case <-time.After(batas):
		// Bunuh seluruh proses grup: perintah shell bisa melahirkan anak yang
		// tetap hidup walau induknya sudah dibunuh.
		if cmd.Process != nil {
			jalankanShell("kill -KILL -"+strconv.Itoa(cmd.Process.Pid)+" 2>/dev/null", "", 5*time.Second)
			cmd.Process.Kill()
		}
		<-selesai
		return false, fmt.Sprintf("⏱ Melewati batas waktu %v\n%s", batas, buf.String())
	}
}

// adaPerintah memeriksa apakah sebuah program tersedia di PATH.
func adaPerintah(nama string) bool {
	_, err := exec.LookPath(nama)
	return err == nil
}

// perintahJalan menyusun perintah untuk menjalankan proyek sesuai bahasanya.
func perintahJalan(dir string, bahasa string, entry string) (string, error) {
	entryAman := kutipShell(entry)

	switch bahasa {
	case bahasaPython:
		if !adaPerintah("python3") {
			return "", fmt.Errorf("python3 tidak tersedia di VPS ini")
		}
		return "python3 -u " + entryAman, nil

	case bahasaNode:
		if !adaPerintah("node") {
			return "", fmt.Errorf("node tidak tersedia di VPS ini")
		}
		return "node " + entryAman, nil

	case bahasaGo:
		// Binary hasil build lebih disukai: tidak butuh Go saat dijalankan dan
		// tidak memakai RAM untuk mengompilasi setiap kali restart.
		biner := filepath.Join(dir, "app_bin")
		if fi, err := os.Stat(biner); err == nil && !fi.IsDir() {
			os.Chmod(biner, fi.Mode()|0o111)
			return "./app_bin", nil
		}
		if !adaPerintah("go") {
			return "", fmt.Errorf("go tidak tersedia dan binary belum dibangun")
		}
		return "go run " + entryAman, nil

	case bahasaBiner:
		// Binary yang diunduh dari arsip sering kehilangan bit eksekusi.
		// Diperbaiki di sini supaya pengguna tidak perlu chmod manual.
		path := filepath.Join(dir, entry)
		if fi, err := os.Stat(path); err == nil {
			os.Chmod(path, fi.Mode()|0o111)
		}
		return "./" + entryAman, nil

	case bahasaShell:
		return "sh " + entryAman, nil
	}

	return "", fmt.Errorf("bahasa %q belum didukung", bahasa)
}

// kutipShell membungkus teks dengan kutip tunggal.
//
// Dipakai setiap kali nilai berasal dari pengguna. Tanpa ini, satu nama
// proyek berisi `;` sudah cukup untuk menjalankan perintah sembarang.
func kutipShell(teks string) string {
	return "'" + strings.ReplaceAll(teks, "'", `'"'"'`) + "'"
}

// jalankanProyekDeploy menyalakan proyek. Kembalikan (berhasil, pesanHTML).
func jalankanProyekDeploy(userID int64, proyek string) (bool, string) {
	dir := filepath.Join(dirDeployPenggunaWajib(userID), proyek)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return false, "❌ Direktori proyek tidak ditemukan."
	}

	scr := namaProsesDeploy(userID, proyek)

	// Sudah jalan? Hentikan dulu supaya tidak ada dua proses memakai port sama.
	if pidHidupDeploy(scr) != 0 {
		matikanProsesDeploy(scr)
	}

	bahasa := deteksiBahasa(dir)
	if bahasa == "" {
		return false, "❌ Bahasa proyek tidak dikenali.\n\n" +
			"Pastikan ada <code>main.py</code>, <code>package.json</code>, " +
			"<code>go.mod</code>, atau binary."
	}

	entry := cariEntrypoint(dir, bahasa)
	if entry == "" {
		return false, "❌ Berkas utama untuk " + labelBahasa(bahasa) + " tidak ditemukan.\n\n" +
			"Tentukan manual lewat tombol <b>Bahasa</b> atau <b>Entry Point</b>."
	}

	perintah, err := perintahJalan(dir, bahasa, entry)
	if err != nil {
		return false, "❌ " + update.HtmlEscapeRingkas(err.Error(), 200)
	}

	pidFile := berkasPIDDeploy(scr)
	logFile := berkasLogDeploy(scr)
	waktuFile := berkasWaktuDeploy(scr)

	// setsid + nohup supaya proses lepas dari induknya. Tanpa itu, menutup bot
	// akan ikut mematikan semua proyek yang dikelola.
	skrip := fmt.Sprintf(
		"cd %s && setsid nohup %s >> %s 2>&1 < /dev/null & echo $! > %s",
		kutipShell(dir), perintah, kutipShell(logFile), kutipShell(pidFile),
	)

	jalankanShell(skrip, dir, batasSingkatDeploy)
	time.Sleep(1500 * time.Millisecond)

	if pidHidupDeploy(scr) == 0 {
		// Proses langsung mati. Ekor log ikut dikirim supaya pengguna tahu
		// sebabnya tanpa harus membuka menu Log.
		return false, "❌ <b>Proyek langsung berhenti.</b>\n\n" +
			"Kemungkinan ada kesalahan saat start:\n<pre>" +
			update.HtmlEscapeRingkas(bacaEkorLogDeploy(scr, 700), 900) + "</pre>"
	}

	os.WriteFile(waktuFile, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0o644)
	return true, ""
}

// bacaEkorLogDeploy membaca bagian akhir berkas log.
func bacaEkorLogDeploy(scr string, batas int) string {
	data, err := os.ReadFile(berkasLogDeploy(scr))
	if err != nil {
		return "(log kosong)"
	}
	teks := string(data)
	if len(teks) > batas {
		teks = "…" + teks[len(teks)-batas:]
	}
	if strings.TrimSpace(teks) == "" {
		return "(log kosong)"
	}
	return teks
}

// statusProsesDeploy meringkas keadaan proses untuk panel kontrol.
type statusProsesDeploy struct {
	Jalan  bool
	PID    int
	Uptime string
	CPU    string
	RAM    string
}

func infoProsesDeploy(userID int64, proyek string) statusProsesDeploy {
	scr := namaProsesDeploy(userID, proyek)
	pid := pidHidupDeploy(scr)

	st := statusProsesDeploy{Uptime: "-", CPU: "-", RAM: "-"}
	if pid == 0 {
		return st
	}
	st.Jalan = true
	st.PID = pid

	if data, err := os.ReadFile(berkasWaktuDeploy(scr)); err == nil {
		if mulai, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
			detik := time.Now().Unix() - mulai
			hari := detik / 86400
			jam := (detik % 86400) / 3600
			menit := (detik % 3600) / 60
			switch {
			case hari > 0:
				st.Uptime = fmt.Sprintf("%dh %dj", hari, jam)
			case jam > 0:
				st.Uptime = fmt.Sprintf("%dj %dm", jam, menit)
			case menit > 0:
				st.Uptime = fmt.Sprintf("%dm %ds", menit, detik%60)
			default:
				st.Uptime = fmt.Sprintf("%ds", detik)
			}
		}
	}

	// ps tidak tersedia di sebagian image minimal. Kegagalan di sini tidak
	// boleh menggagalkan seluruh tampilan.
	ok, keluaran := jalankanShell(
		"ps -p "+strconv.Itoa(pid)+" -o %cpu,rss --no-headers", "", 5*time.Second)
	if ok {
		bagian := strings.Fields(strings.TrimSpace(keluaran))
		if len(bagian) >= 2 {
			if cpu, err := strconv.ParseFloat(bagian[0], 64); err == nil {
				st.CPU = fmt.Sprintf("%.1f%%", cpu)
			}
			if rss, err := strconv.Atoi(bagian[1]); err == nil {
				st.RAM = formatUkuran(int64(rss) * 1024)
			}
		}
	}

	return st
}

// formatUkuran mengubah byte menjadi teks yang mudah dibaca.
func formatUkuran(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// daftarProyekDeploy mengembalikan nama semua proyek milik pengguna.
func daftarProyekDeploy(userID int64) []string {
	entries, err := os.ReadDir(dirDeployPenggunaWajib(userID))
	if err != nil {
		return nil
	}
	var hasil []string
	for _, e := range entries {
		if e.IsDir() {
			hasil = append(hasil, e.Name())
		}
	}
	sort.Strings(hasil)
	return hasil
}
