package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

type blobStoreRepo struct {
	db *pgxpool.Pool
}

// NewBlobStoreRepo returns a postgres-backed BlobStoreRepo.
func NewBlobStoreRepo(db *pgxpool.Pool) *blobStoreRepo {
	return &blobStoreRepo{db: db}
}

func (r *blobStoreRepo) List(ctx context.Context) ([]domain.BlobStore, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, name, type, config, quota_bytes, used_bytes, created_at, updated_at
		FROM blob_stores ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stores []domain.BlobStore
	for rows.Next() {
		bs, err := scanBlobStore(rows)
		if err != nil {
			return nil, err
		}
		stores = append(stores, *bs)
	}
	return stores, rows.Err()
}

func (r *blobStoreRepo) Get(ctx context.Context, name string) (*domain.BlobStore, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, name, type, config, quota_bytes, used_bytes, created_at, updated_at
		FROM blob_stores WHERE name = $1`, name)
	bs, err := scanBlobStore(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	return bs, err
}

func (r *blobStoreRepo) GetByID(ctx context.Context, id string) (*domain.BlobStore, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, name, type, config, quota_bytes, used_bytes, created_at, updated_at
		FROM blob_stores WHERE id = $1`, id)
	bs, err := scanBlobStore(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	return bs, err
}

func (r *blobStoreRepo) Create(ctx context.Context, b *domain.BlobStore) error {
	cfg, _ := json.Marshal(b.Config)
	return r.db.QueryRow(ctx, `
		INSERT INTO blob_stores (name, type, config, quota_bytes)
		VALUES ($1,$2,$3,$4)
		RETURNING id, created_at, updated_at`,
		b.Name, b.Type, cfg, b.QuotaBytes,
	).Scan(&b.ID, &b.CreatedAt, &b.UpdatedAt)
}

func (r *blobStoreRepo) Update(ctx context.Context, b *domain.BlobStore) error {
	cfg, _ := json.Marshal(b.Config)
	_, err := r.db.Exec(ctx, `
		UPDATE blob_stores SET config=$1, quota_bytes=$2, updated_at=NOW()
		WHERE name=$3`,
		cfg, b.QuotaBytes, b.Name,
	)
	return err
}

func (r *blobStoreRepo) Delete(ctx context.Context, name string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM blob_stores WHERE name=$1`, name)
	return err
}

func (r *blobStoreRepo) UpdateUsedBytes(ctx context.Context, name string, delta int64) error {
	_, err := r.db.Exec(ctx, `
		UPDATE blob_stores SET used_bytes = GREATEST(0, used_bytes + $1) WHERE name=$2`,
		delta, name,
	)
	return err
}

func scanBlobStore(row scanner) (*domain.BlobStore, error) {
	var bs domain.BlobStore
	var cfgRaw []byte
	err := row.Scan(&bs.ID, &bs.Name, &bs.Type, &cfgRaw,
		&bs.QuotaBytes, &bs.UsedBytes, &bs.CreatedAt, &bs.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(cfgRaw, &bs.Config)
	return &bs, nil
}
