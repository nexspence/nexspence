package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

type componentRepo struct {
	db *pgxpool.Pool
}

// NewComponentRepo returns a postgres-backed ComponentRepo.
func NewComponentRepo(db *pgxpool.Pool) *componentRepo {
	return &componentRepo{db: db}
}

func (r *componentRepo) List(ctx context.Context, repoName string, limit, offset int) (*domain.Page[domain.Component], error) {
	return r.ListByRepoNames(ctx, []string{repoName}, limit, offset)
}

func (r *componentRepo) ListByRepoNames(ctx context.Context, repoNames []string, limit, offset int) (*domain.Page[domain.Component], error) {
	if len(repoNames) == 0 {
		return &domain.Page[domain.Component]{Items: []domain.Component{}}, nil
	}
	ph := make([]string, len(repoNames))
	args := make([]any, 0, len(repoNames)+2)
	for i, n := range repoNames {
		ph[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, n)
	}
	lim := len(args) + 1
	off := len(args) + 2
	args = append(args, limit+1, offset)
	q := fmt.Sprintf(`
		SELECT c.id, c.repository_id, rep.name, c.format,
		       c.group_id, c.name, c.version, c.tags,
		       c.extra, c.last_downloaded, c.download_count, c.created_at
		FROM components c
		JOIN repositories rep ON rep.id = c.repository_id
		WHERE rep.name IN (%s)
		ORDER BY c.name, c.version
		LIMIT $%d OFFSET $%d`, strings.Join(ph, ","), lim, off)

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

func (r *componentRepo) Get(ctx context.Context, id string) (*domain.Component, error) {
	row := r.db.QueryRow(ctx, `
		SELECT c.id, c.repository_id, rep.name, c.format,
		       c.group_id, c.name, c.version, c.tags,
		       c.extra, c.last_downloaded, c.download_count, c.created_at
		FROM components c
		JOIN repositories rep ON rep.id = c.repository_id
		WHERE c.id = $1`, id)
	c, err := scanComponent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	return c, err
}

func (r *componentRepo) Search(ctx context.Context, p domain.SearchParams) (*domain.Page[domain.Component], error) {
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
	if p.Tag != "" {
		where += fmt.Sprintf(" AND $%d = ANY(c.tags)", i)
		args = append(args, p.Tag)
		i++
	}

	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	offset := p.Offset

	q := fmt.Sprintf(`
		SELECT c.id, c.repository_id, rep.name, c.format,
		       c.group_id, c.name, c.version, c.tags,
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
	extraMap := c.Extra
	if extraMap == nil {
		extraMap = map[string]any{}
	}
	extra, _ := json.Marshal(extraMap)
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

func (r *componentRepo) UpdateExtra(ctx context.Context, id string, extra map[string]any) error {
	b, err := json.Marshal(extra)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx,
		`UPDATE components SET extra = COALESCE(extra, '{}'::jsonb) || $2::jsonb, updated_at = NOW() WHERE id = $1`,
		id, b,
	)
	return err
}

func (r *componentRepo) SetTags(ctx context.Context, id string, tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	_, err := r.db.Exec(ctx,
		`UPDATE components SET tags = $2, updated_at = NOW() WHERE id = $1`,
		id, tags,
	)
	return err
}

func (r *componentRepo) ListDockerBrowseRows(ctx context.Context, repoNames []string, maxRows int) ([]domain.DockerBrowseRow, error) {
	if len(repoNames) == 0 {
		return nil, nil
	}
	if maxRows <= 0 || maxRows > 5000 {
		maxRows = 3000
	}
	ph := make([]string, len(repoNames))
	args := make([]any, 0, len(repoNames)+1)
	for i, n := range repoNames {
		ph[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, n)
	}
	lim := len(args) + 1
	args = append(args, maxRows)
	// oci_artifact_type is grouped, not aggregated: it belongs to the component,
	// and c.id — the components primary key — is already in the GROUP BY, so one
	// value per group is all there is. Components pushed before that metadata
	// existed have no such key and COALESCE reports them as carrying no type.
	q := fmt.Sprintf(`
		SELECT c.id, c.name, c.version, COALESCE(MIN(a.path), '') AS sample_path,
		       COALESCE(c.extra->>'oci_artifact_type', '') AS artifact_type
		FROM components c
		JOIN repositories rep ON rep.id = c.repository_id
		LEFT JOIN assets a ON a.component_id = c.id
		WHERE rep.name IN (%s) AND lower(trim(rep.format)) IN ('docker', 'oci')
		GROUP BY c.id, c.name, c.version, c.extra
		ORDER BY c.name, c.version
		LIMIT $%d`, strings.Join(ph, ","), lim)
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.DockerBrowseRow
	for rows.Next() {
		var row domain.DockerBrowseRow
		if err := rows.Scan(&row.ComponentID, &row.ImageName, &row.Version, &row.SamplePath, &row.ArtifactType); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ListOCIReferrers returns components whose stored manifest metadata names
// subjectDigest as its subject. imageName restricts the search to one
// repository path (the "name" segment of /v2/{name}/...); empty means any.
func (r *componentRepo) ListOCIReferrers(ctx context.Context, repoNames []string, imageName, subjectDigest string) ([]domain.Component, error) {
	if len(repoNames) == 0 {
		return nil, nil
	}
	ph := make([]string, len(repoNames))
	args := make([]any, 0, len(repoNames)+2)
	for i, n := range repoNames {
		ph[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, n)
	}
	digestPos := len(args) + 1
	args = append(args, subjectDigest)
	q := fmt.Sprintf(`
		SELECT c.id, c.repository_id, rep.name, c.format,
		       c.group_id, c.name, c.version, c.tags,
		       c.extra, c.last_downloaded, c.download_count, c.created_at
		FROM components c
		JOIN repositories rep ON rep.id = c.repository_id
		WHERE rep.name IN (%s) AND c.extra->>'oci_subject' = $%d`, strings.Join(ph, ","), digestPos)
	if imageName != "" {
		namePos := len(args) + 1
		args = append(args, imageName)
		q += fmt.Sprintf(" AND c.name = $%d", namePos)
	}
	q += " ORDER BY c.name, c.version"

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
	return items, rows.Err()
}

func (r *componentRepo) DeleteOrphans(ctx context.Context, repoName string) error {
	_, err := r.db.Exec(ctx, `
		DELETE FROM components c
		WHERE c.id IN (
			SELECT c2.id
			FROM components c2
			JOIN repositories rep ON rep.id = c2.repository_id
			WHERE rep.name = $1
			  AND NOT EXISTS (
				  SELECT 1 FROM assets a WHERE a.component_id = c2.id
			  )
		)`, repoName)
	return err
}

func scanComponent(row scanner) (*domain.Component, error) {
	var c domain.Component
	var extraRaw []byte
	err := row.Scan(
		&c.ID, &c.RepositoryID, &c.Repository, &c.Format,
		&c.Group, &c.Name, &c.Version, &c.Tags,
		&extraRaw, &c.LastDownloaded, &c.DownloadCount, &c.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if c.Tags == nil {
		c.Tags = []string{}
	}
	_ = json.Unmarshal(extraRaw, &c.Extra)
	return &c, nil
}
