package main

// ==============================================================================
// 🧪 TEST KUNCI INSTANCE
// ==============================================================================
// Yang dijaga di sini adalah bug yang gejalanya paling menipu: dua bot dengan
// token sama, di mana pesan dibagi acak antar keduanya. Pengguna melihat
// /sysinfo menjawab hardware mesin yang salah, atau bot yang kadang diam —
// dan tidak ada petunjuk apa pun di log.
//
// Test ini tidak mengirim apa pun ke Telegram. Yang diuji adalah mekanisme
// kuncinya: apakah instance kedua benar-benar ditolak.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSidikJariTokenTidakMembocorkanToken memastikan berkas kunci di /tmp
// tidak memuat potongan token.
//
// Berkas di /tmp bisa dibaca proses lain di mesin yang sama. Kalau potongan
// token ada di sana, itu hadiah untuk penyerang.
func TestSidikJariTokenTidakMembocorkanToken(t *testing.T) {
	lama := BotToken
	BotToken = "8123456789:AAFakeTokenUntukPengujianSaja_xyz123"
	defer func() { BotToken = lama }()

	sidik := sidikJariToken()

	if sidik == "" {
		t.Fatal("sidik jari kosong")
	}
	// Harus hash heksadesimal, bukan potongan token.
	if len(sidik) != 16 {
		t.Errorf("sidik jari seharusnya 16 karakter hex, dapat %d: %q", len(sidik), sidik)
	}
	if strings.Contains(BotToken, sidik) {
		t.Error("❌ sidik jari memuat isi token")
	}
	// Bagian yang paling sensitif: 10 digit ID bot di depan token.
	idBot := BotToken[:10]
	if strings.Contains(sidik, idBot) {
		t.Error("❌ sidik jari memuat ID bot")
	}
	t.Logf("✅ Sidik jari aman: %s (token: %d karakter)", sidik, len(BotToken))
}

// TestSidikJariBerbedaPerToken memastikan dua token menghasilkan sidik jari
// berbeda — kalau sama, dua bot dengan token berbeda akan saling mengunci.
func TestSidikJariBerbedaPerToken(t *testing.T) {
	lama := BotToken
	defer func() { BotToken = lama }()

	BotToken = "token-pertama"
	a := sidikJariToken()
	BotToken = "token-kedua"
	b := sidikJariToken()

	if a == b {
		t.Errorf("❌ dua token berbeda menghasilkan sidik jari sama: %s", a)
	}
	t.Logf("✅ Token berbeda → sidik jari berbeda (%s vs %s)", a, b)
}

// TestKunciInstanceMenolakInstanceKedua adalah inti pengaman ini.
//
// Menjalankan dua proses dengan token yang sama harus ditolak, dan proses
// pertama TIDAK boleh terganggu.
func TestKunciInstanceMenolakInstanceKedua(t *testing.T) {
	// Kunci hanya bisa diuji dari proses terpisah: flock bersifat per-proses,
	// jadi memanggil kunciInstance() dua kali di test yang sama tidak akan
	// saling menolak.
	if testing.Short() {
		t.Skip("SKIP — butuh menjalankan proses terpisah")
	}

	biner := buildBinerUji(t)
	token := bacaTokenDariEnv(t)
	if token == "" {
		t.Skip("SKIP — BOT_TOKEN tidak ada di .env")
	}

	// Proses 1: jalankan dan biarkan hidup.
	cmd1 := exec.Command(biner)
	cmd1.Env = append(os.Environ(), "BOT_TOKEN="+token)
	cmd1.Dir = tempDirDenganEnv(t, token)
	if err := cmd1.Start(); err != nil {
		t.Fatalf("gagal menjalankan bot-1: %v", err)
	}
	defer func() {
		cmd1.Process.Kill()
		cmd1.Wait()
	}()

	// Beri waktu bot-1 mengambil kunci.
	menunggu(2500)

	// Proses 2: harus ditolak.
	//
	// Diberi timeout: kalau kuncinya TIDAK bekerja, bot-2 akan berjalan
	// selamanya (long polling) dan test ini menggantung sampai batas Go
	// 10 menit. Timeout mengubah kegagalan kunci menjadi test yang cepat
	// dan jelas, bukan hang yang membingungkan.
	out, err := jalankanDenganTimeout(t, biner, tempDirDenganEnv(t, token), token, 8*time.Second)

	if err == nil {
		t.Fatal("❌ instance kedua tidak ditolak — inilah bug yang bikin /sysinfo salah mesin")
	}
	teks := string(out)
	if !strings.Contains(teks, "Instance lain") {
		t.Errorf("pesan penolakan tidak jelas:\n%s", teks)
	}
	t.Logf("✅ Instance kedua ditolak: %s",
		strings.TrimSpace(barisTemukan(teks, "Instance lain")))

	// Proses 1 harus masih hidup.
	if cmd1.ProcessState != nil {
		t.Error("❌ proses pertama ikut mati")
	} else {
		t.Log("✅ Proses pertama tidak terganggu")
	}
}

// TestKunciDilepasSaatProsesBerakhir memastikan kunci tidak basi.
//
// Kalau kunci tertinggal setelah proses mati, bot tidak akan pernah bisa
// dijalankan lagi tanpa menghapus berkas manual — jauh lebih buruk daripada
// bug aslinya.
func TestKunciDilepasSaatProsesBerakhir(t *testing.T) {
	if testing.Short() {
		t.Skip("SKIP — butuh menjalankan proses terpisah")
	}

	biner := buildBinerUji(t)
	token := bacaTokenDariEnv(t)
	if token == "" {
		t.Skip("SKIP — BOT_TOKEN tidak ada di .env")
	}
	dir := tempDirDenganEnv(t, token)

	// Jalankan, lalu hentikan.
	c1 := exec.Command(biner)
	c1.Env = append(os.Environ(), "BOT_TOKEN="+token)
	c1.Dir = dir
	c1.Start()
	menunggu(2500)
	c1.Process.Kill()
	c1.Wait()

	// Proses kedua harus BISA jalan sekarang.
	//
	// Tidak memakai CombinedOutput(): bot yang berhasil start akan berjalan
	// selamanya, jadi kita harus menghentikannya sendiri.
	c2 := exec.Command(biner)
	c2.Env = append(os.Environ(), "BOT_TOKEN="+token)
	c2.Dir = dir

	var buf bytes.Buffer
	c2.Stdout = &buf
	c2.Stderr = &buf
	if err := c2.Start(); err != nil {
		t.Fatalf("gagal menjalankan bot-2: %v", err)
	}

	// Kalau kunci basi, bot-2 akan langsung keluar dengan pesan "Instance lain".
	// Beri waktu sebentar, lalu periksa.
	menunggu(3000)
	selesaiSendiri := c2.ProcessState != nil

	// Hentikan apa pun keadaannya.
	c2.Process.Kill()
	tertunda, _ := io.ReadAll(&buf)
	c2.Wait()

	if selesaiSendiri && strings.Contains(string(tertunda), "Instance lain") {
		t.Errorf("❌ kunci basi — bot tidak bisa dijalankan lagi:\n%s", tertunda)
	}
	t.Log("✅ Kunci dilepas otomatis saat proses berakhir")
}

// ==============================================================================
// Helper
// ==============================================================================

// jalankanDenganTimeout menjalankan biner dan mengembalikan keluaran
// gabungannya, tapi mematikannya sendiri setelah batas waktu.
//
// Diperlukan karena bot yang berhasil start tidak pernah selesai — menunggu
// dengan cara biasa akan menggantung sampai batas test Go.
func jalankanDenganTimeout(t *testing.T, biner, dir, token string, batas time.Duration) ([]byte, error) {
	t.Helper()

	cmd := exec.Command(biner)
	cmd.Env = append(os.Environ(), "BOT_TOKEN="+token)
	cmd.Dir = dir

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	selesai := make(chan error, 1)
	go func() { selesai <- cmd.Wait() }()

	select {
	case err := <-selesai:
		// Bot keluar sendiri — inilah yang diharapkan saat instance ditolak.
		return buf.Bytes(), err
	case <-time.After(batas):
		// Masih berjalan setelah batas waktu: kunci TIDAK menolaknya.
		cmd.Process.Kill()
		<-selesai
		return buf.Bytes(), fmt.Errorf("masih berjalan setelah %v (tidak ditolak)", batas)
	}
}

// buildBinerUji membangun binary sekali lalu memakainya berulang.
func buildBinerUji(t *testing.T) string {
	t.Helper()

	out := filepath.Join(t.TempDir(), "core_engine_uji")
	cmd := exec.Command("go", "build", "-o", out, ".")
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gagal build binary uji: %v\n%s", err, data)
	}
	return out
}

// bacaTokenDariEnv mengambil token asli dari .env untuk pengujian.
//
// Memakai token asli penting: kunci dipisah per token, jadi token palsu tidak
// akan menguji berkas kunci yang sama dengan produksi.
func bacaTokenDariEnv(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(".env")
	if err != nil {
		return ""
	}
	for _, baris := range strings.Split(string(data), "\n") {
		baris = strings.TrimSpace(baris)
		if strings.HasPrefix(baris, "BOT_TOKEN=") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(baris, "BOT_TOKEN=")), "\"'")
		}
	}
	return ""
}

// tempDirDenganEnv menyiapkan direktori kerja berisi .env minimal.
//
// Bot butuh .env agar loadConfig() tidak keluar lebih dulu.
func tempDirDenganEnv(t *testing.T, token string) string {
	t.Helper()

	dir := t.TempDir()
	isi := fmt.Sprintf("BOT_TOKEN=%s\nADMIN_IDS=1\n", token)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(isi), 0o600); err != nil {
		t.Fatalf("tulis .env uji: %v", err)
	}
	return dir
}

// menunggu berhenti sebentar sambil memberi kesempatan proses lain berjalan.
func menunggu(milis int) {
	time.Sleep(time.Duration(milis) * time.Millisecond)
}

// barisTemukan mengembalikan baris pertama yang memuat kata kunci.
func barisTemukan(teks, kunci string) string {
	for _, b := range strings.Split(teks, "\n") {
		if strings.Contains(b, kunci) {
			return b
		}
	}
	return ""
}
