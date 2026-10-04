package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"warta/internal/model"
	"warta/internal/pagination"
)

const (
	SortNewest  = "newest"
	SortOldest  = "oldest"
	SortTitle   = "title"
	SortUpdated = "updated"
	SortPopular = "popular"
	// SortRelevance hanya berarti bila ada kata kunci; tanpa kata kunci sama
	// dengan newest.
	SortRelevance = "relevance"
)

const (
	likeCountSQL    = "(SELECT COUNT(*) FROM article_likes al WHERE al.article_id = a.id)"
	commentCountSQL = "(SELECT COUNT(*) FROM comments cm WHERE cm.article_id = a.id AND cm.hidden_at IS NULL)"
)

var articleOrder = map[string]string{
	SortNewest:  "COALESCE(a.published_at, a.created_at) DESC, a.id DESC",
	SortOldest:  "COALESCE(a.published_at, a.created_at) ASC, a.id ASC",
	SortTitle:   "a.title ASC, a.id ASC",
	SortUpdated: "a.updated_at DESC, a.id DESC",
	// Populer: satu suka setara lima kali dibaca, satu komentar tiga kali.
	SortPopular: "(a.view_count + 5 * " + likeCountSQL + " + 3 * " + commentCountSQL + ") DESC, a.id DESC",
}

func ValidSort(sort string) bool {
	_, ok := articleOrder[sort]
	return ok || sort == SortRelevance
}

// maxRevisions adalah jumlah revisi yang disimpan per artikel. Yang lebih
// lama dihapus.
const maxRevisions = 50

type ArticleFilter struct {
	Query        string
	CategorySlug string
	TagSlug      string
	AuthorID     int64
	// BookmarkedBy membatasi pada artikel yang disimpan user ini.
	BookmarkedBy int64
	// FollowedBy membatasi pada artikel dari penulis yang diikuti user ini.
	FollowedBy int64
	// Status kosong berarti semua status.
	Status model.ArticleStatus
	Sort   string
}

type ArticleRepository interface {
	// Create menyimpan article beserta tag-nya dalam satu transaksi. Tag yang
	// belum ada dibuat otomatis. editorID dicatat di revisi pertama.
	Create(ctx context.Context, a *model.Article, tags []string, editorID int64) error
	// Update mengganti tag hanya bila tags tidak nil. Revisi baru dicatat bila
	// judul, isi, kategori, tag, atau sampul berubah.
	Update(ctx context.Context, a *model.Article, tags *[]string, editorID int64) error
	Delete(ctx context.Context, id int64) error
	FindByID(ctx context.Context, id int64) (model.Article, error)
	FindBySlug(ctx context.Context, slug string) (model.Article, error)
	List(ctx context.Context, f ArticleFilter, p pagination.Params) ([]model.Article, int64, error)
	SlugsWithPrefix(ctx context.Context, base string, excludeID int64) ([]string, error)

	// PublishDue menerbitkan artikel terjadwal yang waktunya sudah tiba.
	// Aman dijalankan bersamaan oleh beberapa instance.
	PublishDue(ctx context.Context, now time.Time) (int64, error)

	// ListRevisions mengembalikan revisi terbaru dulu, tanpa isi lengkap.
	ListRevisions(ctx context.Context, articleID int64) ([]model.Revision, error)
	FindRevision(ctx context.Context, articleID, revisionID int64) (model.Revision, error)
}

type articleRepository struct {
	db *sql.DB
}

func NewArticleRepository(db *sql.DB) ArticleRepository {
	return &articleRepository{db: db}
}

// excerptSource cukup panjang untuk cuplikan tanpa membaca seluruh isi article
// di halaman daftar.
const (
	excerptSource = 600
	excerptLength = 200
	// snippetSource adalah potongan Markdown di sekitar kata yang dicari,
	// diringkas menjadi snippetLength karakter teks biasa.
	snippetSource = 600
	snippetLength = 180
)

const articleFrom = `
FROM articles a
JOIN users u ON u.id = a.author_id
JOIN categories c ON c.id = a.category_id`

// articleSelect memilih kolom article. extra adalah kolom tambahan di akhir,
// yang dibaca lewat extraDest di scanArticle.
func articleSelect(contentColumn string, extra ...string) string {
	columns := ""
	for _, e := range extra {
		columns += ", " + e
	}
	return `
SELECT a.id, a.title, a.slug, ` + contentColumn + `, CHAR_LENGTH(a.content), a.cover_image, a.status,
       a.author_id, u.name, u.avatar_url, a.category_id, c.name, c.slug,
       ` + commentCountSQL + `, ` + likeCountSQL + `, a.view_count,
       a.published_at, a.scheduled_at, a.created_at, a.updated_at` + columns + articleFrom
}

func scanArticle(row interface{ Scan(...any) error }, a *model.Article, content *string, extraDest ...any) error {
	var publishedAt, scheduledAt sql.NullTime
	var cover, avatar sql.NullString
	dest := append([]any{
		&a.ID, &a.Title, &a.Slug, content, &a.ContentLength, &cover, &a.Status,
		&a.AuthorID, &a.AuthorName, &avatar, &a.CategoryID, &a.CategoryName, &a.CategorySlug,
		&a.CommentCount, &a.LikeCount, &a.ViewCount, &publishedAt, &scheduledAt, &a.CreatedAt, &a.UpdatedAt,
	}, extraDest...)
	err := row.Scan(dest...)
	a.PublishedAt = nullTimePtr(publishedAt)
	a.ScheduledAt = nullTimePtr(scheduledAt)
	a.CoverImage = cover.String
	a.AuthorAvatar = avatar.String
	return err
}

func (r *articleRepository) Create(ctx context.Context, a *model.Article, tags []string, editorID int64) error {
	return withTx(ctx, r.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO articles (author_id, category_id, title, slug, content, cover_image, status, published_at, scheduled_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.AuthorID, a.CategoryID, a.Title, a.Slug, a.Content, nullString(a.CoverImage), a.Status, a.PublishedAt, a.ScheduledAt)
		if err != nil {
			return mapError(err)
		}

		a.ID, err = result.LastInsertId()
		if err != nil {
			return err
		}

		if err := replaceTags(ctx, tx, a.ID, tags); err != nil {
			return err
		}
		return recordRevision(ctx, tx, a.ID, editorID)
	})
}

func (r *articleRepository) Update(ctx context.Context, a *model.Article, tags *[]string, editorID int64) error {
	return withTx(ctx, r.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE articles
			SET category_id = ?, title = ?, slug = ?, content = ?, cover_image = ?, status = ?,
			    published_at = ?, scheduled_at = ?
			WHERE id = ?`,
			a.CategoryID, a.Title, a.Slug, a.Content, nullString(a.CoverImage), a.Status,
			a.PublishedAt, a.ScheduledAt, a.ID)
		if err != nil {
			return mapError(err)
		}

		if tags != nil {
			if err := replaceTags(ctx, tx, a.ID, *tags); err != nil {
				return err
			}
		}
		return recordRevision(ctx, tx, a.ID, editorID)
	})
}

// recordRevision menyalin keadaan article yang baru disimpan ke riwayat,
// kecuali isinya sama dengan revisi terakhir (misalnya hanya status yang
// berubah). Revisi melebihi maxRevisions dihapus dari yang tertua.
func recordRevision(ctx context.Context, tx *sql.Tx, articleID, editorID int64) error {
	var current model.Revision
	var cover sql.NullString
	err := tx.QueryRowContext(ctx,
		"SELECT title, content, category_id, cover_image FROM articles WHERE id = ?", articleID,
	).Scan(&current.Title, &current.Content, &current.CategoryID, &cover)
	if err != nil {
		return err
	}
	current.CoverImage = cover.String
	if current.Tags, err = tagNames(ctx, tx, articleID); err != nil {
		return err
	}

	var last model.Revision
	var lastCategory sql.NullInt64
	var lastCover sql.NullString
	var lastTags []byte
	err = tx.QueryRowContext(ctx, `
		SELECT title, content, category_id, tags, cover_image FROM article_revisions
		WHERE article_id = ? ORDER BY id DESC LIMIT 1`, articleID,
	).Scan(&last.Title, &last.Content, &lastCategory, &lastTags, &lastCover)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	default:
		last.CategoryID = lastCategory.Int64
		last.CoverImage = lastCover.String
		if err := json.Unmarshal(lastTags, &last.Tags); err != nil {
			return err
		}
		if sameContent(current, last) {
			return nil
		}
	}

	tags, err := json.Marshal(current.Tags)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO article_revisions (article_id, editor_id, title, content, category_id, tags, cover_image)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		articleID, nullID(editorID), current.Title, current.Content, current.CategoryID, tags, nullString(current.CoverImage),
	); err != nil {
		return mapError(err)
	}

	// Revisi ke-(maxRevisions+1) dan yang lebih tua dihapus. Bila revisinya
	// belum sebanyak itu, subquery bernilai NULL dan tidak ada yang terhapus.
	_, err = tx.ExecContext(ctx, `
		DELETE FROM article_revisions
		WHERE article_id = ? AND id <= (
			SELECT id FROM (
				SELECT id FROM article_revisions WHERE article_id = ?
				ORDER BY id DESC LIMIT 1 OFFSET ?
			) oldest
		)`, articleID, articleID, maxRevisions)
	return err
}

func sameContent(a, b model.Revision) bool {
	sortedA, sortedB := slices.Clone(a.Tags), slices.Clone(b.Tags)
	slices.Sort(sortedA)
	slices.Sort(sortedB)
	return a.Title == b.Title && a.Content == b.Content && a.CategoryID == b.CategoryID &&
		a.CoverImage == b.CoverImage && slices.Equal(sortedA, sortedB)
}

func tagNames(ctx context.Context, tx *sql.Tx, articleID int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT t.name FROM article_tags at JOIN tags t ON t.id = at.tag_id
		WHERE at.article_id = ? ORDER BY t.name`, articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func nullID(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

func replaceTags(ctx context.Context, tx *sql.Tx, articleID int64, names []string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM article_tags WHERE article_id = ?", articleID); err != nil {
		return err
	}

	ids, err := ensureTags(ctx, tx, names)
	if err != nil || len(ids) == 0 {
		return err
	}

	args := make([]any, 0, len(ids)*2)
	for _, id := range ids {
		args = append(args, articleID, id)
	}

	values := "(?, ?)"
	for i := 1; i < len(ids); i++ {
		values += ", (?, ?)"
	}

	_, err = tx.ExecContext(ctx, "INSERT INTO article_tags (article_id, tag_id) VALUES "+values, args...)
	return mapError(err)
}

func (r *articleRepository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, "DELETE FROM articles WHERE id = ?", id)
	if err != nil {
		return mapError(err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *articleRepository) FindByID(ctx context.Context, id int64) (model.Article, error) {
	return r.findOne(ctx, "a.id = ?", id)
}

func (r *articleRepository) FindBySlug(ctx context.Context, slug string) (model.Article, error) {
	return r.findOne(ctx, "a.slug = ?", slug)
}

func (r *articleRepository) findOne(ctx context.Context, condition string, arg any) (model.Article, error) {
	var a model.Article
	err := scanArticle(r.db.QueryRowContext(ctx, articleSelect("a.content")+" WHERE "+condition, arg), &a, &a.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Article{}, ErrNotFound
	}
	if err != nil {
		return model.Article{}, err
	}

	a.Excerpt = model.Excerpt(a.Content, excerptLength)

	articles := []*model.Article{&a}
	if err := r.loadTags(ctx, articles); err != nil {
		return model.Article{}, err
	}
	return a, nil
}

func (r *articleRepository) List(ctx context.Context, f ArticleFilter, p pagination.Params) ([]model.Article, int64, error) {
	var conditions []string
	var args []any

	if f.Status != "" {
		conditions = append(conditions, "a.status = ?")
		args = append(args, f.Status)
	}
	if f.AuthorID > 0 {
		conditions = append(conditions, "a.author_id = ?")
		args = append(args, f.AuthorID)
	}
	if f.BookmarkedBy > 0 {
		conditions = append(conditions, "EXISTS (SELECT 1 FROM bookmarks b WHERE b.article_id = a.id AND b.user_id = ?)")
		args = append(args, f.BookmarkedBy)
	}
	if f.FollowedBy > 0 {
		conditions = append(conditions, "EXISTS (SELECT 1 FROM follows fw WHERE fw.author_id = a.author_id AND fw.follower_id = ?)")
		args = append(args, f.FollowedBy)
	}
	if f.CategorySlug != "" {
		conditions = append(conditions, "c.slug = ?")
		args = append(args, f.CategorySlug)
	}
	if f.TagSlug != "" {
		conditions = append(conditions, `EXISTS (
			SELECT 1 FROM article_tags at JOIN tags t ON t.id = at.tag_id
			WHERE at.article_id = a.id AND t.slug = ?)`)
		args = append(args, f.TagSlug)
	}
	search := newSearch(f.Query)
	if f.Query != "" && search.empty() {
		// Kata kunci yang hanya berisi tanda baca tidak cocok dengan apa pun.
		return nil, 0, nil
	}
	searchConditions, searchArgs := search.conditions()
	conditions = append(conditions, searchConditions...)
	args = append(args, searchArgs...)
	where := whereClause(conditions)

	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*)"+articleFrom+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	order, ok := articleOrder[f.Sort]
	if !ok {
		order = articleOrder[SortNewest]
	}
	var orderArgs []any
	if score, scoreArgs := search.score(); f.Sort == SortRelevance && score != "" {
		order = score + " DESC, " + articleOrder[SortNewest]
		orderArgs = scoreArgs
	}

	// Hasil pencarian membawa potongan isi di sekitar kata yang dicari.
	selectArgs := []any{excerptSource}
	var extra []string
	term := search.snippetTerm()
	if term != "" {
		extra = append(extra, "SUBSTRING(a.content, GREATEST(1, LOCATE(?, a.content) - ?), ?)")
		selectArgs = append(selectArgs, term, snippetSource/2, snippetSource)
	}

	queryArgs := append(append(append(selectArgs, args...), orderArgs...), p.Limit(), p.Offset())
	rows, err := r.db.QueryContext(ctx,
		articleSelect("LEFT(a.content, ?)", extra...)+where+" ORDER BY "+order+" LIMIT ? OFFSET ?",
		queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var articles []model.Article
	for rows.Next() {
		var a model.Article
		var head, fragment string
		var extraDest []any
		if term != "" {
			extraDest = append(extraDest, &fragment)
		}
		if err := scanArticle(rows, &a, &head, extraDest...); err != nil {
			return nil, 0, err
		}
		a.Excerpt = model.Excerpt(head, excerptLength)
		if term != "" {
			a.Snippet = model.Snippet(fragment, term, snippetLength)
		}
		articles = append(articles, a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	pointers := make([]*model.Article, len(articles))
	for i := range articles {
		pointers[i] = &articles[i]
	}
	if err := r.loadTags(ctx, pointers); err != nil {
		return nil, 0, err
	}

	return articles, total, nil
}

// loadTags mengisi tag semua article dengan satu query.
func (r *articleRepository) loadTags(ctx context.Context, articles []*model.Article) error {
	if len(articles) == 0 {
		return nil
	}

	byID := make(map[int64]*model.Article, len(articles))
	args := make([]any, 0, len(articles))
	for _, a := range articles {
		a.Tags = []model.Tag{}
		byID[a.ID] = a
		args = append(args, a.ID)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT at.article_id, t.id, t.name, t.slug, t.created_at
		FROM article_tags at JOIN tags t ON t.id = at.tag_id
		WHERE at.article_id IN (`+placeholders(len(args))+`)
		ORDER BY t.name ASC`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var articleID int64
		var t model.Tag
		if err := rows.Scan(&articleID, &t.ID, &t.Name, &t.Slug, &t.CreatedAt); err != nil {
			return err
		}
		if a := byID[articleID]; a != nil {
			a.Tags = append(a.Tags, t)
		}
	}
	return rows.Err()
}

func (r *articleRepository) SlugsWithPrefix(ctx context.Context, base string, excludeID int64) ([]string, error) {
	return slugsWithPrefix(ctx, r.db, "articles", base, excludeID)
}

func (r *articleRepository) PublishDue(ctx context.Context, now time.Time) (int64, error) {
	// published_at diisi waktu terjadwal, bukan waktu job berjalan. Artikel
	// yang pernah terbit lalu dijadwalkan ulang tetap memakai tanggal terbit
	// pertamanya.
	result, err := r.db.ExecContext(ctx, `
		UPDATE articles
		SET status = 'published', published_at = COALESCE(published_at, scheduled_at), scheduled_at = NULL
		WHERE status = 'scheduled' AND scheduled_at <= ?`, now.UTC())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

const revisionSelect = `
SELECT rv.id, rv.article_id, rv.editor_id, COALESCE(u.name, ''), rv.title, %s, rv.category_id, rv.tags,
       rv.cover_image, rv.created_at
FROM article_revisions rv
LEFT JOIN users u ON u.id = rv.editor_id`

func scanRevision(row interface{ Scan(...any) error }, rv *model.Revision, content any) error {
	var editor, category sql.NullInt64
	var cover sql.NullString
	var tags []byte
	if err := row.Scan(&rv.ID, &rv.ArticleID, &editor, &rv.EditorName, &rv.Title, content, &category,
		&tags, &cover, &rv.CreatedAt); err != nil {
		return err
	}
	rv.EditorID = editor.Int64
	rv.CategoryID = category.Int64
	rv.CoverImage = cover.String
	return json.Unmarshal(tags, &rv.Tags)
}

func (r *articleRepository) ListRevisions(ctx context.Context, articleID int64) ([]model.Revision, error) {
	rows, err := r.db.QueryContext(ctx,
		fmt.Sprintf(revisionSelect, "CHAR_LENGTH(rv.content)")+" WHERE rv.article_id = ? ORDER BY rv.id DESC",
		articleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var revisions []model.Revision
	for rows.Next() {
		var rv model.Revision
		if err := scanRevision(rows, &rv, &rv.Characters); err != nil {
			return nil, err
		}
		revisions = append(revisions, rv)
	}
	return revisions, rows.Err()
}

func (r *articleRepository) FindRevision(ctx context.Context, articleID, revisionID int64) (model.Revision, error) {
	var rv model.Revision
	err := scanRevision(r.db.QueryRowContext(ctx,
		fmt.Sprintf(revisionSelect, "rv.content")+" WHERE rv.article_id = ? AND rv.id = ?",
		articleID, revisionID), &rv, &rv.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Revision{}, ErrNotFound
	}
	rv.Characters = len([]rune(rv.Content))
	return rv, err
}
