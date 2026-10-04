package repository

import (
	"context"
	"database/sql"
	"errors"

	"warta/internal/model"
	"warta/internal/pagination"
)

type UserFilter struct {
	Query string
	Role  model.Role
}

type UserRepository interface {
	Create(ctx context.Context, u *model.User) error
	FindByID(ctx context.Context, id int64) (model.User, error)
	FindByEmail(ctx context.Context, email string) (model.User, error)
	List(ctx context.Context, f UserFilter, p pagination.Params) ([]model.User, int64, error)
	UpdateProfile(ctx context.Context, id int64, name, bio, avatarURL string) error
	UpdatePassword(ctx context.Context, id int64, hash string) error
	UpdateRole(ctx context.Context, id int64, role model.Role) error
	MarkEmailVerified(ctx context.Context, id int64) error
}

type userRepository struct {
	db *sql.DB
}

func NewUserRepository(db *sql.DB) UserRepository {
	return &userRepository{db: db}
}

const userColumns = "id, name, email, password_hash, role, email_verified_at, bio, avatar_url, created_at, updated_at"

func scanUser(row interface{ Scan(...any) error }, u *model.User) error {
	var verified sql.NullTime
	var avatar sql.NullString
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role, &verified, &u.Bio, &avatar, &u.CreatedAt, &u.UpdatedAt)
	u.EmailVerifiedAt = nullTimePtr(verified)
	u.AvatarURL = avatar.String
	return err
}

func (r *userRepository) Create(ctx context.Context, u *model.User) error {
	result, err := r.db.ExecContext(ctx,
		"INSERT INTO users (name, email, password_hash, role, email_verified_at) VALUES (?, ?, ?, ?, ?)",
		u.Name, u.Email, u.PasswordHash, u.Role, u.EmailVerifiedAt)
	if err != nil {
		return mapError(err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return err
	}

	created, err := r.FindByID(ctx, id)
	if err != nil {
		return err
	}
	*u = created
	return nil
}

func (r *userRepository) FindByID(ctx context.Context, id int64) (model.User, error) {
	return r.findOne(ctx, "id = ?", id)
}

func (r *userRepository) FindByEmail(ctx context.Context, email string) (model.User, error) {
	return r.findOne(ctx, "email = ?", email)
}

func (r *userRepository) findOne(ctx context.Context, condition string, arg any) (model.User, error) {
	var u model.User
	err := scanUser(r.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE "+condition, arg), &u)
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, ErrNotFound
	}
	return u, err
}

func (r *userRepository) List(ctx context.Context, f UserFilter, p pagination.Params) ([]model.User, int64, error) {
	var conditions []string
	var args []any

	if f.Query != "" {
		pattern := "%" + escapeLike(f.Query) + "%"
		conditions = append(conditions, "(name LIKE ? OR email LIKE ?)")
		args = append(args, pattern, pattern)
	}
	if f.Role != "" {
		conditions = append(conditions, "role = ?")
		args = append(args, f.Role)
	}
	where := whereClause(conditions)

	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx,
		"SELECT "+userColumns+" FROM users"+where+" ORDER BY id ASC LIMIT ? OFFSET ?",
		append(args, p.Limit(), p.Offset())...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []model.User
	for rows.Next() {
		var u model.User
		if err := scanUser(rows, &u); err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, rows.Err()
}

func (r *userRepository) UpdateProfile(ctx context.Context, id int64, name, bio, avatarURL string) error {
	return r.update(ctx, "UPDATE users SET name = ?, bio = ?, avatar_url = ? WHERE id = ?", name, bio, nullString(avatarURL), id)
}

func (r *userRepository) UpdatePassword(ctx context.Context, id int64, hash string) error {
	return r.update(ctx, "UPDATE users SET password_hash = ? WHERE id = ?", hash, id)
}

func (r *userRepository) UpdateRole(ctx context.Context, id int64, role model.Role) error {
	return r.update(ctx, "UPDATE users SET role = ? WHERE id = ?", role, id)
}

func (r *userRepository) update(ctx context.Context, query string, args ...any) error {
	_, err := r.db.ExecContext(ctx, query, args...)
	return mapError(err)
}

func (r *userRepository) MarkEmailVerified(ctx context.Context, id int64) error {
	return r.update(ctx, "UPDATE users SET email_verified_at = COALESCE(email_verified_at, CURRENT_TIMESTAMP) WHERE id = ?", id)
}
