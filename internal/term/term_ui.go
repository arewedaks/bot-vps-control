package term

import "bot-vps-control/internal/tg"

// BackToHelpKeyboard menyediakan tombol kembali ke bantuan utama.
func BackToHelpKeyboard() *tg.InlineKeyboardMarkup {
	return &tg.InlineKeyboardMarkup{
		InlineKeyboard: [][]tg.InlineKeyboardButton{
			{
				{Text: "⬅️ Kembali ke Bantuan", CallbackData: "hp:b"},
			},
		},
	}
}
