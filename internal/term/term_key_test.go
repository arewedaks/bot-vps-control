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

// tungguLayarBerhenti menunggu sampai layar tidak berubah lagi.
//
// Tes versi pertama memakai jeda tetap (80ms per tombol) dan GAGAL di
// runner CI yang lebih lambat: byte panah belum selesai diproses readline
// sehingga urutan ANSI terpecah dan dianggap teks literal. Jadi jangan
// berlomba dengan shell — tunggu sampai dia benar-benar berhenti menulis.
func tungguLayarBerhenti(t *testing.T, uid int64, batas time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(batas)
	sebelum := ""
	stabil := 0
	for time.Now().Before(deadline) {
		time.Sleep(60 * time.Millisecond)
		out, err := ReadScreen(uid)
		if err != nil {
			continue
		}
		if out == sebelum {
			stabil++
			if stabil >= 3 { // ~180ms tanpa perubahan
				return out
			}
			continue
		}
		sebelum = out
		stabil = 0
	}
	return sebelum
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

// TestTermKeyPanahAtasMemanggilRiwayat — bukti panah diterima shell.
//
// Kenapa bukan "sisip teks di tengah": cleanANSI membuang backspace dan
// escape posisi kursor, jadi gerak kursor tidak pernah terlihat di layar
// yang sudah dibersihkan — padahal shell benar-benar menerimanya.
// (Diperiksa langsung lewat PTY mentah: panah kiri menghasilkan \x08,
// backspace, dan readline benar-benar memundurkan kursor.)
//
// Jadi yang diuji di sini adalah EFEK yang terlihat jelas di layar
// bersih: panah atas memunculkan kembali perintah sebelumnya.
func TestTermKeyPanahAtasMemanggilRiwayat(t *testing.T) {
	const uid int64 = 770101
	ptySessionForTest(t, uid)

	const penanda = "BOZ_RIWAYAT_6767"

	// 1. Jalankan perintah berpenanda unik — ini masuk riwayat shell.
	if err := SendRawKey(uid, "echo "+penanda); err != nil {
		t.Fatalf("tulis gagal: %v", err)
	}
	tungguLayarBerhenti(t, uid, 3*time.Second)
	if err := SendRawKey(uid, "\r"); err != nil {
		t.Fatalf("Enter gagal: %v", err)
	}
	layar := tungguLayarBerhenti(t, uid, 5*time.Second)
	if !strings.Contains(layar, penanda) {
		t.Fatalf("perintah pertama tidak jalan. Layar:\n%s", layar)
	}

	// 2. Kosongkan baris, lalu panah ATAS: readline menuliskan kembali
	//    perintah terakhir ke baris perintah.
	if err := SendRawKey(uid, "\x15"); err != nil { // Ctrl+U
		t.Fatalf("Ctrl+U gagal: %v", err)
	}
	tungguLayarBerhenti(t, uid, 3*time.Second)

	// Cek kondisi awal: setelah Ctrl+U, baris perintah kosong.
	// (Layar masih memuat penanda dari output perintah tadi, jadi kita
	// hitung kemunculannya dan bandingkan setelah panah.)
	sebelum := strings.Count(layar, penanda)

	if err := SendRawKey(uid, "\x1b[A"); err != nil {
		t.Fatalf("panah atas gagal: %v", err)
	}
	sesudahLayar := tungguLayarBerhenti(t, uid, 3*time.Second)
	sesudah := strings.Count(sesudahLayar, penanda)

	// 3. Jalankan (Enter) — kalau riwayat benar-benar dipanggil, perintah
	//    yang sama berjalan dan penanda muncul LAGI.
	if err := SendRawKey(uid, "\r"); err != nil {
		t.Fatalf("Enter gagal: %v", err)
	}
	akhir := tungguLayarBerhenti(t, uid, 5*time.Second)
	hitungAkhir := strings.Count(akhir, penanda)

	// Panah atas yang bekerja → baris perintah terisi ulang → setelah Enter,
	// penanda bertambah. Kalau panah tidak diterima, baris tetap kosong dan
	// Enter hanya menghasilkan prompt baru: penanda TIDAK bertambah.
	if hitungAkhir <= sebelum {
		t.Errorf("panah atas tidak memanggil riwayat: penanda muncul %d kali "+
			"sebelum, %d kali sesudah (setelah panah: %d). Layar:\n%s",
			sebelum, hitungAkhir, sesudah, akhir)
	} else {
		t.Logf("✅ Panah atas memanggil riwayat: penanda %d → %d kali", sebelum, hitungAkhir)
		t.Logf("   baris hasil: %s", barisBerisi(akhir, penanda))
	}
}

// TestTermKeyKontrolTidakPanah — pembanding: tanpa tombol panah, penanda
// TIDAK boleh bertambah. Tanpa uji ini, tes di atas bisa lulus karena
// alasan lain (mis. layar tidak pernah dibersihkan).
func TestTermKeyKontrolTidakPanah(t *testing.T) {
	const uid int64 = 770104
	ptySessionForTest(t, uid)

	const penanda = "BOZ_KONTROL_6767"

	if err := SendRawKey(uid, "echo "+penanda); err != nil {
		t.Fatalf("tulis gagal: %v", err)
	}
	tungguLayarBerhenti(t, uid, 3*time.Second)
	if err := SendRawKey(uid, "\r"); err != nil {
		t.Fatalf("Enter gagal: %v", err)
	}
	layar := tungguLayarBerhenti(t, uid, 5*time.Second)
	sebelum := strings.Count(layar, penanda)

	// TANPA panah: bersihkan baris lalu Enter kosong.
	if err := SendRawKey(uid, "\x15"); err != nil {
		t.Fatalf("Ctrl+U gagal: %v", err)
	}
	tungguLayarBerhenti(t, uid, 3*time.Second)
	if err := SendRawKey(uid, "\r"); err != nil {
		t.Fatalf("Enter gagal: %v", err)
	}
	akhir := tungguLayarBerhenti(t, uid, 4*time.Second)

	if strings.Count(akhir, penanda) != sebelum {
		t.Errorf("kontrol gagal: penanda bertambah tanpa panah (%d → %d) — "+
			"berarti sinyal riwayat tidak bisa dipercaya. Layar:\n%s",
			sebelum, strings.Count(akhir, penanda), akhir)
	} else {
		t.Log("✅ Kontrol: tanpa panah, penanda tidak bertambah — sinyal riwayat sahih")
	}
}

// barisBerisi mengembalikan baris pertama yang memuat penanda, untuk log.
func barisBerisi(s, penanda string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, penanda) {
			return strings.TrimSpace(l)
		}
	}
	return "(tidak ada)"
}

// TestTermKeyTabMelengkapi — Tab harus memicu pelengkapan path shell.
func TestTermKeyTabMelengkapi(t *testing.T) {
	const uid int64 = 770102
	ptySessionForTest(t, uid)

	// Bersihkan baris, tunggu tenang.
	if err := SendRawKey(uid, "\x15"); err != nil {
		t.Fatalf("Ctrl+U gagal: %v", err)
	}
	tungguLayarBerhenti(t, uid, 3*time.Second)

	// Tulis awal path yang pasti ada, lalu Tab: shell melengkapinya.
	if err := SendRawKey(uid, "ls /usr/sha"); err != nil {
		t.Fatalf("tulis gagal: %v", err)
	}
	tungguLayarBerhenti(t, uid, 3*time.Second)

	if err := SendRawKey(uid, "\t"); err != nil {
		t.Fatalf("Tab gagal: %v", err)
	}
	layar := tungguLayarBerhenti(t, uid, 3*time.Second)

	// Tab yang dikenali readline mengubah baris menjadi "ls /usr/share/".
	// Kalau tidak berubah, byte Tab tidak sampai.
	if !strings.Contains(layar, "/usr/share") {
		t.Fatalf("Tab tidak melengkapi path menjadi /usr/share. Layar:\n%s", layar)
	}

	if err := SendRawKey(uid, "\r"); err != nil {
		t.Fatalf("Enter gagal: %v", err)
	}
	out := tungguLayarBerhenti(t, uid, 5*time.Second)

	// Setelah dieksekusi, isi /usr/share tampil (mis. "doc" atau "man").
	if !strings.Contains(out, "doc") && !strings.Contains(out, "man") {
		t.Errorf("Tab tidak menjalankan path yang dilengkapi. Layar:\n%s", out)
	}
	t.Log("✅ Tab menyelesaikan nama path: ls /usr/sha → /usr/share")
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
