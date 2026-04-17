package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexspence-oss/nexspence/internal/domain"
)

type repositoryRepo struct {
	db *pgxpool.Pool
}

func NewRepositoryRepo(db *pgxpool.Pool) *repositoryRepo {
	return &repositoryRepo{db: db}
}

func (r *repositoryRepo) List(ctx context.Context, format, repoType string) ([]domain.Repository, error) {
	query := `SELECT id, name, format, type, blob_store_id, online,
	                 format_config, http_config, proxy_config, cleanup_policy_ids,
	                 quota_bytes, routing_rule_id, description, created_at, updated_at
	          FROM repositories WHERE 1=1`
	args := []any{}
	i := 1

	if format != "" {
		query += fmt.Sprintf(" AND format = $%d", i)
		args = append(args, format)
		i++
	}
	if repoType != "" {
		query += fmt.Sprintf(" AND type = $%d", i)
		args = append(args, repoType)
		i++
	}
	query += " ORDER BY name"

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []domain.Repository
	for rows.Next() {
		repo, err := scanRepository(rows)
		if err != nil {
			return nil, err
		}
		repos = append(repos, *repo)
	}
	return repos, rows.Err()
}

func (r *repositoryRepo) Get(ctx context.Context, name string) (*domain.Repository, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, name, format, type, blob_store_id, online,
		       format_config, http_config, proxy_config, cleanup_policy_ids,
		       quota_bytes, routing_rule_id, description, created_at, updated_at
		FROM repositories WHERE name = $1`, name)
	repo, err := scanRepository(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return repo, err
}

func (r *repositoryRepo) GetByID(ctx context.Context, id string) (*domain.Repository, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, name, format, type, blob_store_id, online,
		       format_config, http_config, proxy_config, cleanup_policy_ids,
		       quota_bytes, routing_rule_id, description, created_at, updated_at
		FROM repositories WHERE id = $1`, id)
	repo, err := scanRepository(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return repo, err
}

func (r *repositoryRepo) Create(ctx context.Context, repo *domain.Repository) error {
	fmtCfg, _ := json.Marshal(repo.FormatConfig)
	httpCfg, _ := json.Marshal(repo.HTTPConfig)
	proxyCfg, _ := json.Marshal(repo.ProxyConfig)

	return r.db.QueryRow(ctx, `
		INSERT INTO repositories
		  (name, format, type, blob_store_id, online, format_config, http_config,
		   proxy_config, cleanup_policy_ids, quota_bytes, routing_rule_id, description)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id, created_at, updated_at`,
		repo.Name, repo.Format, repo.Type, repo.BlobStoreID, repo.Online,
		fmtCfg, httpCfg, proxyCfg,
		policyIDsToStrings(repo.CleanupPolicyIDs),
		repo.QuotaBytes, repo.RoutingRuleID, repo.Description,
	).Scan(&repo.ID, &repo.CreatedAt, &repo.UpdatedAt)
}

func (r *repositoryRepo) Update(ctx context.Context, repo *domain.Repository) error {
	fmtCfg, _ := json.Marshal(repo.FormatConfig)
	httpCfg, _ := json.Marshal(repo.HTTPConfig)
	proxyCfg, _ := json.Marshal(repo.ProxyConfig)

	_, err := r.db.Exec(ctx, `
		UPDATE repositories SET
		  online=$1, format_config=$2, http_config=$3, proxy_config=$4,
		  cleanup_policy_ids=$5, quota_bytes=$6, routing_rule_id=$7,
		  description=$8, updated_at=NOW()
		WHERE name=$9`,
		repo.Online, fmtCfg, httpCfg, proxyCfg,
		policyIDsToStrings(repo.CleanupPolicyIDs),
		repo.QuotaBytes, repo.RoutingRuleID, repo.Description,
		repo.Name,
	)
	return err
}

func (r *repositoryRepo) Delete(ctx context.Context, name string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM repositories WHERE name = $1`, name)
	return err
}

// ── Helpers ──────────────────────────────────────────────────

type scanner interface {
	Scan(dest ...any) error
}

func scanRepository(row scanner) (*domain.Repository, error) {
	var repo domain.Repository
	var fmtCfgRaw, httpCfgRaw, proxyCfgRaw []byte
	var cleanupIDs []string
	var updatedAt time.Time

	err := row.Scan(
		&repo.ID, &repo.Name, &repo.Format, &repo.Type,
		&repo.BlobStoreID, &repo.Online,
		&fmtCfgRaw, &httpCfgRaw, &proxyCfgRaw,
		&cleanupIDs,
		&repo.QuotaBytes, &repo.RoutingRuleID, &repo.Description,
		&repo.CreatedAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	repo.UpdatedAt = updatedAt
	repo.CleanupPolicyIDs = cleanupIDs

	_ = json.Unmarshal(fmtCfgRaw, &repo.FormatConfig)
	_ = json.Unmarshal(httpCfgRaw, &repo.HTTPConfig)
	_ = json.Unmarshal(proxyCfgRaw, &repo.ProxyConfig)

	return &repo, nil
}

func policyIDsToStrings(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}
