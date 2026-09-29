package term

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
	"bot-vps-control/internal/shell"
	"bot-vps-control/internal/tg"
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

// Mode input langsung: admin mengetik perintah di chat dan hasilnya
// dikirim sebagai pesan baru, bukan ditulis ke panel.
var (
	termDirectMu   sync.Mutex
	termDirectMode = make(map[int64]bool)
)

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
	home := shell.GetHomeDir()
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
func GetOrCreateSession(userID int64) (*TermSession, error) {
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
func ReapIdleSessions() {
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
// KeySeq memetakan callback tombol terminal ke urutan kunci ANSI yang
// ditulis apa adanya ke PTY.
//
// Newline SENGAJA tidak ikut: tombol ini mengedit baris perintah yang sedang
// aktif (pindah kursor, riwayat, pelengkapan), bukan menjalankannya.
//
// Batas yang perlu diketahui: ini mengendalikan shell readline — riwayat,
// gerak kursor, Tab, Esc. TUI layar penuh (nano, top, htop) tetap butuh
// terminal sungguhan: ReadScreen membuang escape sequence dan tampilan
// dipotong 3400 karakter di Telegram.
func KeySeq(action string) (string, bool) {
	seq, ok := keySeqs[action]
	return seq, ok
}

var keySeqs = map[string]string{
	"u":    "\x1b[A", // panah atas — riwayat sebelumnya
	"d":    "\x1b[B", // panah bawah
	"rt":   "\x1b[C", // panah kanan — maju satu karakter
	"l":    "\x1b[D", // panah kiri
	"home": "\x1b[H", // awal baris
	"end":  "\x1b[F", // akhir baris
	"t":    "\t",     // Tab — pelengkapan perintah
	"esc":  "\x1b",   // Esc — batalkan/mode vi
	"en":   "\r",     // Enter — jalankan perintah
}

// SendRawKey menulis urutan kunci apa adanya ke PTY tanpa menambah newline
// dan tanpa membersihkan layar — dipakai tombol panah/Tab/Esc, di mana
// newline akan merusak baris perintah yang sedang diedit.
func SendRawKey(userID int64, seq string) error {
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
	s.Mu.Unlock()
	if !alive || master == nil {
		return fmt.Errorf("sesi terminal sudah mati")
	}

	if _, err := master.WriteString(seq); err != nil {
		return fmt.Errorf("gagal menulis ke PTY: %w", err)
	}
	return nil
}

// SendToTerminal menulis input ke PTY dan membersihkan layar tampilan
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
func TermHandleApt(chatID int64, userID int64, sub string) {
	sub = strings.ToLower(strings.TrimSpace(sub))

	if _, err := GetOrCreateSession(userID); err != nil {
		tg.UpdatePanel(userID, chatID, "❌ <b>Gagal membuka terminal:</b>\n<pre>"+
			html.EscapeString(err.Error())+"</pre>", BackToTerminalKeyboard())
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
		tg.UpdatePanel(userID, chatID, rootWarning+
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
			BackToTerminalKeyboard())
		return

	case "check", "lock":
		// Cek apakah ada proses apt/dpkg lain yang memegang lock.
		if err := SendToTerminal(userID, termAptCheckScript()+"\n"); err != nil {
			tg.UpdatePanel(userID, chatID, "❌ "+html.EscapeString(err.Error()), BackToTerminalKeyboard())
			return
		}
		text, kb := RenderTerminal(userID)
		tg.UpdatePanel(userID, chatID, text, kb)
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
		tg.UpdatePanel(userID, chatID, "⚠️ Sub-perintah apt tidak dikenal: <code>"+
			html.EscapeString(sub)+"</code>\n\nKetik <code>/term apt</code> untuk bantuan.",
			BackToTerminalKeyboard())
		return
	}

	if err := SendToTerminal(userID, cmd+"\n"); err != nil {
		tg.UpdatePanel(userID, chatID, "❌ "+html.EscapeString(err.Error()), BackToTerminalKeyboard())
		return
	}

	// Satu panel menampilkan: peringatan (bila ada), perintah yang jalan, dan hasil.
	// Tidak ada lagi 3 pesan terpisah untuk satu perintah apt.
	time.Sleep(3 * time.Second)
	body, kb := RenderTerminal(userID)
	tg.UpdatePanel(userID, chatID, rootWarning+
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
func HelpText() string {
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
		"• <code>/rm &lt;path&gt;</code> — hapus file/folder\n" +
		"• <code>/bersih</code> — bersihkan chat & proyek\n\n" +
		"📊 <b>Monitor Server</b>\n" +
		"• <code>/stats</code> — CPU, RAM, Disk, Uptime\n" +
		"• <code>/top</code> — 10 proses terberat\n" +
		"• <code>/sysinfo</code> — spesifikasi hardware & OS\n" +
		"• <code>/net</code> — port terbuka\n" +
		"• <code>/ip</code> — IP publik, lokal, Tailscale\n\n" +
		"📤 <b>Upload &amp; Unduh</b>\n" +
		"• <code>/fm</code> → tombol 📤 Upload ke Sini (maks 20MB)\n" +
		"• <code>/unduh &lt;URL&gt;</code> — unduh file besar di VPS\n" +
		"• 📦 Upload Besar — tombol untuk file &gt;20MB (<code>/chunk</code>)\n\n" +
		"🚀 <b>Deploy Bot</b> <i>(jalankan bot lain di sini)</i>\n" +
		"• <code>/deploy</code> — panel deploy per-proyek\n" +
		"• Python · Node · <b>Go</b> · <b>Binary</b> · Shell, otomatis\n" +
		"• Sumber: berkas, URL, atau GitHub\n\n" +
		"🦎 <b>VPN</b>\n" +
		"• <code>/ts</code> — menu Tailscale (tombol interaktif)\n\n" +
		"⚙️ <b>Lainnya</b>\n" +
		"• <code>/exec &lt;cmd&gt;</code> — perintah sekali jalan\n" +
		"• <code>/ping</code> — cek bot hidup\n" +
		"• <code>/update</code> — pasang versi terbaru (otomatis)\n" +
		"• <code>/reboot confirm</code> — restart VPS\n\n" +
		"<i>Perintah lengkap: /commands</i>"
}

// helpKeyboard menyediakan tombol akses cepat ke fitur yang paling sering dipakai.
func HelpKeyboard() *tg.InlineKeyboardMarkup {
	return &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
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
				{Text: "🚀 Deploy Bot", CallbackData: "hp:d"},
			},
			{
				{Text: "📋 Semua Perintah", CallbackData: "hp:a"},
			},
		},
	}
}

// commandsPlainText mengembalikan daftar perintah lengkap — untuk yang butuh detail.
func CommandsPlainText() string {
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
		"<code>/bersih</code> — hapus semua proyek + log/pid + pesan bot\n" +
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
		"🚀 <b>Deploy Bot</b> <i>(menu tombol)</i>\n" +
		"<code>/deploy</code> — buka panel deploy per-proyek\n" +
		"<i>Bahasa: Python · Node.js · Go · Binary · Shell (otomatis)</i>\n" +
		"<i>Sumber: kirim berkas, URL, atau pemilik/repo GitHub</i>\n" +
		"<i>Kontrol: Start · Stop · Restart · Log · Bahasa · Entry Point · Hapus</i>\n\n" +
		"⚙️ <b>Kontrol</b>\n" +
		"<code>/exec &lt;cmd&gt;</code> — perintah sekali jalan (tanpa state)\n" +
		"<code>/reboot confirm</code> — restart VPS"
}

// ==============================================================================
// 🖥️ TERMINAL UI RENDERER (Level 3 — PTY Interaktif)
// ==============================================================================

// renderTerminal menggambar panel terminal dengan tombol kontrol.
func RenderTerminal(userID int64) (string, *tg.InlineKeyboardMarkup) {
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

	kb := &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{
				{Text: "⬆️", CallbackData: "tm:u"},
				{Text: "⬇️", CallbackData: "tm:d"},
				{Text: "🔄", CallbackData: "tm:r"},
				{Text: "🛑 ^C", CallbackData: "tm:c"},
			},
			{
				{Text: "⬅️", CallbackData: "tm:l"},
				{Text: "➡️", CallbackData: "tm:rt"},
				{Text: "📋 Info", CallbackData: "tm:i"},
				{Text: "💀 Tutup", CallbackData: "tm:k"},
			},
			{
				{Text: "Tab ⇥", CallbackData: "tm:t"},
				{Text: "Enter ⏎", CallbackData: "tm:en"},
				{Text: "Esc", CallbackData: "tm:esc"},
				{Text: "🏠", CallbackData: "tm:h"},
			},
		},
	}
	return b.String(), kb
}

// backToTerminalKeyboard menyediakan tombol kembali ke panel terminal.
func BackToTerminalKeyboard() *tg.InlineKeyboardMarkup {
	return &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{
				{Text: "⬅️ Kembali ke Terminal", CallbackData: "tm:r"},
			},
		},
	}
}

// terminalHelpText mengembalikan panduan penggunaan terminal.
func TerminalHelpText() string {
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
		"<b>Tombol kunci:</b>\n" +
		"• ⬆️ ⬇️ — riwayat perintah (seperti panah atas/bawah)\n" +
		"• ⬅️ ➡️ — geser kursor di baris perintah\n" +
		"• <code>Tab</code> — pelengkapan nama perintah/path\n" +
		"• <code>Enter</code> — jalankan perintah yang sedang ditulis\n" +
		"• <code>Esc</code> — batalkan (atau keluar mode vi)\n" +
		"• <code>^C</code> — hentikan proses di depan\n" +
		"• 🔄 — gambar ulang layar\n\n" +
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
func HandleTerminalCommand(chatID int64, userID int64, rawText string) {
	fields := strings.Fields(rawText)

	// /term tanpa argumen -> buka atau tampilkan panel terminal
	if len(fields) < 2 {
		if _, err := GetOrCreateSession(userID); err != nil {
			tg.SendOnce(userID, chatID, fmt.Sprintf("❌ <b>Gagal membuka terminal:</b>\n<pre>%s</pre>\n\n"+
				"<i>Pastikan /dev/pts ter-mount dan bot berjalan sebagai user yang berhak.</i>",
				html.EscapeString(err.Error())))
			return
		}
		// Panel yang sama dipakai ulang — /term berulang tidak menumpuk pesan.
		text, kb := RenderTerminal(userID)
		tg.UpdatePanel(userID, chatID, text, kb)
		return
	}

	sub := strings.ToLower(fields[1])
	// Argumen perintah asli (case dipertahankan)
	argRaw := strings.TrimSpace(strings.TrimPrefix(rawText, fields[0]))
	argRaw = strings.TrimSpace(strings.TrimPrefix(argRaw, fields[1]))

	switch sub {
	case "help", "?":
		// Bantuan terminal TIDAK lagi jadi pesan baru — masuk ke panel.
		tg.UpdatePanel(userID, chatID, TerminalHelpText(), BackToTerminalKeyboard())

	case "info", "status":
		if _, err := GetOrCreateSession(userID); err != nil {
			tg.UpdatePanel(userID, chatID, "❌ <i>Tidak ada sesi terminal:</i> "+
				html.EscapeString(err.Error()), BackToTerminalKeyboard())
			return
		}
		tg.UpdatePanel(userID, chatID,
			"📋 <b>Status Sesi:</b>\n<code>"+html.EscapeString(SessionInfo(userID))+"</code>\n\n"+
				"<i>Ketik /term untuk kembali ke panel terminal.</i>",
			BackToTerminalKeyboard())

	case "kill", "close", "exit":
		if KillSession(userID) {
			tg.UpdatePanel(userID, chatID,
				"💀 <b>Sesi terminal ditutup.</b>\n\nKetik <code>/term</code> untuk memulai sesi baru.", nil)
			tg.ClearPanel(userID)
		} else {
			tg.UpdatePanel(userID, chatID,
				"⚠️ <i>Tidak ada sesi terminal yang aktif.</i>\n\nKetik <code>/term</code> untuk membuat sesi.",
				BackToTerminalKeyboard())
		}

	case "log":
		out, err := ReadScreen(userID)
		if err != nil {
			tg.UpdatePanel(userID, chatID, "❌ "+html.EscapeString(err.Error()), BackToTerminalKeyboard())
			return
		}
		if out == "" {
			tg.UpdatePanel(userID, chatID, "📄 <i>Buffer terminal kosong.</i>", BackToTerminalKeyboard())
			return
		}
		// Log dikirim sebagai FILE — tidak mengotori chat dengan teks raksasa.
		fname := fmt.Sprintf("terminal_log_%d.txt", time.Now().Unix())
		_ = tg.SendTelegramDocument(chatID, fname, []byte(out), "📄 <i>Log penuh sesi terminal</i>")

	case "apt":
		TermHandleApt(chatID, userID, argRaw)

	case "clear", "cls":
		if err := ClearScreen(userID); err != nil {
			tg.UpdatePanel(userID, chatID, "⚠️ "+html.EscapeString(err.Error()), BackToTerminalKeyboard())
		} else {
			// Sungguhan membersihkan layar PTY, bukan cuma buffer bot.
			_ = SendToTerminal(userID, "clear\n")
			text, kb := RenderTerminal(userID)
			tg.UpdatePanel(userID, chatID, text, kb)
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
			tg.UpdatePanel(userID, chatID, "✅ <b>Mode input langsung AKTIF.</b>\n\n"+
				"Kirim teks apa pun ke chat dan teks itu masuk ke stdin shell.\n"+
				"Matikan dengan <code>/term mode off</code>.\n\n"+
				"⚠️ <i>Perintah yang diawali / tetap diproses sebagai perintah bot.</i>",
				BackToTerminalKeyboard())
		case "off":
			termDirectMu.Lock()
			delete(termDirectMode, userID)
			termDirectMu.Unlock()
			tg.UpdatePanel(userID, chatID, "🔕 <b>Mode input langsung NONAKTIF.</b>\n\n"+
				"Gunakan <code>/term &lt;perintah&gt;</code> untuk mengirim perintah.",
				BackToTerminalKeyboard())
		default:
			status := "nonaktif"
			termDirectMu.Lock()
			if termDirectMode[userID] {
				status = "aktif"
			}
			termDirectMu.Unlock()
			tg.UpdatePanel(userID, chatID,
				fmt.Sprintf("ℹ️ Mode input langsung: <b>%s</b>\n\nGunakan <code>/term mode on|off</code>.", status),
				BackToTerminalKeyboard())
		}

	case "^c", "ctrl-c", "ctrlc":
		TermHandleSignal(chatID, userID, syscall.SIGINT, "Ctrl+C (SIGINT)")

	case "^d", "ctrl-d", "ctrld":
		TermHandleSignal(chatID, userID, syscall.SIGHUP, "Ctrl+D / EOF")

	case "^z", "ctrl-z", "ctrlz":
		TermHandleSignal(chatID, userID, syscall.SIGTSTP, "Ctrl+Z (SIGTSTP)")

	default:
		// Sisa argumen diperlakukan sebagai perintah shell.
		cmdToSend := argRaw
		if cmdToSend == "" {
			cmdToSend = sub
		}
		TermExecAndShow(chatID, userID, cmdToSend)
	}
}

// termExecAndShow menyuntik satu perintah ke shell lalu menampilkan hasilnya.
func TermExecAndShow(chatID int64, userID int64, cmd string) {
	if _, err := GetOrCreateSession(userID); err != nil {
		tg.UpdatePanel(userID, chatID,
			fmt.Sprintf("❌ <b>Gagal membuka terminal:</b>\n<pre>%s</pre>", html.EscapeString(err.Error())),
			BackToTerminalKeyboard())
		return
	}

	// Kirim perintah + newline. Shell yang mengeksekusinya, bukan bot.
	if err := SendToTerminal(userID, cmd+"\n"); err != nil {
		tg.UpdatePanel(userID, chatID, "❌ "+html.EscapeString(err.Error()), BackToTerminalKeyboard())
		return
	}

	// Hasil masuk ke panel yang SAMA — chat tidak bertambah.
	text, kb := RenderTerminal(userID)
	tg.UpdatePanel(userID, chatID, text, kb)
}

// termHandleSignal mengirim sinyal ke proses foreground sesi lalu me-refresh panel.
func TermHandleSignal(chatID int64, userID int64, sig syscall.Signal, label string) {
	if err := SendSignal(userID, sig, label); err != nil {
		tg.UpdatePanel(userID, chatID, "⚠️ "+html.EscapeString(err.Error()), BackToTerminalKeyboard())
		return
	}
	text, kb := RenderTerminal(userID)
	tg.UpdatePanel(userID, chatID, text, kb)
}

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
	if _, err := GetOrCreateSession(userID); err != nil {
		tg.UpdatePanel(userID, chatID, "❌ "+html.EscapeString(err.Error()), BackToTerminalKeyboard())
		return
	}
	if err := SendToTerminal(userID, text+"\n"); err != nil {
		tg.UpdatePanel(userID, chatID, "❌ "+html.EscapeString(err.Error()), BackToTerminalKeyboard())
		return
	}
	// Tetap satu panel: setiap perintah menulis ulang pesan yang sama.
	text2, kb := RenderTerminal(userID)
	tg.UpdatePanel(userID, chatID, text2, kb)
}
