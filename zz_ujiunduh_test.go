package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestZZUnduhSungguhanDariGitHub mengunduh Release nyata lewat jalur yang
// dipakai bot, lalu memastikan seluruh langkah berhasil.
func TestZZUnduhSungguhanDariGitHub(t *testing.T) {
	if os.Getenv("UNDUH_UJI") == "" {
		t.Skip("SKIP — set UNDUH_UJI=1")
	}

	dir := t.TempDir()
	fmt.Println("\n=== KONDISI AWAL ===")
	fmt.Println("  Go tersedia :", goTersedia())
	fmt.Println("  metode      :", metodeUpdate())

	binLama := filepath.Join(dir, updateBinaryName)
	os.WriteFile(binLama, []byte("BINARY LAMA"), 0o755)

	fmt.Println("\n=== UNDUH DARI RELEASE SUNGGUHAN ===")
	klien := &http.Client{Timeout: 120 * time.Second}
	versi := versiTerbaruRilis(klien)
	fmt.Println("  versi terbaru:", versi)

	arch := namaArsitekturRilis("amd64")
	nama := "core_engine-linux-" + arch
	temp := filepath.Join(dir, updateTempName)

	n, err := ambilURL(klien, alamatRilis(versi, nama), temp, 64<<20)
	if err != nil {
		t.Fatalf("unduh gagal: %v", err)
	}
	fmt.Printf("  terunduh: %d byte (%.1f MB)\n", n, float64(n)/1024/1024)

	daftar := bacaSHA256SuMS(klien, versi)
	fmt.Println("  entri checksum:", len(daftar))

	terverifikasi, pesan := periksaChecksum(temp, nama, daftar)
	if pesan != "" {
		t.Fatalf("checksum gagal: %s", pesan)
	}
	if !terverifikasi {
		t.Error("checksum tidak terverifikasi")
	} else {
		fmt.Println("  ✅ checksum cocok dengan SHA256SUMS resmi")
	}

	os.Chmod(temp, 0o755)
	if err := ujiBinary(temp, dir); err != nil {
		t.Fatalf("uji jalan gagal: %v", err)
	}
	fmt.Println("  ✅ binary lolos uji jalan")

	isi, _ := os.ReadFile(binLama)
	if string(isi) != "BINARY LAMA" {
		t.Error("❌ binary lama tersentuh sebelum pemasangan")
	} else {
		fmt.Println("  ✅ binary lama masih utuh")
	}
	fmt.Println("\n✅ SELURUH JALUR UNDUH BERHASIL DENGAN RELEASE SUNGGUHAN")
}

// TestZZVpsTanpaGoMemilihUnduh adalah skenario inti fitur ini.
//
// VPS spek rendah tanpa Go toolchain HARUS memakai jalur unduh, dan seluruh
// alur unduh harus bekerja sampai binary siap dipasang — tanpa kompilasi
// sama sekali.
func TestZZVpsTanpaGoMemilihUnduh(t *testing.T) {
	if os.Getenv("UNDUH_UJI") == "" {
		t.Skip("SKIP — set UNDUH_UJI=1")
	}

	fmt.Println("\n=== SIMULASI VPS SPEK RENDAH (TANPA GO) ===")

	// Metode yang akan dipilih bila Go tidak ada.
	metode := pilihMetode(false)
	fmt.Println("  metode terpilih:", metode)
	if metode != metodeUnduh {
		t.Fatalf("❌ VPS tanpa Go seharusnya memakai jalur unduh, dapat %q", metode)
	}

	dir := t.TempDir()

	// Binary lama sebagai korban — harus selamat sampai pemasangan sukses.
	binLama := filepath.Join(dir, updateBinaryName)
	isiLama := []byte("BINARY LAMA YANG SEDANG MELAYANI")
	os.WriteFile(binLama, isiLama, 0o755)

	// Jalankan SELURUH alur unduh seperti di VPS.
	fmt.Println("\n=== MENJALANKAN ALUR UNDUH ===")
	sukses, pesan, _ := unduhDanPasang(dir, func(tahap string) {
		fmt.Println("  [tahap]", tahap)
	})

	fmt.Println("\n  sukses:", sukses)
	if !sukses {
		t.Fatalf("❌ alur unduh gagal: %s", pesan)
	}

	// Binary baru harus sudah terpasang.
	fi, err := os.Stat(binLama)
	if err != nil {
		t.Fatalf("binary tidak ada: %v", err)
	}
	fmt.Printf("  ✅ binary terpasang: %d byte\n", fi.Size())
	if fi.Size() < 1<<20 {
		t.Errorf("❌ binary terpasang terlalu kecil: %d byte", fi.Size())
	}

	// Dan benar-benar bisa dijalankan.
	if err := ujiBinary(binLama, dir); err != nil {
		t.Errorf("❌ binary terpasang tidak bisa dijalankan: %v", err)
	} else {
		fmt.Println("  ✅ binary terpasang lolos uji jalan")
	}

	// Tidak ada berkas sementara tertinggal.
	if _, err := os.Stat(filepath.Join(dir, updateTempName)); err == nil {
		t.Error("⚠️ berkas sementara tertinggal")
	} else {
		fmt.Println("  ✅ tidak ada berkas sementara tertinggal")
	}

	fmt.Println("\n✅ VPS TANPA GO BERHASIL UPDATE TANPA KOMPILASI")
}

// TestZZReleaseTidakAdaMenjagaBinaryLama memastikan bila Release tidak ada,
// binary lama TIDAK tersentuh.
//
// Skenario nyata: repo belum pernah di-tag, atau tag dihapus. Bot harus
// tetap melayani dengan versi lama, bukan mati.
func TestZZReleaseTidakAdaMenjagaBinaryLama(t *testing.T) {
	if os.Getenv("UNDUH_UJI") == "" {
		t.Skip("SKIP — set UNDUH_UJI=1")
	}

	dir := t.TempDir()
	binLama := filepath.Join(dir, updateBinaryName)
	isiLama := []byte("#!/bin/sh\necho lama\n")
	os.WriteFile(binLama, isiLama, 0o755)

	// Arahkan ke repo yang pasti tidak punya Release.
	lamaRepo := repoTuanRumah
	repoTuanRumah = "arewedaks/repo-yang-tidak-pernah-ada-xyz"
	defer func() { repoTuanRumah = lamaRepo }()

	fmt.Println("\n=== SKENARIO: RELEASE BELUM DIBUAT ===")
	sukses, pesan, _ := unduhDanPasang(dir, nil)
	fmt.Println("  sukses:", sukses)

	if sukses {
		t.Fatal("❌ seharusnya gagal saat Release tidak ada")
	}
	if pesan == "" {
		t.Fatal("❌ tidak ada pesan error untuk pengguna")
	}
	fmt.Println("  pesan:", strings.Split(pesan, "\n")[0])

	// Binary lama harus utuh.
	isi, err := os.ReadFile(binLama)
	if err != nil {
		t.Fatalf("❌ binary lama hilang: %v", err)
	}
	if string(isi) != string(isiLama) {
		t.Error("❌ binary lama berubah padahal update gagal")
	} else {
		fmt.Println("  ✅ binary lama utuh, bot tetap melayani")
	}

	// Tidak ada berkas sementara tertinggal.
	if _, err := os.Stat(filepath.Join(dir, updateTempName)); err == nil {
		t.Error("⚠️ berkas sementara tertinggal setelah gagal")
	} else {
		fmt.Println("  ✅ tidak ada sisa berkas sementara")
	}
}

// TestZZChecksumRusakMenjagaBinaryLama memastikan berkas yang checksum-nya
// tidak cocok ditolak, dan binary lama tetap utuh.
//
// Skenario nyata: unduhan terpotong, proxy korporat menyuntik konten, atau
// CDN mengirim halaman error. Kalau berkas begini dipasang, bot mati.
func TestZZChecksumRusakMenjagaBinaryLama(t *testing.T) {
	if os.Getenv("UNDUH_UJI") == "" {
		t.Skip("SKIP — set UNDUH_UJI=1")
	}

	dir := t.TempDir()
	binLama := filepath.Join(dir, updateBinaryName)
	isiLama := []byte("BINARY LAMA YANG SEDANG MELAYANI PRODUKSI")
	os.WriteFile(binLama, isiLama, 0o755)

	fmt.Println("\n=== SKENARIO: UNDUHAN RUSAK / DIUBAH DI TENGAH JALAN ===")

	// Unduh Sungguhan lalu ubah isinya sedikit — meniru unduhan yang rusak.
	klien := &http.Client{Timeout: 120 * time.Second}
	versi := versiTerbaruRilis(klien)
	arch := namaArsitekturRilis("amd64")
	nama := "core_engine-linux-" + arch

	temp := filepath.Join(dir, updateTempName)
	if _, err := ambilURL(klien, alamatRilis(versi, nama), temp, 64<<20); err != nil {
		t.Fatalf("unduh: %v", err)
	}

	// Rusakkan satu byte di tengah berkas.
	data, _ := os.ReadFile(temp)
	data[len(data)/2] ^= 0xFF
	os.WriteFile(temp, data, 0o755)

	// Checksum resmi tetap yang asli → harus TIDAK cocok.
	daftar := bacaSHA256SuMS(klien, versi)
	terverifikasi, pesan := periksaChecksum(temp, nama, daftar)

	if terverifikasi {
		t.Fatal("❌ berkas rusak dinyatakan terverifikasi — bot akan mati setelah restart!")
	}
	if pesan == "" {
		t.Fatal("❌ tidak ada pesan penolakan")
	}
	fmt.Println("  ✅ berkas rusak ditolak")
	fmt.Println("  pesan:", strings.Split(pesan, "\n")[0])

	// Binary lama harus utuh.
	isi, _ := os.ReadFile(binLama)
	if string(isi) != string(isiLama) {
		t.Error("❌ binary lama berubah padahal checksum gagal")
	} else {
		fmt.Println("  ✅ binary lama utuh, bot tetap melayani")
	}
}
