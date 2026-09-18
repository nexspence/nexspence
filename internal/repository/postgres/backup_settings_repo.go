package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// BackupSettingsRepo is a postgres-backed implementation of
// repository.BackupSettingsRepo, backed by the singleton backup_settings row.
type BackupSettingsRepo struct{ pool *pgxpool.Pool }

// NewBackupSettingsRepo returns a postgres-backed BackupSettingsRepo.
func NewBackupSettingsRepo(pool *pgxpool.Pool) *BackupSettingsRepo {
	return &BackupSettingsRepo{pool: pool}
}

// Get returns the singleton row, or column defaults (Enabled=false) if it has
// never been written — an INSERT-on-first-write table, not seeded by a migration.
func (r *BackupSettingsRepo) Get(ctx context.Context) (*domain.BackupSettings, error) {
	var s domain.BackupSettings
	var blobStoreID *string
	err := r.pool.QueryRow(ctx, `
		SELECT enabled, schedule_cron, blob_store_id, retention_count,
		       last_run_at, COALESCE(last_run_key,''), COALESCE(last_run_error,''), updated_at
		FROM backup_settings WHERE id='default'`).
		Scan(&s.Enabled, &s.ScheduleCron, &blobStoreID, &s.RetentionCount,
			&s.LastRunAt, &s.LastRunKey, &s.LastRunError, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// No row yet: the column defaults from the migration (schedule_cron
		// '0 3 * * *', retention_count 7, enabled false) describe an instance
		// that has never had scheduled backup configured.
		return &domain.BackupSettings{ScheduleCron: "0 3 * * *", RetentionCount: 7}, nil
	}
	if err != nil {
		return nil, err
	}
	if blobStoreID != nil {
		s.BlobStoreID = *blobStoreID
	}
	return &s, nil
}

// Upsert creates or replaces the singleton row's editable fields. LastRun*
// columns are untouched — those are written only through RecordRun, so
// saving the settings form never wipes run history.
func (r *BackupSettingsRepo) Upsert(ctx context.Context, s *domain.BackupSettings) error {
	var blobStoreID *string
	if s.BlobStoreID != "" {
		blobStoreID = &s.BlobStoreID
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO backup_settings (id, enabled, schedule_cron, blob_store_id, retention_count, updated_at)
		VALUES ('default', $1, $2, $3, $4, now())
		ON CONFLICT (id) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			schedule_cron = EXCLUDED.schedule_cron,
			blob_store_id = EXCLUDED.blob_store_id,
			retention_count = EXCLUDED.retention_count,
			updated_at = now()`,
		s.Enabled, s.ScheduleCron, blobStoreID, s.RetentionCount)
	return err
}

// RecordRun persists the outcome of one scheduled run. runErr empty means success.
func (r *BackupSettingsRepo) RecordRun(ctx context.Context, at time.Time, key, runErr string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO backup_settings (id, last_run_at, last_run_key, last_run_error)
		VALUES ('default', $1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET
			last_run_at = EXCLUDED.last_run_at,
			last_run_key = EXCLUDED.last_run_key,
			last_run_error = EXCLUDED.last_run_error`,
		at, key, runErr)
	return err
}
