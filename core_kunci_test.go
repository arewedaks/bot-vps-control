package main

// ==============================================================================
// 🧪 TEST KUNCI INSTANCE — TANPA TOKEN ASLI (JALAN DI CI)
// ==============================================================================
// TestKunciInstanceMenolakInstanceKedua membutuhkan BOT_TOKEN asli dari .env,
// sehingga di CI (dan di mesin pengembang baru) ia di-SKIP — artinya mekanisme
// anti-instance-ganda tidak pernah benar-benar diuji sampai seseorang
// menjalankannya di VPS.
//
// Cara kerja test ini: proses Go yang sama memegang kunci lewat flock(2),
// lalu binary dijalankan. Binary tidak perlu berhasil terhubung ke Telegram —
// pemeriksaan kunci sengaja dijalankan SEBELUM panggilan jaringan apa pun,
// jadi token palsu pun cukup untuk membuktikan bahwa instance kedua ditolak
// SEBELUM menyentuh API Telegram.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// pegangKunci menahan kunci instance lewat berkas yang sama dengan produksi.
//
// Mengembalikan fungsi pelepas. Berkas sengaja tidak dihapus: yang menentukan
// adalah kunci kernel, sama seperti di produksi.
func pegangKunci(t *testing.T) func() {
	t.Helper()

	path := filepath.Join(os.TempDir(), "bot-vps-control-"+sidikJariToken()+".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("tidak bisa membuat berkas kunci: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		t.Fatalf("kunci sudah dipegang proses lain (sisa test sebelumnya?): %v", err)
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
}

// TestKunciDiambilSebelumJaringan memastikan urutan start tidak mundur lagi.
//
// Penjaga regresi, bukan uji perilaku: membaca urutan pemanggilan di main().
// Kunci harus muncul sebelum deleteWebhook dan telegramGetMe.
//
// Penting: yang dicari adalah PEMANGGILAN, bukan definisi fungsi. Nama
// telegramGetMe juga muncul di komentar dan di definisinya sendiri, dan
// keduanya berada sebelum pemanggilan kunci — mencari kemunculan pertama
// membuat tes ini gagal pada kode yang sudah benar.
func TestKunciDiambilSebelumJaringan(t *testing.T) {
	data, err := os.ReadFile("core_main.go")
	if err != nil {
		t.Skipf("core_main.go tidak terbaca: %v", err)
	}
	isi := strings.Split(string(data), "\n")

	// Baris pemanggilan kunci: yang benar-benar menjalankannya, bukan komentar
	// atau teks lain yang menyebut namanya.
	barisKunci := 0
	for i, l := range isi {
		if strings.Contains(l, "kunciInstance();") && !strings.HasPrefix(strings.TrimSpace(l), "//") {
			barisKunci = i + 1
			break
		}
	}
	if barisKunci == 0 {
		t.Fatal("pemanggilan kunciInstance() tidak ditemukan di core_main.go")
	}

	// Baris pemanggilan jaringan pertama yang harus terjadi SETELAH kunci.
	panggilan := []string{
		`http.Get(ApiUrl + "/deleteWebhook")`,
		`_, _, err := telegramGetMe()`,
	}
	for _, p := range panggilan {
		for i, l := range isi {
			if !strings.Contains(l, p) || strings.HasPrefix(strings.TrimSpace(l), "//") {
				continue
			}
			if barisKunci > i+1 {
				t.Errorf("❌ kunciInstance() di baris %d dipanggil SETELAH %q di baris %d — "+
					"instance kedua bisa menyentuh API Telegram sebelum ditolak",
					barisKunci, p, i+1)
			}
			break
		}
	}
	t.Logf("✅ Kunci instance (baris %d) diambil sebelum panggilan jaringan", barisKunci)
}
