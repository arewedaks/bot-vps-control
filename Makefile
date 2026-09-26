.PHONY: all build test test-pty check fmt vet run clean install release help

BIN     := core_engine
PKG     := .

all: check build

# ------------------------------------------------------------------------------
# Build
# ------------------------------------------------------------------------------
build:
	@echo "🔨 Mengompilasi $(BIN)..."
	@go build -ldflags="-s -w" -o $(BIN) $(PKG)
	@chmod +x $(BIN)
	@echo "✅ Build selesai: $$(ls -lh $(BIN) | awk '{print $$5}')"

# Build untuk arsitektur lain (mis. VPS ARM)
#   make release ARCH=arm64 OS=linux
release:
	@echo "🔨 Cross-compile untuk $(OS)/$(ARCH)..."
	@GOOS=$(OS) GOARCH=$(ARCH) go build -ldflags="-s -w" -o $(BIN)-$(OS)-$(ARCH) $(PKG)
	@echo "✅ Selesai: $(BIN)-$(OS)-$(ARCH)"

# ------------------------------------------------------------------------------
# Pengujian
# ------------------------------------------------------------------------------
test:
	@echo "🧪 Menjalankan seluruh test..."
	@go test $(PKG)

test-verbose:
	@go test -v $(PKG)

# Hanya test PTY — membutuhkan /dev/ptmx dan /dev/pts yang berfungsi.
# Di container terbatas, test ini SKIP sendiri (bukan gagal).
test-pty:
	@echo "🖥️  Menjalankan test PTY..."
	@go test -run TestPTY -v $(PKG)

# Menampilkan pratinjau tampilan file manager tanpa membuka Telegram.
test-preview:
	@go test -run TestPreviewSemuaKasus -v $(PKG)

# ------------------------------------------------------------------------------
# Pemeriksaan kualitas
# ------------------------------------------------------------------------------
check: fmt vet test
	@echo "✅ Semua pemeriksaan lulus"

fmt:
	@echo "📐 Memeriksa format..."
	@unformatted=$$(gofmt -l $(PKG)); \
	if [ -n "$$unformatted" ]; then \
		echo "❌ File belum diformat:"; echo "$$unformatted"; \
		echo "   Perbaiki dengan: gofmt -w ."; exit 1; \
	fi
	@echo "✅ Format bersih"

vet:
	@echo "🔍 Menjalankan go vet..."
	@go vet $(PKG)
	@echo "✅ Tidak ada masalah"

# Memastikan tidak ada dependency eksternal yang menyusup masuk.
# Nol dependency adalah fitur utama project ini, bukan kebetulan.
check-deps:
	@echo "🔒 Memeriksa dependency..."
	@if grep -q "^require" go.mod 2>/dev/null; then \
		echo "❌ go.mod memuat dependency eksternal:"; \
		grep -A 20 "^require" go.mod; \
		echo "   Project ini harus tetap nol dependency."; exit 1; \
	fi
	@echo "✅ Nol dependency eksternal"

# ------------------------------------------------------------------------------
# Menjalankan
# ------------------------------------------------------------------------------
run: build
	@echo "🚀 Menjalankan bot..."
	@./$(BIN)

# Instalasi sebagai service systemd
#   sudo make install PREFIX=/root/bot-vps-control
install: build
	@test -n "$(PREFIX)" || (echo "❌ Setel PREFIX, contoh: sudo make install PREFIX=/root/bot-vps-control" && exit 1)
	@echo "📦 Memasang ke $(PREFIX)..."
	@mkdir -p $(PREFIX)
	@cp $(BIN) $(PREFIX)/
	@test -f .env && cp .env $(PREFIX)/ || echo "⚠️  .env tidak ada — bot akan menolak berjalan"
	@echo "✅ Terpasang. Jangan lupa sesuaikan WorkingDirectory di bot-vps.service"

# ------------------------------------------------------------------------------
clean:
	@echo "🧹 Membersihkan hasil build..."
	@rm -f $(BIN) $(BIN)-* *.test
	@echo "✅ Bersih"

# ------------------------------------------------------------------------------
help:
	@echo "🤖 Bot VPS Control — perintah make"
	@echo ""
	@echo "  Build:"
	@echo "    make build              Kompilasi binary"
	@echo "    make release ARCH=arm64 Cross-compile untuk arsitektur lain"
	@echo ""
	@echo "  Pengujian:"
	@echo "    make test               Jalankan seluruh test"
	@echo "    make test-verbose       Test dengan output lengkap"
	@echo "    make test-pty           Hanya test PTY (terminal)"
	@echo "    make test-preview       Pratinjau tampilan file manager"
	@echo ""
	@echo "  Pemeriksaan:"
	@echo "    make check              Format + vet + test sekaligus"
	@echo "    make fmt                Cek format kode"
	@echo "    make vet                Cek masalah umum"
	@echo "    make check-deps         Pastikan tetap nol dependency"
	@echo ""
	@echo "  Menjalankan:"
	@echo "    make run                Build lalu jalankan bot"
	@echo "    make clean              Hapus hasil build"
	@echo ""
