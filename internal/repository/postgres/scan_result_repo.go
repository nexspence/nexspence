package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexspence-oss/nexspence/internal/domain"
)

type scanResultRepo struct{ pool *pgxpool.Pool }

func NewScanResultRepo(pool *pgxpool.Pool) *scanResultRepo {
	return &scanResultRepo{pool: pool}
}

func (r *scanResultRepo) Insert(ctx context.Context, row *domain.ScanResultRow) error {
	raw, _ := json.Marshal(row.Raw)
	_, err := r.pool.Exec(ctx, `
		INSERT INTO scan_results
		  (component_id, scanner, status, critical, high, medium, low, unknown, total, scanned_at, raw, error)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		row.ComponentID, row.Scanner, string(row.Status),
		row.Critical, row.High, row.Medium, row.Low, row.Unknown, row.Total,
		row.ScannedAt, raw, row.Error,
	)
	return err
}

func (r *scanResultRepo) GetLatestByComponent(ctx context.Context, componentID string) (*domain.ScanResultRow, error) {
	var (
		row domain.ScanResultRow
		raw []byte
		st  string
	)
	err := r.pool.QueryRow(ctx, `
		SELECT id, component_id, scanner, status, critical, high, medium, low, unknown, total, scanned_at, raw, COALESCE(error,'')
		FROM scan_results
		WHERE component_id = $1
		ORDER BY scanned_at DESC
		LIMIT 1`, componentID).Scan(
		&row.ID, &row.ComponentID, &row.Scanner, &st,
		&row.Critical, &row.High, &row.Medium, &row.Low, &row.Unknown, &row.Total,
		&row.ScannedAt, &raw, &row.Error,
	)
	if err != nil {
		return nil, err
	}
	row.Status = domain.ScanStatus(st)
	if raw != nil {
		_ = json.Unmarshal(raw, &row.Raw)
	}
	return &row, nil
}

func (r *scanResultRepo) Aggregate(ctx context.Context) (*domain.SecuritySummary, error) {
	var s domain.SecuritySummary
	err := r.pool.QueryRow(ctx, `
		WITH latest AS (
			SELECT DISTINCT ON (component_id)
				component_id, critical, high, medium, low, unknown
			FROM scan_results
			ORDER BY component_id, scanned_at DESC
		)
		SELECT
			COALESCE(SUM(critical),0),
			COALESCE(SUM(high),0),
			COALESCE(SUM(medium),0),
			COALESCE(SUM(low),0),
			COALESCE(SUM(unknown),0),
			COUNT(*)
		FROM latest`).Scan(
		&s.Critical, &s.High, &s.Medium, &s.Low, &s.Unknown, &s.ScannedTotal,
	)
	return &s, err
}

func (r *scanResultRepo) List(ctx context.Context, f domain.VulnFilter) ([]*domain.VulnRow, int, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}

	// severity → minimum column filter
	sevFilter := ""
	switch f.Severity {
	case "CRITICAL":
		sevFilter = "AND sr.critical > 0"
	case "HIGH":
		sevFilter = "AND (sr.critical > 0 OR sr.high > 0)"
	case "MEDIUM":
		sevFilter = "AND (sr.critical > 0 OR sr.high > 0 OR sr.medium > 0)"
	case "LOW":
		sevFilter = "AND (sr.critical > 0 OR sr.high > 0 OR sr.medium > 0 OR sr.low > 0)"
	}

	base := `
		FROM (
			SELECT DISTINCT ON (sr.component_id)
				r.name AS repo_name, r.format,
				c.id AS component_id, c.name, c.version,
				sr.critical, sr.high, sr.medium, sr.low, sr.unknown, sr.scanned_at
			FROM scan_results sr
			JOIN components c ON c.id = sr.component_id
			JOIN repositories r ON r.id = c.repository_id
			WHERE ($1 = '' OR r.name = $1)
			  AND ($2 = '' OR r.format = $2)
			  ` + sevFilter + `
			ORDER BY sr.component_id, sr.scanned_at DESC
		) t`

	var total int
	if err := r.pool.QueryRow(ctx, "SELECT COUNT(*) "+base, f.Repo, f.Format).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.pool.Query(ctx,
		"SELECT repo_name, format, component_id, name, version, critical, high, medium, low, unknown, scanned_at"+
			base+" ORDER BY critical DESC, high DESC, scanned_at DESC LIMIT $3 OFFSET $4",
		f.Repo, f.Format, f.Limit, f.Offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []*domain.VulnRow
	for rows.Next() {
		var vr domain.VulnRow
		var scannedAt time.Time
		if err := rows.Scan(&vr.RepoName, &vr.Format, &vr.ComponentID, &vr.Name, &vr.Version,
			&vr.Critical, &vr.High, &vr.Medium, &vr.Low, &vr.Unknown, &scannedAt); err != nil {
			return nil, 0, err
		}
		vr.ScannedAt = scannedAt
		out = append(out, &vr)
	}
	return out, total, rows.Err()
}
