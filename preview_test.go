package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper tampil mencetak tampilan FM untuk ditinjau mata.
func tampil(t *testing.T, judul, dir string, page int) {
	t.Helper()
	text, kb := renderFileManager(dir, page)
	var b strings.Builder
	fmt.Fprintf(&b, "\n========== %s ==========\n%s\n", judul, text)
	for i, row := range kb.InlineKeyboard {
		fmt.Fprintf(&b, "  baris %d: ", i)
		for j, btn := range row {
			if j > 0 {
				b.WriteString(" │ ")
			}
			b.WriteString(btn.Text)
		}
		b.WriteString("\n")
	}
	t.Log(b.String()) // hanya tampil dengan -v
}

// TestPreviewSemuaKasus meninjau tampilan di berbagai keadaan.
func TestPreviewSemuaKasus(t *testing.T) {
	// 1. Folder kosong
	kosong := t.TempDir()
	tampil(t, "FOLDER KOSONG", kosong, 0)

	// 2. Folder dengan banyak item (uji pagination)
	banyak := t.TempDir()
	for i := 0; i < 25; i++ {
		os.MkdirAll(filepath.Join(banyak, fmt.Sprintf("folder_%02d", i)), 0755)
	}
	for i := 0; i < 10; i++ {
		os.WriteFile(filepath.Join(banyak, fmt.Sprintf("file_%02d.txt", i)),
			make([]byte, 1024*(i+1)), 0644)
	}
	tampil(t, "BANYAK ITEM — HALAMAN 1", banyak, 0)
	tampil(t, "BANYAK ITEM — HALAMAN 2", banyak, 1)

	// 3. Nama sangat panjang
	panjang := t.TempDir()
	namaPanjang := "konfigurasi_produksi_server_nginx_2024_final_revisi_terakhir.yaml"
	os.WriteFile(filepath.Join(panjang, namaPanjang), make([]byte, 5000), 0644)
	os.WriteFile(filepath.Join(panjang, ".env.production.backup.old"), make([]byte, 200), 0644)
	os.MkdirAll(filepath.Join(panjang, "direktori_dengan_nama_yang_sangat_panjang_sekali"), 0755)
	tampil(t, "NAMA PANJANG", panjang, 0)

	// 4. Berbagai jenis file (uji ikon)
	jenis := t.TempDir()
	for nama, ukuran := range map[string]int{
		"script.sh":    500,
		"arsip.tar.gz": 1000000,
		"config.yaml":  2000,
		"data.json":    3000,
		"server.log":   50000,
		"gambar.png":   45000,
		"backup.zip":   5000000,
		"catatan.md":   1200,
		"binary.bin":   8000,
	} {
		os.WriteFile(filepath.Join(jenis, nama), make([]byte, ukuran), 0644)
	}
	tampil(t, "BERBAGAI JENIS FILE", jenis, 0)

	// 5. Folder tidak bisa dibaca
	tampil(t, "FOLDER TIDAK ADA", "/tidak/ada/folder/ini", 0)
}

// TestTruncateMidMempertahankanEkstensi memverifikasi perbaikan utama.
func TestTruncateMidMempertahankanEkstensi(t *testing.T) {
	kasus := []struct {
		masuk    string
		maks     int
		harusAda string // bagian yang WAJIB tetap terlihat
	}{
		{"konfigurasi_produksi_server.yaml", 20, ".yaml"},
		{"backup_2024_09_25.tar.gz", 20, ".gz"},
		{"sangat_panjang_sekali_namanya.txt", 18, ".txt"},
		{"README.md", 30, "README.md"}, // tidak dipotong
	}

	for _, c := range kasus {
		hasil := truncateMid(c.masuk, c.maks)
		if !contains(hasil, c.harusAda) {
			t.Errorf("truncateMid(%q, %d) = %q — kehilangan %q",
				c.masuk, c.maks, hasil, c.harusAda)
		}
		if len([]rune(hasil)) > c.maks {
			t.Errorf("truncateMid(%q, %d) = %q (%d rune) — terlalu panjang",
				c.masuk, c.maks, hasil, len([]rune(hasil)))
		}
	}
	t.Log("✅ Ekstensi file selalu terlihat setelah pemotongan")
}

// TestTruncateMidAmanUnicode memastikan tidak merusak emoji.
func TestTruncateMidAmanUnicode(t *testing.T) {
	hasil := truncateMid("file_dengan_emoji_🔥_dan_nama_panjang.txt", 20)
	for _, r := range hasil {
		if r == '\uFFFD' {
			t.Errorf("hasil rusak: %q", hasil)
			break
		}
	}
	t.Logf("✅ Unicode aman: %q", hasil)
}

// TestIsDirEmpty memverifikasi deteksi folder kosong.
func TestIsDirEmpty(t *testing.T) {
	kosong := t.TempDir()
	if !isDirEmpty(kosong) {
		t.Error("folder baru harus terdeteksi kosong")
	}

	ada := t.TempDir()
	os.WriteFile(filepath.Join(ada, "x.txt"), []byte("x"), 0644)
	if isDirEmpty(ada) {
		t.Error("folder berisi harus terdeteksi tidak kosong")
	}

	if isDirEmpty("/tidak/ada") {
		t.Error("folder tidak ada seharusnya bukan 'kosong'")
	}
	t.Log("✅ Deteksi folder kosong bekerja")
}
