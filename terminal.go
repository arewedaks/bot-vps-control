package main

// ==============================================================================
// 🖥️ INTERACTIVE PTY TERMINAL — LEVEL 3 (Zero External Dependency)
// ==============================================================================
// Terminal sesungguhnya lewat pseudo-terminal (PTY) memakai syscall stdlib.
// Tidak memakai github.com/creack/pty — menjaga sifat nol-dependency project.
//
// Kemampuan:
//   - Shell persisten: `cd`, `export`, history, alias semua MENETAP antar perintah.
//   - TTY asli: program yang butuh terminal (sudo, top, apt, mysql) berjalan normal.
//   - Ctrl+C / kirim sinyal ke foreground process group.
//   - Output diambil dari buffer layar, kode ANSI dibersihkan.
//
// Batas yang disadari (ponytail):
//   - Output = snapshot layar saat ini, bukan stream penuh. Program full-screen
//     seperti vim/nano bisa dilihat tapi tidak nyaman. Upgrade: rekam via screen
//     -L atau pipe byte mentah ke file lalu kirim sebagai lampiran.
//   - Satu sesi per admin, disimpan in-memory. Bot restart = semua sesi mati.
//     Upgrade: simpan PID master ke /var/run lalu re-attach saat start.

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// ==============================================================================
// 🧩 STRUKTUR SESI TERMINAL
// ==============================================================================

type TermSession struct {
	Mu        sync.Mutex
	Master    *os.File
	Cmd       *exec.Cmd
	Shell     string
	StartedAt time.Time
	LastUsed  time.Time
	Screen    []byte // buffer output, untuk snapshot layar
	Alive     bool
	Reader    chan struct{} // sinyal pembaca berhenti
}

var (
	termSessionsMu sync.Mutex
	termSessions   = make(map[int64]*TermSession)
)

const (
	termScreenCap     = 256 * 1024 // 256KB: cukup menampung output `apt upgrade` penuh
	termPollWindow    = 900 * time.Millisecond
	termIdleTimeout   = 30 * time.Minute // auto-kill sesi nganggur
	termMaxSessions   = 10               // batas total sesi (anti RAM bocor)
	termIoctlUnlockPT = 0x40045431       // TIOCSPTLCK  — unlock slave PTY
	termIoctlGetPTNum = 0x80045430       // TIOCGPTN    — nomor slave PTY
	termIoctlGetWinSz = 0x5413           // TIOCGWINSZ
	termIoctlSetWinSz = 0x5414           // TIOCSWINSZ
)

// ==============================================================================
// 🔧 SISTEM TERENDAH: MEMBUAT PTY TANPA DEPENDENCY EKSTERNAL
// ==============================================================================

// openPTY membuat pasangan master/slave pseudo-terminal via /dev/ptmx.
// Semua operasi lewat syscall stdlib — tidak ada modul pihak ketiga.
func openPTY() (*os.File, *os.File, error) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("tidak bisa membuka /dev/ptmx (kernel tanpa dukungan PTY?): %w", err)
	}

	// TIOCSPTLCK: buka kunci slave supaya bisa diakses
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), termIoctlUnlockPT, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		master.Close()
		return nil, nil, fmt.Errorf("unlockpt gagal (errno %d): %v", errno, errno)
	}

	// TIOCGPTN: tanyakan nomor slave ke kernel
	var ptNum uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), termIoctlGetPTNum, uintptr(unsafe.Pointer(&ptNum))); errno != 0 {
		master.Close()
		return nil, nil, fmt.Errorf("ptsname gagal (errno %d): %v", errno, errno)
	}

	slavePath := fmt.Sprintf("/dev/pts/%d", ptNum)
	slave, err := os.OpenFile(slavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("tidak bisa membuka %s (devpts tidak ter-mount?): %w", slavePath, err)
	}

	return master, slave, nil
}

// setWinSize mengatur ukuran layar PTY supaya output program terformat benar.
func setWinSize(f *os.File, rows, cols uint16) {
	ws := struct {
		Row, Col, Xpixel, Ypixel uint16
	}{Row: rows, Col: cols}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), termIoctlSetWinSz, uintptr(unsafe.Pointer(&ws)))
}

// findShell memilih shell terbaik yang tersedia di sistem.
func findShell() string {
	for _, sh := range []string{"/bin/bash", "/usr/bin/bash", "/bin/sh", "/usr/bin/sh"} {
		if _, err := os.Stat(sh); err == nil {
			return sh
		}
	}
	return "/bin/sh"
}

// termStartDir menentukan direktori awal sesi terminal.
func termStartDir() string {
	home := getHomeDir()
	if home != "" {
		if st, err := os.Stat(home); err == nil && st.IsDir() {
			return home
		}
	}
	if st, err := os.Stat("/root"); err == nil && st.IsDir() {
		return "/root"
	}
	return "/"
}

// ==============================================================================
// 🚀 SIKLUS HIDUP SESI
// ==============================================================================

// getOrCreateSession mengembalikan sesi milik admin, membuatnya bila belum ada.
func getOrCreateSession(userID int64) (*TermSession, error) {
	termSessionsMu.Lock()

	if s, ok := termSessions[userID]; ok {
		// Sesi ada tapi prosesnya sudah mati -> buang, buat baru.
		if s.Alive && s.Cmd != nil && s.Cmd.Process != nil {
			if err := s.Cmd.Process.Signal(syscall.Signal(0)); err == nil {
				s.LastUsed = time.Now()
				termSessionsMu.Unlock()
				return s, nil
			}
		}
		killSessionLocked(userID)
	}

	// Batas total sesi: tolak pembuatan baru bila sudah penuh.
	if len(termSessions) >= termMaxSessions {
		termSessionsMu.Unlock()
		return nil, fmt.Errorf("batas maksimal %d sesi terminal tercapai. Tutup sesi lama dulu dengan /term kill", termMaxSessions)
	}

	master, slave, err := openPTY()
	if err != nil {
		termSessionsMu.Unlock()
		return nil, err
	}

	shell := findShell()
	cmd := exec.Command(shell, "-i")
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.Dir = termStartDir()
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"PS1=\\u@\\h:\\w\\$ ",
		"HISTFILE=",       // jangan cemari history user
		"PROMPT_COMMAND=", // hindari hook prompt yang mengganggu parsing
		"BOT_TERMINAL=1",
	)

	// Setsid + Setctty: shell jadi pemimpin sesi dan PTY jadi controlling tty.
	// Ctty: 0 menunjuk ke fd ke-0 dari proses anak (yaitu slave).
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}

	if err := cmd.Start(); err != nil {
		master.Close()
		slave.Close()
		termSessionsMu.Unlock()
		return nil, fmt.Errorf("gagal menjalankan shell %s: %w", shell, err)
	}
	_ = slave.Close() // parent hanya butuh master

	setWinSize(master, 40, 120)

	s := &TermSession{
		Master:    master,
		Cmd:       cmd,
		Shell:     shell,
		StartedAt: time.Now(),
		LastUsed:  time.Now(),
		Alive:     true,
	}

	termSessions[userID] = s
	termSessionsMu.Unlock()

	// Pembaca latar: terus menyerap output ke buffer layar.
	// Tanpa ini, buffer PTY penuh dan shell membeku.
	go readLoop(s)

	// Beri waktu shell mencetak prompt awal.
	time.Sleep(400 * time.Millisecond)

	return s, nil
}

// readLoop menyerap output PTY secara terus-menerus ke buffer layar sesi.
func readLoop(s *TermSession) {
	buf := make([]byte, 4096)
	for {
		s.Mu.Lock()
		alive := s.Alive
		master := s.Master
		s.Mu.Unlock()
		if !alive || master == nil {
			return
		}

		_ = master.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, err := master.Read(buf)
		if n > 0 {
			s.Mu.Lock()
			s.Screen = append(s.Screen, buf[:n]...)
			// Jaga agar buffer tidak tumbuh tanpa batas.
			if len(s.Screen) > termScreenCap {
				s.Screen = s.Screen[len(s.Screen)-termScreenCap:]
			}
			s.Mu.Unlock()
		}
		if err != nil {
			if os.IsTimeout(err) {
				continue
			}
			// EOF atau error fatal: proses sudah mati.
			s.Mu.Lock()
			s.Alive = false
			s.Mu.Unlock()
			return
		}
	}
}

// killSessionLocked menutup sesi; pemanggil WAJIB memegang termSessionsMu.
//
// Strategi pembunuhan — aman terhadap PID recycling:
//
//  1. Setiap job di `bash -i` punya PROCESS GROUP sendiri (terbukti lewat
//     pengukuran: shell pgid=321240, job latar pgid=321248). Jadi membunuh
//     process group shell saja TIDAK cukup.
//  2. Membunuh lewat penelusuran /proc global BERBAHAYA — PID di-recycle dan
//     kita bisa mengenai proses milik sesi lain. Jangan lakukan itu.
//  3. Karena itu: bunuh process group shell, lalu untuk setiap job yang
//     dilaporkan shell, bunuh PGID-nya masing-masing.
//
// Hasilnya: hanya proses yang benar-benar milik sesi ini yang terpengaruh.
func killSessionLocked(userID int64) {
	s, ok := termSessions[userID]
	if !ok {
		return
	}
	s.Mu.Lock()
	s.Alive = false
	cmd := s.Cmd
	master := s.Master
	s.Mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		pid := cmd.Process.Pid

		// Process group milik setiap job DIBACA SEBELUM ada yang dibunuh —
		// sesudah shell mati, informasi ini hilang selamanya.
		jobGroups := sessionJobGroups(pid)

		// Fase 1: SIGHUP ke seluruh process group shell (cara sopan).
		_ = syscall.Kill(-pid, syscall.SIGHUP)
		for _, pg := range jobGroups {
			_ = syscall.Kill(-pg, syscall.SIGHUP)
		}

		// Beri waktu shell menutup diri secara bersih.
		for i := 0; i < 20; i++ {
			time.Sleep(25 * time.Millisecond)
			if err := syscall.Kill(pid, 0); err != nil {
				break
			}
		}

		// Fase 2: SIGKILL ke setiap process group job, lalu group shell.
		for _, pg := range jobGroups {
			if pg > 0 && pg != pid {
				_ = syscall.Kill(-pg, syscall.SIGKILL)
			}
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
	}

	if master != nil {
		_ = master.Close()
	}
	delete(termSessions, userID)
}

// sessionJobGroups mengumpulkan process group dari semua proses yang masih
// berada di dalam SESSION milik shell ini.
//
// Inilah pemisah aman: kita tidak menelusuri /proc secara buta, tapi memfilter
// berdasarkan SESSION ID (sid). Setiap proses dalam satu sesi PTY berbagi sid
// yang sama dengan shell pemimpinnya, sehingga proses milik sesi lain tidak
// akan pernah ikut terambil — walau PID-nya di-recycle.
func sessionJobGroups(shellPid int) []int {
	sid := sessionIDOf(shellPid)
	if sid <= 0 {
		return nil
	}

	seen := map[int]bool{}
	var groups []int

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		st, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}

		// Fields setelah ')' terakhir: [0]=state [1]=ppid [2]=pgrp [3]=session
		s := string(st)
		rp := strings.LastIndex(s, ")")
		if rp < 0 || rp+2 >= len(s) {
			continue
		}
		f := strings.Fields(s[rp+2:])
		if len(f) < 4 {
			continue
		}

		// FILTER KUNCI: hanya proses dengan session ID yang sama.
		procSid, err := strconv.Atoi(f[3])
		if err != nil || procSid != sid {
			continue
		}

		pgrp, err := strconv.Atoi(f[2])
		if err != nil || pgrp <= 0 {
			continue
		}
		if !seen[pgrp] {
			seen[pgrp] = true
			groups = append(groups, pgrp)
		}
	}
	return groups
}

// sessionIDOf membaca session ID (sid) dari sebuah proses.
func sessionIDOf(pid int) int {
	st, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return -1
	}
	s := string(st)
	rp := strings.LastIndex(s, ")")
	if rp < 0 || rp+2 >= len(s) {
		return -1
	}
	f := strings.Fields(s[rp+2:])
	if len(f) < 4 {
		return -1
	}
	sid, err := strconv.Atoi(f[3])
	if err != nil {
		return -1
	}
	return sid
}

// collectDescendants mengumpulkan seluruh PID turunan dari sebuah proses.
//
// PERINGATAN: fungsi ini hanya untuk DIAGNOSA/LOG. Jangan pakai hasilnya
// sebagai daftar target SIGKILL — PID di Linux di-recycle, dan menelusuri
// /proc secara global bisa mengenai proses milik sesi lain.
// Pembunuhan sesi memakai process group + filter session ID (sessionJobGroups).
func collectDescendants(root int) []int {
	out := []int{}
	seen := map[int]bool{root: true}
	// Beberapa iterasi karena kedalaman pohon tidak diketahui;
	// proses di Linux sangat jarang melebihi 5 level untuk kasus ini.
	frontier := []int{root}
	for depth := 0; depth < 8 && len(frontier) > 0; depth++ {
		next := []int{}
		for _, parent := range frontier {
			for _, child := range childrenOf(parent) {
				if !seen[child] {
					seen[child] = true
					out = append(out, child)
					next = append(next, child)
				}
			}
		}
		frontier = next
	}
	return out
}

// childrenOf membaca /proc untuk menemukan anak langsung dari sebuah PID.
// Lebih andal daripada memanggil ps, dan tidak butuh proses eksternal.
func childrenOf(ppid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		// Format /proc/PID/stat:
		//   pid (comm) state ppid pgrp session tty_nr ...
		// comm bisa berisi spasi dan tanda kurung, jadi parsing dimulai
		// setelah ')' terakhir. Setelah itu fields[0]=state, [1]=ppid,
		// [2]=pgrp, [3]=session.
		s := string(stat)
		rp := strings.LastIndex(s, ")")
		if rp < 0 || rp+2 >= len(s) {
			continue
		}
		fields := strings.Fields(s[rp+2:])
		if len(fields) < 2 {
			continue
		}
		if p, err := strconv.Atoi(fields[1]); err == nil && p == ppid {
			out = append(out, pid)
		}
	}
	return out
}

// KillSession menutup sesi milik admin secara aman dari luar paket.
func KillSession(userID int64) bool {
	termSessionsMu.Lock()
	defer termSessionsMu.Unlock()
	if _, ok := termSessions[userID]; !ok {
		return false
	}
	killSessionLocked(userID)
	return true
}

// reapIdleSessions membunuh sesi yang menganggur terlalu lama.
// Mencegah shell hidup selamanya memakan RAM VPS.
func reapIdleSessions() {
	termSessionsMu.Lock()
	defer termSessionsMu.Unlock()
	now := time.Now()
	for uid, s := range termSessions {
		if now.Sub(s.LastUsed) > termIdleTimeout {
			killSessionLocked(uid)
		}
	}
}

// ==============================================================================
// ✍️ MENGIRIM INPUT KE SHELL
// ==============================================================================

// SendToTerminal menyuntik teks ke stdin shell yang sedang hidup.
func SendToTerminal(userID int64, input string) error {
	termSessionsMu.Lock()
	s, ok := termSessions[userID]
	if ok {
		s.LastUsed = time.Now()
	}
	termSessionsMu.Unlock()

	if !ok {
		return fmt.Errorf("tidak ada sesi terminal aktif")
	}

	s.Mu.Lock()
	alive := s.Alive
	master := s.Master
	if alive {
		s.Screen = s.Screen[:0] // bersihkan layar sebelum perintah baru
	}
	s.Mu.Unlock()

	if !alive || master == nil {
		return fmt.Errorf("sesi terminal sudah mati")
	}

	if _, err := master.WriteString(input); err != nil {
		return fmt.Errorf("gagal menulis ke PTY: %w", err)
	}
	return nil
}

// ==============================================================================
// 🔐 KONTROL SINYAL (Ctrl+C, dsb)
// ==============================================================================

// SendSignal mengirim sinyal ke foreground process group di dalam sesi.
func SendSignal(userID int64, sig syscall.Signal, label string) error {
	termSessionsMu.Lock()
	s, ok := termSessions[userID]
	termSessionsMu.Unlock()
	if !ok {
		return fmt.Errorf("tidak ada sesi terminal aktif")
	}

	s.Mu.Lock()
	cmd := s.Cmd
	alive := s.Alive
	s.Mu.Unlock()

	if !alive || cmd == nil || cmd.Process == nil {
		return fmt.Errorf("sesi terminal sudah mati")
	}

	pid := cmd.Process.Pid
	// Ke seluruh process group sesi, bukan hanya shell.
	if err := syscall.Kill(-pid, sig); err != nil {
		// Fallback: kirim ke proses utama.
		if err2 := cmd.Process.Signal(sig); err2 != nil {
			return fmt.Errorf("gagal mengirim %s: %v", label, err)
		}
	}
	return nil
}

// ==============================================================================
// 📖 MEMBACA SNAPSHOT LAYAR
// ==============================================================================

// Pola escape ANSI. Catatan penting: Go memakai mesin regexp RE2 yang
// TIDAK mendukung lookahead (?!) atau backreference. Jadi CR tunggal
// ditangani di cleanANSI, bukan di pola ini.
var ansiPattern = regexp.MustCompile(
	`\x1b\[[0-9;?]*[a-zA-Z]` + // CSI: warna, gerak kursor, erase
		`|\x1b\][^\x07\x1b]*(\x07|\x1b\\)` + // OSC: judul window, hyperlink
		`|\x1b[()][A-Z0-9]` + // charset selection
		`|\x1b[=>]` + // keypad modes
		`|\x1b\[[0-9;]*` + // CSI terpotong di ujung buffer
		`|[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`, // kontrol lain (tanpa \n, \t, \r)
)

// cleanANSI membuang escape sequence supaya output terbaca di Telegram.
func cleanANSI(s string) string {
	// CR tunggal (dipakai progress bar) dibuang; CRLF tetap jadi newline.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "")

	s = ansiPattern.ReplaceAllString(s, "")

	// Baris kosong berlebihan dirapikan.
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}

// ReadScreen mengembalikan snapshot output sesi yang sudah dibersihkan.
func ReadScreen(userID int64) (string, error) {
	termSessionsMu.Lock()
	s, ok := termSessions[userID]
	if ok {
		s.LastUsed = time.Now()
	}
	termSessionsMu.Unlock()
	if !ok {
		return "", fmt.Errorf("tidak ada sesi terminal aktif")
	}

	// Beri kesempatan output terbaru mengalir masuk.
	time.Sleep(termPollWindow)

	s.Mu.Lock()
	raw := string(s.Screen)
	alive := s.Alive
	s.Mu.Unlock()

	out := cleanANSI(raw)
	if out == "" && !alive {
		return "⚠️ <i>Shell sudah keluar. Ketik /term untuk memulai sesi baru.</i>", nil
	}
	return out, nil
}

// SessionInfo mengembalikan ringkasan status sesi.
func SessionInfo(userID int64) string {
	termSessionsMu.Lock()
	s, ok := termSessions[userID]
	termSessionsMu.Unlock()
	if !ok {
		return "tidak ada"
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	status := "mati"
	if s.Alive {
		status = "aktif"
	}
	pid := 0
	if s.Cmd != nil && s.Cmd.Process != nil {
		pid = s.Cmd.Process.Pid
	}
	return fmt.Sprintf("%s (shell %s, PID %d, umur %s)", status, s.Shell, pid, time.Since(s.StartedAt).Truncate(time.Second))
}

// ==============================================================================
// 🎛️ PEMBANTU FORMAT
// ==============================================================================

// termChunk membagi output jadi beberapa pesan Telegram (batas ~4096 karakter).
func termChunk(s string, size int) []string {
	runes := []rune(s)
	var out []string
	for i := 0; i < len(runes); i += size {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

// termShellQuote membungkus string agar aman sebagai argumen shell.
func termShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// termParseInt membaca angka dari callback dengan fallback aman.
func termParseInt(s string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return v
	}
	return def
}

var _ = termShellQuote // disediakan untuk pemakaian lanjutan
var _ = termParseInt

// ==============================================================================
// 📦 APT HELPER — apt update / upgrade lewat terminal
// ==============================================================================
//
// Kenapa butuh perlakuan khusus:
//
//  1. apt butuh ROOT. Bot yang jalan sebagai user biasa tidak bisa memakainya.
//     Kita deteksi ini lebih dulu dan beri pesan jelas, bukan gagal membingungkan.
//  2. apt bisa MENANYAKAN hal interaktif (debconf: konflik config, restart
//     layanan). Ini justru keunggulan terminal PTY: prompt-nya TAMPIL di layar
//     dan bisa dijawab lewat Telegram. `/exec` tidak bisa melakukan ini.
//  3. apt MEMEGANG LOCK (/var/lib/dpkg/lock). Kalau unattended-upgrades sedang
//     jalan, apt akan gagal dengan pesan lock. Kita beri tahu apa yang terjadi.
//  4. apt upgrade bisa BERJALAN LAMA (5-30 menit). Terminal PTY tidak punya
//     timeout seperti /exec (60 detik), jadi ini aman. Yang perlu dihindari
//     adalah menjalankan apt di dalam perintah yang menunggu selesai.
//
// Semua perintah di bawah mengembalikan CEPAT — pekerjaannya berjalan di dalam
// shell PTY, dan user memantau lewat /term.

// termHandleApt menjalankan operasi apt di dalam sesi terminal.
func termHandleApt(chatID int64, userID int64, sub string) {
	sub = strings.ToLower(strings.TrimSpace(sub))

	if _, err := getOrCreateSession(userID); err != nil {
		updatePanel(userID, chatID, "❌ <b>Gagal membuka terminal:</b>\n<pre>"+
			html.EscapeString(err.Error())+"</pre>", backToTerminalKeyboard())
		return
	}

	// Peringatan hak akses digabung ke panel — tidak lagi jadi pesan terpisah.
	rootWarning := ""
	if !termIsRoot() {
		rootWarning = "⚠️ <b>Bot tidak berjalan sebagai root.</b> apt butuh root atau sudo.\n\n"
	}

	var cmd string
	switch sub {
	case "", "help", "?":
		updatePanel(userID, chatID, rootWarning+
			"📦 <b>Bantuan APT lewat Terminal</b>\n"+
			"━━━━━━━━━━━━━━━━━━━━\n"+
			"• <code>/term apt check</code> — cek lock & proses apt (jalankan dulu)\n"+
			"• <code>/term apt update</code> — perbarui daftar paket\n"+
			"• <code>/term apt upgrade</code> — upgrade (menanyakan konfirmasi)\n"+
			"• <code>/term apt safe</code> — upgrade tanpa tanya (non-interaktif)\n"+
			"• <code>/term apt full</code> — upgrade + auto-hapus paket usang\n"+
			"• <code>/term apt list</code> — paket yang bisa di-upgrade\n"+
			"• <code>/term apt clean</code> — bersihkan cache paket\n\n"+
			"<b>Untuk jawab prompt debconf:</b> kirim jawabannya langsung "+
			"(mis. <code>y</code>), lalu <code>/term</code> untuk melihat hasilnya.",
			backToTerminalKeyboard())
		return

	case "check", "lock":
		// Cek apakah ada proses apt/dpkg lain yang memegang lock.
		if err := SendToTerminal(userID, termAptCheckScript()+"\n"); err != nil {
			updatePanel(userID, chatID, "❌ "+html.EscapeString(err.Error()), backToTerminalKeyboard())
			return
		}
		text, kb := renderTerminal(userID)
		updatePanel(userID, chatID, text, kb)
		return

	case "update":
		cmd = termAptPrefix() + "apt-get update"

	case "upgrade":
		// Tanpa -y: prompt konfirmasi akan TAMPIL dan bisa dijawab.
		// Ini pilihan sadar — user melihat paket apa yang akan berubah.
		cmd = termAptPrefix() + "apt-get upgrade"

	case "safe", "yes", "y":
		// Non-interaktif: debconf memakai nilai default, tidak ada pertanyaan.
		cmd = "DEBIAN_FRONTEND=noninteractive " + termAptPrefix() + "apt-get -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold upgrade"

	case "full", "full-upgrade", "dist":
		cmd = "DEBIAN_FRONTEND=noninteractive " + termAptPrefix() + "apt-get -y full-upgrade"

	case "list", "upgradable":
		cmd = "apt list --upgradable 2>/dev/null | head -50"

	case "clean":
		cmd = termAptPrefix() + "apt-get clean && " + termAptPrefix() + "apt-get autoclean && echo 'Cache dibersihkan.'"

	case "autoremove":
		cmd = "DEBIAN_FRONTEND=noninteractive " + termAptPrefix() + "apt-get -y autoremove"

	case "update-upgrade":
		// Rantai lengkap dengan && sehingga berhenti bila update gagal.
		cmd = termAptPrefix() + "apt-get update && " + termAptPrefix() + "apt-get upgrade"

	default:
		updatePanel(userID, chatID, "⚠️ Sub-perintah apt tidak dikenal: <code>"+
			html.EscapeString(sub)+"</code>\n\nKetik <code>/term apt</code> untuk bantuan.",
			backToTerminalKeyboard())
		return
	}

	if err := SendToTerminal(userID, cmd+"\n"); err != nil {
		updatePanel(userID, chatID, "❌ "+html.EscapeString(err.Error()), backToTerminalKeyboard())
		return
	}

	// Satu panel menampilkan: peringatan (bila ada), perintah yang jalan, dan hasil.
	// Tidak ada lagi 3 pesan terpisah untuk satu perintah apt.
	time.Sleep(3 * time.Second)
	body, kb := renderTerminal(userID)
	updatePanel(userID, chatID, rootWarning+
		"📦 <i>Menjalankan:</i> <code>"+html.EscapeString(cmd)+"</code>\n\n"+body, kb)
}

// termIsRoot melaporkan apakah bot berjalan dengan hak root.
func termIsRoot() bool {
	return os.Geteuid() == 0
}

// termAptPrefix mengembalikan prefix yang diperlukan sebelum perintah apt.
// Bila sudah root, tidak ada prefix. Bila bukan root tapi sudo tersedia,
// pakai sudo -n (non-interaktif) agar tidak menggantung menunggu password.
func termAptPrefix() string {
	if termIsRoot() {
		return ""
	}
	// -n berarti: jangan menanyakan password. Kalau butuh, langsung gagal
	// dengan pesan jelas alih-alih menggantung di dalam PTY.
	return "sudo -n "
}

// termAptCheckScript memeriksa apakah apt sedang dikunci proses lain.
func termAptCheckScript() string {
	return `echo '--- Status APT ---'; ` +
		`if pgrep -x apt >/dev/null || pgrep -x apt-get >/dev/null || pgrep -x dpkg >/dev/null || pgrep -x unattended-upgr >/dev/null; then ` +
		`echo 'LOCK: ada proses apt/dpkg lain yang berjalan:'; ps -eo pid,comm | grep -E 'apt|dpkg|unattended' | grep -v grep; ` +
		`else echo 'LOCK: tidak ada proses apt lain. Aman untuk lanjut.'; fi; ` +
		`echo "USER: $(id -un) (uid=$(id -u))"; ` +
		`if [ "$(id -u)" != 0 ]; then echo 'PERINGATAN: bukan root. apt butuh root atau sudo.'; fi`
}

// ClearScreen mengosongkan buffer layar sesi, berguna sebelum perintah panjang.
func ClearScreen(userID int64) error {
	termSessionsMu.Lock()
	s, ok := termSessions[userID]
	termSessionsMu.Unlock()
	if !ok {
		return fmt.Errorf("tidak ada sesi terminal aktif")
	}
	s.Mu.Lock()
	s.Screen = s.Screen[:0]
	s.Mu.Unlock()
	return nil
}

// ==============================================================================
// 📋 BANTUAN (/help)
// ==============================================================================
//
// Prinsip penyusunan bantuan:
//   - Hanya perintah yang benar-benar sering dipakai yang ditampilkan di /help.
//   - Perintah yang hanya teks panduan (/upload), duplikat tombol UI (/getfile),
//     atau sudah tercakup terminal (/cat, /mkdir) tidak ditampilkan.
//   - Perintah lengkap tetap tersedia lewat /commands bila diperlukan.

// helpText mengembalikan bantuan utama — ringkas, hanya yang penting.
func helpText() string {
	return "🤖 <b>Bot VPS Control</b>\n" +
		"━━━━━━━━━━━━━━━━━━━━\n\n" +
		"🖥️ <b>Terminal Interaktif</b> <i>(fitur utama)</i>\n" +
		"• <code>/term</code> — buka shell persisten (TTY asli)\n" +
		"• <code>/term &lt;perintah&gt;</code> — jalankan perintah\n" +
		"• <code>/term apt check|update|upgrade</code> — kelola paket\n" +
		"• <code>/term ^c</code> — batalkan proses yang berjalan\n\n" +
		"Setelah sesi terbuka, kirim perintah <b>tanpa</b> garis miring:" +
		" <code>cd /var</code>, <code>ls -la</code>, <code>top</code>\n\n" +
		"📁 <b>File Manager</b>\n" +
		"• <code>/fm [path]</code> — jelajah file (tombol interaktif)\n" +
		"• <code>/rm &lt;path&gt;</code> — hapus file/folder\n\n" +
		"📊 <b>Monitor Server</b>\n" +
		"• <code>/stats</code> — CPU, RAM, Disk, Uptime\n" +
		"• <code>/top</code> — 10 proses terberat\n" +
		"• <code>/sysinfo</code> — spesifikasi hardware & OS\n" +
		"• <code>/net</code> — port terbuka\n" +
		"• <code>/ip</code> — IP publik, lokal, Tailscale\n\n" +
		"📤 <b>Upload &amp; Unduh</b>\n" +
		"• <code>/fm</code> → tombol 📤 Upload ke Sini (maks 20MB)\n" +
		"• <code>/unduh &lt;URL&gt;</code> — unduh file besar langsung di VPS\n" +
		"• <code>/chunk</code> — upload bertahap (file &gt;20MB)\n\n" +
		"🦎 <b>VPN</b>\n" +
		"• <code>/ts</code> — menu Tailscale (tombol interaktif)\n\n" +
		"⚙️ <b>Lainnya</b>\n" +
		"• <code>/exec &lt;cmd&gt;</code> — perintah sekali jalan\n" +
		"• <code>/ping</code> — cek bot hidup\n" +
		"• <code>/update</code> — pasang versi terbaru (otomatis pilih cara)\n" +
		"• <code>/reboot confirm</code> — restart VPS\n\n" +
		"<i>Perintah lengkap: /commands</i>"
}

// helpKeyboard menyediakan tombol akses cepat ke fitur yang paling sering dipakai.
func helpKeyboard() *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "🖥️ Terminal", CallbackData: "hp:t"},
				{Text: "📁 File Manager", CallbackData: "hp:f"},
			},
			{
				{Text: "📊 Stats", CallbackData: "hp:s"},
				{Text: "⚙️ Sysinfo", CallbackData: "hp:y"},
			},
			{
				{Text: "🦎 Tailscale", CallbackData: "hp:x"},
				{Text: "📦 Upload Besar", CallbackData: "hp:u"},
			},
			{
				{Text: "📋 Semua Perintah", CallbackData: "hp:a"},
			},
		},
	}
}

// commandsPlainText mengembalikan daftar perintah lengkap — untuk yang butuh detail.
func commandsPlainText() string {
	return "📋 <b>Daftar Perintah Lengkap</b>\n" +
		"━━━━━━━━━━━━━━━━━━━━\n\n" +
		"🖥️ <b>Terminal</b>\n" +
		"<code>/term</code> — buka/buat sesi shell persisten\n" +
		"<code>/term &lt;cmd&gt;</code> — jalankan perintah di sesi\n" +
		"<code>/term mode on|off</code> — input langsung tanpa garis miring\n" +
		"<code>/term ^c | ^d | ^z</code> — kirim Ctrl+C / Ctrl+D / Ctrl+Z\n" +
		"<code>/term apt check</code> — cek lock apt & hak root\n" +
		"<code>/term apt update</code> — perbarui daftar paket\n" +
		"<code>/term apt upgrade</code> — upgrade dengan konfirmasi\n" +
		"<code>/term apt safe</code> — upgrade non-interaktif\n" +
		"<code>/term apt full</code> — full-upgrade\n" +
		"<code>/term apt list</code> — paket yang bisa di-upgrade\n" +
		"<code>/term apt clean</code> — bersihkan cache\n" +
		"<code>/term log</code> — kirim log sesi sebagai file\n" +
		"<code>/term clear</code> — bersihkan buffer layar\n" +
		"<code>/term info</code> — status sesi\n" +
		"<code>/term kill</code> — tutup sesi\n\n" +
		"📁 <b>File</b>\n" +
		"<code>/fm [path]</code> — file manager interaktif\n" +
		"<code>/cat &lt;path&gt;</code> — lihat isi file teks\n" +
		"<code>/mkdir &lt;path&gt;</code> — buat folder\n" +
		"<code>/rm &lt;path&gt;</code> — hapus file/folder\n" +
		"<code>/getfile &lt;path&gt;</code> — unduh file ke chat\n" +
		"<i>Upload: kirim file + caption berisi path tujuan</i>\n\n" +
		"📊 <b>Monitor</b>\n" +
		"<code>/stats</code> <code>/top</code> <code>/sysinfo</code> <code>/net</code> <code>/ip</code> <code>/ping</code>\n\n" +
		"📤 <b>Upload &amp; Unduh</b>\n" +
		"<code>/unduh &lt;URL&gt; [tujuan]</code> — unduh file besar di VPS (tanpa batas 20MB)\n" +
		"<code>/chunk mulai &lt;nama&gt; &lt;jumlah&gt;</code> — mulai upload bertahap\n" +
		"<code>/chunk status</code> — cek kemajuan upload bertahap\n" +
		"<code>/chunk batal</code> — batalkan upload bertahap\n" +
		"<code>/pecah &lt;file&gt; [MB]</code> — pecah file besar\n" +
		"<code>/gabung &lt;bagian&gt; &lt;hasil&gt;</code> — gabungkan kembali\n" +
		"<i>Upload &lt;20MB: kirim file + caption, atau tombol 📤 di /fm</i>\n\n" +
		"🦎 <b>Tailscale (menu tombol)</b>\n" +
		"<code>/ts</code> — buka menu interaktif (install, connect, status, ssh)\n" +
		"<code>/ts status</code> — status ringkas + tombol\n" +
		"<code>/ts status raw</code> — status lengkap sebagai file\n" +
		"<code>/ts install</code> — install otomatis\n" +
		"<code>/ts up</code> — hubungkan via Web Login (aman)\n" +
		"<code>/ts ip</code> — IP Tailscale & hostname\n" +
		"<code>/ts ssh on|off</code> — aktif/nonaktif Tailscale SSH\n" +
		"<code>/ts down confirm</code> — putuskan (butuh konfirmasi)\n\n" +
		"⚙️ <b>Kontrol</b>\n" +
		"<code>/exec &lt;cmd&gt;</code> — perintah sekali jalan (tanpa state)\n" +
		"<code>/reboot confirm</code> — restart VPS"
}
