package main

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

// ---- Test pembersih ANSI ----

func TestCleanANSI(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"warna sederhana", "\x1b[31mmerah\x1b[0m", "merah"},
		{"warna bold", "\x1b[1;32mhijau\x1b[0m teks", "hijau teks"},
		{"kursor bergerak", "\x1b[2K\x1b[1Ghasil", "hasil"},
		{"OSC judul window", "\x1b]0;judul\x07isi", "isi"},
		{"charset selection", "\x1b(Bisi", "isi"},
		{"keypad mode", "\x1b=isi", "isi"},
		{"CR tunggal dibuang", "progres 50%\rdone", "progres 50%done"},
		{"CRLF jadi newline", "baris1\r\nbaris2", "baris1\nbaris2"},
		{"newline dipertahankan", "baris1\nbaris2", "baris1\nbaris2"},
		{"tab dipertahankan", "a\tb", "a\tb"},
		{"kosong", "", ""},
		{"hanya escape", "\x1b[0m\x1b[2K", ""},
	}
	for _, c := range cases {
		got := cleanANSI(c.in)
		if got != c.want {
			t.Errorf("%s: cleanANSI(%q) = %q, mau %q", c.name, c.in, got, c.want)
		}
	}
}

func TestCleanANSITrim(t *testing.T) {
	if got := cleanANSI("  \n\n  teks  \n\n  "); got != "teks" {
		t.Errorf("trim gagal: %q", got)
	}
}

func TestCleanANSINoRawEscape(t *testing.T) {
	// Pastikan tidak ada byte ESC (0x1b) yang lolos ke Telegram.
	raw := "\x1b[31m\x1b[1m\x1b[0m\x1b[2J\x1b[H teks \x1b]0;x\x07"
	if strings.ContainsRune(cleanANSI(raw), 0x1b) {
		t.Fatal("byte ESC masih lolos — Telegram akan error parse")
	}
}

// ---- Test chunking ----

func TestTermChunk(t *testing.T) {
	long := strings.Repeat("a", 10000)
	chunks := termChunk(long, 4096)
	if len(chunks) != 3 {
		t.Fatalf("mau 3 chunk, dapat %d", len(chunks))
	}
	if len(chunks[0]) != 4096 || len(chunks[1]) != 4096 || len(chunks[2]) != 10000-8192 {
		t.Fatalf("ukuran chunk salah: %d %d %d", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
	if strings.Join(chunks, "") != long {
		t.Fatal("hasil join tidak sama dengan input")
	}
}

func TestTermChunkMultibyte(t *testing.T) {
	// Emoji harus dipecah per rune, bukan per byte.
	s := strings.Repeat("🔥", 100)
	chunks := termChunk(s, 10)
	for _, c := range chunks {
		if !strings.HasPrefix(c, "🔥") && c != "" {
			t.Fatalf("chunk merusak emoji: %q", c)
		}
	}
	if strings.Join(chunks, "") != s {
		t.Fatal("multibyte rusak saat split")
	}
}

func TestTermChunkEmpty(t *testing.T) {
	if got := termChunk("", 100); len(got) != 0 {
		t.Fatalf("input kosong harus menghasilkan 0 chunk, dapat %d", len(got))
	}
}

// ---- Test quoting ----

func TestTermShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", "'hello'"},
		{"a b", "'a b'"},
		{"it's", `'it'\''s'`},
		{"", "''"},
	}
	for _, c := range cases {
		if got := termShellQuote(c.in); got != c.want {
			t.Errorf("termShellQuote(%q) = %q, mau %q", c.in, got, c.want)
		}
	}
}

// ---- Test parser angka ----

func TestTermParseInt(t *testing.T) {
	if got := termParseInt("42", 0); got != 42 {
		t.Errorf("mau 42, dapat %d", got)
	}
	if got := termParseInt("abc", 7); got != 7 {
		t.Errorf("fallback gagal: %d", got)
	}
	if got := termParseInt("", 3); got != 3 {
		t.Errorf("string kosong fail: %d", got)
	}
}

// ---- Test session map tanpa PTY ----

func TestKillSessionAbsent(t *testing.T) {
	if KillSession(999999) {
		t.Fatal("KillSession pada user tak dikenal harus return false")
	}
}

func TestSessionInfoAbsent(t *testing.T) {
	got := SessionInfo(999999)
	if !strings.Contains(got, "tidak ada") {
		t.Fatalf("SessionInfo user kosong = %q", got)
	}
}

func TestSendToTerminalAbsent(t *testing.T) {
	if err := SendToTerminal(999999, "ls\n"); err == nil {
		t.Fatal("SendToTerminal tanpa sesi harus return error")
	}
}

func TestReadScreenAbsent(t *testing.T) {
	if _, err := ReadScreen(999999); err == nil {
		t.Fatal("ReadScreen tanpa sesi harus return error")
	}
}

func TestSendSignalAbsent(t *testing.T) {
	if err := SendSignal(999999, syscall.SIGINT, "SIGINT"); err == nil {
		t.Fatal("SendSignal tanpa sesi harus return error")
	}
}

func TestDirectModeToggle(t *testing.T) {
	u := int64(424242)
	if IsTerminalDirectMode(u) {
		t.Fatal("default harus nonaktif")
	}
	termDirectMu.Lock()
	termDirectMode[u] = true
	termDirectMu.Unlock()
	if !IsTerminalDirectMode(u) {
		t.Fatal("setelah diaktifkan harus true")
	}
	termDirectMu.Lock()
	delete(termDirectMode, u)
	termDirectMu.Unlock()
	if IsTerminalDirectMode(u) {
		t.Fatal("setelah dihapus harus false")
	}
}

func TestReapIdleSessionsEmpty(t *testing.T) {
	// Tidak boleh panic saat map kosong.
	reapIdleSessions()
}

// ---- Test buffer cap (simulasi pertumbuhan Screen) ----

func TestScreenCapTrimsTail(t *testing.T) {
	// Meniru logika readLoop: buffer dipotong dari depan, ekor dipertahankan.
	buf := []byte(strings.Repeat("X", 1000))
	cap := 100
	newChunk := []byte(strings.Repeat("Y", 250))
	buf = append(buf, newChunk...)
	if len(buf) > cap {
		buf = buf[len(buf)-cap:]
	}
	if len(buf) != cap {
		t.Fatalf("buffer tidak dipotong ke %d, panjang = %d", cap, len(buf))
	}
	if !strings.HasSuffix(string(buf), "Y") {
		t.Fatal("ekor buffer (output terbaru) harus dipertahankan")
	}
}

// ---- Test findShell / termStartDir ----

func TestFindShellReturnsExisting(t *testing.T) {
	sh := findShell()
	if sh == "" {
		t.Fatal("findShell kosong")
	}
	if !strings.HasPrefix(sh, "/") {
		t.Fatalf("findShell harus path absolut, dapat %q", sh)
	}
}

func TestTermStartDirValid(t *testing.T) {
	d := termStartDir()
	if d == "" {
		t.Fatal("termStartDir kosong")
	}
	if !strings.HasPrefix(d, "/") {
		t.Fatalf("termStartDir harus absolut, dapat %q", d)
	}
}

// ---- Test timeout sesi ----

func TestIdleTimeoutConstant(t *testing.T) {
	if termIdleTimeout != 30*time.Minute {
		t.Fatalf("termIdleTimeout = %v, mau 30 menit", termIdleTimeout)
	}
	if termMaxSessions < 1 {
		t.Fatal("termMaxSessions harus >= 1")
	}
}
