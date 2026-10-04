package service

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"warta/internal/apperr"
	"warta/internal/auth"
	"warta/internal/dto"
	"warta/internal/mail"
	"warta/internal/model"
	"warta/internal/pagination"
	"warta/internal/repository"
)

// Repository palsu di memori, cukup untuk menguji logika token tanpa MySQL.

type fakeUsers struct {
	byID map[int64]model.User
}

func (f *fakeUsers) Create(_ context.Context, u *model.User) error {
	for _, existing := range f.byID {
		if existing.Email == u.Email {
			return repository.ErrDuplicate
		}
	}
	u.ID = int64(len(f.byID) + 1)
	f.byID[u.ID] = *u
	return nil
}

func (f *fakeUsers) FindByID(_ context.Context, id int64) (model.User, error) {
	u, ok := f.byID[id]
	if !ok {
		return model.User{}, repository.ErrNotFound
	}
	return u, nil
}

func (f *fakeUsers) FindByEmail(_ context.Context, email string) (model.User, error) {
	for _, u := range f.byID {
		if u.Email == email {
			return u, nil
		}
	}
	return model.User{}, repository.ErrNotFound
}

func (f *fakeUsers) List(context.Context, repository.UserFilter, pagination.Params) ([]model.User, int64, error) {
	return nil, 0, nil
}

func (f *fakeUsers) UpdateProfile(_ context.Context, id int64, name, bio, avatarURL string) error {
	u := f.byID[id]
	u.Name, u.Bio, u.AvatarURL = name, bio, avatarURL
	f.byID[id] = u
	return nil
}

func (f *fakeUsers) UpdatePassword(_ context.Context, id int64, hash string) error {
	u := f.byID[id]
	u.PasswordHash = hash
	f.byID[id] = u
	return nil
}

func (f *fakeUsers) UpdateRole(_ context.Context, id int64, role model.Role) error {
	u := f.byID[id]
	u.Role = role
	f.byID[id] = u
	return nil
}

func (f *fakeUsers) MarkEmailVerified(_ context.Context, id int64) error {
	u := f.byID[id]
	if u.EmailVerifiedAt == nil {
		now := time.Now()
		u.EmailVerifiedAt = &now
	}
	f.byID[id] = u
	return nil
}

type fakeUserTokens struct {
	rows    []model.UserToken
	created []time.Time
}

func (f *fakeUserTokens) Replace(_ context.Context, t *model.UserToken) error {
	now := time.Now()
	for i := range f.rows {
		if f.rows[i].UserID == t.UserID && f.rows[i].Purpose == t.Purpose && f.rows[i].UsedAt == nil {
			f.rows[i].UsedAt = &now
		}
	}
	t.ID = int64(len(f.rows) + 1)
	f.rows = append(f.rows, *t)
	f.created = append(f.created, now)
	return nil
}

func (f *fakeUserTokens) FindByHash(_ context.Context, purpose, hash string) (model.UserToken, error) {
	for _, t := range f.rows {
		if t.TokenHash == hash && t.Purpose == purpose {
			return t, nil
		}
	}
	return model.UserToken{}, repository.ErrNotFound
}

func (f *fakeUserTokens) Use(_ context.Context, id int64) (bool, error) {
	t := &f.rows[id-1]
	if t.UsedAt != nil {
		return false, nil
	}
	now := time.Now()
	t.UsedAt = &now
	return true, nil
}

func (f *fakeUserTokens) IssuedSince(_ context.Context, userID int64, purpose string, since time.Time) (bool, error) {
	for i, t := range f.rows {
		if t.UserID == userID && t.Purpose == purpose && f.created[i].After(since) {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeUserTokens) DeleteExpired(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type fakeMailer struct {
	sent []mail.Message
}

func (f *fakeMailer) Send(_ context.Context, m mail.Message) error {
	f.sent = append(f.sent, m)
	return nil
}

var linkToken = regexp.MustCompile(`\?token=(\S+)`)

// tokenFrom mengambil token dari tautan di email terakhir.
func tokenFrom(t *testing.T, m *fakeMailer) string {
	t.Helper()
	if len(m.sent) == 0 {
		t.Fatal("tidak ada email terkirim")
	}
	match := linkToken.FindStringSubmatch(m.sent[len(m.sent)-1].Text)
	if match == nil {
		t.Fatalf("email tanpa tautan: %s", m.sent[len(m.sent)-1].Text)
	}
	token, _ := url.QueryUnescape(match[1])
	return token
}

type fakeTokens struct {
	rows []model.RefreshToken
}

func (f *fakeTokens) Create(_ context.Context, t *model.RefreshToken) error {
	t.ID = int64(len(f.rows) + 1)
	f.rows = append(f.rows, *t)
	return nil
}

func (f *fakeTokens) FindByHash(_ context.Context, hash string) (model.RefreshToken, error) {
	for _, t := range f.rows {
		if t.TokenHash == hash {
			return t, nil
		}
	}
	return model.RefreshToken{}, repository.ErrNotFound
}

func (f *fakeTokens) Revoke(_ context.Context, id int64) (bool, error) {
	t := &f.rows[id-1]
	if t.RevokedAt != nil {
		return false, nil
	}
	now := time.Now()
	t.RevokedAt = &now
	return true, nil
}

func (f *fakeTokens) RevokeAllForUser(_ context.Context, userID int64) error {
	for i := range f.rows {
		if f.rows[i].UserID == userID && f.rows[i].RevokedAt == nil {
			now := time.Now()
			f.rows[i].RevokedAt = &now
		}
	}
	return nil
}

func (f *fakeTokens) DeleteExpired(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func newAuthService() (*authService, *fakeTokens) {
	s, tokens, _ := newAuthServiceWithMail()
	return s, tokens
}

func newAuthServiceWithMail() (*authService, *fakeTokens, *fakeMailer) {
	tokens := &fakeTokens{}
	mailer := &fakeMailer{}
	s := NewAuthService(
		&fakeUsers{byID: map[int64]model.User{}},
		tokens,
		auth.BcryptHasher{Cost: bcrypt.MinCost},
		auth.NewTokenManager("rahasia-test-yang-panjangnya-lebih-dari-32", "warta", time.Minute),
		AuthOptions{RefreshTTL: time.Hour, UserTokens: &fakeUserTokens{}, Mailer: mailer, AppURL: "https://warta.id"},
	).(*authService)
	return s, tokens, mailer
}

func status(err error) int {
	var appErr *apperr.Error
	if errors.As(err, &appErr) {
		return appErr.Status
	}
	return 0
}

func TestRefreshRotation(t *testing.T) {
	s, _ := newAuthService()
	ctx := context.Background()

	first, err := s.Register(ctx, dto.RegisterRequest{Name: "Budi", Email: "budi@warta.test", Password: "rahasia123"})
	if err != nil {
		t.Fatal(err)
	}

	second, err := s.Refresh(ctx, dto.RefreshRequest{RefreshToken: first.RefreshToken})
	if err != nil {
		t.Fatal(err)
	}
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh token seharusnya dirotasi")
	}

	// Token lama dipakai lagi: ditolak, dan token terbaru ikut dicabut.
	if _, err := s.Refresh(ctx, dto.RefreshRequest{RefreshToken: first.RefreshToken}); status(err) != 401 {
		t.Fatalf("token lama: %v", err)
	}
	if _, err := s.Refresh(ctx, dto.RefreshRequest{RefreshToken: second.RefreshToken}); status(err) != 401 {
		t.Fatalf("token terbaru seharusnya ikut dicabut: %v", err)
	}
}

func TestRefreshExpired(t *testing.T) {
	s, _ := newAuthService()
	ctx := context.Background()

	pair, err := s.Register(ctx, dto.RegisterRequest{Name: "Budi", Email: "budi@warta.test", Password: "rahasia123"})
	if err != nil {
		t.Fatal(err)
	}

	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := s.Refresh(ctx, dto.RefreshRequest{RefreshToken: pair.RefreshToken}); status(err) != 401 {
		t.Fatalf("token kedaluwarsa: %v", err)
	}
}

func TestLoginAndPasswordChange(t *testing.T) {
	s, tokens := newAuthService()
	ctx := context.Background()

	pair, err := s.Register(ctx, dto.RegisterRequest{Name: "Budi", Email: "Budi@Warta.test", Password: "rahasia123"})
	if err != nil {
		t.Fatal(err)
	}
	if pair.User.Role != string(model.RoleReader) {
		t.Fatalf("role awal: %s", pair.User.Role)
	}

	if _, err := s.Login(ctx, dto.LoginRequest{Email: "budi@warta.test", Password: "salah-sekali"}); status(err) != 401 {
		t.Fatalf("password salah: %v", err)
	}
	if _, err := s.Login(ctx, dto.LoginRequest{Email: " BUDI@warta.test ", Password: "rahasia123"}); err != nil {
		t.Fatalf("email tidak peka huruf besar: %v", err)
	}

	actor := auth.Actor{ID: pair.User.ID, Role: model.RoleReader}
	err = s.ChangePassword(ctx, actor, dto.ChangePasswordRequest{CurrentPassword: "rahasia123", NewPassword: "rahasia456"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range tokens.rows {
		if row.RevokedAt == nil {
			t.Fatal("semua refresh token seharusnya dicabut setelah ganti password")
		}
	}
	if _, err := s.Login(ctx, dto.LoginRequest{Email: "budi@warta.test", Password: "rahasia456"}); err != nil {
		t.Fatalf("login dengan password baru: %v", err)
	}
}

func TestEnsureAdmin(t *testing.T) {
	s, _ := newAuthService()
	ctx := context.Background()

	pair, _ := s.Register(ctx, dto.RegisterRequest{Name: "Budi", Email: "budi@warta.test", Password: "rahasia123"})
	if err := s.EnsureAdmin(ctx, "Budi", "budi@warta.test", "apa-saja-123"); err != nil {
		t.Fatal(err)
	}

	me, _ := s.Me(ctx, auth.Actor{ID: pair.User.ID})
	if me.Role != string(model.RoleAdmin) || !me.EmailVerified {
		t.Fatalf("akun yang sudah ada seharusnya dinaikkan menjadi admin terverifikasi: %+v", me)
	}
	// Password akun yang sudah ada tidak diubah.
	if _, err := s.Login(ctx, dto.LoginRequest{Email: "budi@warta.test", Password: "rahasia123"}); err != nil {
		t.Fatalf("password lama: %v", err)
	}
}

func TestVerifyEmail(t *testing.T) {
	s, _, mailer := newAuthServiceWithMail()
	ctx := context.Background()

	pair, err := s.Register(ctx, dto.RegisterRequest{Name: "Budi", Email: "budi@warta.test", Password: "rahasia123"})
	if err != nil {
		t.Fatal(err)
	}
	if pair.User.EmailVerified {
		t.Fatal("akun baru belum terverifikasi")
	}
	if len(mailer.sent) != 1 || mailer.sent[0].To != "budi@warta.test" {
		t.Fatalf("email verifikasi: %+v", mailer.sent)
	}
	token := tokenFrom(t, mailer)

	actor := auth.Actor{ID: pair.User.ID}
	if err := s.ResendVerification(ctx, actor); status(err) != 429 {
		t.Fatalf("kirim ulang langsung seharusnya ditahan: %v", err)
	}

	me, err := s.VerifyEmail(ctx, dto.VerifyEmailRequest{Token: token})
	if err != nil || !me.EmailVerified {
		t.Fatalf("verifikasi: %+v %v", me, err)
	}
	if _, err := s.VerifyEmail(ctx, dto.VerifyEmailRequest{Token: token}); status(err) != 422 {
		t.Fatalf("token hanya sekali pakai: %v", err)
	}
	if err := s.ResendVerification(ctx, actor); status(err) != 409 {
		t.Fatalf("akun terverifikasi tidak perlu kirim ulang: %v", err)
	}
}

func TestResetPassword(t *testing.T) {
	s, tokens, mailer := newAuthServiceWithMail()
	ctx := context.Background()

	if _, err := s.Register(ctx, dto.RegisterRequest{Name: "Budi", Email: "budi@warta.test", Password: "rahasia123"}); err != nil {
		t.Fatal(err)
	}
	mailer.sent = nil

	// Email yang tidak terdaftar tetap berhasil, tanpa mengirim apa pun.
	if err := s.ForgotPassword(ctx, dto.ForgotPasswordRequest{Email: "siapa@warta.test"}); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 0 {
		t.Fatal("email tidak terdaftar tidak boleh dikirimi")
	}

	if err := s.ForgotPassword(ctx, dto.ForgotPasswordRequest{Email: " BUDI@warta.test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ForgotPassword(ctx, dto.ForgotPasswordRequest{Email: "budi@warta.test"}); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("permintaan beruntun cukup satu email, terkirim %d", len(mailer.sent))
	}
	token := tokenFrom(t, mailer)

	if err := s.ResetPassword(ctx, dto.ResetPasswordRequest{Token: "palsu", NewPassword: "baru-sekali-1"}); status(err) != 422 {
		t.Fatalf("token palsu: %v", err)
	}
	if err := s.ResetPassword(ctx, dto.ResetPasswordRequest{Token: token, NewPassword: "baru-sekali-1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetPassword(ctx, dto.ResetPasswordRequest{Token: token, NewPassword: "baru-lagi-22"}); status(err) != 422 {
		t.Fatalf("token hanya sekali pakai: %v", err)
	}

	for _, row := range tokens.rows {
		if row.RevokedAt == nil {
			t.Fatal("semua sesi seharusnya dicabut setelah reset password")
		}
	}
	pair, err := s.Login(ctx, dto.LoginRequest{Email: "budi@warta.test", Password: "baru-sekali-1"})
	if err != nil {
		t.Fatalf("login dengan password baru: %v", err)
	}
	if !pair.User.EmailVerified {
		t.Fatal("reset lewat email sekaligus membuktikan email")
	}
}

func TestResetTokenExpires(t *testing.T) {
	s, _, mailer := newAuthServiceWithMail()
	ctx := context.Background()

	_, _ = s.Register(ctx, dto.RegisterRequest{Name: "Budi", Email: "budi@warta.test", Password: "rahasia123"})
	if err := s.ForgotPassword(ctx, dto.ForgotPasswordRequest{Email: "budi@warta.test"}); err != nil {
		t.Fatal(err)
	}
	token := tokenFrom(t, mailer)

	s.now = func() time.Time { return time.Now().Add(resetPasswordTTL + time.Minute) }
	if err := s.ResetPassword(ctx, dto.ResetPasswordRequest{Token: token, NewPassword: "baru-sekali-1"}); status(err) != 422 {
		t.Fatalf("token kedaluwarsa: %v", err)
	}
}
