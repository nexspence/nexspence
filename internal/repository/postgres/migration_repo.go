package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// MigrationRepo is a postgres-backed implementation of repository.MigrationRepo.
type MigrationRepo struct{ pool *pgxpool.Pool }

// NewMigrationRepo returns a postgres-backed MigrationRepo.
func NewMigrationRepo(pool *pgxpool.Pool) *MigrationRepo {
	return &MigrationRepo{pool: pool}
}

const migrationCols = `id, source_url, source_user, source_password, status,
	migrate_repos, migrate_users, migrate_blobs, migrate_policies,
	migrate_privileges, migrate_roles, migrate_routing_rules, user_realms,
	total_repos, done_repos, total_assets, done_assets,
	total_bytes, done_bytes, error_count, last_error,
	started_at, finished_at, created_at, updated_at`

// userRealmsValue maps a nil slice onto the empty array the NOT NULL column
// expects; nil means "not specified", which the runner reads as local-only.
func userRealmsValue(realms []string) []string {
	if realms == nil {
		return []string{}
	}
	return realms
}

func scanJob(row pgx.Row) (*domain.MigrationJob, error) {
	var j domain.MigrationJob
	err := row.Scan(
		&j.ID, &j.SourceURL, &j.SourceUser, &j.SourcePassword, &j.Status,
		&j.MigrateRepos, &j.MigrateUsers, &j.MigrateBlobs, &j.MigratePolicies,
		&j.MigratePrivileges, &j.MigrateRoles, &j.MigrateRoutingRules, &j.UserRealms,
		&j.TotalRepos, &j.DoneRepos, &j.TotalAssets, &j.DoneAssets,
		&j.TotalBytes, &j.DoneBytes, &j.ErrorCount, &j.LastError,
		&j.StartedAt, &j.FinishedAt, &j.CreatedAt, &j.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// List returns all migration jobs ordered newest first.
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

// Get returns the migration job with the given id.
func (r *MigrationRepo) Get(ctx context.Context, id string) (*domain.MigrationJob, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+migrationCols+` FROM migration_jobs WHERE id = $1`, id)
	j, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("migration job not found: %s", id)
	}
	return j, err
}

// Create inserts a new migration job and populates its generated fields.
func (r *MigrationRepo) Create(ctx context.Context, job *domain.MigrationJob) error {
	status := job.Status
	if status == "" {
		status = domain.MigrationPending
	}
	return r.pool.QueryRow(ctx, `
		INSERT INTO migration_jobs
			(source_url, source_user, source_password, status,
			 migrate_repos, migrate_users, migrate_blobs, migrate_policies,
			 migrate_privileges, migrate_roles, migrate_routing_rules, user_realms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, created_at, updated_at`,
		job.SourceURL, job.SourceUser, job.SourcePassword, status,
		job.MigrateRepos, job.MigrateUsers, job.MigrateBlobs, job.MigratePolicies,
		job.MigratePrivileges, job.MigrateRoles, job.MigrateRoutingRules, userRealmsValue(job.UserRealms),
	).Scan(&job.ID, &job.CreatedAt, &job.UpdatedAt)
}

// ListActive returns pending and running jobs, oldest first, so a restart
// re-attaches to them in the order they were created.
func (r *MigrationRepo) ListActive(ctx context.Context) ([]domain.MigrationJob, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+migrationCols+` FROM migration_jobs
		 WHERE status IN ('pending', 'running') ORDER BY created_at`)
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

// SetSourcePassword stores the sealed Nexus credential for the job.
func (r *MigrationRepo) SetSourcePassword(ctx context.Context, id, sealed string) error {
	return r.exec(ctx, id,
		`UPDATE migration_jobs SET source_password = $1, updated_at = NOW() WHERE id = $2`,
		sealed, id)
}

// SetStarted marks the job running and stamps started_at the first time only,
// so a resumed job still reports when the transfer originally began.
func (r *MigrationRepo) SetStarted(ctx context.Context, id string, at time.Time) error {
	return r.exec(ctx, id, `
		UPDATE migration_jobs
		SET status = 'running',
		    started_at = COALESCE(started_at, $1),
		    finished_at = NULL,
		    updated_at = NOW()
		WHERE id = $2`, at, id)
}

// SetTotals records how much work the run discovered.
func (r *MigrationRepo) SetTotals(ctx context.Context, id string, totalRepos int, totalAssets int64) error {
	return r.exec(ctx, id, `
		UPDATE migration_jobs SET total_repos = $1, total_assets = $2, updated_at = NOW()
		WHERE id = $3`, totalRepos, totalAssets, id)
}

// UpdateProgress records completed work along with the running error tally.
func (r *MigrationRepo) UpdateProgress(ctx context.Context, id string, doneRepos int, doneAssets int64,
	errorCount int, lastError *string,
) error {
	return r.exec(ctx, id, `
		UPDATE migration_jobs
		SET done_repos = $1, done_assets = $2, error_count = $3,
		    last_error = COALESCE($4, last_error), updated_at = NOW()
		WHERE id = $5`, doneRepos, doneAssets, errorCount, lastError, id)
}

// FinishJob stamps finished_at and sets the terminal status.
func (r *MigrationRepo) FinishJob(ctx context.Context, id string,
	status domain.MigrationJobStatus, errMsg *string,
) error {
	return r.exec(ctx, id, `
		UPDATE migration_jobs
		SET status = $1, last_error = COALESCE($2, last_error),
		    finished_at = NOW(), updated_at = NOW()
		WHERE id = $3`, status, errMsg, id)
}

// exec runs a single-row update and reports a missing job as a not-found error.
func (r *MigrationRepo) exec(ctx context.Context, id, sql string, args ...any) error {
	tag, err := r.pool.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("migration job not found: %s", id)
	}
	return nil
}

// UpdateStatus sets the status of the migration job with the given id.
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

// Delete removes the migration job with the given id.
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
