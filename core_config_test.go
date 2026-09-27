package main

import (
	"bot-vps-control/internal/tg"
	"os"
	"path/filepath"
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
	var jumlahKode int

	// FailTelusuri seluruh package: root dan setiap subfolder internal/.
	err := filepath.Walk(".", func(p string, e os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			if p != "." && (strings.HasPrefix(e.Name(), ".") || e.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		nama := e.Name()
		if !strings.HasSuffix(nama, ".go") {
			return nil
		}
		rel := strings.TrimPrefix(p, "./")
		diRoot := !strings.Contains(rel, "/")
		// Hanya file root yang wajib berawalan fitur. File di dalam package
		// bernama sudah terkelompok lewat nama foldernya.
		if diRoot {
			ok := false
			for _, a := range awalanFitur {
				if strings.HasPrefix(nama, a) {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("%q di root tidak memakai awalan fitur: %v", rel, awalanFitur)
			}
		}
		if !strings.HasSuffix(nama, "_test.go") {
			jumlahKode++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("gagal menelusuri: %v", err)
	}

	// Fail Penjaga: kalau bukan ini, test tidak akan pernah gagal sama sekali.
	if jumlahKode < 8 {
		t.Errorf("hanya %d file kode ditemukan — test ini mungkin tidak memindai apa pun", jumlahKode)
	}
	t.Logf("✅ %d file kode, root mengikuti awalan fitur", jumlahKode)
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

// TestBridgeTgMenerimaKonfigurasi memastikan nilai Telegram benar-benar
// sampai ke package internal/tg.
//
// Lapisan klien sudah pindah ke sana, tapi root masih memegang salinannya.
// Kalau loadConfig lupa menyalin, bot tetap jalan di RootTesting: semua
// pemanggilan lewat bridge menjadi sia-sia karena endpoint-nya kosong.
// Gejalanya diam dan sulit dilacak, jadi diikat test.
func TestBridgeTgMenerimaKonfigurasi(t *testing.T) {
	// Simpan & pulihkan seluruh state global yang disentuh.
	lamaURL, lamaToken := ApiUrl, BotToken
	lamaTgURL, lamaTgToken := tg.ApiUrl, tg.BotToken
	defer func() {
		ApiUrl, BotToken = lamaURL, lamaToken
		tg.ApiUrl, tg.BotToken = lamaTgURL, lamaTgToken
	}()

	// Env di-set eksplisit supaya hasil tidak ikut terbaca dari mesin test.
	t.Setenv("BOT_TOKEN", "123456:UjiBridge_token_dummy")
	loadConfig()

	if BotToken == "" {
		t.Fatal("loadConfig tidak mengisi BotToken dari BOT_TOKEN")
	}
	if tg.BotToken != BotToken {
		t.Errorf("tg.BotToken = %q, harusnya sama dengan root BotToken = %q",
			tg.BotToken, BotToken)
	}
	if tg.ApiUrl != ApiUrl {
		t.Errorf("tg.ApiUrl = %q, harusnya sama dengan root ApiUrl = %q",
			tg.ApiUrl, ApiUrl)
	}
	if tg.ApiUrl == "" {
		t.Fatal("tg.ApiUrl kosong — seluruh pengiriman akan gagal diam-diam")
	}
	t.Logf("✅ bridge tersambung: %s", strings.TrimSuffix(strings.TrimSuffix(tg.ApiUrl, tg.BotToken), "/"))
}
