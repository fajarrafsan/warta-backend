package dto

import (
	"strings"
	"time"

	"warta/internal/model"
)

// ArticleRequest dipakai POST dan PUT, semua field wajib dikirim.
type ArticleRequest struct {
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	CategoryID int64    `json:"category_id"`
	Tags       []string `json:"tags"`
	Status     string   `json:"status"`
	// CoverImage adalah path hasil POST /api/v1/uploads. Kosong berarti tanpa sampul.
	CoverImage string `json:"cover_image"`
	// ScheduledAt wajib untuk status scheduled dan diabaikan untuk status lain.
	ScheduledAt *time.Time `json:"scheduled_at"`
}

func (r *ArticleRequest) Normalize() {
	r.Title = collapseSpaces(r.Title)
	r.Content = strings.TrimSpace(r.Content)
	r.CoverImage = strings.TrimSpace(r.CoverImage)
	r.Status = strings.ToLower(strings.TrimSpace(r.Status))

	seen := make(map[string]bool, len(r.Tags))
	tags := make([]string, 0, len(r.Tags))
	for _, t := range r.Tags {
		t = NormalizeTag(t)
		if t != "" && !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	r.Tags = tags
}

// AsPatch mengubah request lengkap menjadi patch yang mengisi semua field,
// sehingga PUT dan PATCH berbagi satu jalur di service. Tags yang tidak
// dikirim pada PUT berarti article tidak punya tag.
func (r ArticleRequest) AsPatch() ArticlePatch {
	tags := r.Tags
	if tags == nil {
		tags = []string{}
	}
	return ArticlePatch{
		Title:       &r.Title,
		Content:     &r.Content,
		CategoryID:  &r.CategoryID,
		Tags:        &tags,
		Status:      &r.Status,
		CoverImage:  &r.CoverImage,
		ScheduledAt: r.ScheduledAt,
	}
}

// ArticlePatch dipakai PATCH. Field yang tidak dikirim (nil) tidak diubah.
type ArticlePatch struct {
	Title      *string   `json:"title"`
	Content    *string   `json:"content"`
	CategoryID *int64    `json:"category_id"`
	Tags       *[]string `json:"tags"`
	Status     *string   `json:"status"`
	CoverImage *string   `json:"cover_image"`
	// ScheduledAt nil berarti waktu terjadwal yang tersimpan tetap dipakai.
	ScheduledAt *time.Time `json:"scheduled_at"`
}

// Apply menimpa base dengan field yang dikirim. Hasilnya request lengkap yang
// divalidasi dengan aturan yang sama seperti saat membuat article.
func (p ArticlePatch) Apply(base ArticleRequest) ArticleRequest {
	if p.Title != nil {
		base.Title = *p.Title
	}
	if p.Content != nil {
		base.Content = *p.Content
	}
	if p.CategoryID != nil {
		base.CategoryID = *p.CategoryID
	}
	if p.Tags != nil {
		base.Tags = *p.Tags
	}
	if p.Status != nil {
		base.Status = *p.Status
	}
	if p.CoverImage != nil {
		base.CoverImage = *p.CoverImage
	}
	if p.ScheduledAt != nil {
		base.ScheduledAt = p.ScheduledAt
	}
	return base
}

type ArticleQuery struct {
	Query    string
	Category string
	Tag      string
	AuthorID int64
	Status   string
	Sort     string
}

// Engagement adalah keadaan suka dan bookmark satu artikel bagi pembaca.
type Engagement struct {
	Liked      bool `json:"liked"`
	Bookmarked bool `json:"bookmarked"`
	LikeCount  int  `json:"like_count"`
}

// ArticleSummary dipakai di daftar article: isi lengkap diganti cuplikan.
type ArticleSummary struct {
	ID             int64          `json:"id"`
	Title          string         `json:"title"`
	Slug           string         `json:"slug"`
	Excerpt        string         `json:"excerpt"`
	Status         string         `json:"status"`
	Author         AuthorResponse `json:"author"`
	Category       CategoryRef    `json:"category"`
	Tags           []TagRef       `json:"tags"`
	CoverImage     *string        `json:"cover_image"`
	CommentCount   int            `json:"comment_count"`
	LikeCount      int            `json:"like_count"`
	ViewCount      int            `json:"view_count"`
	ReadingMinutes int            `json:"reading_minutes"`
	PublishedAt    *time.Time     `json:"published_at"`
	ScheduledAt    *time.Time     `json:"scheduled_at"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	// Snippet hanya ada di hasil pencarian: potongan isi di sekitar kata
	// yang dicari.
	Snippet string `json:"snippet,omitempty"`
}

type ArticleResponse struct {
	ArticleSummary
	Content string `json:"content"`
	// Liked dan Bookmarked selalu false bagi pengunjung yang belum login.
	Liked      bool `json:"liked"`
	Bookmarked bool `json:"bookmarked"`
}

func NewArticleSummary(a model.Article) ArticleSummary {
	tags := make([]TagRef, 0, len(a.Tags))
	for _, t := range a.Tags {
		tags = append(tags, TagRef{ID: t.ID, Name: t.Name, Slug: t.Slug})
	}

	var cover *string
	if a.CoverImage != "" {
		cover = &a.CoverImage
	}

	return ArticleSummary{
		ID:             a.ID,
		Title:          a.Title,
		Slug:           a.Slug,
		Excerpt:        a.Excerpt,
		Status:         string(a.Status),
		Author:         AuthorResponse{ID: a.AuthorID, Name: a.AuthorName, AvatarURL: optional(a.AuthorAvatar)},
		Category:       CategoryRef{ID: a.CategoryID, Name: a.CategoryName, Slug: a.CategorySlug},
		Tags:           tags,
		CoverImage:     cover,
		CommentCount:   a.CommentCount,
		LikeCount:      a.LikeCount,
		ViewCount:      a.ViewCount,
		ReadingMinutes: a.ReadingMinutes(),
		PublishedAt:    a.PublishedAt,
		ScheduledAt:    a.ScheduledAt,
		CreatedAt:      a.CreatedAt,
		UpdatedAt:      a.UpdatedAt,
		Snippet:        a.Snippet,
	}
}

func NewArticleSummaries(articles []model.Article) []ArticleSummary {
	summaries := make([]ArticleSummary, 0, len(articles))
	for _, a := range articles {
		summaries = append(summaries, NewArticleSummary(a))
	}
	return summaries
}

func NewArticleResponse(a model.Article) ArticleResponse {
	return ArticleResponse{ArticleSummary: NewArticleSummary(a), Content: a.Content}
}

// RevisionSummary dipakai di daftar riwayat, tanpa isi lengkap.
type RevisionSummary struct {
	ID         int64           `json:"id"`
	Title      string          `json:"title"`
	Editor     *AuthorResponse `json:"editor"`
	Characters int             `json:"characters"`
	CreatedAt  time.Time       `json:"created_at"`
}

type RevisionResponse struct {
	RevisionSummary
	Content    string   `json:"content"`
	CategoryID *int64   `json:"category_id"`
	Tags       []string `json:"tags"`
	CoverImage *string  `json:"cover_image"`
}

func NewRevisionSummary(r model.Revision) RevisionSummary {
	var editor *AuthorResponse
	if r.EditorID > 0 {
		editor = &AuthorResponse{ID: r.EditorID, Name: r.EditorName}
	}
	return RevisionSummary{ID: r.ID, Title: r.Title, Editor: editor, Characters: r.Characters, CreatedAt: r.CreatedAt}
}

func NewRevisionResponse(r model.Revision) RevisionResponse {
	var category *int64
	if r.CategoryID > 0 {
		category = &r.CategoryID
	}
	tags := r.Tags
	if tags == nil {
		tags = []string{}
	}
	return RevisionResponse{
		RevisionSummary: NewRevisionSummary(r),
		Content:         r.Content,
		CategoryID:      category,
		Tags:            tags,
		CoverImage:      optional(r.CoverImage),
	}
}
