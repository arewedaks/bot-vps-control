---
name: Laporan Bug
about: Laporkan perilaku yang tidak sesuai harapan
title: '[Bug] '
labels: bug
assignees: ''
---

## Ringkasan

<!-- Satu atau dua kalimat: apa yang salah? -->

## Langkah reproduksi

1.
2.
3.

## Yang diharapkan

<!-- Apa yang seharusnya terjadi? -->

## Yang terjadi

<!-- Apa yang benar-benar terjadi? -->

## Informasi lingkungan

| | |
| :--- | :--- |
| **OS / distro** | |
| **Versi kernel** | |
| **Arsitektur** | amd64 / arm64 / arm |
| **Cara menjalankan** | langsung / systemd / docker |
| **Versi Go** | |

Jalankan ini dan lampirkan hasilnya:

```bash
uname -a
go version
cat /etc/os-release | head -2
```

## Log

<!--
Jalankan bot, reproduksi masalahnya, lalu salin output terminal di sini.
SENSOR token bot dan User ID sebelum menempel!
-->

```
(tempel log di sini)
```

## Konteks tambahan

<!--
- Apakah ini juga terjadi di versi sebelumnya?
- Perintah Telegram apa yang dipakai?
- Apakah masalahnya muncul setelah perubahan konfigurasi?
-->

## Daftar periksa

- [ ] Saya sudah menjalankan `make check` dan semuanya lulus
- [ ] Saya sudah menghapus token dan User ID dari log di atas
- [ ] Saya sudah mencari issue serupa yang mungkin sudah ada
