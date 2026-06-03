package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

type MigrationRepo struct{ pool *pgxpool.Pool }

func NewMigrationRepo(pool *pgxpool.Pool) *MigrationRepo {
	return &MigrationRepo{pool: pool}
}

const migrationCols = `id, source_url, source_user, status,
	migrate_repos, migrate_users, migrate_blobs, migrate_policies,
	total_repos, done_repos, total_assets, done_assets,
	total_bytes, done_bytes, error_count, last_error,
	started_at, finished_at, created_at, updated_at`

func scanJob(row pgx.Row) (*domain.MigrationJob, error) {
	var j domain.MigrationJob
	err := row.Scan(
		&j.ID, &j.SourceURL, &j.SourceUser, &j.Status,
		&j.MigrateRepos, &j.MigrateUsers, &j.MigrateBlobs, &j.MigratePolicies,
		&j.TotalRepos, &j.DoneRepos, &j.TotalAssets, &j.DoneAssets,
		&j.TotalBytes, &j.DoneBytes, &j.ErrorCount, &j.LastError,
		&j.StartedAt, &j.FinishedAt, &j.CreatedAt, &j.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (r *MigrationRepo) List(ctx context.Context) ([]domain.MigrationJob, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+migrationCols+` FROM migration_jobs ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.MigrationJob
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (r *MigrationRepo) Get(ctx context.Context, id string) (*domain.MigrationJob, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+migrationCols+` FROM migration_jobs WHERE id = $1`, id)
	j, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("migration job not found: %s", id)
	}
	return j, err
}

func (r *MigrationRepo) Create(ctx context.Context, job *domain.MigrationJob) error {
	return r.pool.QueryRow(ctx, `
		INSERT INTO migration_jobs
			(source_url, source_user, migrate_repos, migrate_users, migrate_blobs, migrate_policies)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`,
		job.SourceURL, job.SourceUser,
		job.MigrateRepos, job.MigrateUsers, job.MigrateBlobs, job.MigratePolicies,
	).Scan(&job.ID, &job.CreatedAt, &job.UpdatedAt)
}

func (r *MigrationRepo) UpdateStatus(ctx context.Context, id string, status domain.MigrationJobStatus) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE migration_jobs SET status = $1, updated_at = NOW() WHERE id = $2`,
		status, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("migration job not found: %s", id)
	}
	return nil
}

func (r *MigrationRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM migration_jobs WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("migration job not found: %s", id)
	}
	return nil
}
