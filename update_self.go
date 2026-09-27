package main

// ==============================================================================
// 🔄 UPDATE MANDIRI — /update
// ==============================================================================
// Menarik versi terbaru dari git, membangun ulang, lalu mengganti proses yang
// sedang berjalan. Tanpa ini, setiap perubahan kode harus masuk VPS manual.
//
// Alur: cek → konfirmasi → tarik → bangun → ganti proses → restart service.
//
// Keamanan: pembangunan dilakukan SEBELUM proses lama dimatikan. Kalau gagal,
// bot tetap hidup dengan versi lama — bukan mati tanpa bisa pulih.

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// Nama binary hasil build dan nama binary yang sedang berjalan.
	updateBinaryName = "core_engine"
	updateTempName   = "core_engine.baru"

	// Batas waktu tiap tahap. Git dan build bisa lambat di VPS kecil.
	updateGitTimeout   = 120 * time.Second
	updateBuildTimeout = 240 * time.Second

	// Batas waktu tunggu proses baru benar-benar hidup.
	updateHealthWait = 20 * time.Second
)

// repoDir mengembalikan direktori sumber bot.
//
// Diprioritaskan dari lokasi binary karena service systemd menjalankannya
// dengan working directory root, bukan direktori sumber.
func repoDir() string {
	// Cara paling andal: direktori tempat binary berada.
	if exe, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exe); dir != "" && dir != "." {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
		}
	}
	// Cadangan: direktori kerja saat ini.
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// updateStatus merangkum keadaan repository.
type updateStatus struct {
	Dir       string
	Branch    string
	Commit    string
	Subject   string
	Remote    string
	Dirty     bool
	IsGitRepo bool
}

// bacaStatusUpdate mengumpulkan informasi repository untuk ditampilkan.
func bacaStatusUpdate() updateStatus {
	dir := repoDir()
	st := updateStatus{Dir: dir}

	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return st
	}
	st.IsGitRepo = true

	st.Branch = strings.TrimSpace(runInDir(dir, "git", "rev-parse", "--abbrev-ref", "HEAD"))
	st.Commit = strings.TrimSpace(runInDir(dir, "git", "rev-parse", "--short", "HEAD"))
	st.Subject = strings.TrimSpace(runInDir(dir, "git", "log", "-1", "--pretty=%s"))
	st.Remote = strings.TrimSpace(runInDir(dir, "git", "remote", "get-url", "origin"))

	// Perubahan lokal yang belum di-commit akan menghambat tarikan.
	if out := strings.TrimSpace(runInDir(dir, "git", "status", "--porcelain")); out != "" {
		st.Dirty = true
	}
	return st
}

// runInDir menjalankan perintah di direktori tertentu dan mengembalikan
// gabungan stdout+stderr. Dipakai untuk perintah baca-saja (status, log).
func runInDir(dir string, name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return err.Error()
	}
	return string(out)
}

// updateTersedia memeriksa apakah ada commit baru di remote.
//
// Memakai `git fetch` lalu membandingkan commit lokal dengan remote. Ini tidak
// mengubah file apa pun, jadi aman dipanggil untuk pemeriksaan.
func updateTersedia() (ada bool, lokal string, jauh string, err error) {
	dir := repoDir()

	if _, e := os.Stat(filepath.Join(dir, ".git")); e != nil {
		return false, "", "", fmt.Errorf("bukan repository git")
	}

	// Ambil informasi terbaru dari remote.
	if out := runInDir(dir, "git", "fetch", "--quiet", "origin"); out != "" {
		// fetch bisa mengembalikan progres ke stderr; itu bukan kegagalan.
		_ = out
	}

	lokal = strings.TrimSpace(runInDir(dir, "git", "rev-parse", "HEAD"))
	jauh = strings.TrimSpace(runInDir(dir, "git", "rev-parse", "origin/HEAD"))

	// origin/HEAD belum tentu ada; pakai cabang berjalan sebagai cadangan.
	if jauh == "" || strings.Contains(jauh, "fatal") {
		branch := strings.TrimSpace(runInDir(dir, "git", "rev-parse", "--abbrev-ref", "HEAD"))
		jauh = strings.TrimSpace(runInDir(dir, "git", "rev-parse", "origin/"+branch))
	}
	if lokal == "" || jauh == "" || strings.Contains(jauh, "fatal") {
		return false, lokal, jauh, fmt.Errorf("tidak bisa membandingkan dengan remote")
	}

	return lokal != jauh, lokal, jauh, nil
}

// hitungCommitTertinggal mengembalikan berapa commit lokal tertinggal.
func hitungCommitTertinggal() int {
	dir := repoDir()
	out := strings.TrimSpace(runInDir(dir, "git", "rev-list", "--count", "HEAD..origin/HEAD"))
	if out == "" || strings.Contains(out, "fatal") {
		branch := strings.TrimSpace(runInDir(dir, "git", "rev-parse", "--abbrev-ref", "HEAD"))
		out = strings.TrimSpace(runInDir(dir, "git", "rev-list", "--count", "HEAD..origin/"+branch))
	}
	n := 0
	fmt.Sscanf(out, "%d", &n)
	return n
}

// ringkasPerubahanUpdate menampilkan daftar commit yang akan masuk.
func ringkasPerubahanUpdate(limit int) string {
	dir := repoDir()
	out := strings.TrimSpace(runInDir(dir, "git", "log", "--oneline",
		fmt.Sprintf("-n%d", limit), "HEAD..origin/HEAD"))
	if out == "" || strings.Contains(out, "fatal") {
		branch := strings.TrimSpace(runInDir(dir, "git", "rev-parse", "--abbrev-ref", "HEAD"))
		out = strings.TrimSpace(runInDir(dir, "git", "log", "--oneline",
			fmt.Sprintf("-n%d", limit), "HEAD..origin/"+branch))
	}
	if out == "" || strings.Contains(out, "fatal") {
		return "(tidak bisa membaca daftar perubahan)"
	}
	return out
}

// ==============================================================================
// 🛠 PROSES UPDATE
// ==============================================================================

// jalankanUpdate menarik kode, membangun binary baru, lalu mengganti proses.
//
// Setiap tahap mengembalikan pesan yang bisa langsung dikirim ke Telegram.
// Bila gagal di tengah, proses lama TIDAK dimatikan — bot tetap melayani
// dengan versi sebelumnya.
//
// onProgress dipanggil untuk memberi kabar bertahap ke pengguna.
func jalankanUpdate(onProgress func(string)) (sukses bool, pesan string) {
	dir := repoDir()
	lapor := func(s string) {
		fmt.Println("🔄 update:", s)
		if onProgress != nil {
			onProgress(s)
		}
	}

	// ---- Tahap 1: tarik kode terbaru ----
	lapor("📥 Menarik kode terbaru dari git...")
	pullOut := runInDir(dir, "git", "pull", "--ff-only", "origin")
	if strings.Contains(pullOut, "fatal") || strings.Contains(pullOut, "error:") {
		// Pull ditolak karena perubahan lokal yang belum di-commit.
		if strings.Contains(pullOut, "local changes") ||
			strings.Contains(pullOut, "would be overwritten") {
			return false, "❌ <b>Ada perubahan lokal yang belum di-commit.</b>\n\n" +
				"Bot tidak menimpa perubahanmu. Simpan atau buang dulu:\n" +
				"<code>/term git stash</code> — simpan sementara\n" +
				"<code>/term git checkout .</code> — buang perubahan\n\n" +
				"<pre>" + htmlEscapeRingkas(pullOut, 400) + "</pre>"
		}

		// Pull ditolak karena cabang lokal menyimpang dari remote.
		//
		// Ini sering terjadi di VPS: ada commit lokal (mis. perubahan cepat
		// langsung di server) sehingga tidak bisa fast-forward.
		if strings.Contains(pullOut, "Diverging branches") ||
			strings.Contains(pullOut, "not possible to fast-forward") ||
			strings.Contains(pullOut, "divergent") {
			return false, "❌ <b>Cabang lokal menyimpang dari remote.</b>\n\n" +
				"Ada commit lokal yang tidak ada di GitHub, jadi update tidak bisa " +
				"dilanjutkan tanpa memilih salah satu.\n\n" +
				"<b>Pilihan:</b>\n" +
				"• <code>/term git log --oneline origin/master..HEAD</code> — lihat commit lokalmu\n" +
				"• <code>/term git stash</code> — simpan perubahan lokal\n" +
				"• <code>/term git reset --hard origin/master</code> — pakai versi GitHub " +
				"<i>(perubahan lokal hilang)</i>"
		}

		return false, "❌ <b>Gagal menarik kode.</b>\n<pre>" +
			htmlEscapeRingkas(pullOut, 600) + "</pre>"
	}

	// ---- Tahap 2: bangun binary baru ke nama sementara ----
	lapor("🔨 Membangun binary baru...")
	binPath := filepath.Join(dir, updateBinaryName)
	tempPath := filepath.Join(dir, updateTempName)

	// Bangun seluruh paket ("go build ."), bukan hanya main.go: project ini
	// memakai banyak file dalam satu paket, sehingga membangun main.go saja
	// akan gagal dengan "undefined" untuk setiap simbol di file lain.
	// Flag sama dengan Makefile agar hasilnya identik.
	build := exec.Command("go", "build", "-ldflags=-s -w", "-o", updateTempName, ".")
	build.Dir = dir
	buildOut, err := build.CombinedOutput()
	if err != nil {
		os.Remove(tempPath)
		detail := strings.TrimSpace(string(buildOut))
		if detail == "" {
			detail = err.Error()
		}
		return false, "❌ <b>Build gagal — bot tetap memakai versi lama.</b>\n" +
			"<pre>" + htmlEscapeRingkas(detail, 800) + "</pre>"
	}

	// Pastikan hasil build benar-benar binary yang bisa jalan.
	if fi, err := os.Stat(tempPath); err != nil || fi.Size() == 0 {
		os.Remove(tempPath)
		return false, "❌ <b>Build menghasilkan file kosong.</b> Bot tetap memakai versi lama."
	}

	// ---- Tahap 3: ganti binary lama dengan yang baru ----
	lapor("♻️ Mengganti binary...")
	if err := os.Rename(tempPath, binPath); err != nil {
		// Rename bisa gagal bila binary terpasang di mount berbeda.
		return false, "❌ <b>Gagal mengganti binary.</b>\n<pre>" +
			htmlEscapeRingkas(err.Error(), 300) + "</pre>\n\n" +
			"Binary baru tersimpan di <code>" + updateTempName + "</code>."
	}
	_ = os.Chmod(binPath, 0o755)

	// ---- Selesai: proses perlu dimulai ulang agar binary baru dipakai ----
	//
	// Binary yang sedang berjalan tidak bisa menggantikan dirinya sendiri di
	// dalam memori. Proses baru harus dijalankan, dan proses ini berhenti.
	// Pesan disesuaikan dengan kemampuan sebenarnya: bot PID 1 di container
	// tidak bisa mengganti dirinya sendiri, jadi jangan menjanjikan restart.
	if isPidSatu() {
		lapor("📦 Memasang versi baru (restart container diperlukan setelahnya)...")
	} else {
		lapor("🚀 Memulai ulang bot dengan versi baru...")
	}

	if layananSystemdAktif() {
		// Cara paling bersih: serahkan ke systemd (Restart=always).
		go func() {
			time.Sleep(1200 * time.Millisecond)
			runBashCommand("systemctl restart "+namaLayananSystemd(), 30)
		}()
		return true, "✅ <b>Update berhasil.</b>\n\n" +
			"Bot dibangun ulang dan layanan sedang di-restart.\n" +
			"Tunggu ±10 detik, lalu kirim <code>/ping</code> untuk memastikan bot hidup.\n\n" +
			"<i>Bila bot tidak kembali, cek: <code>/term journalctl -u " +
			namaLayananSystemd() + " -n 30</code></i>"
	}

	// Bukan systemd: jalankan proses baru sendiri, lewat shell terpisah supaya
	// ia tidak ikut mati bersama proses ini.
	script := fmt.Sprintf(
		"sleep 2; cd %s && setsid nohup ./%s >> /tmp/bot_vps_update.log 2>&1 < /dev/null &",
		shellQuote(dir), updateBinaryName)

	detach := exec.Command("sh", "-c", script)
	detach.Dir = dir
	detach.Stdin = nil
	if err := detach.Start(); err != nil {
		return false, "❌ <b>Binary sudah diganti, tapi gagal memulai proses baru.</b>\n" +
			"<pre>" + htmlEscapeRingkas(err.Error(), 300) + "</pre>\n\n" +
			"Jalankan manual: <code>/term ./" + updateBinaryName + "</code>"
	}

	// Beri waktu proses baru mengambil alih polling, lalu hentikan yang ini.
	go func() {
		time.Sleep(1500 * time.Millisecond)
		fmt.Println("🔄 Update selesai — proses lama berhenti, proses baru mengambil alih.")
		os.Exit(0)
	}()

	return true, "✅ <b>Update berhasil.</b>\n\n" +
		"Binary baru dibangun dan proses baru sedang dijalankan.\n" +
		"Proses lama akan berhenti sebentar lagi — tunggu ±10 detik.\n\n" +
		"Log proses baru: <code>/term tail -20 /tmp/bot_vps_update.log</code>"
}

// layananSystemdAktif memeriksa apakah bot berjalan sebagai unit systemd.
func layananSystemdAktif() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	// Bila dijalankan systemd, cgroup proses ini memuat nama unitnya.
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return false
	}
	return strings.Contains(string(b), ".service")
}

// namaLayananSystemd menebak nama unit dari cgroup, dengan cadangan yang wajar.
func namaLayananSystemd() string {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err == nil {
		// Format: .../system.slice/bot-vps.service
		for _, baris := range strings.Split(string(b), "\n") {
			if i := strings.LastIndex(baris, "/"); i >= 0 {
				nama := strings.TrimSpace(baris[i+1:])
				if strings.HasSuffix(nama, ".service") {
					return nama
				}
			}
		}
	}
	return "bot-vps.service"
}

// htmlEscapeRingkas memotong keluaran panjang dan meng-escape karakter HTML.
//
// Tanpa escape, keluaran git yang memuat "<" atau "&" akan membuat Telegram
// menolak pesan seluruhnya.
func htmlEscapeRingkas(s string, maks int) string {
	s = strings.TrimSpace(s)
	if len(s) > maks {
		s = s[:maks] + "\n… dipotong"
	}
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return r.Replace(s)
}

// ==============================================================================
// 💬 HANDLER TELEGRAM
// ==============================================================================

// handleUpdateCommand menangani /update.
//
//	/update          — tampilkan status dan tawaran update
//	/update cek      — hanya memeriksa, tidak mengubah apa pun
//	/update confirm  — jalankan update
func handleUpdateCommand(chatID int64, userID int64, rawText string) {
	arg := ""
	if p := strings.SplitN(rawText, " ", 2); len(p) == 2 {
		arg = strings.ToLower(strings.TrimSpace(p[1]))
	}

	st := bacaStatusUpdate()

	// ---- Belum bisa update: bukan repository git, dan tidak bisa unduh ----
	//
	// Dua jalur update tidak sama kebutuhannya. Jalur unduh hanya butuh
	// koneksi internet dan izin tulis — ia tidak menyentuh git sama sekali.
	// Jadi bot yang dipasang dari binary (Docker, /app, hasil curl) tetap
	// bisa update selama Releases tersedia.
	//
	// Memeriksa git lebih dulu akan menolak kasus yang justru paling butuh
	// jalur unduh: VPS spek rendah yang tidak sanggup mengompilasi.
	if !st.IsGitRepo && goTersedia() {
		sendTelegram(chatID, "⚠️ <b>Update tidak tersedia.</b>\n\n"+
			"Bot ini bukan hasil <code>git clone</code>, jadi tidak ada sumber untuk ditarik.\n\n"+
			"Direktori: <code>"+htmlEscapeRingkas(st.Dir, 200)+"</code>\n\n"+
			"Untuk mengaktifkan fitur ini, pasang bot dari repository:\n"+
			"<code>/term git clone https://github.com/arewedaks/bot-vps-control.git /opt/bot-vps</code>")
		return
	}

	// ---- Belum ada remote git (hanya berlaku bila jalur build dipakai) ----
	if st.IsGitRepo && st.Remote == "" {
		sendTelegram(chatID, "⚠️ <b>Tidak ada remote git.</b>\n\n"+
			"Tambahkan sumber terlebih dahulu:\n"+
			"<code>/term git remote add origin https://github.com/arewedaks/bot-vps-control.git</code>")
		return
	}

	// ---- Bukan repo git: langsung ke jalur unduh ----
	if !st.IsGitRepo {
		// Jalur unduh tidak punya git untuk membandingkan versi, jadi
		// versinya diambil dari GitHub Releases. Tanpa ini, /update cek di
		// bot Docker akan menampilkan tawaran update tanpa bukti apa pun
		// bahwa versi baru benar-benar ada.
		versiRilis := versiTerbaruRilis(&http.Client{Timeout: 20 * time.Second})

		if arg == "cek" {
			pesan := "🔍 <b>Cek Update</b>\n" +
				"━━━━━━━━━━━━━━━━━━━━\n" +
				"<b>Metode:</b> " + metodeUpdate() + "\n"
			if versiRilis == "" {
				pesan += "\n⚠️ Tidak bisa membaca daftar rilis GitHub.\n" +
					"Periksa koneksi internet VPS."
			} else {
				pesan += "<b>Versi terbaru:</b>" + versiRingkasUpdate(versiRilis) + "\n\n" +
					"Ketik <code>/update confirm</code> untuk memasang."
			}
			sendTelegram(chatID, pesan)
			return
		}

		if arg == "confirm" {
			sendTelegram(chatID, "🔄 <b>Memulai update...</b>\n\n"+
				"Metode: "+metodeUpdate()+"\n"+
				"Bot tetap melayani sampai tahap terakhir.")
			sukses, pesan := jalankanUpdateUnduh(st.Dir, func(tahap string) {
				sendTelegram(chatID, tahap)
			})
			_ = sukses
			sendTelegram(chatID, pesan)
			return
		}

		pesan := "🔄 <b>Update Bot</b>\n" +
			"━━━━━━━━━━━━━━━━━━━━\n" +
			"Bot ini dipasang dari binary, bukan <code>git clone</code> — jadi update " +
			"diambil langsung dari GitHub Releases. Tidak perlu git.\n\n" +
			"<b>Direktori:</b> <code>" + htmlEscapeRingkas(st.Dir, 200) + "</code>\n" +
			"<b>Metode:</b> " + metodeUpdate() + "\n"
		if versiRilis != "" {
			pesan += "<b>Versi terbaru:</b>" + versiRingkasUpdate(versiRilis) + "\n"
		}
		pesan += "\n<i>Bila gagal, binary lama tetap dipakai.</i>\n\n" +
			"Ketik <code>/update confirm</code> untuk melanjutkan."
		sendTelegram(chatID, pesan)
		return
	}

	// ---- Perubahan lokal akan menghambat pull ----
	if st.Dirty && arg != "cek" {
		sendTelegram(chatID, "⚠️ <b>Ada perubahan lokal yang belum di-commit.</b>\n\n"+
			"Agar tidak ada pekerjaan yang hilang, bot tidak melanjutkan.\n\n"+
			"<b>Cabang:</b> <code>"+htmlEscapeRingkas(st.Branch, 60)+"</code>\n"+
			"<b>Commit:</b> <code>"+htmlEscapeRingkas(st.Commit, 20)+"</code>\n"+
			"<b>Remote:</b> <code>"+htmlEscapeRingkas(st.Remote, 120)+"</code>\n\n"+
			"Pilih salah satu:\n"+
			"• <code>/term git stash</code> — simpan sementara\n"+
			"• <code>/term git diff</code> — lihat perubahannya\n"+
			"• <code>/term git checkout .</code> — buang perubahan")
		return
	}

	// ---- Periksa ketersediaan versi baru ----
	ada, lokal, jauh, err := updateTersedia()
	if err != nil {
		sendTelegram(chatID, "❌ <b>Gagal memeriksa update.</b>\n<pre>"+
			htmlEscapeRingkas(err.Error(), 400)+"</pre>\n\n"+
			"Pastikan VPS bisa menjangkau GitHub:\n<code>/term git fetch origin</code>")
		return
	}

	if !ada {
		sendTelegram(chatID, "✅ <b>Bot sudah versi terbaru.</b>\n\n"+
			"<b>Cabang:</b> <code>"+htmlEscapeRingkas(st.Branch, 60)+"</code>\n"+
			"<b>Commit:</b> <code>"+htmlEscapeRingkas(st.Commit, 20)+"</code>\n"+
			"<b>Pesan:</b> "+htmlEscapeRingkas(st.Subject, 120)+"\n\n"+
			"Tidak ada perubahan baru di <code>"+htmlEscapeRingkas(st.Remote, 100)+"</code>.")
		return
	}

	// ---- Mode periksa saja ----
	tertinggal := hitungCommitTertinggal()
	ringkas := ringkasPerubahanUpdate(10)
	metode := metodeUpdate()

	if arg == "cek" {
		sendTelegram(chatID, "🔍 <b>Update tersedia</b> ("+
			fmt.Sprintf("%d", tertinggal)+" commit baru)\n\n"+
			"<b>Versi sekarang:</b> <code>"+htmlEscapeRingkas(lokal[:minInt(7, len(lokal))], 20)+"</code>\n"+
			"<b>Versi terbaru:</b> <code>"+htmlEscapeRingkas(jauh[:minInt(7, len(jauh))], 20)+"</code>\n"+
			"<b>Metode:</b> "+metode+"\n\n"+
			"<b>Perubahan:</b>\n<pre>"+htmlEscapeRingkas(ringkas, 900)+"</pre>\n\n"+
			"Jalankan <code>/update confirm</code> untuk memasang.")
		return
	}

	// ---- Mode konfirmasi: benar-benar update ----
	if arg == "confirm" {
		sendTelegram(chatID, "🔄 <b>Memulai update...</b>\n\n"+
			fmt.Sprintf("Memasang %d commit baru.\n", tertinggal)+
			"Metode: "+metode+"\n"+
			"Bot tetap melayani sampai tahap terakhir.")

		sukses, pesan := jalankanUpdateTerpilih(metode, func(tahap string) {
			sendTelegram(chatID, tahap)
		})
		_ = sukses
		sendTelegram(chatID, pesan)
		return
	}

	// ---- Tanpa argumen: tampilkan ringkasan + minta konfirmasi ----
	sendTelegram(chatID, "🔄 <b>Update Bot</b>\n"+
		"━━━━━━━━━━━━━━━━━━━━\n"+
		"<b>Cabang:</b> <code>"+htmlEscapeRingkas(st.Branch, 60)+"</code>\n"+
		"<b>Versi sekarang:</b> <code>"+htmlEscapeRingkas(lokal[:minInt(7, len(lokal))], 20)+"</code>\n"+
		"<b>Versi terbaru:</b> <code>"+htmlEscapeRingkas(jauh[:minInt(7, len(jauh))], 20)+"</code>\n"+
		"<b>Commit baru:</b> "+fmt.Sprintf("%d", tertinggal)+"\n"+
		"<b>Metode:</b> "+metode+"\n\n"+
		"<b>Perubahan:</b>\n<pre>"+htmlEscapeRingkas(ringkas, 700)+"</pre>\n\n"+
		"<i>Bot akan menarik kode, memasang versi baru, lalu restart sendiri.\n"+
		"Bila gagal, bot tetap hidup dengan versi lama.</i>\n\n"+
		"Ketik <code>/update confirm</code> untuk melanjutkan.")
}

// ==============================================================================
// 🔀 PEMILIHAN JALUR UPDATE
// ==============================================================================

// Metode update yang tersedia.
const (
	metodeBuild  = "build"  // kompilasi di VPS (butuh Go toolchain)
	metodeUnduh  = "unduh"  // ambil binary jadi dari GitHub Releases
	metodeManual = "manual" // tidak ada keduanya
)

// pilihMetode menentukan cara update tanpa efek samping.
//
// Dipisahkan dari metodeUpdate() agar bisa diuji tanpa memeriksa sistem.
func pilihMetode(adaGo bool) string {
	if adaGo {
		return metodeBuild
	}
	return metodeUnduh
}

// metodeUpdate menentukan jalur update yang akan dipakai, sekaligus
// mengembalikan teks penjelasan untuk ditampilkan di Telegram.
func metodeUpdate() string {
	adaGo := goTersedia()

	switch pilihMetode(adaGo) {
	case metodeBuild:
		return "🔨 kompilasi di VPS (Go terdeteksi)"
	default:
		return "⬇️ unduh biner jadi (<code>linux/" +
			htmlEscapeRingkas(namaArsitekturRilis(runtime.GOARCH), 20) +
			"</code>, tanpa kompilasi)"
	}
}

// jalankanUpdateTerpilih menjalankan metode yang sudah dipilih.
//
// Bila jalur unduh gagal — misalnya Release belum dibuat — bot TIDAK langsung
// menyerah: bila Go tersedia, ia beralih ke kompilasi lokal. VPS spek rendah
// tetap dapat keuntungan utama: percobaan pertama tidak mengompilasi apa pun.
func jalankanUpdateTerpilih(metode string, onProgress func(string)) (bool, string) {
	dir := repoDir()

	if metode == metodeBuild {
		return jalankanUpdate(onProgress)
	}

	sukses, pesan := jalankanUpdateUnduh(dir, onProgress)
	if sukses {
		return true, pesan
	}

	// Jalur unduh gagal. Beralih ke kompilasi hanya bila memungkinkan.
	if !goTersedia() {
		return false, pesan + "\n\n" +
			"<i>VPS ini tidak punya Go toolchain, jadi tidak bisa beralih " +
			"ke kompilasi lokal. Buat Release di GitHub, atau pasang Go:</i>\n" +
			"<code>/term apt install -y golang-go</code>"
	}

	if onProgress != nil {
		onProgress("⚠️ Jalur unduh gagal — beralih ke kompilasi lokal...")
	}
	return jalankanUpdate(onProgress)
}

// minInt mengembalikan nilai terkecil — dipakai memotong hash commit.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
