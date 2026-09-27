package main

// ==============================================================================
// 📦 UPLOAD FILE BESAR (>20MB) — MELAMPAUI BATAS TELEGRAM
// ==============================================================================
//
// MASALAH
// Telegram Bot API membatasi bot mengunduh file maksimal 20MB. Tidak ada cara
// menaikkan batas ini pada bot biasa — itu batas server Telegram.
//
// TIGA SOLUSI YANG DISEDIAKAN MODUL INI
//
//  1. UNDUH DARI URL (paling praktis)
//     File sudah ada di internet (GitHub Release, S3, Google Drive direct link).
//     Bot memerintahkan VPS mengunduhnya langsung — tidak lewat Telegram.
//
//  2. UPLOAD BERTAHAP (chunk)
//     File besar dipecah di komputermu, dikirim per bagian lewat Telegram,
//     lalu disatukan kembali di VPS. Cocok bila file hanya ada di komputermu.
//
//  3. LOCAL BOT API SERVER (batas jadi 2GB)
//     Menjalankan server Telegram sendiri. Sekali setup, batas naik drastis.
//
// Modul ini juga menambahkan fitur PECAH file di VPS supaya hasil akhir dari
// ketiga metode bisa dikirim keluar lewat /getfile (batas 50MB per pesan).

import (
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// Batas keras Bot API untuk unduh.
	batasUnduhBotAPI = 20 * 1024 * 1024
	// Ukuran potongan saat upload bertahap. Di bawah 20MB agar aman.
	// 15MB memberi ruang untuk overhead multipart.
	ukuranChunk = 15 * 1024 * 1024
	// Berapa lama sesi chunk bertahan sebelum dianggap batal.
	chunkSesiTTL = 60 * time.Minute
)

// ==============================================================================
// 1️⃣ UNDUH DARI URL
// ==============================================================================

// unduhRegex memvalidasi URL yang boleh diunduh.
//
// Ini adalah batas KEAMANAN: URL diteruskan ke shell lewat curl, jadi karakter
// yang punya arti khusus di shell harus ditolak lebih dulu. Yang ditolak:
//
//	`      backtick (command substitution)
//	$      variabel / $(...)
//	; & |  pemisah perintah
//	> <    redireksi
//	' "    pembuka kutip yang bisa memutus argumen
//	\n \r spasi  pemecah argumen
//	( ) { }  pengelompokan shell
//	* ? [ ]  globbing
//	! # ~ ^    karakter lain yang bermakna di shell
//
// unduhRegex hanya memastikan bentuk kasar: skema, lalu isi tanpa spasi.
// Tidak ada karakter ASCII yang dilarang di sini — pembatasan keamanan
// ditangani karkaterTerlarangUnduh, karena URL selalu lewat shellQuote.
var unduhRegex = regexp.MustCompile(`^https?://[^\s]+$`)

// karkaterTerlarangUnduh memuat karakter yang TIDAK boleh ada di URL.
//
// PRINSIP: URL selalu dibungkus shellQuote sebelum masuk shell, jadi karakter
// seperti & ? ~ * yang berbahaya HANYA di luar kutip menjadi aman di dalamnya.
// Yang benar-benar harus ditolak hanyalah karakter yang bisa:
//
//  1. Memutus kutip tunggal → kutip tunggal itu sendiri, dan backslash.
//  2. Mengakhiri baris perintah → newline, carriage return, null byte.
//
// Tidak ada karakter lain yang bisa keluar dari 'kutip tunggal' di POSIX shell,
// sehingga daftar ini cukup untuk menutup injeksi.
const karkaterTerlarangUnduh = "'\\\n\r\x00"

// unduhMu mencegah beberapa unduhan berat berjalan bersamaan.
var unduhMu sync.Mutex

// ValidasiUnduhURL memeriksa URL sebelum dipakai di shell.
// Mengembalikan pesan kesalahan yang siap dikirim, atau "" bila aman.
//
// Model keamanan: URL masuk shell HANYA lewat shellQuote(), yang membungkusnya
// dalam kutip tunggal. Di dalam kutip tunggal POSIX, semua karakter kehilangan
// makna khususnya kecuali kutip tunggal itu sendiri. Jadi validasi ini cukup
// menolak: kutip tunggal, backslash (bisa membentuk '\”), dan karakter yang
// mengakhiri baris (\n, \r, \x00).
func ValidasiUnduhURL(raw string) string {
	if raw == "" {
		return "URL kosong."
	}
	if strings.TrimSpace(raw) != raw {
		return "URL tidak boleh diawali/diakhiri spasi."
	}
	if len(raw) > 2000 {
		return "URL terlalu panjang (maksimal 2000 karakter)."
	}

	// Tolak karakter yang bisa keluar dari kutip tunggal.
	if i := strings.IndexAny(raw, karkaterTerlarangUnduh); i >= 0 {
		r := raw[i]
		nama := fmt.Sprintf("%q", string(r))
		switch r {
		case '\n':
			nama = "baris baru"
		case '\r':
			nama = "carriage return"
		case 0:
			nama = "null byte"
		}
		return fmt.Sprintf("URL memuat karakter terlarang: %s", nama)
	}

	// Wajib skema http atau https.
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return "URL harus diawali <code>http://</code> atau <code>https://</code>."
	}

	// Bentuk umum: tanpa spasi, dan ada isi setelah skema.
	if !unduhRegex.MatchString(raw) {
		return "URL tidak valid."
	}

	return ""
}

// ValidasiUnduhTujuan memeriksa folder tujuan unduhan.
func ValidasiUnduhTujuan(dir string) (string, string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", "Folder tujuan kosong."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "Folder tujuan tidak valid."
	}
	abs = filepath.Clean(abs)

	st, err := os.Stat(abs)
	if err != nil {
		// Coba buat folder bila belum ada.
		if mkErr := os.MkdirAll(abs, 0755); mkErr != nil {
			return "", fmt.Sprintf("Folder tidak ada dan gagal dibuat: %v", mkErr)
		}
		return abs, ""
	}
	if !st.IsDir() {
		return "", "Tujuan bukan folder."
	}
	if !isDirWritable(abs) {
		return "", "Folder tidak bisa ditulis oleh bot. Jalankan bot sebagai root, atau pilih folder lain."
	}
	return abs, ""
}

// renderUnduhInfo menyusun panduan unduh-dari-URL.
func renderUnduhInfo(dir string) (string, *InlineKeyboardMarkup) {
	direktoriContoh := dir
	if direktoriContoh == "" {
		direktoriContoh = "/root"
	}

	text := "🔗 <b>Unduh File Langsung di VPS</b>\n" +
		"━━━━━━━━━━━━━━━━━━━━\n\n" +
		"Tidak ada batas 20MB. File diunduh <b>VPS → internet</b>, " +
		"tidak melewati Telegram.\n\n" +
		"<b>Cara pakai:</b>\n" +
		"<code>/unduh &lt;URL&gt; [nama-file]</code>\n\n" +
		"<b>Contoh:</b>\n" +
		"<code>/unduh https://contoh.com/data.zip</code>\n" +
		"<code>/unduh https://contoh.com/data.zip /root/arsip.zip</code>\n\n" +
		"📁 <b>Folder default:</b> <code>" + html.EscapeString(direktoriContoh) + "</code>\n" +
		"📏 <b>Batas:</b> hanya ruang disk VPS\n" +
		"⏱ <b>Timeout:</b> 30 menit\n\n" +
		"<i>Link Google Drive harus link direct download, bukan halaman preview.</i>"

	kb := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "📁 Buka Folder", CallbackData: "fm:o:" + getPathID(direktoriContoh) + ":0"},
				{Text: "🏠 Menu Utama", CallbackData: "hp:b"},
			},
		},
	}
	return text, kb
}

// UnduhDariURL mengunduh URL ke folder tujuan memakai curl di VPS.
// Mengembalikan (pathHasil, outputLog, error).
func UnduhDariURL(url string, tujuan string, namaFile string) (string, string, error) {
	// Amankan: tolak bila masih ada proses unduh lain.
	if !unduhMu.TryLock() {
		return "", "", fmt.Errorf("masih ada unduhan lain yang berjalan — tunggu selesai dulu")
	}
	defer unduhMu.Unlock()

	absTujuan, pesanErr := ValidasiUnduhTujuan(tujuan)
	if pesanErr != "" {
		return "", "", fmt.Errorf("%s", pesanErr)
	}

	// Tentukan nama file dari URL bila tidak diberikan.
	if strings.TrimSpace(namaFile) == "" {
		namaFile = namaDariURL(url)
	}
	namaAman, err := safeUploadPath(absTujuan, namaFile)
	if err != nil {
		return "", "", err
	}

	// Pakai curl dengan opsi keamanan:
	//   -f  gagal bila HTTP error (bukan menyimpan halaman error)
	//   -L  ikuti redirect
	//   -o  tulis ke file
	//   --retry  coba ulang pada gangguan jaringan
	cmdStr := fmt.Sprintf(
		"curl -fL --retry 3 --retry-delay 2 --connect-timeout 30 -o %s %s",
		shellQuote(namaAman), shellQuote(url))

	// Timeout panjang: file besar butuh waktu.
	code, stdout, stderr := runBashCommand(cmdStr, 1800)
	log := strings.TrimSpace(stdout + "\n" + stderr)

	if code != 0 {
		// Bersihkan file gagal/parsial.
		if fi, e := os.Stat(namaAman); e == nil && fi.Size() < 1024 {
			os.Remove(namaAman)
		}
		if log == "" {
			log = fmt.Sprintf("curl keluar dengan kode %d", code)
		}
		return "", log, fmt.Errorf("unduhan gagal")
	}

	fi, err := os.Stat(namaAman)
	if err != nil {
		return "", log, fmt.Errorf("file hasil tidak ditemukan: %w", err)
	}
	if fi.Size() == 0 {
		os.Remove(namaAman)
		return "", log, fmt.Errorf("file hasil kosong — periksa URL")
	}

	return namaAman, log, nil
}

// namaDariURL mengambil nama file dari URL.
func namaDariURL(url string) string {
	tanpaQuery := url
	if i := strings.IndexAny(tanpaQuery, "?#"); i >= 0 {
		tanpaQuery = tanpaQuery[:i]
	}
	tanpaQuery = strings.TrimSuffix(tanpaQuery, "/")
	base := filepath.Base(tanpaQuery)

	// Nama tidak berguna → pakai nama berbasis waktu.
	if base == "" || base == "." || base == "/" || !strings.Contains(base, ".") {
		if base == "" || base == "." || base == "/" {
			return fmt.Sprintf("unduhan_%d.bin", time.Now().Unix())
		}
	}
	if strings.Contains(base, "/") || base == ".." {
		return fmt.Sprintf("unduhan_%d.bin", time.Now().Unix())
	}
	return base
}

// shellQuote membungkus argumen agar aman dipakai di shell.
// ==============================================================================
// 2️⃣ UPLOAD BERTAHAP (CHUNK)
// ==============================================================================

// chunkSesi menyimpan keadaan upload bertahap per admin.
type chunkSesi struct {
	Dir        string
	NamaAsal   string
	TotalPart  int
	Diterima   int
	Dibuat     time.Time
	Diperbarui time.Time
}

var (
	chunkMu    sync.Mutex
	chunkSesis = make(map[int64]*chunkSesi)
)

// MulaiChunkSesi memulai sesi upload bertahap.
func MulaiChunkSesi(userID int64, dir string, namaAsal string, totalPart int) (*chunkSesi, error) {
	if totalPart < 2 || totalPart > 9999 {
		return nil, fmt.Errorf("jumlah bagian harus antara 2 dan 9999")
	}

	absDir, pesanErr := ValidasiUnduhTujuan(dir)
	if pesanErr != "" {
		return nil, fmt.Errorf("%s", pesanErr)
	}

	// Nama asli divalidasi lewat jalur aman yang sudah ada.
	if _, err := safeUploadPath(absDir, namaAsal); err != nil {
		return nil, err
	}

	// Folder penampung bagian-bagian.
	folderPart := filepath.Join(absDir, ".bvc_parts_"+sanitasiNama(namaAsal))
	if err := os.MkdirAll(folderPart, 0700); err != nil {
		return nil, fmt.Errorf("gagal membuat folder bagian: %w", err)
	}

	s := &chunkSesi{
		Dir:        folderPart,
		NamaAsal:   filepath.Base(namaAsal),
		TotalPart:  totalPart,
		Dibuat:     time.Now(),
		Diperbarui: time.Now(),
	}

	chunkMu.Lock()
	chunkSesis[userID] = s
	chunkMu.Unlock()
	return s, nil
}

// ChunkSesiAktif mengembalikan sesi chunk yang sedang berjalan.
func ChunkSesiAktif(userID int64) (*chunkSesi, bool) {
	chunkMu.Lock()
	defer chunkMu.Unlock()
	s, ok := chunkSesis[userID]
	if !ok {
		return nil, false
	}
	if time.Since(s.Diperbarui) > chunkSesiTTL {
		delete(chunkSesis, userID)
		return nil, false
	}
	return s, true
}

// TerimaChunk menyimpan satu bagian lalu mengembalikan kemajuan.
// Mengembalikan (diterima, total, selesai).
func TerimaChunk(userID int64, nomorPart int, data []byte) (int, int, bool, error) {
	s, ok := ChunkSesiAktif(userID)
	if !ok {
		return 0, 0, false, fmt.Errorf("tidak ada sesi upload bertahap yang aktif")
	}
	if nomorPart < 1 || nomorPart > s.TotalPart {
		return s.Diterima, s.TotalPart, false,
			fmt.Errorf("nomor bagian %d di luar rentang 1-%d", nomorPart, s.TotalPart)
	}

	// Nama bagian selalu 4 digit agar urutan penyatuan benar.
	pathPart := filepath.Join(s.Dir, fmt.Sprintf("part_%04d", nomorPart))
	if err := os.WriteFile(pathPart, data, 0600); err != nil {
		return s.Diterima, s.TotalPart, false, fmt.Errorf("gagal menyimpan bagian: %w", err)
	}

	chunkMu.Lock()
	s.Diterima++ // hitungan sederhana; penggantian bagian yang sama dihitung ulang saat gabung
	s.Diperbarui = time.Now()
	diterima, total := s.Diterima, s.TotalPart
	chunkMu.Unlock()

	return diterima, total, diterima >= total, nil
}

// GabungChunk menyatukan seluruh bagian menjadi satu file.
// Mengembalikan (pathHasil, jumlahByte, error).
func GabungChunk(userID int64) (string, int64, error) {
	s, ok := ChunkSesiAktif(userID)
	if !ok {
		return "", 0, fmt.Errorf("tidak ada sesi upload bertahap yang aktif")
	}

	folderInduk := filepath.Dir(s.Dir)
	hasil := filepath.Join(folderInduk, s.NamaAsal)

	out, err := os.Create(hasil)
	if err != nil {
		return "", 0, fmt.Errorf("gagal membuat file hasil: %w", err)
	}
	defer out.Close()

	var total int64
	for i := 1; i <= s.TotalPart; i++ {
		pathPart := filepath.Join(s.Dir, fmt.Sprintf("part_%04d", i))
		f, err := os.Open(pathPart)
		if err != nil {
			out.Close()
			os.Remove(hasil)
			return "", 0, fmt.Errorf("bagian %d belum diterima — kirim ulang bagian itu", i)
		}
		n, err := io.Copy(out, f)
		f.Close()
		if err != nil {
			out.Close()
			os.Remove(hasil)
			return "", 0, fmt.Errorf("gagal menggabungkan bagian %d: %w", i, err)
		}
		total += n
	}

	// Bersihkan folder bagian setelah berhasil.
	os.RemoveAll(s.Dir)

	chunkMu.Lock()
	delete(chunkSesis, userID)
	chunkMu.Unlock()

	return hasil, total, nil
}

// BatalkanChunkSesi membatalkan sesi dan menghapus bagian-bagiannya.
func BatalkanChunkSesi(userID int64) bool {
	chunkMu.Lock()
	s, ok := chunkSesis[userID]
	delete(chunkSesis, userID)
	chunkMu.Unlock()

	if !ok {
		return false
	}
	os.RemoveAll(s.Dir)
	return true
}

// sanitasiNama membersihkan nama untuk dipakai sebagai komponen folder.
func sanitasiNama(s string) string {
	base := filepath.Base(s)
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	hasil := b.String()
	if hasil == "" || hasil == "." || hasil == ".." {
		return fmt.Sprintf("upload_%d", time.Now().Unix())
	}
	if len(hasil) > 60 {
		hasil = hasil[:60]
	}
	return hasil
}

// ==============================================================================
// 3️⃣ PECAH & GABUNG FILE DI VPS
// ==============================================================================
// Berguna untuk mengirim file besar keluar lewat /getfile (batas 50MB/pesan),
// atau memecah file sebelum dikirim ke tempat lain.

// PecahFile memecah file menjadi bagian-bagian berukuran tetap.
// Mengembalikan (daftarBagian, error).
func PecahFile(pathFile string, ukuranBagianMB int) ([]string, error) {
	fi, err := os.Stat(pathFile)
	if err != nil {
		return nil, fmt.Errorf("file tidak ditemukan: %w", err)
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("itu folder, bukan file")
	}
	if ukuranBagianMB < 1 {
		ukuranBagianMB = 45
	}
	if fi.Size() <= int64(ukuranBagianMB)*1024*1024 {
		return nil, fmt.Errorf(
			"file hanya %s, tidak perlu dipecah (bagian %dMB)",
			formatBytes(fi.Size()), ukuranBagianMB)
	}

	// split menulis nama seperti namafile.part_aa, .part_ab, dst.
	prefix := filepath.Join(filepath.Dir(pathFile),
		filepath.Base(pathFile)+".part_")
	cmdStr := fmt.Sprintf("split -b %dM -a 3 %s %s",
		ukuranBagianMB, shellQuote(pathFile), shellQuote(prefix))

	code, out, errStr := runBashCommand(cmdStr, 900)
	if code != 0 {
		return nil, fmt.Errorf("gagal memecah: %s", strings.TrimSpace(out+" "+errStr))
	}

	// Kumpulkan hasil.
	matches, _ := filepath.Glob(prefix + "*")
	if len(matches) == 0 {
		return nil, fmt.Errorf("tidak ada bagian yang dihasilkan")
	}
	return matches, nil
}

// GabungFile menyatukan kembali bagian-bagian hasil PecahFile.
func GabungFile(pathPertama string, pathHasil string) (int64, error) {
	if _, err := os.Stat(pathPertama); err != nil {
		return 0, fmt.Errorf("bagian pertama tidak ditemukan: %w", err)
	}

	// Pola: buang 3 huruf terakhir dari suffix bagian.
	base := pathPertama
	if i := strings.LastIndex(base, ".part_"); i > 0 {
		base = base[:i+len(".part_")]
	}
	cmdStr := fmt.Sprintf("cat %s* > %s", shellQuote(base), shellQuote(pathHasil))

	code, out, errStr := runBashCommand(cmdStr, 900)
	if code != 0 {
		return 0, fmt.Errorf("gagal menggabungkan: %s", strings.TrimSpace(out+" "+errStr))
	}

	fi, err := os.Stat(pathHasil)
	if err != nil {
		return 0, fmt.Errorf("file hasil tidak ditemukan: %w", err)
	}
	return fi.Size(), nil
}

// ==============================================================================
// 🖼️ TAMPILAN BANTUAN
// ==============================================================================

// renderBantuanUploadBesar menyusun panduan lengkap upload >20MB.
func renderBantuanUploadBesar(dir string) (string, *InlineKeyboardMarkup) {
	if dir == "" {
		dir = getHomeDir()
	}

	text := "📦 <b>Upload File Besar (di atas 20MB)</b>\n" +
		"━━━━━━━━━━━━━━━━━━━━\n\n" +
		"Telegram Bot API membatasi bot mengunduh file maksimal <b>20MB</b>. " +
		"Batas ini tidak bisa dinaikkan pada bot biasa.\n\n" +
		"<b>Tiga cara melewatinya:</b>\n\n" +
		"<b>1️⃣ Unduh dari URL</b> <i>(paling praktis)</i>\n" +
		"File sudah ada di internet → VPS mengunduh sendiri.\n" +
		"<code>/unduh https://link/file.zip</code>\n\n" +
		"<b>2️⃣ Upload bertahap</b> <i>(file hanya di komputermu)</i>\n" +
		"Pecah file di komputermu, kirim per bagian.\n" +
		"<code>/chunk mulai data.zip 5</code>\n" +
		"<i>Pecah dengan: <code>split -b 15M data.zip data.zip.part_</code></i>\n\n" +
		"<b>3️⃣ Local Bot API Server</b> <i>(batas jadi 2GB)</i>\n" +
		"Jalankan server Telegram sendiri sekali setup.\n\n" +
		"💡 <b>Tips:</b> kirim file <code>.zip</code>/<code>.tar.gz</code> " +
		"— kompresi sering menurunkan ukuran di bawah 20MB.\n\n" +
		"📁 <b>Folder kerja:</b> <code>" + html.EscapeString(dir) + "</code>"

	kb := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "🔗 Unduh dari URL", CallbackData: "ub:url:" + getPathID(dir)},
				{Text: "🧩 Upload Bertahap", CallbackData: "ub:chunk:" + getPathID(dir)},
			},
			{
				{Text: "📖 Cara Setup Server 2GB", CallbackData: "ub:localserver"},
			},
			{
				{Text: "📁 Buka Folder", CallbackData: "fm:o:" + getPathID(dir) + ":0"},
				{Text: "🏠 Menu Utama", CallbackData: "hp:b"},
			},
		},
	}
	return text, kb
}

// renderPanduanLocalServer menyusun panduan Local Bot API Server.
func renderPanduanLocalServer() (string, *InlineKeyboardMarkup) {
	text := "🚀 <b>Local Bot API Server — Batas 2GB</b>\n" +
		"━━━━━━━━━━━━━━━━━━━━\n\n" +
		"Telegram menyediakan server API yang bisa dijalankan sendiri. " +
		"Batas unduh naik dari <b>20MB → 2GB</b>.\n\n" +
		"<b>Langkah setup (sekali saja):</b>\n\n" +
		"<b>1.</b> Dapatkan <code>api_id</code> &amp; <code>api_hash</code> di " +
		"<a href=\"https://my.telegram.org\">my.telegram.org</a>\n\n" +
		"<b>2.</b> Unduh server Telegram:\n" +
		"<code>/term curl -fL https://github.com/tdlib/telegram-bot-api/releases/latest/download/telegram-bot-api -o /usr/local/bin/telegram-bot-api</code>\n\n" +
		"<b>3.</b> Izin eksekusi:\n" +
		"<code>/term chmod +x /usr/local/bin/telegram-bot-api</code>\n\n" +
		"<b>4.</b> Jalankan server:\n" +
		"<code>/term telegram-bot-api --api-id=ID --api-hash=HASH --local</code>\n\n" +
		"<b>5.</b> Bot memakai port lokal:\n" +
		"<code>API_URL=http://127.0.0.1:8081/bot</code> di file <code>.env</code>\n\n" +
		"⚠️ <b>Catatan:</b> server ini butuh RAM ~200MB dan berjalan permanen. " +
		"Untuk VPS kecil, cara 1️⃣ dan 2️⃣ lebih hemat.\n\n" +
		"<i>Setelah ini, upload lewat caption/tombol biasa sudah bisa 2GB.</i>"

	kb := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "⬅️ Kembali", CallbackData: "ub:menu"},
			},
		},
	}
	return text, kb
}

// ==============================================================================
// 🧩 HANDLER CALLBACK (ub:*)
// ==============================================================================

// handleUploadBesarCallback menjalankan aksi dari menu upload besar.
func handleUploadBesarCallback(userID int64, chatID int64, action string) (string, *InlineKeyboardMarkup) {
	switch {
	case action == "menu" || action == "":
		return renderBantuanUploadBesar("")

	case action == "localserver":
		return renderPanduanLocalServer()

	case strings.HasPrefix(action, "url:"):
		dir := getPathByID(strings.TrimPrefix(action, "url:"))
		if dir == "" {
			dir = getHomeDir()
		}
		text := "🔗 <b>Unduh dari URL</b>\n" +
			"━━━━━━━━━━━━━━━━━━━━\n\n" +
			"Kirim perintah dengan URL yang ingin diunduh:\n\n" +
			"<code>/unduh &lt;URL&gt;</code>\n" +
			"<code>/unduh &lt;URL&gt; &lt;path-tujuan&gt;</code>\n\n" +
			"<b>Contoh:</b>\n" +
			"<code>/unduh https://contoh.com/data.zip</code>\n" +
			"<code>/unduh https://contoh.com/data.zip /root/arsip.zip</code>\n\n" +
			"📁 <b>Folder default:</b> <code>" + html.EscapeString(dir) + "</code>\n\n" +
			"<i>VPS mengunduh langsung dari internet — tidak ada batas 20MB.</i>"

		kb := &InlineKeyboardMarkup{
			InlineKeyboard: [][]InlineKeyboardButton{
				{
					{Text: "⬅️ Kembali", CallbackData: "ub:menu"},
					{Text: "📁 Folder", CallbackData: "fm:o:" + getPathID(dir) + ":0"},
				},
			},
		}
		return text, kb

	case strings.HasPrefix(action, "chunk:"):
		dir := getPathByID(strings.TrimPrefix(action, "chunk:"))
		if dir == "" {
			dir = getHomeDir()
		}
		return renderPanduanChunk(dir)

	case strings.HasPrefix(action, "chunkmulai:"):
		// Format: chunkmulai:<dirID>:<total>
		bagian := strings.Split(strings.TrimPrefix(action, "chunkmulai:"), ":")
		if len(bagian) < 2 {
			return "❌ Data tidak lengkap.", backToTerminalKeyboard()
		}
		dir := getPathByID(bagian[0])
		total := 0
		fmt.Sscanf(bagian[1], "%d", &total)
		if dir == "" || total < 2 {
			return "❌ Folder atau jumlah bagian tidak valid.", backToTerminalKeyboard()
		}
		return fmt.Sprintf(
			"🧩 <b>Siap menerima %d bagian</b>\n"+
				"━━━━━━━━━━━━━━━━━━━━\n\n"+
				"📁 Folder: <code>%s</code>\n\n"+
				"Kirim bagian-bagiannya sebagai <b>dokumen</b>, berurutan. "+
				"Setelah semua terkirim, bot menyatukannya otomatis.\n\n"+
				"<i>Batalkan dengan /chunk batal</i>",
			total, html.EscapeString(dir)), backToTerminalKeyboard()

	default:
		return renderBantuanUploadBesar("")
	}
}

// renderPanduanChunk menyusun panduan upload bertahap.
func renderPanduanChunk(dir string) (string, *InlineKeyboardMarkup) {
	text := "🧩 <b>Upload Bertahap (Chunk)</b>\n" +
		"━━━━━━━━━━━━━━━━━━━━\n\n" +
		"Untuk file besar yang hanya ada di komputermu.\n\n" +
		"<b>1. Di komputermu</b> — pecah file:\n" +
		"<code>split -b 15M data.zip data.zip.part_</code>\n\n" +
		"<b>2. Hitung jumlah bagian:</b>\n" +
		"<code>ls data.zip.part_* | wc -l</code>\n\n" +
		"<b>3. Di bot</b> — mulai sesi:\n" +
		"<code>/chunk mulai data.zip 5</code>\n" +
		"<i>(5 = jumlah bagian dari langkah 2)</i>\n\n" +
		"<b>4.</b> Kirim tiap bagian <code>data.zip.part_aa</code>, " +
		"<code>part_ab</code>, … sebagai dokumen. Urutan tidak masalah.\n\n" +
		"<b>5.</b> Setelah bagian terakhir, bot menyatukan otomatis.\n\n" +
		"📁 <b>Folder tujuan:</b> <code>" + html.EscapeString(dir) + "</code>\n" +
		"📏 <b>Ukuran per bagian:</b> 15MB (aman di bawah batas 20MB)\n\n" +
		"⚠️ <i>Di Linux/macOS gunakan <code>split</code>. " +
		"Di Windows gunakan 7-Zip dengan mode \"Split to volumes\".</i>"

	kb := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "⬅️ Kembali", CallbackData: "ub:menu"},
				{Text: "📁 Folder", CallbackData: "fm:o:" + getPathID(dir) + ":0"},
			},
		},
	}
	return text, kb
}

// ==============================================================================
// 🧹 PEMBERSIH
// ==============================================================================

// bersihkanChunkKedaluwarsa menghapus sesi chunk yang lama.
func bersihkanChunkKedaluwarsa() {
	chunkMu.Lock()
	defer chunkMu.Unlock()
	for uid, s := range chunkSesis {
		if time.Since(s.Diperbarui) > chunkSesiTTL {
			os.RemoveAll(s.Dir)
			delete(chunkSesis, uid)
		}
	}
}

// ==============================================================================
// 🎛️ HANDLER PERINTAH
// ==============================================================================

// handleUnduhURL menangani /unduh <URL> [tujuan].
func handleUnduhURL(chatID int64, userID int64, rawText string) {
	fields := strings.Fields(rawText)
	if len(fields) < 2 {
		text, kb := renderUnduhInfo(getHomeDir())
		sendPanel(userID, chatID, text, kb)
		return
	}

	url := fields[1]
	if pesan := ValidasiUnduhURL(url); pesan != "" {
		sendPanel(userID, chatID,
			"❌ <b>URL tidak valid.</b>\n\n<pre>"+html.EscapeString(pesan)+"</pre>",
			&InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
				{{Text: "⬅️ Bantuan", CallbackData: "ub:menu"}},
			}})
		return
	}

	// Tujuan: argumen ke-3, atau folder upload aktif, atau home.
	tujuan := ""
	if len(fields) >= 3 {
		tujuan = strings.TrimSpace(fields[2])
	} else if dir, ok := GetUploadTarget(userID); ok {
		tujuan = dir
	} else {
		tujuan = getHomeDir()
	}

	sendPanel(userID, chatID,
		"⏳ <b>Mengunduh…</b>\n━━━━━━━━━━━━━━━━━━━━\n\n"+
			"🔗 <code>"+html.EscapeString(truncateStr(url, 80))+"</code>\n"+
			"📁 <code>"+html.EscapeString(tujuan)+"</code>\n\n"+
			"<i>VPS mengunduh langsung dari internet. Tidak ada batas 20MB.\n"+
			"File besar bisa memerlukan beberapa menit.</i>", nil)

	go func() {
		hasil, logUnduh, err := UnduhDariURL(url, tujuan, "")
		if err != nil {
			pesan := "❌ <b>Unduhan Gagal</b>\n━━━━━━━━━━━━━━━━━━━━\n\n" +
				"🔗 <code>" + html.EscapeString(truncateStr(url, 80)) + "</code>\n\n" +
				"<b>Penyebab:</b>\n<pre>" + html.EscapeString(truncateStr(err.Error()+"\n"+logUnduh, 900)) + "</pre>\n\n" +
				"<b>Periksa:</b>\n" +
				"• URL masih bisa diakses?\n" +
				"• Link direct download (bukan halaman preview)?\n" +
				"• Ruang disk cukup? <code>/term df -h</code>"
			sendPanel(userID, chatID, pesan,
				&InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
					{{Text: "🔁 Coba Lagi", CallbackData: "ub:url:" + getPathID(tujuan)}},
				}})
			return
		}

		fi, _ := os.Stat(hasil)
		var ukuran int64
		if fi != nil {
			ukuran = fi.Size()
		}

		judul := "✅ <b>Unduhan Selesai</b>"
		if ukuran > batasUnduhBotAPI {
			judul = "✅ <b>Unduhan Selesai</b> <i>(di atas 20MB — berhasil!)</i>"
		}

		pesan := judul + "\n━━━━━━━━━━━━━━━━━━━━\n\n" +
			"📄 <b>Nama:</b> <code>" + html.EscapeString(filepath.Base(hasil)) + "</code>\n" +
			"📁 <b>Folder:</b> <code>" + html.EscapeString(filepath.Dir(hasil)) + "</code>\n" +
			"📦 <b>Ukuran:</b> <code>" + formatBytes(ukuran) + "</code>\n" +
			"🕒 <b>Waktu:</b> <code>" + time.Now().Format("2006-01-02 15:04:05") + "</code>"

		sendPanel(userID, chatID, pesan,
			&InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
				{{Text: "📁 Buka Folder", CallbackData: "fm:o:" + getPathID(filepath.Dir(hasil)) + ":0"}},
				{{Text: "🔗 Unduh Lagi", CallbackData: "ub:url:" + getPathID(filepath.Dir(hasil))}},
			}})
	}()
}

// handleChunk menangani /chunk mulai|batal|status.
func handleChunk(chatID int64, userID int64, rawText string) {
	fields := strings.Fields(rawText)
	sub := ""
	if len(fields) >= 2 {
		sub = strings.ToLower(fields[1])
	}

	switch sub {
	case "mulai", "start":
		// /chunk mulai <nama-file> <jumlah-bagian>
		if len(fields) < 4 {
			sendPanel(userID, chatID,
				"❌ Format: <code>/chunk mulai &lt;nama-file&gt; &lt;jumlah-bagian&gt;</code>\n\n"+
					"Contoh: <code>/chunk mulai data.zip 5</code>\n\n"+
					"<i>Jumlah bagian didapat dari: <code>ls data.zip.part_* | wc -l</code></i>",
				&InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
					{{Text: "📖 Panduan Lengkap", CallbackData: "ub:chunk:" + getPathID(getHomeDir())}},
				}})
			return
		}
		namaFile := fields[2]
		total := 0
		if _, err := fmt.Sscanf(fields[3], "%d", &total); err != nil || total < 2 {
			sendPanel(userID, chatID, "❌ Jumlah bagian tidak valid (minimal 2).", nil)
			return
		}

		tujuan := getHomeDir()
		if dir, ok := GetUploadTarget(userID); ok {
			tujuan = dir
		}

		s, err := MulaiChunkSesi(userID, tujuan, namaFile, total)
		if err != nil {
			sendPanel(userID, chatID,
				"❌ <b>Gagal memulai sesi:</b>\n<pre>"+html.EscapeString(err.Error())+"</pre>", nil)
			return
		}

		sendPanel(userID, chatID, fmt.Sprintf(
			"🧩 <b>Sesi upload bertahap dimulai</b>\n"+
				"━━━━━━━━━━━━━━━━━━━━\n\n"+
				"📄 <b>Nama akhir:</b> <code>%s</code>\n"+
				"📁 <b>Folder:</b> <code>%s</code>\n"+
				"🧩 <b>Bagian:</b> <code>%d</code>\n\n"+
				"<b>Sekarang kirim tiap bagian sebagai dokumen.</b>\n"+
				"Urutan tidak masalah. Setelah bagian terakhir, bot menyatukan otomatis.\n\n"+
				"<i>Batalkan: <code>/chunk batal</code> · Status: <code>/chunk status</code></i>",
			html.EscapeString(s.NamaAsal),
			html.EscapeString(filepath.Dir(s.Dir)),
			s.TotalPart), nil)

	case "batal", "cancel", "stop":
		if BatalkanChunkSesi(userID) {
			sendPanel(userID, chatID, "🚫 <b>Sesi upload bertahap dibatalkan.</b>\n\nBagian yang sudah dikirim dihapus.", nil)
		} else {
			sendPanel(userID, chatID, "ℹ️ Tidak ada sesi upload bertahap yang aktif.", nil)
		}

	case "status", "info":
		s, ok := ChunkSesiAktif(userID)
		if !ok {
			sendPanel(userID, chatID, "ℹ️ Tidak ada sesi upload bertahap yang aktif.", nil)
			return
		}
		// Hitung bagian yang benar-benar ada di disk.
		matches, _ := filepath.Glob(filepath.Join(s.Dir, "part_*"))
		sendPanel(userID, chatID, fmt.Sprintf(
			"🧩 <b>Status Sesi Upload Bertahap</b>\n"+
				"━━━━━━━━━━━━━━━━━━━━\n\n"+
				"📄 <b>Nama akhir:</b> <code>%s</code>\n"+
				"📥 <b>Diterima:</b> <code>%d / %d</code>\n"+
				"⏱ <b>Umur sesi:</b> <code>%s</code>\n\n"+
				"<i>Bagian yang hilang harus dikirim ulang sebelum penyatuan.</i>",
			html.EscapeString(s.NamaAsal),
			len(matches), s.TotalPart,
			time.Since(s.Dibuat).Truncate(time.Second)), nil)

	default:
		text, kb := renderPanduanChunk(getHomeDir())
		sendPanel(userID, chatID, text, kb)
	}
}

// handlePecahFile menangani /pecah <file> [ukuranMB].
func handlePecahFile(chatID int64, userID int64, rawText string) {
	fields := strings.Fields(rawText)
	if len(fields) < 2 {
		sendPanel(userID, chatID,
			"🧩 <b>Pecah File</b>\n━━━━━━━━━━━━━━━━━━━━\n\n"+
				"Berguna untuk mengirim file besar keluar lewat <code>/getfile</code> "+
				"(batas 50MB per pesan) atau memindahkan file.\n\n"+
				"<b>Cara pakai:</b>\n"+
				"<code>/pecah &lt;file&gt; [ukuran-MB]</code>\n\n"+
				"<b>Contoh:</b>\n"+
				"<code>/pecah /root/backup.tar.gz</code>\n"+
				"<code>/pecah /root/backup.tar.gz 45</code>\n\n"+
				"<i>Ukuran default: 45MB</i>", nil)
		return
	}

	pathFile := strings.TrimSpace(fields[1])
	ukuran := 45
	if len(fields) >= 3 {
		fmt.Sscanf(fields[2], "%d", &ukuran)
	}

	sendPanel(userID, chatID, "🧩 <b>Memecah file…</b>\n\nMohon tunggu.", nil)

	bagian, err := PecahFile(pathFile, ukuran)
	if err != nil {
		sendPanel(userID, chatID,
			"❌ <b>Gagal memecah:</b>\n<pre>"+html.EscapeString(err.Error())+"</pre>", nil)
		return
	}

	var total int64
	for _, b := range bagian {
		if fi, e := os.Stat(b); e == nil {
			total += fi.Size()
		}
	}

	// Tampilkan beberapa bagian pertama sebagai contoh nama.
	var daftar []string
	for i, b := range bagian {
		if i >= 5 {
			daftar = append(daftar, fmt.Sprintf("<i>… dan %d bagian lain</i>", len(bagian)-5))
			break
		}
		fi, _ := os.Stat(b)
		var uk int64
		if fi != nil {
			uk = fi.Size()
		}
		daftar = append(daftar, fmt.Sprintf("• <code>%s</code> (%s)",
			html.EscapeString(filepath.Base(b)), formatBytes(uk)))
	}

	pesan := fmt.Sprintf(
		"✅ <b>File Dipecah</b>\n"+
			"━━━━━━━━━━━━━━━━━━━━\n\n"+
			"📦 <b>Total bagian:</b> <code>%d</code>\n"+
			"💾 <b>Total ukuran:</b> <code>%s</code>\n"+
			"📁 <b>Folder:</b> <code>%s</code>\n\n"+
			"<b>Bagian:</b>\n%s\n\n"+
			"<b>Menggabungkan kembali</b> (di VPS atau komputer lain):\n"+
			"<code>cat %s.part_* &gt; hasil</code>\n"+
			"Atau lewat bot: <code>/gabung &lt;bagian-pertama&gt; &lt;hasil&gt;</code>",
		len(bagian), formatBytes(total),
		html.EscapeString(filepath.Dir(pathFile)),
		strings.Join(daftar, "\n"),
		html.EscapeString(filepath.Base(pathFile)))

	dir := filepath.Dir(pathFile)
	sendPanel(userID, chatID, pesan,
		&InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
			{{Text: "📁 Buka Folder", CallbackData: "fm:o:" + getPathID(dir) + ":0"}},
		}})
}

// handleGabungFile menangani /gabung <bagian-pertama> <hasil>.
func handleGabungFile(chatID int64, userID int64, rawText string) {
	fields := strings.Fields(rawText)
	if len(fields) < 3 {
		sendPanel(userID, chatID,
			"🧩 <b>Gabung File</b>\n━━━━━━━━━━━━━━━━━━━━\n\n"+
				"Menyatukan bagian hasil <code>/pecah</code>.\n\n"+
				"<b>Cara pakai:</b>\n"+
				"<code>/gabung &lt;bagian-pertama&gt; &lt;file-hasil&gt;</code>\n\n"+
				"<b>Contoh:</b>\n"+
				"<code>/gabung /root/backup.tar.gz.part_aaa /root/backup.tar.gz</code>", nil)
		return
	}

	pertama := strings.TrimSpace(fields[1])
	hasil := strings.TrimSpace(fields[2])

	ukuran, err := GabungFile(pertama, hasil)
	if err != nil {
		sendPanel(userID, chatID,
			"❌ <b>Gagal menggabungkan:</b>\n<pre>"+html.EscapeString(err.Error())+"</pre>", nil)
		return
	}

	dir := filepath.Dir(hasil)
	sendPanel(userID, chatID, fmt.Sprintf(
		"✅ <b>File Digabungkan</b>\n"+
			"━━━━━━━━━━━━━━━━━━━━\n\n"+
			"📄 <b>Hasil:</b> <code>%s</code>\n"+
			"📦 <b>Ukuran:</b> <code>%s</code>",
		html.EscapeString(hasil), formatBytes(ukuran)),
		&InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
			{{Text: "📁 Buka Folder", CallbackData: "fm:o:" + getPathID(dir) + ":0"}},
		}})
}

// ==============================================================================
// 🧩 MENERIMA BAGIAN CHUNK
// ==============================================================================

// nomorBagianDariNama mengambil nomor bagian dari nama file yang dikirim.
//
// Pola yang dikenali:
//
//	data.zip.part_001 → 1     (split -b dengan suffix angka)
//	data.zip.part_aa  → 1     (split -b default, basis-26: aa, ab, …, az, ba)
//	data-002.bin      → 2     (angka di tengah/nama)
//	archivo.10        → 10    (angka di akhir)
func nomorBagianDariNama(nama string) int {
	base := strings.ToLower(filepath.Base(nama))

	// 1. Pola dengan penanda "part".
	for _, pemisah := range []string{"part_", "part-", "part."} {
		i := strings.LastIndex(base, pemisah)
		if i < 0 {
			continue
		}
		angka := base[i+len(pemisah):]
		if angka == "" {
			continue
		}

		// Angka di awal (mis. "001", "12")
		digit := ""
		for _, r := range angka {
			if r >= '0' && r <= '9' {
				digit += string(r)
			} else {
				break
			}
		}
		if digit != "" {
			n := 0
			fmt.Sscanf(digit, "%d", &n)
			if n > 0 {
				return n
			}
		}

		// Huruf dari `split`: aa=1, ab=2, …, az=26, ba=27.
		// Ini basis-26 dengan 'a'=1. Penting: +1 hanya pada hasil akhir,
		// bukan di setiap karakter — kalau tidak "aa" jadi 27, bukan 1.
		if len(angka) > 0 {
			n := 0
			valid := true
			for _, r := range angka {
				if r < 'a' || r > 'z' {
					valid = false
					break
				}
				n = n*26 + int(r-'a')
			}
			if valid {
				return n + 1
			}
		}
	}

	// 2. Angka di akhir nama (mis. "data.1", "arsip-003").
	if n := angkaDiAkhir(base); n > 0 {
		return n
	}

	// 3. Angka yang ditutup pemisah (mis. "data-002.bin", "file_05.tar").
	//    Diambil dari kanan supaya "v2-data-003.bin" → 3, bukan 2.
	trimmed := base
	if titik := strings.LastIndex(trimmed, "."); titik > 0 {
		trimmed = trimmed[:titik]
	}
	for i := len(trimmed) - 1; i >= 0; i-- {
		if trimmed[i] < '0' || trimmed[i] > '9' {
			continue
		}
		// Mundur sampai awal deretan angka.
		akhir := i
		for i > 0 && trimmed[i-1] >= '0' && trimmed[i-1] <= '9' {
			i--
		}
		n := 0
		fmt.Sscanf(trimmed[i:akhir+1], "%d", &n)
		if n > 0 {
			return n
		}
	}

	return 0
}

// angkaDiAkhir mengembalikan deretan angka di ujung string, atau 0.
func angkaDiAkhir(s string) int {
	angka := ""
	for i := len(s) - 1; i >= 0; i-- {
		r := s[i]
		if r >= '0' && r <= '9' {
			angka = string(r) + angka
			continue
		}
		break
	}
	if angka == "" {
		return 0
	}
	n := 0
	fmt.Sscanf(angka, "%d", &n)
	return n
}

// handleChunkPart menerima satu bagian dari upload bertahap.
func handleChunkPart(chatID int64, userID int64, doc *Document, s *chunkSesi) {
	// Validasi ukuran bagian (harus di bawah batas Bot API).
	if msg := checkUploadSize(doc.FileSize); msg != "" {
		sendPanel(userID, chatID,
			"❌ <b>Bagian terlalu besar.</b>\n\n"+
				"Bagian harus di bawah <code>"+formatBytes(batasUnduhBotAPI)+"</code>.\n"+
				"Pecah ulang dengan ukuran lebih kecil (mis. <code>split -b 15M</code>).\n\n"+
				"<i>Sesi masih aktif. Kirim bagian yang benar.</i>", nil)
		return
	}

	nomor := nomorBagianDariNama(doc.FileName)
	if nomor < 1 || nomor > s.TotalPart {
		sendPanel(userID, chatID, fmt.Sprintf(
			"⚠️ <b>Tidak bisa mengenali nomor bagian</b>\n\n"+
				"📄 Nama file: <code>%s</code>\n\n"+
				"<b>Nama harus memuat nomor bagian</b>, contoh:\n"+
				"• <code>data.zip.part_001</code>\n"+
				"• <code>data.zip.part_aa</code>\n"+
				"• <code>data-1.bin</code>\n\n"+
				"<i>Sesi menerima %d bagian (nomor 1-%d).</i>",
			html.EscapeString(doc.FileName), s.TotalPart, s.TotalPart), nil)
		return
	}

	// Unduh bagian ke file sementara lalu pindahkan ke folder sesi.
	tmp, err := os.CreateTemp("", "bvc_chunk_*")
	if err != nil {
		sendPanel(userID, chatID, "❌ Gagal menyiapkan penampung: "+html.EscapeString(err.Error()), nil)
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	written, err := downloadTelegramFile(doc.FileID, tmpPath)
	if err != nil {
		sendPanel(userID, chatID,
			"❌ <b>Gagal mengunduh bagian %d:</b>\n<pre>"+html.EscapeString(err.Error())+"</pre>", nil)
		return
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		sendPanel(userID, chatID, "❌ Gagal membaca bagian: "+html.EscapeString(err.Error()), nil)
		return
	}

	diterima, total, selesai, err := TerimaChunk(userID, nomor, data)
	if err != nil {
		sendPanel(userID, chatID, "❌ "+html.EscapeString(err.Error()), nil)
		return
	}

	// Tampilkan kemajuan sebagai bar.
	persen := diterima * 100 / total
	bar := strings.Repeat("█", persen/5) + strings.Repeat("░", 20-persen/5)

	if !selesai {
		sendPanel(userID, chatID, fmt.Sprintf(
			"🧩 <b>Bagian %d diterima</b>\n"+
				"━━━━━━━━━━━━━━━━━━━━\n\n"+
				"<code>%s</code>\n"+
				"<b>%d / %d</b> bagian (%d%%)\n\n"+
				"📦 Bagian ini: <code>%s</code>\n\n"+
				"<i>Kirim bagian berikutnya.</i>",
			nomor, bar, diterima, total, persen, formatBytes(written)), nil)
		return
	}

	// Semua bagian lengkap — gabungkan.
	sendPanel(userID, chatID,
		"🧩 <b>Semua bagian diterima — menggabungkan…</b>\n\nMohon tunggu.", nil)

	hasil, ukuran, err := GabungChunk(userID)
	if err != nil {
		sendPanel(userID, chatID,
			"❌ <b>Gagal menggabungkan:</b>\n<pre>"+html.EscapeString(err.Error())+"</pre>\n\n"+
				"<i>Sesi masih aktif. Kirim ulang bagian yang hilang, lalu coba lagi.</i>", nil)
		return
	}

	dir := filepath.Dir(hasil)
	sendPanel(userID, chatID, fmt.Sprintf(
		"✅ <b>Upload Bertahap Selesai</b>\n"+
			"━━━━━━━━━━━━━━━━━━━━\n\n"+
			"📄 <b>Nama:</b> <code>%s</code>\n"+
			"📁 <b>Folder:</b> <code>%s</code>\n"+
			"📦 <b>Ukuran:</b> <code>%s</code>\n"+
			"🧩 <b>Dari:</b> <code>%d bagian</code>\n"+
			"🕒 <b>Waktu:</b> <code>%s</code>",
		html.EscapeString(filepath.Base(hasil)),
		html.EscapeString(dir),
		formatBytes(ukuran),
		total,
		time.Now().Format("2006-01-02 15:04:05")),
		&InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
			{{Text: "📁 Buka Folder", CallbackData: "fm:o:" + getPathID(dir) + ":0"}},
			{{Text: "📤 Upload Lagi", CallbackData: "fm:up:" + getPathID(dir)}},
		}})
}
