package seeders

import (
	"edukasi-finansial-goedukasi-finansial-go/app/facades"
	"edukasi-finansial-goedukasi-finansial-go/app/models"
)

type EducationQuizSeeder struct{}

// Signature The name and signature of the seeder.
func (s *EducationQuizSeeder) Signature() string {
	return "EducationQuizSeeder"
}

// quizTargetPerModule adalah ukuran bank soal bawaan untuk satu modul. Kuis
// hanya menampilkan tiga soal acak, jadi sepuluh soal memberi variasi yang cukup
// tanpa membuat user harus menjawab semuanya.
const quizTargetPerModule = 10

// Run mengisi bank soal bawaan. Kalau tabel kuis masih kosong, sepuluh soal per
// modul disisipkan. Kalau sudah ada isinya, seeder hanya menambahkan soal bawaan
// yang belum ada sampai tiap modul punya sepuluh soal, dan melengkapi option_d
// serta explanation yang kosong. Pertanyaan, kunci jawaban, dan suntingan admin
// yang sudah ada tidak diubah.
func (s *EducationQuizSeeder) Run() error {
	modules := make([]models.EducationModule, 0)
	if err := facades.Orm().Query().Get(&modules); err != nil {
		return err
	}

	byTitle := make(map[string]uint, len(modules))
	for _, module := range modules {
		byTitle[module.Title] = module.ID
	}

	seeds := educationQuizSeeds()

	existing := make([]models.EducationQuiz, 0)
	if err := facades.Orm().Query().Get(&existing); err != nil {
		return err
	}

	if len(existing) == 0 {
		pending := make([]models.EducationQuiz, 0, len(seeds)*3)
		for _, seed := range seeds {
			moduleID, found := byTitle[seed.ModuleTitle]
			if !found {
				continue
			}

			for _, question := range seed.Questions {
				question.ModuleID = moduleID
				pending = append(pending, question)
			}
		}

		if len(pending) == 0 {
			return nil
		}

		return facades.Orm().Query().Create(&pending)
	}

	// Peta module -> pertanyaan -> baris, dipakai untuk backfill kolom baru dan
	// untuk menghitung soal mana yang belum ada tanpa mengubah kunci jawaban
	// yang sudah tersimpan.
	byModule := make(map[uint]map[string]models.EducationQuiz, len(modules))
	for _, row := range existing {
		if byModule[row.ModuleID] == nil {
			byModule[row.ModuleID] = make(map[string]models.EducationQuiz)
		}
		byModule[row.ModuleID][row.Question] = row
	}

	pending := make([]models.EducationQuiz, 0, len(seeds)*quizTargetPerModule)
	for _, seed := range seeds {
		moduleID, found := byTitle[seed.ModuleTitle]
		if !found {
			continue
		}
		if byModule[moduleID] == nil {
			byModule[moduleID] = make(map[string]models.EducationQuiz)
		}

		for _, question := range seed.Questions {
			old, found := byModule[moduleID][question.Question]
			if found {
				changed := false
				if old.OptionD == "" {
					old.OptionD = question.OptionD
					changed = true
				}
				if old.Explanation == "" {
					old.Explanation = question.Explanation
					changed = true
				}
				if !changed {
					continue
				}

				if err := facades.Orm().Query().Save(&old); err != nil {
					return err
				}
				continue
			}

			if len(byModule[moduleID]) >= quizTargetPerModule {
				continue
			}

			question.ModuleID = moduleID
			pending = append(pending, question)
			// Dicatat di peta supaya satu pertanyaan tidak masuk dua kali kalau
			// daftar seed berisi judul yang sama.
			byModule[moduleID][question.Question] = question
		}
	}

	if len(pending) == 0 {
		return nil
	}

	return facades.Orm().Query().Create(&pending)
}

// quizSeed mengikat satu modul (lewat judulnya) dengan soal-soalnya.
type quizSeed struct {
	ModuleTitle string
	Questions   []models.EducationQuiz
}

// quiz memperpendek penulisan satu soal; jawaban benar selalu huruf pilihan.
// Pilihan D diisi kalimat pengecoh yang aman supaya semua baris punya empat
// pilihan, dan penjelasan singkat diambil dari teks jawaban yang benar.
func quiz(question, optionA, optionB, optionC, correct string) models.EducationQuiz {
	correctText := map[string]string{
		"a": optionA,
		"b": optionB,
		"c": optionC,
	}[correct]

	return models.EducationQuiz{
		Question:      question,
		OptionA:       optionA,
		OptionB:       optionB,
		OptionC:       optionC,
		OptionD:       "Tidak ada jawaban di atas yang benar",
		CorrectAnswer: correct,
		Explanation:   "Jawaban yang benar adalah " + correctText + ".",
	}
}

func educationQuizSeeds() []quizSeed {
	return []quizSeed{
		{
			ModuleTitle: "Dasar Investasi",
			Questions: []models.EducationQuiz{
				quiz(
					"Apa yang membuat uang yang dibiarkan diam kehilangan daya belinya?",
					"Bunga bank", "Inflasi", "Nilai tukar", "b",
				),
				quiz(
					"Berapa besar dana darurat yang disarankan sebelum mulai berinvestasi?",
					"3-6 bulan pengeluaran", "Satu minggu pengeluaran", "Tidak perlu dana darurat", "a",
				),
				quiz(
					"Manakah keuntungan yang berbentuk arus kas?",
					"Selisih harga jual saham", "Dividen yang dibagikan perusahaan", "Kenaikan harga emas", "b",
				),
				quiz(
					"Apa yang dimaksud bunga majemuk?",
					"Bunga yang dihitung dari pokok dan bunga sebelumnya", "Bunga tetap setiap tahun", "Biaya administrasi bank", "a",
				),
				quiz(
					"Mengapa investasi perlu tujuan yang jelas?",
					"Agar mudah memilih instrumen dan durasi", "Agar bisa mengikuti tren", "Agar tidak perlu menabung", "a",
				),
				quiz(
					"Urutan yang disarankan sebelum mulai berinvestasi adalah...",
					"Dana darurat, lunasi utang mahal, baru investasi", "Investasi, baru dana darurat", "Utang konsumtif, investasi", "a",
				),
				quiz(
					"Apa yang dimaksud return investasi?",
					"Keuntungan atau kerugian dari investasi", "Jumlah uang yang disetor", "Biaya transaksi", "a",
				),
				quiz(
					"Risiko dalam investasi berarti...",
					"Kemungkinan hasil tidak sesuai harapan", "Pasti kehilangan seluruh modal", "Imbal hasil yang selalu naik", "a",
				),
				quiz(
					"Instrumen dengan potensi imbal hasil tinggi umumnya...",
					"Memiliki risiko lebih tinggi", "Bebas risiko", "Dijamin pemerintah", "a",
				),
				quiz(
					"Apa peran inflasi dalam perencanaan investasi?",
					"Menentukan target imbal hasil riil", "Tidak memengaruhi tujuan", "Menghapus risiko pasar", "a",
				),
			},
		},
		{
			ModuleTitle: "Apa itu DCA?",
			Questions: []models.EducationQuiz{
				quiz(
					"Apa inti dari strategi Dollar Cost Averaging?",
					"Membeli dengan nominal tetap secara berkala", "Membeli hanya saat harga terendah", "Menjual saat harga tertinggi", "a",
				),
				quiz(
					"Kapan DCA paling cocok dipakai?",
					"Saat yakin pasar akan naik bulan depan", "Untuk dana yang datang bertahap seperti gaji", "Hanya untuk aset kripto", "b",
				),
				quiz(
					"Apa yang diratakan oleh DCA?",
					"Beban pajak tahunan", "Harga rata-rata pembelian", "Jumlah dividen yang diterima", "b",
				),
				quiz(
					"Apa kelemahan utama DCA?",
					"Tidak selalu membeli di titik terendah", "Selalu menghasilkan kerugian", "Tidak bisa dilakukan otomatis", "a",
				),
				quiz(
					"DCA membantu mengurangi risiko...",
					"Membeli seluruh dana pada satu harga", "Biaya pajak", "Inflasi", "a",
				),
				quiz(
					"Dana yang tepat untuk DCA berasal dari...",
					"Uang yang memang disisihkan tiap periode", "Dana darurat", "Pinjaman konsumtif", "a",
				),
				quiz(
					"Jika harga turun saat DCA, jumlah unit yang dibeli...",
					"Lebih banyak", "Lebih sedikit", "Tetap", "a",
				),
				quiz(
					"DCA cocok untuk investor yang...",
					"Tidak ingin memprediksi waktu pasar", "Ingin trading setiap jam", "Tidak punya tujuan", "a",
				),
				quiz(
					"Apa yang perlu dievaluasi secara berkala dalam DCA?",
					"Tujuan, nominal, dan profil risiko", "Warna aplikasi", "Jumlah pengikut media sosial", "a",
				),
				quiz(
					"DCA bukan jaminan...",
					"Keuntungan", "Biaya transaksi", "Adanya fluktuasi", "a",
				),
			},
		},
		{
			ModuleTitle: "Profil Risiko",
			Questions: []models.EducationQuiz{
				quiz(
					"Apa bedanya kemampuan menanggung risiko dan toleransi risiko?",
					"Kemampuan soal kondisi keuangan, toleransi soal kenyamanan psikologis", "Keduanya istilah yang sama", "Kemampuan soal usia, toleransi soal penghasilan", "a",
				),
				quiz(
					"Cara menguji profil risiko yang paling masuk akal adalah...",
					"Membayangkan portofolio turun 30% dan menilai reaksimu", "Melihat produk dengan kinerja terbaik tahun lalu", "Mengikuti pilihan teman sekelas", "a",
				),
				quiz(
					"Manakah pernyataan yang benar tentang profil risiko?",
					"Sejak lahir menetap dan tidak akan berubah", "Bisa berubah saat penghasilan atau tujuan berubah", "Hanya ditentukan oleh usia", "b",
				),
				quiz(
					"Profil agresif cocok untuk investor yang...",
					"Siap menghadapi fluktuasi besar demi potensi imbal hasil tinggi", "Membutuhkan dana bulan depan", "Menyimpan dana darurat", "a",
				),
				quiz(
					"Profil konservatif biasanya memprioritaskan...",
					"Menjaga modal", "Keuntungan maksimal", "Trading harian", "a",
				),
				quiz(
					"Apa saja faktor yang memengaruhi profil risiko?",
					"Usia, penghasilan, tujuan, dan pengalaman", "Warna favorit", "Jumlah aplikasi", "a",
				),
				quiz(
					"Hasil kuesioner profil risiko sebaiknya...",
					"Ditinjau saat kondisi keuangan berubah", "Diabaikan selamanya", "Disamakan dengan teman", "a",
				),
				quiz(
					"Dana darurat sebaiknya ditempatkan pada...",
					"Instrumen likuid dan berisiko rendah", "Aset spekulatif", "Pinjaman online", "a",
				),
				quiz(
					"Horizon investasi pendek membuat investor...",
					"Lebih rentan terhadap fluktuasi", "Kebal risiko", "Bisa menunggu pemulihan lama", "a",
				),
				quiz(
					"Toleransi risiko berbeda dengan kemampuan risiko karena...",
					"Toleransi soal kenyamanan, kemampuan soal kondisi keuangan", "Keduanya hanya soal usia", "Tidak ada bedanya", "a",
				),
			},
		},
		{
			ModuleTitle: "Diversifikasi",
			Questions: []models.EducationQuiz{
				quiz(
					"Kunci diversifikasi yang benar adalah memiliki aset...",
					"Yang berasal dari satu sektor yang sama", "Yang pergerakannya tidak selalu bersamaan", "Favorit dalam jumlah besar", "b",
				),
				quiz(
					"Apa yang dimaksud rebalancing?",
					"Menjual seluruh aset saat rugi", "Mengembalikan porsi aset ke target awal", "Menambah aset yang paling cepat naik", "b",
				),
				quiz(
					"Risiko apa yang tidak bisa dihapus oleh diversifikasi?",
					"Risiko pasar saat krisis besar", "Risiko salah memilih satu saham", "Risiko konsentrasi pada satu aset", "a",
				),
				quiz(
					"Diversifikasi bisa dilakukan antar...",
					"Kelas aset, sektor, dan wilayah", "Satu saham saja", "Satu aplikasi", "a",
				),
				quiz(
					"Korelasi rendah antar aset berarti...",
					"Pergerakannya tidak selalu searah", "Pasti untung", "Tidak ada risiko", "a",
				),
				quiz(
					"Terlalu banyak aset bisa...",
					"Menyulitkan pemantauan dan menambah biaya", "Menghapus semua risiko", "Menjamin dividen", "a",
				),
				quiz(
					"Rebalancing dilakukan ketika...",
					"Porsi aset menyimpang dari target", "Setiap kali harga naik", "Tidak pernah", "a",
				),
				quiz(
					"Diversifikasi tidak melindungi dari...",
					"Risiko sistemik pasar", "Risiko satu saham", "Risiko konsentrasi", "a",
				),
				quiz(
					"Contoh diversifikasi sederhana adalah...",
					"Memiliki saham dan obligasi", "Membeli satu saham dengan seluruh dana", "Menyimpan uang di satu aplikasi", "a",
				),
				quiz(
					"Tujuan diversifikasi adalah...",
					"Menyeimbangkan risiko dan imbal hasil", "Menghilangkan semua kerugian", "Menaikkan biaya", "a",
				),
			},
		},
		{
			ModuleTitle: "Disiplin dan Emosi",
			Questions: []models.EducationQuiz{
				quiz(
					"Apa gunanya daftar periksa sebelum bertransaksi?",
					"Memastikan alasan transaksi sesuai rencana", "Mencatat saham yang sedang naik", "Menyimpan harga tiap menit", "a",
				),
				quiz(
					"Kenapa harga tidak perlu diperiksa setiap saat?",
					"Karena menghabiskan kuota internet", "Karena memicu keputusan mendesak yang tidak perlu", "Karena harga tidak pernah berubah", "b",
				),
				quiz(
					"Apa yang layak dicatat dalam jurnal keputusan?",
					"Tanggal, alasan, dan ekspektasi transaksi", "Saldo akhir bulan saja", "Daftar teman yang ikut berinvestasi", "a",
				),
				quiz(
					"Apa itu herding behavior?",
					"Mengikuti kerumunan tanpa analisis", "Investasi mandiri", "Diversifikasi", "a",
				),
				quiz(
					"Apa itu anchoring?",
					"Terpaku pada satu angka acuan", "Menghitung rata-rata", "Menjual semua", "a",
				),
				quiz(
					"Apa itu sunk cost fallacy?",
					"Melanjutkan karena sudah mengeluarkan biaya", "Menghindari biaya", "Menghitung keuntungan", "a",
				),
				quiz(
					"Cara mengurangi bias emosi adalah...",
					"Gunakan checklist dan aturan otomatis", "Ikuti berita terus", "Trading setiap hari", "a",
				),
				quiz(
					"Keputusan investasi sebaiknya dibuat...",
					"Saat tenang dan sesuai rencana", "Saat panik", "Saat FOMO", "a",
				),
				quiz(
					"Apa itu recency bias?",
					"Terlalu fokus pada kejadian terbaru", "Mengingat semua data", "Menghindari risiko", "a",
				),
				quiz(
					"Investor yang baik adalah yang...",
					"Konsisten dengan prosesnya", "Selalu untung", "Tidak pernah rugi", "a",
				),
			},
		},
		{
			ModuleTitle: "Mengenal Saham",
			Questions: []models.EducationQuiz{
				quiz(
					"Membeli saham berarti...",
					"Memberi pinjaman kepada perusahaan", "Memiliki sebagian kecil perusahaan", "Menabung dengan bunga tetap", "b",
				),
				quiz(
					"Satu lot saham di bursa sama dengan...",
					"1 lembar", "10 lembar", "100 lembar", "c",
				),
				quiz(
					"Apa dua sumber keuntungan memegang saham?",
					"Capital gain dan dividen", "Bunga dan pajak", "Kupon dan diskonto", "a",
				),
				quiz(
					"Apa itu capital gain?",
					"Selisih harga jual dan beli saham", "Dividen", "Biaya transaksi", "a",
				),
				quiz(
					"IHSG adalah...",
					"Indeks harga saham gabungan bursa Indonesia", "Nama perusahaan sekuritas", "Produk reksadana", "a",
				),
				quiz(
					"Harga saham dipengaruhi oleh...",
					"Kinerja perusahaan, sentimen pasar, dan kondisi ekonomi", "Warna logo", "Jumlah kantor cabang", "a",
				),
				quiz(
					"Apa itu dividen?",
					"Bagian laba yang dibagikan ke pemegang saham", "Biaya beli saham", "Bunga pinjaman", "a",
				),
				quiz(
					"Risiko memiliki satu saham adalah...",
					"Konsentrasi pada satu perusahaan", "Tidak bisa dijual", "Tidak ada risiko", "a",
				),
				quiz(
					"Apa itu right issue?",
					"Penawaran saham baru kepada pemegang saham lama", "Pembagian dividen", "Penghapusan saham", "a",
				),
				quiz(
					"Sebelum membeli saham, sebaiknya membaca...",
					"Laporan keuangan dan prospektus", "Grup obrolan", "Iklan", "a",
				),
			},
		},
		{
			ModuleTitle: "Reksadana vs Saham",
			Questions: []models.EducationQuiz{
				quiz(
					"NAB dalam reksadana adalah...",
					"Nilai aktiva bersih per unit", "Jumlah unit yang kamu miliki", "Biaya pengelolaan tahunan", "a",
				),
				quiz(
					"Jenis reksadana dengan risiko paling rendah adalah...",
					"Reksadana saham", "Reksadana pasar uang", "Reksadana campuran", "b",
				),
				quiz(
					"Reksadana sebaiknya dibeli melalui...",
					"Agen penjual yang terdaftar di OJK", "Akun media sosial tanpa identitas", "Grup percakapan yang menjanjikan keuntungan", "a",
				),
				quiz(
					"Reksadana dikelola oleh...",
					"Manajer investasi", "Bank sentral", "Bursa", "a",
				),
				quiz(
					"Salah satu keunggulan reksadana adalah...",
					"Diversifikasi sudah dilakukan manajer investasi", "Bebas biaya", "Dijamin untung", "a",
				),
				quiz(
					"Saham bisa diperdagangkan secara real time, sedangkan reksadana...",
					"Dihitung dengan NAB pada akhir hari", "Tidak bisa dijual", "Selalu lebih mahal", "a",
				),
				quiz(
					"Risiko reksadana saham lebih tinggi karena...",
					"Isinya saham", "Dikelola profesional", "Biayanya murah", "a",
				),
				quiz(
					"Apa yang perlu dicek sebelum membeli reksadana?",
					"Prospektus, biaya, dan profil risiko", "Jumlah pengikut manajer", "Warna aplikasi", "a",
				),
				quiz(
					"Reksadana pasar uang cocok untuk...",
					"Tujuan jangka pendek", "Tujuan 20 tahun", "Trading harian", "a",
				),
				quiz(
					"Saham memberi hak...",
					"Suara dalam RUPS", "Jaminan bunga tetap", "Pengembalian modal", "a",
				),
			},
		},
		{
			ModuleTitle: "Cara Membaca Grafik",
			Questions: []models.EducationQuiz{
				quiz(
					"Satu batang candlestick merangkum...",
					"Harga buka, tutup, tertinggi, dan terendah", "Hanya harga penutupan", "Besarnya dividen", "a",
				),
				quiz(
					"Volume besar pada suatu pergerakan harga menandakan...",
					"Banyak pelaku pasar sependapat dengan arah harga", "Harga pasti akan naik esok hari", "Saham tersebut akan dihentikan perdagangannya", "a",
				),
				quiz(
					"Untuk investasi jangka panjang, rentang waktu grafik yang lebih sesuai adalah...",
					"Per menit", "Harian atau mingguan", "Per detik", "b",
				),
				quiz(
					"Apa arti candle hijau?",
					"Harga tutup lebih tinggi dari harga buka", "Harga turun", "Volume nol", "a",
				),
				quiz(
					"Support adalah...",
					"Area harga yang cenderung menahan penurunan", "Batas keuntungan", "Nama indikator", "a",
				),
				quiz(
					"Resistance adalah...",
					"Area harga yang cenderung menahan kenaikan", "Biaya transaksi", "Rata-rata volume", "a",
				),
				quiz(
					"Moving average berguna untuk...",
					"Melihat arah tren", "Menjamin harga", "Menghitung pajak", "a",
				),
				quiz(
					"Grafik harian cocok untuk...",
					"Melihat tren jangka menengah", "Trading per detik", "Menghitung dividen", "a",
				),
				quiz(
					"Sinyal grafik sebaiknya...",
					"Dikonfirmasi dengan analisis lain", "Langsung dipercaya", "Diabaikan", "a",
				),
				quiz(
					"Volume menunjukkan...",
					"Jumlah transaksi yang terjadi", "Harga pembukaan", "Dividen", "a",
				),
			},
		},
		{
			ModuleTitle: "Pajak Investasi",
			Questions: []models.EducationQuiz{
				quiz(
					"Pajak final atas penjualan saham dihitung dari...",
					"Keuntungan saja", "Nilai transaksi penjualan", "Besarnya dividen", "b",
				),
				quiz(
					"Dividen dapat dikecualikan dari pajak bila...",
					"Diinvestasikan kembali di Indonesia dalam jangka waktu tertentu", "Dibelanjakan untuk konsumsi", "Dibiarkan di rekening bank", "a",
				),
				quiz(
					"Angka imbal hasil yang adil dibandingkan dengan inflasi adalah...",
					"Imbal hasil kotor", "Imbal hasil bersih setelah pajak dan biaya", "Proyeksi yang tertulis di iklan", "b",
				),
				quiz(
					"Pajak dividen saham di Indonesia bagi investor individu...",
					"Final 10%", "0%", "50%", "a",
				),
				quiz(
					"Pajak penjualan saham di bursa...",
					"0,1% dari nilai transaksi", "10% dari keuntungan", "Tidak ada", "a",
				),
				quiz(
					"Biaya investasi yang perlu diperhitungkan...",
					"Komisi, spread, dan biaya administrasi", "Pajak bumi", "Biaya sekolah", "a",
				),
				quiz(
					"Imbal hasil bersih adalah...",
					"Imbal hasil setelah pajak dan biaya", "Imbal hasil sebelum biaya", "Imbal hasil kotor", "a",
				),
				quiz(
					"Pencatatan transaksi membantu...",
					"Menghitung pajak dan evaluasi", "Menambah risiko", "Menghindari pajak", "a",
				),
				quiz(
					"Pajak bisa mengubah...",
					"Keputusan jual beli", "Harga saham secara langsung", "Jumlah perusahaan", "a",
				),
				quiz(
					"Aturan pajak sebaiknya...",
					"Dicek ke sumber resmi karena bisa berubah", "Diabaikan", "Diikuti dari grup", "a",
				),
			},
		},
		{
			ModuleTitle: "Psikologi Investasi",
			Questions: []models.EducationQuiz{
				quiz(
					"FOMO membuat investor cenderung...",
					"Membeli saat harga sudah tinggi karena takut ketinggalan", "Membeli saat harga sedang murah", "Menahan pembelian lebih lama", "a",
				),
				quiz(
					"Terpaku pada harga beli membuat investor...",
					"Menahan aset hanya karena menunggu balik modal", "Menjual aset saat untung", "Menambah diversifikasi", "a",
				),
				quiz(
					"Cara paling sehat menanggapi berita pasar harian adalah...",
					"Menyesuaikan rencana setiap hari", "Kembali pada rencana dan horison waktu sendiri", "Mengikuti arahan grup percakapan", "b",
				),
				quiz(
					"Apa itu loss aversion?",
					"Kecenderungan merasakan kerugian lebih kuat daripada keuntungan", "Keinginan membeli saat rugi", "Kebiasaan mencatat", "a",
				),
				quiz(
					"Rencana investasi tertulis membantu...",
					"Mengurangi keputusan impulsif", "Menjamin keuntungan", "Menghindari pajak", "a",
				),
				quiz(
					"Apa yang sebaiknya dilakukan saat pasar panik?",
					"Kembali ke rencana dan horizon waktu", "Menjual semua aset", "Membeli tanpa batas", "a",
				),
				quiz(
					"Overconfidence membuat investor...",
					"Meremehkan risiko", "Lebih disiplin", "Selalu benar", "a",
				),
				quiz(
					"Jurnal transaksi berguna untuk...",
					"Mengevaluasi proses, bukan hanya hasil", "Menambah pengikut", "Menghitung pajak otomatis", "a",
				),
				quiz(
					"Apa itu bias konfirmasi?",
					"Mencari informasi yang mendukung keyakinan sendiri", "Mengabaikan semua berita", "Menghitung risiko", "a",
				),
				quiz(
					"Disiplin investasi paling mudah dijaga dengan...",
					"Aturan otomatis dan nominal tetap", "Melihat harga tiap menit", "Mengikuti tren", "a",
				),
			},
		},
	}
}
