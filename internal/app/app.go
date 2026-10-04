package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"warta/internal/auth"
	"warta/internal/clientip"
	"warta/internal/config"
	"warta/internal/handler"
	"warta/internal/mail"
	"warta/internal/middleware"
	"warta/internal/pagination"
	"warta/internal/redisstore"
	"warta/internal/repository"
	"warta/internal/router"
	"warta/internal/service"
	"warta/internal/storage"
)

const maxBodyBytes = 1 << 20

// App merangkai repository, service, dan handler menjadi satu http.Handler.
// Dipakai cmd/api dan test end-to-end.
type App struct {
	Handler http.Handler
	Auth    service.AuthService
	// Articles dipakai job penerbit artikel terjadwal.
	Articles service.ArticleService

	refreshTokens repository.RefreshTokenRepository
	userTokens    repository.UserTokenRepository
	redis         *redis.Client
}

// Close menutup koneksi yang dibuka New selain database.
func (a *App) Close() error {
	if a.redis != nil {
		return a.redis.Close()
	}
	return nil
}

// newStore memilih tempat gambar sampul dari konfigurasi.
func newStore(ctx context.Context, cfg config.Config) (storage.Store, handler.Check, error) {
	if cfg.UploadStorage != "s3" {
		store, err := storage.NewLocal(cfg.UploadDir, cfg.MaxUploadBytes)
		return store, nil, err
	}
	store, err := storage.NewS3(ctx, storage.S3Config{
		Endpoint:  cfg.S3Endpoint,
		Region:    cfg.S3Region,
		Bucket:    cfg.S3Bucket,
		AccessKey: cfg.S3AccessKey,
		SecretKey: cfg.S3SecretKey,
		UseSSL:    cfg.S3UseSSL,
		Prefix:    cfg.S3Prefix,
	}, cfg.MaxUploadBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("object storage: %w", err)
	}
	return store, store.Ping, nil
}

// NewMailer memilih pengirim email dari konfigurasi: SMTP bila diatur, atau
// hanya log untuk development.
func NewMailer(cfg config.Config) mail.Mailer {
	if cfg.SMTPHost == "" {
		return mail.LogMailer{}
	}
	return mail.Async{
		Mailer: mail.NewSMTPMailer(mail.SMTPConfig{
			Host:     cfg.SMTPHost,
			Port:     cfg.SMTPPort,
			Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword,
			From:     cfg.MailFrom,
		}),
		Timeout: 30 * time.Second,
	}
}

func New(cfg config.Config, db *sql.DB, hasher auth.PasswordHasher, mailer mail.Mailer) (*App, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	checks := map[string]handler.Check{}
	uploads, storageCheck, err := newStore(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if storageCheck != nil {
		checks["storage"] = storageCheck
	}

	// Tanpa Redis, rate limit dan pembaca yang sudah dihitung disimpan di
	// memori instance ini. Dengan Redis, semua instance berbagi hitungan.
	var (
		redisClient                                *redis.Client
		authLimiter, commentLimiter, uploadLimiter middleware.Limiter
		views                                      service.ViewDeduper
	)
	if cfg.RedisURL != "" {
		redisClient, err = redisstore.Connect(ctx, cfg.RedisURL)
		if err != nil {
			return nil, fmt.Errorf("redis: %w", err)
		}
		checks["redis"] = func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }
		authLimiter = redisstore.NewLimiter(redisClient, "auth", cfg.AuthRateLimit)
		commentLimiter = redisstore.NewLimiter(redisClient, "comment", cfg.CommentRateLimit)
		uploadLimiter = redisstore.NewLimiter(redisClient, "upload", cfg.UploadRateLimit)
		views = redisstore.NewDeduper(redisClient, service.ViewWindow)
	} else {
		authLimiter = middleware.NewRateLimiter(cfg.AuthRateLimit)
		commentLimiter = middleware.NewRateLimiter(cfg.CommentRateLimit)
		uploadLimiter = middleware.NewRateLimiter(cfg.UploadRateLimit)
	}
	resolver, err := clientip.NewResolver(cfg.TrustedProxies)
	if err != nil {
		if redisClient != nil {
			redisClient.Close()
		}
		return nil, err
	}

	pages := pagination.Parser{DefaultPerPage: cfg.DefaultPerPage, MaxPerPage: cfg.MaxPerPage}
	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTokenTTL)

	users := repository.NewUserRepository(db)
	refreshTokens := repository.NewRefreshTokenRepository(db)
	categories := repository.NewCategoryRepository(db)
	tags := repository.NewTagRepository(db)
	articles := repository.NewArticleRepository(db)
	comments := repository.NewCommentRepository(db)
	engagement := repository.NewEngagementRepository(db)
	userTokens := repository.NewUserTokenRepository(db)

	authService := service.NewAuthService(users, refreshTokens, hasher, tokens, service.AuthOptions{
		RefreshTTL: cfg.RefreshTokenTTL,
		UserTokens: userTokens,
		Mailer:     mailer,
		AppURL:     cfg.AppURL,
		Uploads:    uploads,
	})
	articleService := service.NewArticleService(articles, categories, engagement, uploads, views)
	authors := repository.NewAuthorRepository(db)
	commentService := service.NewCommentService(comments, articles, service.CommentOptions{
		HideThreshold:        cfg.CommentHideThreshold,
		RequireVerifiedEmail: cfg.RequireEmailVerification,
		Users:                users,
	})

	handlers := router.Handlers{
		Health:     handler.NewHealthHandler(db, checks),
		Docs:       handler.NewDocsHandler(),
		Auth:       handler.NewAuthHandler(authService),
		Users:      handler.NewUserHandler(service.NewUserService(users), pages),
		Categories: handler.NewCategoryHandler(service.NewCategoryService(categories)),
		Tags:       handler.NewTagHandler(service.NewTagService(tags), pages),
		Articles:   handler.NewArticleHandler(articleService, pages),
		Comments:   handler.NewCommentHandler(commentService, pages),
		Stats:      handler.NewStatsHandler(service.NewStatsService(repository.NewStatsRepository(db))),
		Uploads:    handler.NewUploadHandler(uploads),
		Feeds:      handler.NewFeedHandler(service.NewFeedService(articles, categories, authors), cfg.AppURL),
		Authors:    handler.NewAuthorHandler(service.NewAuthorService(authors), pages),
		Search:     handler.NewSearchHandler(service.NewSearchService(repository.NewSearchRepository(db))),
	}

	return &App{
		Handler: router.New(handlers, router.Options{
			Tokens:         tokens,
			CORSOrigins:    cfg.CORSOrigins,
			AuthLimiter:    authLimiter,
			CommentLimiter: commentLimiter,
			UploadLimiter:  uploadLimiter,
			ClientIP:       resolver,
			MaxBodyBytes:   maxBodyBytes,
			MaxUploadBytes: cfg.MaxUploadBytes + 64<<10,
		}),
		Auth:          authService,
		Articles:      articleService,
		refreshTokens: refreshTokens,
		userTokens:    userTokens,
		redis:         redisClient,
	}, nil
}

// CleanupTokens menghapus refresh token dan token email yang kedaluwarsa
// secara berkala sampai ctx selesai.
func (a *App) CleanupTokens(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			deleted, err := a.refreshTokens.DeleteExpired(ctx, time.Now())
			if err != nil {
				slog.Error("gagal membersihkan refresh token", "error", err)
				continue
			}
			if deleted > 0 {
				slog.Info("refresh token kedaluwarsa dihapus", "jumlah", deleted)
			}
			if deleted, err := a.userTokens.DeleteExpired(ctx, time.Now()); err != nil {
				slog.Error("gagal membersihkan token email", "error", err)
			} else if deleted > 0 {
				slog.Info("token email kedaluwarsa dihapus", "jumlah", deleted)
			}
		}
	}
}

// PublishScheduled menerbitkan artikel terjadwal yang waktunya tiba, sekarang
// lalu setiap every sampai ctx selesai. Aman dijalankan di setiap instance:
// satu artikel hanya diterbitkan sekali.
func (a *App) PublishScheduled(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		published, err := a.Articles.PublishDue(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			slog.Error("gagal menerbitkan artikel terjadwal", "error", err)
		case published > 0:
			slog.Info("artikel terjadwal diterbitkan", "jumlah", published)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
