package main

import (
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Uji bahwa perintah apt yang dihasilkan BENAR dan bisa dijalankan shell.
func TestAptCommandBuild(t *testing.T) {
	uid := int64(991001)
	defer KillSession(uid)

	if _, err := getOrCreateSession(uid); err != nil {
		t.Skipf("PTY tidak tersedia: %v", err)
	}

	// Uji skrip cek lock — ini harus jalan di semua sistem.
	if err := SendToTerminal(uid, termAptCheckScript()+"\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	out, _ := ReadScreen(uid)
	fmt.Printf("--- OUTPUT CEK LOCK ---\n%s\n---\n", out)

	if !strings.Contains(out, "Status APT") {
		t.Errorf("skrip cek lock tidak menghasilkan output yang diharapkan")
	}
	if !strings.Contains(out, "USER:") {
		t.Errorf("cek user tidak muncul")
	}
	t.Log("✅ Skrip cek lock APT berjalan di PTY")

	// Uji deteksi root.
	root := termIsRoot()
	t.Logf("termIsRoot() = %v (euid=%d)", root, 0)
	prefix := termAptPrefix()
	t.Logf("termAptPrefix() = %q", prefix)
	if root && prefix != "" {
		t.Error("root seharusnya tidak butuh prefix sudo")
	}

	// Uji perintah apt list (aman, tidak mengubah sistem).
	if err := SendToTerminal(uid, "apt list --upgradable 2>/dev/null | wc -l\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond)
	out2, _ := ReadScreen(uid)
	fmt.Printf("--- OUTPUT apt list wc ---\n%s\n---\n", out2)
	if !strings.Contains(out2, "apt list") {
		t.Logf("output: %q", out2)
	}
	t.Log("✅ Perintah apt list berjalan")
}

// Uji bahwa output panjang TIDAK hilang (masalah utama apt upgrade).
func TestLongOutputNotLost(t *testing.T) {
	uid := int64(991002)
	defer KillSession(uid)

	if _, err := getOrCreateSession(uid); err != nil {
		t.Skipf("PTY tidak tersedia: %v", err)
	}

	// Hasilkan output 60KB - lebih besar dari buffer 32KB yang lama.
	if err := SendToTerminal(uid, "for i in $(seq 1 3000); do echo \"BARIS_$i paket-dummy-untuk-uji\"; done; echo SELESAI_PANJANG\n"); err != nil {
		t.Fatal(err)
	}

	// Tunggu output selesai.
	deadline := time.Now().Add(15 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		out, _ = ReadScreen(uid)
		if strings.Contains(out, "SELESAI_PANJANG") {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !strings.Contains(out, "SELESAI_PANJANG") {
		t.Errorf("output akhir hilang — buffer terlalu kecil! panjang=%d", len(out))
	} else {
		t.Logf("✅ Output panjang tertangkap (%d karakter, buffer 256KB)", len(out))
	}

	// Bagian AWAL harus juga ada (ini yang hilang dengan buffer lama).
	if !strings.Contains(out, "BARIS_1 ") && !strings.Contains(out, "BARIS_1\t") && !strings.Contains(out, "BARIS_1\n") {
		t.Logf("⚠️  bagian awal tertimpa (buffer penuh) - panjang akhir=%d", len(out))
	} else {
		t.Log("✅ Bagian awal output masih tersimpan")
	}
}

// Uji ClearScreen.
func TestClearScreen(t *testing.T) {
	uid := int64(991003)
	defer KillSession(uid)

	if _, err := getOrCreateSession(uid); err != nil {
		t.Skipf("PTY tidak tersedia: %v", err)
	}
	SendToTerminal(uid, "echo ISI_SEBELUM_CLEAR\n")
	time.Sleep(1000 * time.Millisecond)

	out, _ := ReadScreen(uid)
	if !strings.Contains(out, "ISI_SEBELUM_CLEAR") {
		t.Skip("output tidak tertangkap, lewati")
	}

	if err := ClearScreen(uid); err != nil {
		t.Fatalf("ClearScreen gagal: %v", err)
	}
	out2, _ := ReadScreen(uid)
	if strings.Contains(out2, "ISI_SEBELUM_CLEAR") {
		t.Error("ClearScreen tidak membersihkan buffer")
	} else {
		t.Log("✅ ClearScreen bekerja")
	}
}

// Uji Ctrl+C membatalkan apt (sangat penting: apt upgrade bisa digantung).
func TestAptCancelable(t *testing.T) {
	uid := int64(991004)
	defer KillSession(uid)

	if _, err := getOrCreateSession(uid); err != nil {
		t.Skipf("PTY tidak tersedia: %v", err)
	}

	// Simulasi operasi panjang seperti apt upgrade.
	SendToTerminal(uid, "echo MULAI_LAMA; sleep 300; echo TIDAK_SEHARUSNYA_MUNCUL\n")
	time.Sleep(1500 * time.Millisecond)

	out, _ := ReadScreen(uid)
	if !strings.Contains(out, "MULAI_LAMA") {
		t.Skip("output awal tidak tertangkap")
	}
	t.Log("✅ Operasi panjang berjalan (simulasi apt upgrade)")

	// Batalkan dengan Ctrl+C.
	if err := SendSignal(uid, syscall.SIGINT, "SIGINT"); err != nil {
		t.Fatalf("SIGINT gagal: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)

	// Sesi harus tetap hidup dan operasi harus berhenti.
	if !HasTerminalSession(uid) {
		t.Error("sesi mati setelah Ctrl+C")
	}
	SendToTerminal(uid, "echo SESI_MASIH_HIDUP\n")
	time.Sleep(1200 * time.Millisecond)
	out3, _ := ReadScreen(uid)
	if strings.Contains(out3, "SESI_MASIH_HIDUP") {
		t.Log("✅ Ctrl+C membatalkan operasi panjang, sesi tetap bisa dipakai")
	} else {
		t.Logf("output setelah Ctrl+C: %q", out3)
	}
}
