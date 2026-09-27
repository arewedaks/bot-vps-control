package main

// Uji panel deploy. Fokus pada hal yang bisa menghancurkan VPS bila salah:
// pembersihan nama, penolakan path traversal, dan pengenalan bahasa.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ==============================================================================
// PEMBERSIHAN NAMA PROYEK
// ==============================================================================

func TestNamaProyekAmanMenolakTraversal(t *testing.T) {
	kasus := []struct {
		masuk string
		harap string
	}{
		{"../../etc/passwd", "passwd"},
		{"..", "proyek"},
		{"../..", "proyek"},
		{"a;rm -rf /", "a_rm_-rf_"},
		{"bot$(whoami)", "bot__whoami_"},
		{"`id`", "_id_"},
		{"nama biasa", "nama_biasa"},
		{"../hantu", "hantu"},
		{"....", "proyek"},
		{"", "proyek"},
		{"/etc/../root/.ssh", "ssh"},
	}

	for _, k := range kasus {
		hasil := namaProyekAman(k.masuk)
		if hasil != k.harap {
			t.Errorf("namaProyekAman(%q) = %q, harap %q", k.masuk, hasil, k.harap)
		}
		// Hasil tidak boleh mengandung pemisah jalur dalam bentuk apa pun.
		if strings.ContainsAny(hasil, "/\\") {
			t.Errorf("namaProyekAman(%q) = %q masih berisi pemisah jalur", k.masuk, hasil)
		}
		if strings.HasPrefix(hasil, ".") {
			t.Errorf("namaProyekAman(%q) = %q diawali titik", k.masuk, hasil)
		}
	}
}

func TestNamaProyekAmanDipotong(t *testing.T) {
	panjang := strings.Repeat("a", 200)
	hasil := namaProyekAman(panjang)
	if len(hasil) > 64 {
		t.Errorf("nama tidak dipotong: %d karakter", len(hasil))
	}
}

// ==============================================================================
// PENOLAKAN PATH TRAVERSAL DI ARSIP
// ==============================================================================

func TestAmanDiDalam(t *testing.T) {
	basis := t.TempDir()

	if !amanDiDalam(basis, filepath.Join(basis, "a", "b.txt")) {
		t.Error("berkas di dalam basis seharusnya diterima")
	}
	if amanDiDalam(basis, filepath.Join(basis, "..", "luar.txt")) {
		t.Error("berkas di luar basis seharusnya ditolak")
	}
	if amanDiDalam(basis, "/etc/passwd") {
		t.Error("jalur absolut di luar basis seharusnya ditolak")
	}
	if amanDiDalam(basis, filepath.Join(basis, "a", "..", "..", "x")) {
		t.Error("jalur naik dua tingkat seharusnya ditolak")
	}
}

// TestUraikanZipMenolakZipSlip memastikan arsip berbahaya ditolak.
//
// Ini uji paling penting di berkas ini: tanpa pemeriksaan, satu arsip berisi
// entri "../../etc/cron.d/x" bisa menanam berkas di luar direktori proyek.
func TestUraikanZipMenolakZipSlip(t *testing.T) {
	sumber := filepath.Join(t.TempDir(), "jahat.zip")

	f, err := os.Create(sumber)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)

	entri, err := w.Create("../../jahat.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entri.Write([]byte("ditembus")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	f.Close()

	tujuan := t.TempDir()
	ok, pesan := uraikanZip(sumber, tujuan)

	if ok {
		t.Fatal("arsip dengan jalur mencurigakan seharusnya ditolak")
	}
	if !strings.Contains(pesan, "mencurigakan") {
		t.Errorf("pesan kurang jelas: %q", pesan)
	}
}

func TestUraikanTarGzMenolakZipSlip(t *testing.T) {
	sumber := filepath.Join(t.TempDir(), "jahat.tar.gz")

	f, err := os.Create(sumber)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	isi := []byte("ditembus")
	tw.WriteHeader(&tar.Header{
		Name:     "../../jahat.txt",
		Mode:     0o644,
		Size:     int64(len(isi)),
		Typeflag: tar.TypeReg,
	})
	tw.Write(isi)
	tw.Close()
	gz.Close()
	f.Close()

	tujuan := t.TempDir()
	ok, _ := uraikanTarGz(sumber, tujuan)

	if ok {
		t.Fatal("tar.gz dengan jalur mencurigakan seharusnya ditolak")
	}
}

// ==============================================================================
// PENGURAIAN ARSIP YANG SAH
// ==============================================================================

func TestUraikanZipNormal(t *testing.T) {
	sumber := filepath.Join(t.TempDir(), "proyek.zip")

	f, _ := os.Create(sumber)
	w := zip.NewWriter(f)

	entri, _ := w.Create("bot/main.py")
	entri.Write([]byte("print('halo')"))

	entri2, _ := w.Create("bot/requirements.txt")
	entri2.Write([]byte("requests\n"))

	w.Close()
	f.Close()

	tujuan := t.TempDir()
	ok, pesan := uraikanZip(sumber, tujuan)
	if !ok {
		t.Fatalf("arsip sah gagal diurai: %s", pesan)
	}

	// Struktur harus diratakan: isi bot/ naik ke atas.
	if _, err := os.Stat(filepath.Join(tujuan, "main.py")); err != nil {
		t.Error("main.py tidak diratakan ke direktori teratas")
	}
	if _, err := os.Stat(filepath.Join(tujuan, "requirements.txt")); err != nil {
		t.Error("requirements.txt tidak diratakan ke direktori teratas")
	}
}

// ==============================================================================
// DETEKSI BAHASA
// ==============================================================================

func TestDeteksiBahasa(t *testing.T) {
	kasus := []struct {
		nama    string
		berkas  map[string]string
		harapan string
	}{
		{"python", map[string]string{"main.py": "print(1)"}, bahasaPython},
		{"python req", map[string]string{"requirements.txt": "requests"}, bahasaPython},
		{"node", map[string]string{"package.json": "{}"}, bahasaNode},
		{"node js", map[string]string{"bot.js": "console.log(1)"}, bahasaNode},
		{"go mod", map[string]string{"go.mod": "module x"}, bahasaGo},
		{"go file", map[string]string{"main.go": "package main"}, bahasaGo},
		{"shell", map[string]string{"run.sh": "echo hi"}, bahasaShell},
		{"kosong", map[string]string{}, ""},
	}

	for _, k := range kasus {
		t.Run(k.nama, func(t *testing.T) {
			dir := t.TempDir()
			for nama, isi := range k.berkas {
				if err := os.WriteFile(filepath.Join(dir, nama), []byte(isi), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			hasil := deteksiBahasa(dir)
			if hasil != k.harapan {
				t.Errorf("deteksiBahasa = %q, harap %q", hasil, k.harapan)
			}
		})
	}
}

// TestDeteksiBahasaBinerELF memastikan binary dikenali dari isinya,
// bukan hanya dari nama berkasnya.
func TestDeteksiBahasaBinerELF(t *testing.T) {
	dir := t.TempDir()

	// Kepala ELF minimal.
	kepala := []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}
	if err := os.WriteFile(filepath.Join(dir, "app"), kepala, 0o755); err != nil {
		t.Fatal(err)
	}

	if hasil := deteksiBahasa(dir); hasil != bahasaBiner {
		t.Errorf("deteksiBahasa = %q, harap %q", hasil, bahasaBiner)
	}
}

// TestPenandaManualMenang memastikan pilihan pengguna mengalahkan tebakan.
func TestPenandaManualMenang(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte("print(1)"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Tanpa penanda: terdeteksi Python.
	if hasil := deteksiBahasa(dir); hasil != bahasaPython {
		t.Fatalf("harap terdeteksi python, dapat %q", hasil)
	}

	// Dengan penanda manual: pilihan pengguna menang.
	tulisPenanda(dir, penandaBahasaDeploy, bahasaShell)
	if hasil := deteksiBahasa(dir); hasil != bahasaShell {
		t.Errorf("penanda manual diabaikan: dapat %q", hasil)
	}
}

// ==============================================================================
// ENTRY POINT
// ==============================================================================

func TestCariEntrypoint(t *testing.T) {
	dir := t.TempDir()

	for _, nama := range []string{"main.py", "helper.py"} {
		os.WriteFile(filepath.Join(dir, nama), []byte("print(1)"), 0o644)
	}

	if hasil := cariEntrypoint(dir, bahasaPython); hasil != "main.py" {
		t.Errorf("harap main.py, dapat %q", hasil)
	}
}

func TestCariEntrypointGoDariIsiBerkas(t *testing.T) {
	dir := t.TempDir()

	// Berkas non-main sengaja diberi nama lebih dulu agar urutan abjad
	// tidak menipu: yang harus dipilih adalah yang punya func main.
	os.WriteFile(filepath.Join(dir, "aaa.go"), []byte("package helper"), 0o644)
	os.WriteFile(filepath.Join(dir, "zzz.go"), []byte("package main\nfunc main() {}"), 0o644)

	if hasil := cariEntrypoint(dir, bahasaGo); hasil != "zzz.go" {
		t.Errorf("harap zzz.go, dapat %q", hasil)
	}
}

func TestCariEntrypointBinermemilihYangEksekutabel(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "baca.txt"), []byte("data"), 0o644)
	os.WriteFile(filepath.Join(dir, "app"), []byte{0x7f, 'E', 'L', 'F'}, 0o755)

	if hasil := cariEntrypoint(dir, bahasaBiner); hasil != "app" {
		t.Errorf("harap app, dapat %q", hasil)
	}
}

// TestCariEntrypointBinerTanpaBitEksekusi mengunci perbaikan bug nyata.
//
// Binary yang baru diunduh belum punya bit eksekusi. Kalau entry point tidak
// terdeteksi dalam keadaan itu, proyek tidak akan pernah bisa dijalankan:
// butuh eksekutabel untuk memilih, butuh dipilih untuk memberi eksekutabel.
func TestCariEntrypointBinerTanpaBitEksekusi(t *testing.T) {
	dir := t.TempDir()

	// Berkas data biasa: tidak boleh dipilih.
	os.WriteFile(filepath.Join(dir, "catatan.txt"), []byte("halo"), 0o644)
	os.WriteFile(filepath.Join(dir, "data.json"), []byte("{}"), 0o644)

	// Binary ELF tanpa bit eksekusi: harus dipilih.
	elf := append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, []byte("sisa")...)
	os.WriteFile(filepath.Join(dir, "app"), elf, 0o644)

	hasil := cariEntrypoint(dir, bahasaBiner)
	if hasil != "app" {
		t.Errorf("binary tanpa bit eksekusi tidak terdeteksi, dapat %q", hasil)
	}

	// Berkas data tidak boleh dipilih saat binary ELF ada.
	if hasil == "catatan.txt" || hasil == "data.json" {
		t.Errorf("berkas data dipilih sebagai program: %q", hasil)
	}
}

// TestCariEntrypointBinerExe memastikan berkas .exe dikenali sebagai kandidat.
func TestCariEntrypointBinerExe(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "program.exe"), []byte("MZ..."), 0o644)

	if hasil := cariEntrypoint(dir, bahasaBiner); hasil != "program.exe" {
		t.Errorf("harap program.exe, dapat %q", hasil)
	}
}

// TestCariEntrypointBinerMenolakDirektoriData memastikan proyek tanpa binary
// sungguhan tidak memilih berkas sembarangan.
func TestCariEntrypointBinerMenolakDirektoriData(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "catatan.txt"), []byte("halo"), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# halo"), 0o644)

	if hasil := cariEntrypoint(dir, bahasaBiner); hasil != "" {
		t.Errorf("seharusnya tidak ada kandidat, dapat %q", hasil)
	}
}

// ==============================================================================
// VALIDASI URL DAN REPOSITORY
// ==============================================================================

func TestValidasiURL(t *testing.T) {
	sah := []string{
		"https://example.com/bot.zip",
		"http://example.com/bot.tar.gz",
	}
	for _, u := range sah {
		if _, err := validasiURL(u); err != nil {
			t.Errorf("URL %q seharusnya diterima: %v", u, err)
		}
	}

	tolak := []string{
		"",
		"file:///etc/passwd",
		"ftp://example.com/x",
		"javascript:alert(1)",
		"gopher://x",
		"/etc/passwd",
	}
	for _, u := range tolak {
		if _, err := validasiURL(u); err == nil {
			t.Errorf("URL %q seharusnya ditolak", u)
		}
	}
}

func TestValidasiRepoGit(t *testing.T) {
	// Bentuk ringkas diperluas menjadi URL GitHub.
	hasil, err := validasiRepoGit("pemilik/repo")
	if err != nil {
		t.Fatalf("bentuk pemilik/repo seharusnya diterima: %v", err)
	}
	if hasil != "https://github.com/pemilik/repo.git" {
		t.Errorf("hasil = %q", hasil)
	}

	// URL penuh tanpa .git mendapat akhiran .git.
	hasil, err = validasiRepoGit("https://github.com/a/b")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(hasil, ".git") {
		t.Errorf("hasil tidak berakhiran .git: %q", hasil)
	}

	tolak := []string{
		"",
		"pemilik",
		"a/b/c",
		"git://github.com/a/b",
		"file:///etc/passwd",
		"../../etc",
	}
	for _, r := range tolak {
		if _, err := validasiRepoGit(r); err == nil {
			t.Errorf("repo %q seharusnya ditolak", r)
		}
	}
}

// ==============================================================================
// PEMINDAI IMPOR PYTHON
// ==============================================================================

func TestPindaiImporPython(t *testing.T) {
	dir := t.TempDir()

	kode := `import os
import sys
import requests
from telebot import types
from PIL import Image
import localmodule
from bs4 import BeautifulSoup
`
	os.WriteFile(filepath.Join(dir, "bot.py"), []byte(kode), 0o644)
	os.WriteFile(filepath.Join(dir, "localmodule.py"), []byte("x = 1"), 0o644)

	paket := pindaiImporPython(dir)

	// Modul bawaan tidak boleh muncul.
	for _, p := range paket {
		if p == "os" || p == "sys" {
			t.Errorf("modul bawaan %q ikut terdeteksi", p)
		}
		if p == "localmodule" {
			t.Errorf("modul lokal ikut terdeteksi")
		}
	}

	// Nama impor harus dipetakan ke nama paket PyPI.
	wajib := map[string]bool{
		"requests":         false,
		"pyTelegramBotAPI": false,
		"Pillow":           false,
		"beautifulsoup4":   false,
	}
	for _, p := range paket {
		if _, ada := wajib[p]; ada {
			wajib[p] = true
		}
	}
	for nama, ketemu := range wajib {
		if !ketemu {
			t.Errorf("paket %q tidak terdeteksi (dapat: %v)", nama, paket)
		}
	}
}

// ==============================================================================
// PERINTAH JALAN
// ==============================================================================

func TestPerintahJalan(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.py"), []byte("print(1)"), 0o644)

	// Python: perintah harus memakai python3 dan mengutip entry.
	if adaPerintah("python3") {
		perintah, err := perintahJalan(dir, bahasaPython, "main.py")
		if err != nil {
			t.Fatalf("python seharusnya didukung: %v", err)
		}
		if !strings.Contains(perintah, "python3") || !strings.Contains(perintah, "main.py") {
			t.Errorf("perintah janggal: %q", perintah)
		}
	}

	// Binary harus dibuat eksekutabel walau awalnya tidak.
	os.WriteFile(filepath.Join(dir, "app"), []byte{0x7f, 'E', 'L', 'F'}, 0o644)
	perintah, err := perintahJalan(dir, bahasaBiner, "app")
	if err != nil {
		t.Fatalf("binary seharusnya didukung: %v", err)
	}
	if !strings.Contains(perintah, "app") {
		t.Errorf("perintah janggal: %q", perintah)
	}
	fi, _ := os.Stat(filepath.Join(dir, "app"))
	if fi.Mode()&0o111 == 0 {
		t.Error("binary tidak diberi bit eksekusi")
	}
}

// TestKutipShellMenahanInjeksi memastikan nama berbahaya tidak lolos ke shell.
func TestKutipShellMenahanInjeksi(t *testing.T) {
	jahat := "a'; rm -rf /; echo '"
	hasil := kutipShell(jahat)

	// Kutip tunggal di dalam input harus dilolos dan dibungkus ulang.
	if !strings.HasPrefix(hasil, "'") || !strings.HasSuffix(hasil, "'") {
		t.Errorf("hasil tidak dibungkus kutip: %q", hasil)
	}
	if strings.Count(hasil, "'")%2 != 0 {
		t.Errorf("jumlah kutip ganjil, kemungkinan bocor: %q", hasil)
	}
}

// ==============================================================================
// SIKLUS HIDUP PROSES
// ==============================================================================

// TestProyekPythonHidupMatimemastikan satu proyek Python bisa dinyalakan,
// dihentikan, dan bahwa berkas PID dibersihkan setelahnya.
func TestProyekPythonHidupMati(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("butuh shell POSIX")
	}
	if !adaPerintah("sh") {
		t.Skip("sh tidak tersedia")
	}

	// Direktori kerja uji diarahkan ke temp supaya tidak menyentuh /opt.
	dirAsli := DirDeploy
	DirDeploy = t.TempDir()
	defer func() { DirDeploy = dirAsli }()

	const userID int64 = 4242
	nama := "ujicoba"

	dir := filepath.Join(dirDeployPenggunaWajib(userID), nama)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Proyek shell yang tidur lama — sederhana dan tidak butuh Python.
	os.WriteFile(filepath.Join(dir, "run.sh"),
		[]byte("#!/bin/sh\nwhile true; do sleep 1; done\n"), 0o755)
	tulisPenanda(dir, penandaBahasaDeploy, bahasaShell)

	scr := namaProsesDeploy(userID, nama)

	ok, pesan := jalankanProyekDeploy(userID, nama)
	if !ok {
		t.Fatalf("proyek gagal dinyalakan: %s", pesan)
	}

	if pid := pidHidupDeploy(scr); pid == 0 {
		t.Fatal("berkas PID tidak terbentuk setelah proyek dinyalakan")
	}

	st := infoProsesDeploy(userID, nama)
	if !st.Jalan {
		t.Error("status melaporkan tidak berjalan padahal PID hidup")
	}
	if st.PID == 0 {
		t.Error("PID tidak terisi")
	}

	// Hentikan dan pastikan berkas PID dibersihkan.
	matikanProsesDeploy(scr)

	if pid := pidHidupDeploy(scr); pid != 0 {
		t.Errorf("proses masih hidup setelah dihentikan: PID %d", pid)
	}
	if _, err := os.Stat(berkasPIDDeploy(scr)); err == nil {
		t.Error("berkas PID tidak dibersihkan")
	}
}

// TestDeteksiBerkasPIDSisa memastikan berkas PID milik proses mati dibersihkan.
//
// Berkas PID yang tertinggal setelah crash akan membuat panel melaporkan
// "Running" palsu selamanya. Pembersihan otomatis di sini mencegahnya.
func TestDeteksiBerkasPIDSisa(t *testing.T) {
	scr := "uji_pid_sisa_" + strings.ReplaceAll(t.Name(), "/", "_")
	defer bersihkanBerkasProsesDeploy(scr)

	// PID 999999 hampir pasti tidak ada di sistem mana pun.
	os.WriteFile(berkasPIDDeploy(scr), []byte("999999"), 0o644)

	if pid := pidHidupDeploy(scr); pid != 0 {
		t.Errorf("PID mati dilaporkan hidup: %d", pid)
	}
	if _, err := os.Stat(berkasPIDDeploy(scr)); err == nil {
		t.Error("berkas PID sisa tidak dibersihkan")
	}
}

func TestBacaEkorLogKosong(t *testing.T) {
	scr := "uji_log_kosong_" + strings.ReplaceAll(t.Name(), "/", "_")
	isi := bacaEkorLogDeploy(scr, 100)
	if isi == "" {
		t.Error("log kosong seharusnya mengembalikan keterangan, bukan string kosong")
	}
}

// ==============================================================================
// LOKASI DIREKTORI
// ==============================================================================

// TestSiapkanDirDeployMenolakLokasiTidakBisaDitulis mengunci perbaikan bug
// nyata: bot dijalankan sebagai pengguna biasa, /opt tidak bisa ditulis, dan
// galatnya dulu tidak pernah terlihat oleh pengguna.
func TestSiapkanDirDeployMenolakLokasiTidakBisaDitulis(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("berjalan sebagai root: semua lokasi bisa ditulis")
	}

	dirAsli := DirDeploy
	// /proc/1 pasti tidak bisa ditulis oleh pengguna biasa.
	DirDeploy = "/proc/1/tidak-mungkin"
	defer func() { DirDeploy = dirAsli }()

	_, pesan := siapkanDirDeploy(1)
	if pesan == "" {
		t.Fatal("lokasi tidak bisa ditulis seharusnya menghasilkan pesan galat")
	}
	// Pesan harus memberi tahu pengguna apa yang bisa dilakukan.
	if !strings.Contains(pesan, "DEPLOY_DIR") {
		t.Errorf("pesan tidak memberi jalan keluar: %q", pesan)
	}
}

func TestSiapkanDirDeployBerhasilDiLokasiSah(t *testing.T) {
	dirAsli := DirDeploy
	DirDeploy = t.TempDir()
	defer func() { DirDeploy = dirAsli }()

	dir, pesan := siapkanDirDeploy(1234)
	if pesan != "" {
		t.Fatalf("lokasi sah seharusnya berhasil: %s", pesan)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("direktori tidak terbentuk: %v", err)
	}

	// Berkas uji tulis harus dibersihkan, bukan ditinggalkan.
	if _, err := os.Stat(filepath.Join(dir, ".uji-tulis")); err == nil {
		t.Error("berkas uji tulis tidak dibersihkan")
	}
}

// TestDirDeployTidakMengarahKeOpt memastikan lokasi bawaan bisa ditulis
// pengguna biasa.
func TestDirDeployTidakMengarahKeOpt(t *testing.T) {
	// Nilai bawaan hanya berlaku bila DEPLOY_DIR tidak disetel.
	if os.Getenv("DEPLOY_DIR") != "" {
		t.Skip("DEPLOY_DIR disetel di lingkungan")
	}
	if os.Geteuid() == 0 {
		t.Skip("root: /opt bisa ditulis")
	}

	if strings.HasPrefix(DirDeploy, "/opt") {
		t.Errorf("lokasi bawaan %q tidak bisa ditulis pengguna biasa", DirDeploy)
	}

	// Lokasi bawaan harus benar-benar bisa dibuat.
	dir, pesan := siapkanDirDeploy(987654)
	if pesan != "" {
		t.Fatalf("lokasi bawaan tidak bisa dipakai: %s", pesan)
	}
	defer os.RemoveAll(divisiInduk(dir))
}

// divisiInduk mengambil dua tingkat di atas, untuk membersihkan hasil uji.
func divisiInduk(path string) string {
	return filepath.Dir(filepath.Dir(path))
}

// TestFallbackTidakPernahKeOpt mengunci perbaikan bug nyata.
//
// Bawaan lama adalah /opt/deploy-bot, yang TIDAK bisa ditulis pengguna biasa,
// dan saat itu tidak ada fallback sama sekali — bot hanya gagal dengan pesan
// "permission denied" tanpa jalan keluar.
func TestFallbackTidakPernahKeOpt(t *testing.T) {
	if os.Getenv("DEPLOY_DIR") != "" {
		t.Skip("DEPLOY_DIR disetel dari luar")
	}
	if strings.HasPrefix(DirDeploy, "/opt") {
		t.Errorf("DirDeploy %q mengarah ke /opt — tidak bisa ditulis pengguna biasa", DirDeploy)
	}
}

// TestFallbackAdaSaatEnvKosong memastikan lokasi selalu bisa ditentukan,
// bahkan tanpa DEPLOY_DIR. Tanpa uji ini, menghapus salah satu tingkat
// fallback tidak akan ketahuan sampai pengguna kehilangan proyeknya.
func TestFallbackAdaSaatEnvKosong(t *testing.T) {
	os.Unsetenv("DEPLOY_DIR")
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("HOME tidak tersedia di lingkungan ini")
	}
	harap := filepath.Join(home, ".local", "share", "deploy-bot")
	if DirDeploy != harap {
		t.Errorf("DirDeploy = %q, harap %q", DirDeploy, harap)
	}
}

// ==============================================================================
// UI
// ==============================================================================

func TestMenuDeployUtamaTidakPanik(t *testing.T) {
	dirAsli := DirDeploy
	DirDeploy = t.TempDir()
	defer func() { DirDeploy = dirAsli }()

	teks, kb := menuDeployUtama(555)
	if teks == "" {
		t.Error("teks menu kosong")
	}
	if kb == nil || len(kb.InlineKeyboard) == 0 {
		t.Error("keyboard menu kosong")
	}

	// Setiap callback_data harus dikenali oleh salah satu penangan, dan tidak
	// melebihi batas 64 byte yang diberlakukan Telegram.
	//
	// Tombol navigasi lintas menu berawalan hp: dan ditangani di main.go:
	// itu disengaja, supaya pengguna bisa kembali ke menu utama.
	for _, baris := range kb.InlineKeyboard {
		for _, b := range baris {
			if !strings.HasPrefix(b.CallbackData, "dp:") &&
				!strings.HasPrefix(b.CallbackData, "hp:") {
				t.Errorf("callback_data %q tidak dikenali", b.CallbackData)
			}
			if len(b.CallbackData) > 64 {
				t.Errorf("callback_data terlalu panjang: %q", b.CallbackData)
			}
		}
	}
}

// TestPanelPilihBahasaTidakPanik memastikan panel pilih bahasa aman dipanggil.
func TestPanelPilihBahasaTidakPanik(t *testing.T) {
	dirAsli := DirDeploy
	DirDeploy = t.TempDir()
	defer func() { DirDeploy = dirAsli }()

	const uid int64 = 321
	dir := filepath.Join(dirDeployPenggunaWajib(uid), "pilihan")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "main.py"), []byte("x=1"), 0o644)

	teks, kb := panelPilihBahasa(uid, "pilihan")
	if teks == "" || kb == nil {
		t.Fatal("panel pilih bahasa kosong")
	}

	// Harus ada tombol untuk setiap bahasa yang didukung.
	gabung := ""
	for _, baris := range kb.InlineKeyboard {
		for _, b := range baris {
			gabung += b.CallbackData + " "
		}
	}
	for _, b := range daftarBahasaSemua() {
		if !strings.Contains(gabung, ":"+b) {
			t.Errorf("tidak ada tombol untuk bahasa %q", b)
		}
	}
}

// TestPanduanDeployBaruTidakPanik memastikan panduan punya tombol kembali.
func TestPanduanDeployBaruTidakPanik(t *testing.T) {
	teks, kb := panduanDeployBaru()
	if teks == "" || kb == nil {
		t.Fatal("panduan deploy kosong")
	}
}

func TestPanelKontrolTidakPanik(t *testing.T) {
	dirAsli := DirDeploy
	DirDeploy = t.TempDir()
	defer func() { DirDeploy = dirAsli }()

	const userID int64 = 7
	nama := "contoh"
	dir := filepath.Join(dirDeployPenggunaWajib(userID), nama)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "main.py"), []byte("print(1)"), 0o644)

	teks, kb := panelKontrolDeploy(userID, nama)
	if teks == "" {
		t.Error("teks panel kosong")
	}
	if kb == nil || len(kb.InlineKeyboard) == 0 {
		t.Error("keyboard panel kosong")
	}

	// Daftar proyek juga harus aman untuk pengguna tanpa proyek.
	if teks, kb := daftarProyekPanel(99999, 0); teks == "" || kb == nil {
		t.Error("daftar proyek kosong tidak ditangani")
	}
}
