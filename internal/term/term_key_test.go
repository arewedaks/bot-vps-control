package term

// ==============================================================================
// 🧪 UJI TOMBOL KUNCI TERMINAL — PANAH, TAB, ESC, ENTER
// ==============================================================================
// Tombol panah di panel Telegram mengirim urutan ANSI mentah ke PTY.
// Uji ini membuktikan urutan itu benar-benar DITERIMA shell readline,
// bukan sekadar "tidak error saat ditulis".
//
// Cara bukti: sisipkan teks, kirim panah kiri, sisipkan lagi.
// Kalau panah kiri bekerja, teks kedua mendarat DI TENGAH — bukan di ujung.
//
// Jalankan: go test -run TestTermKey -v ./...
// Skip otomatis bila lingkungan tidak punya PTY (sandbox/container).

import (
	"strings"
	"testing"
	"time"
)

// ptySessionForTest menyiapkan sesi bersih dengan nama fungsi yang jelas.
func ptySessionForTest(t *testing.T, uid int64) {
	t.Helper()
	ok, why := ptyAvailable()
	if !ok {
		t.Skipf("SKIP — PTY tidak tersedia: %s", why)
	}
	if _, err := GetOrCreateSession(uid); err != nil {
		t.Fatalf("GetOrCreateSession gagal: %v", err)
	}
	t.Cleanup(func() { KillSession(uid) })
}

// TestTermKeyTableLengkap memastikan setiap tombol yang tampil di panel
// punya urutan kunci yang terdaftar. Tombol tanpa urutan = tombol mati.
func TestTermKeyTableLengkap(t *testing.T) {
	// Tombol dari RenderTerminal (internal/term/term_shell.go).
	wajib := []string{
		"u", "d", "l", "rt", // panah: atas, bawah, kiri, kanan
		"t", "esc", "en", // Tab, Esc, Enter
	}
	for _, aksi := range wajib {
		seq, ok := keySeqs[aksi]
		if !ok || seq == "" {
			t.Errorf("tombol %q tidak punya urutan kunci — tombol akan mati", aksi)
		}
	}

	// Semua yang bukan tombol kunci harus TIDAK ada di tabel, supaya
	// r/r/i/k/c/h tetap jatuh ke switch handler biasa.
	for _, aksi := range []string{"r", "i", "k", "c", "h"} {
		if _, ok := keySeqs[aksi]; ok {
			t.Errorf("aksi %q tidak boleh jadi tombol kunci — bentrok dengan handler panel", aksi)
		}
	}

	// Bentuk urutan ANSI: panah selalu CSI (ESC '[' huruf).
	for aksi, seq := range keySeqs {
		if !strings.HasPrefix(seq, "\x1b") && seq != "\t" && seq != "\r" {
			t.Errorf("urutan %q (%s) bukan escape/control yang sah", aksi, seq)
		}
	}
}

// TestTermKeyPanahKiriMenyisipkanTengah — bukti nyata panah menggerakkan kursor.
func TestTermKeyPanahKiriMenyisipkanTengah(t *testing.T) {
	const uid int64 = 770101
	ptySessionForTest(t, uid)

	// Kosongkan baris perintah lebih dulu.
	if err := SendRawKey(uid, "\x15"); err != nil { // Ctrl+U: hapus baris
		t.Fatalf("Ctrl+U gagal: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Tulis "ECHO_TES" tanpa Enter — perintah belum dijalankan.
	if err := SendRawKey(uid, "echo AKHIR"); err != nil {
		t.Fatalf("tulis gagal: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Panah kiri 5x → kursor mundur sebelum "AKHIR".
	for i := 0; i < 5; i++ {
		if err := SendRawKey(uid, "\x1b[D"); err != nil {
			t.Fatalf("panah kiri gagal: %v", err)
		}
		time.Sleep(80 * time.Millisecond)
	}

	// Sisipkan teks di posisi kursor.
	if err := SendRawKey(uid, " TENGAH"); err != nil {
		t.Fatalf("sisip gagal: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Jalankan dengan Enter (juga lewat SendRawKey, bukan SendToTerminal).
	if err := SendRawKey(uid, "\r"); err != nil {
		t.Fatalf("Enter gagal: %v", err)
	}
	time.Sleep(600 * time.Millisecond)

	out, err := ReadScreen(uid)
	if err != nil {
		t.Fatalf("ReadScreen gagal: %v", err)
	}

	// Kursor bergerak → "echo AKHIR" jadi "echo AK TENGAH HIR"-ish.
	// Yang penting: hasilnya BUKAN "echo AKHIR TENGAH" (itu artinya
	// kursor tidak bergerak dan tombol panah tidak bekerja).
	if strings.Contains(out, "AKHIR TENGAH") {
		t.Errorf("❌ kursor tidak bergerak — panah kiri tidak diterima shell.\nOutput:\n%s", out)
	}
	if !strings.Contains(out, "TENGAH") {
		t.Errorf("❌ teks sisipan tidak muncul sama sekali.\nOutput:\n%s", out)
	}
	t.Log("✅ Panah kiri menggerakkan kursor, Enter menjalankan perintah")
}

// TestTermKeyTabMelengkapi — Tab harus memicu pelengkapan path shell.
func TestTermKeyTabMelengkapi(t *testing.T) {
	const uid int64 = 770102
	ptySessionForTest(t, uid)

	// Tulis awal path yang unik lalu Tab: shell melengkapi jadi "TERMINAL/".
	if err := SendRawKey(uid, "echo /usr/share/doc/bash"); err != nil {
		t.Fatalf("tulis gagal: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Ctrl+U bersihkan, lalu uji Tab pada direktori yang pasti ada.
	_ = SendRawKey(uid, "\x15")
	time.Sleep(150 * time.Millisecond)

	if err := SendRawKey(uid, "ls /usr/sha"); err != nil {
		t.Fatalf("tulis gagal: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := SendRawKey(uid, "\t"); err != nil {
		t.Fatalf("Tab gagal: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := SendRawKey(uid, "\r"); err != nil {
		t.Fatalf("Enter gagal: %v", err)
	}
	time.Sleep(700 * time.Millisecond)

	out, err := ReadScreen(uid)
	if err != nil {
		t.Fatalf("ReadScreen gagal: %v", err)
	}
	// Tab melengkapi /usr/sha → /usr/share, jadi ls berhasil dan
	// menampilkan isi /usr/share (mis. "doc" atau "man").
	if !strings.Contains(out, "doc") && !strings.Contains(out, "man") {
		t.Errorf("❌ Tab tidak melengkapi path. Output:\n%s", out)
	}
	t.Log("✅ Tab menyelesaikan nama path di shell")
}

// TestTermKeyTanpaSesiTidakPanik — sesi mati harus mengembalikan error, bukan panic.
func TestTermKeyTanpaSesiTidakPanik(t *testing.T) {
	const uid int64 = 770103
	KillSession(uid) // pastikan tidak ada sesi

	if err := SendRawKey(uid, "\x1b[A"); err == nil {
		t.Error("SendRawKey tanpa sesi seharusnya mengembalikan error")
	} else {
		t.Logf("✅ error jelas tanpa sesi: %v", err)
	}
}
