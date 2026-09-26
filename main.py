#!/usr/bin/env python3
"""
Python Entrypoint Loader for Golang Core Engine (AMD64 Lightweight Edition)
- Menjalankan binary core_engine secara langsung
- Otomatis melakukan build binary jika belum tersedia
"""

import os
import sys
import subprocess

BASE_DIR = os.path.dirname(os.path.abspath(__file__))
bin_target = os.path.join(BASE_DIR, "core_engine")

if not os.path.exists(bin_target):
    main_go = os.path.join(BASE_DIR, "main.go")
    if os.path.exists(main_go):
        print("⚠️ Binary core_engine belum ditemukan. Mencoba build otomatis...", flush=True)
        try:
            res = subprocess.run(
                ["go", "build", "-ldflags=-s -w", "-o", bin_target, "."],
                cwd=BASE_DIR
            )
            if res.returncode != 0:
                print("❌ Gagal melakukan kompilasi otomatis main.go", flush=True)
                sys.exit(1)
            print("✅ Berhasil mengompilasi core_engine.", flush=True)
        except Exception as e:
            print(f"❌ Gagal memanggil go build: {e}", flush=True)
            sys.exit(1)
    else:
        print(f"❌ Binary engine dan source code tidak ditemukan di: {BASE_DIR}", flush=True)
        sys.exit(1)

try:
    os.chmod(bin_target, 0o755)
except Exception:
    pass

try:
    proc = subprocess.run([bin_target] + sys.argv[1:])
    sys.exit(proc.returncode)
except KeyboardInterrupt:
    print("\n👋 Bot dihentikan oleh pengguna.")
    sys.exit(0)
except Exception as e:
    print(f"❌ Gagal menjalankan engine: {e}", flush=True)
    sys.exit(1)
