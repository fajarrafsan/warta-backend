package service

import (
	"context"
	"errors"

	"warta/internal/apperr"
	"warta/internal/auth"
	"warta/internal/dto"
	"warta/internal/model"
	"warta/internal/pagination"
	"warta/internal/repository"
)

// AuthorService mengurus profil publik penulis dan fitur ikuti.
type AuthorService interface {
	// Profile hanya tersedia untuk penulis, admin, atau siapa pun yang pernah
	// menerbitkan artikel. Profil pembaca biasa dianggap tidak ada (404).
	Profile(ctx context.Context, actor auth.Actor, id int64) (dto.AuthorProfile, error)
	SetFollow(ctx context.Context, actor auth.Actor, id int64, follow bool) (dto.FollowState, error)
	ListFollowing(ctx context.Context, actor auth.Actor, p pagination.Params) ([]dto.AuthorProfile, pagination.Meta, error)
}

type authorService struct {
	authors repository.AuthorRepository
}

func NewAuthorService(authors repository.AuthorRepository) AuthorService {
	return &authorService{authors: authors}
}

var errAuthorNotFound = apperr.NotFound("penulis tidak ditemukan")

func (s *authorService) public(ctx context.Context, id int64) (model.AuthorProfile, error) {
	profile, err := s.authors.Profile(ctx, id)
	if errors.Is(err, repository.ErrNotFound) || err == nil && !profile.Public() {
		return model.AuthorProfile{}, errAuthorNotFound
	}
	if err != nil {
		return model.AuthorProfile{}, apperr.Internal(err)
	}
	return profile, nil
}

func (s *authorService) Profile(ctx context.Context, actor auth.Actor, id int64) (dto.AuthorProfile, error) {
	profile, err := s.public(ctx, id)
	if err != nil {
		return dto.AuthorProfile{}, err
	}

	out := dto.NewAuthorProfile(profile)
	if actor.Authenticated() && actor.ID != id {
		if out.Following, err = s.authors.IsFollowing(ctx, actor.ID, id); err != nil {
			return dto.AuthorProfile{}, apperr.Internal(err)
		}
	}
	return out, nil
}

func (s *authorService) SetFollow(ctx context.Context, actor auth.Actor, id int64, follow bool) (dto.FollowState, error) {
	if actor.ID == id {
		return dto.FollowState{}, apperr.BadRequest("tidak bisa mengikuti diri sendiri")
	}
	if _, err := s.public(ctx, id); err != nil {
		return dto.FollowState{}, err
	}

	var err error
	if follow {
		err = s.authors.Follow(ctx, actor.ID, id)
	} else {
		err = s.authors.Unfollow(ctx, actor.ID, id)
	}
	if errors.Is(err, repository.ErrMissingReference) {
		return dto.FollowState{}, errAuthorNotFound
	}
	if err != nil {
		return dto.FollowState{}, apperr.Internal(err)
	}

	profile, err := s.authors.Profile(ctx, id)
	if err != nil {
		return dto.FollowState{}, notFoundOr(err, "penulis tidak ditemukan")
	}
	return dto.FollowState{Following: follow, FollowerCount: profile.FollowerCount}, nil
}

func (s *authorService) ListFollowing(ctx context.Context, actor auth.Actor, p pagination.Params) ([]dto.AuthorProfile, pagination.Meta, error) {
	profiles, total, err := s.authors.ListFollowing(ctx, actor.ID, p)
	if err != nil {
		return nil, pagination.Meta{}, apperr.Internal(err)
	}

	out := make([]dto.AuthorProfile, 0, len(profiles))
	for _, p := range profiles {
		profile := dto.NewAuthorProfile(p)
		profile.Following = true
		out = append(out, profile)
	}
	return out, pagination.NewMeta(p, total), nil
}
