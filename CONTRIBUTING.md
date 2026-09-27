# Panduan Kontribusi

Terima kasih sudah tertarik berkontribusi. Bot ini mengendalikan VPS dengan
akses penuh, jadi kualitas dan keamanan dijaga ketat.

## Sebelum mulai

```bash
git clone https://github.com/arewedaks/bot-vps-control.git
cd bot-vps-control

cp .env.example .env      # isi BOT_TOKEN dan ADMIN_IDS untuk uji manual
make check                # format + vet + test
```

## Prinsip project

### 1. Nol dependency eksternal

Ini **fitur utama**, bukan kebetulan. `go.mod` hanya boleh berisi nama modul dan
versi Go. Alasannya praktis: siapa pun bisa meng-clone lalu langsung
`go build` di VPS apa pun tanpa `go mod download`, tanpa proxy, tanpa jaringan.

Kalau kamu butuh sesuatu, cek dulu apakah standard library bisa melakukannya.
Contoh nyata: terminal PTY di project ini dibuat memakai `syscall` langsung,
bukan `github.com/creack/pty` — sekitar 40 baris dan tidak perlu dirawat.

```bash
make check-deps      # akan menolak bila ada dependency masuk
```

### 2. Satu fitur, satu awalan

Semua `.go` berada di root karena Go menganggap satu folder sebagai satu
package._bot-vps-control_ adalah satu binary, jadi memindahkannya ke subfolder
akan menjadikannya package terpisah dan memaksa mengekspor puluhan simbol
internal. Sebagai gantinya, tiap file diberi awalan fitur sehingga urutannya
mengelompok dengan sendirinya di GitHub dan di listing direktori.

| Awalan | Fitur | Isi |
| :--- | :--- | :--- |
| `core_` | Inti | Loop utama, routing perintah, panel, lock instance, konfigurasi |
| `term_` | Terminal | PTY, shell persisten, teks `/help` dan `/commands` |
| `deploy_` | Deploy Bot | Deteksi bahasa, unduhan, git, panel, alur deploy |
| `upload_` | Upload | Upload ≤20MB dan upload >20MB/berbagi |
| `update_` | Update | Pembaruan mandiri, unduhan rilis, varian kontainer |
| `ts_` | Tailscale | Menu VPN |
| `uji_` | Test manual | Hanya jalan bila variabel lingkungan disetel |

File test memakai awalan yang sama, diakhiri `_test.go` dengan nama berkorespondensi:
`deploy_core.go` → `deploy_core_test.go`.

Tugas test manual diberi awalan `uji_` (bukan `zz_`) supaya tetap menggolong
dengan benar, sekaligus tidak menyamar sebagai test biasa yang harus hijau.

### 3. Setiap fitur wajib punya test

Project ini memegang standar itu: 117 test untuk ~5.800 baris kode.

Test yang **benar-benar menguji**, bukan sekadar menambah coverage:

- ❌ `TestFungsiAda` yang hanya memanggil fungsi
- ✅ `TestPecahDanGabungFile` yang memverifikasi **sha256 identik** setelah siklus penuh

Detail penting: **pastikan testmu bisa gagal.** Sisipkan bug sengaja, jalankan,
lihat apakah test menangkapnya. Test yang tidak pernah bisa gagal tidak berguna.

### 4. Keamanan bukan tambahan

Bot ini punya akses destruktif. Perubahan yang menyentuh area berikut wajib
disertai test keamanan:

| Area | Yang diuji |
| :--- | :--- |
| Nama file dari Telegram | `TestSafeUploadPathMenolakTraversal` |
| URL ke shell | `TestShellQuoteBenarBenarMencegahEksekusi` |
| Callback tombol | `TestSetiapTombolHelpPunyaHandler` |
| Kredensial di source | `TestRepoBersihDariKredensial` |

## Alur kontribusi

```bash
git checkout -b fitur/nama-singkat
# ... kerjakan ...
make check                 # wajib lulus
git commit -m "feat: deskripsi singkat"
git push origin fitur/nama-singkat
```

Lalu buka Pull Request.

### Format commit

```
feat: fitur baru
fix: perbaikan bug
docs: dokumentasi
test: penambahan/perbaikan test
refactor: perubahan struktur tanpa mengubah perilaku
```

## Yang tidak diterima

- Dependency eksternal baru
- Kode yang mengirim pesan baru tiap perintah (pakai panel, lihat `updatePanel`)
- Operasi destruktif tanpa konfirmasi
- `.env`, token, atau User ID di dalam commit
- Test yang tidak bisa gagal

## Pertanyaan

Buka [Discussion](../../discussions) atau issue dengan label `question`.
