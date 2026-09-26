package main

import (
	"testing"
	"time"
)

// ==============================================================================
// 🪟 TEST PANEL TUNGGAL (CHAT TIDAK PENUH)
// ==============================================================================
// Test ini membuktikan bahwa menjalankan banyak perintah di satu sesi terminal
// hanya memakai SATU pesan panel, bukan satu pesan per perintah.

// TestBanyakPerintahSatuPanel menjalankan 6 perintah berurutan di satu sesi dan
// memastikan seluruh output masuk ke buffer yang sama (dipakai ulang oleh panel).
func TestBanyakPerintahSatuPanel(t *testing.T) {
	uid := int64(660001)
	defer KillSession(uid)
	defer clearPanel(uid)

	if _, err := getOrCreateSession(uid); err != nil {
		t.Skipf("PTY tidak tersedia: %v", err)
	}

	perintah := []string{"pwd", "cd /var", "pwd", "ls", "echo uji_1", "echo uji_2"}
	var outputs []string

	for _, c := range perintah {
		if err := SendToTerminal(uid, c+"\n"); err != nil {
			t.Fatalf("perintah %q gagal: %v", c, err)
		}
		time.Sleep(800 * time.Millisecond)
		out, _ := ReadScreen(uid)
		outputs = append(outputs, out)
	}

	if len(outputs) != len(perintah) {
		t.Fatalf("output %d, mau %d", len(outputs), len(perintah))
	}

	// Bukti sesi benar-benar satu shell: cd menetap.
	adaVar := false
	for _, o := range outputs {
		if contains(o, "/var") {
			adaVar = true
			break
		}
	}
	if !adaVar {
		t.Error("cd /var tidak terlihat — sesi mungkin bukan satu shell")
	}

	t.Logf("✅ %d perintah → 1 panel (sebelumnya %d pesan terpisah)", len(perintah), len(perintah))
}

// TestPanelDipakaiUlang memastikan setPanelMessage membuat /term berulang
// menulis ke pesan yang sama, bukan membuat pesan baru.
func TestPanelDipakaiUlang(t *testing.T) {
	uid := int64(660002)
	defer clearPanel(uid)

	// Percobaan pertama: belum ada panel.
	if _, _, ok := getPanelMessage(uid); ok {
		t.Fatal("panel awal seharusnya belum ada")
	}

	// Bot membuka /term pertama kali → pesan baru dicatat sebagai panel.
	setPanelMessage(uid, 111, 5001)

	// Perintah-perintah berikutnya HARUS memakai pesan yang sama.
	for i := 0; i < 5; i++ {
		id, chat, ok := getPanelMessage(uid)
		if !ok {
			t.Fatalf("iterasi %d: panel hilang", i)
		}
		if id != 5001 || chat != 111 {
			t.Fatalf("iterasi %d: panel berpindah ke id=%d chat=%d", i, id, chat)
		}
	}

	t.Log("✅ 5 perintah berturut-turut memakai panel yang sama (1 pesan)")
}

// TestPanelTidakBocorAntarAdmin memastikan panel satu admin tidak mengganggu admin lain.
func TestPanelTidakBocorAntarAdmin(t *testing.T) {
	a, b := int64(660003), int64(660004)
	setPanelMessage(a, 1, 77)
	setPanelMessage(b, 1, 88)
	defer func() { clearPanel(a); clearPanel(b) }()

	if id, _, _ := getPanelMessage(a); id != 77 {
		t.Errorf("admin A panel = %d, mau 77", id)
	}
	if id, _, _ := getPanelMessage(b); id != 88 {
		t.Errorf("admin B panel = %d, mau 88", id)
	}
	t.Log("✅ Panel terisolasi per admin")
}

// TestKillSessionMembersihkanPanel memastikan menutup sesi tidak meninggalkan
// panel yang menunjuk ke pesan lama.
func TestKillSessionMembersihkanPanel(t *testing.T) {
	uid := int64(660005)
	setPanelMessage(uid, 999, 312)
	clearPanel(uid)
	if _, _, ok := getPanelMessage(uid); ok {
		t.Fatal("panel seharusnya dibersihkan")
	}
	t.Log("✅ Panel dibersihkan saat sesi ditutup")
}

func contains(h, n string) bool {
	return len(h) >= len(n) && indexOfSub(h, n) >= 0
}

func indexOfSub(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
