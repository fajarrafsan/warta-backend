package dto

import (
	"strings"
	"time"

	"warta/internal/model"
)

type RegisterRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (r *RegisterRequest) Normalize() {
	r.Name = collapseSpaces(r.Name)
	r.Email = strings.ToLower(strings.TrimSpace(r.Email))
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (r *LoginRequest) Normalize() {
	r.Email = strings.ToLower(strings.TrimSpace(r.Email))
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type TokenResponse struct {
	AccessToken  string       `json:"access_token"`
	TokenType    string       `json:"token_type"`
	ExpiresIn    int          `json:"expires_in"`
	RefreshToken string       `json:"refresh_token"`
	User         UserResponse `json:"user"`
}

// UpdateProfileRequest dipakai PATCH /me. Field yang tidak dikirim tidak
// diubah; avatar_url kosong menghapus foto.
type UpdateProfileRequest struct {
	Name      *string `json:"name"`
	Bio       *string `json:"bio"`
	AvatarURL *string `json:"avatar_url"`
}

func (r *UpdateProfileRequest) Normalize() {
	if r.Name != nil {
		name := collapseSpaces(*r.Name)
		r.Name = &name
	}
	if r.Bio != nil {
		bio := strings.TrimSpace(*r.Bio)
		r.Bio = &bio
	}
	if r.AvatarURL != nil {
		avatar := strings.TrimSpace(*r.AvatarURL)
		r.AvatarURL = &avatar
	}
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type ForgotPasswordRequest struct {
	Email string `json:"email"`
}

func (r *ForgotPasswordRequest) Normalize() {
	r.Email = strings.ToLower(strings.TrimSpace(r.Email))
}

type ResetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type VerifyEmailRequest struct {
	Token string `json:"token"`
}

type UpdateRoleRequest struct {
	Role string `json:"role"`
}

func (r *UpdateRoleRequest) Normalize() {
	r.Role = strings.ToLower(strings.TrimSpace(r.Role))
}

type UserQuery struct {
	Query string
	Role  string
}

// UserResponse berisi email, jadi hanya untuk pemilik akun dan admin.
type UserResponse struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
	// EmailVerified false berarti akun belum bisa berkomentar bila
	// verifikasi email diwajibkan.
	EmailVerified bool      `json:"email_verified"`
	Bio           string    `json:"bio"`
	AvatarURL     *string   `json:"avatar_url"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func NewUserResponse(u model.User) UserResponse {
	return UserResponse{
		ID:            u.ID,
		Name:          u.Name,
		Email:         u.Email,
		Role:          string(u.Role),
		EmailVerified: u.EmailVerifiedAt != nil,
		Bio:           u.Bio,
		AvatarURL:     optional(u.AvatarURL),
		CreatedAt:     u.CreatedAt,
		UpdatedAt:     u.UpdatedAt,
	}
}

func NewUserResponses(users []model.User) []UserResponse {
	responses := make([]UserResponse, 0, len(users))
	for _, u := range users {
		responses = append(responses, NewUserResponse(u))
	}
	return responses
}

// AuthorResponse adalah identitas publik penulis article atau komentar.
type AuthorResponse struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	AvatarURL *string `json:"avatar_url"`
}

// AuthorProfile adalah halaman publik penulis.
type AuthorProfile struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Bio           string    `json:"bio"`
	AvatarURL     *string   `json:"avatar_url"`
	Role          string    `json:"role"`
	JoinedAt      time.Time `json:"joined_at"`
	ArticleCount  int64     `json:"article_count"`
	FollowerCount int64     `json:"follower_count"`
	ViewCount     int64     `json:"view_count"`
	LikeCount     int64     `json:"like_count"`
	// Following selalu false bagi pengunjung yang belum login.
	Following bool `json:"following"`
}

func NewAuthorProfile(p model.AuthorProfile) AuthorProfile {
	return AuthorProfile{
		ID:            p.ID,
		Name:          p.Name,
		Bio:           p.Bio,
		AvatarURL:     optional(p.AvatarURL),
		Role:          string(p.Role),
		JoinedAt:      p.JoinedAt,
		ArticleCount:  p.ArticleCount,
		FollowerCount: p.FollowerCount,
		ViewCount:     p.ViewCount,
		LikeCount:     p.LikeCount,
	}
}

// FollowState adalah hasil mengikuti atau berhenti mengikuti.
type FollowState struct {
	Following     bool  `json:"following"`
	FollowerCount int64 `json:"follower_count"`
}

// optional mengubah string kosong menjadi null di JSON.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
