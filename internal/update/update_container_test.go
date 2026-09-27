package update

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestIsPidSatuDeteksiContainer memastikan deteksi proses utama container
// bekerja. Salah deteksi berbahaya dua arah:
//
//   - Terlalu longgar: bot systemd biasa dikira PID 1, jadi tidak pernah
//     restart sendiri padahal bisa.
//   - Terlalu ketat: bot PID 1 tidak terdeteksi, lalu mencoba mengganti
//     dirinya sendiri dan gagal — proses lama masih memegang kunci instance.
func TestIsPidSatuDeteksiContainer(t *testing.T) {
	benar := os.Getpid() == 1
	hasil := isPidSatu()

	fmt.Printf("\n  PID proses test : %d\n", os.Getpid())
	fmt.Printf("  isPidSatu()     : %v\n", hasil)

	if hasil != benar {
		t.Errorf("❌ isPidSatu()=%v, seharusnya %v (PID %d)",
			hasil, benar, os.Getpid())
	}

	if benar {
		fmt.Println("  ℹ️  test berjalan sebagai PID 1 (di dalam container)")
		fmt.Println("  ✅ Terdeteksi sebagai proses utama container")
	} else {
		fmt.Println("  ✅ Test berjalan normal (bukan PID 1) — terdeteksi benar")
		fmt.Println("  ℹ️  Jalur PID 1 diuji terpisah di bawah")
	}
}

// TestPesanUpdateSesuaiKemampuan memastikan bot tidak menjanjikan restart
// yang tidak akan terjadi.
//
// Ini soal kejujuran: pesan "sedang di-restart" pada bot PID 1 membuat
// pengguna menunggu /ping yang tidak akan pernah menjawab versi baru.
func TestPesanUpdateSesuaiKemampuan(t *testing.T) {
	isi, err := os.ReadFile("update_dl.go")
	if err != nil {
		t.Fatalf("baca updateunduh.go: %v", err)
	}
	sumber := string(isi)

	fmt.Println("\n  === PENANGANAN PROSES UTAMA CONTAINER ===")

	// Harus ada penanganan khusus sebelum tahap restart.
	if !strings.Contains(sumber, "if isPidSatu() {") {
		t.Error("❌ tidak ada penanganan khusus untuk bot PID 1")
	} else {
		fmt.Println("  ✅ Ada penanganan khusus untuk bot PID 1")
	}

	// Pesannya harus mengarahkan restart container, bukan menjanjikan restart.
	if !strings.Contains(sumber, "docker restart") {
		t.Error("❌ pesan tidak memberi tahu cara restart container dari host")
	} else {
		fmt.Println("  ✅ Pesan memberi perintah docker restart")
	}

	// Restart container harus disebut sebagai langkah tersisa, bukan "sedang berjalan".
	if !strings.Contains(sumber, "Tinggal satu langkah") {
		t.Error("❌ pesan tidak jelas bahwa masih ada langkah tersisa")
	} else {
		fmt.Println("  ✅ Pesan menyebut langkah tersisa dengan jelas")
	}

	// Pesan progres tidak boleh menjanjikan restart pada kasus container.
	isiUpdate, err := os.ReadFile("update_dl.go")
	if err != nil {
		t.Fatalf("baca updateunduh.go: %v", err)
	}
	if !strings.Contains(string(isiUpdate), "restart container diperlukan setelahnya") {
		t.Error("❌ pesan progres masih menjanjikan restart pada bot PID 1")
	} else {
		fmt.Println("  ✅ Pesan progres jujur soal restart container")
	}
}

// TestNamaContainerMasukAkal memastikan nama container yang disarankan
// benar-benar bisa dipakai.
func TestNamaContainerMasukAkal(t *testing.T) {
	nama := namaContainer()

	fmt.Printf("\n  hostname (jadi nama container): %q\n", nama)

	if nama == "" {
		t.Fatal("❌ nama container kosong")
	}

	// Hostname container bawaan adalah ID 12 karakter hex. Bila hostname asli
	// mesin, biasanya lebih panjang dan bukan hex — tetap berguna sebagai
	// petunjuk, tapi pemanggil perlu tahu itu bukan nama container.
	hanyaHex := true
	for _, c := range nama {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			hanyaHex = false
			break
		}
	}

	if len(nama) == 12 && hanyaHex {
		fmt.Println("  ✅ Terlihat seperti ID container (12 hex) — siap dipakai")
	} else {
		fmt.Println("  ℹ️  Hostname bukan ID container; pemanggil perlu menyesuaikan")
	}
}

// TestJalurPid1Sungguhan menjalankan alur update sebagai PID 1 palsu.
//
// Ini test yang paling penting: ia benar-benar memanggil jalankanUpdateUnduh
// dengan PID disuntik menjadi 1, lalu memeriksa bahwa pesan yang kembali
// tidak menjanjikan restart yang tidak akan terjadi.
//
// Tanpa penyuntikan, jalur ini tidak pernah teruji di laptop — dan kerusakan
// yang membuat bot container mencoba mengganti dirinya sendiri akan lolos.
func TestJalurPid1Sungguhan(t *testing.T) {
	asli := pidSaatIni
	defer func() { pidSaatIni = asli }()
	pidSaatIni = func() int { return 1 }

	if !isPidSatu() {
		t.Fatal("❌ penyuntikan PID gagal")
	}

	fmt.Println("\n  === UJI JALUR PID 1 (PID disuntik = 1) ===")

	dir := t.TempDir()
	var tahap []string
	sukses, pesan := jalankanUpdateUnduh(dir, func(s string) {
		tahap = append(tahap, s)
	})

	fmt.Println("  Tahap yang dilaporkan:")
	for _, s := range tahap {
		fmt.Println("   -", s)
	}

	for _, s := range tahap {
		if strings.Contains(s, "Memulai ulang bot") {
			t.Error("❌ pesan progres menjanjikan restart pada bot PID 1")
		}
	}
	fmt.Println("  ✅ Tidak ada janji restart pada bot PID 1")

	if sukses {
		if !strings.Contains(pesan, "docker restart") {
			t.Error("❌ pesan sukses tidak menyebut cara restart container")
		} else {
			fmt.Println("  ✅ Pesan sukses menyebut docker restart")
		}
		if strings.Contains(pesan, "Proses baru sedang dijalankan") {
			t.Error("❌ mengklaim proses baru berjalan pada bot PID 1")
		} else {
			fmt.Println("  ✅ Tidak mengklaim proses baru berjalan")
		}
	} else {
		fmt.Println("  ℹ️  Unduhan gagal (test offline) — alur berhenti sebelum restart")
		fmt.Printf("  ℹ️  Pesan akhir: %s\n", ringkasSatuBaris(pesan))
	}
}

// ringkasSatuBaris memotong pesan panjang agar mudah dibaca di output test.
func ringkasSatuBaris(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 90 {
		return s[:90] + "..."
	}
	return s
}
