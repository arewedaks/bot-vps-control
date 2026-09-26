package main

import (
	"strings"
	"testing"
)

// ==============================================================================
// 🦎 TEST NAVIGASI MENU TAILSCALE
// ==============================================================================

// kumpulkanCallback mengumpulkan semua callback_data dari sebuah keyboard.
func kumpulkanCallback(kb *InlineKeyboardMarkup) []string {
	var out []string
	if kb == nil {
		return out
	}
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			out = append(out, b.CallbackData)
		}
	}
	return out
}

// punyaCallback memeriksa apakah keyboard memuat callback tertentu.
func punyaCallback(kb *InlineKeyboardMarkup, target string) bool {
	for _, c := range kumpulkanCallback(kb) {
		if c == target {
			return true
		}
	}
	return false
}

// TestMenuTailscalePunyaJalanKeluar memastikan menu utama tidak mengurung
// pengguna — harus ada jalan ke bantuan dan ke menu utama.
func TestMenuTailscalePunyaJalanKeluar(t *testing.T) {
	kb := tsMenuKeyboard()
	if kb == nil || len(kb.InlineKeyboard) == 0 {
		t.Fatal("tsMenuKeyboard kosong")
	}

	// Harus bisa keluar dari Tailscale menuju bantuan utama.
	if !punyaCallback(kb, "hp:b") {
		t.Error("menu Tailscale harus punya tombol ⬅️ Kembali ke bantuan")
	}
	t.Log("✅ Menu Tailscale punya jalan keluar ke bantuan")
}

// TestLayarDetailTailscalePunyaTombolKembali memverifikasi setiap layar
// detail punya tombol kembali yang jelas.
func TestLayarDetailTailscalePunyaTombolKembali(t *testing.T) {
	// Layar-layar yang seharusnya punya navigasi kembali.
	layar := []struct{ nama, aksi string }{
		{"Status Detail", "s"},
		{"IP & Hostname", "p"},
		{"Set Auth Key", "k"},
	}

	for _, l := range layar {
		text, kb := handleTailscaleCallback(910001, 910001, l.aksi)
		if text == "" {
			t.Errorf("%s menghasilkan teks kosong", l.nama)
			continue
		}
		if kb == nil {
			t.Errorf("%s tidak mengembalikan keyboard", l.nama)
			continue
		}

		// Harus ada jalan kembali ke menu Tailscale.
		if !punyaCallback(kb, "ts:r") {
			t.Errorf("%s tidak punya tombol kembali ke menu Tailscale", l.nama)
		}
		// Harus ada jalan ke menu utama.
		if !punyaCallback(kb, "hp:b") {
			t.Errorf("%s tidak punya tombol ke menu utama", l.nama)
		}
		t.Logf("✅ %s punya Kembali (ts:r) + Menu Utama (hp:b)", l.nama)
	}
}

// TestKonfirmasiDisconnectPunyaBatal memastikan pengguna bisa membatalkan.
func TestKonfirmasiDisconnectPunyaBatal(t *testing.T) {
	text, kb := handleTailscaleCallback(910002, 910002, "d")

	if !strings.Contains(text, "Putuskan") {
		t.Error("harus menanyakan pemutusan")
	}
	// Tombol Batal harus mengembalikan ke menu, bukan sekadar menghilang.
	if !punyaCallback(kb, "ts:r") {
		t.Error("tombol Batal harus kembali ke menu Tailscale (ts:r)")
	}
	if !punyaCallback(kb, "ts:dy") {
		t.Error("harus ada tombol konfirmasi Ya")
	}
	t.Log("✅ Konfirmasi disconnect punya Ya dan Batal")
}

// TestSemuaAksiTailscalePunyaJalanPulang memastikan TIDAK ADA layar Tailscale
// yang mengurung pengguna.
func TestSemuaAksiTailscalePunyaJalanPulang(t *testing.T) {
	aksi := []string{"", "r", "i", "u", "k", "s", "p", "hon", "hoff", "d"}

	for _, a := range aksi {
		_, kb := handleTailscaleCallback(910003, 910003, a)
		if kb == nil {
			t.Errorf("aksi %q tidak mengembalikan keyboard", a)
			continue
		}

		// Setiap layar harus punya minimal satu jalan pulang:
		//   ts:r    → kembali ke menu Tailscale
		//   hp:b    → ke menu bantuan utama
		//   ts:dy   → aksi destruktif (disconnect) — punya jalur sendiri
		adaPulang := punyaCallback(kb, "ts:r") ||
			punyaCallback(kb, "hp:b") ||
			punyaCallback(kb, "ts:dy")

		if !adaPulang {
			nama := a
			if nama == "" {
				nama = "(menu utama)"
			}
			t.Errorf("aksi %q MENGURUNG pengguna — tidak ada tombol kembali\n"+
				"   callback yang ada: %v", nama, kumpulkanCallback(kb))
		}
	}
	t.Logf("✅ %d aksi Tailscale semua punya jalan pulang", len(aksi))
}

// TestCallbackTailscaleTidakBentrok memastikan prefix ts: dan hp: tidak saling
// mengganggu, sehingga tombol Kembali benar-benar sampai ke handler yang benar.
func TestCallbackTailscaleTidakBentrok(t *testing.T) {
	// Semua callback Tailscale harus berawalan ts: atau hp: (untuk kembali).
	semua := append(kumpulkanCallback(tsMenuKeyboard()), kumpulkanCallback(tsSubKeyboard())...)

	// Tambahkan keyboard konfirmasi disconnect.
	_, kbDiskon := handleTailscaleCallback(910004, 910004, "d")
	semua = append(semua, kumpulkanCallback(kbDiskon)...)

	for _, c := range semua {
		ok := strings.HasPrefix(c, "ts:") || strings.HasPrefix(c, "hp:")
		if !ok {
			t.Errorf("callback %q tidak berawalan ts: atau hp: — akan jadi tombol mati", c)
		}
		if len(c) > 64 {
			t.Errorf("callback %q terlalu panjang untuk Telegram (%d byte)", c, len(c))
		}
	}
	t.Logf("✅ %d callback valid dan tidak bentrok", len(semua))
}

// TestCallbackKembaliTerdaftar memastikan setiap callback yang dihasilkan
// tombol Tailscale benar-benar ditangani oleh suatu handler.
func TestCallbackKembaliTerdaftar(t *testing.T) {
	// Aksi yang ditangani handleTailscaleCallback (prefix ts:).
	ditanganiTs := map[string]bool{
		"ts:r": true, "ts:i": true, "ts:u": true, "ts:k": true, "ts:s": true,
		"ts:p": true, "ts:hon": true, "ts:hoff": true, "ts:d": true, "ts:dy": true,
	}
	// Aksi bantuan (prefix hp:).
	ditanganiHp := map[string]bool{
		"hp:t": true, "hp:f": true, "hp:s": true, "hp:y": true,
		"hp:x": true, "hp:u": true, "hp:a": true, "hp:b": true,
	}

	semua := append(kumpulkanCallback(tsMenuKeyboard()), kumpulkanCallback(tsSubKeyboard())...)
	_, kbDiskon := handleTailscaleCallback(910005, 910005, "d")
	semua = append(semua, kumpulkanCallback(kbDiskon)...)

	for _, c := range semua {
		switch {
		case strings.HasPrefix(c, "ts:"):
			if !ditanganiTs[c] {
				t.Errorf("callback %q tidak punya handler", c)
			}
		case strings.HasPrefix(c, "hp:"):
			if !ditanganiHp[c] {
				t.Errorf("callback bantuan %q tidak punya handler", c)
			}
		}
	}
	t.Logf("✅ Semua %d callback punya handler", len(semua))
}

// TestSubKeyboardPunyaDuaJalur memverifikasi keyboard layar detail.
func TestSubKeyboardPunyaDuaJalur(t *testing.T) {
	kb := tsSubKeyboard()
	if !punyaCallback(kb, "ts:r") {
		t.Error("layar detail harus punya tombol kembali ke menu Tailscale")
	}
	if !punyaCallback(kb, "hp:b") {
		t.Error("layar detail harus punya tombol ke menu utama")
	}

	// Jangan terlalu banyak tombol — layar detail hanya butuh navigasi.
	n := len(kumpulkanCallback(kb))
	if n > 3 {
		t.Errorf("layar detail terlalu banyak tombol (%d), cukup 2", n)
	}
	t.Log("✅ Layar detail punya dua jalur pulang yang jelas")
}
