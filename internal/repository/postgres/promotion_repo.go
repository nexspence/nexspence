package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

type promotionRepo struct {
	db *pgxpool.Pool
}

func NewPromotionRepo(db *pgxpool.Pool) *promotionRepo {
	return &promotionRepo{db: db}
}

const promotionRuleFields = `id, name, from_repo, to_repo, path_filter,
	require_scan_pass, require_manual_approval, created_at, updated_at`

func scanPromotionRule(row pgx.Row) (*domain.PromotionRule, error) {
	var r domain.PromotionRule
	var pf *string
	err := row.Scan(
		&r.ID, &r.Name, &r.FromRepo, &r.ToRepo, &pf,
		&r.RequireScanPass, &r.RequireManualApproval, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if pf != nil {
		r.PathFilter = *pf
	}
	return &r, nil
}

func (r *promotionRepo) ListRules(ctx context.Context) ([]domain.PromotionRule, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+promotionRuleFields+` FROM promotion_rules ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.PromotionRule
	for rows.Next() {
		rule, err := scanPromotionRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rule)
	}
	return out, rows.Err()
}

func (r *promotionRepo) GetRule(ctx context.Context, id string) (*domain.PromotionRule, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+promotionRuleFields+` FROM promotion_rules WHERE id = $1`, id)
	rule, err := scanPromotionRule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return rule, err
}

func (r *promotionRepo) ListRulesByFromRepo(ctx context.Context, fromRepo string) ([]domain.PromotionRule, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+promotionRuleFields+` FROM promotion_rules WHERE from_repo = $1 ORDER BY name`, fromRepo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.PromotionRule
	for rows.Next() {
		rule, err := scanPromotionRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rule)
	}
	return out, rows.Err()
}

func (r *promotionRepo) CreateRule(ctx context.Context, rule *domain.PromotionRule) error {
	var pf *string
	if rule.PathFilter != "" {
		pf = &rule.PathFilter
	}
	return r.db.QueryRow(ctx,
		`INSERT INTO promotion_rules
		  (name, from_repo, to_repo, path_filter, require_scan_pass, require_manual_approval)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING id, created_at, updated_at`,
		rule.Name, rule.FromRepo, rule.ToRepo, pf,
		rule.RequireScanPass, rule.RequireManualApproval,
	).Scan(&rule.ID, &rule.CreatedAt, &rule.UpdatedAt)
}

func (r *promotionRepo) UpdateRule(ctx context.Context, rule *domain.PromotionRule) error {
	var pf *string
	if rule.PathFilter != "" {
		pf = &rule.PathFilter
	}
	_, err := r.db.Exec(ctx,
		`UPDATE promotion_rules
		 SET name=$1, from_repo=$2, to_repo=$3, path_filter=$4,
		     require_scan_pass=$5, require_manual_approval=$6, updated_at=now()
		 WHERE id=$7`,
		rule.Name, rule.FromRepo, rule.ToRepo, pf,
		rule.RequireScanPass, rule.RequireManualApproval, rule.ID,
	)
	return err
}

func (r *promotionRepo) DeleteRule(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM promotion_rules WHERE id=$1`, id)
	return err
}

const promotionReqFields = `id, rule_id, component_id, status, requested_by,
	reviewed_by, reviewed_at, completed_at, error, created_at`

func scanPromotionRequest(row pgx.Row) (*domain.PromotionRequest, error) {
	var req domain.PromotionRequest
	var status string
	var errMsg *string
	err := row.Scan(
		&req.ID, &req.RuleID, &req.ComponentID, &status, &req.RequestedBy,
		&req.ReviewedBy, &req.ReviewedAt, &req.CompletedAt, &errMsg, &req.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	req.Status = domain.PromotionStatus(status)
	if errMsg != nil {
		req.Error = *errMsg
	}
	return &req, nil
}

func (r *promotionRepo) CreateRequest(ctx context.Context, req *domain.PromotionRequest) error {
	return r.db.QueryRow(ctx,
		`INSERT INTO promotion_requests (rule_id, component_id, status, requested_by)
		 VALUES ($1,$2,$3,$4)
		 RETURNING id, created_at`,
		req.RuleID, req.ComponentID, string(req.Status), req.RequestedBy,
	).Scan(&req.ID, &req.CreatedAt)
}

func (r *promotionRepo) GetRequest(ctx context.Context, id string) (*domain.PromotionRequest, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+promotionReqFields+` FROM promotion_requests WHERE id=$1`, id)
	req, err := scanPromotionRequest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return req, err
}

func (r *promotionRepo) ListRequests(ctx context.Context, status string) ([]domain.PromotionRequest, error) {
	query := `SELECT ` + promotionReqFields + ` FROM promotion_requests`
	args := []any{}
	if status != "" {
		query += ` WHERE status=$1`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.PromotionRequest
	for rows.Next() {
		req, err := scanPromotionRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *req)
	}
	return out, rows.Err()
}

func (r *promotionRepo) UpdateRequestStatus(
	ctx context.Context, id string, status domain.PromotionStatus,
	reviewedBy *string, reviewedAt, completedAt *time.Time, errMsg string,
) error {
	var em *string
	if errMsg != "" {
		em = &errMsg
	}
	_, err := r.db.Exec(ctx,
		`UPDATE promotion_requests
		 SET status=$1, reviewed_by=$2, reviewed_at=$3, completed_at=$4, error=$5
		 WHERE id=$6`,
		string(status), reviewedBy, reviewedAt, completedAt, em, id,
	)
	return err
}
