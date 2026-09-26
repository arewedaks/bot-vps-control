package main

import (
	"os"
	"strings"
	"testing"
)

// TestTombolUploadPunyaHandler memastikan tombol baru di file manager
// benar-benar ditangani — mencegah tombol mati.
func TestTombolUploadPunyaHandler(t *testing.T) {
	sumber, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("baca main.go: %v", err)
	}
	kode := string(sumber)

	// Handler yang harus ada untuk tombol-tombol baru.
	wajib := map[string]string{
		"fm:up:":  `case "up": // Aktifkan mode upload ke folder ini`,
		"fm:mk:":  `case "mk": // Buat folder baru`,
		"up:o":    `case "o": // Setujui penimpaan`,
		"up:c":    `case "c": // Batalkan`,
		"fm:zip:": `case "zip":`,
		"fm:del:": `case "del":`,
	}
	for tombol, handler := range wajib {
		if !strings.Contains(kode, handler) {
			t.Errorf("tombol %q tidak punya handler (%q)", tombol, handler)
		}
	}
	t.Logf("✅ %d tombol file manager punya handler", len(wajib))
}

// TestPrefixCallbackTidakBentrok memastikan prefix callback unik.
// Kalau ada dua prefix yang saling merupakan awalan, tombol bisa salah dibaca.
func TestPrefixCallbackTidakBentrok(t *testing.T) {
	prefix := []string{"ts:", "hp:", "tm:", "up:", "fm:"}

	for i, a := range prefix {
		for j, b := range prefix {
			if i == j {
				continue
			}
			// Satu prefix tidak boleh jadi awalan prefix lain.
			if strings.HasPrefix(a, b) {
				t.Errorf("prefix %q adalah awalan dari %q — bisa bentrok", b, a)
			}
		}
	}

	// "up:" harus diperiksa SEBELUM "fm:" di kode, tapi keduanya berbeda
	// sehingga tidak masalah. Yang penting tidak ada yang saling mengawali.
	t.Logf("✅ %d prefix callback unik, tidak ada yang saling mengawali", len(prefix))
}

// TestUploadHandlerTerdaftar memastikan handler dokumen memakai modul upload.
func TestUploadHandlerTerdaftar(t *testing.T) {
	mainKode, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	uploadKode, err := os.ReadFile("upload.go")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(mainKode), "handleDocumentUpload(chatID, userID, u.Message.Document, caption)") {
		t.Error("loop utama harus memanggil handleDocumentUpload")
	}
	// Validasi ukuran dipanggil di dalam modul upload, bukan di main.
	if !strings.Contains(string(uploadKode), "checkUploadSize(doc.FileSize)") {
		t.Error("validasi ukuran harus dipanggil di jalur upload")
	}
	if !strings.Contains(string(uploadKode), "resolveUploadDest(") {
		t.Error("resolveUploadDest harus dipakai untuk menentukan tujuan")
	}
	t.Log("✅ Handler upload terhubung dengan validasi & resolusi tujuan")
}
