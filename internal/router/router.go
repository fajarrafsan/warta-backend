package router

import (
	"net/http"

	"warta/internal/apperr"
	"warta/internal/auth"
	"warta/internal/clientip"
	"warta/internal/handler"
	"warta/internal/middleware"
	"warta/internal/model"
	"warta/internal/response"
)

type Handlers struct {
	Health     *handler.HealthHandler
	Docs       *handler.DocsHandler
	Auth       *handler.AuthHandler
	Users      *handler.UserHandler
	Categories *handler.CategoryHandler
	Tags       *handler.TagHandler
	Articles   *handler.ArticleHandler
	Comments   *handler.CommentHandler
	Stats      *handler.StatsHandler
	Uploads    *handler.UploadHandler
	Feeds      *handler.FeedHandler
	Authors    *handler.AuthorHandler
	Search     *handler.SearchHandler
}

type Options struct {
	Tokens      *auth.TokenManager
	CORSOrigins []string
	AuthLimiter middleware.Limiter
	// CommentLimiter dan UploadLimiter menghitung per akun.
	CommentLimiter middleware.Limiter
	UploadLimiter  middleware.Limiter
	ClientIP       *clientip.Resolver
	MaxBodyBytes   int64
	// MaxUploadBytes adalah batas body untuk upload gambar, sedikit di atas
	// batas berkasnya untuk memberi ruang bagi header multipart.
	MaxUploadBytes int64
}

func New(h Handlers, opt Options) http.Handler {
	mux := http.NewServeMux()

	var (
		signedIn = middleware.RequireAuth
		admin    = middleware.RequireRole(model.RoleAdmin)
		writer   = middleware.RequireRole(model.RoleAdmin, model.RoleAuthor)
		limited  = middleware.RateLimit(opt.AuthLimiter)

		commentLimited = middleware.RateLimitPerUser(opt.CommentLimiter, "terlalu banyak komentar dalam waktu singkat, tunggu sebentar")
		uploadLimited  = middleware.RateLimitPerUser(opt.UploadLimiter, "terlalu banyak upload dalam waktu singkat, tunggu sebentar")
	)

	// Setiap rute membatasi ukuran body. Rute upload memakai batasnya sendiri.
	routeWithLimit := func(limit int64, pattern string, fn http.HandlerFunc, mws ...middleware.Middleware) {
		mux.Handle(pattern, middleware.Chain(fn, append([]middleware.Middleware{middleware.MaxBody(limit)}, mws...)...))
	}
	route := func(pattern string, fn http.HandlerFunc, mws ...middleware.Middleware) {
		routeWithLimit(opt.MaxBodyBytes, pattern, fn, mws...)
	}

	route("GET /health", h.Health.Live)
	route("GET /health/live", h.Health.Live)
	route("GET /health/ready", h.Health.Ready)
	route("GET /docs", h.Docs.UI)
	route("GET /sitemap.xml", h.Feeds.Sitemap)
	route("GET /feed.xml", h.Feeds.RSS)
	route("GET /api/v1/openapi.yaml", h.Docs.Spec)

	route("POST /api/v1/auth/register", h.Auth.Register, limited)
	route("POST /api/v1/auth/login", h.Auth.Login, limited)
	route("POST /api/v1/auth/refresh", h.Auth.Refresh, limited)
	route("POST /api/v1/auth/logout", h.Auth.Logout)
	route("POST /api/v1/auth/verify-email", h.Auth.VerifyEmail, limited)
	route("POST /api/v1/auth/resend-verification", h.Auth.ResendVerification, signedIn, limited)
	route("POST /api/v1/auth/forgot-password", h.Auth.ForgotPassword, limited)
	route("POST /api/v1/auth/reset-password", h.Auth.ResetPassword, limited)

	route("GET /api/v1/me", h.Auth.Me, signedIn)
	route("PATCH /api/v1/me", h.Auth.UpdateMe, signedIn)
	route("PUT /api/v1/me/password", h.Auth.ChangePassword, signedIn)
	route("GET /api/v1/me/articles", h.Articles.ListMine, signedIn)
	route("GET /api/v1/me/bookmarks", h.Articles.ListBookmarks, signedIn)
	route("GET /api/v1/me/feed", h.Articles.ListFeed, signedIn)
	route("GET /api/v1/me/following", h.Authors.ListFollowing, signedIn)
	route("GET /api/v1/stats", h.Stats.Overview, signedIn, writer)

	routeWithLimit(opt.MaxUploadBytes, "POST /api/v1/uploads", h.Uploads.Create, signedIn, writer, uploadLimited)
	route("GET /uploads/{name}", h.Uploads.Serve)

	route("GET /api/v1/users", h.Users.List, signedIn, admin)
	route("GET /api/v1/users/{id}", h.Users.Get, signedIn, admin)
	route("PATCH /api/v1/users/{id}/role", h.Users.UpdateRole, signedIn, admin)

	route("GET /api/v1/authors/{id}", h.Authors.Get)
	route("PUT /api/v1/authors/{id}/follow", h.Authors.SetFollow, signedIn)
	route("DELETE /api/v1/authors/{id}/follow", h.Authors.SetFollow, signedIn)

	route("GET /api/v1/search/suggest", h.Search.Suggest)

	route("GET /api/v1/categories", h.Categories.List)
	route("GET /api/v1/categories/{ref}", h.Categories.Get)
	route("POST /api/v1/categories", h.Categories.Create, signedIn, admin)
	route("PUT /api/v1/categories/{id}", h.Categories.Update, signedIn, admin)
	route("DELETE /api/v1/categories/{id}", h.Categories.Delete, signedIn, admin)

	route("GET /api/v1/tags", h.Tags.List)
	route("POST /api/v1/tags", h.Tags.Create, signedIn, admin)
	route("PUT /api/v1/tags/{id}", h.Tags.Update, signedIn, admin)
	route("DELETE /api/v1/tags/{id}", h.Tags.Delete, signedIn, admin)

	route("GET /api/v1/articles", h.Articles.List)
	route("GET /api/v1/articles/{ref}", h.Articles.Get)
	route("POST /api/v1/articles", h.Articles.Create, signedIn, writer)
	route("PUT /api/v1/articles/{id}", h.Articles.Replace, signedIn)
	route("PATCH /api/v1/articles/{id}", h.Articles.Patch, signedIn)
	route("DELETE /api/v1/articles/{id}", h.Articles.Delete, signedIn)

	route("PUT /api/v1/articles/{id}/like", h.Articles.SetLike, signedIn)
	route("DELETE /api/v1/articles/{id}/like", h.Articles.SetLike, signedIn)
	route("PUT /api/v1/articles/{id}/bookmark", h.Articles.SetBookmark, signedIn)
	route("DELETE /api/v1/articles/{id}/bookmark", h.Articles.SetBookmark, signedIn)
	route("POST /api/v1/articles/{id}/view", h.Articles.RecordView)

	route("GET /api/v1/articles/{id}/revisions", h.Articles.ListRevisions, signedIn)
	route("GET /api/v1/articles/{id}/revisions/{revision}", h.Articles.GetRevision, signedIn)
	route("POST /api/v1/articles/{id}/revisions/{revision}/restore", h.Articles.RestoreRevision, signedIn)

	route("GET /api/v1/articles/{id}/comments", h.Comments.List)
	route("POST /api/v1/articles/{id}/comments", h.Comments.Create, signedIn, commentLimited)
	route("PATCH /api/v1/comments/{id}", h.Comments.Update, signedIn, commentLimited)
	route("POST /api/v1/comments/{id}/report", h.Comments.Report, signedIn, commentLimited)
	route("GET /api/v1/moderation/comments", h.Comments.ListReported, signedIn, admin)
	route("POST /api/v1/moderation/comments/{id}", h.Comments.Moderate, signedIn, admin)
	route("DELETE /api/v1/comments/{id}", h.Comments.Delete, signedIn)

	return middleware.Chain(jsonFallback(mux),
		opt.ClientIP.Middleware,
		middleware.RequestID,
		middleware.Logger,
		middleware.Recover,
		middleware.SecurityHeaders,
		middleware.CORS(opt.CORSOrigins),
		middleware.Authenticate(opt.Tokens),
	)
}

// jsonFallback membuat 404 dan 405 bawaan ServeMux ikut berbentuk JSON seperti
// error API lainnya.
func jsonFallback(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, pattern := mux.Handler(r)
		if pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		h.ServeHTTP(&fallbackWriter{ResponseWriter: w, r: r}, r)
	})
}

// fallbackWriter mengganti body teks dari http.Error dengan error JSON, dan
// mempertahankan header lain seperti Allow pada 405.
type fallbackWriter struct {
	http.ResponseWriter
	r       *http.Request
	written bool
}

func (f *fallbackWriter) WriteHeader(status int) {
	if f.written {
		return
	}
	f.written = true

	var err *apperr.Error
	switch status {
	case http.StatusNotFound:
		err = apperr.NotFound("endpoint tidak ditemukan")
	case http.StatusMethodNotAllowed:
		err = apperr.MethodNotAllowed("method tidak didukung untuk endpoint ini")
	default:
		f.ResponseWriter.WriteHeader(status)
		return
	}
	response.Error(f.ResponseWriter, f.r, err)
}

func (f *fallbackWriter) Write(b []byte) (int, error) {
	if !f.written {
		f.WriteHeader(http.StatusOK)
	}
	return len(b), nil
}
