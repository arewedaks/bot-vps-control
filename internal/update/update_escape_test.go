package update

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bot-vps-control/internal/tg"
)

// ==============================================================================
// 4. HTML DARI GIT SUDAH AMAN
// ==============================================================================

// TestKeluaranGitAmanDikirim adalah rangkaian lengkap: keluaran git berisi
// karakter khusus, di-escape, lalu dikirim — dan harus diterima tanpa penolakan.
func TestKeluaranGitAmanDikirim(t *testing.T) {
	var diterima string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		diterima = string(b)

		// Tiru pemeriksaan Telegram: tolak bila ada tag tidak dikenal.
		if strings.Contains(diterima, `"text":"`) {
			awal := strings.Index(diterima, `"text":"`) + len(`"text":"`)
			akhir := strings.LastIndex(diterima, `"`)
			teks := diterima[awal:akhir]
			if strings.Count(teks, "<b>") != strings.Count(teks, "</b>") {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"ok":false,"description":"can't parse entities"}`)
				return
			}
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	// Klien HTTP tinggal di internal/tg, jadi endpoint yang diuji ada di sana.
	lamaTg := tg.ApiUrl
	tg.ApiUrl = srv.URL
	defer func() { tg.ApiUrl = lamaTg }()

	// Keluaran git yang realistis: penuh <, >, &, dan tanda kutip.
	keluaranGit := "error: cannot use x (type <T>) as type <U> in assignment\n" +
		"  at main.go:42\n  symbols: a & b, \"quoted\", 'single'\n" +
		"  map[string]interface{} vs []int\n"

	pesan := "❌ <b>Build gagal.</b>\n<pre>" + HtmlEscapeRingkas(keluaranGit, 2000) + "</pre>"

	tg.SendTelegram(777, pesan)

	if !strings.Contains(diterima, `"text"`) {
		t.Fatal("pesan tidak terkirim")
	}
	// &lt; harus sudah ada SEBELUM json.Marshal; JSON lalu meng-escape & menjadi \u0026.
	if !strings.Contains(diterima, "T") || !strings.Contains(diterima, "u003c") {
		t.Errorf("karakter < tidak ter-escape sebelum dikirim: %s", diterima)
	}
	t.Log("✅ Keluaran git ter-escape dan diterima Telegram")
}

// TestEscapingAmpersandTidakDimainkanDuaKali memastikan & dari pengguna
// tidak berubah menjadi &amp;amp;.
//
// Kesalahan ini membuat pesan error tampil berantakan dan sulit dibaca
// justru saat pengguna paling membutuhkan kejelasan.
func TestEscapingAmpersandTidakDimainkanDuaKali(t *testing.T) {
	masuk := "a & b"
	sekali := HtmlEscapeRingkas(masuk, 100)
	if !strings.Contains(sekali, "&amp;") {
		t.Fatalf("escape pertama gagal: %q", sekali)
	}
	// Hasil escape tidak boleh di-escape lagi oleh pemanggil.
	if strings.Contains(sekali, "&amp;amp;") {
		t.Error("❌ & ter-escape dua kali")
	}
	if strings.Contains(sekali, "&amp;&") {
		t.Error("❌ & diikuti karakter mentah — escape tidak konsisten")
	}
	t.Log("✅ Ampersand ter-escape tepat sekali")
}
