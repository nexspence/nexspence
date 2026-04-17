package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexspence-oss/nexspence/internal/domain"
)

type assetRepo struct {
	db *pgxpool.Pool
}

func NewAssetRepo(db *pgxpool.Pool) *assetRepo {
	return &assetRepo{db: db}
}

const assetSelectCols = `
	a.id, a.component_id, a.repository_id, rep.name,
	a.path, a.blob_store_id, a.blob_key,
	a.size_bytes, a.content_type,
	a.sha1, a.sha256, a.md5,
	a.last_modified, a.last_downloaded, a.download_count, a.created_at`

const assetFromJoin = `FROM assets a JOIN repositories rep ON rep.id = a.repository_id`

func (r *assetRepo) List(ctx context.Context, repoName string, limit, offset int) (*domain.Page[domain.Asset], error) {
	q := fmt.Sprintf(`SELECT %s %s WHERE rep.name = $1 ORDER BY a.path LIMIT $2 OFFSET $3`,
		assetSelectCols, assetFromJoin)

	rows, err := r.db.Query(ctx, q, repoName, limit+1, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []domain.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var token *string
	if len(items) > limit {
		items = items[:limit]
		next := strconv.Itoa(offset + limit)
		token = &next
	}
	return &domain.Page[domain.Asset]{Items: items, ContinuationToken: token}, nil
}

func (r *assetRepo) Get(ctx context.Context, id string) (*domain.Asset, error) {
	q := fmt.Sprintf(`SELECT %s %s WHERE a.id = $1`, assetSelectCols, assetFromJoin)
	row := r.db.QueryRow(ctx, q, id)
	a, err := scanAsset(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

func (r *assetRepo) GetByPath(ctx context.Context, repoName, path string) (*domain.Asset, error) {
	q := fmt.Sprintf(`SELECT %s %s WHERE rep.name = $1 AND a.path = $2`, assetSelectCols, assetFromJoin)
	row := r.db.QueryRow(ctx, q, repoName, path)
	a, err := scanAsset(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

func (r *assetRepo) SearchAssets(ctx context.Context, p domain.SearchParams) (*domain.Page[domain.Asset], error) {
	args := []any{}
	i := 1
	where := "WHERE 1=1"

	if p.Repository != "" {
		where += fmt.Sprintf(" AND rep.name = $%d", i)
		args = append(args, p.Repository)
		i++
	}
	if p.Format != "" {
		where += fmt.Sprintf(" AND a.content_type ILIKE $%d", i)
		args = append(args, "%"+p.Format+"%")
		i++
	}
	if p.Name != "" {
		where += fmt.Sprintf(" AND a.path ILIKE $%d", i)
		args = append(args, "%"+p.Name+"%")
		i++
	}
	if p.SHA256 != "" {
		where += fmt.Sprintf(" AND a.sha256 = $%d", i)
		args = append(args, p.SHA256)
		i++
	}

	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	q := fmt.Sprintf(`SELECT %s %s %s ORDER BY a.path LIMIT $%d OFFSET $%d`,
		assetSelectCols, assetFromJoin, where, i, i+1)
	args = append(args, limit+1, p.Offset)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []domain.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var token *string
	if len(items) > limit {
		items = items[:limit]
		next := strconv.Itoa(p.Offset + limit)
		token = &next
	}
	return &domain.Page[domain.Asset]{Items: items, ContinuationToken: token}, nil
}

func (r *assetRepo) ListStale(ctx context.Context, format string, lastDownloadedDays, artifactAgeDays, limit int) ([]domain.Asset, error) {
	if limit <= 0 {
		limit = 500
	}
	args := []any{}
	i := 1
	where := "WHERE 1=1"

	if format != "" && format != "*" {
		where += fmt.Sprintf(" AND comp.format = $%d", i)
		args = append(args, format)
		i++
	}
	if lastDownloadedDays > 0 {
		where += fmt.Sprintf(" AND (a.last_downloaded IS NULL OR a.last_downloaded < NOW() - INTERVAL '1 day' * $%d)", i)
		args = append(args, lastDownloadedDays)
		i++
	}
	if artifactAgeDays > 0 {
		where += fmt.Sprintf(" AND a.created_at < NOW() - INTERVAL '1 day' * $%d", i)
		args = append(args, artifactAgeDays)
		i++
	}
	args = append(args, limit)

	q := fmt.Sprintf(`
		SELECT %s %s
		JOIN components comp ON comp.id = a.component_id
		%s
		ORDER BY a.created_at ASC
		LIMIT $%d`, assetSelectCols, assetFromJoin, where, i)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (r *assetRepo) Create(ctx context.Context, a *domain.Asset) error {
	return r.db.QueryRow(ctx, `
		INSERT INTO assets
		  (component_id, repository_id, path, blob_store_id, blob_key,
		   size_bytes, content_type, sha1, sha256, md5, uploader_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (repository_id, path) DO UPDATE SET
		  blob_key     = EXCLUDED.blob_key,
		  size_bytes   = EXCLUDED.size_bytes,
		  content_type = EXCLUDED.content_type,
		  sha1         = EXCLUDED.sha1,
		  sha256       = EXCLUDED.sha256,
		  md5          = EXCLUDED.md5,
		  last_modified = NOW()
		RETURNING id, created_at`,
		a.ComponentID, a.RepositoryID, a.Path, a.BlobStoreID, a.BlobKey,
		a.SizeBytes, a.ContentType, nullStr(a.SHA1), nullStr(a.SHA256), nullStr(a.MD5),
		nullStr(a.DownloadURL), // reusing DownloadURL field as uploader placeholder — pass "" or actual UUID
	).Scan(&a.ID, &a.CreatedAt)
}

func (r *assetRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM assets WHERE id = $1`, id)
	return err
}

func (r *assetRepo) IncrementDownload(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `
		UPDATE assets SET
		  download_count = download_count + 1,
		  last_downloaded = NOW()
		WHERE id = $1`, id)
	return err
}

func scanAsset(row scanner) (*domain.Asset, error) {
	var a domain.Asset
	err := row.Scan(
		&a.ID, &a.ComponentID, &a.RepositoryID, &a.Repository,
		&a.Path, &a.BlobStoreID, &a.BlobKey,
		&a.SizeBytes, &a.ContentType,
		&a.SHA1, &a.SHA256, &a.MD5,
		&a.LastModified, &a.LastDownloaded, &a.DownloadCount, &a.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
