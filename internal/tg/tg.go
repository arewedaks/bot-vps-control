// Package tg berisi lapisan klien Telegram: tipe payload, pengiriman pesan,
// unduhan berkas, dan pemotongan pesan panjang.
//
// Dipisah dari package main supaya tiap fitur bisa mengimpornya tanpa
// menarik loop polling dan state panel yang tidak dibutuhkan.
package tg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Konfigurasi koneksi. Diisi package main saat start; diekspor supaya
// lapisan ini tidak ikut membaca environmentvariable sendiri.
var (
	BotToken string
	ApiUrl   string
)

// minInt mengembalikan nilai terkecil. Go 1.22 belum punya min bawaan.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ==============================================================================
// 🤖 TELEGRAM BOT API STRUCTS & HELPERS
// ==============================================================================
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type SendMessagePayload struct {
	ChatID      int64                 `json:"chat_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type EditMessagePayload struct {
	ChatID      int64                 `json:"chat_id"`
	MessageID   int64                 `json:"message_id"`
	Text        string                `json:"text"`
	ParseMode   string                `json:"parse_mode"`
	ReplyMarkup *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

type AnswerCallbackPayload struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
	ShowAlert       bool   `json:"show_alert,omitempty"`
}

type UpdateResponse struct {
	OK          bool     `json:"ok"`
	Result      []Update `json:"result"`
	Description string   `json:"description,omitempty"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

type Document struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileName     string `json:"file_name"`
	MimeType     string `json:"mime_type"`
	FileSize     int64  `json:"file_size"`
}

type Message struct {
	MessageID int64     `json:"message_id"`
	From      *User     `json:"from,omitempty"`
	Chat      *Chat     `json:"chat,omitempty"`
	Text      string    `json:"text,omitempty"`
	Caption   string    `json:"caption,omitempty"`
	Document  *Document `json:"document,omitempty"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data"`
}

type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type Chat struct {
	ID int64 `json:"id"`
}

// sendTelegram mengirim pesan teks standar (HTML) ke Telegram
func SendTelegram(chatID int64, textHTML string) {
	if len(textHTML) > 4000 {
		chunks := SplitMessage(textHTML, 3800)
		for _, ch := range chunks {
			SendSingleMessage(chatID, ch, nil)
			time.Sleep(100 * time.Millisecond)
		}
		return
	}
	SendSingleMessage(chatID, textHTML, nil)
}

// sendTelegramWithKeyboard mengirim pesan HTML dengan tombol Inline Keyboard
func SendTelegramWithKeyboard(chatID int64, textHTML string, keyboard *InlineKeyboardMarkup) {
	SendSingleMessage(chatID, textHTML, keyboard)
}

func SendSingleMessage(chatID int64, textHTML string, keyboard *InlineKeyboardMarkup) {
	payload := SendMessagePayload{
		ChatID:      chatID,
		Text:        textHTML,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("POST", ApiUrl+"/sendMessage", bytes.NewBuffer(jsonData))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("⚠️ Gagal kirim ke Telegram (chat %d): %v\n", chatID, err)
		return
	}
	defer resp.Body.Close()

	// Telegram menolak pesan dengan status != 200 — biasanya karena HTML
	// tidak valid atau teks terlalu panjang. Tanpa pemeriksaan ini,
	// kegagalannya tidak terlihat sama sekali: pengguna hanya melihat
	// "bot diam", padahal ada kesalahan yang bisa diperbaiki.
	//
	// Sangat penting untuk fitur update, karena keluaran git memuat
	// karakter seperti < dan & yang harus di-escape dengan benar.
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 600))
		fmt.Printf("⚠️ Telegram menolak pesan (chat %d, status %d): %s\n",
			chatID, resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

// editTelegramMessage menulis ulang isi pesan yang sudah ada.
// Mengembalikan false bila edit gagal karena pesan tidak ada/lama.
//
// Catatan: error "message is not modified" diperlakukan sebagai BERHASIL —
// teksnya memang sudah sama, jadi tidak perlu kirim ulang.
func EditTelegramMessage(chatID int64, messageID int64, textHTML string, keyboard *InlineKeyboardMarkup) bool {
	payload := EditMessagePayload{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        textHTML,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return false
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("POST", ApiUrl+"/editMessageText", bytes.NewBuffer(jsonData))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode == http.StatusOK
	}

	if resp.StatusCode == http.StatusOK {
		return true
	}

	// "message is not modified" = isi sudah sama. Anggap sukses.
	if bytes.Contains(body, []byte("message is not modified")) {
		return true
	}

	// Pesan sudah dihapus atau terlalu lama untuk diedit — panel perlu dibuat ulang.
	return false
}

// deleteTelegramMessage menghapus pesan. Dipakai untuk menjaga chat tetap bersih.
func DeleteTelegramMessage(chatID int64, messageID int64) {
	if messageID <= 0 {
		return
	}
	payload := map[string]int64{"chat_id": chatID, "message_id": messageID}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", ApiUrl+"/deleteMessage", bytes.NewBuffer(jsonData))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// sendMessageReturningID mengirim pesan dan mengembalikan message_id-nya.
// Diperlukan agar pesan itu bisa di-edit pada perintah berikutnya (pola panel).
func SendMessageReturningID(chatID int64, textHTML string, keyboard *InlineKeyboardMarkup) int64 {
	payload := SendMessagePayload{
		ChatID:      chatID,
		Text:        textHTML,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return 0
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("POST", ApiUrl+"/sendMessage", bytes.NewBuffer(jsonData))
	if err != nil {
		return 0
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	// Telegram membatasi 4096 karakter per pesan.
	// Untuk panel kita potong di 3900 supaya tag HTML tidak terbelah.
	if resp.StatusCode == http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		if bytes.Contains(body, []byte("too long")) {
			trimmed := TruncateHTMLSafe(textHTML, 3900)
			if trimmed != textHTML {
				payload.Text = trimmed
				jsonData, _ = json.Marshal(payload)
				req2, _ := http.NewRequest("POST", ApiUrl+"/sendMessage", bytes.NewBuffer(jsonData))
				req2.Header.Set("Content-Type", "application/json")
				resp2, err2 := client.Do(req2)
				if err2 != nil {
					return 0
				}
				defer resp2.Body.Close()
				return ExtractMessageID(resp2)
			}
		}
		return 0
	}

	return ExtractMessageID(resp)
}

// extractMessageID membaca message_id dari respons Telegram.
func ExtractMessageID(resp *http.Response) int64 {
	var out struct {
		OK     bool `json:"ok"`
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || !out.OK {
		return 0
	}
	return out.Result.MessageID
}

// truncateHTMLSafe memotong teks di batas aman: tidak di tengah tag HTML
// dan tidak di tengah karakter UTF-8.
func TruncateHTMLSafe(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	// Mundur bila kita berada di tengah tag HTML.
	if idx := strings.LastIndex(s[:cut], "<"); idx > strings.LastIndex(s[:cut], ">") {
		cut = idx
	}
	// Mundur ke batas rune yang valid.
	for cut > 0 && !utf8.ValidString(s[:cut]) {
		cut--
	}
	return s[:cut] + "\n…"
}

func AnswerCallbackQuery(callbackQueryID string, alertText string) {
	alertText = strings.TrimSpace(alertText)

	// Popup modal HANYA untuk pesan yang benar-benar harus dibaca pengguna.
	// Teks pendek seperti "⏳", "🔄", "📋" adalah penanda status — menampilkannya
	// sebagai popup berarti pengguna harus mengklik OK setiap kali menekan tombol.
	// Telegram tetap menghentikan animasi tombol walau teks tidak dikirim.
	showAlert := NeedsUserAttention(alertText)

	payload := AnswerCallbackPayload{
		CallbackQueryID: callbackQueryID,
		Text:            alertText,
		ShowAlert:       showAlert,
	}

	// Tanpa popup dan tanpa teks: cukup menghentikan spinner tombol.
	if !showAlert && len([]rune(alertText)) <= 3 {
		payload.Text = ""
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", ApiUrl+"/answerCallbackQuery", bytes.NewBuffer(jsonData))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// needsUserAttention melaporkan apakah pesan callback perlu tampil sebagai popup.
// Hanya pesan yang mengandung kegagalan, penolakan, atau peringatan yang perlu.
// Sisanya cukup jadi toast singkat atau tidak ditampilkan sama sekali.
func NeedsUserAttention(s string) bool {
	if s == "" {
		return false
	}
	penanda := []string{
		"⛔", "❌", "⚠️", "Gagal", "gagal", "Ditolak", "ditolak",
		"Error", "error", "Tidak bisa", "tidak bisa", "Tidak ada",
		"GAGAL", "harus", "Butuh",
	}
	for _, p := range penanda {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// sendTelegramDocument mengirim file dokumen ke Telegram chat
func SendTelegramDocument(chatID int64, filename string, fileBytes []byte, caption string) error {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	if err := w.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return err
	}
	if err := w.WriteField("parse_mode", "HTML"); err != nil {
		return err
	}
	if caption != "" {
		if err := w.WriteField("caption", caption); err != nil {
			return err
		}
	}

	part, err := w.CreateFormFile("document", filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(fileBytes); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest("POST", ApiUrl+"/sendDocument", &b)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// downloadTelegramFile mengunduh file dari Telegram ke penyimpanan VPS
func DownloadTelegramFile(fileID string, destPath string) (int64, error) {
	getFileURL := fmt.Sprintf("%s/getFile?file_id=%s", ApiUrl, fileID)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(getFileURL)
	if err != nil {
		return 0, fmt.Errorf("gagal menghubungi getFile: %w", err)
	}
	defer resp.Body.Close()

	var getFileResp struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
			FileSize int64  `json:"file_size"`
		} `json:"result"`
		Description string `json:"description,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&getFileResp); err != nil || !getFileResp.OK {
		return 0, fmt.Errorf("gagal mendapatkan file path dari Telegram: %s", getFileResp.Description)
	}

	// Host unduhan harus mengikuti ApiUrl yang sama seperti getFile di atas.
	// Hardcode api.telegram.org membuat semua unggahan dan deploy gagal 404
	// saat bot dijalankan memakai Bot API lokal/kustom.
	akhiran := "/bot" + BotToken
	downloadURL := strings.TrimSuffix(ApiUrl, akhiran) +
		"/file" + akhiran + "/" + getFileResp.Result.FilePath

	dlClient := &http.Client{Timeout: 120 * time.Second}
	dlResp, err := dlClient.Get(downloadURL)
	if err != nil {
		return 0, fmt.Errorf("gagal download file dari server Telegram: %w", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("telegram merespons status HTTP %d", dlResp.StatusCode)
	}

	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, fmt.Errorf("gagal membuat folder tujuan: %w", err)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return 0, fmt.Errorf("gagal membuat file di sistem: %w", err)
	}
	defer out.Close()

	written, err := io.Copy(out, dlResp.Body)
	if err != nil {
		return 0, fmt.Errorf("gagal menulis file ke disk: %w", err)
	}

	return written, nil
}

// splitMessage memecah pesan panjang menjadi beberapa bagian.
//
// Telegram membatasi panjang pesan (~4096 karakter). Pemecahan tidak boleh
// membelah sebuah tag HTML: bila tag terbuka di satu bagian dan tertutup di
// bagian berikutnya, Telegram menolak KEDUA bagian dengan "can't parse
// entities", sehingga pengguna tidak melihat apa pun.
//
// Karena itu pemotongan dilakukan di batas tag yang aman:
//   - sebelum '<' dari tag berikutnya, sehingga tag utuh di satu bagian;
//   - bila tidak ada batas yang cocok, potong di karakter aman terakhir
//     agar tidak ada entitas yang terbelah di tengah.
//
// ponytail: hanya menangani batas tag, bukan penyeimbangan tag lintas
// bagian. Bila kelak pesan dibangun dari tag bersarang yang tidak utuh per
// bagian, tambahkan penutup/pembuka otomatis di sini.
func SplitMessage(msg string, chunkSize int) []string {
	if chunkSize <= 0 {
		return []string{msg}
	}
	runes := []rune(msg)
	if len(runes) <= chunkSize {
		return []string{msg}
	}

	var chunks []string
	for i := 0; i < len(runes); {
		sisa := len(runes) - i
		if sisa <= chunkSize {
			chunks = append(chunks, string(runes[i:]))
			break
		}

		end := i + chunkSize

		// Mundur ke awal tag '<' terakhir agar tidak ada tag yang terbelah.
		// Hanya dilakukan bila batasnya tidak terlalu jauh mundur, supaya
		// bagian tetap mendekati ukuran yang diminta.
		batasAman := -1
		for j := end - 1; j > i && j > end-chunkSize/4; j-- {
			if runes[j] == '<' {
				batasAman = j
				break
			}
		}
		if batasAman > i {
			end = batasAman
		} else {
			// Tidak ada tag di dekat batas. Mundur ke spasi terakhir agar
			// kata tidak terbelah dua.
			for j := end - 1; j > i; j-- {
				if runes[j] == ' ' || runes[j] == '\n' {
					end = j + 1
					break
				}
			}
		}

		if end <= i {
			// Jaring pengaman: jangan sampai tidak ada kemajuan.
			end = minInt(i+chunkSize, len(runes))
		}

		chunks = append(chunks, string(runes[i:end]))
		i = end
	}
	return chunks
}
