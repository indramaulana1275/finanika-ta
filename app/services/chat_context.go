package services

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"edukasi-finansial-goedukasi-finansial-go/app/models"
)

// Konteks yang dikirim frontend bersama satu pertanyaan chat. Batasnya kecil
// dengan sengaja: seluruh isinya masuk ke system prompt, jadi teks yang panjang
// hanya menghabiskan token (dan kuota Gemini) tanpa menambah informasi.
const (
	chatContextMaxPrices         = 12
	chatContextMaxNews           = 5
	chatContextMaxPlatforms      = 40
	chatContextTextLimit         = 140
	chatContextPlatformDescLimit = 160
)

// ChatPrice adalah satu baris harga yang sedang tampil di layar pengguna,
// mis. {Asset: "BTC", Price: 81100.5, Unit: "USD", Change: 2.1}.
type ChatPrice struct {
	Asset  string  `json:"asset"`
	Price  float64 `json:"price"`
	Unit   string  `json:"unit"`
	Change float64 `json:"change"`
}

// ChatNews adalah satu judul dari Insight Global yang ikut sebagai konteks.
type ChatNews struct {
	Title    string `json:"title"`
	Category string `json:"category"`
}

// ChatTurn adalah satu giliran percakapan sebelumnya. Riwayat ini dikirim ulang
// ke model supaya pertanyaan lanjutan ("kalau biayanya?", "kenapa begitu?")
// tetap nyambung dengan jawaban sebelumnya. Isinya hanya teks: gambar tidak
// disimpan di chat_messages, jadi giliran bergambar tetap masuk sebagai
// pertanyaannya saja.
type ChatTurn struct {
	Role    string
	Content string
}

// ChatImagePlaceholder adalah teks yang disimpan untuk pesan yang hanya berisi
// gambar. Giliran seperti ini dilewati saat riwayat dikirim ulang ke model.
const ChatImagePlaceholder = "[gambar]"

// ChatHistoryTurns membatasi giliran yang dikirim ulang: enam giliran berarti
// tiga pasang tanya-jawab terakhir, cukup untuk merujuk "platform itu" atau
// "aset tadi" tanpa menggandakan biaya token setiap pertanyaan.
const (
	ChatHistoryTurns     = 6
	chatHistoryTextLimit = 600
)

// cleanChatHistory membuang giliran yang tidak dikenal, merapikan spasinya, dan
// memotong daftarnya dari yang paling baru. Tanpa pembersihan ini, jawaban AI
// yang panjang (atau baris "[gambar]") bisa memenuhi prompt dan menggeser
// pertanyaan yang sedang dijawab.
func cleanChatHistory(turns []ChatTurn) []ChatTurn {
	if len(turns) > ChatHistoryTurns {
		turns = turns[len(turns)-ChatHistoryTurns:]
	}

	cleaned := make([]ChatTurn, 0, len(turns))
	for _, turn := range turns {
		if turn.Role != models.ChatRoleUser && turn.Role != models.ChatRoleAssistant {
			continue
		}

		content := cleanChatText(turn.Content, chatHistoryTextLimit)
		if content == "" || content == ChatImagePlaceholder {
			continue
		}

		cleaned = append(cleaned, ChatTurn{Role: turn.Role, Content: content})
	}

	return cleaned
}

// ChatPlatform adalah satu platform jual-beli di halaman Rekomendasi Platform.
// Daftarnya hanya ada di view (PLATFORMS), jadi bagian inilah yang benar-benar
// baru bagi server — harga, profil, dan berita sudah dimiliki server sendiri.
type ChatPlatform struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

// ChatContext adalah potret data aplikasi yang dikirim frontend bersama
// pertanyaan chat. Semua bagian opsional: chat harus tetap jalan kalau frontend
// lama belum mengirim konteks, dan server mengisinya sendiri.
type ChatContext struct {
	Profile   string         `json:"profile"`
	Language  string         `json:"language"`
	Prices    []ChatPrice    `json:"prices"`
	News      []ChatNews     `json:"news"`
	Platforms []ChatPlatform `json:"platforms"`
}

// chatLanguage adalah satu bahasa jawaban yang didukung tombol bahasa di
// topbar. Closing adalah padanan baris penutup "Saran:" di bahasa itu, supaya
// jawaban berbahasa Inggris tidak diakhiri label Indonesia.
type chatLanguage struct {
	Name    string
	Closing string
}

var chatLanguages = map[string]chatLanguage{
	"id": {Name: "Bahasa Indonesia", Closing: "Saran:"},
	"en": {Name: "English", Closing: "Suggestion:"},
}

// normaliseChatLanguage mengembalikan kode bahasa yang dikenal, atau "id" untuk
// apa pun yang tidak dikenal — termasuk permintaan lama tanpa field language.
func normaliseChatLanguage(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if _, ok := chatLanguages[lang]; ok {
		return lang
	}

	return "id"
}

// chatLanguageInstruction hanya ditambahkan kalau pengguna memilih bahasa selain
// Indonesia. Untuk Indonesia tidak ada baris tambahan karena persona FINANIKA
// memang sudah berbahasa Indonesia.
func chatLanguageInstruction(lang string) string {
	code := normaliseChatLanguage(lang)
	if code == "id" {
		return ""
	}

	language := chatLanguages[code]

	return fmt.Sprintf("Bahasa jawaban: %s. Tulis seluruh jawaban dalam bahasa itu, termasuk baris penutup \"%s\".\n", language.Name, language.Closing)
}

// PrepareChatContext membersihkan konteks kiriman frontend lalu melengkapinya
// dengan data milik server.
//
// Konteks dari klien diperlakukan sebagai data tampilan, bukan kebenaran: setiap
// teks dipotong dan dirapikan (lihat cleanChatText), dan bagian yang kosong atau
// tidak masuk akal diisi dari marketData / berita yang sudah diambil controller.
// Profil risiko tetap ditentukan pemanggil dari tabel risk_profiles, karena hanya
// server yang tahu hasil kuis terakhir user.
func PrepareChatContext(sent *ChatContext, profilRisiko string, marketData map[string]interface{}, berita []InsightItem) ChatContext {
	chat := ChatContext{Profile: normaliseChatProfile(profilRisiko), Language: "id"}

	if sent != nil {
		chat.Language = normaliseChatLanguage(sent.Language)
		chat.Prices = cleanChatPrices(sent.Prices)
		chat.News = cleanChatNews(sent.News)
		chat.Platforms = cleanChatPlatforms(sent.Platforms)
	}

	// Harga milik server selalu jadi pangkal; baris kiriman frontend hanya
	// menambah aset yang belum dicakup server. Dengan begitu angka BTC/emas/kurs
	// di prompt tidak bisa diganti dari browser, sementara aset lain (mis. koin
	// yang sedang dibuka) tetap bisa ikut kalau nanti frontend mengirimnya.
	chat.Prices = mergeChatPrices(chatPricesFromMarket(marketData), chat.Prices)

	// Berita milik server juga jadi pangkal: daftar Insight Global yang sedang
	// berlaku sudah di-cache di server, jadi judul kiriman browser (yang bisa
	// sudah basi atau tidak nyambung dengan daftar yang tampil) hanya dipakai
	// kalau server sama sekali tidak punya berita.
	if news := chatNewsFromInsight(berita); len(news) > 0 {
		chat.News = news
	}

	return chat
}

// mergeChatPrices menambahkan harga kiriman frontend yang belum ada di daftar
// server, dicocokkan lewat kata pertama label aset ("BTC", "Emas", "USD/IDR").
func mergeChatPrices(fromServer, sent []ChatPrice) []ChatPrice {
	merged := make([]ChatPrice, 0, len(fromServer)+len(sent))
	known := make(map[string]bool, len(fromServer))

	for _, price := range fromServer {
		if len(merged) == chatContextMaxPrices {
			return merged
		}

		known[chatPriceKey(price.Asset)] = true
		merged = append(merged, price)
	}

	for _, price := range sent {
		if len(merged) == chatContextMaxPrices {
			break
		}
		if known[chatPriceKey(price.Asset)] {
			continue
		}

		merged = append(merged, price)
	}

	return merged
}

// chatPriceKey memakai kata pertama label sebagai kunci, karena label dari
// klien bebas: "BTC" dan "BTC (Bitcoin)" harus dianggap aset yang sama.
func chatPriceKey(asset string) string {
	fields := strings.Fields(asset)
	if len(fields) == 0 {
		return ""
	}

	return strings.ToLower(fields[0])
}

// normaliseChatProfile menerima hanya tiga tipe risiko yang dikenal. Nilai lain
// (mis. teks bebas dari klien) jatuh ke default aplikasi.
func normaliseChatProfile(profilRisiko string) string {
	switch strings.TrimSpace(profilRisiko) {
	case models.RisikoKonservatif:
		return models.RisikoKonservatif
	case models.RisikoAgresif:
		return models.RisikoAgresif
	default:
		return models.RisikoModerat
	}
}

// cleanChatPrices membuang baris yang tidak bisa dipakai model: harga nol,
// negatif, atau NaN, serta label kosong.
func cleanChatPrices(prices []ChatPrice) []ChatPrice {
	cleaned := make([]ChatPrice, 0, len(prices))

	for _, price := range prices {
		asset := cleanChatLabel(price.Asset, 40)
		if asset == "" || !finiteChatNumber(price.Price) || price.Price <= 0 {
			continue
		}

		change := price.Change
		if !finiteChatNumber(change) {
			change = 0
		}

		cleaned = append(cleaned, ChatPrice{
			Asset:  asset,
			Price:  price.Price,
			Unit:   cleanChatLabel(price.Unit, 20),
			Change: change,
		})

		if len(cleaned) == chatContextMaxPrices {
			break
		}
	}

	return cleaned
}

func cleanChatNews(news []ChatNews) []ChatNews {
	cleaned := make([]ChatNews, 0, len(news))

	for _, item := range news {
		title := cleanChatText(item.Title, chatContextTextLimit)
		if title == "" {
			continue
		}

		cleaned = append(cleaned, ChatNews{
			Title:    title,
			Category: cleanChatText(item.Category, 30),
		})

		if len(cleaned) == chatContextMaxNews {
			break
		}
	}

	return cleaned
}

func cleanChatPlatforms(platforms []ChatPlatform) []ChatPlatform {
	cleaned := make([]ChatPlatform, 0, len(platforms))
	seen := make(map[string]bool, len(platforms))

	for _, platform := range platforms {
		name := cleanChatLabel(platform.Name, 60)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}

		seen[strings.ToLower(name)] = true
		cleaned = append(cleaned, ChatPlatform{
			Name:        name,
			Category:    cleanChatText(platform.Category, 20),
			Type:        cleanChatText(platform.Type, 20),
			Description: cleanChatText(platform.Description, chatContextPlatformDescLimit),
		})

		if len(cleaned) == chatContextMaxPlatforms {
			break
		}
	}

	return cleaned
}

// cleanChatText merapatkan spasi dan baris baru lalu memotong panjangnya. Baris
// baru sengaja dihilangkan: konteks ini disisipkan ke system prompt, jadi teks
// yang bisa memecah struktur prompt tidak boleh lolos.
func cleanChatText(value string, limit int) string {
	joined := strings.Join(strings.Fields(value), " ")
	runes := []rune(joined)
	if len(runes) > limit {
		return string(runes[:limit])
	}

	return joined
}

// cleanChatLabel membersihkan label yang tampil sebagai nama aset atau platform:
// hanya huruf, angka, spasi, dan tanda baca lazim yang dipertahankan, supaya
// teks kiriman klien tidak bisa menyelipkan kalimat perintah ke system prompt.
func cleanChatLabel(value string, limit int) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.IsSpace(r):
			return r
		case strings.ContainsRune("()/.,%-+&:", r):
			return r
		default:
			return -1
		}
	}, value)

	return cleanChatText(cleaned, limit)
}

func finiteChatNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// chatPricesFromMarket merakit tiga kartu Dasbor menjadi konteks harga, dipakai
// saat frontend tidak mengirim harga (mis. chat dibuka sebelum /api/market selesai).
func chatPricesFromMarket(marketData map[string]interface{}) []ChatPrice {
	prices := make([]ChatPrice, 0, 3)

	if bitcoin, ok := toMap(marketData["bitcoin"]); ok {
		if price := toFloat64(bitcoin["price_usd"]); price > 0 {
			prices = append(prices, ChatPrice{
				Asset:  "BTC",
				Price:  price,
				Unit:   "USD",
				Change: toFloat64(bitcoin["change_24h"]),
			})
		}
	}

	if gold, ok := toMap(marketData["gold"]); ok {
		if price := toFloat64(gold["price_usd_oz"]); price > 0 {
			prices = append(prices, ChatPrice{
				Asset:  "Emas",
				Price:  price,
				Unit:   "USD/oz",
				Change: toFloat64(gold["change_percent"]),
			})
		}
	}

	if usd, ok := toMap(marketData["usd"]); ok {
		if rate := toFloat64(usd["idr_rate"]); rate > 0 {
			prices = append(prices, ChatPrice{
				Asset:  "USD/IDR",
				Price:  rate,
				Unit:   "IDR",
				Change: toFloat64(usd["change_percent"]),
			})
		}
	}

	return prices
}

func chatNewsFromInsight(berita []InsightItem) []ChatNews {
	limit := len(berita)
	if limit > chatContextMaxNews {
		limit = chatContextMaxNews
	}

	news := make([]ChatNews, 0, limit)
	for _, item := range berita[:limit] {
		news = append(news, ChatNews{
			Title:    cleanChatText(item.Title, chatContextTextLimit),
			Category: cleanChatText(item.Category, 30),
		})
	}

	return news
}

// formatChatPrices menulis harga sebagai baris teks, bukan JSON, karena model
// hanya perlu membacanya dan JSON menambah tanda baca tanpa informasi baru.
func formatChatPrices(prices []ChatPrice) string {
	if len(prices) == 0 {
		return "(harga pasar sedang tidak tersedia)"
	}

	var builder strings.Builder
	for _, price := range prices {
		unit := price.Unit
		if unit == "" {
			unit = "USD"
		}

		fmt.Fprintf(&builder, "- %s: %s %s (%s 24 jam)\n",
			price.Asset, formatChatNumber(price.Price), unit, formatChatChange(price.Change))
	}

	return strings.TrimRight(builder.String(), "\n")
}

func formatChatNews(news []ChatNews) string {
	if len(news) == 0 {
		return "(belum ada berita terbaru)"
	}

	var builder strings.Builder
	for index, item := range news {
		if item.Category != "" {
			fmt.Fprintf(&builder, "%d. [%s] %s\n", index+1, item.Category, item.Title)
			continue
		}

		fmt.Fprintf(&builder, "%d. %s\n", index+1, item.Title)
	}

	return strings.TrimRight(builder.String(), "\n")
}

// formatChatPlatforms mengelompokkan platform per kategori supaya model tahu
// mana yang untuk kripto, saham, dan komoditas tanpa membaca satu per satu.
// Deskripsi hanya ikut kalau frontend mengirimkannya — yaitu untuk platform yang
// benar-benar disebut di pertanyaan — sehingga daftar 36 platform tetap ringkas.
func formatChatPlatforms(platforms []ChatPlatform) string {
	if len(platforms) == 0 {
		return "(daftar platform tidak tersedia)"
	}

	labels := map[string]string{
		"crypto":    "Kripto",
		"saham":     "Saham",
		"komoditas": "Komoditas",
	}

	var builder strings.Builder
	for _, platform := range platforms {
		category := labels[strings.ToLower(platform.Category)]
		if category == "" {
			category = platform.Category
		}

		meta := make([]string, 0, 2)
		if category != "" {
			meta = append(meta, category)
		}
		if platform.Type != "" {
			meta = append(meta, platform.Type)
		}

		line := "- " + platform.Name
		if len(meta) > 0 {
			line += " (" + strings.Join(meta, ", ") + ")"
		}
		if platform.Description != "" {
			line += ": " + platform.Description
		}

		builder.WriteString(line + "\n")
	}

	return strings.TrimRight(builder.String(), "\n")
}

// formatChatNumber memilih jumlah desimal mengikuti besar nilainya, sama seperti
// usdPrice di frontend: rupiah dan dolar penuh tidak perlu dua desimal.
func formatChatNumber(value float64) string {
	switch {
	case value >= 1000:
		return strconv.FormatFloat(value, 'f', 0, 64)
	case value >= 1:
		return strconv.FormatFloat(value, 'f', 2, 64)
	default:
		return strconv.FormatFloat(value, 'f', 6, 64)
	}
}

func formatChatChange(value float64) string {
	if value == 0 {
		return "0.00%"
	}

	if value > 0 {
		return "+" + strconv.FormatFloat(value, 'f', 2, 64) + "%"
	}

	return strconv.FormatFloat(value, 'f', 2, 64) + "%"
}
