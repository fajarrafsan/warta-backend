package handler

import (
	"context"
	"net/http"
	"strconv"

	"warta/internal/auth"
	"warta/internal/dto"
	"warta/internal/pagination"
	"warta/internal/response"
	"warta/internal/service"
)

type ArticleHandler struct {
	service service.ArticleService
	pages   pagination.Parser
}

func NewArticleHandler(s service.ArticleService, pages pagination.Parser) *ArticleHandler {
	return &ArticleHandler{service: s, pages: pages}
}

type articleLister func(ctx context.Context, actor auth.Actor, q dto.ArticleQuery, p pagination.Params) ([]dto.ArticleSummary, pagination.Meta, error)

func (h *ArticleHandler) List(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, h.service.List)
}

func (h *ArticleHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, h.service.ListMine)
}

func (h *ArticleHandler) list(w http.ResponseWriter, r *http.Request, fetch articleLister) {
	page, err := h.pages.Parse(r.URL.Query())
	if err != nil {
		response.Error(w, r, err)
		return
	}

	authorID, err := queryID(r, "author")
	if err != nil {
		response.Error(w, r, err)
		return
	}

	query := dto.ArticleQuery{
		Query:    queryString(r, "q"),
		Category: queryString(r, "category"),
		Tag:      queryString(r, "tag"),
		AuthorID: authorID,
		Status:   queryString(r, "status"),
		Sort:     queryString(r, "sort"),
	}

	articles, meta, err := fetch(r.Context(), auth.ActorFrom(r.Context()), query, page)
	if err != nil {
		response.Error(w, r, err)
		return
	}

	response.List(w, articles, meta)
}

func (h *ArticleHandler) Get(w http.ResponseWriter, r *http.Request) {
	article, err := h.service.Get(r.Context(), auth.ActorFrom(r.Context()), r.PathValue("ref"))
	if err != nil {
		response.Error(w, r, err)
		return
	}

	response.Data(w, http.StatusOK, article)
}

func (h *ArticleHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.ArticleRequest
	if err := decodeJSON(r, &req); err != nil {
		response.Error(w, r, err)
		return
	}

	article, err := h.service.Create(r.Context(), auth.ActorFrom(r.Context()), req)
	if err != nil {
		response.Error(w, r, err)
		return
	}

	w.Header().Set("Location", "/api/v1/articles/"+strconv.FormatInt(article.ID, 10))
	response.Data(w, http.StatusCreated, article)
}

// Replace menangani PUT: semua field wajib dikirim.
func (h *ArticleHandler) Replace(w http.ResponseWriter, r *http.Request) {
	var req dto.ArticleRequest
	h.update(w, r, &req, func() dto.ArticlePatch { return req.AsPatch() })
}

// Patch menangani PATCH: hanya field yang dikirim yang diubah.
func (h *ArticleHandler) Patch(w http.ResponseWriter, r *http.Request) {
	var patch dto.ArticlePatch
	h.update(w, r, &patch, func() dto.ArticlePatch { return patch })
}

func (h *ArticleHandler) update(w http.ResponseWriter, r *http.Request, body any, toPatch func() dto.ArticlePatch) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}

	if err := decodeJSON(r, body); err != nil {
		response.Error(w, r, err)
		return
	}

	article, err := h.service.Update(r.Context(), auth.ActorFrom(r.Context()), id, toPatch())
	if err != nil {
		response.Error(w, r, err)
		return
	}

	response.Data(w, http.StatusOK, article)
}

func (h *ArticleHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}

	if err := h.service.Delete(r.Context(), auth.ActorFrom(r.Context()), id); err != nil {
		response.Error(w, r, err)
		return
	}

	response.NoContent(w)
}

func (h *ArticleHandler) ListFeed(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, h.service.ListFeed)
}

func (h *ArticleHandler) ListRevisions(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}

	revisions, err := h.service.ListRevisions(r.Context(), auth.ActorFrom(r.Context()), id)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.Data(w, http.StatusOK, revisions)
}

func (h *ArticleHandler) GetRevision(w http.ResponseWriter, r *http.Request) {
	h.revision(w, r, func(ctx context.Context, actor auth.Actor, id, revision int64) (any, error) {
		return h.service.GetRevision(ctx, actor, id, revision)
	})
}

func (h *ArticleHandler) RestoreRevision(w http.ResponseWriter, r *http.Request) {
	h.revision(w, r, func(ctx context.Context, actor auth.Actor, id, revision int64) (any, error) {
		return h.service.RestoreRevision(ctx, actor, id, revision)
	})
}

func (h *ArticleHandler) revision(w http.ResponseWriter, r *http.Request, run func(ctx context.Context, actor auth.Actor, id, revision int64) (any, error)) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}
	revision, err := pathID(r, "revision")
	if err != nil {
		response.Error(w, r, err)
		return
	}

	result, err := run(r.Context(), auth.ActorFrom(r.Context()), id, revision)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.Data(w, http.StatusOK, result)
}
