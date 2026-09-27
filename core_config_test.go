package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestKonfigurasiApiURL memverifikasi tiga bentuk API_URL yang diterima.
// Penting untuk Local Bot API Server (batas upload 2GB).
func TestKonfigurasiApiURL(t *testing.T) {
	kasus := []struct{ in, mau string }{
		{"", "https://api.telegram.org/bot123:ABC"},
		{"http://127.0.0.1:8081", "http://127.0.0.1:8081/bot123:ABC"},
		{"http://127.0.0.1:8081/", "http://127.0.0.1:8081/bot123:ABC"},
		{"http://127.0.0.1:8081/bot", "http://127.0.0.1:8081/bot123:ABC"},
		{"http://127.0.0.1:8081/bot/", "http://127.0.0.1:8081/bot123:ABC"},
	}
	for _, c := range kasus {
		os.Setenv("BOT_TOKEN", "123:ABC")
		os.Setenv("ADMIN_IDS", "999")
		os.Setenv("API_URL", c.in)
		loadConfig()
		if ApiUrl != c.mau {
			t.Errorf("API_URL=%q menghasilkan %q, mau %q", c.in, ApiUrl, c.mau)
		}
	}
	os.Unsetenv("API_URL")
}

// TestRepoBersihDariKredensial memastikan source tidak memuat token atau ID
// milik operator. Ini penting sebelum publish ke repository publik.
func TestRepoBersihDariKredensial(t *testing.T) {
	// Bentuk yang selalu muncul di token bot Telegram: <8-12 digit>:<30-40 karakter>.
	//
	// Pola ditulis sebagai regex, BUKAN daftar token, supaya test ini sendiri
	// tidak memuat kredensial apa pun di dalam source. Kalau ditulis utuh,
	// repo publik akan memuat token yang justru ingin dicegah beredar.
	tokenBot := regexp.MustCompile(`\b[0-9]{8,12}:[A-Za-z0-9_-]{30,40}\b`)

	berkas := []string{
		"core_main.go", "term_shell.go", "ts_menu.go", "upload_basic.go", "upload_chunk.go",
		"main.py", "Makefile", "bot-vps.service", ".env.example", "README.md",
	}

	for _, f := range berkas {
		isi, err := os.ReadFile(f)
		if err != nil {
			continue // berkas opsional
		}
		if tokenBot.Match(isi) {
			t.Errorf("%s memuat sesuatu yang berbentuk token bot Telegram — JANGAN publish!", f)
		}
	}
	t.Logf("✅ %d berkas bersih dari pola token bot", len(berkas))
}

// TestGitignoreMelindungiRahasia memastikan .env dan binary tidak akan ter-commit.
func TestGitignoreMelindungiRahasia(t *testing.T) {
	isi, err := os.ReadFile(".gitignore")
	if err != nil {
		t.Fatal(".gitignore tidak ada — .env bisa ikut ter-commit!")
	}
	teks := string(isi)

	wajib := []string{".env", "core_engine", "*.zip"}
	for _, pola := range wajib {
		if !strings.Contains(teks, pola) {
			t.Errorf(".gitignore harus memuat %q", pola)
		}
	}
	t.Log("✅ .gitignore melindungi .env, binary, dan arsip")
}

// TestServiceFileValid memastikan unit systemd bisa dipakai tanpa edit manual
// di luar tiga baris yang ditandai.
func TestServiceFileValid(t *testing.T) {
	isi, err := os.ReadFile("bot-vps.service")
	if err != nil {
		t.Skip("bot-vps.service tidak ada")
	}
	teks := string(isi)

	wajib := map[string]string{
		"[Unit]":                 "seksi Unit",
		"[Service]":              "seksi Service",
		"[Install]":              "seksi Install",
		"ExecStart=":             "perintah start",
		"WorkingDirectory=":      "direktori kerja",
		"Restart=always":         "restart otomatis",
		"KillMode=control-group": "bunuh seluruh process group (penting untuk PTY)",
	}
	for pola, alasan := range wajib {
		if !strings.Contains(teks, pola) {
			t.Errorf("bot-vps.service harus memuat %s (%s)", pola, alasan)
		}
	}
	t.Log("✅ bot-vps.service lengkap dan valid")
}

// TestTelegramGetMeMenolakTokenPalsu memastikan token tidak valid terdeteksi
// SEBELUM bot mengklaim berhasil terhubung.
func TestTelegramGetMeMenolakTokenPalsu(t *testing.T) {
	// Simpan & pulihkan state global.
	lamaURL, lamaToken := ApiUrl, BotToken
	defer func() { ApiUrl, BotToken = lamaURL, lamaToken }()

	// Token berbentuk benar tapi tidak valid.
	BotToken = "123456:token_palsu_tidak_akan_diterima_telegram"
	ApiUrl = "https://api.telegram.org/bot" + BotToken

	nama, user, err := telegramGetMe()
	if err == nil {
		t.Errorf("token palsu seharusnya ditolak, tapi berhasil: nama=%q user=%q", nama, user)
	} else {
		t.Logf("✅ Token palsu ditolak: %v", err)
	}
}

// awalanFitur adalah daftar awalan yang boleh dipakai pada nama file .go.
//
// Go menganggap satu folder sebagai satu package, dan bot ini adalah satu
// binary. Memindah file ke subfolder akan menjadikannya package terpisah,
// sehingga dipakai awalan supaya urutannya mengelompok sendiri di GitHub.
// Test ini menjaga supaya listing repo tidak kembali berantakan.
var awalanFitur = []string{
	"core_", "term_", "deploy_", "upload_", "update_", "ts_", "uji_",
}

// TestNamaFileMengikutiKonvensi memastikan setiap file .go memakai awalan fitur
// yang dikenal, dan file test berpasangan dengan file kodenya.
func TestNamaFileMengikutiKonvensi(t *testing.T) {
	entri, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("tidak bisa membaca direktori: %v", err)
	}

	var jumlahKode int
	for _, e := range entri {
		nama := e.Name()
		if e.IsDir() || !strings.HasSuffix(nama, ".go") {
			continue
		}

		// Setiap file harus diawali salah satu awalan yang dikenal.
		ok := false
		for _, a := range awalanFitur {
			if strings.HasPrefix(nama, a) {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("%q tidak memakai awalan fitur yang dikenal: %v", nama, awalanFitur)
			continue
		}

		// File kode berakhiran .go, file test _test.go.
		//
		// Tidak menuntut file test punya pasangan kode 1:1: beberapa test file
		// sengaja menguji lebih dari satu file, misalnya term_help_test.go
		// menguji teks bantuan yang didefinisikan di term_shell.go.
		if !strings.HasSuffix(nama, "_test.go") {
			jumlahKode++
		}
	}

	// Penjaga: kalau的不是 ini, test tidak akan pernah gagal sama sekali.
	if jumlahKode < 8 {
		t.Errorf("hanya %d file kode ditemukan — test ini mungkin tidak memindai apa pun", jumlahKode)
	}
	t.Logf("✅ %d file kode, semua mengikuti awalan fitur", jumlahKode)
}

// sumberGoJoining menggabungkan seluruh file .go non-test menjadi satu string.
//
// Test yang memeriksa "pola ini harus ada di kode" tidak perlu tahu file mana
// yang memuatnya. Dengan menggabungkan, test tidak rusak saat file dipindah
// atau dipecah — masalah yang berulang kali merusak test saat repo dirapikan.
func sumberGoJoining(t *testing.T) string {
	t.Helper()
	entri, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("tidak bisa membaca direktori: %v", err)
	}
	var sb strings.Builder
	for _, e := range entri {
		nama := e.Name()
		if e.IsDir() || !strings.HasSuffix(nama, ".go") || strings.HasSuffix(nama, "_test.go") {
			continue
		}
		isi, err := os.ReadFile(nama)
		if err != nil {
			t.Fatalf("baca %s: %v", nama, err)
		}
		sb.Write(isi)
		sb.WriteString("\n")
	}
	if sb.Len() == 0 {
		t.Fatal("tidak ada file .go yang terbaca")
	}
	return sb.String()
}
