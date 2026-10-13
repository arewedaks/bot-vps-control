package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Uji: /etc/bot-vps.env dibaca bila .env tidak ada, dan .env lokal menang.
func TestLoadDotEnvFallback(t *testing.T) {
	dir := t.TempDir()
	etcDir := t.TempDir()
	etcFile := filepath.Join(etcDir, "bot-vps.env")

	os.WriteFile(etcFile, []byte("BOT_TOKEN=DARI_ETC\nADMIN_IDS=1\n"), 0600)

	// .env tidak ada -> harus jatuh ke /etc
	os.Unsetenv("BOT_TOKEN")
	loadDotEnv(filepath.Join(dir, ".env"))
	loadDotEnv(etcFile)
	if got := os.Getenv("BOT_TOKEN"); got != "DARI_ETC" {
		t.Fatalf("fallback gagal: dapat %q, mau DARI_ETC", got)
	}

	// .env ada -> harus menang, tidak ditimpa /etc
	local := filepath.Join(dir, ".env")
	os.WriteFile(local, []byte("BOT_TOKEN=DARI_LOKAL\n"), 0600)
	os.Unsetenv("BOT_TOKEN")
	loadDotEnv(local)
	loadDotEnv(etcFile)
	if got := os.Getenv("BOT_TOKEN"); got != "DARI_LOKAL" {
		t.Fatalf(".env lokal kalah: dapat %q, mau DARI_LOKAL", got)
	}
}

// Uji: env yang sudah ada tidak ditimpa oleh berkas (perilaku lama, dijaga).
func TestLoadDotEnvTidakMenimpaEnv(t *testing.T) {
	f := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(f, []byte("BOT_TOKEN=DARI_BERKAS\n"), 0600)
	os.Setenv("BOT_TOKEN", "DARI_ENV")
	defer os.Unsetenv("BOT_TOKEN")
	loadDotEnv(f)
	if got := os.Getenv("BOT_TOKEN"); got != "DARI_ENV" {
		t.Fatalf("env ditimpa: dapat %q", got)
	}
}
