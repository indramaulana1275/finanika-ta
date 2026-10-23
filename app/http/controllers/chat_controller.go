package controllers

import (
	"strconv"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"edukasi-finansial-goedukasi-finansial-go/app/facades"
	"edukasi-finansial-goedukasi-finansial-go/app/models"
	"edukasi-finansial-goedukasi-finansial-go/app/services"
)

type ChatController struct {
	market  *services.MarketService
	ai      *services.AIService
	insight *services.InsightService
}

func NewChatController(market *services.MarketService, ai *services.AIService, insight *services.InsightService) *ChatController {
	return &ChatController{market: market, ai: ai, insight: insight}
}

func (r *ChatController) SendMessage(ctx http.Context) http.Response {
	input, err := bindChatRequest(ctx)
	if err != nil {
		return chatInputFailure(ctx, err)
	}

	if input.Message == "" && input.Image == nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
			"errors": map[string]map[string]string{
				"message": {"message": "Tulis pertanyaan atau lampirkan gambar dulu ya."},
			},
		})
	}

	userID := currentUserID(ctx)

	// Riwayat dibaca SEBELUM pesan baru disimpan: yang dikirim ke model sebagai
	// "percakapan sebelumnya" hanya giliran lama, sedangkan pertanyaan sekarang
	// masuk sebagai giliran terakhir lewat chatUserPrompt.
	history := recentChatHistory(userID)

	// Gambar tidak disimpan di chat_messages (kolomnya teks, dan base64 gambar
	// akan menggembungkan tabel); riwayat hanya mencatat pertanyaannya.
	userMessage := models.ChatMessage{
		UserID:  userID,
		Role:    models.ChatRoleUser,
		Message: input.ConversationMessage(),
	}
	if err := facades.Orm().Query().Create(&userMessage); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "Gagal menyimpan pesan kamu"})
	}

	marketData, err := r.market.GetCurrentPrices()
	if err != nil {
		return ctx.Response().Json(http.StatusBadGateway, http.Json{
			"error":  "Gagal mengambil data pasar",
			"detail": err.Error(),
		})
	}

	// Profil dari tabel risk_profiles selalu menang: hanya server yang tahu hasil
	// kuis terakhir, sedangkan nilai kiriman frontend bisa basi (localStorage).
	// Nilai itu baru dipakai kalau user memang belum pernah mengisi kuis.
	riskProfile := models.RisikoModerat
	if record, found := latestRiskProfileRecord(userID); found {
		riskProfile = record.TipeRisiko
	} else if input.Context != nil {
		riskProfile = input.Context.Profile
	}

	// Berita Insight Global menjelaskan penyebab pergerakan harga, jadi ikut
	// dikirim sebagai konteks — daftarnya sudah di-cache satu jam, jadi tidak ada
	// panggilan jaringan tambahan per pesan.
	berita, _ := r.insight.List()

	// Konteks kiriman frontend dirapikan dulu; bagian yang kosong atau tidak
	// masuk akal diisi dari data pasar dan berita yang baru diambil di atas.
	chat := services.PrepareChatContext(input.Context, riskProfile, marketData, berita)

	var narasi string
	if input.Image != nil {
		narasi, err = r.ai.GenerateChatWithImage(input.Message, chat, input.Image, history)
	} else {
		narasi, err = r.ai.GenerateChat(input.Message, chat, history)
	}
	if err != nil {
		// Gemini sedang kelebihan beban: kirim pesan yang bisa ditindaklanjuti
		// user, bukan detail teknis dari Google.
		if input.Image != nil {
			return aiFailureWith(ctx, err, "FINANIKA belum bisa membaca gambarnya")
		}

		return aiFailure(ctx, err)
	}

	assistantMessage := models.ChatMessage{
		UserID:  userID,
		Role:    models.ChatRoleAssistant,
		Message: narasi,
	}
	if err := facades.Orm().Query().Create(&assistantMessage); err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "Gagal menyimpan balasan FINANIKA"})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{
		"reply":   narasi,
		"profile": chat.Profile,
		"message": http.Json{
			"id":         assistantMessage.ID,
			"role":       assistantMessage.Role,
			"message":    assistantMessage.Message,
			"created_at": assistantMessage.CreatedAt,
		},
	})
}

// recentChatHistory membaca beberapa giliran terakhir user ini dari
// chat_messages (terbaru dulu, lalu dibalik supaya urutannya kronologis).
// Kegagalan membaca riwayat tidak boleh menggagalkan chat: model cukup menjawab
// tanpa konteks lanjutan.
func recentChatHistory(userID uint) []services.ChatTurn {
	if userID == 0 {
		return nil
	}

	var rows []models.ChatMessage
	if err := facades.Orm().Query().
		Where("user_id", userID).
		Order("id desc").
		Limit(services.ChatHistoryTurns).
		Get(&rows); err != nil {
		facades.Log().Warningf("gagal membaca riwayat chat user %d: %v", userID, err)
		return nil
	}

	turns := make([]services.ChatTurn, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		turns = append(turns, services.ChatTurn{
			Role:    rows[i].Role,
			Content: strings.TrimSpace(rows[i].Message),
		})
	}

	return turns
}

// currentUserID reads the id that AuthMiddleware stored from the JWT "sub"
// claim. Numbers go through JSON, so they arrive as float64.
func currentUserID(ctx http.Context) uint {
	switch value := ctx.Value("user_id").(type) {
	case uint:
		return value
	case int:
		return uint(value)
	case float64:
		return uint(value)
	case string:
		if parsed, err := strconv.ParseUint(value, 10, 64); err == nil {
			return uint(parsed)
		}
	}

	return 0
}
