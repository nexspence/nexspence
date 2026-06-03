package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/repository"
)

type blobRefRepo struct{ pool *pgxpool.Pool }

// NewBlobRefRepo returns a repository.BlobRefRepo backed by the global_blobs table.
func NewBlobRefRepo(pool *pgxpool.Pool) repository.BlobRefRepo {
	return &blobRefRepo{pool: pool}
}

func (r *blobRefRepo) Increment(ctx context.Context, blobKey string, sizeBytes int64) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO global_blobs (blob_key, size_bytes, ref_count)
		VALUES ($1, $2, 1)
		ON CONFLICT (blob_key) DO UPDATE
		    SET ref_count = global_blobs.ref_count + 1
	`, blobKey, sizeBytes)
	return err
}

func (r *blobRefRepo) Decrement(ctx context.Context, blobKey string) (bool, error) {
	var newCount int
	err := r.pool.QueryRow(ctx, `
		UPDATE global_blobs
		SET ref_count = ref_count - 1
		WHERE blob_key = $1
		RETURNING ref_count
	`, blobKey).Scan(&newCount)

	if errors.Is(err, pgx.ErrNoRows) {
		// Key was never tracked (old path-based blob) — caller should delete it.
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if newCount <= 0 {
		_, _ = r.pool.Exec(ctx, `DELETE FROM global_blobs WHERE blob_key = $1`, blobKey)
		return true, nil
	}
	return false, nil
}

func (r *blobRefRepo) Get(ctx context.Context, blobKey string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `
		SELECT ref_count FROM global_blobs WHERE blob_key = $1
	`, blobKey).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return count, err
}
