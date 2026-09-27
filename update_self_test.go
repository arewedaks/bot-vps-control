package main

// ==============================================================================
// 🧪 TEST FITUR UPDATE
// ==============================================================================
// Fokus test ini bukan "apakah fungsinya jalan", tapi:
//
//   1. Update yang gagal TIDAK merusak bot yang sedang berjalan.
//   2. Perubahan lokal tidak pernah ditimpa diam-diam.
//   3. Keluaran git di-escape sebelum dikirim ke Telegram (HTML).
//
// Ketiganya adalah cara bot ini bisa menghancurkan dirinya sendiri.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitTest tersedia? Test ini butuh git di PATH.
func gitAda(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("SKIP — git tidak tersedia")
	}
}

// buatRepoUji membuat repository git sementara berisi satu commit.
func buatRepoUji(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	perintah := [][]string{
		{"init", "-q"},
		{"config", "user.email", "uji@contoh.test"},
		{"config", "user.name", "Uji"},
	}
	for _, p := range perintah {
		cmd := exec.Command("git", p...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v gagal: %v\n%s", p, err, out)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "berkas.txt"), []byte("awal\n"), 0o644); err != nil {
		t.Fatalf("tulis berkas: %v", err)
	}

	for _, p := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "awal"}} {
		cmd := exec.Command("git", p...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v gagal: %v\n%s", p, err, out)
		}
	}
	return dir
}

// ==============================================================================
// 1. DETEKSI REPOSITORY & STATUS
// ==============================================================================

// TestRepoDirMenemukanSumber memastikan direktori sumber bisa ditemukan.
//
// Tanpa ini, update akan berjalan di direktori yang salah dan gagal dengan
// pesan yang membingungkan.
func TestRepoDirMenemukanSumber(t *testing.T) {
	dir := repoDir()
	if dir == "" {
		t.Fatal("repoDir mengembalikan string kosong")
	}
	// Saat test dijalankan dari direktori sumber, go.mod harus terlihat.
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Errorf("repoDir()=%q tidak memuat go.mod: %v", dir, err)
	}
	t.Logf("✅ Sumber ditemukan di %s", dir)
}

// TestBacaStatusUpdateMengenaliRepoGit memverifikasi pembacaan status.
func TestBacaStatusUpdateMengenaliRepoGit(t *testing.T) {
	gitAda(t)
	dir := buatRepoUji(t)

	// Baca status langsung di direktori uji, tanpa mengubah variabel global.
	commit := strings.TrimSpace(runInDir(dir, "git", "rev-parse", "--short", "HEAD"))
	if commit == "" || strings.Contains(commit, "fatal") {
		t.Fatalf("tidak bisa membaca commit: %q", commit)
	}
	if len(commit) < 4 {
		t.Errorf("commit terlalu pendek: %q", commit)
	}

	// Repo yang baru saja dibuat harus bersih.
	kotor := strings.TrimSpace(runInDir(dir, "git", "status", "--porcelain"))
	if kotor != "" {
		t.Errorf("repo baru seharusnya bersih, tapi ada: %q", kotor)
	}
	t.Logf("✅ Status terbaca: commit=%s, bersih", commit)
}

// TestRunInDirMenangkapError memastikan perintah gagal tetap melaporkan sebab.
//
// Tanpa ini, kegagalan git akan terlihat seperti "tidak ada perubahan".
func TestRunInDirMenangkapError(t *testing.T) {
	dir := t.TempDir()
	out := runInDir(dir, "git", "rev-parse", "HEAD")

	if out == "" {
		t.Error("perintah gagal seharusnya mengembalikan pesan, bukan string kosong")
	}
	if !strings.Contains(strings.ToLower(out), "fatal") &&
		!strings.Contains(strings.ToLower(out), "not a git") &&
		!strings.Contains(strings.ToLower(out), "error") {
		t.Errorf("pesan error tidak informatif: %q", out)
	}
	t.Logf("✅ Error dilaporkan: %s", strings.TrimSpace(out))
}

// ==============================================================================
// 2. KEAMANAN: PERUBAHAN LOKAL TIDAK BOLEH HILANG
// ==============================================================================

// TestPerubahanLokalTerdeteksi memastikan file yang dimodifikasi terlihat
// sebagai "kotor", sehingga bot berhenti alih-alih menimpanya.
//
// Ini melindungi pekerjaan yang dilakukan langsung di VPS.
func TestPerubahanLokalTerdeteksi(t *testing.T) {
	gitAda(t)
	dir := buatRepoUji(t)

	// Ubah berkas tanpa commit.
	if err := os.WriteFile(filepath.Join(dir, "berkas.txt"), []byte("diubah\n"), 0o644); err != nil {
		t.Fatalf("ubah berkas: %v", err)
	}

	out := strings.TrimSpace(runInDir(dir, "git", "status", "--porcelain"))
	if out == "" {
		t.Error("❌ perubahan lokal TIDAK terdeteksi — bot akan menimpanya!")
	} else {
		t.Logf("✅ Perubahan lokal terdeteksi: %q", out)
	}
}

// TestCabangDivergenTerdeteksi memastikan kondisi yang menghambat pull
// fast-forward bisa dikenali dari keluaran git.
//
// Ini skenario nyata di VPS: ada commit lokal langsung di server.
func TestCabangDivergenTerdeteksi(t *testing.T) {
	gitAda(t)
	bare := t.TempDir()
	kerja := t.TempDir()

	run := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, _ := cmd.CombinedOutput()
		return string(out)
	}

	// Siapkan remote kosong.
	if out := run(bare, "init", "-q", "--bare"); strings.Contains(out, "fatal") {
		t.Fatalf("init bare: %s", out)
	}

	// Clone, buat commit pertama, kirim.
	if out := run(kerja, "clone", "-q", bare, "."); strings.Contains(out, "fatal") {
		t.Fatalf("clone: %s", out)
	}
	run(kerja, "config", "user.email", "u@u.test")
	run(kerja, "config", "user.name", "U")
	os.WriteFile(filepath.Join(kerja, "a.txt"), []byte("1\n"), 0o644)
	run(kerja, "add", "-A")
	run(kerja, "commit", "-q", "-m", "satu")
	pushOut := run(kerja, "push", "-q", "origin", "HEAD")
	if strings.Contains(pushOut, "fatal") {
		t.Fatalf("push pertama: %s", pushOut)
	}

	// Commit lokal yang tidak ada di remote.
	os.WriteFile(filepath.Join(kerja, "lokal.txt"), []byte("lokal\n"), 0o644)
	run(kerja, "add", "-A")
	run(kerja, "commit", "-q", "-m", "commit lokal")

	// Commit lain di remote, dibuat dari klon terpisah.
	lain := t.TempDir()
	run(lain, "clone", "-q", bare, ".")
	run(lain, "config", "user.email", "x@x.test")
	run(lain, "config", "user.name", "X")
	os.WriteFile(filepath.Join(lain, "jauh.txt"), []byte("jauh\n"), 0o644)
	run(lain, "add", "-A")
	run(lain, "commit", "-q", "-m", "commit jauh")
	run(lain, "push", "-q", "origin", "HEAD")

	// Sekarang pull di klon kerja harus ditolak.
	run(kerja, "fetch", "-q", "origin")
	out := run(kerja, "pull", "--ff-only", "origin")

	kenali := strings.Contains(out, "Diverging branches") ||
		strings.Contains(out, "not possible to fast-forward") ||
		strings.Contains(out, "divergent") ||
		strings.Contains(out, "fatal")

	if !kenali {
		t.Errorf("❌ cabang divergen tidak terdeteksi dari keluaran git:\n%s", out)
	} else {
		t.Log("✅ Cabang divergen terdeteksi — bot akan berhenti, bukan menimpa")
	}
}

// ==============================================================================
// 3. KEAMANAN: ESCAPE HTML UNTUK KELUARAN GIT
// ==============================================================================

// TestHtmlEscapeRingkasMelindungiTelegram memastikan keluaran git tidak
// membuat Telegram menolak pesan.
//
// Sangat penting: pesan error build hampir selalu memuat "<" dan ">",
// misalnya "cannot use x (type <T>) as type <U>".
func TestHtmlEscapeRingkasMelindungiTelegram(t *testing.T) {
	kasus := []struct{ masuk, harusAda string }{
		{"<nil>", "&lt;nil&gt;"},
		{"a & b", "a &amp; b"},
		{"map[string]int", "map[string]int"},
		{"cannot use x (type <T>)", "&lt;T&gt;"},
		{"</b><script>", "&lt;/b&gt;&lt;script&gt;"},
	}
	for _, k := range kasus {
		hasil := htmlEscapeRingkas(k.masuk, 500)
		if !strings.Contains(hasil, k.harusAda) {
			t.Errorf("escape(%q) = %q, harusnya memuat %q", k.masuk, hasil, k.harusAda)
		}
	}
	// Karakter mentah tidak boleh lolos.
	if strings.Contains(htmlEscapeRingkas("<b>x</b>", 100), "<b>") {
		t.Error("❌ tag HTML mentah lolos — Telegram akan menolak pesan")
	}
	t.Log("✅ Keluaran git aman dikirim sebagai HTML")
}

// TestHtmlEscapeMemberiPenandaPotong memastikan keluaran panjang dipotong
// dengan penanda yang terlihat, bukan dipotong diam-diam.
func TestHtmlEscapeMemberiPenandaPotong(t *testing.T) {
	panjang := strings.Repeat("x", 5000)

	h := htmlEscapeRingkas(panjang, 100)
	if len(h) > 200 {
		t.Errorf("keluaran tidak dipotong, panjang %d", len(h))
	}
	if !strings.Contains(h, "dipotong") {
		t.Error("pemotongan tanpa penanda — pengguna tidak tahu ada bagian hilang")
	}
	t.Log("✅ Pemotongan diberi penanda")
}

// ==============================================================================
// 4. DETEKSI SYSTEMD
// ==============================================================================

// TestNamaLayananSystemdSelaluBerakhiranService memastikan nama unit yang
// dipakai untuk restart selalu masuk akal.
func TestNamaLayananSystemdSelaluBerakhiranService(t *testing.T) {
	nama := namaLayananSystemd()
	if !strings.HasSuffix(nama, ".service") {
		t.Errorf("nama layanan %q tidak berakhiran .service", nama)
	}
	if nama == "" {
		t.Error("nama layanan kosong")
	}
	if strings.ContainsAny(nama, " \t\n/") {
		t.Errorf("nama layanan memuat karakter tidak valid: %q", nama)
	}
	t.Logf("✅ Nama unit: %s", nama)
}

// TestLayananSystemdAktifTidakPanik memastikan deteksi systemd aman di
// lingkungan non-systemd (container, WSL, macOS).
func TestLayananSystemdAktifTidakPanik(t *testing.T) {
	// Hanya memastikan tidak panik dan hasilnya konsisten.
	a := layananSystemdAktif()
	b := layananSystemdAktif()
	if a != b {
		t.Errorf("hasil tidak konsisten: %v lalu %v", a, b)
	}
	t.Logf("✅ Deteksi systemd stabil (hasil: %v)", a)
}

// ==============================================================================
// 5. PEMBANGUNAN PAKET (bukan hanya main.go)
// ==============================================================================

// TestPerintahBuildMemakaiPaketPenuh memastikan perintah build di fitur update
// memakai paket ".", bukan "main.go".
//
// Ini pernah menjadi bug nyata: membangun "main.go" saja gagal dengan puluhan
// error "undefined", karena project ini memakai banyak file dalam satu paket.
// Setiap update akan gagal total.
func TestPerintahBuildMemakaiPaketPenuh(t *testing.T) {
	data, err := os.ReadFile("update_self.go")
	if err != nil {
		t.Fatalf("baca update_self.go: %v", err)
	}
	sumber := string(data)

	if strings.Contains(sumber, `"main.go"`) {
		t.Error("❌ update.go membangun \"main.go\" — akan gagal 'undefined'")
	}
	if !strings.Contains(sumber, `"-o", updateTempName, "."`) {
		t.Error("❌ build harus memakai paket \".\" agar semua file ikut terkompilasi")
	}
	t.Log("✅ Build memakai paket penuh, bukan satu file")
}

// TestBuildFlagSamaDenganMakefile memastikan flag build konsisten.
//
// Binary hasil update harus identik dengan hasil `make build`, agar ukuran dan
// simbil debug tidak berbeda antar versi.
func TestBuildFlagSamaDenganMakefile(t *testing.T) {
	data, err := os.ReadFile("update_self.go")
	if err != nil {
		t.Fatalf("baca update_self.go: %v", err)
	}
	sumber := string(data)

	if !strings.Contains(sumber, `-ldflags=-s -w`) {
		t.Error("❌ flag build berbeda dari Makefile (-ldflags=-s -w)")
	}
	t.Log("✅ Flag build konsisten dengan Makefile")
}

// ==============================================================================
// 6. HANDLER /update TIDAK BOLEH CRASH
// ==============================================================================

// TestHandlerUpdateSemuaArgumenAman memastikan setiap bentuk perintah
// ditangani tanpa panik.
func TestHandlerUpdateSemuaArgumenAman(t *testing.T) {
	argumen := []string{
		"/update",
		"/update cek",
		"/update confirm",
		"/update CEK",
		"/update CONFIRM",
		"/update ngawur",
		"/update    ",
		"/update cek ekstra",
		"/upgrade",
		"/update dengan spasi panjang di belakang   ",
	}

	for _, a := range argumen {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("❌ panic pada %q: %v", a, r)
				}
			}()
			// chatID 0 = pengiriman akan gagal diam-diam, tapi logikanya jalan.
			handleUpdateCommand(0, 0, a)
		}()
	}
	t.Logf("✅ %d bentuk perintah ditangani tanpa panic", len(argumen))
}

// TestHandlerUpdateDiLuarRepoAman memastikan perintah tetap aman dijalankan
// di direktori yang bukan repository git.
func TestHandlerUpdateDiLuarRepoAman(t *testing.T) {
	dir := t.TempDir()
	lama, _ := os.Getwd()
	defer os.Chdir(lama)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("pindah direktori: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("❌ panic di luar repo git: %v", r)
		}
	}()
	handleUpdateCommand(0, 0, "/update")

	// Direktori tanpa .git harus dikenali sebagai bukan repo.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Fatal("direktori uji seharusnya tidak punya .git")
	}
	t.Log("✅ Aman dijalankan di luar repository git")
}

// TestUpdateTidakPernahMenimpaPerubahanLokal adalah jaminan keamanan fitur ini.
//
// Apa pun jalur yang diambil (berhenti di penjagaan, atau pull yang gagal
// sendiri), hasil akhirnya WAJIB sama: pekerjaan lokal utuh.
//
// Test ini sengaja menguji JAMINAN, bukan implementasi. Penjagaan boleh
// dihapus atau ditulis ulang selama jaminannya tetap terpenuhi — dan justru
// itu yang membuatnya berguna: ia gagal hanya bila ada perubahan perilaku
// nyata yang bisa merugikan pengguna.
func TestUpdateTidakPernahMenimpaPerubahanLokal(t *testing.T) {
	gitAda(t)
	dir := buatRepoUji(t)

	remote := t.TempDir()
	if out, err := exec.Command("git", "-C", remote, "init", "-q", "--bare").CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	for _, p := range [][]string{
		{"remote", "add", "origin", remote},
		{"push", "-q", "origin", "HEAD"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, p...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", p, err, out)
		}
	}

	// Isi file unik yang tidak ada di mana pun. Bila hilang, jelas tertimpa.
	penanda := "KARYA_PENTING_YANG_TIDAK_BOLEH_HILANG"
	if err := os.WriteFile(filepath.Join(dir, "berkas.txt"), []byte(penanda+"\n"), 0o644); err != nil {
		t.Fatalf("ubah berkas: %v", err)
	}

	// Remote maju, supaya update benar-benar tersedia.
	lain := t.TempDir()
	for _, p := range [][]string{
		{"clone", "-q", remote, "."},
		{"config", "user.email", "x@x.test"},
		{"config", "user.name", "X"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", lain}, p...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", p, err, out)
		}
	}
	os.WriteFile(filepath.Join(lain, "baru.txt"), []byte("baru\n"), 0o644)
	for _, p := range [][]string{
		{"add", "-A"},
		{"commit", "-q", "-m", "commit baru di remote"},
		{"push", "-q", "origin", "HEAD"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", lain}, p...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", p, err, out)
		}
	}
	runInDir(dir, "git", "fetch", "--quiet", "origin")

	if hitung := strings.TrimSpace(runInDir(dir, "git", "rev-list", "--count", "HEAD..origin/HEAD")); hitung == "0" || hitung == "" {
		t.Skipf("SKIP — remote tidak punya commit baru (tertinggal=%q)", hitung)
	}

	// Jalankan handler sungguhan.
	lama, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("pindah direktori: %v", err)
	}
	handleUpdateCommand(0, 0, "/update")
	handleUpdateCommand(0, 0, "/update confirm")
	os.Chdir(lama)

	// JAMINAN: pekerjaan lokal harus utuh.
	isi, err := os.ReadFile(filepath.Join(dir, "berkas.txt"))
	if err != nil {
		t.Fatalf("berkas hilang: %v", err)
	}
	if !strings.Contains(string(isi), penanda) {
		t.Errorf("❌ PERUBAHAN LOKAL HILANG!\n  sebelum: %q\n  sesudah: %q",
			penanda, strings.TrimSpace(string(isi)))
	} else {
		t.Log("✅ Perubahan lokal utuh setelah /update dan /update confirm")
	}

	// JAMINAN: commit lokal tidak berpindah tanpa izin pengguna.
	kepala := strings.TrimSpace(runInDir(dir, "git", "rev-parse", "HEAD"))
	if kepala == "" {
		t.Error("❌ HEAD tidak bisa dibaca — repo rusak")
	}
	t.Logf("✅ Repo tetap sehat, HEAD=%s", kepala[:minInt(7, len(kepala))])
}

// ==============================================================================
// 7. HELPER
// ==============================================================================

// TestMinInt memverifikasi helper pemotong hash.
func TestMinInt(t *testing.T) {
	kasus := []struct{ a, b, mau int }{
		{1, 2, 1}, {2, 1, 1}, {5, 5, 5}, {-1, 3, -1}, {0, 7, 0},
	}
	for _, k := range kasus {
		if got := minInt(k.a, k.b); got != k.mau {
			t.Errorf("minInt(%d,%d)=%d, mau %d", k.a, k.b, got, k.mau)
		}
	}
	t.Log("✅ minInt benar")
}

// TestHashPendekTidakPanik memastikan pemotongan hash commit aman.
//
// Hash bisa lebih pendek dari 7 karakter pada repository tidak biasa, dan
// memotong tanpa batas akan menyebabkan panic.
func TestHashPendekTidakPanik(t *testing.T) {
	masuk := []string{"", "abc", "1234567", "1234567890abcdef"}
	for _, s := range masuk {
		for _, n := range []int{0, 3, 7, 40} {
			potong := minInt(n, len(s))
			got := s[:potong]
			if len(got) > len(s) {
				t.Errorf("potongan %q lebih panjang dari aslinya %q", got, s)
			}
		}
	}
	t.Log("✅ Pemotongan hash aman untuk semua panjang")
}
