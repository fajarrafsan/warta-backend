package handler

import (
	"net/http"

	"warta/internal/auth"
	"warta/internal/pagination"
	"warta/internal/response"
	"warta/internal/service"
)

type AuthorHandler struct {
	service service.AuthorService
	pages   pagination.Parser
}

func NewAuthorHandler(s service.AuthorService, pages pagination.Parser) *AuthorHandler {
	return &AuthorHandler{service: s, pages: pages}
}

func (h *AuthorHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}

	profile, err := h.service.Profile(r.Context(), auth.ActorFrom(r.Context()), id)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.Data(w, http.StatusOK, profile)
}

// SetFollow menangani PUT (ikuti) dan DELETE (berhenti mengikuti).
func (h *AuthorHandler) SetFollow(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		response.Error(w, r, err)
		return
	}

	state, err := h.service.SetFollow(r.Context(), auth.ActorFrom(r.Context()), id, r.Method == http.MethodPut)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.Data(w, http.StatusOK, state)
}

func (h *AuthorHandler) ListFollowing(w http.ResponseWriter, r *http.Request) {
	page, err := h.pages.Parse(r.URL.Query())
	if err != nil {
		response.Error(w, r, err)
		return
	}

	authors, meta, err := h.service.ListFollowing(r.Context(), auth.ActorFrom(r.Context()), page)
	if err != nil {
		response.Error(w, r, err)
		return
	}
	response.List(w, authors, meta)
}

type SearchHandler struct {
	service service.SearchService
}

func NewSearchHandler(s service.SearchService) *SearchHandler {
	return &SearchHandler{service: s}
}

func (h *SearchHandler) Suggest(w http.ResponseWriter, r *http.Request) {
	suggestions, err := h.service.Suggest(r.Context(), queryString(r, "q"))
	if err != nil {
		response.Error(w, r, err)
		return
	}
	// Saran hanya berisi data publik, boleh di-cache sebentar.
	w.Header().Set("Cache-Control", "public, max-age=60")
	response.Data(w, http.StatusOK, suggestions)
}
