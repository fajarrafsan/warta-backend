package service

import (
	"context"

	"warta/internal/apperr"
	"warta/internal/model"
	"warta/internal/pagination"
	"warta/internal/repository"
)

// FeedService menyediakan data untuk sitemap dan RSS: hanya artikel terbit.
type FeedService interface {
	// Latest adalah artikel terbit terbaru, untuk RSS.
	Latest(ctx context.Context, limit int) ([]model.Article, error)
	// Sitemap adalah artikel terbit yang terakhir diubah, kategori yang
	// punya artikel, dan penulis yang punya artikel.
	Sitemap(ctx context.Context, limit int) (SitemapData, error)
}

type SitemapData struct {
	Articles   []model.Article
	Categories []model.Category
	Authors    []model.AuthorProfile
}

type feedService struct {
	articles   repository.ArticleRepository
	categories repository.CategoryRepository
	authors    repository.AuthorRepository
}

func NewFeedService(articles repository.ArticleRepository, categories repository.CategoryRepository, authors repository.AuthorRepository) FeedService {
	return &feedService{articles: articles, categories: categories, authors: authors}
}

func (s *feedService) Latest(ctx context.Context, limit int) ([]model.Article, error) {
	return s.published(ctx, repository.SortNewest, limit)
}

func (s *feedService) Sitemap(ctx context.Context, limit int) (SitemapData, error) {
	var data SitemapData
	var err error
	if data.Articles, err = s.published(ctx, repository.SortUpdated, limit); err != nil {
		return SitemapData{}, err
	}

	all, err := s.categories.List(ctx)
	if err != nil {
		return SitemapData{}, apperr.Internal(err)
	}
	for _, c := range all {
		if c.ArticleCount > 0 {
			data.Categories = append(data.Categories, c)
		}
	}

	if data.Authors, err = s.authors.ListPublic(ctx, limit); err != nil {
		return SitemapData{}, apperr.Internal(err)
	}
	return data, nil
}

func (s *feedService) published(ctx context.Context, sort string, limit int) ([]model.Article, error) {
	articles, _, err := s.articles.List(ctx,
		repository.ArticleFilter{Status: model.StatusPublished, Sort: sort},
		pagination.Params{Page: 1, PerPage: limit})
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return articles, nil
}
