package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ==============================================================================
// 📤 TEST FITUR UPLOAD
// ==============================================================================

// TestSafeUploadPathMenolakTraversal adalah test KEAMANAN yang paling penting.
// Nama file dari Telegram tidak boleh bisa menulis ke luar folder tujuan.
func TestSafeUploadPathMenolakTraversal(t *testing.T) {
	dir := "/tmp/bvc_upload_test"
	os.MkdirAll(dir, 0755)
	defer os.RemoveAll(dir)

	jahat := []string{
		"../../etc/passwd",
		"../../../root/.ssh/authorized_keys",
		"..",
		".",
		"",
		"sub/../../etc/shadow",
		"nama\x00file",
	}
	for _, nama := range jahat {
		got, err := safeUploadPath(dir, nama)
		if err == nil {
			// Bila tidak error, hasilnya WAJIB tetap di dalam dir.
			rel, rerr := filepath.Rel(dir, got)
			if rerr != nil || strings.HasPrefix(rel, "..") {
				t.Errorf("BAHAYA: %q menghasilkan path di luar folder: %q", nama, got)
			}
		}
	}
	t.Log("✅ Nama file berbahaya tidak bisa keluar dari folder tujuan")
}

// TestSafeUploadPathMenerimaNamaNormal memastikan nama wajar tetap jalan.
func TestSafeUploadPathMenerimaNamaNormal(t *testing.T) {
	dir := "/tmp/bvc_upload_test2"
	os.MkdirAll(dir, 0755)
	defer os.RemoveAll(dir)

	kasus := map[string]string{
		"file.txt":      "file.txt",
		"script.sh":     "script.sh",
		"data 2024.csv": "data 2024.csv",
		"gambar-1.png":  "gambar-1.png",
		"a.b.c.tar.gz":  "a.b.c.tar.gz",
		"sub/file.txt":  "file.txt", // path diambil basename-nya
		"/etc/hosts":    "hosts",    // path absolut diambil basename
	}
	for input, mauBase := range kasus {
		got, err := safeUploadPath(dir, input)
		if err != nil {
			t.Errorf("%q seharusnya diterima, dapat error: %v", input, err)
			continue
		}
		if filepath.Base(got) != mauBase {
			t.Errorf("%q → base %q, mau %q", input, filepath.Base(got), mauBase)
		}
		if filepath.Dir(got) != filepath.Clean(dir) {
			t.Errorf("%q → dir %q, mau %q", input, filepath.Dir(got), dir)
		}
	}
	t.Logf("✅ %d nama normal diterima dengan benar", len(kasus))
}

// TestCheckUploadSize memastikan file besar ditolak SEBELUM diunduh.
func TestCheckUploadSize(t *testing.T) {
	// Di bawah batas — lolos.
	if msg := checkUploadSize(1024); msg != "" {
		t.Errorf("file 1KB seharusnya lolos, dapat: %s", msg)
	}
	if msg := checkUploadSize(telegramMaxDownload); msg != "" {
		t.Errorf("file tepat di batas seharusnya lolos, dapat: %s", msg)
	}
	// Ukuran tidak diketahui — dicoba saja.
	if msg := checkUploadSize(0); msg != "" {
		t.Errorf("ukuran 0 seharusnya dicoba, dapat: %s", msg)
	}

	// Di atas batas — ditolak dengan penjelasan.
	msg := checkUploadSize(telegramMaxDownload + 1)
	if msg == "" {
		t.Fatal("file di atas batas harus ditolak")
	}
	for _, kata := range []string{"terlalu besar", "20.0 MB", "Alternatif"} {
		if !strings.Contains(msg, kata) {
			t.Errorf("pesan penolakan harus memuat %q", kata)
		}
	}
	t.Log("✅ File >20MB ditolak lebih awal dengan saran alternatif")
}

// TestUploadTargetLifecycle memverifikasi pencatatan folder tujuan.
func TestUploadTargetLifecycle(t *testing.T) {
	u := int64(880001)

	if _, ok := GetUploadTarget(u); ok {
		t.Fatal("admin baru seharusnya belum punya target")
	}

	SetUploadTarget(u, "/tmp/uji")
	dir, ok := GetUploadTarget(u)
	if !ok || dir != "/tmp/uji" {
		t.Fatalf("target = %q ok=%v", dir, ok)
	}

	ClearUploadTarget(u)
	if _, ok := GetUploadTarget(u); ok {
		t.Fatal("target seharusnya terhapus")
	}
}

// TestUploadTargetKedaluwarsa memastikan mode upload tidak menggantung selamanya.
func TestUploadTargetKedaluwarsa(t *testing.T) {
	u := int64(880002)
	SetUploadTarget(u, "/tmp/uji")
	defer ClearUploadTarget(u)

	// Paksa waktu set ke masa lalu melewati TTL.
	uploadMu.Lock()
	uploadTargets[u].SetAt = time.Now().Add(-uploadModeTTL - time.Minute)
	uploadMu.Unlock()

	if _, ok := GetUploadTarget(u); ok {
		t.Error("target kedaluwarsa seharusnya otomatis dihapus")
	}
	t.Log("✅ Mode upload kedaluwarsa otomatis")
}

// TestOverwriteKonfirmasi memastikan file yang ada tidak tertimpa diam-diam.
func TestOverwriteKonfirmasi(t *testing.T) {
	u := int64(880003)
	dir := t.TempDir()
	dest := filepath.Join(dir, "sudah_ada.txt")
	if err := os.WriteFile(dest, []byte("data lama"), 0644); err != nil {
		t.Fatal(err)
	}

	SetUploadTarget(u, dir)
	defer ClearUploadTarget(u)

	// File sudah ada dan belum disetujui → HARUS minta konfirmasi.
	if !needsOverwriteConfirm(dest, u) {
		t.Error("file yang sudah ada harus minta konfirmasi")
	}

	// Setelah disetujui → tidak perlu tanya lagi.
	AllowOverwrite(u)
	if needsOverwriteConfirm(dest, u) {
		t.Error("setelah disetujui, tidak perlu konfirmasi lagi")
	}

	// File yang belum ada → tidak perlu konfirmasi.
	baru := filepath.Join(dir, "belum_ada.txt")
	if needsOverwriteConfirm(baru, u) {
		t.Error("file baru tidak perlu konfirmasi")
	}
	t.Log("✅ File yang ada minta konfirmasi, file baru langsung disimpan")
}

// TestResolveUploadDestModeTombol memverifikasi prioritas mode tombol.
func TestResolveUploadDestModeTombol(t *testing.T) {
	u := int64(880004)
	dir := t.TempDir()
	SetUploadTarget(u, dir)
	defer ClearUploadTarget(u)

	// Tanpa caption → pakai nama asli di folder mode.
	dest, _, err := resolveUploadDest(u, "", "laporan.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dest) != dir || filepath.Base(dest) != "laporan.pdf" {
		t.Errorf("dest = %q, mau %q", dest, filepath.Join(dir, "laporan.pdf"))
	}

	// Caption jadi NAMA FILE saat mode aktif, bukan path.
	dest2, _, err := resolveUploadDest(u, "nama-kustom.txt", "abaikan.txt")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dest2) != "nama-kustom.txt" {
		t.Errorf("caption harus jadi nama file, dapat %q", filepath.Base(dest2))
	}
	if filepath.Dir(dest2) != dir {
		t.Errorf("caption tidak boleh mengubah folder saat mode aktif: %q", dest2)
	}
	t.Log("✅ Mode tombol: caption jadi nama file, folder tetap")
}

// TestResolveUploadDestCaptionPath memverifikasi perilaku lama tetap jalan.
func TestResolveUploadDestCaptionPath(t *testing.T) {
	u := int64(880005)
	dir := t.TempDir()

	// Tanpa mode tombol + caption path lengkap.
	dest, _, err := resolveUploadDest(u, filepath.Join(dir, "kustom.txt"), "asli.txt")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dest) != "kustom.txt" {
		t.Errorf("caption path harus dipakai, dapat %q", dest)
	}

	// Caption berakhir "/" → simpan dengan nama asli di folder itu.
	dest2, _, err := resolveUploadDest(u, dir+string(filepath.Separator), "asli.txt")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dest2) != dir || filepath.Base(dest2) != "asli.txt" {
		t.Errorf("dest2 = %q", dest2)
	}
	t.Log("✅ Perilaku lama (caption path) tetap berfungsi")
}

// TestRenderUploadPromptTagSeimbang memastikan pesan prompt valid untuk Telegram.
func TestRenderUploadPromptTagSeimbang(t *testing.T) {
	dir := t.TempDir()
	text, kb := renderUploadPrompt(dir)

	if text == "" {
		t.Fatal("prompt kosong")
	}
	if kb == nil || len(kb.InlineKeyboard) == 0 {
		t.Fatal("prompt harus punya tombol")
	}
	for _, tag := range []string{"b", "i", "code"} {
		o := strings.Count(text, "<"+tag+">")
		c := strings.Count(text, "</"+tag+">")
		if o != c {
			t.Errorf("tag <%s> tidak seimbang: %d/%d", tag, o, c)
		}
	}
	// Harus menyebut batas ukuran supaya pengguna tahu.
	if !strings.Contains(text, "20.0 MB") && !strings.Contains(text, "Batas") {
		t.Error("prompt harus menyebut batas ukuran")
	}
	t.Log("✅ Prompt upload valid & informatif")
}

// TestRenderOverwritePromptPunyaTombol memastikan konfirmasi punya pilihan.
func TestRenderOverwritePromptPunyaTombol(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "ada.txt")
	os.WriteFile(dest, []byte("lama"), 0644)

	text, kb := renderOverwritePrompt(dest, 12345)
	if !strings.Contains(text, "Sudah Ada") {
		t.Error("harus menyatakan file sudah ada")
	}
	if !strings.Contains(text, "permanen") {
		t.Error("harus memperingatkan bahwa tindakan tidak bisa dibatalkan")
	}

	adaTimpa, adaBatal := false, false
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			if b.CallbackData == "up:o" {
				adaTimpa = true
			}
			if b.CallbackData == "up:c" {
				adaBatal = true
			}
		}
	}
	if !adaTimpa || !adaBatal {
		t.Error("harus ada tombol Timpa dan Batalkan")
	}
	t.Log("✅ Konfirmasi overwrite punya kedua pilihan")
}

// TestRenderUploadSuccessPunyaNavigasi memastikan laporan hasil bisa ditindaklanjuti.
func TestRenderUploadSuccessPunyaNavigasi(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "hasil.txt")
	text, kb := renderUploadSuccess(dest, 2048, false)

	if !strings.Contains(text, "Berhasil") {
		t.Error("harus menyatakan berhasil")
	}
	if !strings.Contains(text, "2.0 KB") {
		t.Errorf("harus menampilkan ukuran, dapat: %s", text)
	}
	if kb == nil || len(kb.InlineKeyboard) == 0 {
		t.Fatal("harus ada tombol lanjutan")
	}
	t.Log("✅ Laporan sukses menampilkan ukuran & tombol navigasi")
}

// TestIsDirWritable memverifikasi deteksi folder yang bisa ditulis.
func TestIsDirWritable(t *testing.T) {
	dir := t.TempDir()
	if !isDirWritable(dir) {
		t.Error("folder sementara seharusnya bisa ditulis")
	}
	if isDirWritable("/proc/sys/nonexistent_xyz") {
		t.Error("folder tidak ada seharusnya tidak bisa ditulis")
	}
	t.Log("✅ Deteksi folder writable bekerja")
}

// TestUploadEndToEndDisk memverifikasi file benar-benar tertulis ke disk
// lewat alur yang sama dengan yang dipakai bot (resolveUploadDest + tulis).
func TestUploadEndToEndDisk(t *testing.T) {
	u := int64(880006)
	dir := t.TempDir()
	SetUploadTarget(u, dir)
	defer ClearUploadTarget(u)

	// Simulasi: admin memilih folder lalu mengirim file "catatan.txt".
	dest, perluKonfirmasi, err := resolveUploadDest(u, "", "catatan.txt")
	if err != nil {
		t.Fatalf("resolveUploadDest: %v", err)
	}
	if perluKonfirmasi {
		t.Fatal("file baru tidak perlu konfirmasi")
	}

	isi := []byte("isi file uji dari telegram")
	if err := os.WriteFile(dest, isi, 0644); err != nil {
		t.Fatalf("tulis file: %v", err)
	}

	// Baca ulang dari disk — bukan dari memori.
	terbaca, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("baca kembali: %v", err)
	}
	if string(terbaca) != string(isi) {
		t.Errorf("isi file berbeda: %q vs %q", terbaca, isi)
	}

	// Pastikan berada di folder yang dipilih admin.
	if filepath.Dir(dest) != dir {
		t.Errorf("file tersimpan di %q, mau %q", filepath.Dir(dest), dir)
	}
	t.Logf("✅ File tersimpan & terbaca: %s (%d byte)", dest, len(terbaca))
}

// TestUploadOverwriteBenarBenarMenimpa memastikan penimpaan mengganti isi lama.
func TestUploadOverwriteBenarBenarMenimpa(t *testing.T) {
	u := int64(880007)
	dir := t.TempDir()
	dest := filepath.Join(dir, "berkas.txt")

	// File lama.
	os.WriteFile(dest, []byte("ISI LAMA YANG HARUS HILANG"), 0644)
	SetUploadTarget(u, dir)
	defer ClearUploadTarget(u)

	// Percobaan pertama: harus minta konfirmasi.
	_, perlu, err := resolveUploadDest(u, "", "berkas.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !perlu {
		t.Fatal("seharusnya minta konfirmasi")
	}

	// Setelah disetujui.
	AllowOverwrite(u)
	dest2, perlu2, err := resolveUploadDest(u, "", "berkas.txt")
	if err != nil {
		t.Fatal(err)
	}
	if perlu2 {
		t.Fatal("setelah disetujui tidak boleh minta konfirmasi")
	}

	// Timpa dan verifikasi isinya benar-benar berganti.
	os.WriteFile(dest2, []byte("ISI BARU"), 0644)
	got, _ := os.ReadFile(dest)
	if string(got) != "ISI BARU" {
		t.Errorf("isi file = %q, mau ISI BARU", got)
	}
	t.Log("✅ Penimpaan mengganti isi lama setelah konfirmasi")
}

// TestBeberapaFileBerturutTurut memastikan mode upload bisa dipakai berkali-kali.
func TestBeberapaFileBerturutTurut(t *testing.T) {
	u := int64(880008)
	dir := t.TempDir()
	SetUploadTarget(u, dir)
	defer ClearUploadTarget(u)

	berkas := []string{"satu.txt", "dua.txt", "tiga.txt"}
	for _, nama := range berkas {
		dest, perlu, err := resolveUploadDest(u, "", nama)
		if err != nil {
			t.Fatalf("%s: %v", nama, err)
		}
		if perlu {
			t.Fatalf("%s: tidak seharusnya minta konfirmasi", nama)
		}
		if err := os.WriteFile(dest, []byte(nama), 0644); err != nil {
			t.Fatalf("%s: %v", nama, err)
		}
	}

	// Ketiga file harus ada.
	masuk, _ := os.ReadDir(dir)
	if len(masuk) != 3 {
		t.Errorf("ada %d file, mau 3", len(masuk))
	}
	t.Log("✅ Mode upload bertahan untuk beberapa file berturut-turut")
}
