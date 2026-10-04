package repository

import (
	"context"
	"database/sql"
	"strings"

	"warta/internal/model"
)

// Suggestions adalah saran saat mengetik di kotak pencarian.
type Suggestions struct {
	Articles   []ArticleSuggestion
	Categories []model.Category
	Tags       []model.Tag
	Authors    []model.AuthorProfile
}

type ArticleSuggestion struct {
	ID       int64
	Slug     string
	Title    string
	Category string
}

type SearchRepository interface {
	Suggest(ctx context.Context, query string, limit int) (Suggestions, error)
}

type searchRepository struct {
	db *sql.DB
}

func NewSearchRepository(db *sql.DB) SearchRepository {
	return &searchRepository{db: db}
}

func (r *searchRepository) Suggest(ctx context.Context, query string, limit int) (Suggestions, error) {
	var out Suggestions
	s := newSearch(query)
	if s.empty() {
		return out, nil
	}

	var err error
	if out.Articles, err = r.articles(ctx, s, limit); err != nil {
		return out, err
	}
	if out.Categories, err = r.categories(ctx, s); err != nil {
		return out, err
	}
	if out.Tags, err = r.tags(ctx, s); err != nil {
		return out, err
	}
	out.Authors, err = r.authors(ctx, s)
	return out, err
}

// articles mencocokkan judul artikel terbit saja, supaya saran tetap relevan.
func (r *searchRepository) articles(ctx context.Context, s search, limit int) ([]ArticleSuggestion, error) {
	conditions := []string{"a.status = 'published'"}
	var args []any
	order := "a.published_at DESC"
	var orderArgs []any
	if s.fulltext != "" {
		conditions = append(conditions, "MATCH(a.title) AGAINST(? IN BOOLEAN MODE)")
		args = append(args, s.fulltext)
		order = "MATCH(a.title) AGAINST(? IN BOOLEAN MODE) DESC, " + order
		orderArgs = append(orderArgs, s.fulltext)
	}
	for _, t := range s.short {
		conditions = append(conditions, "a.title LIKE ?")
		args = append(args, "%"+escapeLike(t)+"%")
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT a.id, a.slug, a.title, c.name
		FROM articles a JOIN categories c ON c.id = a.category_id`+
		whereClause(conditions)+" ORDER BY "+order+" LIMIT ?",
		append(append(args, orderArgs...), limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ArticleSuggestion
	for rows.Next() {
		var a ArticleSuggestion
		if err := rows.Scan(&a.ID, &a.Slug, &a.Title, &a.Category); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// likeAll membuat syarat "kolom memuat setiap kata".
func likeAll(column string, terms []string) (string, []any) {
	parts := make([]string, 0, len(terms))
	args := make([]any, 0, len(terms))
	for _, t := range terms {
		parts = append(parts, column+" LIKE ?")
		args = append(args, "%"+escapeLike(t)+"%")
	}
	return strings.Join(parts, " AND "), args
}

func (r *searchRepository) categories(ctx context.Context, s search) ([]model.Category, error) {
	condition, args := likeAll("c.name", s.terms)
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.id, c.name, c.slug FROM categories c
		WHERE `+condition+` ORDER BY c.name LIMIT 3`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Category
	for rows.Next() {
		var c model.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Slug); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// tags hanya menyarankan tag yang dipakai artikel terbit, terbanyak dulu.
func (r *searchRepository) tags(ctx context.Context, s search) ([]model.Tag, error) {
	condition, args := likeAll("t.name", s.terms)
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.slug, COUNT(*) AS used
		FROM tags t
		JOIN article_tags at ON at.tag_id = t.id
		JOIN articles a ON a.id = at.article_id AND a.status = 'published'
		WHERE `+condition+`
		GROUP BY t.id, t.name, t.slug
		ORDER BY used DESC, t.name LIMIT 5`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Tag
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.ArticleCount); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// authors hanya menyarankan penulis yang profilnya publik.
func (r *searchRepository) authors(ctx context.Context, s search) ([]model.AuthorProfile, error) {
	condition, args := likeAll("u.name", s.terms)
	rows, err := r.db.QueryContext(ctx, profileSelect+`
		WHERE (`+condition+`)
		  AND (u.role IN ('admin', 'author')
		       OR EXISTS (SELECT 1 FROM articles a WHERE a.author_id = u.id AND a.status = 'published'))
		ORDER BY u.name LIMIT 3`, args...)
	if err != nil {
		return nil, err
	}
	return scanProfiles(rows)
}
