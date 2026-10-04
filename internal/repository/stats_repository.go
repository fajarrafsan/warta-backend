package repository

import (
	"context"
	"database/sql"
	"time"

	"warta/internal/model"
)

type Totals struct {
	Articles  int64
	Published int64
	Draft     int64
	Scheduled int64
	Archived  int64
	Views     int64
	Likes     int64
	Comments  int64
	Bookmarks int64
	// Followers adalah pengikut penulis; untuk admin, semua relasi ikuti.
	Followers int64
}

type DailyActivity struct {
	Day       time.Time
	Views     int64
	Comments  int64
	Published int64
}

type TopArticle struct {
	ID       int64
	Title    string
	Slug     string
	Views    int64
	Likes    int64
	Comments int64
}

// StatsRepository menghitung ringkasan untuk dashboard. authorID nol berarti
// semua penulis.
type StatsRepository interface {
	Totals(ctx context.Context, authorID int64) (Totals, error)
	Daily(ctx context.Context, authorID int64, from time.Time) ([]DailyActivity, error)
	TopArticles(ctx context.Context, authorID int64, limit int) ([]TopArticle, error)
	UsersByRole(ctx context.Context) (map[model.Role]int64, error)
}

type statsRepository struct {
	db *sql.DB
}

func NewStatsRepository(db *sql.DB) StatsRepository {
	return &statsRepository{db: db}
}

// authorScope menghasilkan syarat "a.author_id = ?" bila authorID diisi.
// "? = 0" membuat syarat yang sama tetap sah saat authorID nol.
const authorScope = "(? = 0 OR a.author_id = ?)"

func (r *statsRepository) Totals(ctx context.Context, authorID int64) (Totals, error) {
	var t Totals
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(a.status = 'published'), 0),
		       COALESCE(SUM(a.status = 'draft'), 0),
		       COALESCE(SUM(a.status = 'scheduled'), 0),
		       COALESCE(SUM(a.status = 'archived'), 0),
		       COALESCE(SUM(a.view_count), 0),
		       (SELECT COUNT(*) FROM article_likes al JOIN articles a ON a.id = al.article_id WHERE `+authorScope+`),
		       (SELECT COUNT(*) FROM comments cm JOIN articles a ON a.id = cm.article_id WHERE cm.hidden_at IS NULL AND `+authorScope+`),
		       (SELECT COUNT(*) FROM bookmarks b JOIN articles a ON a.id = b.article_id WHERE `+authorScope+`),
		       (SELECT COUNT(*) FROM follows f WHERE (? = 0 OR f.author_id = ?))
		FROM articles a
		WHERE `+authorScope,
		authorID, authorID, authorID, authorID, authorID, authorID, authorID, authorID, authorID, authorID,
	).Scan(&t.Articles, &t.Published, &t.Draft, &t.Scheduled, &t.Archived, &t.Views, &t.Likes, &t.Comments,
		&t.Bookmarks, &t.Followers)
	return t, err
}

// Daily mengembalikan aktivitas per hari sejak from. Hari tanpa aktivitas
// tidak ikut; service yang mengisinya dengan nol.
func (r *statsRepository) Daily(ctx context.Context, authorID int64, from time.Time) ([]DailyActivity, error) {
	start := from.Format(time.DateOnly)
	rows, err := r.db.QueryContext(ctx, `
		SELECT day, SUM(views), SUM(comments), SUM(published) FROM (
			SELECT dv.day AS day, dv.views AS views, 0 AS comments, 0 AS published
			FROM article_daily_views dv JOIN articles a ON a.id = dv.article_id
			WHERE dv.day >= ? AND `+authorScope+`
			UNION ALL
			SELECT DATE(cm.created_at), 0, 1, 0
			FROM comments cm JOIN articles a ON a.id = cm.article_id
			WHERE cm.created_at >= ? AND cm.hidden_at IS NULL AND `+authorScope+`
			UNION ALL
			SELECT DATE(a.published_at), 0, 0, 1
			FROM articles a
			WHERE a.published_at >= ? AND `+authorScope+`
		) activity
		GROUP BY day
		ORDER BY day`,
		start, authorID, authorID, start, authorID, authorID, start, authorID, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var days []DailyActivity
	for rows.Next() {
		var d DailyActivity
		if err := rows.Scan(&d.Day, &d.Views, &d.Comments, &d.Published); err != nil {
			return nil, err
		}
		days = append(days, d)
	}
	return days, rows.Err()
}

func (r *statsRepository) TopArticles(ctx context.Context, authorID int64, limit int) ([]TopArticle, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.id, a.title, a.slug, a.view_count, `+likeCountSQL+`, `+commentCountSQL+`
		FROM articles a
		WHERE a.status = 'published' AND `+authorScope+`
		ORDER BY `+articleOrder[SortPopular]+`
		LIMIT ?`,
		authorID, authorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var top []TopArticle
	for rows.Next() {
		var t TopArticle
		if err := rows.Scan(&t.ID, &t.Title, &t.Slug, &t.Views, &t.Likes, &t.Comments); err != nil {
			return nil, err
		}
		top = append(top, t)
	}
	return top, rows.Err()
}

func (r *statsRepository) UsersByRole(ctx context.Context) (map[model.Role]int64, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT role, COUNT(*) FROM users GROUP BY role")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[model.Role]int64{model.RoleAdmin: 0, model.RoleAuthor: 0, model.RoleReader: 0}
	for rows.Next() {
		var role model.Role
		var n int64
		if err := rows.Scan(&role, &n); err != nil {
			return nil, err
		}
		counts[role] = n
	}
	return counts, rows.Err()
}
