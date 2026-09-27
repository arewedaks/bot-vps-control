package main

// ==============================================================================
// 🚀 DEPLOY BOT — ANTARMUKA MENU
// ==============================================================================
// Semua teks dan tombol panel deploy. Dipisah dari deploy.go supaya berkas
// mesin (proses, unduhan, deteksi) tetap enak dibaca tanpa terganggu markup.
//
// Format callback: dp:<aksi>[:<argumen>]
//   dp:m            → menu utama deploy
//   dp:l[:N]        → daftar proyek, halaman N
//   dp:mk           → panduan buat proyek baru
//   dp:u[:N]        → panduan deploy dari URL
//   dp:g            → panduan deploy dari GitHub
//   dp:c:<nama>     → panel kontrol proyek
//   dp:run:<nama>   → nyalakan
//   dp:stop:<nama>  → hentikan
//   dp:rst:<nama>   → nyalakan ulang
//   dp:log:<nama>   → lihat log
//   dp:del:<nama>   → konfirmasi hapus
//   dp:del2:<nama>  → hapus sungguhan
//   dp:lang:<nama>  → pilih bahasa manual
//   dp:setlang:<nama>:<kode>
//   dp:setent:<nama> → mulai mode tentukan entry point

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ==============================================================================
// STATE PERCAKAPAN
// ==============================================================================
// Menyimpan langkah percakapan yang sedang berjalan per pengguna, misalnya
// "sedang menunggu URL" atau "sedang menunggu nama entry point".
//
// Diproteksi mutex karena handler Telegram berjalan di goroutine terpisah dan
// peta biasa tidak aman diakses bersamaan.
var (
	muStateDeploy sync.Mutex
	stateDeploy   = map[int64]map[string]string{}
)

type langkahDeploy struct {
	Aksi   string // mk, unduh, git, entry
	Proyek string // untuk aksi entry
	Pesan  int64  // id pesan panel yang harus ditulis ulang
}

func setLangkahDeploy(userID int64, s langkahDeploy) {
	muStateDeploy.Lock()
	defer muStateDeploy.Unlock()
	stateDeploy[userID] = map[string]string{
		"aksi":   s.Aksi,
		"proyek": s.Proyek,
		"pesan":  strconv.FormatInt(s.Pesan, 10),
	}
}

func ambilLangkahDeploy(userID int64) (langkahDeploy, bool) {
	muStateDeploy.Lock()
	defer muStateDeploy.Unlock()

	m, ok := stateDeploy[userID]
	if !ok {
		return langkahDeploy{}, false
	}
	pesan, _ := strconv.ParseInt(m["pesan"], 10, 64)
	return langkahDeploy{Aksi: m["aksi"], Proyek: m["proyek"], Pesan: pesan}, true
}

func hapusLangkahDeploy(userID int64) {
	muStateDeploy.Lock()
	defer muStateDeploy.Unlock()
	delete(stateDeploy, userID)
}

// ==============================================================================
// TEKS PANEL
// ==============================================================================

// menuDeployUtama menampilkan pintu masuk panel deploy.
func menuDeployUtama(userID int64) (string, *InlineKeyboardMarkup) {
	proyek := daftarProyekDeploy(userID)
	aktif := prosesAktifDeploy(userID)

	var jalan int
	for _, p := range proyek {
		if aktif[p] {
			jalan++
		}
	}

	teks := "━━━━━━━━━━━━━━━━━━━━━━━\n" +
		"        🚀 <b>DEPLOY BOT</b> 🚀\n" +
		"━━━━━━━━━━━━━━━━━━━━━━━\n" +
		fmt.Sprintf("📦 <code>Proyek    : %d</code>\n", len(proyek)) +
		fmt.Sprintf("🟢 <code>Berjalan  : %d</code>\n", jalan) +
		fmt.Sprintf("📁 <code>Lokasi    : %s</code>\n", htmlEscapeRingkas(htmlEscapeDir(userID), 60)) +
		"━━━━━━━━━━━━━━━━━━━━━━━\n" +
		"<i>Deploy bot baru per-proyek, kelola terpisah dari bot utama.</i>"

	kb := &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		{
			{Text: "➕ Deploy Baru", CallbackData: "dp:mk"},
			{Text: "📂 Daftar Proyek", CallbackData: "dp:l:0"},
		},
		{
			{Text: "🔗 Dari URL", CallbackData: "dp:u:0"},
			{Text: "🐙 Dari GitHub", CallbackData: "dp:g"},
		},
		{
			{Text: "🔄 Refresh", CallbackData: "dp:m"},
			{Text: "🏠 Menu Utama", CallbackData: "hp:h"},
		},
	}}

	return teks, kb
}

// htmlEscapeDir menyingkat jalur direktori agar tidak memenuhi layar.
func htmlEscapeDir(userID int64) string {
	home := getHomeDir()
	dir := filepath.Join(DirDeploy, strconv.FormatInt(userID, 10))
	if home != "" {
		if rel, err := filepath.Rel(home, dir); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + rel
		}
	}
	return dir
}

// panduanDeployBaru menjelaskan tiga cara menambah proyek.
func panduanDeployBaru() (string, *InlineKeyboardMarkup) {
	teks := "━━━━━━━━━━━━━━━━━━━━━━━\n" +
		"      📥 <b>DEPLOY BARU</b> 📥\n" +
		"━━━━━━━━━━━━━━━━━━━━━━━\n" +
		"<b>1. Kirim berkas</b>\n" +
		"└─➤ <i>Langsung kirim .py .js .zip .tar.gz, atau binary</i>\n" +
		"<b>2. Kirim tautan</b>\n" +
		"└─➤ <i>URL unduhan langsung ke berkas</i>\n" +
		"<b>3. Repository GitHub</b>\n" +
		"└─➤ <i>pemilik/repo atau URL .git penuh</i>\n" +
		"━━━━━━━━━━━━━━━━━━━━━━━\n" +
		"Bahasa dikenali otomatis:\n" +
		"🐍 Python · 🟢 Node.js · 🔵 Go · ⚙️ Binary · 📜 Shell"

	kb := &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		{
			{Text: "🔗 Kirim URL", CallbackData: "dp:u:0"},
			{Text: "🐙 GitHub", CallbackData: "dp:g"},
		},
		{
			{Text: "🔙 Kembali", CallbackData: "dp:m"},
		},
	}}

	return teks, kb
}

// daftarProyekPanel menampilkan daftar proyek dengan navigasi halaman.
func daftarProyekPanel(userID int64, halaman int) (string, *InlineKeyboardMarkup) {
	proyek := daftarProyekDeploy(userID)
	aktif := prosesAktifDeploy(userID)

	if len(proyek) == 0 {
		teks := "━━━━━━━━━━━━━━━━━━━━━━━\n" +
			"        📂 <b>DAFTAR PROYEK</b> 📂\n" +
			"━━━━━━━━━━━━━━━━━━━━━━━\n" +
			"<i>Belum ada proyek.</i>\n\n" +
			"Kirim berkas, tautan, atau alamat GitHub untuk memulai."
		kb := &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
			{{Text: "➕ Deploy Baru", CallbackData: "dp:mk"}},
			{{Text: "🔙 Kembali", CallbackData: "dp:m"}},
		}}
		return teks, kb
	}

	totalHalaman := (len(proyek) + perHalamanDeploy - 1) / perHalamanDeploy
	if halaman < 0 {
		halaman = 0
	}
	if halaman >= totalHalaman {
		halaman = totalHalaman - 1
	}

	mulai := halaman * perHalamanDeploy
	akhir := mulai + perHalamanDeploy
	if akhir > len(proyek) {
		akhir = len(proyek)
	}

	var b strings.Builder
	b.WriteString("━━━━━━━━━━━━━━━━━━━━━━━\n")
	b.WriteString("        📂 <b>DAFTAR PROYEK</b> 📂\n")
	b.WriteString("━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(&b, "<i>Halaman %d dari %d</i>\n\n", halaman+1, totalHalaman)

	var baris [][]InlineKeyboardButton

	for _, nama := range proyek[mulai:akhir] {
		ikon := "🔴"
		if aktif[nama] {
			ikon = "🟢"
		}
		bahasa := deteksiBahasa(filepath.Join(dirDeployPenggunaWajib(userID), nama))
		label := labelBahasa(bahasa)

		fmt.Fprintf(&b, "%s <code>%s</code>\n└─➤ <i>%s</i>\n",
			ikon, htmlEscapeRingkas(nama, 32), htmlEscapeRingkas(label, 20))

		baris = append(baris, []InlineKeyboardButton{{
			Text:         ikon + " " + nama,
			CallbackData: "dp:c:" + nama,
		}})
	}

	// Navigasi halaman.
	var nav []InlineKeyboardButton
	if halaman > 0 {
		nav = append(nav, InlineKeyboardButton{
			Text: "⬅️", CallbackData: fmt.Sprintf("dp:l:%d", halaman-1)})
	}
	nav = append(nav, InlineKeyboardButton{
		Text: "🔙 Kembali", CallbackData: "dp:m"})
	if halaman < totalHalaman-1 {
		nav = append(nav, InlineKeyboardButton{
			Text: "➡️", CallbackData: fmt.Sprintf("dp:l:%d", halaman+1)})
	}
	baris = append(baris, nav)

	return b.String(), &InlineKeyboardMarkup{InlineKeyboard: baris}
}

// panelKontrolDeploy menampilkan status satu proyek beserta tombol kendalinya.
func panelKontrolDeploy(userID int64, nama string) (string, *InlineKeyboardMarkup) {
	dir := filepath.Join(dirDeployPenggunaWajib(userID), nama)
	st := infoProsesDeploy(userID, nama)
	bahasa := deteksiBahasa(dir)
	entry := cariEntrypoint(dir, bahasa)

	ikon := "🔴"
	if st.Jalan {
		ikon = "🟢"
	}

	entryTampil := entry
	if entryTampil == "" {
		entryTampil = "(belum ditentukan)"
	}

	teks := "━━━━━━━━━━━━━━━━━━━━━━━\n" +
		"        🛠️ <b>KONTROL PROYEK</b> 🛠️\n" +
		"━━━━━━━━━━━━━━━━━━━━━━━\n" +
		fmt.Sprintf("📦 <code>Proyek   : %s</code>\n", htmlEscapeRingkas(nama, 40)) +
		fmt.Sprintf("%s <code>Status   : %s</code>\n", ikon, statusDeploy(st)) +
		fmt.Sprintf("🔤 <code>Bahasa   : %s</code>\n", htmlEscapeRingkas(labelBahasa(bahasa), 30)) +
		fmt.Sprintf("▶️ <code>Entry    : %s</code>\n", htmlEscapeRingkas(entryTampil, 40)) +
		fmt.Sprintf("⏱ <code>Uptime   : %s</code>\n", st.Uptime) +
		fmt.Sprintf("💾 <code>RAM      : %s</code>\n", st.RAM) +
		fmt.Sprintf("⚙️ <code>CPU      : %s</code>\n", st.CPU) +
		"━━━━━━━━━━━━━━━━━━━━━━━"

	var kendali []InlineKeyboardButton
	if st.Jalan {
		kendali = []InlineKeyboardButton{
			{Text: "🛑 Stop", CallbackData: "dp:stop:" + nama},
			{Text: "🔄 Restart", CallbackData: "dp:rst:" + nama},
		}
	} else {
		kendali = []InlineKeyboardButton{
			{Text: "▶️ Start", CallbackData: "dp:run:" + nama},
		}
	}

	kb := &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		kendali,
		{
			{Text: "📜 Log", CallbackData: "dp:log:" + nama},
			{Text: "🔤 Bahasa", CallbackData: "dp:lang:" + nama},
		},
		{
			{Text: "▶️ Entry Point", CallbackData: "dp:setent:" + nama},
		},
		{
			{Text: "🗑 Hapus", CallbackData: "dp:del:" + nama},
			{Text: "🔙 Kembali", CallbackData: fmt.Sprintf("dp:l:0")},
		},
	}}

	return teks, kb
}

func statusDeploy(st statusProsesDeploy) string {
	if st.Jalan {
		return fmt.Sprintf("Running (PID %d)", st.PID)
	}
	return "Stopped"
}

// panelPilihBahasa menawarkan penentuan bahasa manual.
//
// Berguna saat tebakan otomatis salah, misalnya proyek berisi binary sekaligus
// beberapa skrip bantu, atau nama berkas tidak mengikuti kebiasaan umum.
func panelPilihBahasa(userID int64, nama string) (string, *InlineKeyboardMarkup) {
	dir := filepath.Join(dirDeployPenggunaWajib(userID), nama)
	sekarang := deteksiBahasa(dir)
	manual := bacaPenanda(dir, penandaBahasaDeploy)

	ket := "(otomatis)"
	if manual != "" {
		ket = "(manual)"
	}

	teks := "━━━━━━━━━━━━━━━━━━━━━━━\n" +
		"        🔤 <b>PILIH BAHASA</b> 🔤\n" +
		"━━━━━━━━━━━━━━━━━━━━━━━\n" +
		fmt.Sprintf("📦 <code>Proyek : %s</code>\n", htmlEscapeRingkas(nama, 40)) +
		fmt.Sprintf("🔍 <code>Terdeteksi : %s %s</code>\n", labelBahasa(sekarang), ket) +
		"━━━━━━━━━━━━━━━━━━━━━━━\n" +
		"<i>Tentukan manual bila deteksi otomatis keliru.</i>"

	kb := &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		{
			{Text: "🐍 Python", CallbackData: "dp:setlang:" + nama + ":python"},
			{Text: "🟢 Node.js", CallbackData: "dp:setlang:" + nama + ":node"},
		},
		{
			{Text: "🔵 Go", CallbackData: "dp:setlang:" + nama + ":go"},
			{Text: "📜 Shell", CallbackData: "dp:setlang:" + nama + ":shell"},
		},
		{
			{Text: "⚙️ Binary", CallbackData: "dp:setlang:" + nama + ":biner"},
		},
		{
			{Text: "🤖 Ikuti Deteksi Otomatis", CallbackData: "dp:setlang:" + nama + ":auto"},
			{Text: "🔙 Kembali", CallbackData: "dp:c:" + nama},
		},
	}}

	return teks, kb
}

// panelPilihEntry menampilkan daftar berkas untuk dipilih sebagai entry point.
func panelPilihEntry(userID int64, nama string) (string, *InlineKeyboardMarkup) {
	dir := filepath.Join(dirDeployPenggunaWajib(userID), nama)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "❌ Gagal membaca direktori proyek.", kbKembaliKontrol(nama)
	}

	bahasa := deteksiBahasa(dir)
	sekarang := cariEntrypoint(dir, bahasa)

	var b strings.Builder
	b.WriteString("━━━━━━━━━━━━━━━━━━━━━━━\n")
	b.WriteString("      ▶️ <b>PILIH ENTRY POINT</b> ▶️\n")
	b.WriteString("━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(&b, "📦 <code>Proyek : %s</code>\n", htmlEscapeRingkas(nama, 40))
	fmt.Fprintf(&b, "🔤 <code>Bahasa : %s</code>\n", htmlEscapeRingkas(labelBahasa(bahasa), 30))
	fmt.Fprintf(&b, "▶️ <code>Kini   : %s</code>\n", htmlEscapeRingkas(sekarang, 40))
	b.WriteString("━━━━━━━━━━━━━━━━━━━━━━━\n")
	b.WriteString("<i>Pilih berkas yang akan dijalankan.</i>")

	var baris [][]InlineKeyboardButton
	var nav []InlineKeyboardButton

	// Batasi agar keyboard tidak melebihi batas Telegram.
	maks := 12
	dihitung := 0
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// Hanya tawarkan berkas yang masuk akal sebagai entry point.
		if !berkasLayakEntry(e.Name(), bahasa) {
			continue
		}
		if dihitung >= maks {
			break
		}
		dihitung++

		tanda := ""
		if e.Name() == sekarang {
			tanda = "✅ "
		}
		baris = append(baris, []InlineKeyboardButton{{
			Text:         tanda + e.Name(),
			CallbackData: "dp:setentry:" + nama + ":" + e.Name(),
		}})
	}

	if dihitung == 0 {
		b.WriteString("\n\n⚠️ <i>Tidak ada berkas yang cocok untuk bahasa ini.</i>")
	}

	nav = append(nav, InlineKeyboardButton{
		Text: "🔙 Kembali", CallbackData: "dp:c:" + nama})
	baris = append(baris, nav)

	return b.String(), &InlineKeyboardMarkup{InlineKeyboard: baris}
}

// berkasLayakEntry menyaring berkas yang masuk akal jadi entry point.
func berkasLayakEntry(nama string, bahasa string) bool {
	lower := strings.ToLower(nama)
	switch bahasa {
	case bahasaPython:
		return strings.HasSuffix(lower, ".py")
	case bahasaNode:
		return strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".mjs") || strings.HasSuffix(lower, ".cjs")
	case bahasaGo:
		return strings.HasSuffix(lower, ".go")
	case bahasaShell:
		return strings.HasSuffix(lower, ".sh") || !strings.Contains(lower, ".")
	case bahasaBiner:
		// Semua berkas ditawarkan: binary sering tanpa ekstensi.
		return true
	}
	return true
}

func kbKembaliKontrol(nama string) *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		{{Text: "🔙 Kembali", CallbackData: "dp:c:" + nama}},
	}}
}

// ==============================================================================
// HASIL DEPLOY
// ==============================================================================

// laporanHasilDeploy menyusun pesan sesudah deploy selesai.
func laporanHasilDeploy(userID int64, nama string) (string, *InlineKeyboardMarkup) {
	dir := filepath.Join(dirDeployPenggunaWajib(userID), nama)
	bahasa := deteksiBahasa(dir)
	entry := cariEntrypoint(dir, bahasa)

	// Jumlah berkas membantu pengguna memastikan isi arsip terurai benar.
	jumlah := 0
	ukuran := int64(0)
	filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if strings.HasPrefix(info.Name(), ".deploy.") {
			return nil
		}
		jumlah++
		ukuran += info.Size()
		return nil
	})

	entryTampil := entry
	if entryTampil == "" {
		entryTampil = "(belum ditentukan)"
	}

	teks := "━━━━━━━━━━━━━━━━━━━━━━━\n" +
		"      ✅ <b>DEPLOY BERHASIL</b> ✅\n" +
		"━━━━━━━━━━━━━━━━━━━━━━━\n" +
		fmt.Sprintf("📦 <code>Proyek : %s</code>\n", htmlEscapeRingkas(nama, 40)) +
		fmt.Sprintf("🔤 <code>Bahasa : %s</code>\n", htmlEscapeRingkas(labelBahasa(bahasa), 30)) +
		fmt.Sprintf("▶️ <code>Entry  : %s</code>\n", htmlEscapeRingkas(entryTampil, 40)) +
		fmt.Sprintf("📄 <code>Berkas : %d (%s)</code>\n", jumlah, formatUkuran(ukuran)) +
		"━━━━━━━━━━━━━━━━━━━━━━━\n" +
		"<i>Tekan Start untuk menjalankan.</i>"

	kb := &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		{
			{Text: "▶️ Start", CallbackData: "dp:run:" + nama},
			{Text: "🛠️ Kontrol", CallbackData: "dp:c:" + nama},
		},
		{
			{Text: "🔙 Daftar", CallbackData: "dp:l:0"},
		},
	}}

	return teks, kb
}

// ==============================================================================
// UTILITAS TAMPILAN
// ==============================================================================

// potongKiri memotong teks dari kiri, dipakai menampilkan ekor log.
func potongKiri(s string, maks int) string {
	if len(s) <= maks {
		return s
	}
	return "…" + s[len(s)-maks:]
}

// daftarBahasaSemua mengembalikan seluruh kode bahasa, terurut.
func daftarBahasaSemua() []string {
	h := []string{bahasaBiner, bahasaGo, bahasaNode, bahasaPython, bahasaShell}
	sort.Strings(h)
	return h
}

// waktuSekarang menghasilkan cap waktu ringkas untuk judul log.
func waktuSekarang() string {
	return time.Now().Format("02 Jan 2006 15:04:05")
}
