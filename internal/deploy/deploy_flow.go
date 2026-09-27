package deploy

// ==============================================================================
// 🚀 DEPLOY BOT — HANDLER
// ==============================================================================
// Jembatan antara Telegram dan mesin deploy: menerima berkas, tautan, dan
// alamat GitHub; menangani tombol-tombol panel.
//
// Semua operasi berat (unduh, pasang dependensi, kompilasi) dijalankan di
// goroutine terpisah. Handler Telegram berjalan berurutan, jadi pekerjaan yang
// memakan menit akan membekukan seluruh bot — termasuk perintah darurat
// seperti /status. Karena itu pengguna dibalas lebih dulu, lalu hasilnya
// ditulis ulang ke pesan yang sama saat selesai.

import (
	"archive/tar"
	"archive/zip"
	"bot-vps-control/internal/tg"
	"bot-vps-control/internal/update"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Pola pencarian baris impor Python.
var (
	regexpImportWajib = regexp.MustCompile(`(?m)^\s*import\s+([A-Za-z_][A-Za-z0-9_]*)`)
	regexpFromWajib   = regexp.MustCompile(`(?m)^\s*from\s+([A-Za-z_][A-Za-z0-9_]*)`)
)

// ==============================================================================
// PENERIMAAN BERKAS
// ==============================================================================

// DeployMenerimaDokumen memeriksa apakah berkas ini ditujukan ke panel deploy.
//
// Dipanggil lebih dulu oleh handleDocumentUpload. Bila langkah percakapan
// deploy sedang aktif, berkas ditangani di sini dan pemanggil harus berhenti.
func DeployMenerimaDokumen(chatID int64, userID int64, doc *tg.Document) bool {
	langkah, aktif := ambilLangkahDeploy(userID)
	if !aktif {
		return false
	}
	if langkah.Aksi != "mk" && langkah.Aksi != "unduh" {
		return false
	}

	hapusLangkahDeploy(userID)
	go prosesTerimaBerkas(chatID, userID, doc, langkah.Pesan)
	return true
}

// prosesTerimaBerkas mengunduh berkas dari Telegram dan menaruhnya ke direktori proyek.
func prosesTerimaBerkas(chatID int64, userID int64, doc *tg.Document, pesanID int64) {
	if doc == nil {
		return
	}

	namaBerkas := strings.TrimSpace(doc.FileName)
	if namaBerkas == "" {
		namaBerkas = fmt.Sprintf("proyek_%d.zip", time.Now().Unix())
	}

	if doc.FileSize > maksUnduhDeploy {
		balasPanel(chatID, pesanID,
			fmt.Sprintf("❌ <b>Berkas terlalu besar.</b>\n\n"+
				"Ukuran: %s\nBatas: %s",
				formatUkuran(doc.FileSize), formatUkuran(maksUnduhDeploy)),
			kbKembaliDeploy())
		return
	}

	namaProyek := namaProyekAman(namaTanpaEkstensi(namaBerkas))

	// Pastikan direktori induk bisa dipakai SEBELUM mengunduh. Tanpa
	// pemeriksaan ini, berkas besar terunduh lebih dulu baru gagal disimpan —
	// membuang waktu dan kuota pengguna.
	induk, pesanInduk := siapkanDirDeploy(userID)
	if pesanInduk != "" {
		balasPanel(chatID, pesanID, pesanInduk, kbKembaliDeploy())
		return
	}

	dir := filepath.Join(induk, namaProyek)

	// Proyek dengan nama sama diganti total. Ini disengaja: menimpa sebagian
	// berkas lama akan meninggalkan sisa versi lama yang membingungkan.
	if err := os.RemoveAll(dir); err != nil {
		balasPanel(chatID, pesanID,
			"❌ Gagal membersihkan direktori proyek:\n<pre>"+
				update.HtmlEscapeRingkas(err.Error(), 300)+"</pre>", kbKembaliDeploy())
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		balasPanel(chatID, pesanID,
			"❌ Gagal membuat direktori proyek:\n<pre>"+
				update.HtmlEscapeRingkas(err.Error(), 300)+"</pre>", kbKembaliDeploy())
		return
	}

	// Simpan di luar direktori proyek supaya berkas arsip tidak ikut terdeteksi
	// sebagai isi proyek.
	tmp := filepath.Join(induk, ".masuk_"+namaBerkas)

	balasPanel(chatID, pesanID,
		"<b><i>Mengunduh:</i></b> ▓▓▓▓▓░░░░░ 50%", nil)

	ukuran, err := tg.DownloadTelegramFile(doc.FileID, tmp)
	if err != nil {
		os.Remove(tmp)
		balasPanel(chatID, pesanID,
			"❌ Gagal mengunduh berkas:\n<pre>"+
				update.HtmlEscapeRingkas(err.Error(), 300)+"</pre>", kbKembaliDeploy())
		return
	}

	balasPanel(chatID, pesanID,
		"<b><i>Mengurai:</i></b> ▓▓▓▓▓▓▓▓░░ 80%", nil)

	// Tentukan cara penanganan dari isi berkas, bukan hanya namanya. Banyak
	// arsip proyek tidak punya ekstensi yang konsisten.
	ok, pesanGalat := uraikanBerkas(tmp, dir, namaBerkas, ukuran)
	os.Remove(tmp)

	if !ok {
		os.RemoveAll(dir)
		balasPanel(chatID, pesanID,
			"❌ <b>Gagal mengurai berkas.</b>\n\n<pre>"+
				update.HtmlEscapeRingkas(pesanGalat, 500)+"</pre>", kbKembaliDeploy())
		return
	}

	pasangDependensiDeploy(userID, namaProyek, chatID, pesanID, true)
}

// namaTanpaEkstensi membuang ekstensi arsip atau skrip dari nama berkas.
func namaTanpaEkstensi(nama string) string {
	lower := strings.ToLower(nama)
	for _, ekstensi := range []string{".tar.gz", ".tgz", ".tar.bz2", ".zip", ".gz"} {
		if strings.HasSuffix(lower, ekstensi) {
			return nama[:len(nama)-len(ekstensi)]
		}
	}
	return strings.TrimSuffix(nama, filepath.Ext(nama))
}

// uraikanBerkas menaruh isi berkas ke direktori proyek.
//
// Bila berkas adalah skrip tunggal, ia disalin apa adanya. Bila arsip, isinya
// diurai. Bila binary, disalin lalu diberi bit eksekusi.
func uraikanBerkas(sumber string, dir string, namaAsli string, ukuran int64) (bool, string) {
	signature, err := bacaSignature(sumber)
	if err != nil {
		return false, err.Error()
	}

	switch signature {
	case "zip":
		return uraikanZip(sumber, dir)
	case "gzip":
		return uraikanTarGz(sumber, dir)
	}

	// Bukan arsip: berkas tunggal.
	tujuan := filepath.Join(dir, filepath.Base(namaAsli))

	src, err := os.Open(sumber)
	if err != nil {
		return false, err.Error()
	}
	defer src.Close()

	dst, err := os.Create(tujuan)
	if err != nil {
		return false, err.Error()
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return false, err.Error()
	}
	dst.Close()

	// Beri bit eksekusi bila isinya binary jadi, supaya pengguna tidak perlu
	// chmod manual.
	if apakahELF(tujuan) || strings.HasSuffix(strings.ToLower(namaAsli), ".exe") {
		os.Chmod(tujuan, 0o755)
	}

	return true, ""
}

// bacaSignature mengenali jenis berkas dari byte awalnya.
func bacaSignature(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var kepala [4]byte
	n, _ := io.ReadFull(f, kepala[:])
	if n < 2 {
		return "", fmt.Errorf("berkas kosong")
	}

	// ZIP: PK\x03\x04 (atau varian lain berawalan PK).
	if kepala[0] == 'P' && kepala[1] == 'K' {
		return "zip", nil
	}
	// GZIP: \x1f\x8b
	if kepala[0] == 0x1f && kepala[1] == 0x8b {
		return "gzip", nil
	}
	return "lain", nil
}

// uraikanZip mengurai arsip zip dengan perlindungan path traversal.
func uraikanZip(sumber string, dir string) (bool, string) {
	r, err := zip.OpenReader(sumber)
	if err != nil {
		return false, "arsip zip rusak: " + err.Error()
	}
	defer r.Close()

	for _, f := range r.File {
		tujuan := filepath.Join(dir, f.Name)

		// Zip Slip: entri arsip bisa memakai "../" untuk menulis di luar
		// direktori proyek. Periksa sebelum menulis.
		if !amanDiDalam(dir, tujuan) {
			return false, "arsip berisi jalur mencurigakan: " + f.Name
		}
	}

	// Lepas kompresi seluruhnya, dengan batas total agar bom zip tidak
	// memenuhi disk.
	var total int64
	for _, f := range r.File {
		tujuan := filepath.Join(dir, f.Name)

		if f.FileInfo().IsDir() {
			os.MkdirAll(tujuan, 0o755)
			continue
		}

		os.MkdirAll(filepath.Dir(tujuan), 0o755)

		rc, err := f.Open()
		if err != nil {
			return false, err.Error()
		}

		out, err := os.Create(tujuan)
		if err != nil {
			rc.Close()
			return false, err.Error()
		}

		n, err := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return false, err.Error()
		}

		total += n
		if total > maksUnduhDeploy {
			return false, "isi arsip melebihi batas setelah diurai"
		}

		// Pertahankan bit eksekusi bila ada di arsip.
		if f.Mode()&0o111 != 0 {
			os.Chmod(tujuan, f.Mode())
		}
	}

	ratakanDirektoriTunggal(dir)
	return true, ""
}

// uraikanTarGz mengurai arsip tar.gz dengan perlindungan yang sama.
func uraikanTarGz(sumber string, dir string) (bool, string) {
	f, err := os.Open(sumber)
	if err != nil {
		return false, err.Error()
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return false, "arsip gzip rusak: " + err.Error()
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var total int64

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, err.Error()
		}

		tujuan := filepath.Join(dir, hdr.Name)
		if !amanDiDalam(dir, tujuan) {
			return false, "arsip berisi jalur mencurigakan: " + hdr.Name
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(tujuan, 0o755)

		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(tujuan), 0o755)

			out, err := os.Create(tujuan)
			if err != nil {
				return false, err.Error()
			}

			n, err := io.Copy(out, tr)
			out.Close()
			if err != nil {
				return false, err.Error()
			}

			total += n
			if total > maksUnduhDeploy {
				return false, "isi arsip melebihi batas setelah diurai"
			}

			mode := os.FileMode(hdr.Mode)
			if mode&0o111 != 0 {
				os.Chmod(tujuan, mode)
			}

		case tar.TypeSymlink, tar.TypeLink:
			// Tautan dalam arsip bisa menunjuk ke luar direktori proyek.
			// Dilewati daripada mencoba menilai keamanannya.
			continue
		}
	}

	ratakanDirektoriTunggal(dir)
	return true, ""
}

// amanDiDalam memastikan tujuan benar-benar berada di dalam basis.
func amanDiDalam(basis string, tujuan string) bool {
	basisAbs, err := filepath.Abs(basis)
	if err != nil {
		return false
	}
	tujuanAbs, err := filepath.Abs(tujuan)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(basisAbs, tujuanAbs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ratakanDirektoriTunggal mengangkat isi satu direktori teratas ke atas.
//
// Arsip GitHub biasanya berisi satu folder pembungkus (mis. repo-main/).
// Tanpa perataan ini, deteksi bahasa dan entry point akan gagal karena
// berkasnya tersembunyi satu tingkat lebih dalam.
func ratakanDirektoriTunggal(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var nyata []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".deploy.") {
			continue
		}
		nyata = append(nyata, e.Name())
	}

	if len(nyata) != 1 {
		return
	}

	satu := filepath.Join(dir, nyata[0])
	fi, err := os.Stat(satu)
	if err != nil || !fi.IsDir() {
		return
	}

	isi, err := os.ReadDir(satu)
	if err != nil {
		return
	}

	for _, e := range isi {
		dari := filepath.Join(satu, e.Name())
		ke := filepath.Join(dir, e.Name())
		if _, err := os.Stat(ke); err == nil {
			// Nama bentrok: biarkan apa adanya daripada menimpa.
			continue
		}
		os.Rename(dari, ke)
	}

	// Sisa direktori kosong dibuang. Kegagalan tidak fatal.
	os.Remove(satu)
}

// ==============================================================================
// DEPLOY DARI URL
// ==============================================================================

// DeployURL memulai deploy dari tautan unduhan langsung.
func DeployURL(chatID int64, userID int64, tautan string, pesanID int64) {
	urlBersih, err := validasiURL(tautan)
	if err != nil {
		kirimKesalahanDeploy(chatID, pesanID, "Tautan tidak valid", err.Error())
		return
	}

	namaProyek := namaProyekAman(namaTanpaEkstensi(filepath.Base(urlBersih)))
	go prosesDeployURL(chatID, userID, urlBersih, namaProyek, pesanID)
}

func prosesDeployURL(chatID int64, userID int64, url string, namaProyek string, pesanID int64) {
	induk, pesanInduk := siapkanDirDeploy(userID)
	if pesanInduk != "" {
		balasPanel(chatID, pesanID, pesanInduk, kbKembaliDeploy())
		return
	}

	dir := filepath.Join(induk, namaProyek)

	if err := os.RemoveAll(dir); err != nil {
		kirimKesalahanDeploy(chatID, pesanID, "Gagal menyiapkan direktori", err.Error())
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		kirimKesalahanDeploy(chatID, pesanID, "Gagal membuat direktori", err.Error())
		return
	}

	balasPanel(chatID, pesanID,
		"<b><i>Mengunduh dari URL:</i></b> ▓▓▓▓▓░░░░░ 40%", nil)

	tmp := filepath.Join(induk, ".masuk_"+namaProyek)

	ukuran, err := unduhKeBerkas(url, tmp, maksUnduhDeploy)
	if err != nil {
		os.Remove(tmp)
		os.RemoveAll(dir)
		kirimKesalahanDeploy(chatID, pesanID, "Gagal mengunduh", err.Error())
		return
	}

	balasPanel(chatID, pesanID,
		"<b><i>Mengurai:</i></b> ▓▓▓▓▓▓▓▓░░ 80%", nil)

	ok, pesan := uraikanBerkas(tmp, dir, namaProyek, ukuran)
	os.Remove(tmp)

	if !ok {
		os.RemoveAll(dir)
		kirimKesalahanDeploy(chatID, pesanID, "Gagal mengurai", pesan)
		return
	}

	pasangDependensiDeploy(userID, namaProyek, chatID, pesanID, true)
}

// unduhKeBerkas mengunduh URL ke berkas dengan batas ukuran.
//
// Batas ukuran diperiksa dari header Content-Length lebih dulu, lalu dari
// jumlah byte yang benar-benar ditulis. Pemeriksaan ganda ini penting karena
// Content-Length bisa tidak ada atau dipalsukan.
func unduhKeBerkas(url string, tujuan string, batas int64) (int64, error) {
	client := &http.Client{Timeout: batasUnduhDeploy}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("tg.User-Agent", "bot-vps-control/deploy")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("server merespons HTTP %d", resp.StatusCode)
	}

	if resp.ContentLength > batas {
		return 0, fmt.Errorf("berkas %s melebihi batas %s",
			formatUkuran(resp.ContentLength), formatUkuran(batas))
	}

	out, err := os.Create(tujuan)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	// Batasi pembacaan sedikit di atas batas agar berkas tepat pada batas
	// masih diterima, sementara yang melewatinya terpotong dan terdeteksi.
	ditulis, err := io.Copy(out, io.LimitReader(resp.Body, batas+1))
	if err != nil {
		return ditulis, err
	}
	if ditulis > batas {
		return ditulis, fmt.Errorf("berkas melebihi batas %s", formatUkuran(batas))
	}

	return ditulis, nil
}

// ==============================================================================
// DEPLOY DARI GITHUB
// ==============================================================================

// DeployGit memulai deploy dari repository GitHub.
func DeployGit(chatID int64, userID int64, alamat string, pesanID int64) {
	repo, err := validasiRepoGit(alamat)
	if err != nil {
		kirimKesalahanDeploy(chatID, pesanID, "Alamat repository tidak valid", err.Error())
		return
	}

	if !adaPerintah("git") {
		kirimKesalahanDeploy(chatID, pesanID, "Git tidak tersedia",
			"Pasang git di VPS ini: apt install -y git")
		return
	}

	// Nama proyek diambil dari nama repository.
	potong := strings.TrimSuffix(filepath.Base(repo), ".git")
	namaProyek := namaProyekAman(potong)

	go prosesDeployGit(chatID, userID, repo, namaProyek, pesanID)
}

func prosesDeployGit(chatID int64, userID int64, repo string, namaProyek string, pesanID int64) {
	induk, pesanInduk := siapkanDirDeploy(userID)
	if pesanInduk != "" {
		balasPanel(chatID, pesanID, pesanInduk, kbKembaliDeploy())
		return
	}

	dir := filepath.Join(induk, namaProyek)

	if err := os.RemoveAll(dir); err != nil {
		kirimKesalahanDeploy(chatID, pesanID, "Gagal menyiapkan direktori", err.Error())
		return
	}

	balasPanel(chatID, pesanID,
		"<b><i>Mengkloning repository:</i></b> ▓▓▓▓▓░░░░░ 40%\n\n"+
			"<code>"+update.HtmlEscapeRingkas(repo, 120)+"</code>", nil)

	// --depth 1: hanya commit terakhir yang dibutuhkan. Riwayat penuh bisa
	// berukuran ratusan MB dan tidak ada gunanya untuk deploy.
	perintah := fmt.Sprintf("git clone --depth 1 %s %s",
		kutipShell(repo), kutipShell(dir))

	ok, keluaran := jalankanShell(perintah, induk, batasGitDeploy)
	if !ok {
		os.RemoveAll(dir)
		kirimKesalahanDeploy(chatID, pesanID, "Gagal mengkloning repository", keluaran)
		return
	}

	// Direktori .git dibuang: tidak dibutuhkan untuk menjalankan proyek, dan
	// menyimpan kredensial remote yang mungkin tersemat di URL.
	os.RemoveAll(filepath.Join(dir, ".git"))

	pasangDependensiDeploy(userID, namaProyek, chatID, pesanID, true)
}

// ==============================================================================
// PEMASANGAN DEPENDENSI
// ==============================================================================

// pasangDependensiDeploy menyiapkan proyek sesuai bahasanya, lalu melaporkan hasil.
//
// Dijalankan di goroutine sendiri karena bisa memakan menit. Kegagalan
// pemasangan TIDAK membatalkan deploy: proyek tetap dilaporkan dan bisa dicoba
// dijalankan, karena sebagian dependensi mungkin sudah tersedia.
func pasangDependensiDeploy(userID int64, namaProyek string, chatID int64, pesanID int64, lapor bool) {
	dir := filepath.Join(dirDeployPenggunaWajib(userID), namaProyek)
	bahasa := deteksiBahasa(dir)

	var catatan []string

	switch bahasa {
	case bahasaPython:
		catatan = append(catatan, pasangPythonDeploy(dir)...)

	case bahasaNode:
		catatan = append(catatan, pasangNodeDeploy(dir)...)

	case bahasaGo:
		catatan = append(catatan, bangunGoDeploy(dir)...)

	case bahasaBiner:
		// Tidak ada dependensi. Pastikan langsung bisa dieksekusi.
		if entry := cariEntrypoint(dir, bahasa); entry != "" {
			path := filepath.Join(dir, entry)
			if fi, err := os.Stat(path); err == nil {
				os.Chmod(path, fi.Mode()|0o111)
			}
		}
		catatan = append(catatan, "⚙️ Binary siap dijalankan.")

	case bahasaShell:
		catatan = append(catatan, "📜 Skrip shell siap dijalankan.")

	default:
		catatan = append(catatan, "⚠️ Bahasa tidak dikenali. Tentukan manual di menu Kontrol.")
	}

	teks, kb := laporanHasilDeploy(userID, namaProyek)
	if len(catatan) > 0 {
		teks += "\n\n<b>Catatan:</b>\n" + strings.Join(catatan, "\n")
	}

	if lapor {
		balasPanel(chatID, pesanID, teks, kb)
	}
}

// pasangPythonDeploy memasang paket Python yang dibutuhkan proyek.
func pasangPythonDeploy(dir string) []string {
	var catatan []string

	if !adaPerintah("pip3") && !adaPerintah("pip") {
		return []string{"⚠️ pip tidak tersedia, dependensi tidak dipasang."}
	}

	pip := "pip3"
	if !adaPerintah("pip3") {
		pip = "pip"
	}

	// requirements.txt lebih dulu: isinya eksplisit dari pengembang proyek,
	// jadi lebih dapat dipercaya daripada hasil pemindaian impor.
	req := filepath.Join(dir, "requirements.txt")
	if _, err := os.Stat(req); err == nil {
		perintah := fmt.Sprintf("%s install --no-input --disable-pip-version-check -r requirements.txt", pip)
		ok, keluaran := jalankanShell(perintah, dir, batasInstalasiDeploy)
		if ok {
			catatan = append(catatan, "✅ requirements.txt terpasang.")
		} else {
			ekor := keluaran
			if len(ekor) > 400 {
				ekor = ekor[len(ekor)-400:]
			}
			catatan = append(catatan, "⚠️ Sebagian paket gagal dipasang:\n<pre>"+
				update.HtmlEscapeRingkas(ekor, 400)+"</pre>")
		}
		return catatan
	}

	// Tanpa requirements.txt, impor dipindai dari kode.
	paket := pindaiImporPython(dir)
	if len(paket) == 0 {
		return []string{"🐍 Tidak ada dependensi tambahan."}
	}

	perintah := fmt.Sprintf("%s install --no-input --disable-pip-version-check %s",
		pip, strings.Join(paket, " "))
	ok, keluaran := jalankanShell(perintah, dir, batasInstalasiDeploy)
	if ok {
		catatan = append(catatan, fmt.Sprintf("✅ %d paket terpasang otomatis.", len(paket)))
	} else {
		ekor := keluaran
		if len(ekor) > 400 {
			ekor = ekor[len(ekor)-400:]
		}
		catatan = append(catatan, "⚠️ Sebagian paket gagal dipasang:\n<pre>"+
			update.HtmlEscapeRingkas(ekor, 400)+"</pre>")
	}

	return catatan
}

// pasangNodeDeploy memasang dependensi Node.js.
func pasangNodeDeploy(dir string) []string {
	packageJSON := filepath.Join(dir, "package.json")
	if _, err := os.Stat(packageJSON); err != nil {
		return []string{"🟢 Tanpa package.json, tidak ada dependensi."}
	}

	if !adaPerintah("npm") {
		return []string{"⚠️ npm tidak tersedia, dependensi tidak dipasang."}
	}

	// node_modules yang sudah ada dilewati: memindai ulang hanya membuang waktu.
	if fi, err := os.Stat(filepath.Join(dir, "node_modules")); err == nil && fi.IsDir() {
		return []string{"🟢 node_modules sudah ada."}
	}

	// --omit=dev: paket pengembangan tidak dibutuhkan untuk menjalankan bot,
	// dan memasangnya bisa menghemat ratusan MB.
	ok, keluaran := jalankanShell("npm install --omit=dev --no-audit --no-fund", dir, batasInstalasiDeploy)
	if ok {
		return []string{"✅ Dependensi npm terpasang."}
	}

	// Sebagian package.json memakai alat pengembangan sebagai dependensi
	// biasa. Coba sekali lagi tanpa --omit=dev sebelum menyerah.
	ok, keluaran2 := jalankanShell("npm install --no-audit --no-fund", dir, batasInstalasiDeploy)
	if ok {
		return []string{"✅ Dependensi npm terpasang (termasuk dev)."}
	}

	ekor := keluaran2
	if ekor == "" {
		ekor = keluaran
	}
	if len(ekor) > 400 {
		ekor = ekor[len(ekor)-400:]
	}
	return []string{"⚠️ Pemasangan npm gagal:\n<pre>" + update.HtmlEscapeRingkas(ekor, 400) + "</pre>"}
}

// bangunGoDeploy mengunduh dependensi Go dan membangun binary.
//
// Binary hasil build disimpan sebagai app_bin supaya saat dijalankan ulang
// proyek tidak perlu mengompilasi lagi. Ini menghemat RAM dan mempercepat
// restart, yang penting di VPS kecil.
func bangunGoDeploy(dir string) []string {
	if !adaPerintah("go") {
		return []string{"⚠️ Go tidak tersedia. Proyek akan dijalankan bila ada binary siap pakai."}
	}

	var catatan []string

	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		ok, keluaran := jalankanShell("go mod download", dir, batasInstalasiDeploy)
		if ok {
			catatan = append(catatan, "✅ Dependensi Go terunduh.")
		} else {
			ekor := keluaran
			if len(ekor) > 300 {
				ekor = ekor[len(ekor)-300:]
			}
			catatan = append(catatan, "⚠️ go mod download bermasalah:\n<pre>"+
				update.HtmlEscapeRingkas(ekor, 300)+"</pre>")
		}
	}

	// -ldflags "-s -w" membuang simbol debug dan tabel simbol: hasilnya
	// sekitar 30% lebih kecil dan tidak mengurangi fungsionalitas.
	perintah := `go build -ldflags "-s -w" -o app_bin .`
	ok, keluaran := jalankanShell(perintah, dir, batasInstalasiDeploy)
	if ok {
		catatan = append(catatan, "✅ Binary Go berhasil dibangun (<code>app_bin</code>).")
	} else {
		ekor := keluaran
		if len(ekor) > 400 {
			ekor = ekor[len(ekor)-400:]
		}
		catatan = append(catatan, "⚠️ Build gagal, akan dicoba dengan <code>go run</code>:\n<pre>"+
			update.HtmlEscapeRingkas(ekor, 400)+"</pre>")
	}

	return catatan
}

// ==============================================================================
// PEMINDAI IMPOR PYTHON
// ==============================================================================

// modulStandarPython berisi modul bawaan Python.
//
// Daftar ini sengaja tidak lengkap: fungsinya hanya menyaring nama yang jelas
// bawaan. Nama yang salah tebak dan tetap dipasang hanya membuang waktu,
// sedangkan nama bawaan yang ikut dipasang akan gagal dan tercatat sebagai
// peringatan yang membingungkan.
var modulStandarPython = map[string]bool{
	"os": true, "sys": true, "json": true, "time": true, "math": true,
	"random": true, "string": true, "base64": true, "hashlib": true,
	"datetime": true, "threading": true, "asyncio": true, "subprocess": true,
	"logging": true, "pathlib": true, "itertools": true, "functools": true,
	"collections": true, "typing": true, "urllib": true, "http": true,
	"email": true, "csv": true, "sqlite3": true, "gzip": true, "zipfile": true,
	"shutil": true, "tempfile": true, "socket": true, "ssl": true, "html": true,
	"xml": true, "re": true, "traceback": true, "platform": true, "signal": true,
	"copy": true, "enum": true, "uuid": true, "io": true, "glob": true,
	"stat": true, "argparse": true, "dataclasses": true, "secrets": true,
	"queue": true, "unicodedata": true, "textwrap": true, "pprint": true,
	"warnings": true, "contextlib": true, "importlib": true, "inspect": true,
	"struct": true, "binascii": true, "hmac": true, "zlib": true, "tarfile": true,
	"concurrent": true, "multiprocessing": true, "ctypes": true, "decimal": true,
	"fractions": true, "statistics": true, "operator": true, "types": true,
	"abc": true, "weakref": true, "gc": true, "atexit": true, "codecs": true,
	"locale": true, "getpass": true, "shlex": true, "filecmp": true,
	"fnmatch": true, "linecache": true, "tokenize": true, "ast": true,
}

// namaPaketPip memetakan nama impor ke nama paket di PyPI.
//
// Sebagian paket punya nama impor yang berbeda dari nama pemasangannya.
// Tanpa pemetaan ini, pemasangan akan gagal dengan pesan yang membingungkan.
var namaPaketPip = map[string]string{
	"telebot":    "pyTelegramBotAPI",
	"bs4":        "beautifulsoup4",
	"PIL":        "Pillow",
	"cv2":        "opencv-python",
	"yaml":       "PyYAML",
	"Crypto":     "pycryptodome",
	"OpenSSL":    "pyOpenSSL",
	"sklearn":    "scikit-learn",
	"user_agent": "user-agent",
	"lxml":       "lxml",
	"dns":        "dnspython",
	"OpenAI":     "openai",
	"dotenv":     "python-dotenv",
	"telegram":   "python-telegram-bot",
	"discord":    "discord.py",
	"jwt":        "PyJWT",
	"serial":     "pyserial",
	"pandas":     "pandas",
	"numpy":      "numpy",
	"requests":   "requests",
	"flask":      "Flask",
	"fastapi":    "fastapi",
	"uvicorn":    "uvicorn",
	"aiohttp":    "aiohttp",
	"httpx":      "httpx",
	"paramiko":   "paramiko",
	"psutil":     "psutil",
	"pymongo":    "pymongo",
	"sqlalchemy": "SQLAlchemy",
	"redis":      "redis",
	"pydantic":   "pydantic",
}

// pindaiImporPython memindai berkas .py dan mengembalikan paket yang perlu dipasang.
//
// Pemindaian memakai pencocokan pola biasa, bukan pengurai Python penuh.
// Alasannya: satu baris `import` atau `from ... import` cukup sederhana untuk
// dikenali dengan andal, dan pendekatan ini tidak akan gagal pada berkas yang
// sintaksisnya tidak lengkap.
func pindaiImporPython(dir string) []string {
	ditemukan := map[string]bool{}

	reImport := regexpImportWajib
	reFrom := regexpFromWajib

	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".py") {
			return nil
		}
		// Lewati direktori virtualenv: isinya bukan kode proyek.
		if strings.Contains(path, "/venv/") || strings.Contains(path, "/.venv/") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		for _, m := range reImport.FindAllStringSubmatch(string(data), -1) {
			if len(m) > 1 {
				ditemukan[strings.Split(m[1], ".")[0]] = true
			}
		}
		for _, m := range reFrom.FindAllStringSubmatch(string(data), -1) {
			if len(m) > 1 {
				ditemukan[strings.Split(m[1], ".")[0]] = true
			}
		}
		return nil
	})

	var hasil []string
	for nama := range ditemukan {
		if modulStandarPython[nama] {
			continue
		}
		// Modul lokal proyek bukan paket yang perlu dipasang.
		if _, err := os.Stat(filepath.Join(dir, nama+".py")); err == nil {
			continue
		}
		if fi, err := os.Stat(filepath.Join(dir, nama)); err == nil && fi.IsDir() {
			continue
		}
		if paket, ada := namaPaketPip[nama]; ada {
			hasil = append(hasil, paket)
			continue
		}
		hasil = append(hasil, nama)
	}

	// Urut dan buang duplikat supaya perintah pemasangan dapat diprediksi.
	sort.Strings(hasil)
	return dedupString(hasil)
}

func dedupString(masukan []string) []string {
	terlihat := map[string]bool{}
	var hasil []string
	for _, s := range masukan {
		if terlihat[s] {
			continue
		}
		terlihat[s] = true
		hasil = append(hasil, s)
	}
	return hasil
}

// ==============================================================================
// PEMBANTU KIRIM PESAN
// ==============================================================================

// balasPanel menulis teks ke pesan panel, atau mengirim pesan baru bila panel
// belum ada. Keyboard nil berarti tombol lama dibiarkan apa adanya.
func balasPanel(chatID int64, pesanID int64, teks string, kb *tg.InlineKeyboardMarkup) {
	if pesanID > 0 {
		if tg.EditTelegramMessage(chatID, pesanID, teks, kb) {
			return
		}
		// Sebagian pesan tidak bisa disunting (terlalu lama, atau sudah
		// dihapus). Pesan baru lebih baik daripada tidak ada balasan.
		if kb != nil {
			tg.SendTelegramWithKeyboard(chatID, teks, kb)
		} else {
			tg.SendTelegram(chatID, teks)
		}
		return
	}
	if kb != nil {
		tg.SendTelegramWithKeyboard(chatID, teks, kb)
	} else {
		tg.SendTelegram(chatID, teks)
	}
}

// kirimKesalahanDeploy mengirim laporan kegagalan yang seragam.
func kirimKesalahanDeploy(chatID int64, pesanID int64, judul string, rincian string) {
	teks := "❌ <b>" + update.HtmlEscapeRingkas(judul, 100) + "</b>\n\n<pre>" +
		update.HtmlEscapeRingkas(rincian, 800) + "</pre>"
	balasPanel(chatID, pesanID, teks, kbKembaliDeploy())
}

func kbKembaliDeploy() *tg.InlineKeyboardMarkup {
	return &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
		{
			{Text: "🚀 Panel Deploy", CallbackData: "dp:m"},
			{Text: "🔙 Utama", CallbackData: "hp:h"},
		},
	}}
}

// ==============================================================================
// PENANGANAN TEKS
// ==============================================================================

// DeployMenerimaTeks menangani teks masuk yang ditujukan ke panel deploy.
//
// Kembalikan true bila teks sudah ditangani, sehingga pemanggil berhenti dan
// teks itu tidak diperlakukan sebagai perintah bot.
func DeployMenerimaTeks(chatID int64, userID int64, teks string) bool {
	langkah, aktif := ambilLangkahDeploy(userID)
	if !aktif {
		return false
	}

	switch langkah.Aksi {
	case "unduh":
		hapusLangkahDeploy(userID)
		DeployURL(chatID, userID, teks, langkah.Pesan)
		return true

	case "git":
		hapusLangkahDeploy(userID)
		DeployGit(chatID, userID, teks, langkah.Pesan)
		return true

	case "entry":
		hapusLangkahDeploy(userID)
		nama := namaProyekAman(langkah.Proyek)
		dir := filepath.Join(dirDeployPenggunaWajib(userID), nama)

		entry := strings.TrimSpace(teks)
		if _, err := os.Stat(filepath.Join(dir, entry)); err != nil {
			balasPanel(chatID, langkah.Pesan,
				"❌ Berkas <code>"+update.HtmlEscapeRingkas(entry, 80)+"</code> tidak ada.\n\n"+
					"Tulis nama berkasnya persis seperti yang terlihat di daftar.",
				kbKembaliKontrol(nama))
			return true
		}

		tulisPenanda(dir, penandaEntryDeploy, entry)

		balasPanel(chatID, langkah.Pesan,
			"✅ Entry point disetel ke <code>"+update.HtmlEscapeRingkas(entry, 80)+"</code>.",
			kbKembaliKontrol(nama))
		return true
	}

	// Langkah "mk" tidak menunggu teks, tapi teks masuk tetap diabaikan supaya
	// tidak dianggap perintah bot.
	if langkah.Aksi == "mk" {
		return true
	}

	return false
}

// ==============================================================================
// PENANGANAN CALLBACK
// ==============================================================================

// DeployTanganiCallback memproses tombol yang berawalan dp:.
//
// Kembalikan true bila callback sudah ditangani.
func DeployTanganiCallback(userID int64, chatID int64, pesanID int64, data string) bool {
	if !strings.HasPrefix(data, "dp:") {
		return false
	}

	potongan := strings.Split(strings.TrimPrefix(data, "dp:"), ":")
	if len(potongan) == 0 {
		return false
	}
	aksi := potongan[0]

	switch aksi {
	case "m": // Menu utama deploy
		teks, kb := MenuDeployUtama(userID)
		tg.SetPanelMessage(userID, chatID, pesanID)
		tg.EditTelegramMessage(chatID, pesanID, teks, kb)
		return true

	case "mk": // Panduan deploy baru
		setLangkahDeploy(userID, langkahDeploy{Aksi: "mk", Pesan: pesanID})
		teks, kb := panduanDeployBaru()
		tg.SetPanelMessage(userID, chatID, pesanID)
		tg.EditTelegramMessage(chatID, pesanID, teks, kb)
		return true

	case "u": // Minta URL
		setLangkahDeploy(userID, langkahDeploy{Aksi: "unduh", Pesan: pesanID})
		tg.SetPanelMessage(userID, chatID, pesanID)
		tg.EditTelegramMessage(chatID, pesanID,
			"━━━━━━━━━━━━━━━━━━━━━━━\n"+
				"      🔗 <b>DEPLOY DARI URL</b> 🔗\n"+
				"━━━━━━━━━━━━━━━━━━━━━━━\n"+
				"Kirim tautan unduhan langsung ke berkas.\n\n"+
				"<b>Didukung:</b>\n"+
				"└─➤ <i>.zip .tar.gz .py .js .sh</i>\n"+
				"└─➤ <i>binary jadi (ELF)</i>\n"+
				"━━━━━━━━━━━━━━━━━━━━━━━\n"+
				"<i>Contoh:</i>\n<code>https://situs.com/bot.zip</code>",
			kbBatalDeploy())
		return true

	case "g": // Minta alamat GitHub
		setLangkahDeploy(userID, langkahDeploy{Aksi: "git", Pesan: pesanID})
		tg.SetPanelMessage(userID, chatID, pesanID)
		tg.EditTelegramMessage(chatID, pesanID,
			"━━━━━━━━━━━━━━━━━━━━━━━\n"+
				"      🐙 <b>DEPLOY DARI GITHUB</b> 🐙\n"+
				"━━━━━━━━━━━━━━━━━━━━━━━\n"+
				"Kirim alamat repository.\n\n"+
				"<b>Bentuk yang diterima:</b>\n"+
				"└─➤ <code>pemilik/repo</code>\n"+
				"└─➤ <code>https://github.com/pemilik/repo</code>\n"+
				"└─➤ <code>https://github.com/pemilik/repo.git</code>\n"+
				"━━━━━━━━━━━━━━━━━━━━━━━\n"+
				"<i>Hanya commit terakhir yang diambil (--depth 1).</i>",
			kbBatalDeploy())
		return true

	case "l": // Daftar proyek
		halaman := 0
		if len(potongan) > 1 {
			halaman, _ = strconv.Atoi(potongan[1])
		}
		teks, kb := daftarProyekPanel(userID, halaman)
		tg.SetPanelMessage(userID, chatID, pesanID)
		tg.EditTelegramMessage(chatID, pesanID, teks, kb)
		return true

	case "c": // Panel kontrol proyek
		if len(potongan) < 2 {
			return true
		}
		nama := namaProyekAman(potongan[1])
		teks, kb := panelKontrolDeploy(userID, nama)
		tg.SetPanelMessage(userID, chatID, pesanID)
		tg.EditTelegramMessage(chatID, pesanID, teks, kb)
		return true

	case "run", "rst": // Start / Restart
		if len(potongan) < 2 {
			return true
		}
		nama := namaProyekAman(potongan[1])
		tg.SetPanelMessage(userID, chatID, pesanID)

		ok, pesan := jalankanProyekDeploy(userID, nama)
		if !ok {
			balasPanel(chatID, pesanID, pesan, kbKembaliKontrol(nama))
			return true
		}

		teks, kb := panelKontrolDeploy(userID, nama)
		teks = "✅ <b>Proyek dinyalakan.</b>\n\n" + teks
		tg.EditTelegramMessage(chatID, pesanID, teks, kb)
		return true

	case "stop": // Stop
		if len(potongan) < 2 {
			return true
		}
		nama := namaProyekAman(potongan[1])
		tg.SetPanelMessage(userID, chatID, pesanID)

		matikanProsesDeploy(namaProsesDeploy(userID, nama))

		teks, kb := panelKontrolDeploy(userID, nama)
		teks = "🛑 <b>Proyek dihentikan.</b>\n\n" + teks
		tg.EditTelegramMessage(chatID, pesanID, teks, kb)
		return true

	case "log": // Lihat log
		if len(potongan) < 2 {
			return true
		}
		nama := namaProyekAman(potongan[1])
		scr := namaProsesDeploy(userID, nama)
		isi := potongKiri(bacaEkorLogDeploy(scr, 2500), 2500)

		teks := "━━━━━━━━━━━━━━━━━━━━━━━\n" +
			"        📜 <b>LOG PROYEK</b> 📜\n" +
			"━━━━━━━━━━━━━━━━━━━━━━━\n" +
			fmt.Sprintf("📦 <code>%s</code>\n", update.HtmlEscapeRingkas(nama, 40)) +
			fmt.Sprintf("🕐 <code>%s</code>\n", waktuSekarang()) +
			"━━━━━━━━━━━━━━━━━━━━━━━\n" +
			"<pre>" + update.HtmlEscapeRingkas(isi, 2600) + "</pre>"

		kb := &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
			{
				{Text: "🔄 Refresh", CallbackData: "dp:log:" + nama},
				{Text: "🗑 Bersihkan", CallbackData: "dp:logclr:" + nama},
			},
			{
				{Text: "🔙 Kembali", CallbackData: "dp:c:" + nama},
			},
		}}
		tg.SetPanelMessage(userID, chatID, pesanID)
		tg.EditTelegramMessage(chatID, pesanID, teks, kb)
		return true

	case "logclr": // Kosongkan log
		if len(potongan) < 2 {
			return true
		}
		nama := namaProyekAman(potongan[1])
		os.Truncate(berkasLogDeploy(namaProsesDeploy(userID, nama)), 0)
		tg.AnswerCallbackQuery("", "🗑 Log dibersihkan.")
		teks, kb := panelKontrolDeploy(userID, nama)
		tg.SetPanelMessage(userID, chatID, pesanID)
		tg.EditTelegramMessage(chatID, pesanID, teks, kb)
		return true

	case "lang": // Pilih bahasa
		if len(potongan) < 2 {
			return true
		}
		nama := namaProyekAman(potongan[1])
		teks, kb := panelPilihBahasa(userID, nama)
		tg.SetPanelMessage(userID, chatID, pesanID)
		tg.EditTelegramMessage(chatID, pesanID, teks, kb)
		return true

	case "setlang": // Tetapkan bahasa
		if len(potongan) < 3 {
			return true
		}
		nama := namaProyekAman(potongan[1])
		kode := potongan[2]
		dir := filepath.Join(dirDeployPenggunaWajib(userID), nama)

		if kode == "auto" {
			os.Remove(filepath.Join(dir, penandaBahasaDeploy))
		} else {
			switch kode {
			case bahasaPython, bahasaNode, bahasaGo, bahasaBiner, bahasaShell:
				tulisPenanda(dir, penandaBahasaDeploy, kode)
			default:
				return true
			}
		}

		// Restart supaya perubahan bahasa langsung berlaku. Melewatkan langkah
		// ini akan membuat pengguna bingung karena status tidak berubah.
		if pidHidupDeploy(namaProsesDeploy(userID, nama)) != 0 {
			matikanProsesDeploy(namaProsesDeploy(userID, nama))
		}

		teks, kb := panelKontrolDeploy(userID, nama)
		teks = "✅ <b>Bahasa diperbarui.</b>\n\n" + teks
		tg.SetPanelMessage(userID, chatID, pesanID)
		tg.EditTelegramMessage(chatID, pesanID, teks, kb)
		return true

	case "setent": // Pilih entry point dari daftar
		if len(potongan) < 2 {
			return true
		}
		nama := namaProyekAman(potongan[1])
		teks, kb := panelPilihEntry(userID, nama)
		tg.SetPanelMessage(userID, chatID, pesanID)
		tg.EditTelegramMessage(chatID, pesanID, teks, kb)
		return true

	case "setentry": // Tetapkan entry point
		if len(potongan) < 3 {
			return true
		}
		nama := namaProyekAman(potongan[1])
		entry := potongan[2]
		dir := filepath.Join(dirDeployPenggunaWajib(userID), nama)

		// Periksa ulang: nilai berasal dari callback_data yang bisa dipalsukan.
		if !amanDiDalam(dir, filepath.Join(dir, entry)) {
			tg.AnswerCallbackQuery("", "⛔ Nama berkas tidak valid.")
			return true
		}
		if _, err := os.Stat(filepath.Join(dir, entry)); err != nil {
			tg.AnswerCallbackQuery("", "⛔ Berkas tidak ditemukan.")
			return true
		}

		tulisPenanda(dir, penandaEntryDeploy, entry)

		if pidHidupDeploy(namaProsesDeploy(userID, nama)) != 0 {
			matikanProsesDeploy(namaProsesDeploy(userID, nama))
		}

		teks, kb := panelKontrolDeploy(userID, nama)
		teks = "✅ <b>Entry point disetel.</b>\n\n" + teks
		tg.SetPanelMessage(userID, chatID, pesanID)
		tg.EditTelegramMessage(chatID, pesanID, teks, kb)
		return true

	case "del": // Konfirmasi hapus
		if len(potongan) < 2 {
			return true
		}
		nama := namaProyekAman(potongan[1])

		kb := &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
			{
				{Text: "🗑 Ya, Hapus", CallbackData: "dp:del2:" + nama},
				{Text: "❌ Batal", CallbackData: "dp:c:" + nama},
			},
		}}
		tg.SetPanelMessage(userID, chatID, pesanID)
		tg.EditTelegramMessage(chatID, pesanID,
			"⚠️ <b>KONFIRMASI HAPUS</b> ⚠️\n\n"+
				"Proyek <code>"+update.HtmlEscapeRingkas(nama, 40)+"</code> akan dihapus\n"+
				"beserta seluruh berkasnya.\n\n"+
				"<i>Tindakan ini tidak bisa dibatalkan.</i>", kb)
		return true

	case "del2": // Hapus sungguhan
		if len(potongan) < 2 {
			return true
		}
		nama := namaProyekAman(potongan[1])

		// Hentikan dulu: menghapus berkas proyek yang sedang berjalan akan
		// membuat prosesnya terus hidup tanpa bisa dihentikan lewat panel.
		matikanProsesDeploy(namaProsesDeploy(userID, nama))
		os.Truncate(berkasLogDeploy(namaProsesDeploy(userID, nama)), 0)

		os.RemoveAll(filepath.Join(dirDeployPenggunaWajib(userID), nama))

		teks, kb := daftarProyekPanel(userID, 0)
		teks = "🗑 <b>Proyek dihapus.</b>\n\n" + teks
		tg.SetPanelMessage(userID, chatID, pesanID)
		tg.EditTelegramMessage(chatID, pesanID, teks, kb)
		return true
	}

	return false
}

func kbBatalDeploy() *tg.InlineKeyboardMarkup {
	return &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
		{{Text: "🔙 Batal", CallbackData: "dp:m"}},
	}}
}
