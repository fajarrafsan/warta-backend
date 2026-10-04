package service

import (
	"context"
	"unicode/utf8"

	"warta/internal/apperr"
	"warta/internal/dto"
	"warta/internal/repository"
)

const (
	minSuggestRunes = 2
	suggestLimit    = 5
)

type SearchService interface {
	// Suggest memberi saran artikel, kategori, tag, dan penulis saat
	// mengetik. Kata kunci kurang dari dua huruf menghasilkan saran kosong.
	Suggest(ctx context.Context, query string) (dto.Suggestions, error)
}

type searchService struct {
	search repository.SearchRepository
}

func NewSearchService(search repository.SearchRepository) SearchService {
	return &searchService{search: search}
}

func (s *searchService) Suggest(ctx context.Context, query string) (dto.Suggestions, error) {
	var found repository.Suggestions
	if utf8.RuneCountInString(query) >= minSuggestRunes {
		var err error
		if found, err = s.search.Suggest(ctx, query, suggestLimit); err != nil {
			return dto.Suggestions{}, apperr.Internal(err)
		}
	}

	out := dto.Suggestions{
		Articles:   make([]dto.ArticleSuggestion, 0, len(found.Articles)),
		Categories: make([]dto.CategoryRef, 0, len(found.Categories)),
		Tags:       make([]dto.TagRef, 0, len(found.Tags)),
		Authors:    make([]dto.AuthorSuggestion, 0, len(found.Authors)),
	}
	for _, a := range found.Articles {
		out.Articles = append(out.Articles, dto.ArticleSuggestion(a))
	}
	for _, c := range found.Categories {
		out.Categories = append(out.Categories, dto.CategoryRef{ID: c.ID, Name: c.Name, Slug: c.Slug})
	}
	for _, t := range found.Tags {
		out.Tags = append(out.Tags, dto.TagRef{ID: t.ID, Name: t.Name, Slug: t.Slug})
	}
	for _, a := range found.Authors {
		profile := dto.NewAuthorProfile(a)
		out.Authors = append(out.Authors, dto.AuthorSuggestion{ID: a.ID, Name: a.Name, AvatarURL: profile.AvatarURL})
	}
	return out, nil
}
