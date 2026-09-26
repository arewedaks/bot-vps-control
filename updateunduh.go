package main

// ==============================================================================
// ⬇️ UPDATE RINGAN — UNDUH BINARY JADI DARI GITHUB
// ==============================================================================
// VPS spek rendah (RAM 512MB, CPU 1 core) sering kehabisan memori saat
// mengompilasi Go, dan belum tentu punya Go toolchain.
//
// Jalur ini mengambil binary yang sudah dibangun GitHub Actions, jadi VPS
// hanya perlu: unduh → verifikasi → tukar. Nol kompilasi.
//
// Kenapa tetap aman:
//   - Ukuran file diperiksa, agar unduhan yang terpotong tidak dipasang.
//   - SHA256 diperiksa terhadap SHA256SUMS dari Release yang sama.
//     Ini menangkap unduhan yang rusak maupun yang diubah di tengah jalan.
//   - Binary baru diuji jalan sebentar (--cek) sebelum dipasang.
//   - Pemasangan tetap lewat os.Rename, jadi binary lama utuh bila gagal.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// repoTuanRumah adalah sumber rilis. Bisa diubah di .env dengan
// UPDATE_REPO=user/repo bila fork dipakai.
var repoTuanRumah = "arewedaks/bot-vps-control"

// Nama arsitektur Go → nama yang dipakai file rilis.
//
// Dipakai untuk menyusun nama unduhan, jadi VPS tidak perlu tahu apa pun.
func namaArsitekturRilis(goarch string) string {
	switch goarch {
	case "amd64", "arm64", "arm", "386":
		return goarch
	default:
		return goarch
	}
}

// alamatRilis menyusun URL aset Release untuk versi tertentu.
//
// versi kosong berarti "latest".
func alamatRilis(versi string, namaFile string) string {
	base := "https://github.com/" + repoTuanRumah + "/releases"
	if versi == "" {
		return base + "/latest/download/" + namaFile
	}
	return base + "/download/" + versi + "/" + namaFile
}

// ambilURL mengunduh URL ke sebuah berkas sementara dan mengembalikan
// jumlah byte tertulis.
//
// Batas ukuran mencegah unduhan tak terbatas bila URL salah.
func ambilURL(klien *http.Client, url string, tujuan string, maksByte int64) (int64, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}
	// GitHub menolak permintaan tanpa User-Agent yang jelas.
	req.Header.Set("User-Agent", "bot-vps-control/update")

	resp, err := klien.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d dari %s", resp.StatusCode, url)
	}

	f, err := os.Create(tujuan)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	// Batasi agar berkas tidak tumbuh tanpa kendali.
	n, err := io.Copy(f, io.LimitReader(resp.Body, maksByte))
	if err != nil {
		os.Remove(tujuan)
		return 0, err
	}
	if n == 0 {
		os.Remove(tujuan)
		return 0, fmt.Errorf("unduhan kosong")
	}
	return n, nil
}

// bacaSHA256SuMS mengambil daftar checksum dari Release.
//
// Mengembalikan peta nama berkas → hash. Bila tidak ada, peta kosong
// (bukan error) supaya update tetap bisa jalan tanpa verifikasi.
func bacaSHA256SuMS(klien *http.Client, versi string) map[string]string {
	hasil := map[string]string{}

	sementara := filepath.Join(os.TempDir(),
		fmt.Sprintf("sha-%d.txt", time.Now().UnixNano()))
	defer os.Remove(sementara)

	if _, err := ambilURL(klien, alamatRilis(versi, "SHA256SUMS"), sementara, 1<<20); err != nil {
		return hasil
	}

	data, err := os.ReadFile(sementara)
	if err != nil {
		return hasil
	}

	for _, baris := range strings.Split(string(data), "\n") {
		bidang := strings.Fields(strings.TrimSpace(baris))
		if len(bidang) != 2 {
			continue
		}
		nama := strings.TrimPrefix(bidang[1], "*")
		hasil[nama] = strings.ToLower(bidang[0])
	}
	return hasil
}

// hashBerkas menghitung SHA256 sebuah berkas.
func hashBerkas(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// goTersedia memeriksa apakah toolchain Go ada dan cukup baru.
//
// Dipakai untuk memilih jalur update: build lokal bila Go ada, unduh
// binary bila tidak.
func goTersedia() bool {
	if _, err := exec.LookPath("go"); err != nil {
		return false
	}
	cmd := exec.Command("go", "version")
	cmd.Dir = repoDir()
	out, err := cmd.CombinedOutput()
	return err == nil && strings.Contains(string(out), "go1.")
}

// versiTerbaruRilis membaca tag Release terbaru dari GitHub.
//
// Memakai endpoint /releases/latest yang mengembalikan redirect ke tag.
// Mengembalikan string kosong bila tidak bisa dibaca — pemanggil lalu
// memakai "latest" sebagai gantinya.
func versiTerbaruRilis(klien *http.Client) string {
	req, err := http.NewRequest("GET",
		"https://api.github.com/repos/"+repoTuanRumah+"/releases/latest", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "bot-vps-control/update")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := klien.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	// Ambil hanya "tag_name" tanpa perlu parser JSON penuh.
	// Endpoint ini dipakai sekali jalan; menghindari struct tambahan.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	teks := string(body)

	kunci := `"tag_name"`
	i := strings.Index(teks, kunci)
	if i < 0 {
		return ""
	}
	sisa := teks[i+len(kunci):]
	awal := strings.Index(sisa, `"`)
	if awal < 0 {
		return ""
	}
	sisa = sisa[awal+1:]
	akhir := strings.Index(sisa, `"`)
	if akhir < 0 {
		return ""
	}
	return sisa[:akhir]
}

// periksaChecksum membandingkan hash berkas dengan yang diumumkan Release.
//
// Mengembalikan (terverifikasi, pesanGagal). Bila daftar tidak memuat berkas
// ini, verifikasi dianggap terlewat (terverifikasi=false) — bukan gagal,
// supaya Release tanpa SHA256SUMS tetap bisa dipakai.
//
// Dipisahkan dari jalankanUpdateUnduh agar logika penolakan ini bisa diuji
// langsung. Ini penjaga paling penting dari jalur unduh: berkas yang diubah
// di tengah jalan TIDAK boleh dipasang, karena memasangnya berarti bot mati
// tanpa cara pulih.
func periksaChecksum(path string, namaBerkas string, daftar map[string]string) (bool, string) {
	mau, ada := daftar[namaBerkas]
	if !ada || mau == "" {
		return false, ""
	}

	hash, err := hashBerkas(path)
	if err != nil {
		return false, "❌ <b>Gagal membaca berkas hasil unduhan.</b>\n<pre>" +
			htmlEscapeRingkas(err.Error(), 300) + "</pre>"
	}

	if mau != hash {
		return false, "❌ <b>Checksum tidak cocok — unduhan dibatalkan.</b>\n\n" +
			"Berkas mungkin rusak saat diunduh, atau diubah di tengah jalan. " +
			"Binary lama tetap dipakai.\n\n" +
			"<b>Diharapkan:</b> <code>" + htmlEscapeRingkas(panjangAman(mau, 16), 20) + "…</code>\n" +
			"<b>Hasil hitung:</b> <code>" + htmlEscapeRingkas(panjangAman(hash, 16), 20) + "…</code>\n\n" +
			"Ulangi beberapa saat lagi, atau pasang manual."
	}

	return true, ""
}

// panjangAman memotong string tanpa melewati panjang aslinya.
func panjangAman(s string, n int) string {
	return s[:minInt(n, len(s))]
}

// ==============================================================================
// 🛠 JALUR UPDATE RINGAN
// ==============================================================================

// jalankanUpdateUnduh mengambil binary jadi dari GitHub Releases.
//
// Tidak memerlukan Go toolchain maupun source code di VPS. Yang dibutuhkan
// hanya koneksi internet dan izin tulis di direktori binary.
//
// Mengembalikan (sukses, pesan) dengan format yang sama seperti jalankanUpdate,
// sehingga handler Telegram bisa memakai keduanya tanpa perbedaan.
func jalankanUpdateUnduh(dir string, onProgress func(string)) (sukses bool, pesan string) {
	sukses, pesan, hasil := unduhDanPasang(dir, onProgress)
	if !sukses {
		return false, pesan
	}

	// ---- Restart ---- //
	// Dipisahkan dari unduhDanPasang supaya tahap unduh bisa diuji tanpa
	// benar-benar menghentikan proses yang menjalankan test.
	if onProgress != nil {
		if isPidSatu() {
			onProgress("📦 Memasang versi baru (restart container diperlukan setelahnya)...")
		} else {
			onProgress("🚀 Memulai ulang bot dengan versi baru...")
		}
	}

	catatan := ""
	if !hasil.terverifikasi {
		catatan = "\n\n⚠️ <i>Checksum tidak tersedia di Release, " +
			"jadi berkas tidak diverifikasi.</i>"
	}

	// ---- Kasus container: bot adalah PID 1 ---- //
	//
	// Di dalam container bot sering dijalankan sebagai proses utama, jadi
	// PID 1. Proses seperti ini tidak bisa menggantikan dirinya sendiri:
	// keluar berarti container mati, dan container tidak punya mekanisme
	// untuk menjalankan perintah setelahnya.
	//
	// Yang lebih buruk: memulai proses baru di sini justru gagal. Proses lama
	// masih hidup dan memegang kunci instance, jadi proses baru ditolak dan
	// langsung keluar.
	//
	// Jadi lebih jujur melaporkan apa adanya — binary terpasang, tinggal
	// container di-restart dari host — daripada mengirim "sedang restart"
	// yang tidak akan terjadi.
	if isPidSatu() {
		return true, "✅ <b>Update berhasil dipasang.</b>\n\n" +
			"Versi baru" + versiRingkasUpdate(hasil.versi) + " " +
			"(<code>linux/" + hasil.arch + "</code>) sudah ada di disk.\n\n" +
			"⚠️ <b>Tinggal satu langkah.</b>\n" +
			"Bot ini berjalan sebagai proses utama container (<code>PID 1</code>), " +
			"jadi ia tidak bisa mengganti dirinya sendiri.\n\n" +
			"Restart container dari <b>host</b> (di luar container):\n" +
			"<pre>docker restart " + htmlEscapeRingkas(namaContainer(), 60) + "</pre>\n" +
			"Setelah itu kirim <code>/ping</code> untuk memastikan versi baru jalan." + catatan
	}

	if layananSystemdAktif() {
		go func() {
			time.Sleep(1200 * time.Millisecond)
			runBashCommand("systemctl restart "+namaLayananSystemd(), 30)
		}()
		return true, "✅ <b>Update berhasil.</b>\n\n" +
			"Versi baru dipasang" + versiRingkasUpdate(hasil.versi) + " " +
			"(<code>linux/" + hasil.arch + "</code>).\n" +
			"Layanan sedang di-restart. Tunggu ±10 detik, lalu kirim <code>/ping</code>." +
			catatan
	}

	script := fmt.Sprintf(
		"sleep 2; cd %s && setsid nohup ./%s >> /tmp/bot_vps_update.log 2>&1 < /dev/null &",
		shellQuote(dir), updateBinaryName)

	detach := exec.Command("sh", "-c", script)
	detach.Dir = dir
	detach.Stdin = nil
	if err := detach.Start(); err != nil {
		return false, "❌ <b>Binary sudah dipasang, tapi gagal menjalankan proses baru.</b>\n" +
			"<pre>" + htmlEscapeRingkas(err.Error(), 300) + "</pre>\n\n" +
			"Jalankan manual: <code>/term ./" + updateBinaryName + "</code>"
	}

	go func() {
		time.Sleep(1500 * time.Millisecond)
		fmt.Println("⬇️ Update selesai — proses lama berhenti, proses baru mengambil alih.")
		os.Exit(0)
	}()

	return true, "✅ <b>Update berhasil.</b>\n\n" +
		"Versi baru dipasang" + versiRingkasUpdate(hasil.versi) + " " +
		"(<code>linux/" + hasil.arch + "</code>).\n" +
		"Proses baru sedang dijalankan — tunggu ±10 detik." + catatan
}

// hasilUnduh merangkum apa yang berhasil dipasang.
type hasilUnduh struct {
	versi         string // tag Release, mis. v1.0.0
	arch          string // arsitektur yang dipasang
	terverifikasi bool   // checksum cocok dengan SHA256SUMS resmi
}

// unduhDanPasang menjalankan tahap unduh sampai binary terpasang, tanpa
// menyentuh proses yang sedang berjalan.
//
// Dipisahkan dari jalankanUpdateUnduh agar seluruh tahap ini bisa diuji tanpa
// restart — kalau test memicu restart, test-nya sendiri yang mati.
//
// Urutan langkahnya sengaja begini: setiap pemeriksaan dilakukan SEBELUM
// binary lama disentuh, jadi setiap kegagalan meninggalkan bot tetap hidup.
func unduhDanPasang(dir string, onProgress func(string)) (bool, string, hasilUnduh) {
	var hasil hasilUnduh

	binPath := filepath.Join(dir, updateBinaryName)
	tempPath := filepath.Join(dir, updateTempName)

	lapor := func(s string) {
		fmt.Println("⬇️ update:", s)
		if onProgress != nil {
			onProgress(s)
		}
	}

	klien := &http.Client{Timeout: 120 * time.Second}

	// ---- Tentukan arsitektur ---- //
	// Ini yang membuat satu Release bisa dipakai semua VPS: nama berkas
	// disusun dari arsitektur mesin yang sedang berjalan.
	arch := namaArsitekturRilis(runtime.GOARCH)
	if runtime.GOOS != "linux" {
		return false, "❌ <b>Update unduh hanya untuk Linux.</b>\n\n" +
			"Sistem ini: <code>" + htmlEscapeRingkas(runtime.GOOS+"/"+runtime.GOARCH, 40) + "</code>\n" +
			"Pasang manual atau bangun dari sumber.", hasil
	}

	hasil.arch = arch
	namaBerkas := "core_engine-linux-" + arch
	lapor("⬇️ Mengunduh binary untuk <code>linux/" + arch + "</code>...")

	versi := versiTerbaruRilis(klien)
	hasil.versi = versi

	// ---- Unduh binary ---- //
	// Batas 64 MB: binary proyek ini ~7 MB, jadi ini longgar tapi tetap
	// mencegah unduhan liar bila URL salah arah.
	if _, err := ambilURL(klien, alamatRilis(versi, namaBerkas), tempPath, 64<<20); err != nil {
		os.Remove(tempPath)
		return false, "❌ <b>Gagal mengunduh binary.</b>\n\n" +
			"Berkas: <code>" + htmlEscapeRingkas(namaBerkas, 60) + "</code>\n" +
			"<pre>" + htmlEscapeRingkas(err.Error(), 400) + "</pre>\n\n" +
			"Pastikan Release sudah dibuat:\n" +
			"<code>git tag v1.0.0 &amp;&amp; git push origin v1.0.0</code>", hasil
	}

	fi, err := os.Stat(tempPath)
	if err != nil || fi.Size() < 1<<20 {
		os.Remove(tempPath)
		ukuran := int64(0)
		if fi != nil {
			ukuran = fi.Size()
		}
		return false, fmt.Sprintf("❌ <b>Berkas hasil unduhan tidak wajar.</b>\n\n"+
			"Ukuran: %d byte (harusnya > 1 MB).\n"+
			"Mungkin Release belum berisi binary.", ukuran), hasil
	}

	// ---- Verifikasi checksum ---- //
	// Ini pembeda penting: unduhan yang terpotong atau diubah di tengah
	// jalan akan tertangkap di sini, bukan setelah dipasang.
	lapor("🔐 Memeriksa keaslian berkas...")
	daftar := bacaSHA256SuMS(klien, versi)

	terverifikasi, pesanGagalChecksum := periksaChecksum(tempPath, namaBerkas, daftar)
	if pesanGagalChecksum != "" {
		os.Remove(tempPath)
		return false, pesanGagalChecksum, hasil
	}
	hasil.terverifikasi = terverifikasi

	// ---- Uji jalan sebelum dipasang ---- //
	// Binary yang tidak bisa dijalankan (arsitektur salah, rusak) akan gagal
	// di sini — sebelum menyentuh binary lama.
	lapor("🧪 Menguji binary baru...")
	if err := os.Chmod(tempPath, 0o755); err != nil {
		os.Remove(tempPath)
		return false, "❌ <b>Gagal memberi izin eksekusi.</b>\n<pre>" +
			htmlEscapeRingkas(err.Error(), 300) + "</pre>", hasil
	}

	if err := ujiBinary(tempPath, dir); err != nil {
		os.Remove(tempPath)
		return false, "❌ <b>Binary baru tidak bisa dijalankan.</b>\n\n" +
			"Kemungkinan arsitektur tidak cocok, atau berkas rusak. " +
			"Binary lama tetap dipakai.\n\n" +
			"<pre>" + htmlEscapeRingkas(err.Error(), 400) + "</pre>", hasil
	}

	// ---- Pasang ---- //
	// os.Rename bersifat atomik di filesystem yang sama: pembaca melihat
	// binary lama atau binary baru, tidak pernah campuran keduanya.
	lapor("♻️ Mengganti binary...")
	if err := os.Rename(tempPath, binPath); err != nil {
		os.Remove(tempPath)
		return false, "❌ <b>Gagal memasang binary.</b>\n<pre>" +
			htmlEscapeRingkas(err.Error(), 300) + "</pre>", hasil
	}
	_ = os.Chmod(binPath, 0o755)

	return true, "", hasil
}

// ujiBinary menjalankan binary dengan --cek untuk memastikan ia benar-benar
// bisa dijalankan di mesin ini.
//
// Ini lapisan kedua setelah checksum: berkas bisa lolos checksum karena
// Release-nya sendiri salah (arsitektur berbeda), tapi tetap tidak bisa
// dijalankan di sini.
func ujiBinary(path string, dir string) error {
	uji := exec.Command(path, "--cek")
	uji.Dir = dir
	out, err := uji.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("%s", detail)
	}
	return nil
}

// versiRingkasUpdate memformat tag versi untuk ditampilkan.
func versiRingkasUpdate(versi string) string {
	if versi == "" {
		return ""
	}
	return " <code>" + htmlEscapeRingkas(versi, 40) + "</code>"
}

// ==============================================================================
// 🐳 DETEKSI CONTAINER
// ==============================================================================

// isPidSatu melaporkan apakah proses ini adalah proses utama container.
//
// Di dalam container, bot sering dijalankan sebagai PID 1. Proses seperti itu
// tidak bisa menggantikan dirinya sendiri: keluar berarti container mati.
//
// Deteksi lewat PID sudah cukup dan tidak butuh berkas khusus — PID 1 di
// container selalu proses yang dijalankan image, apa pun itu.
func isPidSatu() bool {
	return pidSaatIni() == 1
}

// pidSaatIni memisahkan pembacaan PID agar bisa diganti saat pengujian.
//
// Tanpa pemisahan ini, jalur PID 1 tidak bisa diuji sama sekali: test di
// laptop selalu berjalan sebagai PID biasa, sehingga kode penanganan
// container tidak pernah tersentuh — dan kerusakannya tidak terdeteksi.
var pidSaatIni = os.Getpid

// namaContainer menebak nama container untuk perintah restart di host.
//
// Hostname container secara default adalah ID container (12 karakter hex),
// dan `docker restart` menerima ID itu. Jadi tebakan ini biasanya langsung
// bisa dipakai tanpa perlu tahu nama yang diberikan saat `docker run`.
//
// Bila hostname diubah manual, tebakannya salah — tetapi perintahnya masih
// memperlihatkan polanya, jadi pengguna tahu apa yang harus diganti.
func namaContainer() string {
	// Bila dijalankan docker-compose, hostname sering berupa nama service.
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "nama-container"
}
