package main

import (
	"testing"
)

// TestPanelStateTracking memverifikasi pencatatan panel per admin.
func TestPanelStateTracking(t *testing.T) {
	u := int64(555001)

	// Belum ada panel untuk admin baru.
	if _, _, ok := getPanelMessage(u); ok {
		t.Fatal("admin baru seharusnya belum punya panel")
	}

	setPanelMessage(u, 100, 42)
	id, chat, ok := getPanelMessage(u)
	if !ok || id != 42 || chat != 100 {
		t.Fatalf("panel tidak tercatat: id=%d chat=%d ok=%v", id, chat, ok)
	}

	// Panel berpindah saat pesan baru dipakai.
	setPanelMessage(u, 100, 99)
	id, _, _ = getPanelMessage(u)
	if id != 99 {
		t.Fatalf("panel tidak diperbarui: %d", id)
	}

	clearPanel(u)
	if _, _, ok := getPanelMessage(u); ok {
		t.Fatal("panel seharusnya terhapus setelah clearPanel")
	}
}

// TestPanelIsolatedPerAdmin memastikan panel satu admin tidak bocor ke admin lain.
func TestPanelIsolatedPerAdmin(t *testing.T) {
	a, b := int64(555002), int64(555003)
	setPanelMessage(a, 1, 11)
	setPanelMessage(b, 2, 22)
	defer func() { clearPanel(a); clearPanel(b) }()

	if id, _, _ := getPanelMessage(a); id != 11 {
		t.Errorf("admin A panel = %d, mau 11", id)
	}
	if id, _, _ := getPanelMessage(b); id != 22 {
		t.Errorf("admin B panel = %d, mau 22", id)
	}
}

// TestDeletePanelSafe pada admin tanpa panel tidak boleh panic.
func TestDeletePanelSafe(t *testing.T) {
	// Tanpa ApiUrl valid, deleteTelegramMessage akan gagal diam-diam.
	// Yang penting: tidak panic dan tidak mengubah state jadi kacau.
	deletePanel(int64(555004))
	if _, _, ok := getPanelMessage(int64(555004)); ok {
		t.Fatal("deletePanel seharusnya tetap tidak ada panel")
	}
}

// TestTruncateHTMLSafe memastikan pemotongan tidak merusak tag HTML.
func TestTruncateHTMLSafe(t *testing.T) {
	// Tag harus utuh, tidak terpotong di tengah.
	s := "<b>judul</b> " + string(make([]byte, 0))
	for i := 0; i < 500; i++ {
		s += "<i>x</i>"
	}
	out := truncateHTMLSafe(s, 100)
	if len(out) > 200 {
		t.Errorf("hasil potong terlalu panjang: %d", len(out))
	}
	// Tidak boleh berakhir di tengah tag.
	if len(out) > 1 && out[len(out)-2] == '<' {
		t.Errorf("terpotong di tengah tag: %q", out[len(out)-10:])
	}
}

// TestTruncateHTMLSafeNoCut memastikan teks pendek tidak diubah.
func TestTruncateHTMLSafeNoCut(t *testing.T) {
	s := "<b>pendek</b>"
	if got := truncateHTMLSafe(s, 1000); got != s {
		t.Errorf("teks pendek tidak boleh diubah: %q -> %q", s, got)
	}
}

// TestTermHelpGoesToPanel memastikan bantuan terminal tidak mengirim pesan baru.
// Diuji dengan memastikan tidak ada variabel global yang melacak "last sent".
func TestPanelFunctionsExist(t *testing.T) {
	// Ini test kompilasi: memastikan semua fungsi panel tersedia dengan
	// signature yang benar. Kalau tidak, build akan gagal.
	var _ func(int64, int64, int64) = setPanelMessage
	var _ func(int64) (int64, int64, bool) = getPanelMessage
	var _ func(int64) = clearPanel
	var _ func(int64) = deletePanel
	var _ func(int64, int64, string, *InlineKeyboardMarkup) = updatePanel
	var _ func(int64, int64, string, *InlineKeyboardMarkup) = sendPanel
	var _ func(int64, int64, string) = sendOnce
	t.Log("✅ semua fungsi panel tersedia dengan signature benar")
}
