package main

import (
	"strings"
	"testing"
)

// TestHelpTextRingkas memastikan /help tidak lagi memuat perintah yang dibuang.
func TestHelpTextRingkas(t *testing.T) {
	h := helpText()

	// Harus memuat fitur utama.
	harusAda := []string{
		"/term", "/fm", "/stats", "/top", "/sysinfo", "/net",
		"/ip", "/ts", "/exec", "/ping", "/reboot", "/commands",
	}
	for _, c := range harusAda {
		if !strings.Contains(h, c) {
			t.Errorf("helpText() harus memuat %q", c)
		}
	}

	// Tidak boleh memuat yang sudah dibuang dari bantuan utama.
	// Ini menjaga /help tetap ringkas dan relevan.
	dibuang := []string{"/upload", "/getfile", "/mkdir", "/cat", "/ls", "/ps", "/ports", "/view", "/download", "/cmd"}
	for _, c := range dibuang {
		if strings.Contains(h, "<code>"+c) {
			t.Errorf("helpText() tidak boleh memuat %q di bantuan utama", c)
		}
	}

	// Panjang wajar — ini halaman bantuan, bukan dokumentasi.
	if len(h) > 1800 {
		t.Errorf("helpText() terlalu panjang: %d karakter (maks 1800)", len(h))
	}
	t.Logf("✅ helpText() %d karakter, %d baris", len(h), strings.Count(h, "\n")+1)
}

// TestHelpTextBisaDiparseHTML memastikan semua tag HTML seimbang.
func TestHelpTextBisaDiparseHTML(t *testing.T) {
	for nama, teks := range map[string]string{
		"helpText":          helpText(),
		"commandsPlainText": commandsPlainText(),
		"terminalHelpText":  terminalHelpText(),
	} {
		for _, tag := range []string{"b", "i", "code", "pre"} {
			open := strings.Count(teks, "<"+tag+">")
			close := strings.Count(teks, "</"+tag+">")
			if open != close {
				t.Errorf("%s: tag <%s> tidak seimbang (%d buka, %d tutup). Telegram akan menolak pesan ini.", nama, tag, open, close)
			}
		}
	}
}

// TestCommandsPlainTextLengkap memastikan daftar lengkap tetap punya semuanya.
func TestCommandsPlainTextLengkap(t *testing.T) {
	c := commandsPlainText()
	semua := []string{
		"/term", "/term apt check", "/term apt update", "/term apt upgrade",
		"/term apt safe", "/term apt full", "/term apt list", "/term apt clean",
		"/term log", "/term clear", "/term info", "/term kill", "/term mode",
		"/fm", "/cat", "/mkdir", "/rm", "/getfile",
		"/stats", "/top", "/sysinfo", "/net", "/ip", "/ping",
		"/ts", "/exec", "/reboot",
	}
	for _, s := range semua {
		if !strings.Contains(c, s) {
			t.Errorf("commandsPlainText() harus memuat %q", s)
		}
	}
	// Upload dijelaskan sebagai instruksi, bukan perintah.
	if !strings.Contains(c, "caption") {
		t.Error("commandsPlainText() harus menjelaskan cara upload via caption")
	}
}

// TestHelpMenyebutSemuaPerintahUtama mengikat fitur ke dokumentasi.
//
// Tanpa uji ini, fitur baru mudah terlupa didokumentasikan dan pengguna baru
// tahu fitur itu ada secara kebetulan. Menambahkan perintah baru ke switch di
// main.go harus diikuti pembaruan bantuan agar uji ini tetap lulus.
func TestHelpMenyebutSemuaPerintahUtama(t *testing.T) {
	h := helpText()
	c := commandsPlainText()

	// Perintah yang wajib muncul di helpText (gambaran singkat).
	for _, perintah := range []string{
		"/term", "/fm", "/stats", "/unduh", "/deploy", "/ts",
	} {
		if !strings.Contains(h, perintah) {
			t.Errorf("helpText() tidak menyebut %q", perintah)
		}
	}

	// Perintah yang wajib muncul di commandsPlainText (daftar rinci).
	for _, perintah := range []string{
		"/deploy", "/term", "/fm", "/ts", "/unduh", "/chunk",
	} {
		if !strings.Contains(c, perintah) {
			t.Errorf("commandsPlainText() tidak menyebut %q", perintah)
		}
	}

	// Setiap tombol bantuan harus punya padanan penjelasan di helpText.
	// Dipetakan manual karena label tombol memakai bahasa manusia, bukan
	// perintah, sehingga tidak bisa dicocokkan secara otomatis.
	kb := helpKeyboard()
	for _, baris := range kb.InlineKeyboard {
		for _, b := range baris {
			if b.CallbackData == "hp:a" || b.CallbackData == "hp:b" {
				continue // tombol navigasi, tidak perlu penjelasan tersendiri
			}
			teksTombol := strings.TrimSpace(strings.TrimLeft(b.Text, "🖥️📁📊⚙️🦎📦🚀 "))
			if teksTombol != "" && !strings.Contains(strings.ToLower(h), strings.ToLower(teksTombol)) {
				t.Logf("⚠️ tombol %q belum dijelaskan di helpText()", b.Text)
			}
		}
	}
}

// TestHelpKeyboardValid memastikan tombol bantuan punya callback_data yang sah.
func TestHelpKeyboardValid(t *testing.T) {
	kb := helpKeyboard()
	if kb == nil || len(kb.InlineKeyboard) == 0 {
		t.Fatal("helpKeyboard() kosong")
	}
	n := 0
	for _, baris := range kb.InlineKeyboard {
		for _, b := range baris {
			n++
			if b.Text == "" {
				t.Error("tombol tanpa label")
			}
			if !strings.HasPrefix(b.CallbackData, "hp:") {
				t.Errorf("callback_data %q harus berawalan hp:", b.CallbackData)
			}
			// Telegram membatasi callback_data 64 byte.
			if len(b.CallbackData) > 64 {
				t.Errorf("callback_data terlalu panjang: %q", b.CallbackData)
			}
		}
	}
	t.Logf("✅ helpKeyboard() %d tombol, semua callback_data valid", n)
}

// TestBackKeyboardValid memastikan tombol kembali menunjuk ke handler yang ada.
func TestBackKeyboardValid(t *testing.T) {
	kb := backToHelpKeyboard()
	if kb == nil || len(kb.InlineKeyboard) == 0 {
		t.Fatal("backToHelpKeyboard() kosong")
	}
	got := kb.InlineKeyboard[0][0].CallbackData
	if got != "hp:b" {
		t.Errorf("tombol kembali harus hp:b, dapat %q", got)
	}
}

// TestBuildersTidakPanic memastikan pembangun pesan tidak crash.
func TestBuildersTidakPanic(t *testing.T) {
	for nama, fn := range map[string]func() string{
		"buildStatsMessage":   buildStatsMessage,
		"buildSysInfoMessage": buildSysInfoMessage,
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panic: %v", nama, r)
				}
			}()
			out := fn()
			if out == "" {
				t.Errorf("%s menghasilkan string kosong", nama)
			}
		}()
	}
}
