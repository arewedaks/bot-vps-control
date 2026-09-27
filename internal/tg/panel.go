package tg

import "sync"

// ==============================================================================
// 🪟 PANEL TUNGGAL — MENCEGAH CHAT PENUH
// ==============================================================================
//
// Masalah: setiap perintah /term sebelumnya mengirim pesan BARU, sehingga chat
// penuh setelah beberapa perintah. Solusinya sama seperti file manager: satu
// pesan "panel" per admin yang isinya ditulis ulang (editMessageText).
//
// Aturan:
//   - Output panjang (hasil perintah, panel terminal) → SELALU ke panel.
//   - Pesan singkat (konfirmasi, error) → kirim sekali, dan hapus pesan
//     panel lama supaya tidak menumpuk.
//   - Notifikasi penting (startup, reboot) → tetap pesan baru.

type panelState struct {
	MessageID int64
	ChatID    int64
}

var (
	panelMu     sync.Mutex
	panelByUser = make(map[int64]*panelState)
)

// setPanelMessage mencatat pesan mana yang dipakai sebagai panel untuk admin.
func SetPanelMessage(userID, chatID, msgID int64) {
	panelMu.Lock()
	panelByUser[userID] = &panelState{MessageID: msgID, ChatID: chatID}
	panelMu.Unlock()
}

// getPanelMessage mengambil panel aktif milik admin.
func GetPanelMessage(userID int64) (int64, int64, bool) {
	panelMu.Lock()
	defer panelMu.Unlock()
	p, ok := panelByUser[userID]
	if !ok {
		return 0, 0, false
	}
	return p.MessageID, p.ChatID, true
}

// sendPanel mengirim panel BARU dan mengingatnya. Dipakai saat belum ada panel.
func SendPanel(userID, chatID int64, text string, kb *InlineKeyboardMarkup) {
	msgID := SendMessageReturningID(chatID, text, kb)
	if msgID > 0 {
		SetPanelMessage(userID, chatID, msgID)
	}
}

// updatePanel menulis ulang panel yang ada. Bila belum ada panel, buat baru.
// Ini yang membuat chat tidak penuh: satu pesan, ditulis ulang terus.
func UpdatePanel(userID, chatID int64, text string, kb *InlineKeyboardMarkup) {
	msgID, _, ok := GetPanelMessage(userID)
	if !ok {
		SendPanel(userID, chatID, text, kb)
		return
	}
	if !EditTelegramMessage(chatID, msgID, text, kb) {
		// Pesan sudah dihapus / terlalu lama → kirim panel baru.
		ClearPanel(userID)
		SendPanel(userID, chatID, text, kb)
	}
}

// clearPanel melupakan panel (mis. setelah dihapus atau sesi ditutup).
func ClearPanel(userID int64) {
	panelMu.Lock()
	delete(panelByUser, userID)
	panelMu.Unlock()
}

// deletePanel menghapus pesan panel dan melupakannya. Dipakai sebelum
// mengirim pesan singkat supaya chat tidak menumpuk pesan lama.
func DeletePanel(userID int64) {
	msgID, chatID, ok := GetPanelMessage(userID)
	if !ok {
		return
	}
	DeleteTelegramMessage(chatID, msgID)
	ClearPanel(userID)
}

// sendOnce mengirim pesan singkat: hapus panel lama, kirim pesan ini sekali.
// Hasilnya chat tetap bersih — hanya pesan terbaru yang terlihat.
func SendOnce(userID, chatID int64, text string) {
	DeletePanel(userID)
	SendTelegram(chatID, text)
}
