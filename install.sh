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

# Hanya buat service bila systemd benar-benar ada, agar tidak merusak
# lingkungan kontainer yang tidak punya systemd.
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    log "🔧 Menyiapkan service systemd..."
    install -d -m 0755 /var/lib/bot-vps-control
    curl -fsSL -o /etc/systemd/system/bot-vps.service \
        "https://raw.githubusercontent.com/${REPO}/master/bot-vps.service" \
        || log "⚠️  Gagal mengunduh bot-vps.service — lewati (sudah ada unit sistem?)"

    # Konfigurasi bisa dikirim langsung lewat env var, supaya satu baris
    # instalasi langsung menghidupkan bot tanpa edit manual:
    #   sudo env BOT_TOKEN=... ADMIN_IDS=... sh -c "$(curl -fsSL ...)"
    # Tanpa keduanya, /etc/bot-vps.env dibuat kosong sebagai templat.
    if [ ! -f /etc/bot-vps.env ]; then
        : > /etc/bot-vps.env
        chmod 600 /etc/bot-vps.env
        [ -n "${BOT_TOKEN:-}" ] && printf 'BOT_TOKEN=%s\n' "$BOT_TOKEN" >> /etc/bot-vps.env
        [ -n "${ADMIN_IDS:-}" ] && printf 'ADMIN_IDS=%s\n' "$ADMIN_IDS" >> /etc/bot-vps.env
        [ -n "${DEPLOY_DIR:-}" ] && printf 'DEPLOY_DIR=%s\n' "$DEPLOY_DIR" >> /etc/bot-vps.env
    fi

    if ! grep -q '^BOT_TOKEN=' /etc/bot-vps.env; then
        log ""
        log "⚠️  BOT_TOKEN belum terisi. Lengkapi lalu hidupkan:"
        log "      nano /etc/bot-vps.env        # isi BOT_TOKEN dan ADMIN_IDS"
        log "      systemctl enable --now bot-vps"
        log ""
        log "Lokasi proyek akan dipakai: \${DEPLOY_DIR:-/var/lib/bot-vps-control/berkas}"
    else
        systemctl daemon-reload
        systemctl enable --now bot-vps
        log "✅ Service berjalan: systemctl status bot-vps"
    fi
else
    log "ℹ️  systemd tidak terdeteksi — service tidak dipasang."
fi

log ""
log "Selesai. Perintah dasar:"
log "  /start    — menu utama"
log "  /help     — daftar perintah"
log "  /term     — terminal interaktif"
log "  /deploy   — jalankan bot lain di VPS ini"
