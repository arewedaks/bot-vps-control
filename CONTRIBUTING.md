# Panduan Kontribusi

Terima kasih sudah tertarik berkontribusi. Bot ini mengendalikan VPS dengan
akses penuh, jadi kualitas dan keamanan dijaga ketat.

## Sebelum mulai

> Repository ini **privat**. Kamu perlu diberi akses oleh pemilik repo.

```bash
# Clone — GitHub CLI menangani autentikasi repo privat secara otomatis
gh repo clone arewedaks/bot-vps-control
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

### 2. Satu fitur, satu file

| File | Tanggung jawab |
| :--- | :--- |
| `main.go` | Loop utama, handler Telegram, UI file manager |
| `terminal.go` | Terminal PTY, panel, bantuan |
| `tailscale.go` | Menu Tailscale |
| `upload.go` | Upload file (≤20MB) |
| `uploadbesar.go` | Upload >20MB, unduh URL, chunk |

### 3. Setiap fitur wajib punya test

Project ini memegang standar itu: 110 test untuk ~5.800 baris kode.

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
