package main

// ==============================================================================
// 🔒 KUNCI INSTANCE — CEGAH DUA BOT BERBUT PESAN
// ==============================================================================
// Telegram long polling hanya memberikan satu update ke satu pemanggil
// getUpdates. Bila dua proses memakai token yang sama, pesan dibagi acak:
// sebagian mendarat di VPS, sebagian di komputer lain.
//
// Gejalanya menipu sekali dan sulit dilacak dari log, karena kedua bot
// sama-sama tampak sehat. Yang paling khas:
//
//   • /sysinfo menjawab spesifikasi mesin yang salah — ini yang paling sering
//     membuat orang bingung, karena bot "deploy di VPS" tapi melaporkan
//     hardware laptop.
//   • Perintah kadang dijawab, kadang diabaikan tanpa pola yang jelas.
//   • Notifikasi startup terkirim berkali-kali.
//
// Kunci ini menolak start kedua secara tegas, jadi kegagalannya kelihatan
// langsung alih-alih menjadi teka-teki.

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// kunciInstance memastikan hanya satu bot berjalan per token.
//
// Memakai flock(2) pada berkas kunci. Berkasnya boleh tetap ada — yang
// menentukan adalah kunci kernel-nya, dan kunci itu otomatis lepas saat
// proses berakhir. Jadi bot yang ter-crash tidak meninggalkan kunci basi.
//
// Berkas kunci diletakkan di /tmp, bukan di direktori program: direktori
// program bisa read-only, dan dua instalasi berbeda pada satu mesin tetap
// harus saling terdeteksi.
//
// Dipisah per token: menjalankan dua bot dengan token BERBEDA di satu mesin
// tetap boleh.
func kunciInstance() error {
	tokenSidikJari := sidikJariToken()
	path := filepath.Join(os.TempDir(), "bot-vps-control-"+tokenSidikJari+".lock")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		// Tidak bisa membuat berkas kunci bukan alasan untuk menolak jalan —
		// itu bisa mematikan bot hanya karena /tmp bermasalah. Lebih baik
		// jalan tanpa pengaman daripada tidak jalan sama sekali.
		fmt.Printf("⚠️  Tidak bisa membuat kunci instance: %v\n", err)
		fmt.Println("   Melanjutkan tanpa pengaman anti-instance-ganda.")
		return nil
	}

	// LOCK_EX = kunci eksklusif. LOCK_NB = jangan menunggu; langsung gagal
	// bila sudah dikunci. Inilah yang membuat deteksinya seketika.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// Baca PID pemegang kunci supaya pesannya benar-benar berguna.
		isi, _ := os.ReadFile(path)
		f.Close()

		pesan := "Instance lain sudah memakai token bot ini"
		if len(isi) > 0 {
			pesan += " (PID " + trimSpasi(string(isi)) + ")"
		}
		return fmt.Errorf("%s.", pesan)
	}

	// Tulis PID untuk ditampilkan bila ada yang mencoba start kedua.
	// Berkas ini tidak perlu di-truncate dulu: panjangnya tetap, dan isi lama
	// tidak berbahaya karena kunci kernel yang menentukan siapa pemiliknya.
	f.Truncate(0)
	f.Seek(0, 0)
	fmt.Fprintf(f, "%d", os.Getpid())

	// Berkas sengaja TIDAK ditutup: menutupnya akan melepas kunci. Ia hidup
	// selama proses berjalan, lalu dilepas kernel saat proses berakhir.
	return nil
}

// sidikJariToken menghasilkan pengenal pendek dari token.
//
// Memakai hash, BUKAN potongan token: berkas kunci di /tmp bisa dibaca proses
// lain di mesin yang sama, dan potongan token di sana akan berguna untuk
// penyerang. Hash hanya perlu unik, bukan bisa dibaca manusia.
func sidikJariToken() string {
	// FNV-1a: bukan untuk keamanan, hanya untuk penyebaran. Token asli tidak
	// bisa dipulihkan dari hash 64-bit ini.
	var h uint64 = 14695981039346656037
	for i := 0; i < len(BotToken); i++ {
		h ^= uint64(BotToken[i])
		h *= 1099511628211
	}
	return fmt.Sprintf("%016x", h)
}

// trimSpasi membuang spasi dan baris baru di ujung.
func trimSpasi(s string) string {
	awal, akhir := 0, len(s)
	for awal < akhir && (s[awal] == ' ' || s[awal] == '\n' || s[awal] == '\r' || s[awal] == '\t') {
		awal++
	}
	for akhir > awal && (s[akhir-1] == ' ' || s[akhir-1] == '\n' || s[akhir-1] == '\r' || s[akhir-1] == '\t') {
		akhir--
	}
	return s[awal:akhir]
}
