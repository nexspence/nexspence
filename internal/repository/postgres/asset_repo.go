package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

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
	a.last_modified, a.last_downloaded, a.download_count, a.created_at,
	a.uploader_id, u.username`

const assetFromJoin = `FROM assets a
	JOIN repositories rep ON rep.id = a.repository_id
	LEFT JOIN users u ON u.id = a.uploader_id`

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

func (r *assetRepo) ListByComponentID(ctx context.Context, componentID string) ([]domain.Asset, error) {
	q := fmt.Sprintf(`SELECT %s %s WHERE a.component_id = $1 ORDER BY a.path`, assetSelectCols, assetFromJoin)
	rows, err := r.db.Query(ctx, q, componentID)
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

func (r *assetRepo) SearchAssets(ctx context.Context, p domain.SearchParams) (*domain.Page[domain.Asset], error) {
	args := []any{}
	i := 1
	where := "WHERE 1=1"

	if len(p.RepositoryNames) > 0 {
		ph := make([]string, len(p.RepositoryNames))
		for j := range p.RepositoryNames {
			ph[j] = fmt.Sprintf("$%d", i)
			args = append(args, p.RepositoryNames[j])
			i++
		}
		where += " AND rep.name IN (" + strings.Join(ph, ",") + ")"
	} else if p.Repository != "" {
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

func (r *assetRepo) ListStale(ctx context.Context, format string, repoNames []string, lastDownloadedDays, artifactAgeDays int, pathPrefix, nameGlob string, limit int) ([]domain.Asset, error) {
	if limit <= 0 {
		limit = 500
	}
	args := []any{}
	i := 1
	where := "WHERE 1=1"

	if len(repoNames) > 0 {
		where += fmt.Sprintf(" AND rep.name = ANY($%d::text[])", i)
		args = append(args, repoNames)
		i++
	}

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
	if pathPrefix != "" {
		escaped := strings.ReplaceAll(strings.ReplaceAll(pathPrefix, `\`, `\\`), "%", `\%`)
		escaped = strings.ReplaceAll(escaped, "_", `\_`)
		where += fmt.Sprintf(` AND a.path LIKE $%d ESCAPE '\'`, i)
		args = append(args, escaped+"%")
		i++
	}
	if nameGlob != "" {
		like := globToLike(nameGlob)
		where += fmt.Sprintf(" AND a.path LIKE $%d", i)
		args = append(args, like)
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
		  uploader_id  = COALESCE(EXCLUDED.uploader_id, assets.uploader_id),
		  last_modified = NOW()
		RETURNING id, created_at`,
		a.ComponentID, a.RepositoryID, a.Path, a.BlobStoreID, a.BlobKey,
		a.SizeBytes, a.ContentType, nullStr(a.SHA1), nullStr(a.SHA256), nullStr(a.MD5),
		nullStr(a.UploaderID),
	).Scan(&a.ID, &a.CreatedAt)
}

func (r *assetRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM assets WHERE id = $1`, id)
	return err
}

func (r *assetRepo) ListAllBlobKeys(ctx context.Context) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT DISTINCT blob_key FROM assets WHERE blob_key IS NOT NULL AND TRIM(blob_key) <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (r *assetRepo) SumSizeByRepo(ctx context.Context, repoName string) (int64, error) {
	row := r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(a.size_bytes), 0)
		FROM assets a
		JOIN repositories rep ON rep.id = a.repository_id
		WHERE rep.name = $1`, repoName)
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (r *assetRepo) IncrementDownload(ctx context.Context, id string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE assets SET
		  download_count = download_count + 1,
		  last_downloaded = NOW()
		WHERE id = $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE components c
		SET last_downloaded = NOW(),
		    download_count = c.download_count + 1
		FROM assets a
		WHERE c.id = a.component_id AND a.id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanAsset(row scanner) (*domain.Asset, error) {
	var a domain.Asset
	var uploaderID sql.NullString
	var uploaderName sql.NullString
	err := row.Scan(
		&a.ID, &a.ComponentID, &a.RepositoryID, &a.Repository,
		&a.Path, &a.BlobStoreID, &a.BlobKey,
		&a.SizeBytes, &a.ContentType,
		&a.SHA1, &a.SHA256, &a.MD5,
		&a.LastModified, &a.LastDownloaded, &a.DownloadCount, &a.CreatedAt,
		&uploaderID, &uploaderName,
	)
	if err != nil {
		return nil, err
	}
	if uploaderID.Valid {
		a.UploaderID = uploaderID.String
	}
	if uploaderName.Valid {
		a.UploaderUsername = uploaderName.String
	}
	return &a, nil
}

func globToLike(glob string) string {
	var b strings.Builder
	for _, c := range glob {
		switch c {
		case '%', '_':
			b.WriteRune('\\')
			b.WriteRune(c)
		case '*':
			b.WriteByte('%')
		case '?':
			b.WriteByte('_')
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ListPathsByRepo returns unique directory-level path prefixes derived from
// asset paths in the given repository. q is an optional case-insensitive
// substring filter applied after prefix extraction.
func (r *assetRepo) ListPathsByRepo(ctx context.Context, repoName, q string) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT DISTINCT a.path
		 FROM assets a
		 JOIN repositories rep ON rep.id = a.repository_id
		 WHERE rep.name = $1
		 ORDER BY a.path
		 LIMIT 5000`,
		repoName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		// extract all directory prefixes: /da/devops/foo.jar → /da/, /da/devops/
		for {
			idx := strings.LastIndex(p, "/")
			if idx <= 0 {
				break
			}
			p = p[:idx+1]
			if q == "" || strings.Contains(strings.ToLower(p), strings.ToLower(q)) {
				seen[p] = struct{}{}
			}
			p = p[:idx]
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (r *assetRepo) ListRawAssetPaths(ctx context.Context, repoName string) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT DISTINCT a.path
		 FROM assets a
		 JOIN repositories rep ON rep.id = a.repository_id
		 WHERE rep.name = $1
		 ORDER BY a.path
		 LIMIT 5000`,
		repoName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *assetRepo) ListRawBrowseAssets(ctx context.Context, repoNames []string) ([]domain.RawBrowseAsset, error) {
	rows, err := r.db.Query(ctx,
		`SELECT a.path, a.size_bytes, COALESCE(a.sha256, ''), COALESCE(a.content_type, ''),
		        a.updated_at, COALESCE(a.component_id::text, ''), rep.name
		 FROM assets a
		 JOIN components c ON c.id = a.component_id
		 JOIN repositories rep ON rep.id = c.repository_id
		 WHERE rep.name = ANY($1)
		   AND lower(trim(rep.format)) = 'raw'
		 ORDER BY a.path`,
		repoNames,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RawBrowseAsset
	for rows.Next() {
		var a domain.RawBrowseAsset
		if err := rows.Scan(&a.Path, &a.SizeBytes, &a.SHA256, &a.ContentType, &a.UpdatedAt, &a.ComponentID, &a.RepoName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *assetRepo) ListByRepoAndPath(ctx context.Context, repoName, pathPrefix string) ([]domain.Asset, error) {
	var q string
	var args []any
	if pathPrefix == "" {
		q = fmt.Sprintf(`SELECT %s %s WHERE rep.name = $1 ORDER BY a.path`,
			assetSelectCols, assetFromJoin)
		args = []any{repoName}
	} else {
		q = fmt.Sprintf(`SELECT %s %s WHERE rep.name = $1 AND a.path LIKE $2 ORDER BY a.path`,
			assetSelectCols, assetFromJoin)
		args = []any{repoName, pathPrefix + "%"}
	}
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

func (r *assetRepo) CountByBlobKey(ctx context.Context, blobKey, excludeID string) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM assets WHERE blob_key = $1 AND id != $2`,
		blobKey, excludeID,
	).Scan(&count)
	return count, err
}
