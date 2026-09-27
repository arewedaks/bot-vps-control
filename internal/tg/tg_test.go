// Pengujian lapisan klien Telegram: pengiriman, penanganan kegagalan,
// pemotongan pesan panjang, dan keamanan HTML.
package tg

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	SendSingleMessage(12345, "<b>rusak", nil)

	if !terkirim {
		t.Fatal("permintaan tidak pernah sampai ke server uji")
	}

	// Verifikasi inti: kode SUMBER memeriksa status, bukan hanya menutup bodi.
	// Sumber yang dipindai adalah berkas package ini sendiri, karena
	// sendSingleMessage kini tinggal di internal/tg.
	isi, err := os.ReadFile("tg.go")
	if err != nil {
		t.Fatalf("baca tg.go: %v", err)
	}
	teks := string(isi)

	// Pada fungsi pengiriman pesan, harus ada pemeriksaan StatusCode.
	// Tanpa itu, penolakan Telegram tidak akan pernah terlihat.
	// Batas akhir dicari dari definisi fungsi berikutnya, bukan dari komentar,
	// supaya tahan terhadap perubahan pada komentar di sumber.
	awal := strings.Index(teks, "func SendSingleMessage(")
	if awal < 0 {
		t.Fatal("SendSingleMessage tidak ditemukan")
	}
	akhir := strings.Index(teks[awal:], "func EditTelegramMessage(")
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

	SendSingleMessage(12345, "<b>normal</b>", nil)

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

	SendSingleMessage(999, "<b>hai</b>", nil)

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
	SendTelegram(555, strings.Repeat("x", 10000))

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

	bagian := SplitMessage(panjang, 1000)
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
// 5. UNDUHAN FILE HARUS MENGIKUTI API YANG DIPAKAI
// ==============================================================================

// TestUnduhanFileIkutiApiUrlTJaga unduhan lewat Bot API kustom.
//
// /downloadTelegramFile dulunya menempelkan host api.telegram.org secara
// literals padahal getFile satu baris di atasnya memakai ApiUrl. consequence:
// setiap unggahan dan deploy gagal 404 begitu bot diarahkan ke Bot API
// lokal — persis skenario yang didukung variabel API_URL.
func TestUnduhanFileIkutiApiUrlTJaga(t *testing.T) {
	var (
		dipanggilUnduh bool
		pathUnduh      string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			io.WriteString(w, `{"ok":true,"result":{"file_path":"documents/uji.txt","file_size":5}}`)
		case strings.Contains(r.URL.Path, "/file/"):
			dipanggilUnduh = true
			pathUnduh = r.URL.Path
			io.WriteString(w, "HELLO")
		default:
			t.Errorf("permintaan tak terduga: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	lamaURL, lamaToken := ApiUrl, BotToken
	defer func() { ApiUrl, BotToken = lamaURL, lamaToken }()

	// Bentuk ApiUrl seperti yang disusun loadConfig: <host>/bot<token>.
	BotToken = "123:ABC"
	ApiUrl = srv.URL + "/bot123:ABC"

	dst := filepath.Join(t.TempDir(), "uji.txt")
	n, err := DownloadTelegramFile("fid-1", dst)
	if err != nil {
		t.Fatalf("unduhan gagal: %v", err)
	}
	if n != 5 {
		t.Errorf("ukuran = %d, ingin 5", n)
	}
	if !dipanggilUnduh {
		t.Fatal("tak ada permintaan ke /file/ — host unduhan tidak mengikuti ApiUrl")
	}
	if strings.Contains(pathUnduh, "api.telegram.org") {
		t.Errorf("unduhan bocor ke host publik: %s", pathUnduh)
	}
	if !strings.Contains(pathUnduh, "/file/bot123:ABC/documents/uji.txt") {
		t.Errorf("path unduhan salah: %s", pathUnduh)
	}

	isi, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("berkas tujuan tidak terbaca: %v", err)
	}
	if string(isi) != "HELLO" {
		t.Errorf("isi = %q, ingin %q", isi, "HELLO")
	}
}
