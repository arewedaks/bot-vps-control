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
		"main.go", "terminal.go", "tailscale.go", "upload.go", "uploadbesar.go",
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
