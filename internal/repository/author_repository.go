package repository

import (
	"context"
	"database/sql"
	"errors"

	"warta/internal/model"
	"warta/internal/pagination"
)

// AuthorRepository mengurus profil publik penulis dan siapa mengikuti siapa.
type AuthorRepository interface {
	Profile(ctx context.Context, id int64) (model.AuthorProfile, error)
	// Follow tidak gagal bila sudah mengikuti.
	Follow(ctx context.Context, followerID, authorID int64) error
	Unfollow(ctx context.Context, followerID, authorID int64) error
	IsFollowing(ctx context.Context, followerID, authorID int64) (bool, error)
	// ListFollowing adalah penulis yang diikuti followerID, terbaru diikuti dulu.
	ListFollowing(ctx context.Context, followerID int64, p pagination.Params) ([]model.AuthorProfile, int64, error)
	// ListPublic adalah semua penulis yang punya artikel terbit, untuk sitemap.
	ListPublic(ctx context.Context, limit int) ([]model.AuthorProfile, error)
}

type authorRepository struct {
	db *sql.DB
}

func NewAuthorRepository(db *sql.DB) AuthorRepository {
	return &authorRepository{db: db}
}

// profileSelect menghitung angka profil dari artikel yang sudah terbit saja.
const profileSelect = `
SELECT u.id, u.name, u.bio, u.avatar_url, u.role, u.created_at,
       (SELECT COUNT(*) FROM articles a WHERE a.author_id = u.id AND a.status = 'published'),
       (SELECT COUNT(*) FROM follows f WHERE f.author_id = u.id),
       (SELECT COALESCE(SUM(a.view_count), 0) FROM articles a WHERE a.author_id = u.id AND a.status = 'published'),
       (SELECT COUNT(*) FROM article_likes al JOIN articles a ON a.id = al.article_id
        WHERE a.author_id = u.id AND a.status = 'published')
FROM users u`

func scanProfile(row interface{ Scan(...any) error }, p *model.AuthorProfile) error {
	var avatar sql.NullString
	err := row.Scan(&p.ID, &p.Name, &p.Bio, &avatar, &p.Role, &p.JoinedAt,
		&p.ArticleCount, &p.FollowerCount, &p.ViewCount, &p.LikeCount)
	p.AvatarURL = avatar.String
	return err
}

func (r *authorRepository) Profile(ctx context.Context, id int64) (model.AuthorProfile, error) {
	var p model.AuthorProfile
	err := scanProfile(r.db.QueryRowContext(ctx, profileSelect+" WHERE u.id = ?", id), &p)
	if errors.Is(err, sql.ErrNoRows) {
		return model.AuthorProfile{}, ErrNotFound
	}
	return p, err
}

func (r *authorRepository) Follow(ctx context.Context, followerID, authorID int64) error {
	_, err := r.db.ExecContext(ctx,
		"INSERT IGNORE INTO follows (follower_id, author_id) VALUES (?, ?)", followerID, authorID)
	return mapError(err)
}

func (r *authorRepository) Unfollow(ctx context.Context, followerID, authorID int64) error {
	_, err := r.db.ExecContext(ctx,
		"DELETE FROM follows WHERE follower_id = ? AND author_id = ?", followerID, authorID)
	return err
}

func (r *authorRepository) IsFollowing(ctx context.Context, followerID, authorID int64) (bool, error) {
	var following bool
	err := r.db.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM follows WHERE follower_id = ? AND author_id = ?)",
		followerID, authorID).Scan(&following)
	return following, err
}

func (r *authorRepository) ListFollowing(ctx context.Context, followerID int64, p pagination.Params) ([]model.AuthorProfile, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM follows WHERE follower_id = ?", followerID).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	rows, err := r.db.QueryContext(ctx, profileSelect+`
		JOIN follows fw ON fw.author_id = u.id AND fw.follower_id = ?
		ORDER BY fw.created_at DESC, u.id DESC
		LIMIT ? OFFSET ?`, followerID, p.Limit(), p.Offset())
	if err != nil {
		return nil, 0, err
	}
	profiles, err := scanProfiles(rows)
	return profiles, total, err
}

func (r *authorRepository) ListPublic(ctx context.Context, limit int) ([]model.AuthorProfile, error) {
	rows, err := r.db.QueryContext(ctx, profileSelect+`
		WHERE EXISTS (SELECT 1 FROM articles a WHERE a.author_id = u.id AND a.status = 'published')
		ORDER BY u.id
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanProfiles(rows)
}

func scanProfiles(rows *sql.Rows) ([]model.AuthorProfile, error) {
	defer rows.Close()
	var profiles []model.AuthorProfile
	for rows.Next() {
		var p model.AuthorProfile
		if err := scanProfile(rows, &p); err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}
