package main

import (
	"strings"
	"testing"
)

// ==============================================================================
// 🔔 TEST POPUP CALLBACK (JANGAN GANGGU PENGGUNA)
// ==============================================================================
// Popup modal memaksa pengguna mengklik OK. Hanya pesan yang benar-benar perlu
// dibaca (kegagalan, penolakan, peringatan) yang boleh tampil sebagai popup.

// TestPopupHanyaUntukPesanPenting memverifikasi penyaringan popup.
func TestPopupHanyaUntukPesanPenting(t *testing.T) {
	// Harus muncul sebagai popup (pengguna perlu tahu).
	perluPopup := []string{
		"⛔ Akses Ditolak.",
		"❌ Gagal menghapus: permission denied",
		"⚠️ Tidak ada sesi aktif",
		"Gagal mengirim sinyal",
		"Tidak bisa membuka PTY",
	}
	for _, s := range perluPopup {
		if !needsUserAttention(s) {
			t.Errorf("%q seharusnya BUTUH popup", s)
		}
	}

	// TIDAK boleh muncul sebagai popup (indikator status biasa).
	tidakPerlu := []string{
		"",
		"⏳",
		"🔄",
		"📋 Status Sesi",
		"🛑 Ctrl+C terkirim",
		"💀 Sesi ditutup",
		"✅ Berhasil dihapus!",
		"File Manager ditutup.",
	}
	for _, s := range tidakPerlu {
		if needsUserAttention(s) {
			t.Errorf("%q TIDAK seharusnya jadi popup — mengganggu", s)
		}
	}
	t.Log("✅ Popup hanya untuk pesan yang perlu perhatian")
}

// TestEmojiLoadingTidakJadiPopup memastikan indikator loading tidak mengganggu.
func TestEmojiLoadingTidakJadiPopup(t *testing.T) {
	loading := []string{"⏳", "🔄", "📦", "⏳ Mengunduh file..."}
	for _, s := range loading {
		if needsUserAttention(s) {
			t.Errorf("indikator loading %q tidak boleh jadi popup", s)
		}
	}
	t.Log("✅ Indikator loading tidak memunculkan popup")
}

// TestAnswerCallbackKosong tidak boleh panic.
func TestAnswerCallbackKosong(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("answerCallbackQuery panic: %v", r)
		}
	}()
	// Tanpa ApiUrl valid, panggilan ini gagal diam-diam — yang penting tidak panic.
	answerCallbackQuery("", "")
	answerCallbackQuery("dummy-id", "")
	answerCallbackQuery("dummy-id", "⏳")
}

// TestNeedsUserAttentionKataKunci memastikan kata kunci penting terdeteksi.
func TestNeedsUserAttentionKataKunci(t *testing.T) {
	khasus := map[string]bool{
		"⛔ Akses Ditolak":        true,
		"❌ Gagal":                true,
		"⚠️ peringatan":          true,
		"Tidak ada sesi aktif":   true,
		"Tidak bisa membuka":     true,
		"error saat menghubungi": true,
		"harus dikonfirmasi":     true,
		"Butuh hak akses":        true,
		"✅ Berhasil":             false,
		"💀 Sesi ditutup":         false,
		"📋 Info":                 false,
		"🔄":                      false,
	}
	for s, mau := range khasus {
		if got := needsUserAttention(s); got != mau {
			t.Errorf("needsUserAttention(%q) = %v, mau %v", s, got, mau)
		}
	}
	t.Logf("✅ %d kasus kata kunci benar", len(khasus))
}

// TestTidakAdaPopupLoadingTersisa memindai FILE SUMBER untuk memastikan tidak
// ada kode yang masih mengirim indikator loading sebagai popup.
// Ini mencegah pola lama kembali saat ada perubahan kode di masa depan.
func TestTidakAdaPopupLoadingTersisa(t *testing.T) {
	polaTerlarang := []string{
		`answerCallbackQuery(cb.ID, "⏳")`,
		`answerCallbackQuery(cb.ID, "🔄")`,
		`answerCallbackQuery(cb.ID, "📦 Sedang mengompresi folder...")`,
		`answerCallbackQuery(cb.ID, "⏳ Mengunduh file...")`,
	}

	// Seluruh kode dipindai, bukan daftar file tertentu, supaya file baru
	// ikut diperiksa tanpa perlu menambahkan nama-nya di sini.
	teks := sumberGoJoining(t)
	dilanggar := 0
	for _, pola := range polaTerlarang {
		if strings.Contains(teks, pola) {
			t.Errorf("kode masih memuat popup loading: %s", pola)
			dilanggar++
		}
	}

	if dilanggar == 0 {
		t.Logf("✅ seluruh kode dipindai, tidak ada popup loading tersisa")
	}
}
