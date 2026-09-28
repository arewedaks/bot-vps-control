package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestBersihkanBerkasProsesLengkap(t *testing.T) {
	scr := "deploy_999_uji_bersih"
	for _, ekstensi := range []string{".pid", ".start", ".log"} {
		jalur := "/tmp/" + scr + ekstensi
		if err := os.WriteFile(jalur, []byte("uji"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bersihkanBerkasProsesLengkap(scr)
	for _, ekstensi := range []string{".pid", ".start", ".log"} {
		if _, err := os.Stat("/tmp/" + scr + ekstensi); !os.IsNotExist(err) {
			t.Errorf("berkas %s%s masih ada setelah dibersihkan", scr, ekstensi)
		}
	}
}

func TestBersihkanSemuaProyek(t *testing.T) {
	// Proyek uji milik pengguna fiktif, bukan milik admin asli.
	// Simpan dan pulihkan DirDeploy: global yang dipakai test lain.
	DirDeployLama := DirDeploy
	DirDeploy = t.TempDir()
	defer func() { DirDeploy = DirDeployLama }()
	const uid = int64(999888)
	dir, err := dirDeployPengguna(uid)
	if err != nil {
		t.Fatal(err)
	}
	// Dua proyek, satu dengan berkas /tmp tertinggal.
	for _, nama := range []string{"satu", "dua"} {
		p := filepath.Join(dir, nama)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Pid HARUS proses yang benar-benar hidup dan milik kita, supaya
	// matikanProsesDeploy benar-benar membunuhnya alih-alih mencoba kill 1
	// (yang menggantung dan tak berwenang). sleep di latar adalah kandidat aman.
	cmdSleeper := exec.Command("sleep", "120")
	if err := cmdSleeper.Start(); err != nil {
		t.Skipf("tidak bisa menyiapkan proses uji: %v", err)
	}
	defer cmdSleeper.Process.Kill()
	pid := berkasPIDDeploy(namaProsesDeploy(uid, "satu"))
	if err := os.WriteFile(pid, []byte(strconv.Itoa(cmdSleeper.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	log := berkasLogDeploy(namaProsesDeploy(uid, "satu"))
	if err := os.WriteFile(log, []byte("log uji"), 0o600); err != nil {
		t.Fatal(err)
	}

	n := BersihkanSemuaProyek(uid)
	if n != 2 {
		t.Fatalf("proyek dihapus = %d, mau 2", n)
	}
	if sisa, _ := os.ReadDir(dir); len(sisa) != 0 {
		t.Errorf("folder proyek belum kosong: %d sisa", len(sisa))
	}
	if _, err := os.Stat(pid); !os.IsNotExist(err) {
		t.Error("berkas .pid masih ada")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Error("berkas .log masih ada — ini yang dulu cuma di-truncate")
	}
	// Folder pengguna ikut hilang tidak diwajibkan, tapi tidak boleh error
	// saat BersihkanSemuaProyek dipanggil lagi (idempoten).
	if lagi := BersihkanSemuaProyek(uid); lagi != 0 {
		t.Errorf("panggilan kedua harus 0, dapat %d", lagi)
	}
}
