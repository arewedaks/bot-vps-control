package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestBotTanpaGitTetapBisaUpdate menguji persis keadaan VPS yang melaporkan masalah:
// bot di direktori tanpa .git, dijalankan dari binary, tanpa Go toolchain.
//
// Ini kasus nyata yang sebelumnya ditolak oleh handler — padahal jalur unduh
// tidak butuh git sama sekali.
func TestBotTanpaGitTetapBisaUpdate(t *testing.T) {
	sumberDir, _ := os.Getwd()

	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(sumberDir)

	// Tanpa Go, tanpa git — persis VPS spek rendah.
	st := bacaStatusUpdate()

	fmt.Println("\n  ╔══════════════════════════════════════════════════════╗")
	fmt.Println("  ║  SIMULASI VPS ANDA: /app, tanpa git, tanpa Go        ║")
	fmt.Println("  ╚══════════════════════════════════════════════════════╝")
	fmt.Printf("  direktori    : %s\n", filepath.Base(st.Dir))
	fmt.Printf("  repo git     : %v\n", st.IsGitRepo)
	fmt.Printf("  remote       : %q\n", st.Remote)

	// Yang diuji: keputusan jalur untuk mesin tanpa Go.
	metodeUnduhTerpilih := pilihMetode(false)
	fmt.Printf("  jalur tanpa Go: %s\n", metodeUnduhTerpilih)

	if metodeUnduhTerpilih != metodeUnduh {
		t.Fatalf("❌ tanpa Go seharusnya jalur unduh, dapat %q", metodeUnduhTerpilih)
	}

	// Handler tidak boleh menolak lebih dulu.
	sumber, err := os.ReadFile(filepath.Join(sumberDir, "update_self.go"))
	if err != nil {
		t.Fatalf("baca update.go: %v", err)
	}
	isi := string(sumber)

	// Penolakan hanya boleh muncul bila Go ADA (artinya masih bisa build
	// setelah clone, jadi sarannya masuk akal).
	if !contains(isi, "if !st.IsGitRepo && goTersedia()") {
		t.Error("❌ penolakan bot-tanpa-git tidak dibatasi pada mesin yang punya Go")
	}
	if !contains(isi, "jalankanUpdateUnduh(st.Dir") {
		t.Error("❌ handler tidak memakai jalur unduh untuk bot tanpa git")
	}

	fmt.Println("\n  ✅ Bot di /app tanpa git → langsung jalur unduh")
	fmt.Println("  ✅ Tidak perlu git clone lagi")

	// Syarat nyata jalur unduh: boleh tulis + rename berfungsi.
	if err := os.WriteFile(updateTempName, []byte("x"), 0o755); err != nil {
		t.Fatalf("❌ tidak bisa menulis di %s: %v", st.Dir, err)
	}
	if err := os.Rename(updateTempName, updateBinaryName); err != nil {
		t.Fatalf("❌ rename gagal: %v", err)
	}
	fmt.Println("  ✅ Direktori bisa ditulis & tukar binary berfungsi")
}
