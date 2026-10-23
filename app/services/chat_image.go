package services

import (
	"errors"
	"fmt"
	"net/http"
)

// Batas gambar yang diterima. Gemini sendiri menerima inline_data sampai puluhan
// MB, tetapi gambar sebesar itu tidak diperlukan untuk membaca grafik atau
// tangkapan layar portofolio, dan body permintaan yang besar hanya membebani
// server (base64 menambah ~33%). Frontend sudah mengecilkan gambar sebelum
// dikirim, jadi batas ini hanya jaring pengaman untuk klien lain.
const ChatImageMaxBytes = 4 << 20 // 4 MB

var (
	// ErrChatImageTooLarge dikembalikan kalau gambar melebihi ChatImageMaxBytes.
	ErrChatImageTooLarge = errors.New("ukuran gambar terlalu besar")
	// ErrChatImageType dikembalikan kalau isi berkas bukan gambar yang didukung.
	ErrChatImageType = errors.New("format gambar tidak didukung")
	// ErrChatImageEmpty dikembalikan kalau berkasnya kosong.
	ErrChatImageEmpty = errors.New("gambar kosong")
)

// chatImageMimeTypes adalah format yang bisa dibaca Gemini. Daftarnya sengaja
// pendek: hanya format yang benar-benar mungkin dihasilkan ponsel dan tangkapan
// layar. GIF tidak ada di sini karena Gemini tidak menerimanya sebagai inline
// image, dan browser sudah mengubah gambar apa pun jadi JPEG sebelum dikirim.
var chatImageMimeTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
}

// ChatImage adalah satu gambar yang dilampirkan ke pertanyaan chat, sudah dalam
// bentuk byte mentah. Base64-nya baru dibuat saat mengirim ke Gemini (lihat
// geminiParts), jadi tidak ada salinan string besar yang menganggur di memori.
type ChatImage struct {
	MIMEType string
	Data     []byte
}

// NewChatImage memvalidasi isi berkas kiriman frontend.
//
// Tipe MIME ditentukan dari isi berkas (http.DetectContentType), bukan dari nama
// atau header kiriman klien: itulah satu-satunya cara memastikan yang dikirim ke
// Gemini benar-benar gambar, bukan berkas lain yang dinamai .png.
func NewChatImage(data []byte) (*ChatImage, error) {
	if len(data) == 0 {
		return nil, ErrChatImageEmpty
	}

	if len(data) > ChatImageMaxBytes {
		return nil, fmt.Errorf("%w (maksimal %d MB)", ErrChatImageTooLarge, ChatImageMaxBytes>>20)
	}

	mimeType := http.DetectContentType(data)
	if !chatImageMimeTypes[mimeType] {
		return nil, fmt.Errorf("%w: %s", ErrChatImageType, mimeType)
	}

	return &ChatImage{MIMEType: mimeType, Data: data}, nil
}
