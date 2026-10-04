package model

import "time"

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleAuthor Role = "author"
	RoleReader Role = "reader"
)

func (r Role) Valid() bool {
	switch r {
	case RoleAdmin, RoleAuthor, RoleReader:
		return true
	}
	return false
}

// CanWrite menandakan role yang boleh menulis article.
func (r Role) CanWrite() bool {
	return r == RoleAdmin || r == RoleAuthor
}

type User struct {
	ID           int64
	Name         string
	Email        string
	PasswordHash string
	Role         Role
	// EmailVerifiedAt kosong berarti email belum dibuktikan milik pengguna.
	EmailVerifiedAt *time.Time
	// Bio dan AvatarURL tampil di profil publik penulis.
	Bio       string
	AvatarURL string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// RefreshToken disimpan dalam bentuk hash. Token aslinya hanya pernah
// dipegang klien.
type RefreshToken struct {
	ID        int64
	UserID    int64
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

const (
	TokenVerifyEmail   = "verify_email"
	TokenResetPassword = "reset_password"
)

// UserToken adalah token sekali pakai yang dikirim lewat email.
type UserToken struct {
	ID        int64
	UserID    int64
	Purpose   string
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// AuthorProfile adalah profil publik penulis beserta angka ringkasnya. Hanya
// artikel yang sudah terbit yang dihitung.
type AuthorProfile struct {
	ID            int64
	Name          string
	Bio           string
	AvatarURL     string
	Role          Role
	JoinedAt      time.Time
	ArticleCount  int64
	FollowerCount int64
	ViewCount     int64
	LikeCount     int64
}

// Public menentukan apakah profil boleh ditampilkan: penulis dan admin, atau
// siapa pun yang pernah menerbitkan artikel.
func (p AuthorProfile) Public() bool {
	return p.Role.CanWrite() || p.ArticleCount > 0
}
