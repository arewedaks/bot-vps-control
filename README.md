# 🤖 Bot VPS Control

**Kendalikan VPS Linux kamu sepenuhnya lewat Telegram** — terminal interaktif,
file manager, monitoring, VPN, dan manajemen paket. Ditulis dalam Go murni
tanpa satu pun dependency eksternal.

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Dependencies](https://img.shields.io/badge/dependencies-0-brightgreen)](go.mod)
[![CI](https://github.com/arewedaks/bot-vps-control/actions/workflows/ci.yml/badge.svg)](https://github.com/arewedaks/bot-vps-control/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-linux-lightgrey)](#)

---

## ✨ Kenapa bot ini berbeda

| | |
| :--- | :--- |
| 🪶 **Nol dependency** | Hanya standard library Go. `go build` jalan di VPS mana pun, tanpa `go mod download`. Binary ~7MB, RAM ~15MB. |
| 🖥️ **Terminal sungguhan** | PTY asli via `syscall`. `cd`, `export`, dan history **menetap** antar perintah. `sudo`, `apt`, `mysql` berjalan normal. |
| 🪟 **Chat tidak menumpuk** | Satu pesan panel yang ditulis ulang, bukan pesan baru tiap perintah. |
| 🎛️ **Menu tombol** | Tailscale, file manager, dan bantuan semuanya berbasis tombol — tanpa hafal perintah. |
| 📦 **Upload tanpa batas 20MB** | Tiga cara melewati batas Bot API, termasuk unduh langsung dari URL. |
| 🧪 **117 test** | Termasuk test keamanan: injeksi shell, path traversal, dan proses yatim. |

---

## 🌟 Fitur

### 🖥️ Terminal Interaktif (fitur utama)
Shell **persisten** dengan TTY asli — bukan sekadar eksekusi perintah sekali jalan.

- `cd`, `export`, alias, dan history **menetap** antar perintah
- Program yang butuh terminal berjalan normal: `sudo`, `top`, `apt`, `mysql`
- Kirim `Ctrl+C` / `Ctrl+D` / `Ctrl+Z` ke proses yang berjalan
- **Mode input langsung**: kirim teks apa pun, masuk ke stdin shell
- Sesi auto-kill setelah 30 menit menganggur
- Bekerja di container/VPS tanpa `/dev/net/tun`

### 📦 Manajemen Paket (apt)
- `/term apt check` — cek lock & hak akses **sebelum** mencoba
- `/term apt update` / `upgrade` / `full` / `safe`
- **Prompt debconf tampil dan bisa dijawab** dari Telegram
- Tanpa timeout — aman untuk upgrade yang butuh 30 menit

### 🗂️ File Manager (tombol interaktif)
- Navigasi folder dengan tombol, pagination otomatis
- Ikon sesuai jenis file: 📦 arsip · 🔧 config · ⚙️ script · 🖼 gambar
- Nama panjang dipotong **di tengah** agar ekstensi selalu terlihat
- Penanda folder kosong, peringatan folder tidak bisa ditulis
- Zip & download folder, hapus dengan konfirmasi, preview file teks

### 📤 Upload & Unduh
- **Tombol upload**: pilih folder, kirim file, selesai
- **Konfirmasi timpa** — tidak ada data hilang diam-diam
- **Anti path traversal** — `../../etc/passwd` ditolak
- `/unduh <URL>` — unduh file besar langsung di VPS (tanpa batas 20MB)
- `/chunk` — upload bertahap untuk file >20MB
- `/pecah` & `/gabung` — pecah dan satukan file besar

### 🦎 Tailscale VPN (menu tombol)
- Install otomatis, tombol berubah sesuai keadaan
- Status ringkas: peer online/offline, bukan dump 30 baris
- Web Login — auth key tidak pernah masuk riwayat chat
- Auto-detect userspace networking untuk container

### 📊 Monitoring Server
- `/stats` — CPU, RAM, disk, uptime, load average
- `/top` — 10 proses terberat · `/net` — port terbuka
- `/sysinfo` — hardware, CPU, OS, kernel
- `/ip` — IP publik, lokal, dan Tailscale

### ⚙️ Lainnya
- `/exec` — perintah sekali jalan tanpa state
- `/reboot confirm` — restart VPS dengan konfirmasi
- Notifikasi startup dengan info akses SSH

---

## 🚀 Mulai Cepat

```bash
# 1. Clone
git clone https://github.com/arewedaks/bot-vps-control.git
cd bot-vps-control

# 2. Konfigurasi
cp .env.example .env
nano .env                    # isi BOT_TOKEN dan ADMIN_IDS

# 3. Build & jalankan
make build
./core_engine
```

Bot akan mengirim notifikasi ke chat kamu saat berhasil terhubung.

### Menjalankan sebagai service (systemd)

```bash
sudo cp bot-vps.service /etc/systemd/system/
sudo nano /etc/systemd/system/bot-vps.service   # sesuaikan WorkingDirectory
sudo systemctl enable --now bot-vps
sudo systemctl status bot-vps
```

### Menjalankan lewat Python (opsional)

`main.py` adalah launcher tipis yang otomatis build binary bila belum ada:

```bash
python3 main.py
```

---

## 🦎 Menu Tailscale

Ketik `/ts` untuk membuka menu tombol. Tidak perlu menghapal sub-perintah —
tombolnya berubah sesuai keadaan:

| Keadaan | Tombol yang muncul |
| :--- | :--- |
| **Belum install** | ⬇️ Install Tailscale |
| **Terinstall, terputus** | 🔗 Connect (Web Login) · 📋 Status Detail · 🔄 Refresh |
| **Terhubung** | 📋 Status Detail · 🔄 Refresh · 🔒 SSH On · 🔓 SSH Off · 🌐 IP & Hostname · 📥 Set Auth Key · 🛑 Disconnect |

### Tampilan status ringkas

Alih-alih dump `tailscale status` mentah (bisa 30+ baris), bot menampilkan:

```text
🦎 Tailscale
━━━━━━━━━━━━━━━━━━━━
🟢 Status: Terhubung
🌐 IP: 100.64.0.11
👥 Peer: 4 online, 3 offline
🖥 Host ini: vps-utama
```

Untuk detail lengkap: tombol **📋 Status Detail**, atau `/ts status raw` (file).

### Catatan keamanan

| Hal | Penjelasan |
| :--- | :--- |
| **Auth key** | Jangan kirim sebagai pesan — tersimpan di riwayat chat & log. Tombol 🔗 Connect memakai Web Login yang aman. Alternatif: `/term tailscale up --auth-key=...` |
| **`/ts down`** | Memutus akses remote ke VPS. Bot meminta konfirmasi + tombol Batal. |
| **SSH toggle** | Tombol 🔒/🔓 langsung mengubah `tailscale set --ssh`. |

---

## 🪟 Panel Tunggal (Chat Tidak Penuh)

Bot ini memakai **satu pesan panel** yang isinya ditulis ulang, bukan mengirim
pesan baru berulang kali. Chat tetap rapi walau kamu menjalankan puluhan perintah.

| Skenario | Sebelum | Sesudah |
| :--- | :--- | :--- |
| 6 perintah `/term` | 6 pesan | **1 panel** |
| `/term apt upgrade` | 3 pesan | **1 panel** |
| `/mkdir` | 2 pesan | **1 panel** |
| `/term info`, `/term mode on` | 1 pesan masing-masing | **edit panel** |

Cara kerjanya:
- **Output panjang** (terminal, file manager, hasil perintah) → selalu ke panel yang sama.
- **Pesan singkat** (error) → hapus panel lama, kirim sekali.
- **Notifikasi penting** (startup, reboot) → tetap pesan baru.

Tombol inline (`🔄 Refresh`, `🛑 Ctrl+C`, `📋 Info`, `💀 Tutup`) mengedit panel
yang sama, jadi menekan tombol berulang kali tidak menumpuk pesan.

---

## 📋 Daftar Perintah Telegram

### Yang muncul di `/help`

| Perintah | Deskripsi |
| :--- | :--- |
| **`/term`** | Buka **Terminal Interaktif (PTY)** — shell persisten dengan TTY asli. Fitur utama. |
| **`/term <perintah>`** | Jalankan perintah di dalam sesi terminal. |
| **`/term apt ...`** | Kelola paket: `check`, `update`, `upgrade`, `safe`, `full`, `list`, `clean`. |
| **`/term ^c`** | Batalkan proses yang berjalan (sesi tetap hidup). |
| **`/fm [path]`** | Buka **Interactive File Manager** (tombol navigasi, upload, zip, hapus). |
| **`/rm <path>`** | Hapus file / folder dengan konfirmasi. |
| **`/stats`** | Monitor Uptime, Load, RAM, Disk, CPU, OS. |
| **`/top`** | 10 proses dengan CPU/RAM tertinggi. |
| **`/sysinfo`** | Spesifikasi hardware, CPU, OS, hostname, user. |
| **`/net`** | Port jaringan yang sedang aktif (*listening ports*). |
| **`/ip`** | Public IP, Local IP, Tailscale IP, hostname, format SSH. |
| **`/ts`** | 🦎 Buka **menu Tailscale interaktif** (tombol). |
| **`/ts status`** | Status ringkas: peer online/offline, IP, host. |
| **`/ts status raw`** | Status lengkap dikirim sebagai file. |
| **`/ts install`** | Install Tailscale otomatis. |
| **`/ts up`** | Hubungkan via Web Login (tanpa menaruh auth key di chat). |
| **`/ts ip`** | IP Tailscale IPv4/IPv6 + hostname + format SSH. |
| **`/ts ssh on|off`** | Aktif/nonaktifkan Tailscale SSH. |
| **`/ts down confirm`** | Putuskan Tailscale — **butuh konfirmasi**. |
| **`/unduh <URL> [tujuan]`** | Unduh file dari internet langsung di VPS — tanpa batas 20MB. |
| **`/chunk mulai <nama> <n>`** | Mulai upload bertahap untuk file >20MB. |
| **`/chunk status`** | Cek kemajuan upload bertahap. |
| **`/chunk batal`** | Batalkan upload bertahap. |
| **`/pecah <file> [MB]`** | Pecah file besar jadi beberapa bagian. |
| **`/gabung <bagian> <hasil>`** | Gabungkan kembali bagian hasil `/pecah`. |
| **`/exec <perintah>`** | Perintah shell sekali jalan, tanpa state. |
| **`/ping`** | Cek bot hidup + waktu server. |
| **`/reboot confirm`** | Restart VPS. |
| **`/help`** | Bantuan ringkas dengan tombol akses cepat. |
| **`/commands`** | Daftar perintah lengkap termasuk alias. |

### Perintah lain (tersedia, tidak ditampilkan di `/help`)

Perintah berikut tetap berfungsi tapi tidak ditampilkan agar `/help` tetap ringkas:

| Perintah | Deskripsi |
| :--- | :--- |
| **`/ls`** | Alias `/fm`. |
| **`/cat <path>`** | Lihat isi file teks. Bisa juga lewat `/term cat`. |
| **`/mkdir <path>`** | Buat folder. Bisa juga lewat `/term mkdir`. |
| **`/getfile <path>`** | Unduh file ke chat. Di file manager sudah ada tombol ⬇️. |
| **`/status`**, **`/ps`**, **`/ports`** | Alias dari `/stats`, `/top`, `/net`. |

### Upload file dari Telegram ke VPS

Ada **dua cara**.

#### Cara 1 — Lewat tombol (lebih mudah)

```
/fm                     → jelajah ke folder tujuan
Tekan "📤 Upload ke Sini"  → bot mengingat folder itu
Kirim file apa pun       → tersimpan ke folder tersebut
```

Fitur pada mode ini:
- Bisa kirim **beberapa file berturut-turut** ke folder yang sama.
- Kirim **caption** untuk memberi nama file sendiri (folder tetap).
- Mode aktif **15 menit**, lalu mati sendiri.
- Tombol **📁 Folder Baru** untuk membuat folder langsung dari file manager.

#### Cara 2 — Lewat caption (path manual)

Kirim file, lalu beri **caption** berisi path:

```
/root/                          → simpan dengan nama asli di /root
/var/www/html/index.html        → simpan dengan nama kustom
/etc/nginx/sites-available/     → path berakhir / = folder tujuan
(kosong)                        → simpan di folder kerja bot
```

#### Perlindungan yang aktif

| Perlindungan | Penjelasan |
| :--- | :--- |
| **Batas ukuran 20MB** | File lebih besar ditolak **sebelum** diunduh, dengan saran alternatif. Telegram Bot API tidak bisa mengunduh file >20MB. |
| **Konfirmasi timpa** | File dengan nama sama **tidak** ditimpa diam-diam. Bot menampilkan ukuran & waktu file lama, lalu minta konfirmasi. |
| **Anti path traversal** | Nama file seperti `../../etc/passwd` tidak bisa menulis ke luar folder tujuan. |
| **Validasi folder** | Bot memberi tahu bila folder tidak ada atau tidak bisa ditulis (sebelum mengunduh). |
| **Auto-bersih** | File gagal yang berukuran 0 byte dihapus otomatis. |

#### Bila file lebih dari 20MB

Telegram Bot API membatasi bot mengunduh file maksimal **20MB**, dan batas ini
**tidak bisa dinaikkan** pada bot biasa. Ada tiga cara melewatinya:

**1️⃣ Unduh dari URL** — *paling praktis*

File sudah ada di internet? VPS mengunduhnya sendiri, tanpa lewat Telegram.

```
/unduh https://contoh.com/data.zip
/unduh https://contoh.com/data.zip /root/arsip.zip
```

Tidak ada batas 20MB — hanya ruang disk VPS. Timeout 30 menit.

**2️⃣ Upload bertahap** — *file hanya di komputermu*

Pecah file di komputermu, kirim per bagian lewat Telegram, bot menyatukan otomatis.

```bash
# Di komputermu:
split -b 15M data.zip data.zip.part_
ls data.zip.part_* | wc -l      # catat jumlahnya
```

```
/chunk mulai data.zip 5         # 5 = jumlah bagian
# Kirim tiap bagian data.zip.part_aa, part_ab, … sebagai dokumen
/chunk status                    # cek kemajuan
```

Bot menyatukan otomatis setelah bagian terakhir. Batalkan dengan `/chunk batal`.

**3️⃣ Local Bot API Server** — *batas naik jadi 2GB*

Jalankan server Telegram sendiri. Sekali setup, semua upload jadi 2GB.
Lihat panduan di bot: `/help` → **📦 Upload Besar** → **Cara Setup Server 2GB**.

> Butuh RAM ~200MB dan berjalan permanen. Untuk VPS kecil, cara 1️⃣ dan 2️⃣ lebih hemat.

#### Pecah & gabung file

Untuk mengirim file besar **keluar** dari VPS (batas `/getfile` = 50MB/pesan):

```
/pecah /root/backup.tar.gz       # pecah jadi bagian 45MB
/gabung /root/backup.tar.gz.part_aaa /root/backup.tar.gz
```

Digabung juga bisa di komputer lain: `cat backup.tar.gz.part_* > backup.tar.gz`

#### Tabel perbandingan

| Metode | Batas | Kelebihan | Kekurangan |
| :--- | :--- | :--- | :--- |
| Tombol upload | 20MB | Paling mudah, beberapa file sekaligus | Terbatas Bot API |
| Caption path | 20MB | Cepat untuk path yang sudah dihafal | Sama |
| `/unduh <URL>` | Disk VPS | Tanpa batas, langsung dari internet | File harus sudah online |
| `/chunk` | 15MB/bagian | Bisa file apa pun dari komputermu | Perlu pecah manual dulu |
| Local Bot API | 2GB | Sekali setup, lancar | Butuh RAM 200MB |

💡 **Tips:** kirim file `.zip`/`.tar.gz` — kompresi sering menurunkan ukuran
di bawah 20MB tanpa perlu cara lain.

---

## ⚙️ Konfigurasi

Semua kredensial diambil dari **environment variable** atau file `.env`.
Tidak ada token yang ditanam di source code — ini disengaja, karena token bot
setara akses shell ke VPS.

```bash
cp .env.example .env
nano .env
```

```env
BOT_TOKEN=token_dari_botfather
ADMIN_IDS=user_id_kamu
```

### Cara mendapatkan nilainya

| Nilai | Cara |
| :--- | :--- |
| `BOT_TOKEN` | Kirim `/newbot` ke [@BotFather](https://t.me/BotFather) |
| `ADMIN_IDS` | Kirim pesan apa pun ke [@userinfobot](https://t.me/userinfobot) |

Beberapa admin bisa dipisahkan koma: `ADMIN_IDS=123456789,987654321`

### Variabel opsional

| Variabel | Fungsi |
| :--- | :--- |
| `API_URL` | Arahkan ke Local Bot API Server sendiri — batas upload naik dari 20MB ke 2GB |
| `TELEGRAM_BOT_TOKEN` | Nama alternatif untuk `BOT_TOKEN` |

### Prioritas pembacaan

```
Environment variable  →  file .env  →  gagal dengan pesan jelas
```

Bot **menolak berjalan** bila `BOT_TOKEN` atau `ADMIN_IDS` tidak ada, dengan
pesan yang menjelaskan cara mengisinya.

---

## 🧪 Testing

Project ini menyertakan suite test lengkap:

```bash
# Semua test (unit + PTY)
go test -v .

# Hanya test PTY (butuh /dev/ptmx + /dev/pts)
go test -run TestPTY -v .
```

Test PTY otomatis **di-skip** (bukan gagal) di lingkungan yang tidak punya
dukungan PTY penuh, seperti container terbatas.

Yang diverifikasi test:
- PTY dibuat lewat `syscall` stdlib — **nol dependency eksternal**.
- Shell mendapat TTY asli (`/dev/pts/N`), bukan pipe.
- **`cd` dan `export` menetap** antar perintah terpisah (inti fitur Level 3).
- Sesi ditutup tanpa meninggalkan proses yatim.
- Batas sesi ditegakkan (anti RAM bocor).
- Escape sequence ANSI dibersihkan sebelum dikirim ke Telegram.

---

## 📦 Menjalankan apt update / upgrade

Bisa, dan ini salah satu kasus di mana terminal PTY lebih unggul dari `/exec` —
karena **prompt interaktif debconf bisa muncul dan dijawab dari Telegram**.

### Langkah yang benar

```
/term apt check      ← WAJIB: cek lock apt & hak root
/term clear          ← bersihkan buffer sebelum mulai
/term apt update     ← perbarui daftar paket
/term                ← lihat hasilnya
/term apt upgrade    ← mulai upgrade
```

Jika muncul prompt seperti ini di chat:
```
Do you want to continue? [Y/n]
```
Cukup kirim jawabannya sebagai pesan biasa:
```
y
```
Lalu ketik `/term` untuk melihat lanjutannya.

### Hal penting yang perlu diketahui

| Hal | Penjelasan |
| :--- | :--- |
| **Butuh root** | apt tidak bisa jalan sebagai user biasa. Jalankan bot sebagai root, atau user dengan sudo (bot otomatis mencoba `sudo -n`). |
| **Cek lock dulu** | `unattended-upgrades` sering memegang lock. Kalau bentrok, apt gagal. Pakai `/term apt check`. |
| **Tidak ada timeout** | Berbeda dari `/exec` (60 detik), terminal PTY tidak timeout — aman untuk upgrade yang butuh 5-30 menit. |
| **Output panjang** | Buffer 256KB. Untuk log lebih besar, pakai `/term log`. |
| **Batalkan kapan saja** | Kirim `/term ^c` — operasi berhenti, sesi tetap hidup. |
| **Reboot diperlukan** | Setelah update kernel, VPS perlu di-reboot: `/reboot confirm`. |

---

## 🐳 Deploy di Container / VPS Tanpa TUN

Bot ini dirancang jalan di lingkungan terbatas:

| Lingkungan | Catatan |
| :--- | :--- |
| **VPS biasa** | Jalan langsung. Jalankan sebagai root agar `apt` dan file manager punya akses penuh. |
| **Docker / LXC** | Butuh `--privileged` atau minimal `--cap-add=SYS_ADMIN` untuk PTY. |
| **Container tanpa `/dev/net/tun`** | Tailscale otomatis memakai mode *userspace networking*. |
| **Tanpa systemd** | `tailscaled` dijalankan langsung sebagai fallback. |

### Bila PTY tidak berjalan

```bash
# Cek dukungan PTY
go test -run TestPTY -v .
```

Bot memberi pesan jelas bila `/dev/ptmx` atau `/dev/pts` tidak tersedia —
fitur terminal akan nonaktif, sisanya tetap jalan.

---

## 🤝 Kontribusi

```bash
# Jalankan semua test sebelum mengirim PR
go test ./...

# Cek format & masalah umum
gofmt -l .
go vet ./...
```

Panduan singkat:

- **Satu fitur = satu file.** `terminal.go`, `upload.go`, `tailscale.go`.
- **Setiap fitur baru wajib punya test.** Repo ini memegang standar itu: 117 test
  untuk ~5.800 baris kode.
- **Jangan tambah dependency.** Nol dependency adalah fitur utama, bukan kebetulan.
  Kalau standard library bisa melakukannya, pakai standard library.
- **Jangan pernah commit `.env`** atau kredensial apa pun. Test
  `TestRepoBersihDariKredensial` akan menolaknya.

---

## ⚠️ Catatan Keamanan

**Token bot Telegram = akses shell ke VPS kamu.** Siapa pun yang memegang token
bisa menjalankan perintah apa pun lewat `/exec` dan `/term`.

| Praktik | Alasan |
| :--- | :--- |
| Jangan commit `.env` | Sudah ada di `.gitignore`. Bila bocor: `/revoke` di @BotFather |
| Batasi `ADMIN_IDS` | Bot menolak semua orang di luar daftar ini |
| Jangan pakai bot ini di VPS bersama | Satu admin = satu pemilik penuh |
| Aktifkan Tailscale SSH | Akses lewat jaringan privat, bukan internet terbuka |
| Periksa PR sebelum merge | Bot ini punya akses destruktif (`/rm`, `/reboot`) |

Fitur yang sudah dilindungi:

- **Anti path traversal** pada upload — `../../etc/passwd` ditolak
- **Anti injeksi shell** pada `/unduh` — URL selalu di-quote
- **Konfirmasi** untuk hapus, timpa, dan memutus Tailscale
- **Batas sesi** — maksimal 10 sesi terminal, auto-kill saat menganggur
- **Pembunuhan process group** — tidak meninggalkan proses yatim
