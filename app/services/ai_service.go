package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/goravel/framework/contracts/http/client"

	"edukasi-finansial-goedukasi-finansial-go/app/facades"
	"edukasi-finansial-goedukasi-finansial-go/app/models"
)

// Cooldown Gemini disimpan di proses, bukan di cache: sifatnya hanya menghindari
// menunggu timeout berulang, dan tidak perlu bertahan setelah server restart.
var (
	geminiCooldownMu    sync.Mutex
	geminiCooldownUntil time.Time
)

const (
	defaultGeminiModel = "gemini-3.8-flash"
	geminiTemperature  = 0.9
	// Analisis data dan penjelasan berita harus sedatar mungkin: kesimpulannya
	// tidak boleh berubah-ubah setiap kali diminta.
	geminiFocusedTemperature = 0.4
	// Jawaban Insight Global sempat terpotong di tengah kalimat. Batas 2048
	// terlalu sempit untuk model yang memakai token "berpikir" sebelum menulis,
	// jadi plafonnya dinaikkan. Angka ini hanya batas maksimum: jawaban pendek
	// tetap memakai token seperlunya.
	geminiMaxTokens = 4096
	// Gemini kerap menjawab di bawah 10 detik, tetapi kadang tidak menjawab sama
	// sekali. Groq sudah terbukti menyelesaikan jawaban yang sama dalam hitungan
	// detik, jadi penantian dibatasi 12 detik; lewat dari itu langsung fallback.
	geminiTimeout = 12 * time.Second

	// Kalau Gemini diam saja sampai timeout, setiap permintaan berikutnya tidak
	// perlu menunggu 12 detik lagi untuk menemukan masalah yang sama. Selama
	// cooldown, permintaan langsung dilayani Groq; Gemini dicoba lagi setelahnya.
	geminiCooldown = 3 * time.Minute

	// Analisis deret harga tidak berubah banyak dalam satu jam, jadi hasil satu
	// aset disimpan selama itu. Efeknya ganda: user yang bolak-balik membuka aset
	// yang sama tetap dapat jawaban instan, dan kuota Gemini tidak terbakar
	// (Google membalas 429 kalau dipanggil terlalu sering).
	aiAnalysisCacheTTL = time.Hour

	// Kartu "FINANIKA Financial Advisor" meminta ringkasan pasar setiap kali
	// Dasbor dibuka, jadi narasinya disimpan 30 menit: satu panggilan Gemini
	// melayani semua pembukaan Dasbor dalam rentang itu. Kuncinya dipisah per
	// profil risiko karena narasinya memang dirakit dari profil itu (lihat
	// systemInstruction), sehingga kuota yang terpakai paling banyak satu
	// panggilan per profil per 30 menit — bukan satu per buka halaman.
	aiSummaryCacheTTL = 30 * time.Minute

	// Penjelasan satu berita Insight Global juga disimpan 30 menit. Tombol
	// "Tanya FinGo AI" boleh ditekan berkali-kali, tetapi AI hanya dipanggil
	// sekali per berita per bahasa selama rentang itu.
	aiNewsCacheTTL = 30 * time.Minute

	// Nilai contoh yang tertulis di .env; diperlakukan sebagai "belum diisi"
	// supaya user mendapat pesan yang jelas, bukan error 400 dari Google.
	geminiApiKeyPlaceholder = "masukkan_api_key_gemini_anda_disini"

	// Cadangan Groq. Endpoint-nya memakai format OpenAI, jadi payload dan
	// pembacaan balasannya berbeda dari Gemini — lihat GenerateWithGroq.
	//
	// Modelnya BUKAN "llama-3.3-70b-versatile" lagi: Groq sudah menghentikannya
	// (404 model_not_found saat dicek), dan daftar model akun ini menyisakan
	// keluarga openai/gpt-oss serta qwen. Kalau model ini pensiun juga, ganti
	// lewat GROQ_MODEL di .env; daftar resminya di GET /openai/v1/models.
	groqDefaultModel      = "openai/gpt-oss-120b"
	groqTemperature       = 0.7
	groqMaxTokens         = 4096
	groqApiKeyPlaceholder = "masukkan_api_key_groq_anda_disini"

	// Anggaran waktu satu rantai Gemini -> Groq. Permintaan HTTP dibatasi
	// HTTP_REQUEST_TIMEOUT (90 detik): kalau rantai ini tidak selesai di
	// bawahnya, user melihat 408 alih-alih jawaban cadangan.
	aiFallbackBudget = 80 * time.Second
)

// ErrAIHighDemand menandai Gemini menolak permintaan karena bebannya sedang
// tinggi (HTTP 503). Controller memetakannya jadi pesan ramah untuk user,
// bukan dump JSON mentah dari Google.
var ErrAIHighDemand = errors.New("Gemini sedang menangani beban tinggi")

// ErrAIRateLimited menandai kuota permintaan Gemini habis (HTTP 429). Sama
// seperti 503, penyebabnya bukan permintaan user, jadi jangan tampilkan JSON
// mentah — beri tahu user untuk menunggu sebentar.
var ErrAIRateLimited = errors.New("Gemini membatasi laju permintaan (429)")

// ErrAIPromptRejected menandai Gemini menolak isi prompt (blockReason), bukan
// menolak melayani. Hanya kegagalan ini yang TIDAK dialihkan ke Groq: keputusan
// model atas pertanyaannya bukan gangguan layanan, jadi jawabannya tidak boleh
// dicari ke provider lain (lihat shouldFallbackToGroq).
var ErrAIPromptRejected = errors.New("prompt ditolak oleh Gemini")

// aiSummaryPrompt adalah pertanyaan tetap kartu FINANIKA Financial Advisor di
// Dasbor; sengaja sama dengan chip "Ringkasan pasar" di panel chat.
const aiSummaryPrompt = "Berikan ringkasan pasar hari ini"

// MarketSummaryResult adalah isi kartu Financial Advisor: narasinya sendiri,
// profil risiko yang dipakai menyusunnya, kapan narasi itu dibuat, dan apakah
// balasan ini datang dari cache (bukan panggilan Gemini baru).
type MarketSummaryResult struct {
	Summary     string
	Profile     string
	GeneratedAt string
	Cached      bool
}

type AIService struct{}

func NewAIService() *AIService {
	return &AIService{}
}

// GenerateNarasi menjawab pertanyaan chat user sebagai "FINANIKA": data pasar
// yang sedang berjalan memberi konteks harga, berita terbaru dari Insight Global
// memberi konteks penyebabnya, profil risiko memilih saran DCA-nya, dan pertanyaan
// adalah hal yang benar-benar ditanyakan user.
func (s *AIService) GenerateNarasi(profilRisiko, language string, marketData map[string]interface{}, berita []InsightItem, pertanyaan string) (string, error) {
	return s.GenerateWithFallback(systemInstruction(profilRisiko), userPrompt(marketData, berita, pertanyaan, language), geminiTemperature)
}

// GenerateChat menjawab satu pertanyaan chat dengan konteks lengkap dari
// aplikasi: profil risiko, harga yang sedang tampil, berita Insight Global, dan
// daftar platform rekomendasi. Berbeda dari GenerateNarasi (yang hanya dipakai
// kartu ringkasan pasar), konteksnya sudah dirapikan PrepareChatContext.
//
// history adalah beberapa giliran terakhir percakapan yang sama. Tanpa itu,
// pertanyaan lanjutan seperti "kalau biayanya?" tidak punya rujukan dan model
// menjawab topik lain. Riwayat tetap dibatasi cleanChatHistory supaya prompt
// tidak membengkak.
func (s *AIService) GenerateChat(pertanyaan string, chat ChatContext, history []ChatTurn) (string, error) {
	return s.generateWithFallback(
		chatSystemInstruction(chat),
		chatUserPrompt(chat, pertanyaan, chatDefaultQuestionFor(chat.Language, false)),
		geminiTemperature,
		nil,
		history,
	)
}

// chatDefaultQuestion dipakai kalau user menekan Kirim tanpa menulis apa pun,
// sehingga prompt tetap berbentuk pertanyaan yang jelas.
const chatDefaultQuestion = "Berikan ringkasan kondisi pasar hari ini dan saran DCA untuk profil risikoku."

// chatImageQuestion dipakai kalau yang dikirim hanya gambar tanpa teks.
const chatImageQuestion = "Tolong analisis gambar ini dan hubungkan dengan kondisi pasar saat ini."

// chatDefaultQuestionFor memilih pertanyaan cadangan dalam bahasa yang sedang
// dipakai. Tanpa ini, user English yang menekan Kirim pada gambar tanpa teks
// mengirim pertanyaan Indonesia dan jawabannya bisa ikut Indonesia.
func chatDefaultQuestionFor(language string, image bool) string {
	switch normaliseChatLanguage(language) {
	case "en":
		if image {
			return "Please analyse this image and connect it to current market conditions."
		}
		return "Give me a summary of today's market and DCA advice for my risk profile."
	default:
		if image {
			return chatImageQuestion
		}
		return chatDefaultQuestion
	}
}

// GenerateChatWithImage menjawab pertanyaan yang disertai gambar (tangkapan layar
// portofolio, grafik saham, dsb). Prompt-nya sama dengan GenerateChat — konteks
// aplikasi tetap ikut — hanya jalurnya Gemini saja, karena itulah satu-satunya
// provider yang bisa membaca gambar di konfigurasi ini.
func (s *AIService) GenerateChatWithImage(pertanyaan string, chat ChatContext, image *ChatImage, history []ChatTurn) (string, error) {
	return s.GenerateChatImage(
		chatSystemInstruction(chat)+"\n\n"+imageInstruction,
		chatUserPrompt(chat, pertanyaan, chatDefaultQuestionFor(chat.Language, true)),
		image,
		history,
	)
}

// imageInstruction hanya ditambahkan kalau ada gambar yang ikut dikirim.
const imageInstruction = `Gambar yang dilampirkan:
- Pengguna mengirim satu gambar bersama pertanyaannya, biasanya tangkapan layar portofolio atau grafik harga.
- Baca isi gambarnya lebih dulu: sebutkan aset, angka, atau pola yang benar-benar terlihat.
- Jangan mengarang angka yang tidak terbaca di gambar. Kalau ada bagian yang buram atau terpotong, katakan begitu.
- Hubungkan temuan di gambar dengan data konteks aplikasi di atas, lalu beri saran sesuai profil risiko pengguna.`

// chatSystemInstruction menggabungkan persona FINANIKA dengan blok konteks
// aplikasi. Persona tetap dipakai apa adanya supaya aturan off-topic, DCA per
// profil, penutup "Saran:", dan larangan markdown tidak hilang.
func chatSystemInstruction(chat ChatContext) string {
	return systemInstruction(chat.Profile) + "\n\n" + chatContextInstruction(chat)
}

// chatContextInstruction adalah blok data yang diminta brief: profil, harga,
// berita, dan platform, ditambah aturan cara memakainya. Semua angka di sini
// berasal dari server atau dari layar pengguna, jadi model tidak perlu (dan tidak
// boleh) mengarang.
func chatContextInstruction(chat ChatContext) string {
	return fmt.Sprintf(`Konteks aplikasi FINANIKA (pakai ini sebagai satu-satunya sumber angka):

Profil risiko pengguna: %s

Harga pasar saat ini:
%s

Berita terbaru (Insight Global):
%s

Platform yang direkomendasikan di aplikasi ini:
%s

%sAturan konteks:
- Jawab pertanyaan pengguna HANYA seputar finansial, ekonomi, dan investasi berdasarkan data konteks di atas.
- Bila pengguna bertanya tentang satu platform tertentu (misalnya Indodax, Binance, atau Ajaib),
  jawab HANYA tentang platform itu: kelebihan, kekurangan, gambaran biaya, dan cara mendaftar.
  JANGAN mengubahnya menjadi analisis pasar kripto atau saham secara umum, kecuali pengguna
  memang meminta analisis pasar.
- Bila pertanyaannya di luar topik itu, tolak dengan satu kalimat sopan tanpa membahas isinya, lalu arahkan pengguna kembali ke topik finansial.
- Urutkan platform dari daftar di atas. Kalau pengguna menyebut platform lain, jelaskan secara
  umum sebagai edukasi, tetapi jangan mengarang angka biaya atau izin yang spesifik.
- Jangan mengarang harga, persentase, atau judul berita yang tidak ada di konteks.
- Sesuaikan saran DCA dengan profil risiko pengguna di atas, dan jelaskan alasannya secara edukatif.`,
		chat.Profile,
		formatChatPrices(chat.Prices),
		formatChatNews(chat.News),
		formatChatPlatforms(chat.Platforms),
		chatLanguageInstruction(chat.Language))
}

// chatUserPrompt merangkai giliran user. fallback dipakai kalau user mengirim
// gambar tanpa teks sama sekali: chat biasa lalu meminta ringkasan pasar,
// sedangkan pertanyaan bergambar meminta gambar itu yang dibaca.
func chatUserPrompt(chat ChatContext, pertanyaan, fallback string) string {
	pertanyaan = strings.TrimSpace(pertanyaan)
	if pertanyaan == "" {
		pertanyaan = fallback
	}

	language := chatLanguages[normaliseChatLanguage(chat.Language)]

	return fmt.Sprintf(`Pertanyaan pengguna: "%s"

Jawab pertanyaan itu dengan bahasa yang edukatif dan mudah dipahami, berpijak pada data konteks di atas.
Kalau pertanyaannya soal kondisi pasar, sebut harga aset yang relevan dan kaitkan pergerakannya dengan salah satu berita di daftar.
Kalau pertanyaannya soal satu platform, bahas platform itu saja (kelebihan, kekurangan, biaya, cara daftar) tanpa menyelipkan analisis pasar umum.
Tutup dengan baris "%s" berisi langkah konkret yang sesuai profil risiko pengguna.`,
		pertanyaan,
		language.Closing)
}

// AssetPricePoint adalah satu titik harga historis yang dikirim frontend untuk
// dianalisis, mis. {"date":"2026-10-18","price":81100.1}.
type AssetPricePoint struct {
	Date  string  `json:"date"`
	Price float64 `json:"price"`
}

// AnalyzeAsset meminta Gemini membaca pola deret harga satu aset lalu menuliskan
// analisis tren dan saran DCA untuk mahasiswa pemula. Analisisnya sengaja hanya
// bersandar pada data yang dikirim, bukan pada pengetahuan umum model.
//
// Hasilnya di-cache satu jam per aset: permintaan pertama untuk sebuah aset yang
// memanggil Gemini, sisanya dilayani dari memori (dan tidak pernah ikut di-cache
// kalau Gemini gagal — lihat facades.Cache().Remember).
func (s *AIService) AnalyzeAsset(label, asset string, points []AssetPricePoint) (string, error) {
	if len(points) == 0 {
		return "", fmt.Errorf("data historis %s kosong", asset)
	}

	if strings.TrimSpace(label) == "" {
		label = asset
	}

	cacheKey := fmt.Sprintf("ai_analysis_%s", strings.ToLower(strings.TrimSpace(asset)))
	cached, err := facades.Cache().Remember(cacheKey, aiAnalysisCacheTTL, func() (interface{}, error) {
		return s.GenerateWithFallback(assetAnalysisInstruction(), assetAnalysisPrompt(label, asset, points), geminiFocusedTemperature)
	})
	if err != nil {
		return "", err
	}

	analysis, ok := cached.(string)
	if !ok {
		return "", fmt.Errorf("unexpected AI analysis cache payload of type %T for %s", cached, cacheKey)
	}

	return analysis, nil
}

// MarketSummary menyusun narasi "ringkasan pasar hari ini" untuk kartu Financial
// Advisor, dengan hasil yang disimpan aiSummaryCacheTTL (30 menit) per profil
// risiko.
//
// Panggilan pertama dalam rentang itu meminta Gemini lalu menyimpan narasinya;
// panggilan berikutnya — termasuk pembukaan Dasbor oleh user lain dengan profil
// risiko yang sama — dilayani dari memori tanpa menghubungi Gemini, dan itulah
// gunanya Cached di MarketSummaryResult. Kegagalan (429/503) tidak ikut disimpan
// karena Remember hanya menyimpan hasil yang sukses, jadi begitu kuota kembali,
// permintaan berikutnya dicoba lagi alih-alih menyajikan pesan gagal selama 30
// menit.
//
// marketData hanya dipakai saat cache kosong (itulah bahan narasinya). Harga yang
// ditampilkan kartu diambil pemanggil di luar cache supaya tetap terbaru.
//
// berita adalah judul berita dari Insight Global yang dipakai model untuk
// menjelaskan penyebab pergerakan harga; daftarnya ikut di-cache bersama
// narasinya, jadi selama 30 menit itu beritanya bisa sedikit lebih tua dari
// daftar yang sedang tampil di halaman berita.
func (s *AIService) MarketSummary(profilRisiko, language string, marketData map[string]interface{}, berita []InsightItem) (MarketSummaryResult, error) {
	profilRisiko = strings.TrimSpace(profilRisiko)
	language = normaliseChatLanguage(language)
	cacheKey := aiSummaryCacheKey(profilRisiko, language)
	servedFromCache := facades.Cache().Has(cacheKey)

	cached, err := facades.Cache().Remember(cacheKey, aiSummaryCacheTTL, func() (interface{}, error) {
		facades.Log().Infof("ringkasan pasar %s (%s) belum ada di cache, meminta AI", profilRisiko, language)

		narasi, err := s.GenerateNarasi(profilRisiko, language, marketData, berita, aiSummaryPrompt)
		if err != nil {
			return nil, err
		}

		// Payload cache sengaja dibuat peta JSON (bukan struct) supaya tetap
		// terbaca kalau driver cache-nya bukan memori.
		return map[string]interface{}{
			"summary":      narasi,
			"generated_at": time.Now().Format(time.RFC3339),
		}, nil
	})
	if err != nil {
		return MarketSummaryResult{}, err
	}

	payload, ok := toMap(cached)
	if !ok {
		return MarketSummaryResult{}, fmt.Errorf("unexpected AI summary cache payload of type %T for %s", cached, cacheKey)
	}

	return MarketSummaryResult{
		Summary:     toStr(payload["summary"]),
		Profile:     profilRisiko,
		GeneratedAt: toStr(payload["generated_at"]),
		Cached:      servedFromCache,
	}, nil
}

// aiSummaryCacheKey memisahkan cache ringkasan pasar per profil risiko dan per
// bahasa jawaban. Tanpa bahasa di kunci, user yang berganti ke English akan
// menerima narasi Indonesia dari cache. Profil yang kosong memakai default
// aplikasi, sama seperti latestRiskProfile.
func aiSummaryCacheKey(profilRisiko, language string) string {
	profil := strings.ToLower(strings.TrimSpace(profilRisiko))
	if profil == "" {
		profil = strings.ToLower(models.RisikoModerat)
	}

	return "ai_market_summary_" + profil + "_" + normaliseChatLanguage(language)
}

// ExplainNews menjelaskan dampak satu berita ekonomi global ke pergerakan aset,
// dengan bahasa yang dipakai mahasiswa. language mengikuti pilihan bahasa di
// topbar supaya jawabannya tidak selalu Indonesia.
//
// Hasilnya disimpan aiNewsCacheTTL (30 menit) per berita dan per bahasa. Tombol
// "Tanya FinGo AI" bisa ditekan berkali-kali untuk berita yang sama tanpa
// memanggil provider lagi; kegagalan tidak ikut disimpan karena Remember hanya
// menyimpan hasil yang sukses.
func (s *AIService) ExplainNews(newsID, title, summary, language string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("judul berita kosong")
	}

	language = normaliseChatLanguage(language)
	cacheKey := aiNewsExplanationCacheKey(newsID, title, language)

	cached, err := facades.Cache().Remember(cacheKey, aiNewsCacheTTL, func() (interface{}, error) {
		return s.GenerateWithFallback(newsInstruction(language), newsPrompt(title, summary, language), geminiFocusedTemperature)
	})
	if err != nil {
		return "", err
	}

	explanation, ok := cached.(string)
	if !ok {
		return "", fmt.Errorf("unexpected AI news cache payload of type %T for %s", cached, cacheKey)
	}

	return explanation, nil
}

// aiNewsExplanationCacheKey memakai id berita kalau ada. Kalau pemanggil lama
// tidak mengirim id, judulnya di-hash supaya kunci cache tetap pendek dan aman
// dipakai driver cache apa pun.
func aiNewsExplanationCacheKey(newsID, title, language string) string {
	id := strings.TrimSpace(newsID)
	if id == "" {
		sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(title))))
		id = hex.EncodeToString(sum[:8])
	}

	return "ai_news_explanation_" + normaliseChatLanguage(language) + "_" + id
}

// geminiParts menyusun isi satu giliran user. Tanpa gambar hasilnya satu part
// teks seperti sebelumnya; dengan gambar, teks dan inline_data dikirim bersama
// supaya Gemini membaca keduanya sebagai satu pertanyaan.
func geminiParts(user string, image *ChatImage) []map[string]any {
	parts := []map[string]any{{"text": user}}
	if image == nil {
		return parts
	}

	return append(parts, map[string]any{
		"inline_data": map[string]any{
			"mime_type": image.MIMEType,
			"data":      base64.StdEncoding.EncodeToString(image.Data),
		},
	})
}

// generate mengirim satu percakapan (system + riwayat + giliran terakhir) ke
// Gemini saja dan mengembalikan teks jawabannya. Pemeriksaan API key, pemetaan
// 503/429, dan pembacaan blockReason hanya ada di sini; satu-satunya pemanggilnya
// GenerateWithFallback, yang mengambil alih kalau method ini gagal.
//
// history dikirim sebagai giliran user/model sebelum pertanyaan terakhir supaya
// pertanyaan lanjutan tetap nyambung. Konteksnya datang dari pemanggil supaya
// anggaran waktu seluruh rantai Gemini -> Groq dijaga di satu tempat (lihat
// aiFallbackBudget).
func (s *AIService) generate(ctx context.Context, system, user string, temperature float64, image *ChatImage, history []ChatTurn) (string, error) {
	apiKey := strings.TrimSpace(facades.Config().GetString("gemini.api_key"))
	if apiKey == "" || apiKey == geminiApiKeyPlaceholder {
		return "", fmt.Errorf("GEMINI_API_KEY belum diisi di .env, ambil API key dulu dari Google AI Studio")
	}

	model := facades.Config().GetString("gemini.model", defaultGeminiModel)
	baseURL := strings.TrimSuffix(
		facades.Config().GetString("gemini.base_url", "https://generativelanguage.googleapis.com/v1beta"),
		"/",
	)
	endpoint := fmt.Sprintf("%s/models/%s:generateContent", baseURL, model)

	contents := make([]map[string]any, 0, len(history)+1)
	for _, turn := range history {
		role := "user"
		if turn.Role == models.ChatRoleAssistant {
			role = "model"
		}

		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []map[string]any{{"text": turn.Content}},
		})
	}
	contents = append(contents, map[string]any{
		"role":  "user",
		"parts": geminiParts(user, image),
	})

	payload, err := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]any{{"text": system}},
		},
		"contents": contents,
		"generationConfig": map[string]any{
			"temperature":     temperature,
			"maxOutputTokens": geminiMaxTokens,
		},
	})
	if err != nil {
		return "", fmt.Errorf("gagal menyusun request Gemini: %w", err)
	}

	// Jalur gambar memakai klien dengan timeout lebih panjang: Gemini vision lebih
	// lambat daripada jawaban teks, dan tidak ada kaki Groq yang perlu disisakan.
	httpClient := facades.Http()
	if image != nil {
		httpClient = facades.App().MakeHttp().Client("ai_vision")
	}

	response, err := httpClient.
		FlushHeaders().
		AcceptJSON().
		WithHeader("Content-Type", "application/json").
		WithHeader("x-goog-api-key", apiKey).
		WithContext(ctx).
		Post(endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("gagal menghubungi Gemini API: %w", err)
	}

	// 503 = "High Demand" dan 429 = kuota permintaan habis. Keduanya datang dari
	// sisi Google, bukan dari permintaan user, jadi diterjemahkan jadi sentinel
	// supaya controller bisa memberi pesan yang bisa ditindaklanjuti.
	if response.Status() == http.StatusServiceUnavailable {
		return "", ErrAIHighDemand
	}

	if response.Status() == http.StatusTooManyRequests {
		return "", ErrAIRateLimited
	}

	if response.Failed() {
		return "", fmt.Errorf("Gemini API membalas status %d: %s", response.Status(), responseSnippet(response))
	}

	result, err := response.Json()
	if err != nil {
		return "", fmt.Errorf("gagal membaca respons Gemini: %w", err)
	}

	logGeminiFinishReason(result)

	if feedback, ok := toMap(result["promptFeedback"]); ok {
		if reason, ok := feedback["blockReason"].(string); ok && reason != "" {
			return "", fmt.Errorf("%w (blockReason: %s)", ErrAIPromptRejected, reason)
		}
	}

	narasi := extractText(result)
	if narasi == "" {
		return "", fmt.Errorf("Gemini tidak mengembalikan teks narasi: %s", responseSnippet(response))
	}

	return narasi, nil
}

// GenerateWithFallback mencoba Gemini dulu, lalu Groq.
//
// Ini pintu masuk yang dipakai semua fitur AI (chat, ringkasan pasar, analisis
// aset, penjelasan berita): kalau Gemini menjawab, jawabannya langsung dipakai;
// kalau Gemini gagal — 429/503 seperti yang sering terjadi, atau gangguan lain —
// prompt yang sama dikirim ulang ke Groq. Error hanya keluar kalau KEDUANYA
// gagal, dan kalau salah satu sisi melaporkan "sibuk/limit" sentinel itulah yang
// dikembalikan supaya user membaca "coba lagi sebentar", bukan detail mentah.
//
// Penolakan isi prompt oleh Gemini (blockReason) sengaja TIDAK dialihkan ke Groq:
// itu keputusan model atas pertanyaannya, bukan gangguan layanan — lihat
// shouldFallbackToGroq.
func (s *AIService) GenerateWithFallback(system, user string, temperature float64) (string, error) {
	return s.generateWithFallback(system, user, temperature, nil, nil)
}

// GenerateChatImage menjawab pertanyaan yang disertai gambar. Gemini adalah
// satu-satunya jalur yang dipakai: model Groq di konfigurasi ini (gpt-oss-120b)
// tidak bisa membaca gambar, jadi meneruskan prompt ke sana hanya akan
// menghasilkan jawaban yang berpura-pura sudah melihat gambarnya. Kalau Gemini
// gagal, errornya dikembalikan apa adanya supaya controller memberi pesan
// "coba lagi" alih-alih jawaban tanpa gambar.
//
// Batas waktunya aiFallbackBudget, bukan geminiTimeout: di sini tidak ada kaki
// Groq yang perlu disisakan jatah, dan permintaan bergambar memang lebih lama
// diproses daripada pertanyaan teks.
func (s *AIService) GenerateChatImage(system, user string, image *ChatImage, history []ChatTurn) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), aiFallbackBudget)
	defer cancel()

	return s.generate(ctx, system, user, geminiTemperature, image, cleanChatHistory(history))
}

func (s *AIService) generateWithFallback(system, user string, temperature float64, image *ChatImage, history []ChatTurn) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), aiFallbackBudget)
	defer cancel()

	history = cleanChatHistory(history)

	// Kalau Gemini baru saja timeout, jangan ulangi penantian yang sama untuk
	// setiap permintaan berikutnya. Groq sudah terbukti bisa menjawab, jadi
	// selama cooldown permintaan langsung diarahkan ke sana.
	var narasiErr error
	if geminiCoolingDown() {
		narasiErr = fmt.Errorf("Gemini masih dalam masa cooldown")
		facades.Log().Infof("Gemini cooldown aktif, langsung memakai Groq")
	} else {
		// Gemini memakai jatahnya sendiri supaya Groq masih kebagian sisa anggaran
		// kalau Google diam saja (klien HTTP-nya sendiri berbatas HTTP_CLIENT_TIMEOUT).
		geminiCtx, cancelGemini := context.WithTimeout(ctx, geminiTimeout)
		narasi, err := s.generate(geminiCtx, system, user, temperature, image, history)
		cancelGemini()

		if err == nil {
			return narasi, nil
		}

		if !shouldFallbackToGroq(err) {
			return "", err
		}

		narasiErr = err
		markGeminiCooldown()
		facades.Log().Warningf("Gemini gagal (%v), beralih ke Groq", err)
	}

	groqNarasi, groqErr := s.GenerateWithGroq(ctx, system, user, history)
	if groqErr == nil {
		return groqNarasi, nil
	}

	facades.Log().Errorf("cadangan Groq juga gagal: %v", groqErr)

	// Pesan ramah menang: kalau ada sisi yang bilang "sibuk/limit", user diberi
	// tahu untuk mencoba lagi alih-alih membaca detail mentah dari provider.
	if errors.Is(groqErr, ErrAIRateLimited) || errors.Is(groqErr, ErrAIHighDemand) {
		return "", groqErr
	}

	if errors.Is(narasiErr, ErrAIRateLimited) || errors.Is(narasiErr, ErrAIHighDemand) {
		return "", narasiErr
	}

	return "", fmt.Errorf("%w (cadangan Groq juga gagal: %v)", narasiErr, groqErr)
}

func geminiCoolingDown() bool {
	geminiCooldownMu.Lock()
	defer geminiCooldownMu.Unlock()

	return time.Now().Before(geminiCooldownUntil)
}

func markGeminiCooldown() {
	geminiCooldownMu.Lock()
	defer geminiCooldownMu.Unlock()

	geminiCooldownUntil = time.Now().Add(geminiCooldown)
}

// GenerateWithGroq mengirim satu percakapan (system + riwayat + giliran terakhir)
// ke Groq dan mengembalikan teks jawabannya. Groq memakai format OpenAI —
// pesannya berupa daftar {role, content} dan jawabannya ada di
// choices[0].message.content — bukan format Gemini, jadi payload dan pembacaan
// balasannya memang berbeda.
//
// Hanya dipanggil GenerateWithFallback; suhunya sengaja tidak jadi parameter
// karena jawaban cadangan selalu memakai groqTemperature (0.7).
func (s *AIService) GenerateWithGroq(ctx context.Context, system, user string, history []ChatTurn) (string, error) {
	apiKey := strings.TrimSpace(facades.Config().GetString("groq.api_key"))
	if apiKey == "" || apiKey == groqApiKeyPlaceholder {
		return "", fmt.Errorf("GROQ_API_KEY belum diisi di .env, ambil API key dulu dari console.groq.com")
	}

	model := facades.Config().GetString("groq.model", groqDefaultModel)
	baseURL := strings.TrimSuffix(
		facades.Config().GetString("groq.base_url", "https://api.groq.com/openai/v1"),
		"/",
	)
	endpoint := baseURL + "/chat/completions"

	messages := make([]map[string]string, 0, len(history)+2)
	if strings.TrimSpace(system) != "" {
		messages = append(messages, map[string]string{"role": "system", "content": system})
	}
	for _, turn := range history {
		role := "user"
		if turn.Role == models.ChatRoleAssistant {
			role = "assistant"
		}

		messages = append(messages, map[string]string{"role": role, "content": turn.Content})
	}
	messages = append(messages, map[string]string{"role": "user", "content": user})

	payloadBody := map[string]any{
		"model":       model,
		"messages":    messages,
		"temperature": groqTemperature,
		"max_tokens":  groqMaxTokens,
	}

	// gpt-oss adalah model penalaran: tanpa ini ia bisa menghabiskan sebagian
	// besar max_tokens untuk "berpikir" lalu berhenti di tengah jawaban. Hanya
	// dikirim untuk model gpt-oss supaya model lain yang tidak mengenal field
	// ini tidak ditolak.
	if strings.Contains(strings.ToLower(model), "gpt-oss") {
		payloadBody["reasoning_effort"] = "low"
	}

	payload, err := json.Marshal(payloadBody)
	if err != nil {
		return "", fmt.Errorf("gagal menyusun request Groq: %w", err)
	}

	response, err := facades.Http().
		FlushHeaders().
		AcceptJSON().
		WithHeader("Content-Type", "application/json").
		WithHeader("Authorization", "Bearer "+apiKey).
		WithContext(ctx).
		Post(endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("gagal menghubungi Groq API: %w", err)
	}

	// Dua status ini datang dari sisi Groq, bukan dari permintaan user, jadi
	// diterjemahkan ke sentinel yang sama seperti Gemini supaya controller
	// memberi pesan ramah yang sama pula.
	if response.Status() == http.StatusServiceUnavailable {
		return "", ErrAIHighDemand
	}

	if response.Status() == http.StatusTooManyRequests {
		return "", ErrAIRateLimited
	}

	if response.Failed() {
		return "", fmt.Errorf("Groq API membalas status %d: %s", response.Status(), responseSnippet(response))
	}

	result, err := response.Json()
	if err != nil {
		return "", fmt.Errorf("gagal membaca respons Groq: %w", err)
	}

	narasi := extractGroqText(result)
	if narasi == "" {
		return "", fmt.Errorf("Groq tidak mengembalikan teks jawaban: %s", responseSnippet(response))
	}

	return narasi, nil
}

// shouldFallbackToGroq memutuskan apakah satu kegagalan Gemini layak dicoba ulang
// ke provider cadangan. Semua gangguan layanan dialihkan — 429, 503, timeout,
// 5xx, model yang tidak dikenal, bahkan API key Gemini yang belum diisi (selama
// GROQ_API_KEY ada, fitur AI tetap jalan). Satu-satunya pengecualian adalah
// penolakan isi prompt: itu bukan masalah ketersediaan layanan.
func shouldFallbackToGroq(err error) bool {
	return err != nil && !errors.Is(err, ErrAIPromptRejected)
}

// plainTextInstruction menutup pintu ke format Markdown. Panel-panel FINANIKA
// menampilkan jawaban sebagai teks biasa (lihat cleanMarkdown di dashboard.html),
// jadi penekanan dari model akan terlihat mentah seperti "**Bitcoin**". Ketiga
// system prompt memakainya supaya Gemini dan Groq sama-sama tidak menghasilkannya.
//
// Ini lapis pertama; lapis keduanya membersihkan sisa penanda di frontend, karena
// narasi yang sudah tersimpan di cache bisa dibuat sebelum aturan ini ada.
const plainTextInstruction = `Format jawaban:
- JANGAN gunakan format Markdown seperti **tebal** atau *miring* dalam jawabanmu. Cukup gunakan teks biasa.
- Jangan memakai tanda bintang atau garis bawah untuk menekankan kata, dan jangan memakai tanda pagar (#) untuk membuat judul.
- Bila perlu daftar, tulis tiap poin di barisnya sendiri dan awali dengan tanda hubung "- ".`

// penutupInstruction mengatur kalimat penutup semua jawaban AI, dan dipakai
// ketiga system prompt bersama plainTextInstruction.
//
// Kalimat penyangkalan ("ini hanya edukasi, bukan nasihat keuangan profesional")
// sengaja diminta berhenti dipakai: pembaca sudah tahu aplikasinya edukatif, dan
// yang lebih berguna di ujung jawaban adalah satu langkah konkret yang bisa
// dijalankan. Karena itu penutupnya diganti baris "Saran: ..." yang menyebut
// alasannya — harga yang bergerak dan berita yang menyebabkannya.
const penutupInstruction = `Penutup jawaban:
- Tutup jawaban dengan satu baris "Saran:" berisi langkah konkret yang bisa langsung
  dijalankan pembaca, misalnya menunggu penurunan (buy the dip), menambah porsi DCA saat
  tren sedang naik, atau menjaga porsi kas saat pasar bergejolak.
- Saran itu harus menyebut alasan atau datanya, bukan sekadar perintah. Contoh bentuknya:
  "Saran: Pasar sedang volatil karena The Fed menaikkan suku bunga, sebaiknya tunggu penurunan
  (buy the dip) atau lakukan DCA secara berkala."
- JANGAN menutup jawaban dengan kalimat penyangkalan seperti "ini hanya edukasi",
  "bukan nasihat keuangan profesional", atau "bukan saran investasi".`

// aiNewsDigestLimit membatasi berita yang dikirim sebagai konteks. Daftar Insight
// Global sendiri sudah dipotong di insightMaxItems; batas ini menjaga prompt tetap
// ringkas, karena yang dibutuhkan model hanya beberapa judul terbaru untuk dikaitkan
// dengan pergerakan harga. Lima judul cukup: lebih dari itu hanya menambah token
// dan waktu tunggu tanpa mengubah kesimpulan.
const aiNewsDigestLimit = 5

// formatNewsDigest menyusun judul berita jadi teks biasa — bukan JSON — supaya
// tokennya tidak habis untuk tanda baca. Hanya judul dan kategorinya yang dikirim:
// ringkasan dari feed RSS cuma nama media, dan teks contoh yang panjang justru
// mengundang model mengarang detail yang tidak ada di beritanya.
func formatNewsDigest(berita []InsightItem) string {
	if len(berita) == 0 {
		return "Berita ekonomi terbaru: (belum ada daftar berita yang tersedia)."
	}

	if len(berita) > aiNewsDigestLimit {
		berita = berita[:aiNewsDigestLimit]
	}

	var builder strings.Builder
	builder.WriteString("Berita ekonomi terbaru (Insight Global):\n")

	for index, item := range berita {
		fmt.Fprintf(&builder, "%d. [%s] %s\n", index+1, item.Category, item.Title)
	}

	return strings.TrimRight(builder.String(), "\n")
}

// systemInstruction is the persona prompt: FINANIKA, the laid-back Gen Z advisor
// who maps the user's risk profile to a concrete DCA strategy.
func systemInstruction(profilRisiko string) string {
	prompt := fmt.Sprintf(`Kamu adalah "FINANIKA", penasihat keuangan digital untuk Gen Z Indonesia.

Gaya bicara:
- Santai, akrab, dan tidak kaku. Pakai "kamu", bukan "Anda".
- Hindari jargon berlebihan; jelaskan istilah dengan bahasa sehari-hari.
- Ringkas: maksimal 3 paragraf pendek, sekitar 150 kata.

Profil risiko pengguna saat ini: %s

Panduan saran DCA (Dollar Cost Averaging) per profil:
- Konservatif: mayoritas ke instrumen aman (reksa dana pasar uang, deposito, emas), porsi kecil ke aset volatil. DCA bulanan dengan nominal tetap, fokus menjaga nilai.
- Moderat: seimbang antara instrumen aman dan aset tumbuh (reksa dana campuran, saham indeks, Bitcoin porsi kecil). DCA rutin, siap menerima fluktuasi 10-20%%.
- Agresif: mayoritas ke aset tumbuh. DCA rutin, boleh menambah porsi saat koreksi tajam, siap menghadapi penurunan sementara di atas 30%%.

Aturan:
- Topik: hanya finansial, ekonomi, dan investasi. Bila user bertanya di luar itu
  (resep masakan, film, olahraga, hiburan, dan sejenisnya), tolak dengan satu kalimat
  sopan, jangan bahas isi pertanyaannya, lalu arahkan user kembali ke topik finansial.
- Gunakan data pasar yang diberikan untuk memberi konteks harga. Bila kurs USD/IDR tersedia, konversikan angka penting ke Rupiah.
- Jangan mengarang angka di luar data pasar yang diberikan.
- Kaitkan pergerakan harga hari ini dengan berita ekonomi terbaru yang disertakan: sebut judul
  beritanya sebagai penyebab atau konteksnya. Jangan mengarang isi berita yang tidak ada di daftar itu.`, profilRisiko)

	return prompt + "\n\n" + penutupInstruction + "\n\n" + plainTextInstruction
}

func userPrompt(marketData map[string]interface{}, berita []InsightItem, pertanyaan, language string) string {
	pertanyaan = strings.TrimSpace(pertanyaan)
	if pertanyaan == "" {
		pertanyaan = "Berikan ringkasan kondisi pasar hari ini."
	}

	closing := chatLanguages[normaliseChatLanguage(language)].Closing

	return fmt.Sprintf(`Berikut data pasar terbaru dalam format JSON (harga dalam USD, kecuali usd.idr_rate yang sudah dalam Rupiah):

%s

%s

Pertanyaan user: "%s"

Jawab pertanyaan itu dengan berpijak pada data pasar dan berita di atas, lalu kaitkan
keduanya seperti aturan penutup: sebut harga aset yang bergerak, berita yang jadi
konteksnya, dan langkah konkret yang bisa diambil.
Bila pertanyaannya di luar topik finansial, ekonomi, atau investasi, tolak dengan sopan
tanpa membahas isi pertanyaannya, lalu arahkan user kembali ke topik finansial.
Tutup dengan baris "%s" berisi langkah konkret.

%s`,
		formatMarketData(marketData), formatNewsDigest(berita), pertanyaan, closing, chatLanguageInstruction(language))
}

// assetAnalysisInstruction adalah persona analis untuk fitur AI Analysis: yang
// diminta bukan opini soal pasar secara umum, melainkan pembacaan pola deret harga
// yang dikirim frontend.
func assetAnalysisInstruction() string {
	prompt := `Kamu adalah "FINANIKA", analis data pasar yang menjelaskan temuanmu kepada mahasiswa Indonesia.

Gaya bicara:
- Santai dan jelas. Pakai "kamu", bukan "Anda".
- Hindari jargon berlebihan; kalau memakai istilah (volatilitas, support), jelaskan singkat dalam tanda kurung.

Aturan:
- Analisis HANYA pola dari deret harga yang dikirim: arah tren, rentang naik-turun
  (tertinggi–terendah), dan seberapa liar harganya bergerak.
- Jangan memberikan prediksi pasti, target harga, atau jaminan keuntungan. Sebutkan
  bahwa pola bisa berubah, bukan bahwa harga "akan" naik atau turun.
- Jangan mengarang angka yang tidak ada di data.
- Satu paragraf analisis tren, lalu satu paragraf saran strategi DCA (nominal tetap
  dan rutin, porsi kecil dulu, hindari membeli karena panik) untuk mahasiswa pemula.`

	return prompt + "\n\n" + penutupInstruction + "\n\n" + plainTextInstruction
}

// assetAnalysisPrompt menyusun satu titik data per baris "YYYY-MM-DD: harga"
// supaya deretnya enak dibaca model tanpa membuang token untuk tanda JSON.
func assetAnalysisPrompt(label, asset string, points []AssetPricePoint) string {
	var builder strings.Builder
	for _, point := range points {
		fmt.Fprintf(&builder, "%s: %.10g\n", point.Date, point.Price)
	}

	return fmt.Sprintf(`Data historis %s (%s), %d titik, format "tanggal: harga penutupan":

%s
Berdasarkan data historis di atas, berikan analisis tren singkat (1 paragraf) dan saran
strategi DCA untuk mahasiswa pemula. Jangan berikan prediksi pasti: fokus pada pola data.`,
		label, asset, len(points), builder.String())
}

// newsInstruction adalah persona untuk fitur Insight Global: menghubungkan satu
// berita ekonomi dengan arah pergerakan aset.
func newsInstruction(language string) string {
	closing := chatLanguages[normaliseChatLanguage(language)].Closing

	prompt := fmt.Sprintf(`Kamu adalah "FINANIKA", penasihat keuangan digital untuk Gen Z Indonesia yang sedang menjelaskan berita ekonomi global.

Gaya bicara:
- Santai dan mudah dipahami mahasiswa. Pakai "kamu", bukan "Anda".
- Bebaskan istilah teknis; kalau perlu, jelaskan dalam tanda kurung.

Aturan:
- Hubungkan berita dengan arah pergerakan Emas, Bitcoin, Saham, dan USD: mana yang
  biasanya tertekan, mana yang diuntungkan, dan alasan ekonominya (suku bunga, inflasi,
  kurs, dan sebagainya).
- Jangan memberikan prediksi pasti atau jaminan keuntungan; sebutkan bahwa reaksi pasar
  bisa berbeda dari kebiasaannya.
- Maksimal 2 paragraf pendek, sekitar 150 kata.
- Setelah menjelaskan dampaknya, beri pembaca satu langkah yang bisa diambil, lalu
  tutup dengan baris "%s" seperti aturan penutup di bawah.`, closing)

	return prompt + "\n\n" + penutupInstruction + "\n\n" + plainTextInstruction
}

func newsPrompt(title, summary, language string) string {
	ringkasan := strings.TrimSpace(summary)
	if ringkasan == "" {
		ringkasan = "(tidak ada ringkasan tambahan)"
	}

	return fmt.Sprintf(`Jelaskan dampak berita ekonomi global '%s' terhadap pergerakan aset (Emas, Bitcoin, Saham, USD) dalam bahasa yang mudah dipahami mahasiswa.

Ringkasan berita: %s

%s`, title, ringkasan, chatLanguageInstruction(language))
}

func formatMarketData(marketData map[string]interface{}) string {
	if len(marketData) == 0 {
		return "Data pasar sedang tidak tersedia."
	}

	encoded, err := json.MarshalIndent(marketData, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", marketData)
	}

	return string(encoded)
}

// logGeminiFinishReason mencatat kalau Gemini berhenti karena plafon token,
// bukan karena jawabannya selesai. Tanpa catatan ini, jawaban yang terpotong
// terlihat seperti jawaban normal di UI dan penyebabnya sulit dilacak.
func logGeminiFinishReason(result map[string]any) {
	candidates, ok := toSlice(result["candidates"])
	if !ok || len(candidates) == 0 {
		return
	}

	first, ok := toMap(candidates[0])
	if !ok {
		return
	}

	reason := toStr(first["finishReason"])
	if reason == "MAX_TOKENS" {
		facades.Log().Warningf("Gemini berhenti karena MAX_TOKENS; jawaban mungkin terpotong")
	}
}

// extractText pulls the answer out of candidates[0].content.parts[].text, which
// is where the Gemini REST API puts the generated narration.
func extractText(result map[string]any) string {
	candidates, ok := toSlice(result["candidates"])
	if !ok || len(candidates) == 0 {
		return ""
	}

	first, ok := toMap(candidates[0])
	if !ok {
		return ""
	}

	content, ok := toMap(first["content"])
	if !ok {
		return ""
	}

	parts, ok := toSlice(content["parts"])
	if !ok {
		return ""
	}

	var builder strings.Builder
	for _, part := range parts {
		partMap, ok := toMap(part)
		if !ok {
			continue
		}

		if text, ok := partMap["text"].(string); ok {
			builder.WriteString(text)
		}
	}

	return strings.TrimSpace(builder.String())
}

// extractGroqText pulls the answer out of choices[0].message.content, which is
// where Groq (dan API lain yang mengikuti format OpenAI) meletakkan jawabannya.
// Model penalaran seperti gpt-oss juga mengirim `reasoning`, tapi itu bukan
// jawaban untuk user, jadi sengaja tidak dibaca.
func extractGroqText(result map[string]any) string {
	choices, ok := toSlice(result["choices"])
	if !ok || len(choices) == 0 {
		return ""
	}

	first, ok := toMap(choices[0])
	if !ok {
		return ""
	}

	message, ok := toMap(first["message"])
	if !ok {
		return ""
	}

	return strings.TrimSpace(toStr(message["content"]))
}

// responseSnippet keeps error messages short while still showing what Gemini said.
func responseSnippet(response client.Response) string {
	raw, err := response.Body()
	if err != nil {
		return "(body tidak bisa dibaca)"
	}

	runes := []rune(strings.TrimSpace(raw))
	const max = 300
	if len(runes) <= max {
		return string(runes)
	}

	return string(runes[:max]) + "..."
}
