package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

type CleanupPolicyRepo struct{ pool *pgxpool.Pool }

func NewCleanupPolicyRepo(pool *pgxpool.Pool) *CleanupPolicyRepo {
	return &CleanupPolicyRepo{pool: pool}
}

func (r *CleanupPolicyRepo) List(ctx context.Context) ([]domain.CleanupPolicy, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, description, format, criteria, COALESCE(schedule_cron,''),
		       enabled, dry_run, COALESCE(retain_n_versions,0),
		       last_run_at, COALESCE(last_run_freed,0), COALESCE(last_run_count,0),
		       created_at, updated_at, COALESCE(scope,'{}')
		FROM cleanup_policies ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.CleanupPolicy
	for rows.Next() {
		var p domain.CleanupPolicy
		var criteriaJSON []byte
		var scopeJSON []byte
		if err := rows.Scan(
			&p.ID, &p.Name, &p.Description, &p.Format, &criteriaJSON,
			&p.ScheduleCron, &p.Enabled, &p.DryRun, &p.RetainNVersions,
			&p.LastRunAt, &p.LastRunFreed, &p.LastRunCount,
			&p.CreatedAt, &p.UpdatedAt, &scopeJSON,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(criteriaJSON, &p.Criteria)
		_ = json.Unmarshal(scopeJSON, &p.Scope)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *CleanupPolicyRepo) Get(ctx context.Context, id string) (*domain.CleanupPolicy, error) {
	var p domain.CleanupPolicy
	var criteriaJSON []byte
	var scopeJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, description, format, criteria, COALESCE(schedule_cron,''),
		       enabled, dry_run, COALESCE(retain_n_versions,0),
		       last_run_at, COALESCE(last_run_freed,0), COALESCE(last_run_count,0),
		       created_at, updated_at, COALESCE(scope,'{}')
		FROM cleanup_policies WHERE id=$1`, id).
		Scan(&p.ID, &p.Name, &p.Description, &p.Format, &criteriaJSON,
			&p.ScheduleCron, &p.Enabled, &p.DryRun, &p.RetainNVersions,
			&p.LastRunAt, &p.LastRunFreed, &p.LastRunCount,
			&p.CreatedAt, &p.UpdatedAt, &scopeJSON)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(criteriaJSON, &p.Criteria)
	_ = json.Unmarshal(scopeJSON, &p.Scope)
	return &p, nil
}

func (r *CleanupPolicyRepo) Create(ctx context.Context, p *domain.CleanupPolicy) error {
	criteriaJSON, _ := json.Marshal(p.Criteria)
	scopeJSON, _ := json.Marshal(p.Scope)
	return r.pool.QueryRow(ctx, `
		INSERT INTO cleanup_policies (name, description, format, criteria, schedule_cron, enabled, dry_run, retain_n_versions, scope)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, created_at, updated_at`,
		p.Name, p.Description, p.Format, criteriaJSON,
		p.ScheduleCron, p.Enabled, p.DryRun, p.RetainNVersions, scopeJSON,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

func (r *CleanupPolicyRepo) Update(ctx context.Context, p *domain.CleanupPolicy) error {
	criteriaJSON, _ := json.Marshal(p.Criteria)
	scopeJSON, _ := json.Marshal(p.Scope)
	tag, err := r.pool.Exec(ctx, `
		UPDATE cleanup_policies
		SET name=$1, description=$2, format=$3, criteria=$4,
		    schedule_cron=$5, enabled=$6, dry_run=$7, retain_n_versions=$8, scope=$9, updated_at=NOW()
		WHERE id=$10`,
		p.Name, p.Description, p.Format, criteriaJSON,
		p.ScheduleCron, p.Enabled, p.DryRun, p.RetainNVersions, scopeJSON, p.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("cleanup policy not found: %s", p.ID)
	}
	return nil
}

func (r *CleanupPolicyRepo) Delete(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM cleanup_policies WHERE id=$1`, id)
	return err
}
