package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ==============================================================================
// 📦 TEST UPLOAD FILE BESAR
// ==============================================================================

// TestValidasiUnduhURL adalah test KEAMANAN: URL dipakai di shell,
// jadi skema berbahaya harus ditolak sebelum menyentuh perintah.
func TestValidasiUnduhURL(t *testing.T) {
	aman := []string{
		"https://contoh.com/file.zip",
		"http://192.168.1.1/data.tar.gz",
		"https://github.com/user/repo/releases/download/v1/app",
		"https://contoh.com/file?a=1&b=2",
	}
	for _, u := range aman {
		if msg := ValidasiUnduhURL(u); msg != "" {
			t.Errorf("%q seharusnya diterima, dapat: %s", u, msg)
		}
	}

	// Yang ditolak: skema salah, dan karakter pemutus-kutip.
	// Catatan: `;`, `&&`, `|`, `$( )`, backtick AMAN karena shellQuote
	// membungkus URL dalam kutip tunggal (dibuktikan di test injeksi).
	ditolak := []string{
		"",
		"file:///etc/passwd",
		"ftp://contoh.com/x",
		"javascript:alert(1)",
		"contoh.com/file.zip",
		"https://contoh.com/x'y",
		"https://contoh.com/x\\y",
	}
	for _, u := range ditolak {
		if msg := ValidasiUnduhURL(u); msg == "" {
			t.Errorf("%q seharusnya ditolak!", u)
		}
	}
	t.Logf("✅ %d URL diterima, %d ditolak", len(aman), len(ditolak))
}

// TestNamaDariURL memverifikasi pengambilan nama file dari URL.
func TestNamaDariURL(t *testing.T) {
	kasus := map[string]string{
		"https://contoh.com/data.zip":                 "data.zip",
		"https://contoh.com/path/arsip.tar.gz":        "arsip.tar.gz",
		"https://contoh.com/file.zip?token=abc":       "file.zip",
		"https://contoh.com/file.zip#section":         "file.zip",
		"https://contoh.com/unduh/some-file-v1.2.bin": "some-file-v1.2.bin",
	}
	for url, mau := range kasus {
		if got := namaDariURL(url); got != mau {
			t.Errorf("namaDariURL(%q) = %q, mau %q", url, got, mau)
		}
	}

	// URL tanpa nama jelas harus menghasilkan nama pengganti, bukan kosong.
	for _, url := range []string{"https://contoh.com/", "https://contoh.com"} {
		got := namaDariURL(url)
		if got == "" || got == "." || got == "/" {
			t.Errorf("namaDariURL(%q) menghasilkan %q — tidak valid", url, got)
		}
	}
	t.Logf("✅ %d pola URL menghasilkan nama benar", len(kasus))
}

// TestShellQuoteAman memastikan argumen tidak bisa keluar dari kutip.
func TestShellQuoteAman(t *testing.T) {
	kasus := map[string]string{
		"simpel":      "'simpel'",
		"a b":         "'a b'",
		"it's":        `'it'\''s'`,
		"'; rm -rf /": `''\''; rm -rf /'`,
	}
	for masuk, mau := range kasus {
		if got := shellQuote(masuk); got != mau {
			t.Errorf("shellQuote(%q) = %q, mau %q", masuk, got, mau)
		}
	}
	t.Log("✅ shellQuote menutup celah injeksi")
}

// TestPEcahDanGabungFile menguji siklus lengkap dengan FILE SUNGGUHAN.
// Ini membuktikan pemecahan & penggabungan menghasilkan file identik.
func TestPecahDanGabungFile(t *testing.T) {
	dir := t.TempDir()
	asal := filepath.Join(dir, "besar.bin")

	// Buat file 5MB dengan data pseudo-acak yang bisa diverifikasi.
	data := make([]byte, 5*1024*1024)
	for i := range data {
		data[i] = byte((i*7 + 13) % 251)
	}
	if err := os.WriteFile(asal, data, 0644); err != nil {
		t.Fatal(err)
	}

	// Pecah jadi bagian 1MB.
	bagian, err := PecahFile(asal, 1)
	if err != nil {
		t.Fatalf("PecahFile: %v", err)
	}
	if len(bagian) != 5 {
		t.Errorf("menghasilkan %d bagian, mau 5", len(bagian))
	}
	t.Logf("✅ File 5MB dipecah jadi %d bagian", len(bagian))

	// Pastikan bagian-bagiannya benar-benar ada dan berukuran wajar.
	var totalBagian int64
	for _, b := range bagian {
		fi, err := os.Stat(b)
		if err != nil {
			t.Fatalf("bagian %s tidak ada: %v", b, err)
		}
		totalBagian += fi.Size()
	}
	if totalBagian != int64(len(data)) {
		t.Errorf("total bagian %d byte, mau %d", totalBagian, len(data))
	}

	// Gabungkan kembali.
	hasil := filepath.Join(dir, "hasil.bin")
	ukuran, err := GabungFile(bagian[0], hasil)
	if err != nil {
		t.Fatalf("GabungFile: %v", err)
	}

	// VERIFIKASI INTI: hash harus identik.
	hashAsal := sha256File(t, asal)
	hashHasil := sha256File(t, hasil)
	if hashAsal != hashHasil {
		t.Errorf("hash berbeda!\n  asal : %s\n  hasil: %s", hashAsal[:16], hashHasil[:16])
	}
	if ukuran != int64(len(data)) {
		t.Errorf("ukuran hasil %d, mau %d", ukuran, len(data))
	}
	t.Logf("✅ Gabung menghasilkan file IDENTIK (sha256 %s…, %s)",
		hashAsal[:16], formatBytes(ukuran))
}

// TestPecahFileKecilDitolak memastikan file kecil tidak dipecah sia-sia.
func TestPecahFileKecilDitolak(t *testing.T) {
	dir := t.TempDir()
	kecil := filepath.Join(dir, "kecil.txt")
	os.WriteFile(kecil, []byte("isi kecil"), 0644)

	_, err := PecahFile(kecil, 10)
	if err == nil {
		t.Error("file kecil seharusnya tidak perlu dipecah")
	} else if !strings.Contains(err.Error(), "tidak perlu") {
		t.Errorf("pesan harus menjelaskan alasannya, dapat: %v", err)
	}
	t.Log("✅ File kecil ditolak dengan penjelasan")
}

// TestPecahFileTidakAda memastikan file hilang ditangani rapi.
func TestPecahFileTidakAda(t *testing.T) {
	_, err := PecahFile("/tidak/ada/file.bin", 10)
	if err == nil {
		t.Error("file tidak ada seharusnya error")
	}
}

// TestNomorBagianDariNama memverifikasi pengenalan nomor bagian.
func TestNomorBagianDariNama(t *testing.T) {
	kasus := map[string]int{
		// Pola `split` dengan angka.
		"data.zip.part_001": 1,
		"data.zip.part_002": 2,
		"data.zip.part_010": 10,
		"data.zip.part-003": 3,
		"data.zip.part.004": 4,
		// Pola `split` dengan huruf (basis 26): aa=1, ab=2, az=26, ba=27.
		"data.zip.part_aa": 1,
		"data.zip.part_ab": 2,
		"data.zip.part_az": 26,
		"data.zip.part_ba": 27,
		// Pola angka di akhir.
		"data.bin.1":   1,
		"data-002.bin": 2,
		"archivo.10":   10,
	}
	for nama, mau := range kasus {
		if got := nomorBagianDariNama(nama); got != mau {
			t.Errorf("nomorBagianDariNama(%q) = %d, mau %d", nama, got, mau)
		}
	}

	// Nama tanpa nomor harus mengembalikan 0 (dianggap tidak dikenali).
	for _, nama := range []string{"data.zip", "file.txt", ""} {
		if got := nomorBagianDariNama(nama); got != 0 {
			t.Errorf("nomorBagianDariNama(%q) = %d, mau 0", nama, got)
		}
	}
	t.Logf("✅ %d pola nama dikenali dengan benar", len(kasus))
}

// TestChunkSesiLifecycle memverifikasi siklus sesi upload bertahap.
func TestChunkSesiLifecycle(t *testing.T) {
	u := int64(990001)
	dir := t.TempDir()
	defer BatalkanChunkSesi(u)

	if _, ok := ChunkSesiAktif(u); ok {
		t.Fatal("admin baru seharusnya belum punya sesi")
	}

	s, err := MulaiChunkSesi(u, dir, "gabungan.bin", 3)
	if err != nil {
		t.Fatalf("MulaiChunkSesi: %v", err)
	}
	if s.TotalPart != 3 {
		t.Errorf("TotalPart = %d, mau 3", s.TotalPart)
	}

	// Terima 2 bagian.
	for i := 1; i <= 2; i++ {
		data := []byte(fmt.Sprintf("bagian-%d", i))
		_, total, selesai, err := TerimaChunk(u, i, data)
		if err != nil {
			t.Fatalf("TerimaChunk(%d): %v", i, err)
		}
		if total != 3 {
			t.Errorf("total = %d, mau 3", total)
		}
		if selesai {
			t.Errorf("belum selesai setelah bagian %d", i)
		}
	}

	// Bagian ke-3 → selesai.
	_, _, selesai, err := TerimaChunk(u, 3, []byte("bagian-3"))
	if err != nil {
		t.Fatal(err)
	}
	if !selesai {
		t.Error("harus selesai setelah bagian terakhir")
	}

	// Gabungkan.
	hasil, ukuran, err := GabungChunk(u)
	if err != nil {
		t.Fatalf("GabungChunk: %v", err)
	}
	if filepath.Base(hasil) != "gabungan.bin" {
		t.Errorf("nama hasil %q", hasil)
	}
	isi, _ := os.ReadFile(hasil)
	if string(isi) != "bagian-1bagian-2bagian-3" {
		t.Errorf("isi gabungan salah: %q", isi)
	}
	if ukuran != int64(len(isi)) {
		t.Errorf("ukuran %d, mau %d", ukuran, len(isi))
	}
	t.Logf("✅ Sesi chunk: 3 bagian → %s (%s)", filepath.Base(hasil), formatBytes(ukuran))

	// Sesi harus bersih setelah selesai.
	if _, ok := ChunkSesiAktif(u); ok {
		t.Error("sesi seharusnya terhapus setelah digabung")
	}
}

// TestChunkBagianHilangDitolak memastikan penyatuan menolak bila ada bagian kurang.
func TestChunkBagianHilangDitolak(t *testing.T) {
	u := int64(990002)
	dir := t.TempDir()
	defer BatalkanChunkSesi(u)

	if _, err := MulaiChunkSesi(u, dir, "kurang.bin", 4); err != nil {
		t.Fatal(err)
	}

	// Hanya kirim bagian 1 dan 2 (3 dan 4 hilang).
	TerimaChunk(u, 1, []byte("a"))
	TerimaChunk(u, 2, []byte("b"))

	// Paksa dianggap selesai untuk menguji validasi penyatuan.
	chunkMu.Lock()
	chunkSesis[u].Diterima = 4
	chunkMu.Unlock()

	_, _, err := GabungChunk(u)
	if err == nil {
		t.Fatal("penyatuan harus GAGAL bila ada bagian hilang")
	}
	if !strings.Contains(err.Error(), "belum diterima") {
		t.Errorf("pesan harus menyebut bagian yang hilang, dapat: %v", err)
	}
	t.Log("✅ Penyatuan menolak bila bagian tidak lengkap")
}

// TestChunkNomorDiluarRentang memastikan nomor bagian divalidasi.
func TestChunkNomorDiluarRentang(t *testing.T) {
	u := int64(990003)
	dir := t.TempDir()
	defer BatalkanChunkSesi(u)

	if _, err := MulaiChunkSesi(u, dir, "x.bin", 3); err != nil {
		t.Fatal(err)
	}

	for _, nomor := range []int{0, 4, 99, -1} {
		_, _, _, err := TerimaChunk(u, nomor, []byte("x"))
		if err == nil {
			t.Errorf("nomor %d seharusnya ditolak", nomor)
		}
	}
	t.Log("✅ Nomor bagian di luar rentang ditolak")
}

// TestChunkSesiKedaluwarsa memastikan sesi tidak menggantung selamanya.
func TestChunkSesiKedaluwarsa(t *testing.T) {
	u := int64(990004)
	dir := t.TempDir()
	if _, err := MulaiChunkSesi(u, dir, "lama.bin", 2); err != nil {
		t.Fatal(err)
	}

	// Paksa waktu ke masa lalu.
	chunkMu.Lock()
	chunkSesis[u].Diperbarui = time.Now().Add(-chunkSesiTTL - time.Minute)
	chunkMu.Unlock()

	if _, ok := ChunkSesiAktif(u); ok {
		t.Error("sesi kedaluwarsa seharusnya dihapus")
	}
	t.Log("✅ Sesi chunk kedaluwarsa otomatis")
}

// TestBatalChunkMembersihkanFolder memastikan pembatalan tidak menyisakan sampah.
func TestBatalChunkMembersihkanFolder(t *testing.T) {
	u := int64(990005)
	dir := t.TempDir()

	s, err := MulaiChunkSesi(u, dir, "batal.bin", 2)
	if err != nil {
		t.Fatal(err)
	}
	folderPart := s.Dir
	TerimaChunk(u, 1, []byte("data"))

	if _, err := os.Stat(folderPart); err != nil {
		t.Fatal("folder bagian seharusnya ada")
	}

	if !BatalkanChunkSesi(u) {
		t.Error("BatalkanChunkSesi harus mengembalikan true")
	}
	if _, err := os.Stat(folderPart); !os.IsNotExist(err) {
		t.Error("folder bagian seharusnya dihapus setelah batal")
	}
	t.Log("✅ Pembatalan membersihkan folder bagian")
}

// TestSanitasiNama memastikan nama folder aman dari karakter aneh.
func TestSanitasiNama(t *testing.T) {
	kasus := map[string]string{
		"data.zip":          "data.zip",
		"file dengan spasi": "file_dengan_spasi",
		"../jahat":          "jahat",
		"a/b/c.txt":         "c.txt",
		"emoji🔥.bin":        "emoji_.bin",
		"":                  "upload_", // akan ditambah timestamp
	}
	for masuk, mau := range kasus {
		got := sanitasiNama(masuk)
		if mau == "upload_" {
			if !strings.HasPrefix(got, "upload_") {
				t.Errorf("sanitasiNama(%q) = %q, mau berawalan upload_", masuk, got)
			}
			continue
		}
		if got != mau {
			t.Errorf("sanitasiNama(%q) = %q, mau %q", masuk, got, mau)
		}
		// Tidak boleh ada pemisah path.
		if strings.Contains(got, "/") {
			t.Errorf("sanitasiNama(%q) = %q masih memuat /", masuk, got)
		}
	}
	t.Logf("✅ %d nama disanitasi dengan benar", len(kasus))
}

// TestRenderBantuanTagSeimbang memastikan panduan valid untuk Telegram.
func TestRenderBantuanTagSeimbang(t *testing.T) {
	pembangun := map[string]func() (string, *InlineKeyboardMarkup){
		"renderBantuanUploadBesar": func() (string, *InlineKeyboardMarkup) {
			return renderBantuanUploadBesar("/root")
		},
		"renderPanduanLocalServer": renderPanduanLocalServer,
		"renderPanduanChunk": func() (string, *InlineKeyboardMarkup) {
			return renderPanduanChunk("/root")
		},
		"renderUnduhInfo": func() (string, *InlineKeyboardMarkup) {
			return renderUnduhInfo("/root")
		},
	}

	for nama, fn := range pembangun {
		text, kb := fn()
		if text == "" {
			t.Errorf("%s menghasilkan teks kosong", nama)
		}
		if kb == nil || len(kb.InlineKeyboard) == 0 {
			t.Errorf("%s harus punya tombol", nama)
		}
		for _, tag := range []string{"b", "i", "code", "pre"} {
			o := strings.Count(text, "<"+tag+">")
			c := strings.Count(text, "</"+tag+">")
			if o != c {
				t.Errorf("%s: tag <%s> tidak seimbang %d/%d", nama, tag, o, c)
			}
		}
	}
	t.Logf("✅ %d tampilan bantuan valid", len(pembangun))
}

// TestHandleUploadBesarCallbackAman memastikan semua aksi callback aman.
func TestHandleUploadBesarCallbackAman(t *testing.T) {
	aksi := []string{"menu", "", "localserver", "url:abc", "chunk:abc", "chunkmulai:abc:5", "ngawur"}
	for _, a := range aksi {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("aksi %q panic: %v", a, r)
				}
			}()
			text, kb := handleUploadBesarCallback(990010, 990010, a)
			if text == "" {
				t.Errorf("aksi %q menghasilkan teks kosong", a)
			}
			if kb == nil {
				t.Errorf("aksi %q tidak mengembalikan keyboard", a)
			}
		}()
	}
	t.Logf("✅ %d aksi callback upload besar aman", len(aksi))
}

// TestValidasiUnduhTujuan memverifikasi penanganan folder tujuan.
func TestValidasiUnduhTujuan(t *testing.T) {
	dir := t.TempDir()
	got, msg := ValidasiUnduhTujuan(dir)
	if msg != "" {
		t.Errorf("folder valid ditolak: %s", msg)
	}
	if got != dir {
		t.Errorf("path = %q, mau %q", got, dir)
	}

	// Folder kosong ditolak.
	if _, msg := ValidasiUnduhTujuan(""); msg == "" {
		t.Error("folder kosong harus ditolak")
	}

	// File (bukan folder) ditolak.
	file := filepath.Join(dir, "f.txt")
	os.WriteFile(file, []byte("x"), 0644)
	if _, msg := ValidasiUnduhTujuan(file); msg == "" {
		t.Error("file seharusnya ditolak sebagai tujuan")
	}
	t.Log("✅ Validasi folder tujuan bekerja")
}

// sha256File menghitung hash file untuk verifikasi integritas.
func sha256File(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("baca %s: %v", path, err)
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// TestValidasiUnduhURLInjeksiShell menguji karakter injeksi secara menyeluruh.
// Ini test KEAMANAN: URL masuk ke shell, jadi setiap vektor harus ditutup.
func TestValidasiUnduhURLInjeksiShell(t *testing.T) {
	// HANYA karakter ini yang berbahaya: bisa memutus kutip tunggal atau
	// mengakhiri baris perintah. Karakter lain (& ; | ` $ ~ * ?) AMAN karena
	// URL dibungkus shellQuote — sudah dibuktikan secara empiris.
	berbahaya := []struct{ nama, url string }{
		{"kutip tunggal", "https://c.com/x'y"},
		{"backslash", "https://c.com/x\\y"},
		{"newline", "https://c.com/x\nid"},
		{"carriage return", "https://c.com/x\rid"},
		{"null byte", "https://c.com/x\x00y"},
	}

	gagal := 0
	for _, v := range berbahaya {
		if ms := ValidasiUnduhURL(v.url); ms == "" {
			t.Errorf("BAHAYA: vektor %s lolos: %q", v.nama, v.url)
			gagal++
		}
	}
	if gagal == 0 {
		t.Logf("✅ %d vektor pemutus-kutip ditolak", len(berbahaya))
	}
}

// TestValidasiUnduhURLSahDiterima memastikan URL wajar tidak ikut ditolak.
func TestValidasiUnduhURLSahDiterima(t *testing.T) {
	sah := []string{
		"https://contoh.com/file.zip",
		"http://localhost:8080/data.bin",
		"https://cdn.contoh.com/path/to/file.tar.gz",
		"https://contoh.com/file.zip?token=abc123&expires=999",
		"https://contoh.com/file.zip#section",
		"https://contoh.com/file%20dengan%20spasi.zip",
		"https://192.168.1.100:9000/bucket/objek",
		"https://contoh.com/a+b/c-d_e.f~g",
	}
	for _, u := range sah {
		if ms := ValidasiUnduhURL(u); ms != "" {
			t.Errorf("URL sah ditolak: %q → %s", u, ms)
		}
	}
	t.Logf("✅ %d URL sah diterima", len(sah))
}

// TestShellQuoteBenarBenarMencegahEksekusi adalah test KEAMANAN paling penting.
// Membuktikan bahwa URL berbahaya yang dibungkus shellQuote benar-benar menjadi
// teks literal — bukan hanya "lolos validasi", tapi tidak dieksekusi shell.
func TestShellQuoteBenarBenarMencegahEksekusi(t *testing.T) {
	// Penanda: kalau injeksi berhasil, file ini akan terbuat.
	penanda := filepath.Join(t.TempDir(), "INJEKSI_BERHASIL")
	os.Remove(penanda)

	vektor := []string{
		// Setiap URL mencoba membuat file penanda.
		fmt.Sprintf("https://c.com/x; touch %s", penanda),
		fmt.Sprintf("https://c.com/x && touch %s", penanda),
		fmt.Sprintf("https://c.com/x | touch %s", penanda),
		fmt.Sprintf("https://c.com/x`touch %s`", penanda),
		fmt.Sprintf("https://c.com/x$(touch %s)", penanda),
		fmt.Sprintf("https://c.com/x\ntouch %s", penanda),
	}

	for _, url := range vektor {
		// Simulasi persis seperti UnduhDariURL: shellQuote lalu dipakai di shell.
		perintah := fmt.Sprintf("echo %s", shellQuote(url))
		runBashCommand(perintah, 5)

		if _, err := os.Stat(penanda); err == nil {
			os.Remove(penanda)
			t.Fatalf("BAHAYA: injeksi berhasil dengan URL %q — file penanda terbuat!", url)
		}
	}
	t.Logf("✅ %d vektor injeksi TIDAK mengeksekusi perintah apa pun", len(vektor))
}

// TestUnduhDariURLMenolakTujuanTidakValid memverifikasi validasi tujuan.
func TestUnduhDariURLMenolakTujuanTidakValid(t *testing.T) {
	// Folder tujuan kosong harus ditolak sebelum curl dipanggil.
	_, _, err := UnduhDariURL("https://contoh.com/x.zip", "", "")
	if err == nil {
		t.Error("tujuan kosong harus ditolak")
	}

	// Tujuan berupa file (bukan folder) harus ditolak.
	dir := t.TempDir()
	file := filepath.Join(dir, "bukan-folder.txt")
	os.WriteFile(file, []byte("x"), 0644)
	_, _, err = UnduhDariURL("https://contoh.com/x.zip", file, "")
	if err == nil {
		t.Error("tujuan berupa file harus ditolak")
	}
	t.Log("✅ UnduhDariURL menolak tujuan tidak valid")
}
