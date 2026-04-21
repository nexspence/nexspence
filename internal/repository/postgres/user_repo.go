package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexspence-oss/nexspence/internal/domain"
)

type userRepo struct {
	db *pgxpool.Pool
}

func NewUserRepo(db *pgxpool.Pool) *userRepo {
	return &userRepo{db: db}
}

const userSelect = `
	SELECT id, username, COALESCE(email,''), COALESCE(password_hash,''), first_name, last_name,
	       status, source, COALESCE(external_id,''), last_login, created_at, updated_at
	FROM users`

func (r *userRepo) List(ctx context.Context, source string) ([]domain.User, error) {
	q := userSelect
	args := []any{}
	if source != "" {
		q += " WHERE source = $1"
		args = append(args, source)
	}
	q += " ORDER BY username"

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		roles, _ := r.userRoleNames(ctx, u.ID)
		u.Roles = roles
		users = append(users, *u)
	}
	return users, rows.Err()
}

func (r *userRepo) Get(ctx context.Context, username string) (*domain.User, error) {
	row := r.db.QueryRow(ctx, userSelect+" WHERE username=$1", username)
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	roles, _ := r.userRoleNames(ctx, u.ID)
	u.Roles = roles
	return u, nil
}

func (r *userRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	row := r.db.QueryRow(ctx, userSelect+" WHERE id=$1", id)
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	roles, _ := r.userRoleNames(ctx, u.ID)
	u.Roles = roles
	return u, nil
}

func (r *userRepo) Create(ctx context.Context, u *domain.User) error {
	return r.db.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, first_name, last_name, status, source)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, created_at, updated_at`,
		u.Username, nilIfEmpty(u.Email), nilIfEmpty(u.PasswordHash),
		u.FirstName, u.LastName, u.Status, u.Source,
	).Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt)
}

func (r *userRepo) Update(ctx context.Context, u *domain.User) error {
	_, err := r.db.Exec(ctx, `
		UPDATE users SET email=$1, first_name=$2, last_name=$3, status=$4, updated_at=NOW()
		WHERE username=$5`,
		nilIfEmpty(u.Email), u.FirstName, u.LastName, u.Status, u.Username,
	)
	return err
}

func (r *userRepo) UpdatePassword(ctx context.Context, username, hash string) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET password_hash=$1, updated_at=NOW() WHERE username=$2`, hash, username)
	return err
}

func (r *userRepo) Delete(ctx context.Context, username string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM users WHERE username=$1`, username)
	return err
}

func (r *userRepo) UpdateLastLogin(ctx context.Context, username string) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET last_login=NOW() WHERE username=$1`, username)
	return err
}

// ── Helpers ──────────────────────────────────────────────────

func scanUser(row scanner) (*domain.User, error) {
	var u domain.User
	err := row.Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash,
		&u.FirstName, &u.LastName, &u.Status, &u.Source,
		&u.ExternalID, &u.LastLogin,
		&u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	u.Roles = []string{}
	return &u, nil
}

func (r *userRepo) userRoleNames(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT ro.name FROM roles ro
		JOIN user_roles ur ON ur.role_id = ro.id
		WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
