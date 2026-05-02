package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexspence-oss/nexspence/internal/domain"
)

type blobStoreMigrationRepo struct {
	db *pgxpool.Pool
}

func NewBlobStoreMigrationRepo(db *pgxpool.Pool) *blobStoreMigrationRepo {
	return &blobStoreMigrationRepo{db: db}
}

func (r *blobStoreMigrationRepo) Create(ctx context.Context, m *domain.BlobStoreMigration) error {
	var sourceID *string
	if m.SourceStoreID != "" {
		sourceID = &m.SourceStoreID
	}
	return r.db.QueryRow(ctx, `
		INSERT INTO blob_store_migrations
		  (repository_name, source_store_id, target_store_id, status)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at`,
		m.RepositoryName, sourceID, m.TargetStoreID, m.Status,
	).Scan(&m.ID, &m.CreatedAt, &m.UpdatedAt)
}

func (r *blobStoreMigrationRepo) Get(ctx context.Context, id string) (*domain.BlobStoreMigration, error) {
	row := r.db.QueryRow(ctx, `SELECT `+blobStoreMigrationCols+` FROM blob_store_migrations WHERE id = $1`, id)
	m, err := scanMigration(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

func (r *blobStoreMigrationRepo) GetActiveByRepo(ctx context.Context, repoName string) (*domain.BlobStoreMigration, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+blobStoreMigrationCols+` FROM blob_store_migrations
		WHERE repository_name = $1 AND status IN ('pending','running')
		ORDER BY created_at DESC LIMIT 1`, repoName)
	m, err := scanMigration(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

func (r *blobStoreMigrationRepo) GetLatestByRepo(ctx context.Context, repoName string) (*domain.BlobStoreMigration, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+blobStoreMigrationCols+` FROM blob_store_migrations
		WHERE repository_name = $1
		ORDER BY created_at DESC LIMIT 1`, repoName)
	m, err := scanMigration(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

func (r *blobStoreMigrationRepo) SetTotals(ctx context.Context, id string, totalAssets int, totalBytes int64) error {
	_, err := r.db.Exec(ctx, `
		UPDATE blob_store_migrations
		SET total_assets=$1, total_bytes=$2, updated_at=NOW()
		WHERE id=$3`, totalAssets, totalBytes, id)
	return err
}

func (r *blobStoreMigrationRepo) UpdateProgress(ctx context.Context, id string, doneAssets int, doneBytes int64) error {
	_, err := r.db.Exec(ctx, `
		UPDATE blob_store_migrations
		SET done_assets=$1, done_bytes=$2, updated_at=NOW()
		WHERE id=$3`, doneAssets, doneBytes, id)
	return err
}

func (r *blobStoreMigrationRepo) UpdateStatus(ctx context.Context, id string, status string, errMsg *string) error {
	now := time.Now()
	var startedAt *time.Time
	if status == "running" {
		startedAt = &now
	}
	_, err := r.db.Exec(ctx, `
		UPDATE blob_store_migrations
		SET status=$1, error_message=$2, started_at=COALESCE(started_at,$3), updated_at=NOW()
		WHERE id=$4`, status, errMsg, startedAt, id)
	return err
}

func (r *blobStoreMigrationRepo) FinishMigration(ctx context.Context, id string, status string, errMsg *string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE blob_store_migrations
		SET status=$1, error_message=$2, finished_at=NOW(), updated_at=NOW()
		WHERE id=$3`, status, errMsg, id)
	return err
}

func (r *blobStoreMigrationRepo) ListActive(ctx context.Context) ([]domain.BlobStoreMigration, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+blobStoreMigrationCols+` FROM blob_store_migrations
		WHERE status IN ('pending','running')
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ms []domain.BlobStoreMigration
	for rows.Next() {
		m, err := scanMigration(rows)
		if err != nil {
			return nil, err
		}
		ms = append(ms, *m)
	}
	return ms, rows.Err()
}

const blobStoreMigrationCols = `
	id, repository_name,
	COALESCE(source_store_id::text,'') as source_store_id,
	target_store_id::text,
	status, total_assets, done_assets, total_bytes, done_bytes,
	error_message, started_at, finished_at, created_at, updated_at`

func scanMigration(row scanner) (*domain.BlobStoreMigration, error) {
	var m domain.BlobStoreMigration
	err := row.Scan(
		&m.ID, &m.RepositoryName, &m.SourceStoreID, &m.TargetStoreID,
		&m.Status, &m.TotalAssets, &m.DoneAssets, &m.TotalBytes, &m.DoneBytes,
		&m.ErrorMessage, &m.StartedAt, &m.FinishedAt, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}
