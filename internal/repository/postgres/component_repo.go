package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexspence-oss/nexspence/internal/domain"
)

type componentRepo struct {
	db *pgxpool.Pool
}

func NewComponentRepo(db *pgxpool.Pool) *componentRepo {
	return &componentRepo{db: db}
}

func (r *componentRepo) List(ctx context.Context, repoName string, limit, offset int) (*domain.Page[domain.Component], error) {
	const q = `
		SELECT c.id, c.repository_id, rep.name, c.format,
		       c.group_id, c.name, c.version,
		       c.extra, c.last_downloaded, c.download_count, c.created_at
		FROM components c
		JOIN repositories rep ON rep.id = c.repository_id
		WHERE rep.name = $1
		ORDER BY c.name, c.version
		LIMIT $2 OFFSET $3`

	rows, err := r.db.Query(ctx, q, repoName, limit+1, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []domain.Component
	for rows.Next() {
		c, err := scanComponent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *c)
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
	return &domain.Page[domain.Component]{Items: items, ContinuationToken: token}, nil
}

func (r *componentRepo) Get(ctx context.Context, id string) (*domain.Component, error) {
	row := r.db.QueryRow(ctx, `
		SELECT c.id, c.repository_id, rep.name, c.format,
		       c.group_id, c.name, c.version,
		       c.extra, c.last_downloaded, c.download_count, c.created_at
		FROM components c
		JOIN repositories rep ON rep.id = c.repository_id
		WHERE c.id = $1`, id)
	c, err := scanComponent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (r *componentRepo) Search(ctx context.Context, p domain.SearchParams) (*domain.Page[domain.Component], error) {
	args := []any{}
	i := 1
	where := "WHERE 1=1"

	if p.Repository != "" {
		where += fmt.Sprintf(" AND rep.name = $%d", i)
		args = append(args, p.Repository)
		i++
	}
	if p.Format != "" {
		where += fmt.Sprintf(" AND c.format = $%d", i)
		args = append(args, p.Format)
		i++
	}
	if p.Group != "" {
		where += fmt.Sprintf(" AND c.group_id ILIKE $%d", i)
		args = append(args, "%"+p.Group+"%")
		i++
	}
	if p.Name != "" {
		where += fmt.Sprintf(" AND c.name ILIKE $%d", i)
		args = append(args, "%"+p.Name+"%")
		i++
	}
	if p.Version != "" {
		where += fmt.Sprintf(" AND c.version = $%d", i)
		args = append(args, p.Version)
		i++
	}
	if p.MavenGroupID != "" {
		where += fmt.Sprintf(" AND c.group_id = $%d", i)
		args = append(args, p.MavenGroupID)
		i++
	}
	if p.MavenArtifactID != "" {
		where += fmt.Sprintf(" AND c.name = $%d", i)
		args = append(args, p.MavenArtifactID)
		i++
	}

	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	offset := p.Offset

	q := fmt.Sprintf(`
		SELECT c.id, c.repository_id, rep.name, c.format,
		       c.group_id, c.name, c.version,
		       c.extra, c.last_downloaded, c.download_count, c.created_at
		FROM components c
		JOIN repositories rep ON rep.id = c.repository_id
		%s
		ORDER BY c.name, c.version
		LIMIT $%d OFFSET $%d`, where, i, i+1)
	args = append(args, limit+1, offset)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []domain.Component
	for rows.Next() {
		c, err := scanComponent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *c)
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
	return &domain.Page[domain.Component]{Items: items, ContinuationToken: token}, nil
}

func (r *componentRepo) Create(ctx context.Context, c *domain.Component) error {
	extra, _ := json.Marshal(c.Extra)
	return r.db.QueryRow(ctx, `
		INSERT INTO components
		  (repository_id, format, group_id, name, version, extra)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (repository_id, format, group_id, name, version) DO UPDATE
		  SET extra = EXCLUDED.extra, updated_at = NOW()
		RETURNING id, created_at`,
		c.RepositoryID, c.Format, c.Group, c.Name, c.Version, extra,
	).Scan(&c.ID, &c.CreatedAt)
}

func (r *componentRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM components WHERE id = $1`, id)
	return err
}

func scanComponent(row scanner) (*domain.Component, error) {
	var c domain.Component
	var extraRaw []byte
	err := row.Scan(
		&c.ID, &c.RepositoryID, &c.Repository, &c.Format,
		&c.Group, &c.Name, &c.Version,
		&extraRaw, &c.LastDownloaded, &c.DownloadCount, &c.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(extraRaw, &c.Extra)
	return &c, nil
}
