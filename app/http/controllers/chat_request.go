package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/goravel/framework/contracts/filesystem"
	"github.com/goravel/framework/contracts/http"

	"edukasi-finansial-goedukasi-finansial-go/app/services"
)

// chatRequest adalah isi satu permintaan chat setelah diurai dari JSON maupun
// multipart. Chat teks biasa tetap memakai JSON seperti sebelumnya; multipart
// dipakai hanya kalau ada gambar yang ikut, karena berkas biner tidak masuk akal
// dikirim sebagai JSON (base64 di dalam JSON menggembungkan permintaan ~33% dan
// memaksa dua kali encode).
type chatRequest struct {
	Message string
	Context *services.ChatContext
	Image   *services.ChatImage
}

// ConversationMessage adalah teks yang disimpan ke chat_messages. Pesan yang
// hanya berisi gambar tetap butuh teks di tabel, jadi diberi penanda.
func (r chatRequest) ConversationMessage() string {
	if r.Message != "" {
		return r.Message
	}

	return services.ChatImagePlaceholder
}

// errChatInput menandai kesalahan yang disebabkan isi permintaan (422), bukan
// kesalahan server (400/500).
type errChatInput struct{ message string }

func (e errChatInput) Error() string { return e.message }

// bindChatRequest mengurai isi permintaan chat. Content-Type yang menentukan
// jalurnya: multipart/form-data dibaca sebagai form + berkas, selain itu JSON.
func bindChatRequest(ctx http.Context) (chatRequest, error) {
	if strings.HasPrefix(ctx.Request().Header("Content-Type"), "multipart/form-data") {
		return bindMultipartChat(ctx)
	}

	var body struct {
		Message string                `json:"message"`
		Context *services.ChatContext `json:"context"`
	}
	if err := ctx.Request().Bind(&body); err != nil {
		return chatRequest{}, fmt.Errorf("isi permintaan tidak terbaca: %w", err)
	}

	return chatRequest{Message: strings.TrimSpace(body.Message), Context: body.Context}, nil
}

func bindMultipartChat(ctx http.Context) (chatRequest, error) {
	request := chatRequest{Message: strings.TrimSpace(ctx.Request().Input("message"))}

	if raw := strings.TrimSpace(ctx.Request().Input("context")); raw != "" {
		var chatContext services.ChatContext
		if err := json.Unmarshal([]byte(raw), &chatContext); err != nil {
			return chatRequest{}, errChatInput{message: "Konteks chat tidak terbaca."}
		}
		request.Context = &chatContext
	}

	file, err := ctx.Request().File("image")
	if err != nil || file == nil {
		return request, nil
	}

	data, err := readChatImageFile(file)
	if err != nil {
		return chatRequest{}, err
	}

	image, err := services.NewChatImage(data)
	if err != nil {
		return chatRequest{}, errChatInput{message: chatImageMessage(err)}
	}
	request.Image = image

	return request, nil
}

// readChatImageFile membaca berkas unggahan. Berkasnya sudah berupa berkas
// sementara di disk (dibuat framework saat multipart diurai), jadi dibaca
// sekaligus lalu dihapus — framework tidak membersihkannya sendiri.
func readChatImageFile(file filesystem.File) ([]byte, error) {
	handle, err := os.Open(file.File())
	if err != nil {
		return nil, fmt.Errorf("gagal membaca gambar: %w", err)
	}
	defer handle.Close()
	defer os.Remove(file.File())

	// Satu byte lebih dari batas sudah cukup untuk tahu ukurannya kelebihan,
	// jadi berkas besar tidak pernah masuk memori seluruhnya.
	data, err := io.ReadAll(io.LimitReader(handle, services.ChatImageMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("gagal membaca gambar: %w", err)
	}

	return data, nil
}

func chatImageMessage(err error) string {
	switch {
	case errors.Is(err, services.ErrChatImageTooLarge):
		return "Gambarnya kegedean. Maksimal 4 MB ya."
	case errors.Is(err, services.ErrChatImageEmpty):
		return "Gambarnya kosong, coba pilih ulang."
	case errors.Is(err, services.ErrChatImageType):
		return "Format gambar itu belum didukung. Pakai JPG, PNG, atau WEBP."
	default:
		return "Gambarnya tidak bisa dibaca."
	}
}

// chatInputFailure memetakan kesalahan penguraian permintaan ke status yang
// sesuai: isi yang salah dari user adalah 422, kegagalan lain tetap 400.
func chatInputFailure(ctx http.Context, err error) http.Response {
	var invalid errChatInput
	if errors.As(err, &invalid) {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": invalid.Error()})
	}

	return ctx.Response().Json(http.StatusBadRequest, http.Json{"error": err.Error()})
}
