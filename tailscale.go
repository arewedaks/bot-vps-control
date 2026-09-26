package main

import (
	"fmt"
	"html"
	"os"
	"os/user"
	"strings"
	"unicode/utf8"
)

// ==============================================================================
// 🦎 TAILSCALE MENU — TOMBOL INTERAKTIF
// ==============================================================================
// Tujuan: menghilangkan kebutuhan menghapal sub-perintah, menampilkan status
// yang mudah dibaca, dan menutup dua celah keamanan:
//
//   1. Auth key tidak lagi ditampilkan di riwayat chat. `/ts up` tanpa argumen
//      memakai alur Web Login (link), sehingga rahasia tidak pernah diketik
//      ke Telegram.
//   2. `/ts down` (memutus akses remote ke VPS) sekarang butuh konfirmasi.

// tsStatusRingkas menghasilkan ringkasan status yang enak dibaca Telegram.
// Berbeda dari `tailscale status` mentah yang bisa puluhan baris, fungsi ini
// mengelompokkan peer menjadi online/offline dan membatasi jumlah tampilan.
func tsStatusRingkas() string {
	var b strings.Builder

	installed := isTailscaleInstalled()
	if !installed {
		b.WriteString("🦎 <b>Tailscale</b>\n━━━━━━━━━━━━━━━━━━━━\n")
		b.WriteString("❌ <b>Belum terinstall</b>\n\n")
		b.WriteString("<i>Ketik tombol ⬇️ Install di bawah untuk memasang otomatis.</i>")
		return b.String()
	}

	// Pastikan daemon hidup sebelum bertanya status.
	ensureTailscaledRunning()

	tsIP4 := getTailscaleIP()

	b.WriteString("🦎 <b>Tailscale</b>\n━━━━━━━━━━━━━━━━━━━━\n")

	if tsIP4 == "" {
		b.WriteString("🔴 <b>Status:</b> <i>Terputus (offline)</i>\n\n")
		b.WriteString("<i>Hubungkan dengan tombol 🔗 Connect di bawah.</i>")
		return b.String()
	}

	b.WriteString(fmt.Sprintf("🟢 <b>Status:</b> <i>Terhubung</i>\n"))
	b.WriteString(fmt.Sprintf("🌐 <b>IP:</b> <code>%s</code>\n", html.EscapeString(tsIP4)))

	if _, ip6, _ := runBashCommand(getTailscaleCmd("ip -6"), 5); strings.TrimSpace(ip6) != "" {
		b.WriteString(fmt.Sprintf("🌐 <b>IPv6:</b> <code>%s</code>\n", html.EscapeString(strings.TrimSpace(ip6))))
	}

	// Ambil daftar peer dan ringkas.
	_, stOut, _ := runBashCommand(getTailscaleCmd("status"), 10)
	online, offline, self := tsCountPeers(stOut)

	b.WriteString(fmt.Sprintf("👥 <b>Peer:</b> <code>%d online</code>", online))
	if offline > 0 {
		b.WriteString(fmt.Sprintf(", <code>%d offline</code>", offline))
	}
	b.WriteString("\n")

	if self != "" {
		b.WriteString(fmt.Sprintf("🖥 <b>Host ini:</b> <code>%s</code>\n", html.EscapeString(self)))
	}

	b.WriteString("\n<i>Detail lengkap: tombol 📋 Status Detail.</i>")
	return b.String()
}

// tsCountPeers menghitung peer online/offline dari output `tailscale status`.
// Juga mengembalikan nama host ini sendiri bila bisa dikenali.
//
// Format baris peer (dipisah spasi/tab):
//
//	<IP> <hostname> <user> <os> <status...>
//
// Kolom status bernilai "-" untuk peer yang online, atau
// "offline, last seen ..." untuk peer yang tidak aktif.
func tsCountPeers(out string) (online int, offline int, self string) {
	// Tentukan dulu nama host ini lewat sumber yang otoritatif, bukan tebakan
	// dari pola status — "-" ambigu antara "online" dan "tanpa status".
	self = tsSelfHostname()

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		// Hanya hitung baris yang diawali IP Tailscale.
		if !strings.HasPrefix(parts[0], "100.") && !strings.HasPrefix(parts[0], "fd7a:") {
			continue
		}
		if strings.Contains(line, "offline") {
			offline++
		} else {
			online++
		}
	}
	return
}

// tsSelfHostname mengembalikan hostname Tailscale milik mesin ini.
// Memakai `tailscale status --self --peers=false` bila tersedia; jika tidak,
// jatuh ke `tailscale status --json` yang selalu ada di versi modern.
func tsSelfHostname() string {
	// Cara ringan dan stabil: minta JSON, ambil Self.HostName.
	_, out, _ := runBashCommand(getTailscaleCmd("status --json"), 8)
	if out == "" {
		return ""
	}
	// Parsing minimal tanpa dependency: cari "HostName" di blok Self.
	// Blok Self selalu muncul sebelum blok Peer pada output tailscale.
	selfIdx := strings.Index(out, `"Self":`)
	if selfIdx < 0 {
		return ""
	}
	rest := out[selfIdx:]
	// Batasi pencarian sampai awal blok "Peer" supaya tidak tertukar.
	if peerIdx := strings.Index(rest, `"Peer":`); peerIdx > 0 {
		rest = rest[:peerIdx]
	}
	key := `"HostName":`
	i := strings.Index(rest, key)
	if i < 0 {
		return ""
	}
	rest = rest[i+len(key):]
	// Lewati spasi, lalu ambil isi tanda kutip.
	rest = strings.TrimLeft(rest, " \t")
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// tsDetailStatus mengembalikan output `tailscale status` mentah yang sudah
// dibersihkan, untuk dikirim sebagai pesan atau lampiran file.
func tsDetailStatus() (string, error) {
	if !isTailscaleInstalled() {
		return "", fmt.Errorf("Tailscale belum terinstall")
	}
	ensureTailscaledRunning()
	_, out, errStr := runBashCommand(getTailscaleCmd("status"), 10)
	if strings.TrimSpace(out) == "" {
		out = errStr
	}
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("Tidak ada output dari perintah status")
	}
	return out, nil
}

// tsMenuKeyboard menyusun tombol menu Tailscale.
// Tombolnya berubah sesuai keadaan: belum install / terputus / terhubung.
func tsMenuKeyboard() *InlineKeyboardMarkup {
	installed := isTailscaleInstalled()
	connected := installed && getTailscaleIP() != ""

	var rows [][]InlineKeyboardButton

	if !installed {
		// Belum install: satu tombol utama.
		rows = append(rows,
			[]InlineKeyboardButton{
				{Text: "⬇️ Install Tailscale", CallbackData: "ts:i"},
			},
			[]InlineKeyboardButton{
				{Text: "⬅️ Kembali", CallbackData: "hp:b"},
			},
		)
	} else if !connected {
		// Terinstall tapi belum terhubung.
		rows = append(rows,
			[]InlineKeyboardButton{
				{Text: "🔗 Connect (Web Login)", CallbackData: "ts:u"},
			},
			[]InlineKeyboardButton{
				{Text: "📋 Status Detail", CallbackData: "ts:s"},
				{Text: "🔄 Refresh", CallbackData: "ts:r"},
			},
			[]InlineKeyboardButton{
				{Text: "⬅️ Kembali", CallbackData: "hp:b"},
			},
		)
	} else {
		// Terhubung: tampilkan aksi yang relevan.
		rows = append(rows,
			[]InlineKeyboardButton{
				{Text: "📋 Status Detail", CallbackData: "ts:s"},
				{Text: "🔄 Refresh", CallbackData: "ts:r"},
			},
			[]InlineKeyboardButton{
				{Text: "🌐 IP & Hostname", CallbackData: "ts:p"},
				{Text: "📥 Set Auth Key", CallbackData: "ts:k"},
			},
			[]InlineKeyboardButton{
				{Text: "🔒 SSH: On", CallbackData: "ts:hon"},
				{Text: "🔓 SSH: Off", CallbackData: "ts:hoff"},
			},
			[]InlineKeyboardButton{
				{Text: "🛑 Disconnect", CallbackData: "ts:d"},
				{Text: "⬅️ Kembali", CallbackData: "hp:b"},
			},
		)
	}

	return &InlineKeyboardMarkup{InlineKeyboard: rows}
}

// tsSubKeyboard adalah keyboard untuk layar DETAIL (status, IP, auth key).
//
// Berbeda dari tsMenuKeyboard, layar ini punya tombol "⬅️ Kembali" yang jelas
// kembali ke menu Tailscale — sebelumnya pengguna harus menebak bahwa
// "🔄 Refresh" adalah satu-satunya jalan pulang.
func tsSubKeyboard() *InlineKeyboardMarkup {
	return &InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: "⬅️ Kembali", CallbackData: "ts:r"},
				{Text: "🏠 Menu Utama", CallbackData: "hp:b"},
			},
		},
	}
}

// ==============================================================================
// 🎛️ HANDLER CALLBACK TAILSCALE (ts:*)
// ==============================================================================

// handleTailscaleCallback menjalankan aksi dari tombol menu Tailscale.
// Mengembalikan pesan panel terbaru supaya pemanggil bisa mengedit pesannya.
func handleTailscaleCallback(userID int64, chatID int64, action string) (string, *InlineKeyboardMarkup) {

	switch action {
	case "r", "": // Refresh — hanya render ulang menu.
		return tsStatusRingkas() + "\n\n" + tsMenuFooter(), tsMenuKeyboard()

	case "i": // Install
		if isTailscaleInstalled() {
			ensureTailscaledRunning()
			return "✅ <b>Tailscale sudah terinstall.</b>\n\n" + tsStatusRingkas(), tsMenuKeyboard()
		}
		code, out, errStr := runBashCommand(
			"curl -fsSL https://tailscale.com/install.sh | sh && "+
				"(systemctl enable --now tailscaled 2>/dev/null || service tailscaled start 2>/dev/null)",
			180)
		ensureTailscaledRunning()

		if code == 0 && isTailscaleInstalled() {
			return "🎉 <b>Tailscale berhasil diinstall!</b>\n\n" + tsStatusRingkas(), tsMenuKeyboard()
		}
		detail := strings.TrimSpace(out + "\n" + errStr)
		if len(detail) > 1200 {
			detail = detail[:1200] + "\n… (dipotong)"
		}
		return "❌ <b>Gagal menginstall Tailscale.</b>\n<pre>" +
			html.EscapeString(detail) + "</pre>\n\n" +
			"<i>Periksa koneksi internet VPS dan hak akses root.</i>", tsMenuKeyboard()

	case "u": // Connect / up — Tanpa auth key agar rahasia tidak masuk chat.
		if !isTailscaleInstalled() {
			return "❌ <b>Tailscale belum terinstall.</b>", tsMenuKeyboard()
		}
		ensureTailscaledRunning()

		code, out, errStr := runBashCommand(
			getTailscaleCmd("up --accept-routes --ssh --reset"), 35)
		combined := out + "\n" + errStr

		if url := tsExtractLoginURL(combined); url != "" {
			return fmt.Sprintf(
				"🔗 <b>Autentikasi Diperlukan</b>\n━━━━━━━━━━━━━━━━━━━━\n\n"+
					"Buka tautan ini di browser untuk mengotorisasi VPS ini:\n\n"+
					"👉 <a href=\"%s\"><b>Login ke Tailscale</b></a>\n\n"+
					"<i>Setelah disetujui, tekan tombol 🔄 Refresh.</i>",
				html.EscapeString(url)), tsMenuKeyboard()
		}
		if code == 0 {
			return "✅ <b>Tailscale berhasil terhubung!</b>\n\n" + tsStatusRingkas(), tsMenuKeyboard()
		}
		return "⚠️ <b>Respons Tailscale:</b>\n<pre>" +
			html.EscapeString(truncateStr(combined, 1000)) + "</pre>", tsMenuKeyboard()

	case "k": // Panduan set auth key (tidak menampilkan key di chat)
		return "🔑 <b>Menghubungkan dengan Auth Key (Lebih Aman)</b>\n" +
				"━━━━━━━━━━━━━━━━━━━━\n\n" +
				"Auth key adalah <b>rahasia</b>. Mengirimnya sebagai pesan Telegram " +
				"berarti key itu tersimpan di riwayat chat dan log.\n\n" +
				"<b>Cara aman:</b>\n" +
				"1. Buat auth key di <a href=\"https://login.tailscale.com/admin/settings/keys\">Tailscale Admin</a>\n" +
				"2. Jalankan lewat terminal bot:\n" +
				"   <code>/term tailscale up --auth-key=tskey-auth-...</code>\n" +
				"3. Atau tanam ke file lalu hapus:\n" +
				"   <code>/term tailscale up --auth-key=$(cat /root/.tskey)</code>\n\n" +
				"<i>Lebih mudah: pakai tombol 🔗 Connect yang memakai Web Login.</i>",
			tsSubKeyboard()

	case "s": // Status detail
		detail, err := tsDetailStatus()
		if err != nil {
			return "❌ " + html.EscapeString(err.Error()), tsSubKeyboard()
		}
		// Output panjang dikirim sebagai file, bukan membanjiri chat.
		if len(detail) > 3000 {
			return "📋 <b>Status Detail</b>\n\nOutput terlalu panjang untuk chat " +
				"(" + fmt.Sprintf("%d", len(detail)) + " karakter).\n" +
				"Ketik <code>/ts status raw</code> untuk menerimanya sebagai file.", tsSubKeyboard()
		}
		return "📋 <b>Status Detail</b>\n━━━━━━━━━━━━━━━━━━━━\n<pre>" +
			html.EscapeString(detail) + "</pre>", tsSubKeyboard()

	case "p": // IP & hostname
		if !isTailscaleInstalled() {
			return "❌ <b>Tailscale belum terinstall.</b>", tsSubKeyboard()
		}
		hostname, _ := os.Hostname()
		tsIP4 := getTailscaleIP()
		_, tsIP6, _ := runBashCommand(getTailscaleCmd("ip -6"), 5)

		text := "🌐 <b>IP & Hostname</b>\n━━━━━━━━━━━━━━━━━━━━\n"
		text += fmt.Sprintf("• <b>Tailscale IPv4:</b> <code>%s</code>\n", html.EscapeString(orDash(tsIP4)))
		text += fmt.Sprintf("• <b>Tailscale IPv6:</b> <code>%s</code>\n", html.EscapeString(orDash(strings.TrimSpace(tsIP6))))
		text += fmt.Sprintf("• <b>Hostname:</b> <code>%s</code>\n", html.EscapeString(hostname))
		if tsIP4 != "" {
			text += fmt.Sprintf("\n💻 <b>SSH via Tailscale:</b>\n<code>ssh %s@%s</code>",
				html.EscapeString(getCurrentUser()), html.EscapeString(tsIP4))
		}
		return text, tsSubKeyboard()

	case "hon": // SSH on
		return tsSetSSH(true), tsMenuKeyboard()

	case "hoff": // SSH off
		return tsSetSSH(false), tsMenuKeyboard()

	case "d": // Disconnect — butuh konfirmasi (memutus akses remote!)
		return "🛑 <b>Putuskan Tailscale?</b>\n" +
				"━━━━━━━━━━━━━━━━━━━━\n\n" +
				"⚠️ <b>Perhatian:</b> jika kamu mengakses VPS ini <b>hanya</b> lewat " +
				"Tailscale, memutuskan koneksi berarti kamu <b>kehilangan akses</b> " +
				"sampai bisa masuk lagi lewat jalur lain.\n\n" +
				"Pastikan kamu punya akses SSH publik sebagai cadangan.\n\n" +
				"Tekan tombol di bawah untuk melanjutkan.",
			&InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
				{
					{Text: "✅ Ya, Putuskan", CallbackData: "ts:dy"},
					{Text: "❌ Batal", CallbackData: "ts:r"},
				},
			}}

	case "dy": // Konfirmasi disconnect
		if !isTailscaleInstalled() {
			return "❌ <b>Tailscale belum terinstall.</b>", tsMenuKeyboard()
		}
		_, out, errStr := runBashCommand(getTailscaleCmd("down"), 10)
		if strings.Contains(out, "Success") || strings.TrimSpace(errStr) == "" {
			return "🛑 <b>Tailscale diputuskan.</b>\n\n" + tsStatusRingkas(), tsMenuKeyboard()
		}
		return "⚠️ <b>Output:</b>\n<pre>" +
			html.EscapeString(truncateStr(out+"\n"+errStr, 800)) + "</pre>", tsMenuKeyboard()

	default:
		return tsStatusRingkas() + "\n\n" + tsMenuFooter(), tsMenuKeyboard()
	}
}

// tsSetSSH mengaktifkan atau menonaktifkan Tailscale SSH.
func tsSetSSH(on bool) string {
	if !isTailscaleInstalled() {
		return "❌ <b>Tailscale belum terinstall.</b>"
	}
	ensureTailscaledRunning()
	mode := "false"
	label := "dinonaktifkan"
	if on {
		mode = "true"
		label = "diaktifkan"
	}
	_, out, errStr := runBashCommand(getTailscaleCmd(fmt.Sprintf("set --ssh=%s", mode)), 10)

	text := fmt.Sprintf("🔒 <b>Tailscale SSH %s.</b>\n\n", label)
	if strings.TrimSpace(out) != "" {
		text += "<pre>" + html.EscapeString(truncateStr(out, 500)) + "</pre>"
	} else if strings.TrimSpace(errStr) != "" {
		text += "⚠️ <pre>" + html.EscapeString(truncateStr(errStr, 500)) + "</pre>"
	}
	return text + "\n\n" + tsStatusRingkas()
}

// tsMenuFooter memberi petunjuk singkat di bawah menu.
func tsMenuFooter() string {
	return "<i>Pilih aksi lewat tombol di bawah. " +
		"Perintah manual: <code>/ts install|up|status|ip|ssh|down</code></i>"
}

// truncateStr memotong string di batas aman (tidak di tengah rune UTF-8).
func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.ValidString(s[:cut]) {
		cut--
	}
	return s[:cut] + "\n… (dipotong)"
}

// tsCallbackText menjalankan aksi callback Tailscale dan mengembalikan teksnya saja.
// Dipakai oleh handler perintah teks (/ts ...) yang tidak butuh keyboard baru.
func tsCallbackText(userID int64, chatID int64, action string) string {
	text, _ := handleTailscaleCallback(userID, chatID, action)
	return text
}

// tsExtractLoginURL mencari link autentikasi pada output tailscale.
func tsExtractLoginURL(s string) string {
	for _, line := range strings.Split(s, "\n") {
		for _, p := range strings.Fields(line) {
			if strings.HasPrefix(p, "https://login.tailscale.com/a/") {
				return p
			}
		}
	}
	return ""
}

// orDash mengganti string kosong dengan tanda "-" agar tampilan tidak menggantung.
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// getCurrentUser mengembalikan username efektif.
func getCurrentUser() string {
	if usr, err := user.Current(); err == nil && usr.Username != "" {
		return usr.Username
	}
	return "root"
}
