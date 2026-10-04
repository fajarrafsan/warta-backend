package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"warta/internal/app"
	"warta/internal/auth"
	"warta/internal/config"
	"warta/internal/database"
	"warta/internal/logging"
)

func main() {
	if err := run(); err != nil {
		slog.Error("service berhenti", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	slog.SetDefault(logging.New(os.Stdout, cfg.LogFormat, cfg.LogLevel))
	for _, warning := range cfg.Warnings() {
		slog.Warn(warning)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.AutoMigrate {
		if err := database.CreateDatabase(ctx, cfg.ServerDSN(), cfg.DBName); err != nil {
			return errors.Join(errors.New("gagal membuat database"), err)
		}
		if err := database.MigrateUp(cfg.MigrationDSN(), cfg.DBName); err != nil {
			return errors.Join(errors.New("gagal menjalankan migrasi"), err)
		}
		slog.Info("migrasi database selesai")
	}

	db, err := database.Connect(ctx, cfg.DSN())
	if err != nil {
		return errors.Join(errors.New("gagal terhubung ke database"), err)
	}
	defer db.Close()

	a, err := app.New(cfg, db, auth.BcryptHasher{Cost: bcrypt.DefaultCost}, app.NewMailer(cfg))
	if err != nil {
		return errors.Join(errors.New("gagal menyiapkan service"), err)
	}
	defer a.Close()

	if cfg.AdminEmail != "" {
		if err := a.Auth.EnsureAdmin(ctx, cfg.AdminName, cfg.AdminEmail, cfg.AdminPassword); err != nil {
			return errors.Join(errors.New("gagal menyiapkan akun admin"), err)
		}
		slog.Info("akun admin siap", "email", cfg.AdminEmail)
	}

	go a.CleanupTokens(ctx, time.Hour)
	go a.PublishScheduled(ctx, 30*time.Second)

	server := &http.Server{
		Addr:              ":" + cfg.AppPort,
		Handler:           a.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("service berjalan", "addr", "http://localhost:"+cfg.AppPort, "docs", "http://localhost:"+cfg.AppPort+"/docs")
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}

	slog.Info("mematikan service...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return errors.Join(errors.New("gagal mematikan service dengan rapi"), err)
	}
	slog.Info("service berhenti")
	return nil
}
