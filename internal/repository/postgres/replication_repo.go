package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

type replicationRepo struct {
	db *pgxpool.Pool
}

func NewReplicationRepo(db *pgxpool.Pool) *replicationRepo {
	return &replicationRepo{db: db}
}

const ruleColumns = `id, name, source_repo, target_url, target_repo, target_username,
	target_password_enc, cron_expr, enabled, last_run_at, last_run_status, created_at`

func scanRule(row pgx.Row) (*domain.ReplicationRule, error) {
	var r domain.ReplicationRule
	err := row.Scan(
		&r.ID, &r.Name, &r.SourceRepo, &r.TargetURL, &r.TargetRepo,
		&r.TargetUsername, &r.TargetPasswordEnc, &r.CronExpr,
		&r.Enabled, &r.LastRunAt, &r.LastRunStatus, &r.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (r *replicationRepo) ListRules(ctx context.Context) ([]domain.ReplicationRule, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+ruleColumns+` FROM replication_rules ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.ReplicationRule, 0)
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rule)
	}
	return out, rows.Err()
}

func (r *replicationRepo) GetRule(ctx context.Context, id string) (*domain.ReplicationRule, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+ruleColumns+` FROM replication_rules WHERE id = $1`, id)
	rule, err := scanRule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return rule, err
}

func (r *replicationRepo) CreateRule(ctx context.Context, rule *domain.ReplicationRule) error {
	return r.db.QueryRow(ctx,
		`INSERT INTO replication_rules
			(name, source_repo, target_url, target_repo, target_username, target_password_enc, cron_expr, enabled)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 RETURNING id, created_at`,
		rule.Name, rule.SourceRepo, rule.TargetURL, rule.TargetRepo,
		rule.TargetUsername, rule.TargetPasswordEnc, rule.CronExpr, rule.Enabled,
	).Scan(&rule.ID, &rule.CreatedAt)
}

func (r *replicationRepo) UpdateRule(ctx context.Context, rule *domain.ReplicationRule) error {
	_, err := r.db.Exec(ctx,
		`UPDATE replication_rules
		 SET name=$1, source_repo=$2, target_url=$3, target_repo=$4,
		     target_username=$5, target_password_enc=$6, cron_expr=$7, enabled=$8
		 WHERE id=$9`,
		rule.Name, rule.SourceRepo, rule.TargetURL, rule.TargetRepo,
		rule.TargetUsername, rule.TargetPasswordEnc, rule.CronExpr, rule.Enabled, rule.ID,
	)
	return err
}

func (r *replicationRepo) DeleteRule(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM replication_rules WHERE id = $1`, id)
	return err
}

func (r *replicationRepo) UpdateRuleStatus(ctx context.Context, id, status string, at time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE replication_rules SET last_run_status=$1, last_run_at=$2 WHERE id=$3`,
		status, at, id,
	)
	return err
}

func (r *replicationRepo) AddHistory(ctx context.Context, h *domain.ReplicationHistory) error {
	return r.db.QueryRow(ctx,
		`INSERT INTO replication_history
			(rule_id, started_at, finished_at, duration_ms, pushed_count, skipped_count, failed_count, transferred_bytes, error)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 RETURNING id`,
		h.RuleID, h.StartedAt, h.FinishedAt, h.DurationMs,
		h.PushedCount, h.SkippedCount, h.FailedCount, h.TransferredBytes, h.Error,
	).Scan(&h.ID)
}

func (r *replicationRepo) ListHistory(ctx context.Context, ruleID string, limit int) ([]domain.ReplicationHistory, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, rule_id, started_at, finished_at, duration_ms,
		        pushed_count, skipped_count, failed_count, transferred_bytes, error
		 FROM replication_history WHERE rule_id=$1 ORDER BY started_at DESC LIMIT $2`,
		ruleID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]domain.ReplicationHistory, 0)
	for rows.Next() {
		var h domain.ReplicationHistory
		if err := rows.Scan(&h.ID, &h.RuleID, &h.StartedAt, &h.FinishedAt, &h.DurationMs,
			&h.PushedCount, &h.SkippedCount, &h.FailedCount, &h.TransferredBytes, &h.Error); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
