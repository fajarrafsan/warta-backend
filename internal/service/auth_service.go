package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"warta/internal/apperr"
	"warta/internal/auth"
	"warta/internal/dto"
	"warta/internal/mail"
	"warta/internal/model"
	"warta/internal/repository"
	"warta/internal/validation"
)

type AuthService interface {
	Register(ctx context.Context, req dto.RegisterRequest) (dto.TokenResponse, error)
	Login(ctx context.Context, req dto.LoginRequest) (dto.TokenResponse, error)
	Refresh(ctx context.Context, req dto.RefreshRequest) (dto.TokenResponse, error)
	Logout(ctx context.Context, req dto.RefreshRequest) error

	Me(ctx context.Context, actor auth.Actor) (dto.UserResponse, error)
	UpdateProfile(ctx context.Context, actor auth.Actor, req dto.UpdateProfileRequest) (dto.UserResponse, error)
	ChangePassword(ctx context.Context, actor auth.Actor, req dto.ChangePasswordRequest) error

	// VerifyEmail menandai email terverifikasi dari tautan yang dikirim saat
	// mendaftar.
	VerifyEmail(ctx context.Context, req dto.VerifyEmailRequest) (dto.UserResponse, error)
	ResendVerification(ctx context.Context, actor auth.Actor) error
	// ForgotPassword selalu berhasil, terdaftar atau tidak emailnya, supaya
	// endpoint ini tidak bisa dipakai menebak email yang terdaftar.
	ForgotPassword(ctx context.Context, req dto.ForgotPasswordRequest) error
	ResetPassword(ctx context.Context, req dto.ResetPasswordRequest) error

	// EnsureAdmin membuat akun admin pertama bila belum ada, dipanggil saat
	// service menyala.
	EnsureAdmin(ctx context.Context, name, email, password string) error
}

// AuthOptions adalah pengaturan tambahan AuthService.
type AuthOptions struct {
	RefreshTTL time.Duration
	// UserTokens menyimpan token sekali pakai untuk verifikasi email dan
	// reset password.
	UserTokens repository.UserTokenRepository
	Mailer     mail.Mailer
	// AppURL adalah alamat frontend, awal tautan di email.
	AppURL string
	// Uploads memeriksa bahwa foto profil memang sudah diunggah.
	Uploads CoverChecker
}

type authService struct {
	users      repository.UserRepository
	tokens     repository.RefreshTokenRepository
	userTokens repository.UserTokenRepository
	mailer     mail.Mailer
	appURL     string
	uploads    CoverChecker
	hasher     auth.PasswordHasher
	jwt        *auth.TokenManager
	refreshTTL time.Duration
	now        func() time.Time

	dummyOnce sync.Once
	dummyHash string
}

func NewAuthService(
	users repository.UserRepository,
	tokens repository.RefreshTokenRepository,
	hasher auth.PasswordHasher,
	jwt *auth.TokenManager,
	opt AuthOptions,
) AuthService {
	return &authService{
		users:      users,
		tokens:     tokens,
		userTokens: opt.UserTokens,
		mailer:     opt.Mailer,
		appURL:     opt.AppURL,
		uploads:    opt.Uploads,
		hasher:     hasher,
		jwt:        jwt,
		refreshTTL: opt.RefreshTTL,
		now:        time.Now,
	}
}

var (
	errBadCredentials = apperr.Unauthorized("email atau password salah")
	errBadRefresh     = apperr.Unauthorized("refresh token tidak valid atau sudah kedaluwarsa")
)

func (s *authService) Register(ctx context.Context, req dto.RegisterRequest) (dto.TokenResponse, error) {
	req.Normalize()

	if problems := validation.ValidateRegister(req); len(problems) > 0 {
		return dto.TokenResponse{}, apperr.Validation(problems)
	}

	if _, err := s.users.FindByEmail(ctx, req.Email); err == nil {
		return dto.TokenResponse{}, emailTaken()
	} else if !errors.Is(err, repository.ErrNotFound) {
		return dto.TokenResponse{}, apperr.Internal(err)
	}

	hash, err := s.hasher.Hash(req.Password)
	if err != nil {
		return dto.TokenResponse{}, apperr.Internal(err)
	}

	// Akun baru selalu reader. Admin yang menaikkannya menjadi author.
	user := model.User{Name: req.Name, Email: req.Email, PasswordHash: hash, Role: model.RoleReader}
	if err := s.users.Create(ctx, &user); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			return dto.TokenResponse{}, emailTaken()
		}
		return dto.TokenResponse{}, apperr.Internal(err)
	}

	// Pendaftaran tetap berhasil walau email gagal dikirim; pengguna bisa
	// meminta kirim ulang.
	if err := s.sendVerification(ctx, user); err != nil {
		slog.ErrorContext(ctx, "gagal menyiapkan email verifikasi", "user_id", user.ID, "error", err)
	}

	return s.issue(ctx, user)
}

func emailTaken() *apperr.Error {
	return apperr.Validation(map[string]string{"email": "email sudah terdaftar"})
}

func (s *authService) Login(ctx context.Context, req dto.LoginRequest) (dto.TokenResponse, error) {
	req.Normalize()

	if problems := validation.ValidateLogin(req); len(problems) > 0 {
		return dto.TokenResponse{}, apperr.Validation(problems)
	}

	user, err := s.users.FindByEmail(ctx, req.Email)
	if errors.Is(err, repository.ErrNotFound) {
		// Tetap menjalankan bcrypt supaya waktu respons email yang tidak
		// terdaftar tidak berbeda dari password yang salah.
		s.hasher.Compare(s.dummy(), req.Password)
		return dto.TokenResponse{}, errBadCredentials
	}
	if err != nil {
		return dto.TokenResponse{}, apperr.Internal(err)
	}

	if !s.hasher.Compare(user.PasswordHash, req.Password) {
		return dto.TokenResponse{}, errBadCredentials
	}

	return s.issue(ctx, user)
}

func (s *authService) dummy() string {
	s.dummyOnce.Do(func() {
		s.dummyHash, _ = s.hasher.Hash("warta-dummy-password")
	})
	return s.dummyHash
}

// Refresh menukar refresh token dengan pasangan token baru. Token lama
// langsung dicabut (rotasi). Bila token yang sudah dicabut dipakai lagi,
// kemungkinan besar token itu bocor, jadi semua sesi pemiliknya ikut dicabut.
func (s *authService) Refresh(ctx context.Context, req dto.RefreshRequest) (dto.TokenResponse, error) {
	if req.RefreshToken == "" {
		return dto.TokenResponse{}, apperr.Validation(map[string]string{"refresh_token": "refresh_token wajib diisi"})
	}

	stored, err := s.tokens.FindByHash(ctx, auth.HashRefreshToken(req.RefreshToken))
	if errors.Is(err, repository.ErrNotFound) {
		return dto.TokenResponse{}, errBadRefresh
	}
	if err != nil {
		return dto.TokenResponse{}, apperr.Internal(err)
	}

	if stored.RevokedAt != nil {
		return dto.TokenResponse{}, s.revokeAll(ctx, stored.UserID)
	}
	if !s.now().Before(stored.ExpiresAt) {
		return dto.TokenResponse{}, errBadRefresh
	}

	revoked, err := s.tokens.Revoke(ctx, stored.ID)
	if err != nil {
		return dto.TokenResponse{}, apperr.Internal(err)
	}
	if !revoked {
		return dto.TokenResponse{}, s.revokeAll(ctx, stored.UserID)
	}

	user, err := s.users.FindByID(ctx, stored.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return dto.TokenResponse{}, errBadRefresh
	}
	if err != nil {
		return dto.TokenResponse{}, apperr.Internal(err)
	}

	return s.issue(ctx, user)
}

func (s *authService) revokeAll(ctx context.Context, userID int64) error {
	if err := s.tokens.RevokeAllForUser(ctx, userID); err != nil {
		return apperr.Internal(err)
	}
	return errBadRefresh
}

// Logout mencabut refresh token. Token yang tidak dikenal diabaikan supaya
// logout selalu berhasil dari sisi klien.
func (s *authService) Logout(ctx context.Context, req dto.RefreshRequest) error {
	if req.RefreshToken == "" {
		return apperr.Validation(map[string]string{"refresh_token": "refresh_token wajib diisi"})
	}

	stored, err := s.tokens.FindByHash(ctx, auth.HashRefreshToken(req.RefreshToken))
	if errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	if err != nil {
		return apperr.Internal(err)
	}

	if _, err := s.tokens.Revoke(ctx, stored.ID); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

func (s *authService) Me(ctx context.Context, actor auth.Actor) (dto.UserResponse, error) {
	user, err := s.current(ctx, actor)
	if err != nil {
		return dto.UserResponse{}, err
	}
	return dto.NewUserResponse(user), nil
}

func (s *authService) UpdateProfile(ctx context.Context, actor auth.Actor, req dto.UpdateProfileRequest) (dto.UserResponse, error) {
	req.Normalize()

	if problems := validation.ValidateProfile(req); len(problems) > 0 {
		return dto.UserResponse{}, apperr.Validation(problems)
	}

	user, err := s.current(ctx, actor)
	if err != nil {
		return dto.UserResponse{}, err
	}

	name, bio, avatar := user.Name, user.Bio, user.AvatarURL
	if req.Name != nil {
		name = *req.Name
	}
	if req.Bio != nil {
		bio = *req.Bio
	}
	if req.AvatarURL != nil {
		avatar = *req.AvatarURL
		if avatar != "" && avatar != user.AvatarURL && (s.uploads == nil || !s.uploads.Exists(ctx, avatar)) {
			return dto.UserResponse{}, apperr.Validation(map[string]string{"avatar_url": "foto tidak ditemukan, unggah ulang"})
		}
	}

	if err := s.users.UpdateProfile(ctx, actor.ID, name, bio, avatar); err != nil {
		return dto.UserResponse{}, apperr.Internal(err)
	}
	return s.Me(ctx, actor)
}

// ChangePassword juga mencabut semua refresh token, sehingga sesi di
// perangkat lain harus login ulang.
func (s *authService) ChangePassword(ctx context.Context, actor auth.Actor, req dto.ChangePasswordRequest) error {
	if problems := validation.ValidateChangePassword(req); len(problems) > 0 {
		return apperr.Validation(problems)
	}

	user, err := s.current(ctx, actor)
	if err != nil {
		return err
	}
	if !s.hasher.Compare(user.PasswordHash, req.CurrentPassword) {
		return apperr.Validation(map[string]string{"current_password": "current_password salah"})
	}

	hash, err := s.hasher.Hash(req.NewPassword)
	if err != nil {
		return apperr.Internal(err)
	}
	if err := s.users.UpdatePassword(ctx, user.ID, hash); err != nil {
		return apperr.Internal(err)
	}
	if err := s.tokens.RevokeAllForUser(ctx, user.ID); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

func (s *authService) EnsureAdmin(ctx context.Context, name, email, password string) error {
	// Email admin berasal dari konfigurasi server, jadi dianggap
	// terverifikasi.
	user, err := s.users.FindByEmail(ctx, email)
	if err == nil {
		if user.Role != model.RoleAdmin {
			if err := s.users.UpdateRole(ctx, user.ID, model.RoleAdmin); err != nil {
				return err
			}
		}
		if user.EmailVerifiedAt == nil {
			return s.users.MarkEmailVerified(ctx, user.ID)
		}
		return nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return err
	}

	hash, err := s.hasher.Hash(password)
	if err != nil {
		return err
	}

	verified := s.now()
	admin := model.User{Name: name, Email: email, PasswordHash: hash, Role: model.RoleAdmin, EmailVerifiedAt: &verified}
	return s.users.Create(ctx, &admin)
}

// current memuat akun pemilik token. Akun yang sudah tidak ada diperlakukan
// sebagai token yang tidak berlaku.
func (s *authService) current(ctx context.Context, actor auth.Actor) (model.User, error) {
	user, err := s.users.FindByID(ctx, actor.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return model.User{}, apperr.Unauthorized("akun tidak ditemukan")
	}
	if err != nil {
		return model.User{}, apperr.Internal(err)
	}
	return user, nil
}

func (s *authService) issue(ctx context.Context, user model.User) (dto.TokenResponse, error) {
	access, err := s.jwt.Issue(user)
	if err != nil {
		return dto.TokenResponse{}, apperr.Internal(err)
	}

	plain, hash, err := auth.NewRefreshToken()
	if err != nil {
		return dto.TokenResponse{}, apperr.Internal(err)
	}

	stored := model.RefreshToken{UserID: user.ID, TokenHash: hash, ExpiresAt: s.now().Add(s.refreshTTL)}
	if err := s.tokens.Create(ctx, &stored); err != nil {
		return dto.TokenResponse{}, apperr.Internal(err)
	}

	return dto.TokenResponse{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    int(s.jwt.TTL().Seconds()),
		RefreshToken: plain,
		User:         dto.NewUserResponse(user),
	}, nil
}
