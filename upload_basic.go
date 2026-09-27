package main

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ==============================================================================
// 📤 UPLOAD FILE KE VPS — MODE TOMBOL
// ==============================================================================
// Sebelumnya upload hanya bisa lewat caption (harus hapal path dan ketik manual).
// Modul ini menambahkan alur berbasis tombol:
//
//   1. Jelajah folder di file manager
//   2. Tekan "📤 Upload ke Sini" → bot mengingat folder itu
//   3. Kirim file apa pun ke chat → tersimpan ke folder tersebut
//
// Selain itu modul ini menutup dua masalah pada alur lama:
//
//   - VALIDASI UKURAN: Telegram Bot API membatasi 20MB. Tanpa cek awal, file
//     besar akan menggantung sampai timeout 120 detik lalu gagal tanpa sebab jelas.
//   - KONFIRMASI OVERWRITE: file bernama sama sebelumnya tertimpa diam-diam.

const (
	// Batas unduh Telegram Bot API untuk bot biasa (bukan Local Bot API Server).
	telegramMaxDownload = 20 * 1024 * 1024
	// Batas unggah ke Telegram (untuk /getfile).
	telegramMaxUpload = 50 * 1024 * 1024
	// Berapa lama mode upload bertahan sebelum dianggap kedaluwarsa.
	uploadModeTTL = 15 * time.Minute
)

// uploadTarget menyimpan folder tujuan upload per admin.
type uploadTarget struct {
	Dir      string
	SetAt    time.Time
	Overwite bool // bila true, izinkan menimpa tanpa tanya
}

var (
	uploadMu      sync.Mutex
	uploadTargets = make(map[int64]*uploadTarget)
)

// SetUploadTarget mengaktifkan mode upload ke folder tertentu.
func SetUploadTarget(userID int64, dir string) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	uploadMu.Lock()
	uploadTargets[userID] = &uploadTarget{Dir: filepath.Clean(abs), SetAt: time.Now()}
	uploadMu.Unlock()
}

// GetUploadTarget mengembalikan folder tujuan upload yang aktif, bila ada.
// Target yang sudah kedaluwarsa otomatis dihapus.
func GetUploadTarget(userID int64) (string, bool) {
	uploadMu.Lock()
	defer uploadMu.Unlock()
	t, ok := uploadTargets[userID]
	if !ok {
		return "", false
	}
	if time.Since(t.SetAt) > uploadModeTTL {
		delete(uploadTargets, userID)
		return "", false
	}
	return t.Dir, true
}

// ClearUploadTarget mematikan mode upload.
func ClearUploadTarget(userID int64) {
	uploadMu.Lock()
	delete(uploadTargets, userID)
	uploadMu.Unlock()
}

// AllowOverwrite menandai bahwa admin menyetujui penimpaan file di folder ini.
func AllowOverwrite(userID int64) {
	uploadMu.Lock()
	if t, ok := uploadTargets[userID]; ok {
		t.Overwite = true
	}
	uploadMu.Unlock()
}

// IsOverwriteAllowed melaporkan apakah penimpaan sudah disetujui.
func IsOverwriteAllowed(userID int64) bool {
	uploadMu.Lock()
	defer uploadMu.Unlock()
	t, ok := uploadTargets[userID]
	return ok && t.Overwite
}

// ==============================================================================
// 📐 VALIDASI
// ==============================================================================

// checkUploadSize memvalidasi ukuran file sebelum diunduh.
// Mengembalikan pesan kesalahan yang siap dikirim ke pengguna, atau "" bila lolos.
func checkUploadSize(fileSize int64) string {
	if fileSize <= 0 {
		// Ukuran tidak diketahui — coba saja, Telegram akan menolak bila terlalu besar.
		return ""
	}
	if fileSize > telegramMaxDownload {
		return fmt.Sprintf(
			"❌ <b>File terlalu besar untuk diunduh bot.</b>\n\n"+
				"📦 Ukuran file: <code>%s</code>\n"+
				"📏 Batas Bot API: <code>%s</code>\n\n"+
				"<b>Penyebab:</b> Telegram Bot API hanya mengizinkan bot mengunduh "+
				"file maksimal 20MB.\n\n"+
				"<b>Alternatif:</b>\n"+
				"1. Upload ke penyimpanan lain, lalu unduh di VPS:\n"+
				"   <code>/term curl -L -o /path/file URL</code>\n"+
				"2. Kompres dulu, lalu kirim versi kecilnya\n"+
				"3. Pakai <code>scp</code>/<code>rsync</code> dari komputermu",
			formatBytes(fileSize), formatBytes(telegramMaxDownload))
	}
	return ""
}

// safeUploadPath memastikan nama file tidak keluar dari folder tujuan.
// Mencegah nama seperti "../../etc/passwd" menulis ke luar folder yang dipilih.
func safeUploadPath(dir string, fileName string) (string, error) {
	// Telegram bisa mengirim nama dengan komponen path — ambil basename-nya saja.
	base := filepath.Base(filepath.Clean(fileName))

	// Nama kosong, ".", ".." atau berisi pemisah path tidak diterima.
	if base == "" || base == "." || base == ".." || base == string(filepath.Separator) {
		return "", fmt.Errorf("nama file tidak valid: %q", fileName)
	}
	if strings.ContainsAny(base, "/\x00") {
		return "", fmt.Errorf("nama file mengandung karakter terlarang: %q", fileName)
	}

	target := filepath.Join(dir, base)

	// Sabuk pengaman terakhir: pastikan hasilnya benar-benar di dalam dir.
	rel, err := filepath.Rel(dir, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path keluar dari folder tujuan: %q", fileName)
	}
	return target, nil
}

// ==============================================================================
// 🎛️ TAMPILAN MODE UPLOAD
// ==============================================================================

// renderUploadPrompt menyiapkan pesan instruksi upload untuk folder tertentu.
func renderUploadPrompt(dir string) (string, *InlineKeyboardMarkup) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	abs = filepath.Clean(abs)

	// Pastikan folder ada dan bisa ditulis.
	status := ""
	if st, err := os.Stat(abs); err != nil {
		status = "❌ <i>Folder tidak ditemukan.</i>\n\n"
	} else if !st.IsDir() {
		status = "❌ <i>Itu bukan folder.</i>\n\n"
	} else if !isDirWritable(abs) {
		status = "⚠️ <b>Folder tidak bisa ditulis oleh bot.</b>\n" +
			"<i>Coba jalankan bot sebagai root, atau pilih folder lain.</i>\n\n"
	}

	text := "📤 <b>Mode Upload Aktif</b>\n" +
		"━━━━━━━━━━━━━━━━━━━━\n\n" +
		status +
		"📁 <b>Folder tujuan:</b>\n<code>" + html.EscapeString(abs) + "</code>\n\n" +
		"<b>Cara pakai:</b>\n" +
		"Kirim file/dokumen apa pun ke chat ini. File akan tersimpan ke folder di atas " +
		"dengan nama aslinya.\n\n" +
		"• 📏 Batas <code>" + formatBytes(telegramMaxDownload) + "</code> (batas Bot API)\n" +
		"• ⏱ Mode ini aktif " + fmt.Sprintf("%d menit", int(uploadModeTTL.Minutes())) + "\n" +
		"• 📝 Kirim <b>caption</b> untuk memberi nama file sendiri"

	kb := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "📁 Lihat Folder", CallbackData: "fm:o:" + getPathID(abs) + ":0"},
				{Text: "🚫 Batalkan", CallbackData: "up:c"},
			},
		},
	}
	return text, kb
}

// isDirWritable memeriksa apakah folder bisa ditulis proses ini.
func isDirWritable(dir string) bool {
	// Cara paling andal: coba buat file sementara.
	f, err := os.CreateTemp(dir, ".bvc_write_test_*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// ==============================================================================
// 🚦 ALUR UPLOAD (dipakai handler dokumen)
// ==============================================================================

// resolveUploadDest menentukan path tujuan untuk file yang baru dikirim.
// Mengembalikan (pathTujuan, perluKonfirmasi, pesanError).
//
// Aturan:
//   - Bila mode upload aktif, folder mode itu yang dipakai.
//   - Bila caption berisi path lengkap, caption yang menang (perilaku lama tetap jalan).
//   - Nama file selalu dibersihkan lewat safeUploadPath.
func resolveUploadDest(userID int64, caption string, fileName string) (string, bool, error) {
	// Bersihkan caption dari awalan perintah /upload atau /save.
	clean := strings.TrimSpace(caption)
	if strings.HasPrefix(strings.ToLower(clean), "/upload") {
		clean = strings.TrimSpace(clean[len("/upload"):])
	} else if strings.HasPrefix(strings.ToLower(clean), "/save") {
		clean = strings.TrimSpace(clean[len("/save"):])
	}

	// Mode tombol: folder sudah dipilih lewat file manager.
	if dir, ok := GetUploadTarget(userID); ok {
		if clean != "" {
			// Caption dipakai sebagai NAMA FILE, bukan path, saat mode aktif.
			dest, err := safeUploadPath(dir, clean)
			if err != nil {
				return "", false, err
			}
			return dest, needsOverwriteConfirm(dest, userID), nil
		}
		dest, err := safeUploadPath(dir, fileName)
		if err != nil {
			return "", false, err
		}
		return dest, needsOverwriteConfirm(dest, userID), nil
	}

	// Perilaku lama: caption dianggap path.
	if clean == "" {
		dest, err := safeUploadPath(".", fileName)
		return dest, false, err
	}

	if strings.HasSuffix(clean, "/") {
		dest, err := safeUploadPath(clean, fileName)
		return dest, false, err
	}

	// Caption berupa folder yang sudah ada → simpan di dalamnya.
	if st, err := os.Stat(clean); err == nil && st.IsDir() {
		dest, err := safeUploadPath(clean, fileName)
		return dest, false, err
	}

	// Selain itu: caption adalah path/nama file lengkap.
	dest, err := filepath.Abs(clean)
	if err != nil {
		return "", false, fmt.Errorf("path tujuan tidak valid: %w", err)
	}
	return dest, needsOverwriteConfirm(dest, userID), nil
}

// needsOverwriteConfirm melaporkan apakah file sudah ada dan belum disetujui ditimpa.
func needsOverwriteConfirm(dest string, userID int64) bool {
	if dest == "" {
		return false
	}
	if _, err := os.Stat(dest); err != nil {
		return false // belum ada — aman
	}
	return !IsOverwriteAllowed(userID)
}

// renderOverwritePrompt menyusun pesan konfirmasi sebelum menimpa file.
func renderOverwritePrompt(dest string, fileSize int64) (string, *InlineKeyboardMarkup) {
	info, _ := os.Stat(dest)
	ukuranLama := int64(0)
	waktu := "-"
	if info != nil {
		ukuranLama = info.Size()
		waktu = info.ModTime().Format("2006-01-02 15:04:05")
	}

	text := "⚠️ <b>File Sudah Ada</b>\n" +
		"━━━━━━━━━━━━━━━━━━━━\n\n" +
		"📄 <b>Nama:</b> <code>" + html.EscapeString(filepath.Base(dest)) + "</code>\n" +
		"📁 <b>Folder:</b> <code>" + html.EscapeString(filepath.Dir(dest)) + "</code>\n\n" +
		"<b>File lama:</b>\n" +
		"• Ukuran: <code>" + formatBytes(ukuranLama) + "</code>\n" +
		"• Diubah: <code>" + waktu + "</code>\n\n" +
		"<b>File baru:</b>\n" +
		"• Ukuran: <code>" + formatBytes(fileSize) + "</code>\n\n" +
		"❗ Menimpa akan <b>menghapus file lama secara permanen</b> dan tidak bisa dibatalkan."

	kb := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "✅ Timpa File Lama", CallbackData: "up:o"},
				{Text: "❌ Batalkan", CallbackData: "up:c"},
			},
		},
	}
	return text, kb
}

// ==============================================================================
// 📊 LAPORAN HASIL
// ==============================================================================

// renderUploadSuccess menyusun pesan sukses setelah file tersimpan.
func renderUploadSuccess(dest string, written int64, overwritten bool) (string, *InlineKeyboardMarkup) {
	abs := dest
	if a, err := filepath.Abs(dest); err == nil {
		abs = a
	}
	dir := filepath.Dir(abs)

	judul := "✅ <b>File Berhasil Diunggah</b>"
	if overwritten {
		judul = "✅ <b>File Berhasil Ditimpa</b>"
	}

	text := judul + "\n" +
		"━━━━━━━━━━━━━━━━━━━━\n\n" +
		"📄 <b>Nama:</b> <code>" + html.EscapeString(filepath.Base(abs)) + "</code>\n" +
		"📁 <b>Folder:</b> <code>" + html.EscapeString(dir) + "</code>\n" +
		"📦 <b>Ukuran:</b> <code>" + formatBytes(written) + "</code>\n" +
		"🕒 <b>Waktu:</b> <code>" + time.Now().Format("2006-01-02 15:04:05") + "</code>"

	kb := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "📁 Buka Folder", CallbackData: "fm:o:" + getPathID(dir) + ":0"},
			},
			{
				{Text: "📤 Upload Lagi", CallbackData: "fm:up:" + getPathID(dir)},
				{Text: "🏠 Menu Utama", CallbackData: "hp:b"},
			},
		},
	}
	return text, kb
}

// renderUploadError menyusun pesan kesalahan upload yang informatif.
func renderUploadError(dest string, err error, fileSize int64) (string, *InlineKeyboardMarkup) {
	dir := filepath.Dir(dest)

	text := "❌ <b>Gagal Menyimpan File</b>\n" +
		"━━━━━━━━━━━━━━━━━━━━\n\n" +
		"📄 <b>Nama:</b> <code>" + html.EscapeString(filepath.Base(dest)) + "</code>\n" +
		"📁 <b>Tujuan:</b> <code>" + html.EscapeString(dest) + "</code>\n" +
		"📦 <b>Ukuran:</b> <code>" + formatBytes(fileSize) + "</code>\n\n" +
		"<b>Penyebab:</b>\n<pre>" + html.EscapeString(err.Error()) + "</pre>\n"

	// Beri saran sesuai jenis kegagalan.
	msg := err.Error()
	switch {
	case strings.Contains(msg, "permission") || strings.Contains(msg, "izin"):
		text += "\n💡 <i>Folder tidak bisa ditulis. Jalankan bot sebagai root " +
			"atau pilih folder lain.</i>"
	case strings.Contains(msg, "no space") || strings.Contains(msg, "space"):
		text += "\n💡 <i>Disk VPS penuh. Cek dengan <code>/term df -h</code></i>"
	case strings.Contains(msg, "not exist"):
		text += "\n💡 <i>Folder tujuan tidak ada.</i>"
	default:
		text += "\n💡 <i>Cek folder tujuan dan hak akses bot.</i>"
	}

	kb := &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "📁 Buka Folder", CallbackData: "fm:o:" + getPathID(dir) + ":0"},
				{Text: "🔄 Coba Lagi", CallbackData: "fm:up:" + getPathID(dir)},
			},
		},
	}
	return text, kb
}

// ==============================================================================
// 📥 HANDLER DOKUMEN (dipanggil dari loop utama)
// ==============================================================================

// handleDocumentUpload memproses file yang dikirim admin ke chat.
//
// Alurnya:
//  1. Tentukan folder tujuan (mode tombol atau caption).
//  2. Validasi ukuran SEBELUM mengunduh — Bot API hanya 20MB.
//  3. Bila file sudah ada dan belum disetujui, minta konfirmasi.
//  4. Unduh, lalu laporkan hasilnya dengan tombol lanjutan.
func handleDocumentUpload(chatID int64, userID int64, doc *Document, caption string) {
	if doc == nil {
		return
	}

	// Bila ada sesi upload bertahap aktif, file ini diperlakukan sebagai bagian.
	if s, ok := ChunkSesiAktif(userID); ok {
		handleChunkPart(chatID, userID, doc, s)
		return
	}

	fileName := doc.FileName
	if strings.TrimSpace(fileName) == "" {
		fileName = fmt.Sprintf("file_%d", time.Now().Unix())
	}

	// 1. Validasi ukuran lebih awal — jangan buang 120 detik untuk gagal.
	if msg := checkUploadSize(doc.FileSize); msg != "" {
		ClearUploadTarget(userID)
		sendTelegram(chatID, msg)
		return
	}

	// 2. Tentukan tujuan.
	dest, perluKonfirmasi, err := resolveUploadDest(userID, caption, fileName)
	if err != nil {
		ClearUploadTarget(userID)
		sendTelegram(chatID, "❌ <b>Nama/path file tidak valid:</b>\\n<pre>"+
			html.EscapeString(err.Error())+"</pre>")
		return
	}
	if dest == "" {
		ClearUploadTarget(userID)
		sendTelegram(chatID, "❌ Tidak bisa menentukan folder tujuan.")
		return
	}

	// 3. Konfirmasi bila akan menimpa file yang sudah ada.
	if perluKonfirmasi {
		text, kb := renderOverwritePrompt(dest, doc.FileSize)
		sendPanel(userID, chatID, text, kb)
		return
	}

	// 4. Siapkan folder tujuan bila belum ada.
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0755); err != nil {
		text, kb := renderUploadError(dest, fmt.Errorf("gagal membuat folder tujuan: %w", err), doc.FileSize)
		sendPanel(userID, chatID, text, kb)
		return
	}

	// Catat apakah ini penimpaan, untuk laporan hasil.
	_, statErr := os.Stat(dest)
	overwritten := statErr == nil

	fmt.Printf("[%s] 📥 Mengunduh '%s' → '%s' (%d bytes)\\n",
		time.Now().Format("15:04:05"), fileName, dest, doc.FileSize)

	// Tampilkan progres di panel (bukan pesan baru).
	sendPanel(userID, chatID,
		"📥 <b>Mengunduh file…</b>\\n━━━━━━━━━━━━━━━━━━━━\\n\\n"+
			"📄 <code>"+html.EscapeString(filepath.Base(dest))+"</code>\\n"+
			"📁 <code>"+html.EscapeString(dir)+"</code>\\n"+
			"📦 <code>"+formatBytes(doc.FileSize)+"</code>\\n\\n"+
			"<i>Mohon tunggu…</i>",
		nil)

	written, err := downloadTelegramFile(doc.FileID, dest)
	if err != nil {
		text, kb := renderUploadError(dest, err, doc.FileSize)
		sendPanel(userID, chatID, text, kb)
		// Hapus file separuh jadi supaya tidak menyisakan sampah rusak.
		if fi, e := os.Stat(dest); e == nil && fi.Size() == 0 {
			os.Remove(dest)
		}
		return
	}

	// 5. Laporkan hasil.
	text, kb := renderUploadSuccess(dest, written, overwritten)
	sendPanel(userID, chatID, text, kb)

	// Mode upload tetap aktif supaya bisa mengirim beberapa file berturut-turut,
	// kecuali bila tadi memakai caption path manual.
	if _, modeAktif := GetUploadTarget(userID); !modeAktif {
		// Tidak ada mode tombol — tidak perlu dibersihkan.
		return
	}

	fmt.Printf("[%s] ✅ Selesai: %s (%s)\\n",
		time.Now().Format("15:04:05"), dest, formatBytes(written))
}
