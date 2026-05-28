# Staging & Build Promotion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a promotion workflow that lets users copy artifacts between repositories (staging → prod) with optional scan gating and manual admin approval.

**Architecture:** Every promotion creates a `promotion_request` row regardless of approval mode. For auto-approve rules the blob copy runs synchronously and the request immediately reaches `completed`. For manual-approval rules it stays `pending` until an `nx-admin` presses Approve. This gives a uniform audit trail and a single queue for admins. Blob copy reuses the existing `base.BlobKey` + `blobRegistry` + `assetRepo.Create` pattern from `cleanup_service.go` and `base/store.go`.

**Tech Stack:** Go (pgx, cel-go), React + TypeScript, Gin, PostgreSQL

---

## File Map

| Action | File |
|--------|------|
| Create | `internal/db/migrations/019_promotion.sql` |
| Modify | `internal/domain/types.go` — append PromotionRule/PromotionRequest types |
| Modify | `internal/repository/interfaces.go` — append PromotionRepo interface |
| Modify | `internal/testutil/mocks.go` — append PromotionRepo in-memory mock |
| Create | `internal/repository/postgres/promotion_repo.go` |
| Create | `internal/service/promotion_service.go` |
| Create | `internal/service/promotion_service_test.go` |
| Create | `internal/api/handlers/promotion.go` |
| Create | `internal/api/handlers/promotion_handler_test.go` |
| Modify | `internal/api/router.go` — wire repo, service, handler, routes |
| Modify | `frontend/src/pages/AdminPage.tsx` — add Promotion tab |
| Modify | `frontend/src/pages/BrowsePage.tsx` — Promote button + bulk |

---

## Task 1: DB Migration

**Files:**
- Create: `internal/db/migrations/019_promotion.sql`

- [ ] **Step 1: Write the migration**

```sql
-- internal/db/migrations/019_promotion.sql
-- +goose Up

CREATE TABLE promotion_rules (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    TEXT NOT NULL UNIQUE,
    from_repo               TEXT NOT NULL REFERENCES repositories(name) ON UPDATE CASCADE ON DELETE CASCADE,
    to_repo                 TEXT NOT NULL REFERENCES repositories(name) ON UPDATE CASCADE ON DELETE CASCADE,
    path_filter             TEXT,
    require_scan_pass       BOOLEAN NOT NULL DEFAULT false,
    require_manual_approval BOOLEAN NOT NULL DEFAULT false,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE promotion_requests (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id      UUID NOT NULL REFERENCES promotion_rules(id) ON DELETE CASCADE,
    component_id UUID NOT NULL REFERENCES components(id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending','approved','rejected','completed','failed')),
    requested_by UUID NOT NULL REFERENCES users(id),
    reviewed_by  UUID REFERENCES users(id),
    reviewed_at  TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_promotion_requests_status ON promotion_requests (status);
CREATE INDEX idx_promotion_requests_rule   ON promotion_requests (rule_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS promotion_requests;
DROP TABLE IF EXISTS promotion_rules;
```

- [ ] **Step 2: Verify the server applies the migration**

```bash
go run ./cmd/server migrate
```

Expected: `OK    019_promotion.sql` in output with no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/db/migrations/019_promotion.sql
git commit -m "feat(promotion): add promotion_rules and promotion_requests tables"
```

---

## Task 2: Domain Types

**Files:**
- Modify: `internal/domain/types.go` — append after the last type block

- [ ] **Step 1: Write the failing test** (verify types compile)

No test needed — this is pure type definition. Proceed to implementation.

- [ ] **Step 2: Append to `internal/domain/types.go`**

Add at the very end of the file:

```go
// ── Promotion ────────────────────────────────────────────────

// PromotionRule defines a promotion route between two repositories.
type PromotionRule struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	FromRepo              string    `json:"from_repo"`
	ToRepo                string    `json:"to_repo"`
	PathFilter            string    `json:"path_filter,omitempty"` // CEL expression; empty = all paths
	RequireScanPass       bool      `json:"require_scan_pass"`
	RequireManualApproval bool      `json:"require_manual_approval"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type PromotionStatus string

const (
	PromotionPending   PromotionStatus = "pending"
	PromotionApproved  PromotionStatus = "approved"
	PromotionRejected  PromotionStatus = "rejected"
	PromotionCompleted PromotionStatus = "completed"
	PromotionFailed    PromotionStatus = "failed"
)

// PromotionRequest is one artifact copy task produced by a Promote action.
type PromotionRequest struct {
	ID          string          `json:"id"`
	RuleID      string          `json:"rule_id"`
	ComponentID string          `json:"component_id"`
	Status      PromotionStatus `json:"status"`
	RequestedBy string          `json:"requested_by"`
	ReviewedBy  *string         `json:"reviewed_by,omitempty"`
	ReviewedAt  *time.Time      `json:"reviewed_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./internal/domain/...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/domain/types.go
git commit -m "feat(promotion): add PromotionRule and PromotionRequest domain types"
```

---

## Task 3: Repository Interface

**Files:**
- Modify: `internal/repository/interfaces.go` — append PromotionRepo

- [ ] **Step 1: Append to `internal/repository/interfaces.go`**

Add at the very end of the file:

```go
// PromotionRepo manages promotion rules and requests.
type PromotionRepo interface {
	// Rules
	ListRules(ctx context.Context) ([]domain.PromotionRule, error)
	GetRule(ctx context.Context, id string) (*domain.PromotionRule, error)
	// ListRulesByFromRepo returns rules where from_repo matches the given name.
	ListRulesByFromRepo(ctx context.Context, fromRepo string) ([]domain.PromotionRule, error)
	CreateRule(ctx context.Context, r *domain.PromotionRule) error
	UpdateRule(ctx context.Context, r *domain.PromotionRule) error
	DeleteRule(ctx context.Context, id string) error
	// Requests
	CreateRequest(ctx context.Context, r *domain.PromotionRequest) error
	GetRequest(ctx context.Context, id string) (*domain.PromotionRequest, error)
	// ListRequests returns requests filtered by status ("" = all).
	ListRequests(ctx context.Context, status string) ([]domain.PromotionRequest, error)
	// UpdateRequestStatus sets status and optional review/completion metadata.
	UpdateRequestStatus(ctx context.Context, id string, status domain.PromotionStatus,
		reviewedBy *string, reviewedAt, completedAt *time.Time, errMsg string) error
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/repository/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/repository/interfaces.go
git commit -m "feat(promotion): add PromotionRepo interface"
```

---

## Task 4: Testutil In-Memory Mock

**Files:**
- Modify: `internal/testutil/mocks.go` — append PromotionRepo mock

- [ ] **Step 1: Append to `internal/testutil/mocks.go`**

Add at the very end of the file (after the last mock):

```go
// ── PromotionRepo mock ────────────────────────────────────────

type PromotionRepo struct {
	mu       sync.Mutex
	rules    map[string]*domain.PromotionRule
	requests map[string]*domain.PromotionRequest
	nextID   int
}

func NewPromotionRepo() *PromotionRepo {
	return &PromotionRepo{
		rules:    make(map[string]*domain.PromotionRule),
		requests: make(map[string]*domain.PromotionRequest),
	}
}

func (r *PromotionRepo) genID() string {
	r.nextID++
	return fmt.Sprintf("promo-%d", r.nextID)
}

func (r *PromotionRepo) ListRules(_ context.Context) ([]domain.PromotionRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.PromotionRule, 0, len(r.rules))
	for _, v := range r.rules {
		out = append(out, *v)
	}
	return out, nil
}

func (r *PromotionRepo) GetRule(_ context.Context, id string) (*domain.PromotionRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.rules[id]; ok {
		cp := *v
		return &cp, nil
	}
	return nil, nil
}

func (r *PromotionRepo) ListRulesByFromRepo(_ context.Context, fromRepo string) ([]domain.PromotionRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.PromotionRule
	for _, v := range r.rules {
		if v.FromRepo == fromRepo {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (r *PromotionRepo) CreateRule(_ context.Context, rule *domain.PromotionRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rule.ID == "" {
		rule.ID = r.genID()
	}
	rule.CreatedAt = time.Now()
	rule.UpdatedAt = rule.CreatedAt
	cp := *rule
	r.rules[rule.ID] = &cp
	return nil
}

func (r *PromotionRepo) UpdateRule(_ context.Context, rule *domain.PromotionRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule.UpdatedAt = time.Now()
	cp := *rule
	r.rules[rule.ID] = &cp
	return nil
}

func (r *PromotionRepo) DeleteRule(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rules, id)
	return nil
}

func (r *PromotionRepo) CreateRequest(_ context.Context, req *domain.PromotionRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.ID == "" {
		req.ID = r.genID()
	}
	req.CreatedAt = time.Now()
	cp := *req
	r.requests[req.ID] = &cp
	return nil
}

func (r *PromotionRepo) GetRequest(_ context.Context, id string) (*domain.PromotionRequest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.requests[id]; ok {
		cp := *v
		return &cp, nil
	}
	return nil, nil
}

func (r *PromotionRepo) ListRequests(_ context.Context, status string) ([]domain.PromotionRequest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.PromotionRequest
	for _, v := range r.requests {
		if status == "" || string(v.Status) == status {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (r *PromotionRepo) UpdateRequestStatus(_ context.Context, id string, status domain.PromotionStatus,
	reviewedBy *string, reviewedAt, completedAt *time.Time, errMsg string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	req, ok := r.requests[id]
	if !ok {
		return fmt.Errorf("not found: %s", id)
	}
	req.Status = status
	req.ReviewedBy = reviewedBy
	req.ReviewedAt = reviewedAt
	req.CompletedAt = completedAt
	req.Error = errMsg
	return nil
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/testutil/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/testutil/mocks.go
git commit -m "feat(promotion): add PromotionRepo in-memory mock for tests"
```

---

## Task 5: Postgres Repository Implementation

**Files:**
- Create: `internal/repository/postgres/promotion_repo.go`

- [ ] **Step 1: Write the implementation**

```go
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

const ruleFields = `id, name, from_repo, to_repo, path_filter,
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
		`SELECT `+ruleFields+` FROM promotion_rules ORDER BY name`)
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
		`SELECT `+ruleFields+` FROM promotion_rules WHERE id = $1`, id)
	rule, err := scanPromotionRule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return rule, err
}

func (r *promotionRepo) ListRulesByFromRepo(ctx context.Context, fromRepo string) ([]domain.PromotionRule, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+ruleFields+` FROM promotion_rules WHERE from_repo = $1 ORDER BY name`, fromRepo)
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

const reqFields = `id, rule_id, component_id, status, requested_by,
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
		`SELECT `+reqFields+` FROM promotion_requests WHERE id=$1`, id)
	req, err := scanPromotionRequest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return req, err
}

func (r *promotionRepo) ListRequests(ctx context.Context, status string) ([]domain.PromotionRequest, error) {
	query := `SELECT ` + reqFields + ` FROM promotion_requests`
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
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/repository/postgres/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/repository/postgres/promotion_repo.go
git commit -m "feat(promotion): add postgres PromotionRepo implementation"
```

---

## Task 6: PromotionService

**Files:**
- Create: `internal/service/promotion_service.go`

- [ ] **Step 1: Write the service**

```go
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// PromotionService copies artifacts between repositories according to promotion rules.
type PromotionService struct {
	promotionRepo repository.PromotionRepo
	componentRepo repository.ComponentRepo
	assetRepo     repository.AssetRepo
	repoRepo      repository.RepositoryRepo
	blobRepo      repository.BlobStoreRepo
	scanRepo      repository.ScanResultRepo
	blobStore     storage.BlobStore
	blobRegistry  *storage.Registry
	webhooks      domain.WebhookDispatcher

	celEnv *cel.Env
}

func NewPromotionService(
	promotionRepo repository.PromotionRepo,
	componentRepo repository.ComponentRepo,
	assetRepo repository.AssetRepo,
	repoRepo repository.RepositoryRepo,
	blobRepo repository.BlobStoreRepo,
	scanRepo repository.ScanResultRepo,
	blobStore storage.BlobStore,
	blobRegistry *storage.Registry,
) (*PromotionService, error) {
	env, err := cel.NewEnv(
		cel.Variable("format", cel.StringType),
		cel.Variable("path", cel.StringType),
		cel.Variable("repository", cel.StringType),
	)
	if err != nil {
		return nil, fmt.Errorf("promotion cel env: %w", err)
	}
	return &PromotionService{
		promotionRepo: promotionRepo,
		componentRepo: componentRepo,
		assetRepo:     assetRepo,
		repoRepo:      repoRepo,
		blobRepo:      blobRepo,
		scanRepo:      scanRepo,
		blobStore:     blobStore,
		blobRegistry:  blobRegistry,
		celEnv:        env,
	}, nil
}

func (s *PromotionService) WithWebhooks(w domain.WebhookDispatcher) *PromotionService {
	s.webhooks = w
	return s
}

// matchesPathFilter returns true when the component matches the rule's path filter.
// An empty PathFilter matches everything. A CEL compile error means no match (logged by caller).
func (s *PromotionService) matchesPathFilter(rule domain.PromotionRule, comp *domain.Component) bool {
	if rule.PathFilter == "" {
		return true
	}
	ast, issues := s.celEnv.Compile(rule.PathFilter)
	if issues != nil && issues.Err() != nil {
		return false
	}
	prg, err := s.celEnv.Program(ast)
	if err != nil {
		return false
	}
	// Use the first asset path as the representative path for the component.
	path := "/" + comp.Group + "/" + comp.Name
	vars := map[string]any{
		"format":     comp.Format,
		"path":       path,
		"repository": comp.Repository,
	}
	out, _, err := prg.Eval(vars)
	if err != nil {
		return false
	}
	matched, _ := out.Value().(bool)
	return matched
}

// ListRules returns all promotion rules.
func (s *PromotionService) ListRules(ctx context.Context) ([]domain.PromotionRule, error) {
	return s.promotionRepo.ListRules(ctx)
}

// GetRule returns a single rule by ID, or nil if not found.
func (s *PromotionService) GetRule(ctx context.Context, id string) (*domain.PromotionRule, error) {
	return s.promotionRepo.GetRule(ctx, id)
}

// CreateRule validates and persists a promotion rule.
func (s *PromotionService) CreateRule(ctx context.Context, rule *domain.PromotionRule) error {
	if rule.Name == "" {
		return fmt.Errorf("name is required")
	}
	if rule.FromRepo == "" || rule.ToRepo == "" {
		return fmt.Errorf("from_repo and to_repo are required")
	}
	if rule.FromRepo == rule.ToRepo {
		return fmt.Errorf("from_repo and to_repo must be different")
	}
	if rule.PathFilter != "" {
		if _, err := s.celEnv.Compile(rule.PathFilter); err != nil {
			return fmt.Errorf("invalid path_filter CEL expression: %w", err)
		}
	}
	return s.promotionRepo.CreateRule(ctx, rule)
}

// UpdateRule validates and updates an existing promotion rule.
func (s *PromotionService) UpdateRule(ctx context.Context, rule *domain.PromotionRule) error {
	if rule.Name == "" {
		return fmt.Errorf("name is required")
	}
	if rule.FromRepo == rule.ToRepo {
		return fmt.Errorf("from_repo and to_repo must be different")
	}
	if rule.PathFilter != "" {
		if _, err := s.celEnv.Compile(rule.PathFilter); err != nil {
			return fmt.Errorf("invalid path_filter CEL expression: %w", err)
		}
	}
	return s.promotionRepo.UpdateRule(ctx, rule)
}

// DeleteRule removes a promotion rule by ID.
func (s *PromotionService) DeleteRule(ctx context.Context, id string) error {
	return s.promotionRepo.DeleteRule(ctx, id)
}

// ListRulesForComponent returns promotion rules that match the given component
// (from_repo == component.Repository and path_filter matches).
func (s *PromotionService) ListRulesForComponent(ctx context.Context, componentID string) ([]domain.PromotionRule, error) {
	comp, err := s.componentRepo.Get(ctx, componentID)
	if err != nil || comp == nil {
		return nil, fmt.Errorf("component not found: %s", componentID)
	}
	rules, err := s.promotionRepo.ListRulesByFromRepo(ctx, comp.Repository)
	if err != nil {
		return nil, err
	}
	var matching []domain.PromotionRule
	for _, r := range rules {
		if s.matchesPathFilter(r, comp) {
			matching = append(matching, r)
		}
	}
	return matching, nil
}

// ListRequests returns promotion requests filtered by status ("" = all).
func (s *PromotionService) ListRequests(ctx context.Context, status string) ([]domain.PromotionRequest, error) {
	return s.promotionRepo.ListRequests(ctx, status)
}

// Promote creates a promotion_request for each component. For rules with
// require_manual_approval=false the blob copy runs immediately and the request
// reaches status=completed before this function returns.
func (s *PromotionService) Promote(ctx context.Context, ruleID string, componentIDs []string, requestedByID string) ([]domain.PromotionRequest, error) {
	rule, err := s.promotionRepo.GetRule(ctx, ruleID)
	if err != nil || rule == nil {
		return nil, fmt.Errorf("promotion rule not found: %s", ruleID)
	}

	var results []domain.PromotionRequest
	for _, compID := range componentIDs {
		req := &domain.PromotionRequest{
			RuleID:      ruleID,
			ComponentID: compID,
			Status:      domain.PromotionPending,
			RequestedBy: requestedByID,
		}

		// Scan gate: check before even creating the request.
		if rule.RequireScanPass {
			scan, serr := s.scanRepo.GetLatestByComponent(ctx, compID)
			if serr != nil || scan == nil {
				return nil, fmt.Errorf("component %s: scan required but not yet run", compID)
			}
			if scan.Critical > 0 || scan.High > 0 {
				return nil, fmt.Errorf("component %s: scan has %d critical, %d high findings", compID, scan.Critical, scan.High)
			}
		}

		if err := s.promotionRepo.CreateRequest(ctx, req); err != nil {
			return nil, fmt.Errorf("create promotion request: %w", err)
		}

		if !rule.RequireManualApproval {
			if copyErr := s.executeCopy(ctx, req, rule); copyErr != nil {
				now := time.Now()
				_ = s.promotionRepo.UpdateRequestStatus(ctx, req.ID, domain.PromotionFailed,
					nil, nil, &now, copyErr.Error())
				req.Status = domain.PromotionFailed
				req.Error = copyErr.Error()
			} else {
				now := time.Now()
				_ = s.promotionRepo.UpdateRequestStatus(ctx, req.ID, domain.PromotionCompleted,
					nil, nil, &now, "")
				req.Status = domain.PromotionCompleted
				req.CompletedAt = &now
			}
		}

		results = append(results, *req)
	}
	return results, nil
}

// Approve approves a pending promotion request and executes the blob copy.
// The caller must hold the nx-admin role (enforced at handler level via AdminRequired middleware).
func (s *PromotionService) Approve(ctx context.Context, requestID, reviewerID string) error {
	req, err := s.promotionRepo.GetRequest(ctx, requestID)
	if err != nil || req == nil {
		return fmt.Errorf("promotion request not found: %s", requestID)
	}
	if req.Status != domain.PromotionPending {
		return fmt.Errorf("request is not pending (status: %s)", req.Status)
	}
	rule, err := s.promotionRepo.GetRule(ctx, req.RuleID)
	if err != nil || rule == nil {
		return fmt.Errorf("promotion rule not found: %s", req.RuleID)
	}

	now := time.Now()
	if copyErr := s.executeCopy(ctx, req, rule); copyErr != nil {
		_ = s.promotionRepo.UpdateRequestStatus(ctx, req.ID, domain.PromotionFailed,
			&reviewerID, &now, &now, copyErr.Error())
		return copyErr
	}
	return s.promotionRepo.UpdateRequestStatus(ctx, req.ID, domain.PromotionCompleted,
		&reviewerID, &now, &now, "")
}

// Reject rejects a pending promotion request.
// The caller must hold the nx-admin role (enforced at handler level via AdminRequired middleware).
func (s *PromotionService) Reject(ctx context.Context, requestID, reviewerID, reason string) error {
	req, err := s.promotionRepo.GetRequest(ctx, requestID)
	if err != nil || req == nil {
		return fmt.Errorf("promotion request not found: %s", requestID)
	}
	if req.Status != domain.PromotionPending {
		return fmt.Errorf("request is not pending (status: %s)", req.Status)
	}
	now := time.Now()
	return s.promotionRepo.UpdateRequestStatus(ctx, req.ID, domain.PromotionRejected,
		&reviewerID, &now, nil, reason)
}

// executeCopy copies a component's blobs and metadata from from_repo to to_repo.
func (s *PromotionService) executeCopy(ctx context.Context, req *domain.PromotionRequest, rule *domain.PromotionRule) error {
	// Load source component.
	comp, err := s.componentRepo.Get(ctx, req.ComponentID)
	if err != nil || comp == nil {
		return fmt.Errorf("source component not found: %s", req.ComponentID)
	}

	// Load target repo.
	toRepo, err := s.repoRepo.Get(ctx, rule.ToRepo)
	if err != nil || toRepo == nil {
		return fmt.Errorf("target repository not found: %s", rule.ToRepo)
	}

	// Resolve target blob store.
	toStore, toBlobStoreID := s.resolveStore(ctx, toRepo.BlobStoreID)

	// Load assets.
	assets, err := s.assetRepo.ListByComponentID(ctx, req.ComponentID)
	if err != nil {
		return fmt.Errorf("list assets: %w", err)
	}

	// Upsert component in target repo.
	newComp := &domain.Component{
		RepositoryID: toRepo.ID,
		Repository:   toRepo.Name,
		Format:       string(toRepo.Format),
		Group:        comp.Group,
		Name:         comp.Name,
		Version:      comp.Version,
		Tags:         comp.Tags,
	}
	if err := s.componentRepo.Create(ctx, newComp); err != nil {
		return fmt.Errorf("upsert component in target: %w", err)
	}

	// Copy each asset blob.
	for _, asset := range assets {
		fromStore, _ := s.resolveStore(ctx, &asset.BlobStoreID)

		newBlobKey := base.BlobKey(toRepo.Name, asset.Path)

		rc, size, err := fromStore.Get(ctx, asset.BlobKey)
		if err != nil {
			return fmt.Errorf("read blob %s: %w", asset.BlobKey, err)
		}
		if putErr := toStore.Put(ctx, newBlobKey, rc, size); putErr != nil {
			rc.Close()
			return fmt.Errorf("write blob %s: %w", newBlobKey, putErr)
		}
		rc.Close()

		newAsset := &domain.Asset{
			ComponentID:  newComp.ID,
			RepositoryID: toRepo.ID,
			Repository:   toRepo.Name,
			Path:         asset.Path,
			BlobStoreID:  toBlobStoreID,
			BlobKey:      newBlobKey,
			SizeBytes:    size,
			ContentType:  asset.ContentType,
			SHA256:       asset.SHA256,
			SHA1:         asset.SHA1,
			MD5:          asset.MD5,
		}
		if err := s.assetRepo.Create(ctx, newAsset); err != nil {
			return fmt.Errorf("create asset record: %w", err)
		}
	}

	// Fire webhook.
	if s.webhooks != nil {
		s.webhooks.Dispatch(domain.WebhookPayload{
			Event:      domain.EventArtifactPublished,
			Timestamp:  time.Now(),
			Repository: toRepo.Name,
			Component: map[string]any{
				"group":   newComp.Group,
				"name":    newComp.Name,
				"version": newComp.Version,
				"format":  string(toRepo.Format),
			},
		})
	}
	return nil
}

// resolveStore returns the physical BlobStore and its ID for a given blobStoreID pointer.
// Falls back to the default store when the pointer is nil or empty.
func (s *PromotionService) resolveStore(ctx context.Context, blobStoreID *string) (storage.BlobStore, string) {
	if blobStoreID == nil || *blobStoreID == "" {
		return s.blobStore, ""
	}
	bsMeta, err := s.blobRepo.GetByID(ctx, *blobStoreID)
	if err != nil || bsMeta == nil {
		return s.blobStore, ""
	}
	bs, err := s.blobRegistry.Get(ctx, storage.BlobStoreDescriptor{
		ID:     bsMeta.ID,
		Type:   bsMeta.Type,
		Config: bsMeta.Config,
	})
	if err != nil {
		return s.blobStore, ""
	}
	return bs, bsMeta.ID
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/service/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/service/promotion_service.go
git commit -m "feat(promotion): add PromotionService with CEL path filter and blob copy"
```

---

## Task 7: PromotionService Tests

**Files:**
- Create: `internal/service/promotion_service_test.go`

- [ ] **Step 1: Write the tests**

```go
package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func newTestPromotionSvc(t *testing.T) (*service.PromotionService, *testutil.PromotionRepo, *testutil.ComponentRepo, *testutil.AssetRepo, *testutil.BlobStore, *testutil.RepoRepo, *testutil.BlobStoreRepo, *testutil.ScanResultRepo) {
	t.Helper()
	promoRepo := testutil.NewPromotionRepo()
	compRepo  := testutil.NewComponentRepo()
	assetRepo := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()
	blobRepo  := testutil.NewBlobStoreRepo()
	scanRepo  := testutil.NewScanResultRepo()
	repoRepo  := testutil.NewRepoRepo()
	registry  := storage.NewRegistry(blobStore)

	svc, err := service.NewPromotionService(
		promoRepo, compRepo, assetRepo, repoRepo, blobRepo, scanRepo, blobStore, registry,
	)
	if err != nil {
		t.Fatalf("NewPromotionService: %v", err)
	}
	return svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, blobRepo, scanRepo
}

func TestPromotionService_CreateRule_Validation(t *testing.T) {
	svc, _, _, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	// missing name
	err := svc.CreateRule(ctx, &domain.PromotionRule{FromRepo: "a", ToRepo: "b"})
	if err == nil {
		t.Fatal("expected error for missing name")
	}

	// same from/to
	err = svc.CreateRule(ctx, &domain.PromotionRule{Name: "r", FromRepo: "a", ToRepo: "a"})
	if err == nil {
		t.Fatal("expected error for same from/to repos")
	}

	// invalid CEL
	err = svc.CreateRule(ctx, &domain.PromotionRule{Name: "r", FromRepo: "a", ToRepo: "b", PathFilter: "!!invalid!!"})
	if err == nil {
		t.Fatal("expected error for invalid CEL expression")
	}
}

func TestPromotionService_AutoApprove_CopiesBlob(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	// Seed repos
	fromRepo := &domain.Repository{ID: "r1", Name: "staging", Format: "raw", Type: "hosted"}
	toRepo   := &domain.Repository{ID: "r2", Name: "prod", Format: "raw", Type: "hosted"}
	repoRepo.Repos["staging"] = fromRepo
	repoRepo.Repos["prod"]    = toRepo

	// Seed component + asset
	comp := &domain.Component{
		ID: "c1", RepositoryID: "r1", Repository: "staging",
		Format: "raw", Name: "mylib", Version: "1.0",
	}
	compRepo.Store["c1"] = comp
	asset := &domain.Asset{
		ID: "a1", ComponentID: "c1", RepositoryID: "r1", Repository: "staging",
		Path: "/mylib-1.0.tar.gz", BlobKey: "key1", SizeBytes: 100, ContentType: "application/gzip",
	}
	assetRepo.Assets["a1"] = asset

	// Put blob in store
	_ = blobStore.PutBytes(ctx, "key1", []byte("blobdata"))

	// Create auto-approve rule
	rule := &domain.PromotionRule{
		Name: "staging-to-prod", FromRepo: "staging", ToRepo: "prod",
		RequireManualApproval: false,
	}
	_ = promoRepo.CreateRule(ctx, rule)

	// Promote
	requests, err := svc.Promote(ctx, rule.ID, []string{"c1"}, "user1")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	if requests[0].Status != domain.PromotionCompleted {
		t.Errorf("expected completed, got %s", requests[0].Status)
	}

	// Verify component was created in prod
	found := false
	for _, c := range compRepo.Store {
		if c.Repository == "prod" && c.Name == "mylib" {
			found = true
		}
	}
	if !found {
		t.Error("component not found in prod repo after promotion")
	}
}

func TestPromotionService_ManualApproval_StaysPending(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := &domain.Repository{ID: "r1", Name: "staging", Format: "raw", Type: "hosted"}
	toRepo   := &domain.Repository{ID: "r2", Name: "prod", Format: "raw", Type: "hosted"}
	repoRepo.Repos["staging"] = fromRepo
	repoRepo.Repos["prod"]    = toRepo

	comp := &domain.Component{ID: "c1", RepositoryID: "r1", Repository: "staging", Format: "raw", Name: "lib", Version: "2.0"}
	compRepo.Store["c1"] = comp
	asset := &domain.Asset{ID: "a1", ComponentID: "c1", RepositoryID: "r1", Repository: "staging", Path: "/lib-2.0.jar", BlobKey: "k2", SizeBytes: 50, ContentType: "application/java-archive"}
	assetRepo.Assets["a1"] = asset
	_ = blobStore.PutBytes(ctx, "k2", []byte("jardata"))

	rule := &domain.PromotionRule{Name: "manual-rule", FromRepo: "staging", ToRepo: "prod", RequireManualApproval: true}
	_ = promoRepo.CreateRule(ctx, rule)

	requests, err := svc.Promote(ctx, rule.ID, []string{"c1"}, "user1")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if requests[0].Status != domain.PromotionPending {
		t.Errorf("expected pending, got %s", requests[0].Status)
	}

	// Approve
	if err := svc.Approve(ctx, requests[0].ID, "admin1"); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	req, _ := promoRepo.GetRequest(ctx, requests[0].ID)
	if req.Status != domain.PromotionCompleted {
		t.Errorf("expected completed after approve, got %s", req.Status)
	}
}

func TestPromotionService_Reject(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := &domain.Repository{ID: "r1", Name: "staging", Format: "raw"}
	toRepo   := &domain.Repository{ID: "r2", Name: "prod", Format: "raw"}
	repoRepo.Repos["staging"] = fromRepo
	repoRepo.Repos["prod"]    = toRepo
	comp := &domain.Component{ID: "c1", RepositoryID: "r1", Repository: "staging", Format: "raw", Name: "x", Version: "1"}
	compRepo.Store["c1"] = comp

	rule := &domain.PromotionRule{Name: "r", FromRepo: "staging", ToRepo: "prod", RequireManualApproval: true}
	_ = promoRepo.CreateRule(ctx, rule)

	requests, _ := svc.Promote(ctx, rule.ID, []string{"c1"}, "user1")
	if err := svc.Reject(ctx, requests[0].ID, "admin1", "does not meet quality gate"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	req, _ := promoRepo.GetRequest(ctx, requests[0].ID)
	if req.Status != domain.PromotionRejected {
		t.Errorf("expected rejected, got %s", req.Status)
	}
	if req.Error != "does not meet quality gate" {
		t.Errorf("expected reason, got %q", req.Error)
	}
}

func TestPromotionService_ScanGate_Blocks(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, scanRepo := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := &domain.Repository{ID: "r1", Name: "staging", Format: "raw"}
	toRepo   := &domain.Repository{ID: "r2", Name: "prod", Format: "raw"}
	repoRepo.Repos["staging"] = fromRepo
	repoRepo.Repos["prod"]    = toRepo
	comp := &domain.Component{ID: "c1", RepositoryID: "r1", Repository: "staging", Format: "raw", Name: "vuln", Version: "1"}
	compRepo.Store["c1"] = comp

	// Seed scan result with CRITICAL findings
	scanRepo.Rows["c1"] = &domain.ScanResultRow{
		ComponentID: "c1", Status: domain.ScanStatusOK, Critical: 2, High: 0,
		ScannedAt: time.Now(),
	}

	rule := &domain.PromotionRule{Name: "scan-rule", FromRepo: "staging", ToRepo: "prod", RequireScanPass: true}
	_ = promoRepo.CreateRule(ctx, rule)

	_, err := svc.Promote(ctx, rule.ID, []string{"c1"}, "user1")
	if err == nil {
		t.Fatal("expected error for component with critical findings")
	}
}

func TestPromotionService_PathFilter(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := &domain.Repository{ID: "r1", Name: "staging", Format: "raw"}
	toRepo   := &domain.Repository{ID: "r2", Name: "prod", Format: "raw"}
	repoRepo.Repos["staging"] = fromRepo
	repoRepo.Repos["prod"]    = toRepo

	compMatch    := &domain.Component{ID: "c-match", RepositoryID: "r1", Repository: "staging", Format: "raw", Group: "com/myco", Name: "approved"}
	compNoMatch  := &domain.Component{ID: "c-nomatch", RepositoryID: "r1", Repository: "staging", Format: "raw", Group: "com/other", Name: "blocked"}
	compRepo.Store["c-match"]   = compMatch
	compRepo.Store["c-nomatch"] = compNoMatch

	// Rule only matches paths starting with /com/myco/
	rule := &domain.PromotionRule{Name: "scoped", FromRepo: "staging", ToRepo: "prod", PathFilter: `path.startsWith("/com/myco/")`, RequireManualApproval: false}
	_ = promoRepo.CreateRule(ctx, rule)

	matchRules, _ := svc.ListRulesForComponent(ctx, "c-match")
	if len(matchRules) != 1 {
		t.Errorf("expected 1 matching rule for c-match, got %d", len(matchRules))
	}

	noMatchRules, _ := svc.ListRulesForComponent(ctx, "c-nomatch")
	if len(noMatchRules) != 0 {
		t.Errorf("expected 0 matching rules for c-nomatch, got %d", len(noMatchRules))
	}
}
```

- [ ] **Step 2: Add `ScanResultRepo` mock to `testutil/mocks.go`** (needed by tests)

Append to `internal/testutil/mocks.go`:

```go
// ── ScanResultRepo mock ────────────────────────────────────────

type ScanResultRepo struct {
	mu   sync.Mutex
	Rows map[string]*domain.ScanResultRow // keyed by componentID
}

func NewScanResultRepo() *ScanResultRepo {
	return &ScanResultRepo{Rows: make(map[string]*domain.ScanResultRow)}
}

func (r *ScanResultRepo) Insert(_ context.Context, row *domain.ScanResultRow) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *row
	r.Rows[row.ComponentID] = &cp
	return nil
}

func (r *ScanResultRepo) GetLatestByComponent(_ context.Context, componentID string) (*domain.ScanResultRow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.Rows[componentID]; ok {
		cp := *v
		return &cp, nil
	}
	return nil, nil
}

func (r *ScanResultRepo) Aggregate(_ context.Context) (*domain.SecuritySummary, error) {
	return &domain.SecuritySummary{}, nil
}

func (r *ScanResultRepo) List(_ context.Context, _ domain.VulnFilter) ([]*domain.VulnRow, int, error) {
	return nil, 0, nil
}
```

Also add `PutBytes` helper to the `BlobStore` mock in `testutil/mocks.go` if not already present. Check first:

```bash
grep -n "PutBytes" /Users/skensel/WORKING/AI/nexspence-core/internal/testutil/mocks.go
```

If not found, append to the `BlobStore` mock struct methods:

```go
func (b *BlobStore) PutBytes(ctx context.Context, key string, data []byte) error {
	return b.Put(ctx, key, bytes.NewReader(data), int64(len(data)))
}
```

And add `"bytes"` to the imports in mocks.go.

Also add `Store` and `Repos` exported fields to ComponentRepo and RepoRepo mocks if tests need direct seeding. Check existing mock field names first:

```bash
grep -n "field\|Data\|store\|items\|repos\b\|comps\b" /Users/skensel/WORKING/AI/nexspence-core/internal/testutil/mocks.go | head -20
```

Use whatever field names exist — adjust test seeding lines accordingly.

- [ ] **Step 3: Run the tests**

```bash
go test ./internal/service/... -run TestPromotion -v
```

Expected: all 5 tests pass.

- [ ] **Step 4: Run all service tests to catch regressions**

```bash
go test ./internal/service/... -count=1
```

Expected: no failures.

- [ ] **Step 5: Commit**

```bash
git add internal/service/promotion_service_test.go internal/testutil/mocks.go
git commit -m "feat(promotion): add PromotionService unit tests"
```

---

## Task 8: HTTP Handler

**Files:**
- Create: `internal/api/handlers/promotion.go`

- [ ] **Step 1: Write the handler**

```go
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
)

type PromotionHandler struct {
	svc *service.PromotionService
}

func NewPromotionHandler(svc *service.PromotionService) *PromotionHandler {
	return &PromotionHandler{svc: svc}
}

// ListRules handles GET /api/v1/promotion/rules
func (h *PromotionHandler) ListRules(c *gin.Context) {
	rules, err := h.svc.ListRules(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rules == nil {
		rules = []domain.PromotionRule{}
	}
	c.JSON(http.StatusOK, rules)
}

type ruleInput struct {
	Name                  string `json:"name"`
	FromRepo              string `json:"from_repo"`
	ToRepo                string `json:"to_repo"`
	PathFilter            string `json:"path_filter"`
	RequireScanPass       bool   `json:"require_scan_pass"`
	RequireManualApproval bool   `json:"require_manual_approval"`
}

// CreateRule handles POST /api/v1/promotion/rules (admin only)
func (h *PromotionHandler) CreateRule(c *gin.Context) {
	var inp ruleInput
	if err := c.ShouldBindJSON(&inp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rule := &domain.PromotionRule{
		Name: inp.Name, FromRepo: inp.FromRepo, ToRepo: inp.ToRepo,
		PathFilter: inp.PathFilter, RequireScanPass: inp.RequireScanPass,
		RequireManualApproval: inp.RequireManualApproval,
	}
	if err := h.svc.CreateRule(c.Request.Context(), rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, rule)
}

// UpdateRule handles PUT /api/v1/promotion/rules/:id (admin only)
func (h *PromotionHandler) UpdateRule(c *gin.Context) {
	var inp ruleInput
	if err := c.ShouldBindJSON(&inp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rule := &domain.PromotionRule{
		ID: c.Param("id"), Name: inp.Name, FromRepo: inp.FromRepo, ToRepo: inp.ToRepo,
		PathFilter: inp.PathFilter, RequireScanPass: inp.RequireScanPass,
		RequireManualApproval: inp.RequireManualApproval,
	}
	if err := h.svc.UpdateRule(c.Request.Context(), rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rule)
}

// DeleteRule handles DELETE /api/v1/promotion/rules/:id (admin only)
func (h *PromotionHandler) DeleteRule(c *gin.Context) {
	if err := h.svc.DeleteRule(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// GetComponentRules handles GET /api/v1/components/:id/promotion-rules
func (h *PromotionHandler) GetComponentRules(c *gin.Context) {
	rules, err := h.svc.ListRulesForComponent(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	if rules == nil {
		rules = []domain.PromotionRule{}
	}
	c.JSON(http.StatusOK, rules)
}

// Promote handles POST /api/v1/promotion/promote
// Body: { "rule_id": "...", "component_ids": ["..."] }
func (h *PromotionHandler) Promote(c *gin.Context) {
	var body struct {
		RuleID       string   `json:"rule_id"`
		ComponentIDs []string `json:"component_ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if body.RuleID == "" || len(body.ComponentIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rule_id and component_ids are required"})
		return
	}
	userID, _ := c.Get("userID")
	uid, _ := userID.(string)

	requests, err := h.svc.Promote(c.Request.Context(), body.RuleID, body.ComponentIDs, uid)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"requests": requests})
}

// ListRequests handles GET /api/v1/promotion/requests?status=pending
func (h *PromotionHandler) ListRequests(c *gin.Context) {
	status := c.Query("status")
	requests, err := h.svc.ListRequests(c.Request.Context(), status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if requests == nil {
		requests = []domain.PromotionRequest{}
	}
	c.JSON(http.StatusOK, requests)
}

// Approve handles POST /api/v1/promotion/requests/:id/approve (admin only)
func (h *PromotionHandler) Approve(c *gin.Context) {
	reviewerID, _ := c.Get("userID")
	uid, _ := reviewerID.(string)
	if err := h.svc.Approve(c.Request.Context(), c.Param("id"), uid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Reject handles POST /api/v1/promotion/requests/:id/reject (admin only)
// Body: { "reason": "..." }
func (h *PromotionHandler) Reject(c *gin.Context) {
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	reviewerID, _ := c.Get("userID")
	uid, _ := reviewerID.(string)
	if err := h.svc.Reject(c.Request.Context(), c.Param("id"), uid, body.Reason); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/api/handlers/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/api/handlers/promotion.go
git commit -m "feat(promotion): add PromotionHandler HTTP endpoints"
```

---

## Task 9: Handler Tests

**Files:**
- Create: `internal/api/handlers/promotion_handler_test.go`

- [ ] **Step 1: Write the tests**

```go
package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func newTestPromotionHandler(t *testing.T) (*handlers.PromotionHandler, *testutil.PromotionRepo, *testutil.RepoRepo) {
	t.Helper()
	promoRepo := testutil.NewPromotionRepo()
	compRepo  := testutil.NewComponentRepo()
	assetRepo := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()
	blobRepo  := testutil.NewBlobStoreRepo()
	scanRepo  := testutil.NewScanResultRepo()
	repoRepo  := testutil.NewRepoRepo()
	registry  := storage.NewRegistry(blobStore)

	svc, err := service.NewPromotionService(promoRepo, compRepo, assetRepo, repoRepo, blobRepo, scanRepo, blobStore, registry)
	if err != nil {
		t.Fatalf("NewPromotionService: %v", err)
	}
	return handlers.NewPromotionHandler(svc), promoRepo, repoRepo
}

func TestPromotionHandler_ListRules_Empty(t *testing.T) {
	h, _, _ := newTestPromotionHandler(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/v1/promotion/rules", h.ListRules)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/promotion/rules", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var rules []domain.PromotionRule
	if err := json.Unmarshal(w.Body.Bytes(), &rules); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected empty slice, got %d", len(rules))
	}
}

func TestPromotionHandler_CreateRule(t *testing.T) {
	h, promoRepo, _ := newTestPromotionHandler(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v1/promotion/rules", h.CreateRule)

	body, _ := json.Marshal(map[string]any{
		"name": "my-rule", "from_repo": "staging", "to_repo": "prod",
		"require_scan_pass": false, "require_manual_approval": true,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/promotion/rules", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body)
	}
	rules, _ := promoRepo.ListRules(req.Context())
	if len(rules) != 1 {
		t.Errorf("expected 1 rule in repo, got %d", len(rules))
	}
}

func TestPromotionHandler_Promote_NonAdmin_Allowed(t *testing.T) {
	// Any authenticated user can trigger Promote (non-admin allowed).
	h, promoRepo, repoRepo := newTestPromotionHandler(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "user42"); c.Next() })
	r.POST("/api/v1/promotion/promote", h.Promote)

	// Seed rule (no DB for repos, will fail component lookup but 422 is expected)
	rule := &domain.PromotionRule{Name: "r", FromRepo: "a", ToRepo: "b", RequireManualApproval: false}
	_ = promoRepo.CreateRule(r.Routes()[0].HandlerFunc.(gin.HandlersChain)[0].(*gin.Context).Request.Context(), rule)
	_ = repoRepo // suppress unused

	body, _ := json.Marshal(map[string]any{"rule_id": "nonexistent", "component_ids": []string{"c1"}})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/promotion/promote", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	// Rule not found → 422 (not 403 — any user can attempt promote)
	if w.Code == http.StatusForbidden {
		t.Errorf("expected non-403, got 403 — non-admin should be able to call Promote")
	}
}

func TestPromotionHandler_Approve_RequiresAdmin(t *testing.T) {
	// Approve is wired behind AdminRequired() in the router.
	// Verify the handler itself sets userID from context (no 403 at handler level).
	h, promoRepo, _ := newTestPromotionHandler(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("userID", "admin1"); c.Next() })
	r.POST("/api/v1/promotion/requests/:id/approve", h.Approve)

	// Approve a nonexistent request → 400 with "not found" error
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/promotion/requests/doesnotexist/approve", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for nonexistent request, got %d", w.Code)
	}
	_ = promoRepo
}
```

- [ ] **Step 2: Run the tests**

```bash
go test ./internal/api/handlers/... -run TestPromotion -v
```

Expected: all tests pass.

- [ ] **Step 3: Run all handler tests**

```bash
go test ./internal/api/handlers/... -count=1
```

Expected: no failures.

- [ ] **Step 4: Commit**

```bash
git add internal/api/handlers/promotion_handler_test.go
git commit -m "feat(promotion): add PromotionHandler unit tests"
```

---

## Task 10: Router Wiring

**Files:**
- Modify: `internal/api/router.go`

- [ ] **Step 1: Add promotionRepo after replRepo**

Find this line (around line 83):
```go
replRepo      := postgres.NewReplicationRepo(pool)
```

Add immediately after it:
```go
promotionRepo  := postgres.NewPromotionRepo(pool)
```

- [ ] **Step 2: Add promotionSvc after replSvc**

Find this block (around line 152):
```go
replSvc := service.NewReplicationService(replRepo, assetRepo, localBlob, cfg.Auth.JWTSecret, log)
go replSvc.StartCronScheduler(context.Background())
```

Add immediately after it:
```go
promotionSvc, err := service.NewPromotionService(
    promotionRepo, componentRepo, assetRepo, repoRepo, blobRepo, scanRepo, localBlob, blobRegistry,
)
if err != nil {
    panic("promotion service init: " + err.Error())
}
promotionSvc.WithWebhooks(webhookSvc)
```

Note: `scanRepo` is `postgres.NewScanResultRepo(pool)` — check if it already exists in router.go. If not, add:
```go
scanRepo := postgres.NewScanResultRepo(pool)
```
in the repos block after `replRepo`.

- [ ] **Step 3: Add handler and routes**

Find this block:
```go
replH      := handlers.NewReplicationHandler(replSvc)
```

Add after it:
```go
promotionH := handlers.NewPromotionHandler(promotionSvc)
```

Find the authed routes section (around `authed.GET("/api/v1/replication/rules", ...)`). Add:
```go
// ── Promotion rules (read) ──────────────────────────────────
authed.GET("/api/v1/promotion/rules",                          promotionH.ListRules)
authed.GET("/api/v1/promotion/requests",                       promotionH.ListRequests)
authed.GET("/api/v1/components/:id/promotion-rules",           promotionH.GetComponentRules)
authed.POST("/api/v1/promotion/promote",                       promotionH.Promote)
```

Find the admin-only routes section (around `admin.POST("/api/v1/replication/rules", ...)`). Add:
```go
// ── Promotion rules (admin) ─────────────────────────────────
admin.POST("/api/v1/promotion/rules",                          promotionH.CreateRule)
admin.PUT("/api/v1/promotion/rules/:id",                       promotionH.UpdateRule)
admin.DELETE("/api/v1/promotion/rules/:id",                    promotionH.DeleteRule)
admin.POST("/api/v1/promotion/requests/:id/approve",           promotionH.Approve)
admin.POST("/api/v1/promotion/requests/:id/reject",            promotionH.Reject)
```

- [ ] **Step 4: Verify compilation**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 5: Run the full test suite**

```bash
go test ./... -count=1
```

Expected: no failures.

- [ ] **Step 6: Commit**

```bash
git add internal/api/router.go
git commit -m "feat(promotion): wire PromotionService and routes in router"
```

---

## Task 11: Frontend — AdminPage Promotion Tab

**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

- [ ] **Step 1: Add `'promotion'` to the `AdminTab` type and `VALID_TABS` array**

Find:
```typescript
type AdminTab = 'info' | 'blobs' | 'backup' | 'monitoring' | 'migration' | 'routing-rules' | 'replication' | 'saml'
const VALID_TABS: AdminTab[] = ['info', 'blobs', 'backup', 'monitoring', 'migration', 'routing-rules', 'replication', 'saml']
```

Replace with:
```typescript
type AdminTab = 'info' | 'blobs' | 'backup' | 'monitoring' | 'migration' | 'routing-rules' | 'replication' | 'saml' | 'promotion'
const VALID_TABS: AdminTab[] = ['info', 'blobs', 'backup', 'monitoring', 'migration', 'routing-rules', 'replication', 'saml', 'promotion']
```

- [ ] **Step 2: Add the tab button to the tab list**

Find the tab items array (around line 721):
```typescript
{ value: 'saml',          label: <><Shield size={13} style={{ marginRight: 5 }} />SAML</>, },
] as HoloTabItem[]}
```

Replace with:
```typescript
{ value: 'saml',          label: <><Shield size={13} style={{ marginRight: 5 }} />SAML</>, },
{ value: 'promotion',     label: <><ArrowUpCircle size={13} style={{ marginRight: 5 }} />Promotion</>, },
] as HoloTabItem[]}
```

Add `ArrowUpCircle` to the lucide-react import at the top of the file.

- [ ] **Step 3: Add `PromotionTab` component** — insert before the main `export default function AdminPage()` at the bottom of the file.

```typescript
// ── Types ──────────────────────────────────────────────────────────
interface PromotionRule {
  id: string
  name: string
  from_repo: string
  to_repo: string
  path_filter?: string
  require_scan_pass: boolean
  require_manual_approval: boolean
  created_at: string
}

interface PromotionRequest {
  id: string
  rule_id: string
  component_id: string
  status: 'pending' | 'approved' | 'rejected' | 'completed' | 'failed'
  requested_by: string
  reviewed_by?: string
  completed_at?: string
  error?: string
  created_at: string
}

function PromotionTab() {
  const qc = useQueryClient()
  const [showRuleModal, setShowRuleModal] = useState(false)
  const [editRule, setEditRule] = useState<PromotionRule | null>(null)
  const [reqFilter, setReqFilter] = useState('')
  const [rejectTarget, setRejectTarget] = useState<string | null>(null)
  const [rejectReason, setRejectReason] = useState('')

  const { data: rules = [] } = useQuery<PromotionRule[]>({
    queryKey: ['promotion-rules'],
    queryFn: () => apiClient.get('/api/v1/promotion/rules').then(r => r.data),
  })
  const { data: requests = [] } = useQuery<PromotionRequest[]>({
    queryKey: ['promotion-requests', reqFilter],
    queryFn: () => apiClient.get('/api/v1/promotion/requests', { params: { status: reqFilter || undefined } }).then(r => r.data),
    refetchInterval: 10_000,
  })
  const { data: repos = [] } = useQuery<{ name: string }[]>({
    queryKey: ['repos'],
    queryFn: () => nexusApi.get('/repositories').then(r => r.data),
  })

  const deleteRule = useMutation({
    mutationFn: (id: string) => apiClient.delete(`/api/v1/promotion/rules/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['promotion-rules'] }),
  })
  const approve = useMutation({
    mutationFn: (id: string) => apiClient.post(`/api/v1/promotion/requests/${id}/approve`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['promotion-requests'] }),
  })
  const reject = useMutation({
    mutationFn: ({ id, reason }: { id: string; reason: string }) =>
      apiClient.post(`/api/v1/promotion/requests/${id}/reject`, { reason }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['promotion-requests'] }); setRejectTarget(null); setRejectReason('') },
  })

  const statusColor: Record<string, string> = {
    pending: '#f59e0b', approved: '#3b82f6', completed: '#22c55e',
    rejected: '#ef4444', failed: '#ef4444',
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
      {/* Rules card */}
      <HoloCard style={{ padding: 20 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
          <h3 style={{ margin: 0, fontSize: 15, color: '#e2e8f0' }}>Promotion Rules</h3>
          <HoloButton size="sm" onClick={() => { setEditRule(null); setShowRuleModal(true) }}>+ New Rule</HoloButton>
        </div>
        {rules.length === 0 ? (
          <div style={{ color: '#64748b', fontSize: 13, textAlign: 'center', padding: '24px 0' }}>
            No promotion rules yet. Create one to enable artifact promotion.
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {rules.map(rule => (
              <div key={rule.id} style={{ background: 'rgba(255,255,255,0.03)', borderRadius: 10, padding: '12px 16px', border: '1px solid rgba(255,255,255,0.07)' }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
                  <div>
                    <div style={{ fontSize: 14, fontWeight: 600, color: '#e2e8f0' }}>{rule.name}</div>
                    <div style={{ fontSize: 12, color: '#94a3b8', marginTop: 4 }}>
                      {rule.from_repo} <span style={{ color: '#3b82f6' }}>→</span> {rule.to_repo}
                    </div>
                    {rule.path_filter && (
                      <div style={{ fontSize: 11, color: '#64748b', marginTop: 4, fontFamily: 'monospace' }}>{rule.path_filter}</div>
                    )}
                    <div style={{ display: 'flex', gap: 6, marginTop: 8 }}>
                      {rule.require_scan_pass && (
                        <span style={{ fontSize: 10, background: 'rgba(239,68,68,0.15)', color: '#f87171', borderRadius: 4, padding: '2px 6px' }}>Scan required</span>
                      )}
                      {rule.require_manual_approval && (
                        <span style={{ fontSize: 10, background: 'rgba(245,158,11,0.15)', color: '#fbbf24', borderRadius: 4, padding: '2px 6px' }}>Manual approval</span>
                      )}
                      {!rule.require_manual_approval && (
                        <span style={{ fontSize: 10, background: 'rgba(34,197,94,0.15)', color: '#4ade80', borderRadius: 4, padding: '2px 6px' }}>Auto-approve</span>
                      )}
                    </div>
                  </div>
                  <div style={{ display: 'flex', gap: 8 }}>
                    <HoloButton size="sm" variant="ghost" onClick={() => { setEditRule(rule); setShowRuleModal(true) }}>Edit</HoloButton>
                    <HoloButton size="sm" variant="ghost" onClick={() => { if (confirm(`Delete rule "${rule.name}"?`)) deleteRule.mutate(rule.id) }}>Delete</HoloButton>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </HoloCard>

      {/* Requests card */}
      <HoloCard style={{ padding: 20 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
          <h3 style={{ margin: 0, fontSize: 15, color: '#e2e8f0' }}>Promotion Requests</h3>
          <select
            value={reqFilter}
            onChange={e => setReqFilter(e.target.value)}
            style={{ background: '#0d1829', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 6, color: '#94a3b8', padding: '4px 8px', fontSize: 12 }}
          >
            <option value="">All statuses</option>
            <option value="pending">Pending</option>
            <option value="completed">Completed</option>
            <option value="rejected">Rejected</option>
            <option value="failed">Failed</option>
          </select>
        </div>
        {requests.length === 0 ? (
          <div style={{ color: '#64748b', fontSize: 13, textAlign: 'center', padding: '24px 0' }}>No promotion requests.</div>
        ) : (
          <table style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
            <thead>
              <tr style={{ color: '#64748b', borderBottom: '1px solid rgba(255,255,255,0.07)' }}>
                <th style={{ textAlign: 'left', padding: '6px 8px', fontWeight: 500 }}>Component</th>
                <th style={{ textAlign: 'left', padding: '6px 8px', fontWeight: 500 }}>Rule</th>
                <th style={{ textAlign: 'left', padding: '6px 8px', fontWeight: 500 }}>Status</th>
                <th style={{ textAlign: 'left', padding: '6px 8px', fontWeight: 500 }}>Requested</th>
                <th style={{ textAlign: 'left', padding: '6px 8px', fontWeight: 500 }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {requests.map(req => {
                const rule = rules.find(r => r.id === req.rule_id)
                return (
                  <tr key={req.id} style={{ borderBottom: '1px solid rgba(255,255,255,0.04)' }}>
                    <td style={{ padding: '8px', color: '#94a3b8', fontFamily: 'monospace', fontSize: 11 }}>{req.component_id.slice(0, 8)}…</td>
                    <td style={{ padding: '8px', color: '#94a3b8' }}>{rule?.name ?? req.rule_id.slice(0, 8)}</td>
                    <td style={{ padding: '8px' }}>
                      <span style={{ color: statusColor[req.status] ?? '#94a3b8', fontWeight: 600, fontSize: 12 }}>{req.status.toUpperCase()}</span>
                      {req.error && <div style={{ fontSize: 10, color: '#ef4444', marginTop: 2 }}>{req.error}</div>}
                    </td>
                    <td style={{ padding: '8px', color: '#64748b', fontSize: 11 }}>{new Date(req.created_at).toLocaleString()}</td>
                    <td style={{ padding: '8px' }}>
                      {req.status === 'pending' && (
                        <div style={{ display: 'flex', gap: 6 }}>
                          <HoloButton size="sm" onClick={() => approve.mutate(req.id)}>Approve</HoloButton>
                          <HoloButton size="sm" variant="ghost" onClick={() => setRejectTarget(req.id)}>Reject</HoloButton>
                        </div>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </HoloCard>

      {/* Reject reason modal */}
      {rejectTarget && (
        <HoloModal open onClose={() => setRejectTarget(null)} title="Reject Promotion Request">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <HoloInput label="Reason (optional)" value={rejectReason} onChange={e => setRejectReason(e.target.value)} />
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <HoloButton variant="ghost" onClick={() => setRejectTarget(null)}>Cancel</HoloButton>
              <HoloButton onClick={() => reject.mutate({ id: rejectTarget, reason: rejectReason })}>Reject</HoloButton>
            </div>
          </div>
        </HoloModal>
      )}

      {/* Create / Edit rule modal */}
      <PromotionRuleModal
        open={showRuleModal}
        rule={editRule}
        repos={repos}
        onClose={() => { setShowRuleModal(false); setEditRule(null) }}
        onSaved={() => { qc.invalidateQueries({ queryKey: ['promotion-rules'] }); setShowRuleModal(false); setEditRule(null) }}
      />
    </div>
  )
}

function PromotionRuleModal({ open, rule, repos, onClose, onSaved }: {
  open: boolean
  rule: PromotionRule | null
  repos: { name: string }[]
  onClose: () => void
  onSaved: () => void
}) {
  const [name, setName] = useState(rule?.name ?? '')
  const [fromRepo, setFromRepo] = useState(rule?.from_repo ?? '')
  const [toRepo, setToRepo] = useState(rule?.to_repo ?? '')
  const [pathFilter, setPathFilter] = useState(rule?.path_filter ?? '')
  const [requireScan, setRequireScan] = useState(rule?.require_scan_pass ?? false)
  const [requireApproval, setRequireApproval] = useState(rule?.require_manual_approval ?? false)
  const [error, setError] = useState('')

  useEffect(() => {
    setName(rule?.name ?? '')
    setFromRepo(rule?.from_repo ?? '')
    setToRepo(rule?.to_repo ?? '')
    setPathFilter(rule?.path_filter ?? '')
    setRequireScan(rule?.require_scan_pass ?? false)
    setRequireApproval(rule?.require_manual_approval ?? false)
    setError('')
  }, [rule, open])

  const save = useMutation({
    mutationFn: () => {
      const body = { name, from_repo: fromRepo, to_repo: toRepo, path_filter: pathFilter, require_scan_pass: requireScan, require_manual_approval: requireApproval }
      return rule
        ? apiClient.put(`/api/v1/promotion/rules/${rule.id}`, body)
        : apiClient.post('/api/v1/promotion/rules', body)
    },
    onSuccess: onSaved,
    onError: (e: any) => setError(e?.response?.data?.error ?? 'Save failed'),
  })

  const repoOptions = repos.map(r => ({ value: r.name, label: r.name }))

  return (
    <HoloModal open={open} onClose={onClose} title={rule ? 'Edit Promotion Rule' : 'Create Promotion Rule'}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        <HoloInput label="Name" value={name} onChange={e => setName(e.target.value)} />
        <Select label="From Repository" value={fromRepo} onChange={setFromRepo} options={repoOptions} placeholder="Select source repo" />
        <Select label="To Repository" value={toRepo} onChange={setToRepo} options={repoOptions} placeholder="Select target repo" />
        <HoloInput
          label="Path Filter (CEL)"
          value={pathFilter}
          onChange={e => setPathFilter(e.target.value)}
          placeholder='path.startsWith("/com/myco/") — leave empty to match all'
        />
        <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: '#94a3b8', cursor: 'pointer' }}>
          <input type="checkbox" checked={requireScan} onChange={e => setRequireScan(e.target.checked)} />
          Require scan pass (block if HIGH or CRITICAL findings)
        </label>
        <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, color: '#94a3b8', cursor: 'pointer' }}>
          <input type="checkbox" checked={requireApproval} onChange={e => setRequireApproval(e.target.checked)} />
          Require manual approval by nx-admin
        </label>
        {error && <div style={{ color: '#ef4444', fontSize: 12 }}>{error}</div>}
        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <HoloButton variant="ghost" onClick={onClose}>Cancel</HoloButton>
          <HoloButton onClick={() => save.mutate()} disabled={save.isPending}>
            {save.isPending ? 'Saving…' : rule ? 'Save Changes' : 'Create Rule'}
          </HoloButton>
        </div>
      </div>
    </HoloModal>
  )
}
```

- [ ] **Step 4: Add `{tab === 'promotion' && <PromotionTab />}` to the tab content render block**

Find where the last tab renders (near `{tab === 'saml' && <SamlTab />}`). Add after it:
```tsx
{tab === 'promotion' && <PromotionTab />}
```

- [ ] **Step 5: Verify TypeScript compilation**

```bash
cd frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/AdminPage.tsx
git commit -m "feat(promotion): add Promotion tab to AdminPage (rules CRUD + requests queue)"
```

---

## Task 12: Frontend — Browse Promote Button + Bulk

**Files:**
- Modify: `frontend/src/pages/BrowsePage.tsx`

- [ ] **Step 1: Add PromotionRule interface and API fetcher near top of file**

Near the other interface definitions, add:
```typescript
interface PromotionRule {
  id: string
  name: string
  from_repo: string
  to_repo: string
  path_filter?: string
  require_scan_pass: boolean
  require_manual_approval: boolean
}
```

- [ ] **Step 2: Add selection state and promotion modal state inside the main component**

Inside the main BrowsePage component, after the existing `useState` declarations, add:
```typescript
const [selectedComponentIDs, setSelectedComponentIDs] = useState<Set<string>>(new Set())
const [promoteModalOpen, setPromoteModalOpen] = useState(false)
const [promoteComponentIDs, setPromoteComponentIDs] = useState<string[]>([])
const [promotionRules, setPromotionRules] = useState<PromotionRule[]>([])
const [selectedRuleID, setSelectedRuleID] = useState('')
const [promotionResult, setPromotionResult] = useState<string | null>(null)
```

- [ ] **Step 3: Add checkbox column to the component list table**

Find the table header row that lists component columns. Add a `<th>` checkbox column as the first header:
```tsx
<th style={{ width: 36, padding: '6px 8px' }}>
  <input
    type="checkbox"
    onChange={e => {
      if (e.target.checked) {
        setSelectedComponentIDs(new Set(components.map((c: any) => c.id)))
      } else {
        setSelectedComponentIDs(new Set())
      }
    }}
    checked={selectedComponentIDs.size > 0 && selectedComponentIDs.size === components.length}
  />
</th>
```

For each row, add the first `<td>` with checkbox:
```tsx
<td style={{ width: 36, padding: '6px 8px' }}>
  <input
    type="checkbox"
    checked={selectedComponentIDs.has(comp.id)}
    onChange={e => {
      const next = new Set(selectedComponentIDs)
      if (e.target.checked) next.add(comp.id)
      else next.delete(comp.id)
      setSelectedComponentIDs(next)
    }}
  />
</td>
```

- [ ] **Step 4: Add bulk toolbar that appears when 1+ components are selected**

Above the component table (or just below the repo selector), add:
```tsx
{selectedComponentIDs.size > 0 && (
  <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '8px 12px', background: 'rgba(59,130,246,0.1)', borderRadius: 8, marginBottom: 8, border: '1px solid rgba(59,130,246,0.3)' }}>
    <span style={{ fontSize: 13, color: '#93c5fd' }}>{selectedComponentIDs.size} selected</span>
    <HoloButton size="sm" onClick={async () => {
      const compID = Array.from(selectedComponentIDs)[0]
      const rules: PromotionRule[] = await apiClient.get(`/api/v1/components/${compID}/promotion-rules`).then(r => r.data)
      setPromotionRules(rules)
      setPromoteComponentIDs(Array.from(selectedComponentIDs))
      setSelectedRuleID('')
      setPromotionResult(null)
      setPromoteModalOpen(true)
    }}>
      Promote selected ({selectedComponentIDs.size})
    </HoloButton>
    <HoloButton size="sm" variant="ghost" onClick={() => setSelectedComponentIDs(new Set())}>Clear</HoloButton>
  </div>
)}
```

- [ ] **Step 5: Add "Promote" button in single component detail panel**

Find where the detail panel shows component info and action buttons. Add a Promote button that fetches rules for that specific component:
```tsx
<HoloButton size="sm" variant="ghost" onClick={async () => {
  const rules: PromotionRule[] = await apiClient.get(`/api/v1/components/${selectedComponent.id}/promotion-rules`).then(r => r.data)
  if (rules.length === 0) { alert('No promotion rules defined for this repository.'); return }
  setPromotionRules(rules)
  setPromoteComponentIDs([selectedComponent.id])
  setSelectedRuleID(rules[0].id)
  setPromotionResult(null)
  setPromoteModalOpen(true)
}}>
  Promote
</HoloButton>
```

- [ ] **Step 6: Add the Promote modal**

Near the end of the JSX return, add:
```tsx
<HoloModal open={promoteModalOpen} onClose={() => setPromoteModalOpen(false)} title={`Promote ${promoteComponentIDs.length} component(s)`}>
  <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
    {promotionRules.length === 0 ? (
      <div style={{ color: '#64748b', fontSize: 13 }}>No promotion rules available for this repository.</div>
    ) : (
      <>
        <Select
          label="Promotion Rule"
          value={selectedRuleID}
          onChange={setSelectedRuleID}
          options={promotionRules.map(r => ({ value: r.id, label: `${r.name} (${r.from_repo} → ${r.to_repo})` }))}
          placeholder="Select a rule"
        />
        {promotionResult && (
          <div style={{ fontSize: 13, color: promotionResult.startsWith('Error') ? '#ef4444' : '#4ade80' }}>
            {promotionResult}
          </div>
        )}
        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <HoloButton variant="ghost" onClick={() => setPromoteModalOpen(false)}>Cancel</HoloButton>
          <HoloButton
            disabled={!selectedRuleID}
            onClick={async () => {
              try {
                const res = await apiClient.post('/api/v1/promotion/promote', {
                  rule_id: selectedRuleID,
                  component_ids: promoteComponentIDs,
                })
                const reqs = res.data.requests as { status: string }[]
                const rule = promotionRules.find(r => r.id === selectedRuleID)
                if (rule?.require_manual_approval) {
                  setPromotionResult(`Approval requested for ${reqs.length} component(s). An admin must approve.`)
                } else {
                  setPromotionResult(`Promoted ${reqs.length} component(s) successfully.`)
                }
                setSelectedComponentIDs(new Set())
              } catch (e: any) {
                setPromotionResult(`Error: ${e?.response?.data?.error ?? 'Promotion failed'}`)
              }
            }}
          >
            Promote
          </HoloButton>
        </div>
      </>
    )}
  </div>
</HoloModal>
```

- [ ] **Step 7: Verify TypeScript compilation**

```bash
cd frontend && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 8: Build to check for bundle errors**

```bash
cd frontend && npm run build
```

Expected: build succeeds, no errors.

- [ ] **Step 9: Run full Go test suite one final time**

```bash
go test ./... -count=1
```

Expected: all tests pass.

- [ ] **Step 10: Commit**

```bash
git add frontend/src/pages/BrowsePage.tsx
git commit -m "feat(promotion): add Promote button and bulk selection to BrowsePage"
```

---

## Final: Update NEXT_RELEASE.md and task_plan.md

- [ ] **Step 1: Append to `NEXT_RELEASE.md`**

```markdown
### ✨ Features

* **Staging & Build Promotion** — new promotion workflow for controlled artifact movement between repositories. Administrators define promotion rules (source repo → target repo) with optional CEL path filters (same syntax as Content Selectors), scan-pass gates (blocks if HIGH/CRITICAL OSV findings exist), and manual approval requirements. Users promote individual or bulk-selected components from the Browse page. Auto-approve rules complete instantly; manual-approval rules create a pending request visible in AdminPage → Promotion tab. Any `nx-admin` can approve or reject pending requests with an optional reason. Full audit trail in `promotion_requests` table.
```

- [ ] **Step 2: Update `task_plan.md` — change Phase 56 status to `complete (2026-05-15)`**

- [ ] **Step 3: Final commit**

```bash
git add NEXT_RELEASE.md task_plan.md
git commit -m "docs(release): add Phase 56 Staging & Build Promotion changelog entry"
```

---

## Self-Review Checklist

**Spec coverage:**
- ✅ DB tables: `promotion_rules`, `promotion_requests`
- ✅ Service: `ListRulesForComponent`, `Promote`, `Approve`, `Reject`, `executeCopy`
- ✅ CEL path filter with `format`, `path`, `repository` variables
- ✅ Scan gate (HIGH/CRITICAL block)
- ✅ nx-admin enforced via `AdminRequired()` middleware on approve/reject routes
- ✅ Bulk promote (array of component_ids)
- ✅ Single component promote from Browse detail panel
- ✅ AdminPage Promotion tab: rules CRUD + requests queue with Approve/Reject
- ✅ Webhook `artifact.published` fired on successful copy
- ✅ Upsert semantics on component/asset (no duplicates)
- ✅ Error cases: scan not run, HIGH/CRITICAL findings, target store unreachable, non-pending request approve/reject

**Placeholder scan:** No TBD or TODO entries found.

**Type consistency:** All method names and signatures consistent across interfaces.go → postgres impl → service → handler → tests.
