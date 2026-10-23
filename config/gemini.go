package config

import (
	"edukasi-finansial-goedukasi-finansial-go/app/facades"
)

func init() {
	config := facades.Config()
	config.Add("gemini", map[string]any{
		// API Key Google Gemini
		//
		// Ambil dari Google AI Studio (https://aistudio.google.com/apikey)
		// lalu simpan di .env sebagai GEMINI_API_KEY.
		"api_key": config.Env("GEMINI_API_KEY", ""),

		// Model Gemini yang dipakai FINANIKA.
		//
		// gemini-1.5-flash dan gemini-2.5-flash sudah tidak bisa dipakai akun
		// baru, jadi default diarahkan ke model yang direkomendasikan Google.
		"model": config.Env("GEMINI_MODEL", "gemini-3.8-flash"),

		// Base URL REST API Gemini.
		"base_url": config.Env("GEMINI_BASE_URL", "https://generativelanguage.googleapis.com/v1beta"),
	})
}
