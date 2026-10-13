#!/usr/bin/env sh
# ==============================================================================
# Pemasang bot-vps-control (Core Engine) tanpa Go toolchain.
#
#   curl -fsSL https://raw.githubusercontent.com/arewedaks/bot-vps-control/master/install.sh | sh
#
# Setelah selesai, isi /etc/bot-vps.env (lihat .env.example) lalu:
#   sudo systemctl enable --now bot-vps
# ==============================================================================
set -eu

REPO="arewedaks/bot-vps-control"
BIN="core_engine"
DEST="${INSTALL_DIR:-/usr/local/bin}"
VER="${VERSION:-latest}"

log()  { printf '%s\n' "$*"; }
die()  { printf '❌ %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "jalankan sebagai root: sudo -s lalu ulangi"

# ---------------------------------------------------------------- arsitektur
# uname -m mengembalikan nama kernel; GitHub memakai nama Go.
case "$(uname -m)" in
    x86_64|amd64)  ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    armv7l|armv7)  ARCH=arm ;;
    i386|i686)     ARCH=386  ;;
    *) die "arsitektur tidak dikenali: $(uname -m). Unduh manual dari halaman Releases." ;;
esac

log "📦 bot-vps-control ${VER} — ${ARCH}"

# ---------------------------------------------------------------- unduh
if [ "$VER" = "latest" ]; then
    BASE="https://github.com/${REPO}/releases/latest/download"
else
    BASE="https://github.com/${REPO}/releases/download/${VER}"
fi
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT INT TERM

log "⬇️  Mengunduh ${BIN}-linux-${ARCH}..."
curl -fsSL --retry 3 --retry-delay 2 -o "$TMP" "${BASE}/${BIN}-linux-${ARCH}" \
    || die "gagal mengunduh. Cek konektivitas ke github.com"

# ---------------------------------------------------------------- verifikasi
# Checksum diverifikasi bila tersedia. Lewati dengan tenang kalau tidak ada,
# karena file checksum tidak selalu ikut rilis.
if curl -fsSL --retry 2 -o "${TMP}.sum" "${BASE}/SHA256SUMS" 2>/dev/null; then
    WANT="$(grep " ${BIN}-linux-${ARCH}\$" "${TMP}.sum" | awk '{print $1}')"
    GOT="$(sha256sum "$TMP" | awk '{print $1}')"
    [ -n "$WANT" ] || die "checksum untuk ${BIN}-linux-${ARCH} tidak ada di SHA256SUMS"
    [ "$WANT" = "$GOT" ] || die "checksum tidak cocok — berkas rusak atau dimanipulasi"
    rm -f "${TMP}.sum"
    log "✅ Checksum terverifikasi"
else
    log "⚠️  SHA256SUMS tidak tersedia — lewati verifikasi checksum"
fi

# ---------------------------------------------------------------- pasang
install -m 0755 "$TMP" "${DEST}/${BIN}"
log "✅ Terpasang: ${DEST}/${BIN}"

# ---------------------------------------------------------------- konfigurasi
# Ditulis di luar cabang systemd: init system tidak ada hubungannya dengan
# penyimpanan konfigurasi. Kalau ini diletakkan di dalam 'if systemd', kontainer
# tanpa systemd akan kehilangan BOT_TOKEN tanpa pesan apa pun.
ENVF=/etc/bot-vps.env
if [ ! -f "$ENVF" ]; then
    : > "$ENVF"
fi
chmod 600 "$ENVF"

# Isi hanya baris yang belum ada, agar token lama tidak tertimpa dan env var
# yang dikirim ulang pada installer kedua tetap terpakai.
set_kv() {
    _k="$1"; _v="${2:-}"
    [ -n "$_v" ] || return 0
    grep -q "^${_k}=" "$ENVF" && return 0
    printf '%s=%s\n' "$_k" "$_v" >>"$ENVF"
    log "✅ ${_k} disimpan ke ${ENVF}"
}
set_kv BOT_TOKEN "${BOT_TOKEN:-}"
set_kv ADMIN_IDS "${ADMIN_IDS:-}"
set_kv DEPLOY_DIR "${DEPLOY_DIR:-}"

# ---------------------------------------------------------------- jalankan
if grep -q '^BOT_TOKEN=' "$ENVF"; then
    if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
        log "🔧 Menyiapkan service systemd..."
        install -d -m 0755 /var/lib/bot-vps-control
        curl -fsSL -o /etc/systemd/system/bot-vps.service \
            "https://raw.githubusercontent.com/${REPO}/master/bot-vps.service" \
            || log "⚠️  Gagal mengunduh bot-vps.service — lewati (sudah ada unit sistem?)"
        systemctl daemon-reload
        systemctl enable --now bot-vps
        log "✅ Service berjalan: systemctl status bot-vps"
    else
        # Tanpa systemd (container/LXC/WSL) bot tidak akan menyala sendiri.
        # setsid melepasnya dari sesi SSH supaya tahan logout.
        if pgrep -x "$BIN" >/dev/null 2>&1; then
            log "ℹ️  $BIN sudah berjalan — dilewati"
        else
            install -d -m 0755 /var/lib/bot-vps-control
            setsid nohup "${DEST}/${BIN}" >>/var/log/bot-vps.log 2>&1 &
            sleep 2
            if pgrep -x "$BIN" >/dev/null 2>&1; then
                log "✅ Berjalan di latar (systemd tidak ada). Log: /var/log/bot-vps.log"
            else
                log "⚠️  Gagal start — cek: tail -20 /var/log/bot-vps.log"
            fi
        fi
        log "ℹ️  Nyala ulang saat reboot: pasang @reboot crontab (lihat README)"
    fi
else
    log ""
    log "⚠️  BOT_TOKEN belum terisi. Lengkapi lalu jalankan:"
    log "      nano ${ENVF}        # isi BOT_TOKEN dan ADMIN_IDS"
    log "      ${DEST}/${BIN}"
    log ""
fi

log ""
log "Selesai. Perintah dasar:"
log "  /start    — menu utama"
log "  /help     — daftar perintah"
log "  /term     — terminal interaktif"
log "  /deploy   — jalankan bot lain di VPS ini"
