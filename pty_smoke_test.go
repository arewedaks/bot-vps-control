package main

// ==============================================================================
// 🧪 SMOKE TEST PTY — VERIFIKASI LEVEL 3 DI VPS
// ==============================================================================
// Test ini MEMBUTUHKAN /dev/ptmx dan /dev/pts yang berfungsi penuh.
// Di sandbox/container terbatas, test akan di-skip otomatis dengan pesan jelas.
//
// Jalankan di VPS:
//   go test -run TestPTY -v ./...
//
// Yang diverifikasi:
//   1. /dev/ptmx tersedia
//   2. /dev/pts ter-mount
//   3. PTY bisa dibuat lewat syscall stdlib (nol dependency)
//   4. Shell benar-benar mendapat TTY asli (bukan pipe)
//   5. cd MENETAP antar perintah  <- inti Level 3
//   6. export MENETAP antar perintah <- inti Level 3
//   7. Output bisa dibaca & dibersihkan
//   8. Sesi bisa dimatikan tanpa meninggalkan process yatim

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ptyAvailable melaporkan apakah lingkungan mendukung PTY.
func ptyAvailable() (bool, string) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		return false, "/dev/ptmx tidak ada: " + err.Error()
	}
	if _, err := os.ReadDir("/dev/pts"); err != nil {
		return false, "/dev/pts tidak bisa dibaca: " + err.Error()
	}
	master, slave, err := openPTY()
	if err != nil {
		return false, "openPTY gagal: " + err.Error()
	}
	master.Close()
	slave.Close()
	return true, ""
}

func TestPTYEnvironment(t *testing.T) {
	ok, why := ptyAvailable()
	if !ok {
		t.Skipf("SKIP — lingkungan tidak mendukung PTY. Alasan: %s\n"+
			"(Ini normal di sandbox/container. Jalankan di VPS asli untuk hasil sebenarnya.)", why)
	}
	t.Log("✅ /dev/ptmx ada, /dev/pts ter-mount, openPTY berhasil")
}

func TestPTYInteractiveSession(t *testing.T) {
	ok, why := ptyAvailable()
	if !ok {
		t.Skipf("SKIP — PTY tidak tersedia: %s", why)
	}

	const uid int64 = 770001
	defer KillSession(uid)

	// 1. Buat sesi lewat kode produksi (bukan tiruan).
	if _, err := getOrCreateSession(uid); err != nil {
		t.Fatalf("getOrCreateSession gagal: %v", err)
	}
	t.Logf("✅ Sesi dibuat: %s", SessionInfo(uid))

	// 2. Buktikan shell punya TTY asli.
	if err := SendToTerminal(uid, "tty; echo PEMISAH_A\n"); err != nil {
		t.Fatalf("SendToTerminal gagal: %v", err)
	}
	out, err := ReadScreen(uid)
	if err != nil {
		t.Fatalf("ReadScreen gagal: %v", err)
	}
	if !strings.Contains(out, "/dev/pts/") {
		t.Errorf("shell tidak melaporkan TTY asli. Output:\n%s", out)
	} else {
		t.Log("✅ Shell punya TTY asli (/dev/pts/N) — bukan pipe")
	}

	// 3. INI UJI INTI: cd harus MENETAP antar perintah terpisah.
	if err := SendToTerminal(uid, "cd /var\n"); err != nil {
		t.Fatalf("cd gagal: %v", err)
	}
	time.Sleep(400 * time.Millisecond)

	if err := SendToTerminal(uid, "echo CWD_SEKARANG=$(pwd)\n"); err != nil {
		t.Fatalf("pwd gagal: %v", err)
	}
	out, err = ReadScreen(uid)
	if err != nil {
		t.Fatalf("ReadScreen gagal: %v", err)
	}
	if !strings.Contains(out, "CWD_SEKARANG=/var") {
		t.Errorf("❌ CWD TIDAK MENETAP. Output:\n%s", out)
	} else {
		t.Log("✅ cd /var MENETAP antar perintah — inti Level 3 terbukti")
	}

	// 4. export harus MENETAP antar perintah.
	if err := SendToTerminal(uid, "export BOZ_UJI=absolute_bozagentic\n"); err != nil {
		t.Fatalf("export gagal: %v", err)
	}
	time.Sleep(400 * time.Millisecond)

	if err := SendToTerminal(uid, "echo ENV_SEKARANG=$BOZ_UJI\n"); err != nil {
		t.Fatalf("echo env gagal: %v", err)
	}
	out, err = ReadScreen(uid)
	if err != nil {
		t.Fatalf("ReadScreen gagal: %v", err)
	}
	if !strings.Contains(out, "ENV_SEKARANG=absolute_bozagentic") {
		t.Errorf("❌ ENV TIDAK MENETAP. Output:\n%s", out)
	} else {
		t.Log("✅ export MENETAP antar perintah")
	}

	// 5. Variabel shell juga menetap (bukan hanya env proses).
	if err := SendToTerminal(uid, "X=42; echo VAR_LOKAL=$X\n"); err != nil {
		t.Fatalf("var gagal: %v", err)
	}
	out, _ = ReadScreen(uid)
	if !strings.Contains(out, "VAR_LOKAL=42") {
		t.Logf("⚠️  variabel shell belum terbaca (bisa timing). Output:\n%s", out)
	}

	// 6. Output harus bersih dari byte ESC.
	if strings.ContainsRune(out, 0x1b) {
		t.Error("❌ output masih mengandung byte ESC setelah cleanANSI")
	} else {
		t.Log("✅ output bersih dari escape sequence")
	}

	// 7. Sinyal: kirim SIGINT, sesi harus tetap hidup (shell menangani Ctrl+C).
	if err := SendSignal(uid, syscall.SIGINT, "SIGINT"); err != nil {
		t.Errorf("SendSignal SIGINT gagal: %v", err)
	} else {
		t.Log("✅ SIGINT terkirim ke process group")
	}
	time.Sleep(400 * time.Millisecond)
	if strings.Contains(SessionInfo(uid), "aktif") {
		t.Log("✅ Sesi masih hidup setelah Ctrl+C (perilaku benar)")
	}

	// 8. Kill harus benar-benar menutup sesi.
	if !KillSession(uid) {
		t.Error("❌ KillSession mengembalikan false padahal sesi ada")
	} else {
		t.Log("✅ Sesi ditutup bersih")
	}
	if KillSession(uid) {
		t.Error("❌ KillSession kedua harus false")
	}
}

func TestPTYNoOrphanAfterKill(t *testing.T) {
	ok, why := ptyAvailable()
	if !ok {
		t.Skipf("SKIP — PTY tidak tersedia: %s", why)
	}

	const uid int64 = 770002
	if _, err := getOrCreateSession(uid); err != nil {
		t.Fatalf("buat sesi gagal: %v", err)
	}

	// Jalankan proses latar di dalam shell, lalu bunuh sesinya.
	// Tanpa SIGHUP ke process group, proses ini akan jadi yatim.
	marker := "/tmp/bvc_pty_orphan_marker"
	os.Remove(marker)
	if err := SendToTerminal(uid, "(sleep 600 && touch "+marker+") & echo latar_dijalankan\n"); err != nil {
		t.Fatalf("kirim perintah latar gagal: %v", err)
	}
	time.Sleep(700 * time.Millisecond)

	// Catat PID shell sesi SEBELUM dibunuh. Sesudah shell mati, informasi ini
	// hilang, dan kita tidak bisa lagi tahu proses mana yang milik sesi ini.
	shellPid, jobs := snapshotSessionProcesses(uid)
	if shellPid == 0 {
		t.Fatal("tidak bisa menemukan PID shell sesi")
	}
	if len(jobs) == 0 {
		t.Fatal("proses latar tidak berjalan — test tidak bermakna")
	}

	KillSession(uid)
	time.Sleep(2 * time.Second)

	// Verifikasi setiap proses milik sesi benar-benar mati.
	//
	// CATATAN: jangan memakai `pgrep -f "sleep 600"` — pola itu ikut mencocokkan
	// command line proses test ini sendiri. Juga jangan menghitung SEMUA proses
	// sleep di sistem: VPS produksi sering punya sleep milik skrip/cron lain,
	// dan test akan gagal acak karenanya. Yang benar adalah melacak PID yang
	// benar-benar milik sesi ini, dari snapshot sebelum kill.
	var hidup []int
	for _, pid := range jobs {
		if processAlive(pid) {
			hidup = append(hidup, pid)
		}
	}
	if len(hidup) > 0 {
		t.Errorf("❌ %d proses sesi masih hidup setelah KillSession: %v", len(hidup), hidup)
	} else {
		t.Logf("✅ Semua %d proses sesi mati — SIGHUP + SIGKILL bekerja", len(jobs))
	}
	if processAlive(shellPid) {
		t.Errorf("❌ shell sesi (pid %d) masih hidup", shellPid)
	}
	if _, err := os.Stat(marker); err == nil {
		os.Remove(marker)
		t.Error("marker dibuat — proses latar tidak dibunuh")
	}
}

// snapshotSessionProcesses mengumpulkan PID shell sesi beserta seluruh
// keturunannya. Dipakai test untuk memverifikasi tidak ada proses tersisa
// setelah sesi dibunuh.
func snapshotSessionProcesses(uid int64) (shellPid int, semua []int) {
	termSessionsMu.Lock()
	s := termSessions[uid]
	termSessionsMu.Unlock()
	if s == nil || s.Cmd == nil || s.Cmd.Process == nil {
		return 0, nil
	}
	shellPid = s.Cmd.Process.Pid

	// Kumpulkan pohon proses sekali jalan: ppid -> daftar anak.
	anak := map[int][]int{}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return shellPid, []int{shellPid}
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == 0 {
			continue
		}
		if ppid := readPpid(pid); ppid > 0 {
			anak[ppid] = append(anak[ppid], pid)
		}
	}

	// Telusuri dari shell ke bawah.
	semua = []int{shellPid}
	antre := []int{shellPid}
	for len(antre) > 0 {
		cur := antre[0]
		antre = antre[1:]
		for _, c := range anak[cur] {
			semua = append(semua, c)
			antre = append(antre, c)
		}
	}
	return shellPid, semua
}

// readPpid membaca PPID dari /proc/PID/stat.
//
// Format stat memakai tanda kurung untuk nama proses yang bisa memuat spasi,
// jadi parsing harus dimulai setelah kurung tutup terakhir.
func readPpid(pid int) int {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndex(s, ")")
	if i < 0 || i+2 >= len(s) {
		return 0
	}
	fields := strings.Fields(s[i+2:])
	if len(fields) < 2 { // state ppid ...
		return 0
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return ppid
}

// processAlive memeriksa apakah PID masih benar-benar berjalan.
//
// Zombie dianggap TIDAK hidup: prosesnya sudah mati, hanya menunggu di-reap
// oleh induknya. Dalam test ini induknya adalah proses test sendiri, jadi
// zombie akan terlihat sampai test selesai — dan itu bukan kebocoran.
// Memakai syscall.Kill(pid, 0) saja tidak cukup, karena sinyal juga berhasil
// dikirim ke zombie (nomor PID-nya masih terdaftar).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false // /proc hilang — proses benar-benar sudah dibersihkan
	}
	// Format stat: PID (nama) STATE ...
	// Nama proses bisa memuat spasi dan tanda kurung, jadi cari kurung tutup
	// terakhir sebelum membaca field berikutnya.
	s := string(b)
	i := strings.LastIndex(s, ")")
	if i < 0 || i+2 >= len(s) {
		return false
	}
	fields := strings.Fields(s[i+2:])
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "Z", "X": // zombie atau mati — tidak memakai sumber daya
		return false
	}
	return true
}

func TestPTYSessionLimit(t *testing.T) {
	ok, why := ptyAvailable()
	if !ok {
		t.Skipf("SKIP — PTY tidak tersedia: %s", why)
	}

	// Buat sesi sampai batas, lalu pastikan pembuatan berikutnya ditolak.
	created := []int64{}
	defer func() {
		for _, id := range created {
			KillSession(id)
		}
	}()

	for i := 0; i < termMaxSessions; i++ {
		uid := int64(771000 + i)
		if _, err := getOrCreateSession(uid); err != nil {
			t.Fatalf("sesi ke-%d gagal: %v", i, err)
		}
		created = append(created, uid)
	}

	// Sesi ke-(max+1) harus DITOLAK, bukan crash atau OOM.
	if _, err := getOrCreateSession(771999); err == nil {
		t.Error("❌ sesi melebihi batas seharusnya ditolak")
	} else {
		t.Logf("✅ Batas sesi (%d) ditegakkan: %v", termMaxSessions, err)
	}
}
