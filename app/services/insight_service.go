package services

import (
	"encoding/xml"
	"fmt"
	"hash/fnv"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"edukasi-finansial-goedukasi-finansial-go/app/facades"
)

const (
	// Daftar berita disimpan satu jam: judul berita ekonomi tidak berubah tiap
	// menit, jadi satu permintaan halaman tidak perlu memanggil Google News lagi.
	insightCacheTTL = time.Hour
	// Daftar cadangan hanya sebentar — begitu internetnya kembali, halaman tidak
	// menunggu satu jam untuk berita sungguhan.
	insightFallbackCacheTTL = 5 * time.Minute

	// Berapa banyak kartu berita di halaman Insight Global. Nilainya dipakai dua
	// jalur sekaligus: membatasi hasil gabungan feed RSS, dan menentukan panjang
	// daftar contoh di bawah — jadi dua daftar itu selalu sepanjang ini.
	insightMaxItems = 20

	// Tiap topik feed diambil lebih banyak dari bagiannya, karena dua pencarian
	// Google News sering memuat judul yang sama dan yang kembar dibuang setelah
	// digabung: tanpa cadangan ini daftar 20 kartu bisa menyusut jadi belasan.
	// Batas akhirnya tetap insightMaxItems, dipotong setelah diurutkan terbaru.
	insightFeedPerTopicSlack = 2

	// Penanda dari mana daftarnya datang; dikirim apa adanya ke frontend supaya
	// halaman bisa jujur soal sumbernya.
	insightSourceFeed     = "google_news"
	insightSourceFallback = "contoh"

	// Feed RSS Google News: gratis dan tanpa API key (GNews/NewsAPI keduanya
	// butuh key). Dua topik diambil sekaligus supaya daftarnya tidak didominasi
	// satu tema dan kartunya tetap beragam.
	insightFeedBaseURL = "https://news.google.com/rss/search"
)

var insightFeedQueries = []string{
	"ekonomi global OR inflasi OR suku bunga OR bank sentral",
	"harga emas OR bitcoin OR IHSG OR rupiah OR harga minyak",
}

// InsightItem adalah satu berita ekonomi global di halaman Insight Global.
type InsightItem struct {
	ID       string
	Title    string
	Category string
	Date     string
	Summary  string
}

// insightList adalah isi halaman Insight Global yang di-cache: beritanya plus
// sumber yang benar-benar melayaninya — sama seperti historySeries di market,
// sumbernya ikut dibawa bersama datanya supaya tidak ada tebakan.
type insightList struct {
	Items  []InsightItem
	Source string
}

type InsightService struct{}

func NewInsightService() *InsightService {
	return &InsightService{}
}

// List mengembalikan berita yang sedang dipakai halaman Insight Global beserta
// sumbernya ("google_news" atau "contoh").
func (s *InsightService) List() ([]InsightItem, string) {
	list := s.cached()

	return list.Items, list.Source
}

// Find mencari satu berita di daftar yang sedang dipakai — id lebih dulu, lalu
// judulnya. Karena yang dicari selalu ada di daftar halaman, endpoint penjelasan
// AI tidak bisa dipakai untuk membahas teks sembarangan.
func (s *InsightService) Find(id, title string) (InsightItem, bool) {
	id = strings.TrimSpace(id)
	title = strings.ToLower(strings.TrimSpace(title))

	for _, item := range s.cached().Items {
		if id != "" && item.ID == id {
			return item, true
		}

		if title != "" && strings.ToLower(item.Title) == title {
			return item, true
		}
	}

	return InsightItem{}, false
}

// cached membaca daftar berita dari cache, atau menyusunnya sekali kalau belum
// ada. Daftar cadangan diberi masa simpan pendek supaya kegagalan feed sesaat
// tidak mengunci halaman pada berita contoh selama satu jam.
func (s *InsightService) cached() insightList {
	const cacheKey = "insight_news"

	if cached := facades.Cache().Get(cacheKey); cached != nil {
		if list, ok := cached.(insightList); ok {
			return list
		}
	}

	list := fetchInsightFeed()

	ttl := insightCacheTTL
	if list.Source == insightSourceFallback {
		ttl = insightFallbackCacheTTL
	}

	if err := facades.Cache().Put(cacheKey, list, ttl); err != nil {
		facades.Log().Warningf("gagal menyimpan cache berita: %v", err)
	}

	return list
}

// fetchInsightFeed mengambil berita dari feed RSS; kalau tidak ada satu pun yang
// berhasil, daftar contoh yang dipakai supaya halaman Insight Global tidak pernah
// kosong. Bisa dimatikan lewat INSIGHT_FEED=false untuk demo tanpa internet.
func fetchInsightFeed() insightList {
	if !facades.Config().GetBool("insight.feed_enabled", true) {
		return insightList{Items: contohInsightItems(), Source: insightSourceFallback}
	}

	type result struct {
		items []InsightItem
		err   error
	}

	// Setiap topik diambil lebih banyak dari bagiannya karena judul kembar baru
	// dibuang setelah kedua hasil digabung (lihat insightFeedPerTopicSlack), lalu
	// hasilnya dipotong ke insightMaxItems dari yang paling baru.
	perFeed := (insightMaxItems + len(insightFeedQueries) - 1) / len(insightFeedQueries) * insightFeedPerTopicSlack

	results := make([]result, len(insightFeedQueries))
	var waitGroup sync.WaitGroup

	for index, query := range insightFeedQueries {
		waitGroup.Add(1)

		go func(slot int, topic string) {
			defer waitGroup.Done()

			items, err := fetchNewsFeed(insightFeedURL(topic), perFeed)
			results[slot] = result{items: items, err: err}
		}(index, query)
	}

	waitGroup.Wait()

	groups := make([][]InsightItem, 0, len(results))
	for _, res := range results {
		if res.err != nil {
			facades.Log().Warningf("feed berita gagal dipakai: %v", res.err)
			continue
		}

		groups = append(groups, res.items)
	}

	items := mergeInsightItems(groups...)
	if len(items) == 0 {
		return insightList{Items: contohInsightItems(), Source: insightSourceFallback}
	}

	return insightList{Items: items, Source: insightSourceFeed}
}

// insightFeedURL merangkai URL pencarian Google News untuk satu topik.
func insightFeedURL(query string) string {
	return insightFeedBaseURL + "?q=" + url.QueryEscape(query) + "&hl=id&gl=ID&ceid=ID:id"
}

// rssFeed hanya mengambil bagian yang dipakai dari RSS Google News; description
// dan link-nya sengaja diabaikan karena isinya cuma tautan, bukan ringkasan.
// Source ikut dibaca karena nama medianya kadang sendiri memuat " - "
// (mis. "Bisnis.com - Market"), sehingga judulnya tidak bisa dipotong dengan
// menebak dari tanda hubung saja.
type rssFeed struct {
	Channel struct {
		Items []struct {
			Title   string `xml:"title"`
			PubDate string `xml:"pubDate"`
			Source  string `xml:"source"`
		} `xml:"item"`
	} `xml:"channel"`
}

// fetchNewsFeed membaca satu feed dan menyaringnya jadi daftar berita siap pakai.
func fetchNewsFeed(feedURL string, limit int) ([]InsightItem, error) {
	response, err := facades.Http().
		FlushHeaders().
		WithHeader("User-Agent", marketUserAgent).
		Get(feedURL)
	if err != nil {
		return nil, fmt.Errorf("request to %s failed: %w", feedURL, err)
	}

	if response.Failed() {
		return nil, fmt.Errorf("request to %s returned status %d", feedURL, response.Status())
	}

	body, err := response.Body()
	if err != nil {
		return nil, fmt.Errorf("could not read response from %s: %w", feedURL, err)
	}

	var feed rssFeed
	if err := xml.Unmarshal([]byte(body), &feed); err != nil {
		return nil, fmt.Errorf("could not decode feed from %s: %w", feedURL, err)
	}

	items := make([]InsightItem, 0, limit)
	for _, entry := range feed.Channel.Items {
		if len(items) >= limit {
			break
		}

		title, media := splitNewsTitle(entry.Title, entry.Source)
		if title == "" {
			continue
		}

		items = append(items, InsightItem{
			ID:       newsID(title),
			Title:    title,
			Category: newsCategory(title),
			Date:     newsDate(entry.PubDate),
			Summary:  newsSummary(media),
		})
	}

	return items, nil
}

// mergeInsightItems menggabungkan hasil beberapa feed: judul kembar dibuang
// supaya satu berita tidak muncul sebagai dua kartu, lalu diurutkan dari yang
// paling baru dan dipotong sesuai jumlah kartu yang disediakan.
func mergeInsightItems(groups ...[]InsightItem) []InsightItem {
	merged := make([]InsightItem, 0, insightMaxItems)
	seen := make(map[string]bool)

	for _, group := range groups {
		for _, item := range group {
			key := strings.ToLower(item.Title)
			if seen[key] {
				continue
			}

			seen[key] = true
			merged = append(merged, item)
		}
	}

	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].Date > merged[j].Date
	})

	if len(merged) > insightMaxItems {
		merged = merged[:insightMaxItems]
	}

	return merged
}

// splitNewsTitle memisahkan judul dari nama medianya. Google News menulisnya
// sebagai "Judul - Media", dan nama media itu tidak perlu ikut dikirim ke Gemini.
//
// Media dari elemen <source> dipakai lebih dulu bila judulnya memang diakhiri
// nama itu, karena beberapa media memakai tanda hubung di namanya sendiri
// ("Bisnis.com - Market") sehingga pemotongan dari belakang menyisakan potongan
// nama media di judul. Kalau tidak cocok, cara lama yang dipakai: potong di
// pemisah " - " terakhir.
func splitNewsTitle(raw, source string) (string, string) {
	raw = strings.TrimSpace(raw)
	source = strings.TrimSpace(source)

	if source != "" {
		if trimmed, ok := strings.CutSuffix(raw, " - "+source); ok {
			return strings.TrimSpace(trimmed), source
		}
	}

	index := strings.LastIndex(raw, " - ")
	if index <= 0 {
		return raw, ""
	}

	return strings.TrimSpace(raw[:index]), strings.TrimSpace(raw[index+3:])
}

// newsID memberi tiap berita penanda yang aman dipakai sebagai id elemen HTML.
// Judulnya sendiri tidak bisa dipakai karena mengandung spasi dan tanda baca.
func newsID(title string) string {
	hash := fnv.New32a()
	hash.Write([]byte(title))

	return fmt.Sprintf("gn-%08x", hash.Sum32())
}

// newsCategory menebak kategori dari kata kunci di judul. Feed RSS tidak membawa
// topik, jadi labelnya sederhana saja — gunanya hanya untuk warna badge di UI.
var newsCategoryKeywords = []struct {
	category string
	keywords []string
}{
	{"Inflasi", []string{"inflasi", "harga pangan", "daya beli", "kenaikan harga"}},
	{"Moneter", []string{"suku bunga", "the fed", "bank sentral", "bank indonesia", "obligasi", "moneter", "kebijakan moneter"}},
	{"Kripto", []string{"kripto", "bitcoin", "ethereum", "crypto", "aset digital"}},
	{"Kurs", []string{"rupiah", "kurs", "nilai tukar", "dolar as", "yuan", "yen", "mata uang"}},
	{"Saham", []string{"saham", "ihsg", "indeks", "bursa", "emiten", "pasar modal"}},
	{"Komoditas", []string{"minyak", "emas", "batu bara", "nikel", "komoditas", "gandum", "gas"}},
	{"Regulasi", []string{"pajak", "regulasi", "aturan", "kebijakan", "apbn", "subsidi", "ojk"}},
	{"Tenaga Kerja", []string{"pengangguran", "tenaga kerja", "upah", "phk", "lowongan"}},
	{"Geopolitik", []string{"konflik", "perang", "geopolitik", "timur tengah", "sanksi", "tarif impor"}},
}

func newsCategory(title string) string {
	lower := strings.ToLower(title)

	for _, entry := range newsCategoryKeywords {
		for _, keyword := range entry.keywords {
			if strings.Contains(lower, keyword) {
				return entry.category
			}
		}
	}

	return "Ekonomi"
}

// newsDate menormalkan pubDate feed (RFC1123) jadi YYYY-MM-DD, format yang sama
// dengan daftar contoh, supaya frontend dan pengurutan tanggal memakai satu
// bentuk saja. Tanggal yang gagal dibaca memakai tanggal hari ini.
func newsDate(raw string) string {
	if parsed, err := time.Parse(time.RFC1123, strings.TrimSpace(raw)); err == nil {
		return parsed.Format("2006-01-02")
	}

	return time.Now().Format("2006-01-02")
}

// newsSummary menyusun ringkasan sendiri karena description dari feed cuma
// berisi tautan dan nama media.
func newsSummary(media string) string {
	if strings.TrimSpace(media) == "" {
		return "Laporan berita ekonomi terkini."
	}

	return "Laporan " + media + "."
}

// contohInsightItems adalah daftar bawaan yang dipakai kalau feed tidak bisa
// dihubungi atau dimatikan, jadi halaman Insight Global tidak pernah kosong.
// Tanggalnya ikut ditulis tetap supaya urutannya tetap masuk akal.
func contohInsightItems() []InsightItem {
	items := make([]InsightItem, len(contohInsightNews))
	copy(items, contohInsightNews)

	return items
}

var contohInsightNews = []InsightItem{
	{
		ID:       "fed-suku-bunga",
		Title:    "The Fed Naikkan Suku Bunga Acuan 25 Basis Poin",
		Category: "Moneter",
		Date:     "2026-10-17",
		Summary:  "Bank sentral Amerika Serikat menaikkan suku bunga untuk menahan inflasi. Dolar menguat, sementara aset berisiko seperti saham teknologi dan kripto cenderung tertekan.",
	},
	{
		ID:       "timur-tengah",
		Title:    "Ketegangan Timur Tengah Memanas",
		Category: "Geopolitik",
		Date:     "2026-10-16",
		Summary:  "Eskalasi konflik di Timur Tengah membuat pasar mencari aset aman. Harga minyak dan emas naik, sedangkan saham global berfluktuasi tajam.",
	},
	{
		ID:       "inflasi-as-turun",
		Title:    "Inflasi AS Turun ke 2,4 Persen",
		Category: "Moneter",
		Date:     "2026-10-15",
		Summary:  "Angka inflasi tahunan Amerika Serikat turun lebih cepat dari perkiraan pasar. Harapan suku bunga ikut turun mengangkat saham dan kripto, sementara dolar melemah.",
	},
	{
		ID:       "yuan-melemah",
		Title:    "Yuan Melemah Terhadap Dolar",
		Category: "Kurs",
		Date:     "2026-10-14",
		Summary:  "Pelemahan yuan menekan mata uang Asia, termasuk rupiah, dan membuat barang impor lebih mahal bagi Indonesia.",
	},
	{
		ID:       "minyak-naik",
		Title:    "Harga Minyak Dunia Naik 4 Persen",
		Category: "Komoditas",
		Date:     "2026-10-13",
		Summary:  "Pemangkasan produksi OPEC dan permintaan musim dingin mendorong harga minyak naik, yang biasanya ikut mengangkat emas sebagai pelindung nilai inflasi.",
	},
	{
		ID:       "pengangguran-as",
		Title:    "Rilis Data Pengangguran AS Naik Tipis",
		Category: "Tenaga Kerja",
		Date:     "2026-10-12",
		Summary:  "Tingkat pengangguran Amerika Serikat naik menjadi 4,3 persen. Pasar membacanya sebagai tanda ekonomi melambat, sehingga harapan pemangkasan suku bunga kembali menguat.",
	},
	{
		ID:       "batu-bara",
		Title:    "Harga Batu Bara Global Melonjak",
		Category: "Komoditas",
		Date:     "2026-10-11",
		Summary:  "Permintaan pembangkit listrik Asia dan gangguan pasokan membuat harga batu bara melonjak. Saham emiten tambang ikut menguat, begitu pula mata uang negara pengekspor.",
	},
	{
		ID:       "pajak-kripto",
		Title:    "Regulasi Pajak Kripto di Indonesia Diperketat",
		Category: "Regulasi",
		Date:     "2026-10-10",
		Summary:  "Pemerintah menaikkan tarif pajak transaksi aset kripto dan mewajibkan pelaporan lewat bursa berizin. Pelaku pasar memperkirakan volume perdagangan jangka pendek menurun.",
	},
	{
		ID:       "suku-bunga-bi",
		Title:    "Bank Indonesia Tahan Suku Bunga Acuan",
		Category: "Moneter",
		Date:     "2026-10-09",
		Summary:  "BI memutuskan menahan suku bunga demi menjaga stabilitas rupiah di tengah ketidakpastian global. Keputusan ini menahan laju pelemahan rupiah tanpa menekan pertumbuhan kredit.",
	},
	{
		ID:       "pangan-global",
		Title:    "Harga Pangan Global Naik karena Cuaca Ekstrem",
		Category: "Inflasi",
		Date:     "2026-10-08",
		Summary:  "Gagal panen akibat cuaca ekstrem di beberapa negara produsen mengangkat harga gandum dan beras dunia, memicu kekhawatiran inflasi pangan di negara berkembang.",
	},
	{
		ID:       "ecb-tahan-suku-bunga",
		Title:    "ECB Tahan Suku Bunga, Sinyal Pemangkasan Mulai Terbuka",
		Category: "Moneter",
		Date:     "2026-10-07",
		Summary:  "Bank Sentral Eropa menahan suku bunga acuan tetapi membuka peluang pemangkasan bila inflasi terus melandai. Euro melemah terhadap dolar, dan saham pasar berkembang ikut mendapat aliran dana.",
	},
	{
		ID:       "opec-tambah-pasokan",
		Title:    "OPEC+ Tambah Pasokan, Harga Minyak Terkoreksi",
		Category: "Komoditas",
		Date:     "2026-10-06",
		Summary:  "Keputusan OPEC+ menambah produksi menekan harga minyak mentah. Saham emiten energi dan mata uang negara pengekspor minyak melemah, sementara biaya angkutan global berpotensi turun.",
	},
	{
		ID:       "etf-bitcoin-rekor",
		Title:    "Arus Masuk ETF Bitcoin Cetak Rekor Bulanan",
		Category: "Kripto",
		Date:     "2026-10-05",
		Summary:  "Dana investor asing yang masuk ke ETF Bitcoin mencapai rekor bulanan, menandakan permintaan institusi masih kuat. Harga kripto besar ikut terangkat, meski volatilitasnya tetap tinggi.",
	},
	{
		ID:       "ihsg-rekor-perbankan",
		Title:    "IHSG Cetak Rekor Baru Ditopang Sektor Perbankan",
		Category: "Saham",
		Date:     "2026-10-04",
		Summary:  "Indeks Harga Saham Gabungan menembus level tertinggi sepanjang sejarah, dipimpin saham bank besar. Aliran dana asing masuk kembali setelah rupiah stabil.",
	},
	{
		ID:       "indeks-dolar-menguat",
		Title:    "Indeks Dolar Menguat ke Level Tertinggi Tiga Bulan",
		Category: "Kurs",
		Date:     "2026-10-03",
		Summary:  "Dolar Amerika Serikat menguat terhadap hampir semua mata uang utama. Rupiah dan mata uang Asia tertekan, sehingga harga barang impor dan utang valas jadi lebih mahal.",
	},
	{
		ID:       "inflasi-indonesia-terkendali",
		Title:    "Inflasi Indonesia Terkendali di 2,8 Persen",
		Category: "Inflasi",
		Date:     "2026-10-02",
		Summary:  "Laju inflasi tahunan Indonesia tetap di dalam sasaran bank sentral. Daya beli masyarakat terjaga dan ruang penurunan suku bunga kembali terbuka, kabar baik bagi saham dan obligasi.",
	},
	{
		ID:       "selat-taiwan-semikonduktor",
		Title:    "Ketegangan Selat Taiwan Ganggu Rantai Pasok Semikonduktor",
		Category: "Geopolitik",
		Date:     "2026-10-01",
		Summary:  "Meningkatnya ketegangan membuat pengiriman komponen chip terganggu. Saham teknologi global bergejolak, sementara emas kembali dicari sebagai aset aman.",
	},
	{
		ID:       "gas-eropa-melonjak",
		Title:    "Harga Gas Eropa Melonjak Menjelang Musim Dingin",
		Category: "Komoditas",
		Date:     "2026-09-30",
		Summary:  "Kebutuhan pemanas dan pasokan yang ketat mendorong harga gas alam Eropa naik tajam. Biaya listrik industri meningkat dan saham produsen energi ikut menguat.",
	},
	{
		ID:       "yield-obligasi-as",
		Title:    "Imbal Hasil Obligasi AS Sentuh 4,8 Persen",
		Category: "Moneter",
		Date:     "2026-09-29",
		Summary:  "Imbal hasil surat utang Amerika Serikat naik ke level tertinggi tahun ini. Aset berisiko seperti saham teknologi dan kripto cenderung ditinggalkan sementara waktu demi obligasi yang lebih aman.",
	},
	{
		ID:       "nikel-tertekan-pasokan",
		Title:    "Harga Nikel Tertekan Pasokan Indonesia",
		Category: "Komoditas",
		Date:     "2026-09-28",
		Summary:  "Bertambahnya pasokan nikel dari Indonesia menekan harga dunia, meski permintaan baterai tetap tumbuh. Emiten tambang nikel menghadapi tekanan margin.",
	},
}
