#!/bin/sh
# Penjaga core_engine untuk lingkungan tanpa systemd/cron.
# Jalankan sekali; ia me-restart bot bila prosesnya mati.
#   setsid nohup /usr/local/bin/core_engine-guard >>/var/log/bot-vps-guard.log 2>&1 &
set -u
BIN=${BIN:-/usr/local/bin/core_engine}
LOG=${LOG:-/var/log/bot-vps.log}
JEDA=${JEDA:-5}

echo "$(date -Is) guard up pid=$$ -> $BIN" >>"$LOG"
while :; do
    "$BIN" >>"$LOG" 2>&1 &
    pid=$!
    wait "$pid"
    echo "$(date -Is) core_engine exit rc=$?; restart dalam ${JEDA}s" >>"$LOG"
    sleep "$JEDA"
done
