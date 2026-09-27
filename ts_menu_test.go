package main

import (
	"strings"
	"testing"
)

// ==============================================================================
// 🦎 TEST MENU TAILSCALE
// ==============================================================================

// TestTsCountPeers memverifikasi penghitungan peer dari format keluaran nyata.
// Kolom ke-5 bernilai "-" untuk peer online.
//
// Data di bawah adalah contoh sintetis — bukan perangkat sungguhan — supaya
// repository publik tidak membocorkan topologi Tailscale milik siapa pun.
func TestTsCountPeers(t *testing.T) {
	out := `100.64.0.11      vps-utama         user@  linux    -
100.64.0.12      nas-rumah         user@  linux    offline, last seen 12d ago
100.64.0.13      router-lantai2    user@  linux    -
100.64.0.14      laptop-kerja      user@  windows  offline, last seen 3d ago
100.64.0.15      server-tidur      user@  linux    offline, last seen 1d ago
100.64.0.16      mini-pc           user@  linux    -
100.64.0.17      printer-jaringan  user@  linux    -`

	online, offline, _ := tsCountPeers(out)
	if online != 4 {
		t.Errorf("online = %d, mau 4", online)
	}
	if offline != 3 {
		t.Errorf("offline = %d, mau 3", offline)
	}
	t.Logf("✅ online=%d offline=%d", online, offline)
}

// TestTsCountPeersKosong memastikan output kosong tidak panic.
// Catatan: self diisi dari Tailscale mesin ini (bila ada), bukan dari teks kosong.
func TestTsCountPeersKosong(t *testing.T) {
	o, f, _ := tsCountPeers("")
	if o != 0 || f != 0 {
		t.Errorf("output kosong harus 0/0, dapat %d/%d", o, f)
	}
}

// TestTsCountPeersAdaHeader memastikan baris header/komentar diabaikan.
func TestTsCountPeersAdaHeader(t *testing.T) {
	out := "# HEALTH: check ok\n" +
		"100.64.0.20  host-saya  user@  linux  -\n" +
		"100.1.2.3     lain       user@  linux  offline, last seen 1h ago"

	online, offline, _ := tsCountPeers(out)
	if online != 1 {
		t.Errorf("online = %d, mau 1", online)
	}
	if offline != 1 {
		t.Errorf("offline = %d, mau 1", offline)
	}
}

// TestTsCountPeersAbaikanNonTailscale memastikan hanya baris peer Tailscale
// yang dihitung, bukan IP jaringan lain.
func TestTsCountPeersAbaikanNonTailscale(t *testing.T) {
	out := "100.1.1.1  node-a  u@  linux  -\n" +
		"192.168.1.5  bukan-tailscale  u@  linux  -\n" +
		"10.0.0.1  juga-bukan  u@  linux  -\n" +
		"fd7a:115c:a1e0::1  node-ipv6  u@  linux  -"

	online, offline, _ := tsCountPeers(out)
	if online != 2 {
		t.Errorf("online = %d (hanya 100.x dan fd7a: yang dihitung), mau 2", online)
	}
	if offline != 0 {
		t.Errorf("offline = %d, mau 0", offline)
	}
}

// TestTsSelfHostnameTidakPanic memastikan pembacaan hostname aman.
func TestTsSelfHostnameTidakPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("tsSelfHostname panic: %v", r)
		}
	}()
	t.Logf("tsSelfHostname() = %q", tsSelfHostname())
}

// TestTsExtractLoginURL memastikan link login ditemukan dari output tailscale.
func TestTsExtractLoginURL(t *testing.T) {
	out := "To authenticate, visit:\n\n\thttps://login.tailscale.com/a/abcdef123456\n"
	got := tsExtractLoginURL(out)
	if got != "https://login.tailscale.com/a/abcdef123456" {
		t.Errorf("URL = %q", got)
	}

	// Tidak boleh salah ambil link lain.
	if tsExtractLoginURL("see https://tailscale.com/download for info") != "" {
		t.Error("tidak boleh mengambil link non-login")
	}
}

// TestTsMenuKeyboardValid memastikan tombol menu punya callback_data yang sah.
func TestTsMenuKeyboardValid(t *testing.T) {
	kb := tsMenuKeyboard()
	if kb == nil || len(kb.InlineKeyboard) == 0 {
		t.Fatal("tsMenuKeyboard kosong")
	}

	n := 0
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			n++
			if b.Text == "" {
				t.Error("tombol tanpa label")
			}
			// Sebagian besar tombol memakai prefix ts:, tapi tombol kembali
			// ke bantuan memakai hp: (ditangani blok terpisah di main.go).
			valid := strings.HasPrefix(b.CallbackData, "ts:") ||
				strings.HasPrefix(b.CallbackData, "hp:")
			if !valid {
				t.Errorf("callback_data tidak valid: %q", b.CallbackData)
			}
			// Telegram membatasi callback_data 64 byte.
			if len(b.CallbackData) > 64 {
				t.Errorf("callback_data terlalu panjang: %q", b.CallbackData)
			}
		}
	}
	t.Logf("✅ %d tombol, semua callback_data valid", n)
}

// TestTsStatusRingkasTagSeimbang memastikan hasil ringkasan bisa diterima Telegram.
func TestTsStatusRingkasTagSeimbang(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("tsStatusRingkas panic: %v", r)
		}
	}()

	out := tsStatusRingkas()
	if out == "" {
		t.Fatal("tsStatusRingkas menghasilkan string kosong")
	}
	for _, tag := range []string{"b", "i", "code", "pre"} {
		o := strings.Count(out, "<"+tag+">")
		c := strings.Count(out, "</"+tag+">")
		if o != c {
			t.Errorf("tag <%s> tidak seimbang: %d buka, %d tutup", tag, o, c)
		}
	}
	t.Logf("✅ %d karakter, tag HTML seimbang", len(out))
}

// TestTsCallbackSemuaAksi memastikan setiap aksi callback menghasilkan teks
// dan tidak panic. Aksi destruktif tidak dijalankan isinya.
func TestTsCallbackSemuaAksi(t *testing.T) {
	uid, chat := int64(770001), int64(770001)
	aksi := []string{"r", "", "s", "p", "k", "d", "hon", "hoff"}

	for _, a := range aksi {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("aksi %q panic: %v", a, r)
				}
			}()
			text, kb := handleTailscaleCallback(uid, chat, a)
			if text == "" {
				t.Errorf("aksi %q menghasilkan teks kosong", a)
			}
			if kb == nil {
				t.Errorf("aksi %q tidak mengembalikan keyboard", a)
			}
		}()
	}
	t.Logf("✅ %d aksi callback aman dijalankan", len(aksi))
}

// TestTsDownButuhKonfirmasi memastikan tombol disconnect meminta konfirmasi.
// Ini celah keamanan: memutus Tailscale bisa menghilangkan akses remote.
func TestTsDownButuhKonfirmasi(t *testing.T) {
	text, kb := handleTailscaleCallback(770002, 770002, "d")

	if !strings.Contains(text, "Putuskan") {
		t.Error("pesan harus menanyakan pemutusan")
	}
	if !strings.Contains(text, "kehilangan akses") {
		t.Error("peringatan kehilangan akses harus ada")
	}

	adaYa, adaBatal := false, false
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			if b.CallbackData == "ts:dy" {
				adaYa = true
			}
			if b.CallbackData == "ts:r" {
				adaBatal = true
			}
		}
	}
	if !adaYa {
		t.Error("tombol konfirmasi 'Ya' tidak ada")
	}
	if !adaBatal {
		t.Error("tombol 'Batal' tidak ada")
	}
	t.Log("✅ Disconnect butuh konfirmasi + ada tombol batal")
}

// TestTsAuthKeyTidakDisarankanDiChat memastikan panduan auth key mengarahkan
// ke jalur yang tidak mengekspos rahasia ke riwayat chat.
func TestTsAuthKeyTidakDisarankanDiChat(t *testing.T) {
	text, _ := handleTailscaleCallback(770003, 770003, "k")

	if !strings.Contains(text, "rahasia") {
		t.Error("harus menjelaskan bahwa auth key adalah rahasia")
	}
	if !strings.Contains(text, "/term") {
		t.Error("harus mengarahkan ke terminal sebagai jalur aman")
	}
	t.Log("✅ Panduan auth key mengarahkan ke jalur aman")
}

// TestTsInstallActionTidakPanic memastikan aksi install tidak crash.
// Tidak dijalankan sungguhan karena butuh jaringan.
func TestTsInstallActionTidakPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("aksi install panic: %v", r)
		}
	}()
	text, kb := handleTailscaleCallback(770004, 770004, "i")
	if text == "" {
		t.Error("aksi install menghasilkan teks kosong")
	}
	if kb == nil {
		t.Error("aksi install harus mengembalikan keyboard")
	}
}

// TestTsActionTidakDikenalFallback memastikan aksi asing jatuh ke menu utama.
func TestTsActionTidakDikenalFallback(t *testing.T) {
	text, kb := handleTailscaleCallback(770005, 770005, "aksi_ngawur")
	if text == "" {
		t.Error("aksi tidak dikenal harus tetap menghasilkan teks")
	}
	if kb == nil {
		t.Error("aksi tidak dikenal harus mengembalikan keyboard")
	}
}

// TestSetiapTombolHelpPunyaHandler memastikan setiap callback_data di keyboard
// bantuan benar-benar ditangani. Ini mencegah tombol mati (dead button).
func TestSetiapTombolHelpPunyaHandler(t *testing.T) {
	// Aksi yang ditangani di switch "hp:" pada main.go.
	ditangani := map[string]bool{
		"hp:t": true, // buka terminal
		"hp:f": true, // buka file manager
		"hp:s": true, // stats
		"hp:y": true, // sysinfo
		"hp:x": true, // tailscale
		"hp:u": true, // panduan upload besar
		"hp:d": true, // panel deploy bot
		"hp:a": true, // semua perintah
		"hp:b": true, // kembali ke bantuan
	}

	kb := helpKeyboard()
	hitung := 0
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			hitung++
			if !ditangani[b.CallbackData] {
				t.Errorf("tombol %q (%s) TIDAK punya handler — akan jadi tombol mati",
					b.Text, b.CallbackData)
			}
		}
	}
	t.Logf("✅ %d tombol bantuan, semua punya handler", hitung)
}

// TestSetiapTombolTsPunyaHandler memastikan tombol menu Tailscale ditangani.
func TestSetiapTombolTsPunyaHandler(t *testing.T) {
	ditangani := map[string]bool{
		"ts:i":    true, // install
		"ts:u":    true, // connect
		"ts:s":    true, // status detail
		"ts:r":    true, // refresh
		"ts:p":    true, // ip
		"ts:k":    true, // auth key
		"ts:d":    true, // disconnect (konfirmasi)
		"ts:dy":   true, // disconnect (ya)
		"ts:hon":  true, // ssh on
		"ts:hoff": true, // ssh off
		// Tombol kembali ke bantuan utama — ditangani blok hp:, bukan ts:.
		"hp:b": true, // kembali ke menu utama
	}

	kb := tsMenuKeyboard()
	hitung := 0
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			hitung++
			if !ditangani[b.CallbackData] {
				t.Errorf("tombol %q (%s) TIDAK punya handler", b.Text, b.CallbackData)
			}
		}
	}
	t.Logf("✅ %d tombol Tailscale, semua punya handler", hitung)
}
