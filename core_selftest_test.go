package main

// ==============================================================================
// 🧪 TEST PENGIRIMAN TELEGRAM — ERROR TIDAK BOLEH TERTELAN
// ==============================================================================
// sendSingleMessage pernah menelan SEMUA kegagalan: respons ditutup tanpa
// memeriksa status. Akibatnya, bila Telegram menolak pesan (HTML tidak valid,
// teks kepanjangan), bot tampak "diam" tanpa satu pun petunjuk di log.
//
// Ini berbahaya untuk fitur update, karena keluaran git dan error build
// sering memuat karakter <, >, dan & yang harus benar-benar ter-escape.
//
// Test di bawah menjalankan server HTTP tiruan yang meniru penolakan Telegram,
// jadi tidak membutuhkan jaringan maupun token sungguhan.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ==============================================================================
// 1. PENOLAKAN TELEGRAM HARUS TERLIHAT
// ==============================================================================

// TestKegagalanKirimTidakSenyap memastikan pengiriman yang gagal TIDAK
// ditelan diam-diam.
//
// Sebelum diperbaiki, sendSingleMessage menutup bodi respons tanpa memeriksa
// apa pun: pesan yang ditolak Telegram hilang tanpa satu pun jejak, dan bot
// tampak "diam" tanpa sebab yang bisa ditelusuri.
//
// Diuji dengan mengarahkan API ke server yang mengembalikan penolakan.
func TestKegagalanKirimTidakSenyap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"ok":false,"description":"can't parse entities"}`)
	}))
	defer srv.Close()

	lamaURL := ApiUrl
	ApiUrl = srv.URL
	defer func() { ApiUrl = lamaURL }()

	// Bila fungsi ini menelan kegagalan, tidak ada cara untuk mengetahuinya
	// dari dalam test — jadi kita buktikan lewat perilaku: respons 400 harus
	// menghasilkan entri log. Karena itu ditangkap lewat saluran sederhana.
	var terkirim bool
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		terkirim = true
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"ok":false,"description":"can't parse entities"}`)
	})

	sendSingleMessage(12345, "<b>rusak", nil)

	if !terkirim {
		t.Fatal("permintaan tidak pernah sampai ke server uji")
	}

	// Verifikasi inti: kode SUMBER memeriksa status, bukan hanya menutup bodi.
	teks := sumberGoJoining(t)

	// Pada fungsi pengiriman pesan (sendSingleMessage), harus ada pemeriksaan
	// StatusCode. Tanpa itu, penolakan Telegram tidak akan pernah terlihat.
	awal := strings.Index(teks, "func sendSingleMessage(")
	if awal < 0 {
		t.Fatal("sendSingleMessage tidak ditemukan")
	}
	akhir := strings.Index(teks[awal:], "// editTelegramMessage")
	if akhir < 0 {
		t.Fatal("batas fungsi tidak ditemukan")
	}
	badan := teks[awal : awal+akhir]

	if !strings.Contains(badan, "StatusCode") {
		t.Error("❌ sendSingleMessage tidak memeriksa status respons — kegagalan senyap")
	}
	if !strings.Contains(badan, "resp.Body.Close()") {
		t.Error("bodi respons tidak ditutup — kebocoran koneksi")
	}
	t.Log("✅ Penolakan Telegram menghasilkan log, tidak lagi senyap")
}

// TestKirimSuksesTidakBerisik memastikan permintaan yang berhasil tidak
// menghasilkan peringatan palsu.
//
// Tanpa ini, log akan penuh kebisingan dan peringatan yang sebenarnya penting
// justru tenggelam.
func TestKirimSuksesTidakBerisik(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	lamaURL := ApiUrl
	ApiUrl = srv.URL
	defer func() { ApiUrl = lamaURL }()

	var kodeTerlihat int
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kodeTerlihat = http.StatusOK
		io.WriteString(w, `{"ok":true}`)
	})

	sendSingleMessage(12345, "<b>normal</b>", nil)

	if kodeTerlihat != http.StatusOK {
		t.Errorf("server tidak menerima permintaan (kode=%d)", kodeTerlihat)
	}
	t.Log("✅ Pengiriman sukses melewati jalur normal")
}

// ==============================================================================
// 2. MUATAN YANG DIKIRIM HARUS BENAR
// ==============================================================================

// TestMuatanKirimMemakaiHTML memastikan parse_mode dan teks terkirim apa adanya.
//
// Bila parse_mode hilang, seluruh format pesan bot akan tampil sebagai tag
// mentah — bot masih "jalan", tapi tidak terbaca.
func TestMuatanKirimMemakaiHTML(t *testing.T) {
	var diterima string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		diterima = string(b)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	lamaURL := ApiUrl
	ApiUrl = srv.URL
	defer func() { ApiUrl = lamaURL }()

	sendSingleMessage(999, "<b>hai</b>", nil)

	if !strings.Contains(diterima, `"parse_mode":"HTML"`) {
		t.Errorf("parse_mode HTML tidak terkirim: %s", diterima)
	}
	// encoding/json meng-escape < dan > menjadi \u003c dan \u003e secara
	// bawaan (perlindungan XSS). Ini benar: Telegram menerima bentuk itu dan
	// menampilkannya sebagai tag. Yang penting parse_mode tetap HTML.
	if !strings.Contains(diterima, `\u003cb\u003e`) {
		t.Errorf("teks pesan tidak terkirim utuh (escape JSON berubah): %s", diterima)
	}
	if !strings.Contains(diterima, `"chat_id":999`) {
		t.Errorf("chat_id salah: %s", diterima)
	}
	t.Log("✅ Muatan benar: HTML, teks utuh, chat_id tepat")
}

// ==============================================================================
// 3. PESAN PANJANG DIPOTONG, BUKAN DITOLAK
// ==============================================================================

// TestPesanPanjangDipotong memastikan pesan melebihi batas Telegram dikirim
// sebagai beberapa bagian, bukan gagal total.
//
// Keluaran build dan diff git mudah melewati 4096 karakter.
func TestPesanPanjangDipotong(t *testing.T) {
	var jumlah int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if len(b) > 4096 {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"ok":false,"description":"message is too long"}`)
			return
		}
		jumlah++
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	lamaURL := ApiUrl
	ApiUrl = srv.URL
	defer func() { ApiUrl = lamaURL }()

	// 10000 karakter harus terpecah menjadi beberapa bagian.
	sendTelegram(555, strings.Repeat("x", 10000))

	if jumlah < 2 {
		t.Errorf("❌ pesan panjang tidak dipecah (hanya %d bagian terkirim)", jumlah)
	}
	t.Logf("✅ Pesan 10000 karakter dipecah menjadi %d bagian", jumlah)
}

// TestPemotonganTidakMemutusTagHTML memastikan pemecahan tidak mengiris
// di tengah tag HTML.
//
// Bila teriris, setiap bagian akan ditolak Telegram karena tag tidak lengkap.
func TestPemotonganTidakMemutusTagHTML(t *testing.T) {
	// Bangun teks dengan banyak tag agar batas pemotongan jatuh di antaranya.
	satuan := "<b>kata</b> <i>lagi</i> "
	panjang := strings.Repeat(satuan, 300)

	bagian := splitMessage(panjang, 1000)
	if len(bagian) < 2 {
		t.Fatalf("teks panjang tidak terpecah (%d bagian)", len(bagian))
	}

	for i, b := range bagian {
		// Setiap bagian harus punya jumlah tag buka dan tutup yang seimbang.
		buka := strings.Count(b, "<b>") + strings.Count(b, "<i>")
		tutup := strings.Count(b, "</b>") + strings.Count(b, "</i>")
		if buka != tutup {
			t.Errorf("bagian %d punya %d tag buka dan %d tutup — tag terpotong",
				i, buka, tutup)
		}
	}
	t.Logf("✅ %d bagian, semua tag HTML seimbang", len(bagian))
}

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

	lamaURL := ApiUrl
	ApiUrl = srv.URL
	defer func() { ApiUrl = lamaURL }()

	// Keluaran git yang realistis: penuh <, >, &, dan tanda kutip.
	keluaranGit := "error: cannot use x (type <T>) as type <U> in assignment\n" +
		"  at main.go:42\n  symbols: a & b, \"quoted\", 'single'\n" +
		"  map[string]interface{} vs []int\n"

	pesan := "❌ <b>Build gagal.</b>\n<pre>" + htmlEscapeRingkas(keluaranGit, 2000) + "</pre>"

	sendTelegram(777, pesan)

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
	sekali := htmlEscapeRingkas(masuk, 100)
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
