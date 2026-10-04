package service

import (
	"context"
	"errors"
	"time"

	"warta/internal/apperr"
	"warta/internal/auth"
	"warta/internal/dto"
	"warta/internal/model"
	"warta/internal/pagination"
	"warta/internal/repository"
	"warta/internal/slug"
	"warta/internal/validation"
)

// statusAll dipakai admin untuk melihat article semua status sekaligus.
const statusAll = "all"

type ArticleService interface {
	// List menampilkan article yang sudah terbit. Admin bisa memilih status lain.
	List(ctx context.Context, actor auth.Actor, q dto.ArticleQuery, p pagination.Params) ([]dto.ArticleSummary, pagination.Meta, error)
	// ListMine menampilkan article milik actor dengan status apa pun.
	ListMine(ctx context.Context, actor auth.Actor, q dto.ArticleQuery, p pagination.Params) ([]dto.ArticleSummary, pagination.Meta, error)
	// Get menerima id maupun slug.
	Get(ctx context.Context, actor auth.Actor, ref string) (dto.ArticleResponse, error)
	Create(ctx context.Context, actor auth.Actor, req dto.ArticleRequest) (dto.ArticleResponse, error)
	Update(ctx context.Context, actor auth.Actor, id int64, patch dto.ArticlePatch) (dto.ArticleResponse, error)
	Delete(ctx context.Context, actor auth.Actor, id int64) error

	// ListBookmarks menampilkan artikel terbit yang disimpan actor.
	ListBookmarks(ctx context.Context, actor auth.Actor, q dto.ArticleQuery, p pagination.Params) ([]dto.ArticleSummary, pagination.Meta, error)
	// ListFeed menampilkan artikel terbit dari penulis yang diikuti actor.
	ListFeed(ctx context.Context, actor auth.Actor, q dto.ArticleQuery, p pagination.Params) ([]dto.ArticleSummary, pagination.Meta, error)
	SetLike(ctx context.Context, actor auth.Actor, id int64, liked bool) (dto.Engagement, error)
	SetBookmark(ctx context.Context, actor auth.Actor, id int64, bookmarked bool) (dto.Engagement, error)
	// RecordView menambah hitungan dibaca. viewer mengenali pembaca (misalnya
	// alamat IP) supaya muat ulang halaman tidak dihitung berkali-kali.
	RecordView(ctx context.Context, actor auth.Actor, id int64, viewer string) error

	// Riwayat revisi hanya untuk penulis artikel dan admin.
	ListRevisions(ctx context.Context, actor auth.Actor, id int64) ([]dto.RevisionSummary, error)
	GetRevision(ctx context.Context, actor auth.Actor, id, revisionID int64) (dto.RevisionResponse, error)
	// RestoreRevision mengembalikan judul, isi, kategori, tag, dan sampul ke
	// isi revisi itu. Statusnya tidak berubah. Pemulihan tercatat sebagai
	// revisi baru, jadi bisa dibatalkan.
	RestoreRevision(ctx context.Context, actor auth.Actor, id, revisionID int64) (dto.ArticleResponse, error)

	// PublishDue menerbitkan artikel terjadwal yang waktunya sudah tiba.
	PublishDue(ctx context.Context) (int64, error)
}

// maxScheduleAhead membatasi seberapa jauh artikel bisa dijadwalkan.
const maxScheduleAhead = 365 * 24 * time.Hour

// CoverChecker memastikan gambar sampul memang sudah diunggah.
type CoverChecker interface {
	Exists(ctx context.Context, url string) bool
}

type articleService struct {
	articles   repository.ArticleRepository
	categories repository.CategoryRepository
	engagement repository.EngagementRepository
	covers     CoverChecker
	views      ViewDeduper
	now        func() time.Time
}

func NewArticleService(
	articles repository.ArticleRepository,
	categories repository.CategoryRepository,
	engagement repository.EngagementRepository,
	covers CoverChecker,
	views ViewDeduper,
) ArticleService {
	// Tanpa penyimpanan bersama, pembaca diingat di memori instance ini.
	if views == nil {
		views = newViewDeduper(ViewWindow)
	}
	return &articleService{
		articles:   articles,
		categories: categories,
		engagement: engagement,
		covers:     covers,
		views:      views,
		now:        time.Now,
	}
}

var errArticleNotFound = apperr.NotFound("article tidak ditemukan")

// canView: article yang belum terbit hanya terlihat oleh penulisnya dan admin.
// Bagi orang lain article itu dianggap tidak ada (404), bukan 403, supaya
// keberadaan draft tidak bocor.
func canView(actor auth.Actor, a model.Article) bool {
	return a.IsPublished() || actor.IsAdmin() || (actor.Authenticated() && actor.ID == a.AuthorID)
}

func canModify(actor auth.Actor, a model.Article) bool {
	return actor.IsAdmin() || (actor.CanWrite() && actor.ID == a.AuthorID)
}

func (s *articleService) List(ctx context.Context, actor auth.Actor, q dto.ArticleQuery, p pagination.Params) ([]dto.ArticleSummary, pagination.Meta, error) {
	filter, err := articleFilter(q)
	if err != nil {
		return nil, pagination.Meta{}, err
	}

	switch {
	case q.Status == "":
		filter.Status = model.StatusPublished
	case filter.Status != model.StatusPublished && !actor.IsAdmin():
		return nil, pagination.Meta{}, apperr.Forbidden("hanya admin yang bisa melihat article yang belum terbit")
	}

	return s.list(ctx, filter, p)
}

func (s *articleService) ListMine(ctx context.Context, actor auth.Actor, q dto.ArticleQuery, p pagination.Params) ([]dto.ArticleSummary, pagination.Meta, error) {
	filter, err := articleFilter(q)
	if err != nil {
		return nil, pagination.Meta{}, err
	}
	filter.AuthorID = actor.ID

	return s.list(ctx, filter, p)
}

func (s *articleService) list(ctx context.Context, f repository.ArticleFilter, p pagination.Params) ([]dto.ArticleSummary, pagination.Meta, error) {
	articles, total, err := s.articles.List(ctx, f, p)
	if err != nil {
		return nil, pagination.Meta{}, apperr.Internal(err)
	}
	return dto.NewArticleSummaries(articles), pagination.NewMeta(p, total), nil
}

func articleFilter(q dto.ArticleQuery) (repository.ArticleFilter, error) {
	f := repository.ArticleFilter{
		Query:        q.Query,
		CategorySlug: q.Category,
		TagSlug:      q.Tag,
		AuthorID:     q.AuthorID,
		Sort:         q.Sort,
	}

	// Pencarian diurutkan menurut relevansi bila urutan tidak dipilih.
	switch {
	case f.Sort == "" && q.Query != "":
		f.Sort = repository.SortRelevance
	case f.Sort == "":
		f.Sort = repository.SortNewest
	}
	if !repository.ValidSort(f.Sort) {
		return f, apperr.BadRequest("sort harus newest, oldest, title, updated, popular, atau relevance")
	}

	switch status := model.ArticleStatus(q.Status); {
	case q.Status == "" || q.Status == statusAll:
	case status.Valid():
		f.Status = status
	default:
		return f, apperr.BadRequest("status harus draft, scheduled, published, archived, atau all")
	}

	return f, nil
}

func (s *articleService) Get(ctx context.Context, actor auth.Actor, ref string) (dto.ArticleResponse, error) {
	var article model.Article
	var err error

	if id, ok := parseID(ref); ok {
		article, err = s.articles.FindByID(ctx, id)
	} else {
		article, err = s.articles.FindBySlug(ctx, ref)
	}
	if errors.Is(err, repository.ErrNotFound) || err == nil && !canView(actor, article) {
		return dto.ArticleResponse{}, errArticleNotFound
	}
	if err != nil {
		return dto.ArticleResponse{}, apperr.Internal(err)
	}

	response := dto.NewArticleResponse(article)
	if actor.Authenticated() {
		response.Liked, response.Bookmarked, err = s.engagement.State(ctx, actor.ID, article.ID)
		if err != nil {
			return dto.ArticleResponse{}, apperr.Internal(err)
		}
	}
	return response, nil
}

func (s *articleService) Create(ctx context.Context, actor auth.Actor, req dto.ArticleRequest) (dto.ArticleResponse, error) {
	if !actor.CanWrite() {
		return dto.ArticleResponse{}, apperr.Forbidden("hanya author dan admin yang bisa menulis article")
	}

	req.Normalize()
	if err := s.validate(ctx, req, 0, ""); err != nil {
		return dto.ArticleResponse{}, err
	}
	scheduledAt, err := s.schedule(req, nil)
	if err != nil {
		return dto.ArticleResponse{}, err
	}

	article := model.Article{
		AuthorID:    actor.ID,
		CategoryID:  req.CategoryID,
		Title:       req.Title,
		Content:     req.Content,
		CoverImage:  req.CoverImage,
		Status:      model.ArticleStatus(req.Status),
		ScheduledAt: scheduledAt,
	}

	if article.Slug, err = s.uniqueSlug(ctx, article.Title, 0); err != nil {
		return dto.ArticleResponse{}, apperr.Internal(err)
	}
	if article.IsPublished() {
		now := s.now()
		article.PublishedAt = &now
	}

	if err := s.articles.Create(ctx, &article, req.Tags, actor.ID); err != nil {
		return dto.ArticleResponse{}, writeError(err)
	}

	return s.Get(ctx, actor, idRef(article.ID))
}

func (s *articleService) Update(ctx context.Context, actor auth.Actor, id int64, patch dto.ArticlePatch) (dto.ArticleResponse, error) {
	article, err := s.editable(ctx, actor, id)
	if err != nil {
		return dto.ArticleResponse{}, err
	}

	req := patch.Apply(dto.ArticleRequest{
		Title:       article.Title,
		Content:     article.Content,
		CategoryID:  article.CategoryID,
		Tags:        article.TagNames(),
		Status:      string(article.Status),
		CoverImage:  article.CoverImage,
		ScheduledAt: article.ScheduledAt,
	})
	req.Normalize()

	if err := s.validate(ctx, req, article.CategoryID, article.CoverImage); err != nil {
		return dto.ArticleResponse{}, err
	}
	scheduledAt, err := s.schedule(req, &article)
	if err != nil {
		return dto.ArticleResponse{}, err
	}

	// Slug ikut berubah bersama judul hanya selama article belum pernah
	// terbit. Setelah terbit, tautannya mungkin sudah tersebar.
	if req.Title != article.Title && article.PublishedAt == nil {
		if article.Slug, err = s.uniqueSlug(ctx, req.Title, article.ID); err != nil {
			return dto.ArticleResponse{}, apperr.Internal(err)
		}
	}

	article.Title = req.Title
	article.Content = req.Content
	article.CategoryID = req.CategoryID
	article.CoverImage = req.CoverImage
	article.Status = model.ArticleStatus(req.Status)
	article.ScheduledAt = scheduledAt
	if article.IsPublished() && article.PublishedAt == nil {
		now := s.now()
		article.PublishedAt = &now
	}

	var tags *[]string
	if patch.Tags != nil {
		tags = &req.Tags
	}

	if err := s.articles.Update(ctx, &article, tags, actor.ID); err != nil {
		return dto.ArticleResponse{}, writeError(err)
	}

	return s.Get(ctx, actor, idRef(article.ID))
}

func (s *articleService) Delete(ctx context.Context, actor auth.Actor, id int64) error {
	if _, err := s.editable(ctx, actor, id); err != nil {
		return err
	}

	err := s.articles.Delete(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return errArticleNotFound
	}
	if err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// editable memuat article yang akan diubah atau dihapus oleh actor.
func (s *articleService) editable(ctx context.Context, actor auth.Actor, id int64) (model.Article, error) {
	article, err := s.articles.FindByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) || err == nil && !canView(actor, article) {
		return model.Article{}, errArticleNotFound
	}
	if err != nil {
		return model.Article{}, apperr.Internal(err)
	}

	if !canModify(actor, article) {
		return model.Article{}, apperr.Forbidden("hanya penulis article dan admin yang bisa mengubahnya")
	}
	return article, nil
}

// validate memeriksa aturan field lalu keberadaan kategori dan sampul.
// knownCategory dan knownCover adalah nilai yang sudah tersimpan sehingga
// tidak perlu dicek ulang.
func (s *articleService) validate(ctx context.Context, req dto.ArticleRequest, knownCategory int64, knownCover string) error {
	if problems := validation.ValidateArticle(req); len(problems) > 0 {
		return apperr.Validation(problems)
	}
	if req.CoverImage != "" && req.CoverImage != knownCover && !s.covers.Exists(ctx, req.CoverImage) {
		return apperr.Validation(map[string]string{"cover_image": "gambar sampul tidak ditemukan, unggah ulang"})
	}
	if req.CategoryID == knownCategory {
		return nil
	}

	_, err := s.categories.FindByID(ctx, req.CategoryID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperr.Validation(map[string]string{"category_id": "kategori tidak ditemukan"})
	}
	if err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// schedule memeriksa waktu terbit artikel terjadwal dan mengembalikan nilai
// yang disimpan: nil untuk status selain scheduled. current adalah keadaan
// tersimpan saat mengubah artikel; waktu yang tidak berubah tidak diperiksa
// ulang, supaya artikel yang hampir terbit tetap bisa disunting.
func (s *articleService) schedule(req dto.ArticleRequest, current *model.Article) (*time.Time, error) {
	if req.Status != string(model.StatusScheduled) || req.ScheduledAt == nil {
		return nil, nil
	}

	at := req.ScheduledAt.UTC().Truncate(time.Second)
	unchanged := current != nil && current.Status == model.StatusScheduled &&
		current.ScheduledAt != nil && current.ScheduledAt.Equal(at)
	if unchanged {
		return &at, nil
	}

	now := s.now()
	switch {
	case !at.After(now):
		return nil, apperr.Validation(map[string]string{"scheduled_at": "scheduled_at harus di masa depan"})
	case at.After(now.Add(maxScheduleAhead)):
		return nil, apperr.Validation(map[string]string{"scheduled_at": "scheduled_at paling jauh satu tahun dari sekarang"})
	}
	return &at, nil
}

func (s *articleService) uniqueSlug(ctx context.Context, title string, excludeID int64) (string, error) {
	base := slug.Make(title, "artikel", 200)
	taken, err := s.articles.SlugsWithPrefix(ctx, base, excludeID)
	if err != nil {
		return "", err
	}
	return slug.Unique(base, taken), nil
}

func writeError(err error) error {
	switch {
	case errors.Is(err, repository.ErrMissingReference):
		return apperr.Validation(map[string]string{"category_id": "kategori tidak ditemukan"})
	case errors.Is(err, repository.ErrDuplicate):
		return apperr.Conflict("article atau tag yang sama sedang disimpan bersamaan, coba lagi")
	}
	return apperr.Internal(err)
}

func (s *articleService) ListBookmarks(ctx context.Context, actor auth.Actor, q dto.ArticleQuery, p pagination.Params) ([]dto.ArticleSummary, pagination.Meta, error) {
	filter, err := articleFilter(q)
	if err != nil {
		return nil, pagination.Meta{}, err
	}
	filter.BookmarkedBy = actor.ID
	filter.Status = model.StatusPublished

	return s.list(ctx, filter, p)
}

func (s *articleService) ListFeed(ctx context.Context, actor auth.Actor, q dto.ArticleQuery, p pagination.Params) ([]dto.ArticleSummary, pagination.Meta, error) {
	filter, err := articleFilter(q)
	if err != nil {
		return nil, pagination.Meta{}, err
	}
	filter.FollowedBy = actor.ID
	filter.Status = model.StatusPublished

	return s.list(ctx, filter, p)
}

func (s *articleService) SetLike(ctx context.Context, actor auth.Actor, id int64, liked bool) (dto.Engagement, error) {
	return s.engage(ctx, actor, id, func() error {
		return s.engagement.SetLike(ctx, actor.ID, id, liked)
	})
}

func (s *articleService) SetBookmark(ctx context.Context, actor auth.Actor, id int64, bookmarked bool) (dto.Engagement, error) {
	return s.engage(ctx, actor, id, func() error {
		return s.engagement.SetBookmark(ctx, actor.ID, id, bookmarked)
	})
}

// engage menjalankan perubahan suka atau bookmark pada artikel terbit, lalu
// mengembalikan keadaan terbarunya.
func (s *articleService) engage(ctx context.Context, actor auth.Actor, id int64, change func() error) (dto.Engagement, error) {
	if _, err := s.published(ctx, actor, id); err != nil {
		return dto.Engagement{}, err
	}
	if err := change(); err != nil {
		if errors.Is(err, repository.ErrMissingReference) {
			return dto.Engagement{}, errArticleNotFound
		}
		return dto.Engagement{}, apperr.Internal(err)
	}

	article, err := s.articles.FindByID(ctx, id)
	if err != nil {
		return dto.Engagement{}, notFoundOr(err, "article tidak ditemukan")
	}
	liked, bookmarked, err := s.engagement.State(ctx, actor.ID, id)
	if err != nil {
		return dto.Engagement{}, apperr.Internal(err)
	}
	return dto.Engagement{Liked: liked, Bookmarked: bookmarked, LikeCount: article.LikeCount}, nil
}

func (s *articleService) RecordView(ctx context.Context, actor auth.Actor, id int64, viewer string) error {
	article, err := s.published(ctx, actor, id)
	if err != nil {
		return err
	}
	// Penulis yang membuka artikelnya sendiri tidak dihitung.
	if actor.ID == article.AuthorID || !s.views.First(ctx, viewer, id) {
		return nil
	}

	if err := s.engagement.RecordView(ctx, id, s.now().UTC()); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// published memuat artikel yang boleh disukai, disimpan, dan dihitung
// pembacanya: hanya yang sudah terbit.
func (s *articleService) published(ctx context.Context, actor auth.Actor, id int64) (model.Article, error) {
	article, err := s.articles.FindByID(ctx, id)
	if errors.Is(err, repository.ErrNotFound) || err == nil && !canView(actor, article) {
		return model.Article{}, errArticleNotFound
	}
	if err != nil {
		return model.Article{}, apperr.Internal(err)
	}
	if !article.IsPublished() {
		return model.Article{}, apperr.Forbidden("hanya artikel yang sudah terbit yang bisa disukai, disimpan, dan dihitung pembacanya")
	}
	return article, nil
}

func (s *articleService) ListRevisions(ctx context.Context, actor auth.Actor, id int64) ([]dto.RevisionSummary, error) {
	if _, err := s.editable(ctx, actor, id); err != nil {
		return nil, err
	}
	revisions, err := s.articles.ListRevisions(ctx, id)
	if err != nil {
		return nil, apperr.Internal(err)
	}

	out := make([]dto.RevisionSummary, 0, len(revisions))
	for _, r := range revisions {
		out = append(out, dto.NewRevisionSummary(r))
	}
	return out, nil
}

func (s *articleService) GetRevision(ctx context.Context, actor auth.Actor, id, revisionID int64) (dto.RevisionResponse, error) {
	revision, err := s.revision(ctx, actor, id, revisionID)
	if err != nil {
		return dto.RevisionResponse{}, err
	}
	return dto.NewRevisionResponse(revision), nil
}

func (s *articleService) RestoreRevision(ctx context.Context, actor auth.Actor, id, revisionID int64) (dto.ArticleResponse, error) {
	revision, err := s.revision(ctx, actor, id, revisionID)
	if err != nil {
		return dto.ArticleResponse{}, err
	}

	patch := dto.ArticlePatch{
		Title:      &revision.Title,
		Content:    &revision.Content,
		Tags:       &revision.Tags,
		CoverImage: &revision.CoverImage,
	}
	// Kategori yang sudah dihapus tidak bisa dipulihkan; kategori sekarang
	// dipertahankan.
	if revision.CategoryID > 0 {
		patch.CategoryID = &revision.CategoryID
	}
	return s.Update(ctx, actor, id, patch)
}

func (s *articleService) revision(ctx context.Context, actor auth.Actor, id, revisionID int64) (model.Revision, error) {
	if _, err := s.editable(ctx, actor, id); err != nil {
		return model.Revision{}, err
	}
	revision, err := s.articles.FindRevision(ctx, id, revisionID)
	if err != nil {
		return model.Revision{}, notFoundOr(err, "revisi tidak ditemukan")
	}
	return revision, nil
}

func (s *articleService) PublishDue(ctx context.Context) (int64, error) {
	return s.articles.PublishDue(ctx, s.now())
}
