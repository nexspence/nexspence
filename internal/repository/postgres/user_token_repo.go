package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

type userTokenRepo struct {
	db *pgxpool.Pool
}

func NewUserTokenRepo(db *pgxpool.Pool) *userTokenRepo {
	return &userTokenRepo{db: db}
}

//nolint:gosec // not a credential: SQL column list ("token_hash" is a DB column name, no secret value)
const userTokenColumns = `t.id, t.user_id, u.username, t.name, t.token_hash, t.scopes, t.last_used, t.expires_at, t.created_at`

func scanUserToken(row pgx.Row) (*domain.UserToken, error) {
	var t domain.UserToken
	err := row.Scan(&t.ID, &t.UserID, &t.Username, &t.Name, &t.TokenHash, &t.Scopes, &t.LastUsed, &t.ExpiresAt, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *userTokenRepo) ListByUser(ctx context.Context, userID string) ([]domain.UserToken, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+userTokenColumns+`
		   FROM user_tokens t JOIN users u ON u.id = t.user_id
		  WHERE t.user_id = $1
		  ORDER BY t.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.UserToken
	for rows.Next() {
		t, err := scanUserToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (r *userTokenRepo) Get(ctx context.Context, id string) (*domain.UserToken, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+userTokenColumns+`
		   FROM user_tokens t JOIN users u ON u.id = t.user_id
		  WHERE t.id = $1`, id)
	t, err := scanUserToken(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

func (r *userTokenRepo) GetByHash(ctx context.Context, tokenHash string) (*domain.UserToken, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+userTokenColumns+`
		   FROM user_tokens t JOIN users u ON u.id = t.user_id
		  WHERE t.token_hash = $1`, tokenHash)
	t, err := scanUserToken(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

func (r *userTokenRepo) Create(ctx context.Context, t *domain.UserToken) error {
	scopes := t.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return r.db.QueryRow(ctx,
		`INSERT INTO user_tokens (user_id, name, token_hash, scopes, expires_at)
		      VALUES ($1, $2, $3, $4, $5)
		   RETURNING id, created_at`,
		t.UserID, t.Name, t.TokenHash, scopes, t.ExpiresAt,
	).Scan(&t.ID, &t.CreatedAt)
}

func (r *userTokenRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM user_tokens WHERE id = $1`, id)
	return err
}

func (r *userTokenRepo) TouchLastUsed(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `UPDATE user_tokens SET last_used = now() WHERE id = $1`, id)
	return err
}
