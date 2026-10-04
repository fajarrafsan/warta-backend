package model

import (
	"strings"
	"time"
)

type ArticleStatus string

const (
	StatusDraft     ArticleStatus = "draft"
	StatusScheduled ArticleStatus = "scheduled"
	StatusPublished ArticleStatus = "published"
	StatusArchived  ArticleStatus = "archived"
)

func (s ArticleStatus) Valid() bool {
	switch s {
	case StatusDraft, StatusScheduled, StatusPublished, StatusArchived:
		return true
	}
	return false
}

// Article memuat kolom tabel articles beserta data hasil join yang selalu
// ikut ditampilkan: nama penulis, kategori, tag, dan jumlah komentar.
type Article struct {
	ID           int64
	Title        string
	Slug         string
	Content      string
	Excerpt      string
	Status       ArticleStatus
	AuthorID     int64
	AuthorName   string
	AuthorAvatar string
	CategoryID   int64
	CategoryName string
	CategorySlug string
	Tags         []Tag
	CommentCount int
	// CoverImage adalah path gambar sampul, misalnya /uploads/ab12....webp.
	// Kosong berarti tanpa sampul.
	CoverImage string
	ViewCount  int
	LikeCount  int
	// ContentLength adalah panjang isi dalam karakter, dasar perkiraan waktu baca.
	ContentLength int
	PublishedAt   *time.Time
	// ScheduledAt hanya terisi untuk artikel berstatus scheduled.
	ScheduledAt *time.Time
	// Snippet adalah potongan isi di sekitar kata yang dicari, hanya terisi
	// di hasil pencarian.
	Snippet   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (a Article) IsPublished() bool {
	return a.Status == StatusPublished
}

// charsPerMinute kira-kira 200 kata per menit untuk teks berbahasa Indonesia.
const charsPerMinute = 1200

// ReadingMinutes memperkirakan lama membaca, paling sedikit satu menit.
func (a Article) ReadingMinutes() int {
	return max(1, (a.ContentLength+charsPerMinute-1)/charsPerMinute)
}

func (a Article) TagNames() []string {
	names := make([]string, 0, len(a.Tags))
	for _, t := range a.Tags {
		names = append(names, t.Name)
	}
	return names
}

// Excerpt meringkas isi Markdown menjadi teks biasa paling banyak n karakter,
// dipotong di batas kata supaya tidak berhenti di tengah kata.
func Excerpt(markdown string, n int) string {
	text := strings.Join(strings.Fields(PlainText(markdown)), " ")

	runes := []rune(text)
	if len(runes) <= n {
		return text
	}

	cut := string(runes[:n])
	// Bila karakter berikutnya spasi, potongan sudah berhenti di akhir kata.
	if runes[n] != ' ' {
		if i := strings.LastIndex(cut, " "); i > 0 {
			cut = cut[:i]
		}
	}
	return strings.TrimRight(cut, " ,.;:-") + "…"
}

// Revision adalah salinan isi artikel pada satu waktu.
type Revision struct {
	ID         int64
	ArticleID  int64
	EditorID   int64
	EditorName string
	Title      string
	Content    string
	CategoryID int64
	Tags       []string
	CoverImage string
	// Characters adalah panjang isi; di daftar revisi isi lengkap tidak dimuat.
	Characters int
	CreatedAt  time.Time
}
