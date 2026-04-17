package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexspence-oss/nexspence/internal/domain"
)

type roleRepo struct {
	db *pgxpool.Pool
}

func NewRoleRepo(db *pgxpool.Pool) *roleRepo {
	return &roleRepo{db: db}
}

func (r *roleRepo) List(ctx context.Context) ([]domain.Role, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, name, description, source, read_only, created_at, updated_at
		FROM roles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []domain.Role
	for rows.Next() {
		ro, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		roles = append(roles, *ro)
	}
	return roles, rows.Err()
}

func (r *roleRepo) Get(ctx context.Context, id string) (*domain.Role, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, name, description, source, read_only, created_at, updated_at
		FROM roles WHERE id = $1`, id)
	ro, err := scanRole(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return ro, err
}

func (r *roleRepo) Create(ctx context.Context, ro *domain.Role) error {
	return r.db.QueryRow(ctx, `
		INSERT INTO roles (name, description, source, read_only)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at`,
		ro.Name, ro.Description, ro.Source, ro.ReadOnly,
	).Scan(&ro.ID, &ro.CreatedAt, &ro.UpdatedAt)
}

func (r *roleRepo) Update(ctx context.Context, ro *domain.Role) error {
	_, err := r.db.Exec(ctx, `
		UPDATE roles SET name=$1, description=$2, updated_at=NOW()
		WHERE id=$3`,
		ro.Name, ro.Description, ro.ID,
	)
	return err
}

func (r *roleRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM roles WHERE id=$1`, id)
	return err
}

func (r *roleRepo) GetUserRoles(ctx context.Context, userID string) ([]domain.Role, error) {
	rows, err := r.db.Query(ctx, `
		SELECT ro.id, ro.name, ro.description, ro.source, ro.read_only, ro.created_at, ro.updated_at
		FROM roles ro
		JOIN user_roles ur ON ur.role_id = ro.id
		WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []domain.Role
	for rows.Next() {
		ro, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		roles = append(roles, *ro)
	}
	return roles, rows.Err()
}

func (r *roleRepo) SetUserRoles(ctx context.Context, userID string, roleIDs []string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, roleID := range roleIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO user_roles (user_id, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			userID, roleID,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func scanRole(row scanner) (*domain.Role, error) {
	var ro domain.Role
	err := row.Scan(&ro.ID, &ro.Name, &ro.Description, &ro.Source, &ro.ReadOnly,
		&ro.CreatedAt, &ro.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &ro, nil
}
