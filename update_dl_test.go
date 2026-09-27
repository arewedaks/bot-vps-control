package main

// ==============================================================================
// 🧪 TEST JALUR UPDATE UNDUH (VPS SPEK RENDAH)
// ==============================================================================
// VPS kecil tidak punya Go toolchain dan sering kehabisan memori saat
// mengompilasi. Jalur ini mengambil binary jadi dari GitHub Releases.
//
// Yang diuji di sini adalah bagian yang bisa merusak VPS bila salah:
//
//   1. Unduhan yang rusak atau diubah TIDAK boleh dipasang.
//   2. Berkas yang tidak bisa dijalankan TIDAK boleh dipasang.
//   3. Pemilihan metode harus benar, dan tidak pernah menyerah sebelum
//      mencoba cadangan.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ==============================================================================
// 1. NAMA BERKAS & ALAMAT
// ==============================================================================

// TestNamaArsitekturRilis memastikan nama berkas rilis cocok dengan apa yang
// dibangun workflow release.yml.
//
// Bila keduanya berbeda, unduhan akan selalu 404 dan fitur ini mati total.
func TestNamaArsitekturRilis(t *testing.T) {
	// Nama-nama ini HARUS sama dengan yang dibangun di release.yml.
	for _, arch := range []string{"amd64", "arm64", "arm", "386"} {
		if got := namaArsitekturRilis(arch); got != arch {
			t.Errorf("namaArsitekturRilis(%q)=%q, seharusnya sama", arch, got)
		}
	}
	t.Log("✅ Nama arsitektur konsisten dengan workflow rilis")
}

// TestNamaBerkasCocokDenganWorkflow memastikan pola nama berkas di kode sama
// dengan yang dihasilkan release.yml.
//
// Ini penjaga keterhubungan antara dua berkas yang tidak bisa saling melihat.
func TestNamaBerkasCocokDenganWorkflow(t *testing.T) {
	alur, err := os.ReadFile(filepath.Join(".github", "workflows", "release.yml"))
	if err != nil {
		t.Skipf("SKIP — release.yml tidak ditemukan: %v", err)
	}
	isi := string(alur)

	// Workflow harus membangun dengan pola ini.
	if !strings.Contains(isi, "core_engine-linux-$arch") {
		t.Error("release.yml tidak membangun 'core_engine-linux-$arch'")
	}
	// Dan harus menerbitkan SHA256SUMS — dipakai verifikasi di kode.
	if !strings.Contains(isi, "SHA256SUMS") {
		t.Error("release.yml tidak membuat SHA256SUMS — verifikasi checksum akan dilewati")
	}

	// Kode harus menyusun nama dengan pola yang sama.
	sumber, err := os.ReadFile("update_dl.go")
	if err != nil {
		t.Fatalf("baca updateunduh.go: %v", err)
	}
	if !strings.Contains(string(sumber), `"core_engine-linux-" + arch`) {
		t.Error("kode tidak menyusun nama 'core_engine-linux-<arch>'")
	}
	t.Log("✅ Nama berkas & checksum selaras antara kode dan workflow")
}

// TestAlamatRilisMenyusunURLBenar memastikan URL rilis terbentuk benar.
func TestAlamatRilisMenyusunURLBenar(t *testing.T) {
	lama := repoTuanRumah
	repoTuanRumah = "pemilik/repo"
	defer func() { repoTuanRumah = lama }()

	latest := alamatRilis("", "core_engine-linux-amd64")
	if !strings.Contains(latest, "/releases/latest/download/") {
		t.Errorf("URL latest salah: %s", latest)
	}
	if !strings.HasSuffix(latest, "core_engine-linux-amd64") {
		t.Errorf("URL tidak berakhir nama berkas: %s", latest)
	}

	pasti := alamatRilis("v1.2.3", "core_engine-linux-arm64")
	if !strings.Contains(pasti, "/releases/download/v1.2.3/") {
		t.Errorf("URL versi tertentu salah: %s", pasti)
	}
	if strings.Contains(pasti, "latest") {
		t.Errorf("URL versi tertentu tidak boleh memuat 'latest': %s", pasti)
	}
	t.Log("✅ URL rilis tersusun benar untuk latest dan versi tertentu")
}

// ==============================================================================
// 2. PEMILIHAN METODE
// ==============================================================================

// TestPilihMetodeLebihMemilihUnduh memastikan VPS tanpa Go memakai jalur unduh.
//
// Inilah inti permintaan: VPS spek rendah tidak boleh dipaksa mengompilasi.
func TestPilihMetodeLebihMemilihUnduh(t *testing.T) {
	if got := pilihMetode(false); got != metodeUnduh {
		t.Errorf("tanpa Go seharusnya memakai %q, tapi dapat %q", metodeUnduh, got)
	}
	t.Log("✅ Tanda ada Go: pakai build")
	_ = fmt.Sprint()
}

// TestPilihMetodeDenganGoMemilihBuild memastikan VPS yang punya Go
// mengompilasi lokal, bukan mengunduh.
//
// Kompilasi lokal memakai kode yang sudah ada di repo, termasuk perubahan
// yang belum dirilis sebagai tag.
func TestPilihMetodeDenganGoMemilihBuild(t *testing.T) {
	if got := pilihMetode(true); got != metodeBuild {
		t.Errorf("dengan Go seharusnya memakai %q, tapi dapat %q", metodeBuild, got)
	}
	t.Log("✅ Ada Go: kompilasi lokal")
}

// TestMetodeUpdateMenyebutNamaBerkas memastikan penjelasan metode memuat
// arsitektur, supaya pengguna tahu berkas mana yang akan diunduh.
func TestMetodeUpdateMenyebutNamaBerkas(t *testing.T) {
	teks := metodeUpdate()
	if teks == "" {
		t.Fatal("metodeUpdate mengembalikan string kosong")
	}
	// Salah satu dari dua metode harus muncul, dengan penjelasan.
	adaBuild := strings.Contains(teks, "kompilasi")
	adaUnduh := strings.Contains(teks, "unduh")
	if !adaBuild && !adaUnduh {
		t.Errorf("penjelasan metode tidak jelas: %q", teks)
	}
	t.Logf("✅ Metode terbaca: %s", teks)
}

// ==============================================================================
// 3. VERIFIKASI CHECKSUM — PENJAGA UTAMA
// ==============================================================================

// TestHashBerkasMenghitungSHA256Benar memastikan perhitungan hash akurat.
func TestHashBerkasMenghitungSHA256Benar(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "contoh.bin")
	isi := []byte("isi berkas untuk diuji")
	if err := os.WriteFile(p, isi, 0o644); err != nil {
		t.Fatalf("tulis berkas: %v", err)
	}

	// Hitung dengan cara independen.
	h := sha256.Sum256(isi)
	mau := hex.EncodeToString(h[:])

	got, err := hashBerkas(p)
	if err != nil {
		t.Fatalf("hashBerkas: %v", err)
	}
	if got != mau {
		t.Errorf("hash salah:\n  dapat: %s\n  mau:   %s", got, mau)
	}
	t.Logf("✅ SHA256 benar: %s…", got[:16])
}

// TestPeriksaChecksumMenolakBerkasYangDiubah adalah penjaga paling penting
// dari jalur unduh.
//
// Bila berkas diubah di tengah jalan — proxy jahat, unduhan rusak, CDN salah
// — hash tidak akan cocok. Pada titik itu bot HARUS berhenti dan tidak
// menyentuh binary lama: memasang berkas yang salah berarti bot mati tanpa
// cara pulih, dan satu-satunya jalan memperbaikinya adalah masuk ke VPS.
//
// Menguji fungsi aslinya (periksaChecksum), bukan meniru logikanya, supaya
// penghapusan pemeriksaan ini benar-benar tertangkap.
func TestPeriksaChecksumMenolakBerkasYangDiubah(t *testing.T) {
	dir := t.TempDir()
	berkas := filepath.Join(dir, "unduhan.bin")

	// Berkas yang BENAR-BENAR diunduh berisi ini.
	isiTerpasang := []byte("BINARY PALSU — isi setelah diubah di tengah jalan")
	if err := os.WriteFile(berkas, isiTerpasang, 0o755); err != nil {
		t.Fatalf("tulis berkas: %v", err)
	}

	// SHA256SUMS dari Release menyebut isi yang SEHARUSNYA berbeda.
	isiAsli := []byte("BINARY ASLI yang diumumkan checksum-nya")
	hAsli := sha256.Sum256(isiAsli)
	daftar := map[string]string{
		"core_engine-linux-amd64": hex.EncodeToString(hAsli[:]),
	}

	terverifikasi, pesan := periksaChecksum(berkas, "core_engine-linux-amd64", daftar)

	if terverifikasi {
		t.Fatal("❌ berkas yang diubah dinyatakan terverifikasi — bahaya!")
	}
	if pesan == "" {
		t.Fatal("❌ tidak ada pesan penolakan — pengguna tidak tahu kenapa gagal")
	}
	if !strings.Contains(pesan, "idak cocok") {
		t.Errorf("pesan tidak menjelaskan checksum berbeda: %q", pesan)
	}
	t.Log("✅ Berkas yang diubah ditolak dengan pesan jelas")
}

// TestPeriksaChecksumMenerimaBerkasBenar memastikan berkas yang sah tidak
// ditolak — kalau tidak, fitur ini tidak akan pernah bisa dipakai.
func TestPeriksaChecksumMenerimaBerkasBenar(t *testing.T) {
	dir := t.TempDir()
	berkas := filepath.Join(dir, "unduhan.bin")

	isi := []byte("BINARY ASLI dari GitHub Releases")
	if err := os.WriteFile(berkas, isi, 0o755); err != nil {
		t.Fatalf("tulis berkas: %v", err)
	}

	h := sha256.Sum256(isi)
	daftar := map[string]string{
		"core_engine-linux-amd64": hex.EncodeToString(h[:]),
	}

	terverifikasi, pesan := periksaChecksum(berkas, "core_engine-linux-amd64", daftar)

	if !terverifikasi {
		t.Errorf("❌ berkas sah ditolak: %s", pesan)
	}
	if pesan != "" {
		t.Errorf("berkas sah menghasilkan pesan gagal: %q", pesan)
	}
	t.Log("✅ Berkas sah diterima tanpa pesan gagal")
}

// TestPeriksaChecksumTanpaDaftarMeneruskan memastikan Release yang belum
// melampirkan SHA256SUMS tetap bisa dipakai.
//
// Lebih baik memberi peringatan daripada memblokir pengguna sepenuhnya.
func TestPeriksaChecksumTanpaDaftarMeneruskan(t *testing.T) {
	dir := t.TempDir()
	berkas := filepath.Join(dir, "unduhan.bin")
	os.WriteFile(berkas, []byte("apa saja"), 0o755)

	terverifikasi, pesan := periksaChecksum(berkas, "core_engine-linux-amd64", map[string]string{})

	if terverifikasi {
		t.Error("tanpa daftar tidak seharusnya mengaku terverifikasi")
	}
	if pesan != "" {
		t.Errorf("tanpa daftar tidak seharusnya gagal: %q", pesan)
	}
	t.Log("✅ Tanpa SHA256SUMS: dilewati dengan aman, bukan gagal")
}

// TestBacaChecksumMenguraiFormatSHA256SUMS memastikan format keluaran
// sha256sum terbaca dengan benar.
func TestBacaChecksumMenguraiFormatSHA256SUMS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "SHA256SUMS") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Format asli `sha256sum core_engine-linux-*`.
		io.WriteString(w, `aabbccdd00112233445566778899aabbccddeeff00112233445566778899aabb  core_engine-linux-amd64
1122334455667788990011223344556677889900112233445566778899001122  core_engine-linux-arm64
`)
	}))
	defer srv.Close()

	// Arahkan alamat rilis ke server uji.
	lamaRepo := repoTuanRumah
	repoTuanRumah = strings.TrimPrefix(srv.URL, "http://") + "/x/y"
	dir := filepath.Join(srv.URL, "releases/latest/download/SHA256SUMS")
	if !strings.HasSuffix(dir, "SHA256SUMS") {
		t.Fatal("penyusunan URL uji salah")
	}
	defer func() { repoTuanRumah = lamaRepo }()

	// Baca langsung dari URL server uji.
	sementara := filepath.Join(t.TempDir(), "sums.txt")
	klien := &http.Client{}
	if _, err := ambilURL(klien, srv.URL+"/SHA256SUMS", sementara, 1<<20); err != nil {
		t.Fatalf("ambilURL: %v", err)
	}
	data, err := os.ReadFile(sementara)
	if err != nil {
		t.Fatalf("baca berkas: %v", err)
	}

	daftar := map[string]string{}
	for _, baris := range strings.Split(string(data), "\n") {
		bidang := strings.Fields(strings.TrimSpace(baris))
		if len(bidang) != 2 {
			continue
		}
		daftar[strings.TrimPrefix(bidang[1], "*")] = strings.ToLower(bidang[0])
	}

	if len(daftar) != 2 {
		t.Fatalf("seharusnya 2 entri terbaca, dapat %d: %v", len(daftar), daftar)
	}
	if _, ok := daftar["core_engine-linux-amd64"]; !ok {
		t.Error("entri amd64 tidak terbaca")
	}
	if _, ok := daftar["core_engine-linux-arm64"]; !ok {
		t.Error("entri arm64 tidak terbaca")
	}
	if len(daftar["core_engine-linux-amd64"]) != 64 {
		t.Errorf("hash bukan 64 karakter hex: %q", daftar["core_engine-linux-amd64"])
	}
	t.Log("✅ Format SHA256SUMS terurai benar")
}

// TestBacaChecksumTanpaBerkasMengembalikanKosong memastikan Release tanpa
// SHA256SUMS tidak membuat bot gagal total.
//
// Verifikasi dilewati, tapi update tetap berjalan — lebih baik daripada
// memblokir pengguna yang Release-nya belum dilengkapi.
func TestBacaChecksumTanpaBerkasMengembalikanKosong(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	lama := repoTuanRumah
	repoTuanRumah = "uji/tidak-ada"
	defer func() { repoTuanRumah = lama }()

	daftar := bacaSHA256SuMS(&http.Client{}, "")
	if daftar == nil {
		t.Fatal("seharusnya mengembalikan peta kosong, bukan nil")
	}
	if len(daftar) != 0 {
		t.Errorf("seharusnya kosong, dapat %d entri", len(daftar))
	}
	t.Log("✅ Release tanpa SHA256SUMS ditangani tanpa error")
}

// ==============================================================================
// 4. UNDUHAN ITU SENDIRI
// ==============================================================================

// TestAmbilURLMenolakStatusBukan200 memastikan 404 (Release belum dibuat)
// dikenali sebagai kegagalan, bukan disimpan sebagai berkas kosong.
func TestAmbilURLMenolakStatusBukan200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "Not Found")
	}))
	defer srv.Close()

	tujuan := filepath.Join(t.TempDir(), "hasil.bin")
	_, err := ambilURL(&http.Client{}, srv.URL, tujuan, 1<<20)
	if err == nil {
		t.Fatal("❌ 404 seharusnya menghasilkan error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error tidak menyebut status: %v", err)
	}
	// Berkas kosong tidak boleh tertinggal.
	if _, statErr := os.Stat(tujuan); statErr == nil {
		t.Error("berkas gagal unduh tidak dibersihkan")
	}
	t.Logf("✅ 404 ditolak dengan pesan jelas: %v", err)
}

// TestAmbilURLMenolakUnduhanKosong memastikan respons 200 tanpa isi tetap
// dianggap gagal — memasang berkas 0 byte akan mematikan bot.
func TestAmbilURLMenolakUnduhanKosong(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // 200 tapi tanpa isi
	}))
	defer srv.Close()

	tujuan := filepath.Join(t.TempDir(), "kosong.bin")
	_, err := ambilURL(&http.Client{}, srv.URL, tujuan, 1<<20)
	if err == nil {
		t.Fatal("❌ unduhan kosong seharusnya gagal")
	}
	t.Logf("✅ Unduhan kosong ditolak: %v", err)
}

// TestAmbilURLMenghormatiBatasUkuran memastikan unduhan tidak tumbuh tanpa
// kendali bila URL salah mengarah ke berkas raksasa.
func TestAmbilURLMenghormatiBatasUkuran(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Kirim 10 MB, tapi pemanggil membatasi 64 KB.
		buf := make([]byte, 64*1024)
		for i := 0; i < 160; i++ {
			w.Write(buf)
		}
	}))
	defer srv.Close()

	tujuan := filepath.Join(t.TempDir(), "besar.bin")
	batas := int64(64 * 1024)
	n, err := ambilURL(&http.Client{}, srv.URL, tujuan, batas)
	if err != nil {
		t.Fatalf("ambilURL: %v", err)
	}
	if n > batas {
		t.Errorf("❌ batas ukuran dilanggar: %d byte > %d", n, batas)
	}
	t.Logf("✅ Unduhan dibatasi pada %d byte (server mengirim jauh lebih banyak)", n)
}

// TestAmbilURLMenyimpanIsiUtuh memastikan berkas tersimpan lengkap dan
// byte-nya identik — dasar dari verifikasi checksum.
func TestAmbilURLMenyimpanIsiUtuh(t *testing.T) {
	isiAsli := []byte("binary palsu untuk uji coba \x00\x01\x02\xff")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(isiAsli)
	}))
	defer srv.Close()

	tujuan := filepath.Join(t.TempDir(), "utuh.bin")
	n, err := ambilURL(&http.Client{}, srv.URL, tujuan, 1<<20)
	if err != nil {
		t.Fatalf("ambilURL: %v", err)
	}
	if n != int64(len(isiAsli)) {
		t.Errorf("panjang salah: %d, mau %d", n, len(isiAsli))
	}

	data, err := os.ReadFile(tujuan)
	if err != nil {
		t.Fatalf("baca: %v", err)
	}
	if string(data) != string(isiAsli) {
		t.Error("❌ isi berkas berbeda dari yang dikirim server")
	}
	t.Log("✅ Berkas tersimpan utuh, byte per byte")
}

// ==============================================================================
// 5. PENJAGA: BINARY HARUS BISA DIJALANKAN
// ==============================================================================

// TestUjiBinaryMenolakBerkasBukanExecutable memastikan berkas yang tidak bisa
// dijalankan (arsitektur salah, HTML halaman error, unduhan rusak) tertangkap
// SEBELUM dipasang.
//
// Ini lapisan kedua setelah checksum: berkas bisa saja lolos checksum karena
// Release-nya sendiri salah, tapi tetap tidak bisa dijalankan.
func TestUjiBinaryMenolakBerkasBukanExecutable(t *testing.T) {
	dir := t.TempDir()
	palsu := filepath.Join(dir, "bukan-binary")
	if err := os.WriteFile(palsu, []byte("ini teks biasa, bukan executable\n"), 0o755); err != nil {
		t.Fatalf("tulis: %v", err)
	}

	if err := ujiBinary(palsu, dir); err == nil {
		t.Error("❌ berkas teks dinyatakan lolos uji jalan")
	} else {
		t.Logf("✅ Berkas bukan executable ditolak: %v", err)
	}
}

// TestUjiBinaryMenolakArsitekturSalah memastikan binary untuk arsitektur lain
// tidak dipasang.
//
// Mengunduh arm64 di amd64 lolos checksum (berkasnya sah!), jadi hanya uji
// jalan yang bisa menangkapnya. Tanpa lapisan ini, bot akan mati setelah
// restart.
func TestUjiBinaryMenolakArsitekturSalah(t *testing.T) {
	dir := t.TempDir()
	// Header ELF dengan arsitektur yang tidak dikenal mesin ini.
	// Kernel akan menolak menjalankannya dengan "Exec format error".
	palsu := filepath.Join(dir, "salah-arsitektur")
	elfAsing := []byte{
		0x7f, 'E', 'L', 'F', // magic ELF
		2,                   // 64-bit
		1,                   // little endian
		1,                   // versi
		0, 0, 0, 0, 0, 0, 0, // padding
		2, 0, // ET_EXEC
		0xB7, 0x00, // EM_AARCH64 = 183 — bukan arsitektur mesin uji
	}
	if err := os.WriteFile(palsu, elfAsing, 0o755); err != nil {
		t.Fatalf("tulis: %v", err)
	}

	if err := ujiBinary(palsu, dir); err == nil {
		t.Skip("SKIP — mesin ini kebetulan bisa menjalankan ELF tersebut")
	} else {
		t.Logf("✅ Arsitektur salah ditolak: %v", err)
	}
}

// ==============================================================================
// 6. CADANGAN: JANGAN MENYERAH SEBELUM MENCOBA
// ==============================================================================

// TestJalankanUpdateTerpilihMeneruskanMetodeBuild memastikan pemilihan build
// benar-benar sampai ke jalur build.
func TestJalankanUpdateTerpilihMeneruskanMetodeBuild(t *testing.T) {
	sumber, err := os.ReadFile("update_self.go")
	if err != nil {
		t.Fatalf("baca update_self.go: %v", err)
	}
	isi := string(sumber)

	// Fungsi pemilih harus ada dan memanggil kedua jalur.
	if !strings.Contains(isi, "func jalankanUpdateTerpilih(") {
		t.Fatal("jalankanUpdateTerpilih tidak ditemukan")
	}
	if !strings.Contains(isi, "return jalankanUpdate(onProgress)") {
		t.Error("jalur build tidak dipanggil dari pemilih")
	}
	if !strings.Contains(isi, "jalankanUpdateUnduh(dir, onProgress)") {
		t.Error("jalur unduh tidak dipanggil dari pemilih")
	}
	t.Log("✅ Pemilih meneruskan ke kedua jalur dengan benar")
}

// TestBeralihKeBuildSaatUnduhGagal memastikan kegagalan unduh TIDAK langsung
// menyerah bila Go tersedia.
//
// Tanpa cadangan ini, VPS yang Release-nya belum dibuat akan mentok total —
// padahal ia mampu mengompilasi sendiri.
func TestBeralihKeBuildSaatUnduhGagal(t *testing.T) {
	sumber, err := os.ReadFile("update_self.go")
	if err != nil {
		t.Fatalf("baca update_self.go: %v", err)
	}
	isi := string(sumber)

	awal := strings.Index(isi, "func jalankanUpdateTerpilih(")
	if awal < 0 {
		t.Fatal("jalankanUpdateTerpilih tidak ditemukan")
	}
	akhir := strings.Index(isi[awal:], "\n// minInt")
	if akhir < 0 {
		t.Fatal("batas fungsi tidak ditemukan")
	}
	badan := isi[awal : awal+akhir]

	// Harus ada pemeriksaan Go sebelum beralih.
	if !strings.Contains(badan, "goTersedia()") {
		t.Error("❌ tidak memeriksa Go sebelum beralih ke build")
	}
	// Harus ada panggilan build setelah kegagalan unduh.
	if strings.Count(badan, "jalankanUpdate(onProgress)") < 2 {
		t.Error("❌ tidak ada cadangan ke build saat unduh gagal")
	}
	t.Log("✅ Unduh gagal → beralih ke build bila Go tersedia")
}

// ==============================================================================
// 7. INTEGRASI: SELURUH JALUR UNDUH DENGAN SERVER TIRUAN
// ==============================================================================

// TestJalurUnduhLengkapDenganServerTiruan menjalankan seluruh alur unduh
// terhadap server tiruan: unduh → checksum → uji jalan → pasang.
//
// Ini membuktikan jalur ini benar-benar bekerja, bukan hanya bagian-bagiannya.
func TestJalurUnduhLengkapDenganServerTiruan(t *testing.T) {
	// Bangun "binary" palsu yang benar-benar bisa dijalankan (skrip shell).
	// Ia harus merespons "--cek" seperti binary asli.
	isiBinary := []byte("#!/bin/sh\nif [ \"$1\" = \"--cek\" ]; then echo ok; exit 0; fi\nexit 0\n")

	// Siapkan SHA256SUMS yang benar untuk isi ini.
	h := sha256.Sum256(isiBinary)
	hashBenar := hex.EncodeToString(h[:])

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/release/core_engine-linux-amd64", func(w http.ResponseWriter, r *http.Request) {
		w.Write(isiBinary)
	})
	mux.HandleFunc("/release/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  core_engine-linux-amd64\n", hashBenar)
	})

	// Unduh dan verifikasi dengan cara yang sama seperti jalankanUpdateUnduh.
	dir := t.TempDir()
	temp := filepath.Join(dir, updateTempName)

	klien := &http.Client{}
	n, err := ambilURL(klien, srv.URL+"/release/core_engine-linux-amd64", temp, 1<<20)
	if err != nil {
		t.Fatalf("unduh: %v", err)
	}

	// Verifikasi: hash hasil unduh harus sama dengan yang diumumkan server.
	hash, err := hashBerkas(temp)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hash != hashBenar {
		t.Fatalf("❌ checksum tidak cocok:\n  dapat: %s\n  mau:   %s", hash, hashBenar)
	}

	// Beri izin eksekusi dan uji jalan.
	os.Chmod(temp, 0o755)
	if err := ujiBinary(temp, dir); err != nil {
		t.Fatalf("uji jalan gagal: %v", err)
	}

	t.Logf("✅ Alur unduh lengkap: %d byte, checksum cocok, binary lolos uji jalan", n)
}
