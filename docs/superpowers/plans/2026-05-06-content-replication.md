# Content Replication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Push artifacts from a local Nexspence repository to a remote Nexspence instance on a cron schedule, detecting duplicates by asset path.

**Architecture:** DB tables `replication_rules` + `replication_history`; `ReplicationRepo` interface + postgres impl + in-memory mock; `ReplicationService` with cron scheduler (robfig/cron/v3), AES-256-GCM credential encryption keyed on JWT secret, and a `runRule` loop that lists local assets, queries target REST API for existing paths, and PUT-pushes new blobs via Basic Auth; REST handler wired under `/api/v1/replication/`; `ReplicationTab` in AdminPage.

**Tech Stack:** Go (pgx/v5, robfig/cron/v3, crypto/aes+cipher), React + TypeScript (react-query, HoloKit components)

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| Create | `internal/db/migrations/017_replication_rules.sql` | DB schema |
| Modify | `internal/domain/types.go` | Add `ReplicationRule`, `ReplicationHistory` |
| Modify | `internal/repository/interfaces.go` | Add `ReplicationRepo` interface |
| Create | `internal/repository/postgres/replication_repo.go` | Postgres implementation |
| Modify | `internal/testutil/mocks.go` | Add in-memory `ReplicationRepo` mock |
| Create | `internal/service/replication_service.go` | Cron + encrypt/decrypt + runRule |
| Create | `internal/service/replication_service_test.go` | Unit tests |
| Create | `internal/api/handlers/replication.go` | HTTP handler (7 endpoints) |
| Modify | `internal/api/router.go` | Wire service + handler + routes |
| Modify | `frontend/src/api/client.ts` | Replication types + API calls |
| Modify | `frontend/src/pages/AdminPage.tsx` | ReplicationTab + new tab entry |
| Modify | `NEXT_RELEASE.md` | Changelog |
| Modify | `task_plan.md` | Mark Phase 55 complete |
| Modify | `CLAUDE.md` | Update current phase description |
| Modify | `README.md` | Feature mention |
| Modify | `../nexspence-demo/README.md` | Same update (keep in sync) |

---

## Task 1: DB Migration

**Files:**
- Create: `internal/db/migrations/017_replication_rules.sql`

- [ ] **Step 1: Write migration**

```sql
-- +goose Up
CREATE TABLE replication_rules (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 TEXT NOT NULL UNIQUE,
    source_repo          TEXT NOT NULL,
    target_url           TEXT NOT NULL,
    target_repo          TEXT NOT NULL,
    target_username      TEXT NOT NULL DEFAULT '',
    target_password_enc  TEXT NOT NULL DEFAULT '',
    cron_expr            TEXT NOT NULL DEFAULT '0 2 * * *',
    enabled              BOOLEAN NOT NULL DEFAULT true,
    last_run_at          TIMESTAMPTZ,
    last_run_status      TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE replication_history (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id           UUID NOT NULL REFERENCES replication_rules(id) ON DELETE CASCADE,
    started_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at       TIMESTAMPTZ,
    duration_ms       BIGINT,
    pushed_count      INT NOT NULL DEFAULT 0,
    skipped_count     INT NOT NULL DEFAULT 0,
    failed_count      INT NOT NULL DEFAULT 0,
    transferred_bytes BIGINT NOT NULL DEFAULT 0,
    error             TEXT
);

CREATE INDEX idx_replication_history_rule ON replication_history (rule_id, started_at DESC);

-- +goose Down
DROP TABLE IF EXISTS replication_history;
DROP TABLE IF EXISTS replication_rules;
```

- [ ] **Step 2: Verify migration file exists**

```bash
ls internal/db/migrations/017_replication_rules.sql
```

Expected: file present.

- [ ] **Step 3: Commit**

```bash
git add internal/db/migrations/017_replication_rules.sql
git commit -m "feat(db): add replication_rules and replication_history tables (migration 017)"
```

---

## Task 2: Domain Types

**Files:**
- Modify: `internal/domain/types.go` (append after the last struct)

- [ ] **Step 1: Append types to `internal/domain/types.go`**

Add at the end of the file:

```go
// ── Replication ──────────────────────────────────────────────────

// ReplicationRule defines a push-replication job from a local repo to a remote Nexspence instance.
type ReplicationRule struct {
	ID                string
	Name              string
	SourceRepo        string
	TargetURL         string
	TargetRepo        string
	TargetUsername    string
	TargetPasswordEnc string // AES-256-GCM encrypted, base64url; never returned in API responses
	CronExpr          string
	Enabled           bool
	LastRunAt         *time.Time
	LastRunStatus     string // "ok", "error", "running", ""
	CreatedAt         time.Time
}

// ReplicationHistory records the outcome of a single replication run.
type ReplicationHistory struct {
	ID               string
	RuleID           string
	StartedAt        time.Time
	FinishedAt       *time.Time
	DurationMs       int64
	PushedCount      int
	SkippedCount     int
	FailedCount      int
	TransferredBytes int64
	Error            string
}
```

Make sure `"time"` is already imported in `types.go` (it is).

- [ ] **Step 2: Build to verify no errors**

```bash
go build ./internal/domain/...
```

Expected: no output (success).

- [ ] **Step 3: Commit**

```bash
git add internal/domain/types.go
git commit -m "feat(domain): add ReplicationRule and ReplicationHistory types"
```

---

## Task 3: Repository Interface + Postgres Implementation

**Files:**
- Modify: `internal/repository/interfaces.go`
- Create: `internal/repository/postgres/replication_repo.go`

- [ ] **Step 1: Add `ReplicationRepo` interface to `internal/repository/interfaces.go`**

Add after the `ScanResultRepo` interface (at the end of the file):

```go
// ReplicationRepo manages replication rules and their run history.
type ReplicationRepo interface {
	ListRules(ctx context.Context) ([]domain.ReplicationRule, error)
	GetRule(ctx context.Context, id string) (*domain.ReplicationRule, error)
	CreateRule(ctx context.Context, r *domain.ReplicationRule) error
	UpdateRule(ctx context.Context, r *domain.ReplicationRule) error
	DeleteRule(ctx context.Context, id string) error
	UpdateRuleStatus(ctx context.Context, id, status string, at time.Time) error
	AddHistory(ctx context.Context, h *domain.ReplicationHistory) error
	ListHistory(ctx context.Context, ruleID string, limit int) ([]domain.ReplicationHistory, error)
}
```

Make sure `"time"` is imported in `interfaces.go` — it already is (used by AuditQuery).

- [ ] **Step 2: Create `internal/repository/postgres/replication_repo.go`**

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

	var out []domain.ReplicationRule
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

	var out []domain.ReplicationHistory
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
```

- [ ] **Step 3: Build to verify no errors**

```bash
go build ./internal/repository/...
```

Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/repository/interfaces.go internal/repository/postgres/replication_repo.go
git commit -m "feat(repo): add ReplicationRepo interface and postgres implementation"
```

---

## Task 4: In-Memory Mock

**Files:**
- Modify: `internal/testutil/mocks.go`

- [ ] **Step 1: Add compile-time check and `ReplicationRepo` mock to `internal/testutil/mocks.go`**

After the existing `_ repository.ScanResultRepo = (*ScanResultRepo)(nil)` line in the interface checks block, add:

```go
_ repository.ReplicationRepo = (*ReplicationRepo)(nil)
```

Then append the mock at the end of the file:

```go
// ── ReplicationRepo ──────────────────────────────────────────────

type ReplicationRepo struct {
	mu      sync.Mutex
	rules   map[string]*domain.ReplicationRule
	history []domain.ReplicationHistory
}

func NewReplicationRepo() *ReplicationRepo {
	return &ReplicationRepo{rules: make(map[string]*domain.ReplicationRule)}
}

func (r *ReplicationRepo) ListRules(_ context.Context) ([]domain.ReplicationRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.ReplicationRule, 0, len(r.rules))
	for _, v := range r.rules {
		out = append(out, *v)
	}
	return out, nil
}

func (r *ReplicationRepo) GetRule(_ context.Context, id string) (*domain.ReplicationRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.rules[id]
	if !ok {
		return nil, nil
	}
	cp := *v
	return &cp, nil
}

func (r *ReplicationRepo) CreateRule(_ context.Context, rule *domain.ReplicationRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rule.ID = fmt.Sprintf("rule-%d", len(r.rules)+1)
	rule.CreatedAt = time.Now()
	cp := *rule
	r.rules[rule.ID] = &cp
	return nil
}

func (r *ReplicationRepo) UpdateRule(_ context.Context, rule *domain.ReplicationRule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.rules[rule.ID]; !ok {
		return fmt.Errorf("rule not found: %s", rule.ID)
	}
	cp := *rule
	r.rules[rule.ID] = &cp
	return nil
}

func (r *ReplicationRepo) DeleteRule(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rules, id)
	return nil
}

func (r *ReplicationRepo) UpdateRuleStatus(_ context.Context, id, status string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.rules[id]; ok {
		v.LastRunStatus = status
		v.LastRunAt = &at
	}
	return nil
}

func (r *ReplicationRepo) AddHistory(_ context.Context, h *domain.ReplicationHistory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	h.ID = fmt.Sprintf("hist-%d", len(r.history)+1)
	r.history = append(r.history, *h)
	return nil
}

func (r *ReplicationRepo) ListHistory(_ context.Context, ruleID string, limit int) ([]domain.ReplicationHistory, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.ReplicationHistory
	for i := len(r.history) - 1; i >= 0 && len(out) < limit; i-- {
		if r.history[i].RuleID == ruleID {
			out = append(out, r.history[i])
		}
	}
	return out, nil
}

// Also update the existing AssetRepo.ListByRepoAndPath stub in mocks.go to actually
// return stored assets, replacing the stub `return nil, nil` with:

// func (a *AssetRepo) ListByRepoAndPath(_ context.Context, repoName, pathPrefix string) ([]domain.Asset, error) {
// 	a.mu.Lock()
// 	defer a.mu.Unlock()
// 	var out []domain.Asset
// 	for _, asset := range a.byID {
// 		if asset.Repository == repoName && strings.HasPrefix(asset.Path, pathPrefix) {
// 			out = append(out, *asset)
// 		}
// 	}
// 	return out, nil
// }
//
// Replace the stub at line ~480 with this implementation.
// Also add `"strings"` import if not already present in mocks.go.
```

- [ ] **Step 2: Build to verify no errors**

```bash
go build ./internal/testutil/...
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/testutil/mocks.go
git commit -m "feat(testutil): add ReplicationRepo in-memory mock"
```

---

## Task 5: ReplicationService

**Files:**
- Create: `internal/service/replication_service.go`

- [ ] **Step 1: Create `internal/service/replication_service.go`**

```go
package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/robfig/cron/v3"
)

// ReplicationService pushes artifacts from local repos to remote Nexspence instances.
type ReplicationService struct {
	repo      repository.ReplicationRepo
	assets    repository.AssetRepo
	blobStore storage.BlobStore
	jwtSecret string
	log       logger.Logger

	mu            sync.Mutex
	cronScheduler *cron.Cron
	entryIDs      map[string]cron.EntryID
}

func NewReplicationService(
	repo repository.ReplicationRepo,
	assets repository.AssetRepo,
	blobStore storage.BlobStore,
	jwtSecret string,
	log logger.Logger,
) *ReplicationService {
	return &ReplicationService{
		repo:      repo,
		assets:    assets,
		blobStore: blobStore,
		jwtSecret: jwtSecret,
		log:       log,
		entryIDs:  make(map[string]cron.EntryID),
	}
}

// EncryptPassword encrypts plain with AES-256-GCM using a key derived from jwtSecret.
// Returns base64url(nonce + ciphertext). Returns "" for empty plain.
func (s *ReplicationService) EncryptPassword(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	key := deriveKey(s.jwtSecret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.URLEncoding.EncodeToString(sealed), nil
}

// DecryptPassword decrypts enc back to plaintext.
func (s *ReplicationService) DecryptPassword(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	data, err := base64.URLEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("replication: base64 decode: %w", err)
	}
	key := deriveKey(s.jwtSecret)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return "", fmt.Errorf("replication: ciphertext too short")
	}
	plain, err := gcm.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("replication: decrypt: %w", err)
	}
	return string(plain), nil
}

func deriveKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// StartCronScheduler loads all enabled rules and registers cron jobs. Run as a goroutine.
func (s *ReplicationService) StartCronScheduler(ctx context.Context) {
	s.mu.Lock()
	s.cronScheduler = cron.New()
	s.mu.Unlock()

	rules, err := s.repo.ListRules(ctx)
	if err != nil {
		s.log.Error("replication: failed to load rules for scheduler", "err", err)
	} else {
		s.mu.Lock()
		for _, r := range rules {
			if r.Enabled {
				s.addEntryLocked(r)
			}
		}
		s.mu.Unlock()
	}

	s.cronScheduler.Start()
	<-ctx.Done()
	s.cronScheduler.Stop()
}

// ReloadRule updates the cron entry for a single rule (call after Create/Update/Delete).
func (s *ReplicationService) ReloadRule(ctx context.Context, ruleID string) {
	rule, _ := s.repo.GetRule(ctx, ruleID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cronScheduler == nil {
		return
	}
	if eid, ok := s.entryIDs[ruleID]; ok {
		s.cronScheduler.Remove(eid)
		delete(s.entryIDs, ruleID)
	}
	if rule == nil || !rule.Enabled {
		return
	}
	s.addEntryLocked(*rule)
}

func (s *ReplicationService) addEntryLocked(rule domain.ReplicationRule) {
	job := func() {
		if err := s.RunRule(context.Background(), rule.ID); err != nil {
			s.log.Error("replication cron error", "rule", rule.Name, "err", err)
		}
	}
	id, err := s.cronScheduler.AddFunc(rule.CronExpr, job)
	if err != nil {
		s.log.Warn("replication: invalid cron_expr, skipping rule", "rule", rule.Name, "expr", rule.CronExpr, "err", err)
		return
	}
	s.entryIDs[rule.ID] = id
}

// RunRule executes a single replication rule immediately (used by cron and manual trigger).
func (s *ReplicationService) RunRule(ctx context.Context, ruleID string) error {
	rule, err := s.repo.GetRule(ctx, ruleID)
	if err != nil {
		return err
	}
	if rule == nil {
		return fmt.Errorf("replication rule %q not found", ruleID)
	}

	_ = s.repo.UpdateRuleStatus(ctx, ruleID, "running", time.Now())

	hist := &domain.ReplicationHistory{
		RuleID:    ruleID,
		StartedAt: time.Now(),
	}

	runErr := s.runRule(ctx, rule, hist)

	now := time.Now()
	hist.FinishedAt = &now
	hist.DurationMs = now.Sub(hist.StartedAt).Milliseconds()

	status := "ok"
	if runErr != nil || hist.FailedCount > 0 {
		status = "error"
		if runErr != nil {
			hist.Error = runErr.Error()
		}
	}
	_ = s.repo.UpdateRuleStatus(ctx, ruleID, status, now)
	_ = s.repo.AddHistory(ctx, hist)

	return runErr
}

// runRule performs the actual diff + push for a rule.
func (s *ReplicationService) runRule(ctx context.Context, rule *domain.ReplicationRule, hist *domain.ReplicationHistory) error {
	password, err := s.DecryptPassword(rule.TargetPasswordEnc)
	if err != nil {
		return fmt.Errorf("decrypt credentials: %w", err)
	}

	// 1. Build set of paths already on target.
	targetPaths, err := s.listTargetPaths(ctx, rule, password)
	if err != nil {
		return fmt.Errorf("list target assets: %w", err)
	}

	// 2. List local assets.
	localAssets, err := s.assets.ListByRepoAndPath(ctx, rule.SourceRepo, "")
	if err != nil {
		return fmt.Errorf("list local assets: %w", err)
	}

	// 3. Push missing assets.
	client := &http.Client{Timeout: 5 * time.Minute}
	for _, asset := range localAssets {
		if _, exists := targetPaths[asset.Path]; exists {
			hist.SkippedCount++
			continue
		}

		pushed, transferred, pushErr := s.pushAsset(ctx, client, rule, password, asset)
		if pushErr != nil {
			hist.FailedCount++
			if hist.Error == "" {
				hist.Error = pushErr.Error()
			}
			s.log.Warn("replication: push failed", "rule", rule.Name, "path", asset.Path, "err", pushErr)
			continue
		}
		if pushed {
			hist.PushedCount++
			hist.TransferredBytes += transferred
		}
	}
	return nil
}

// listTargetPaths queries the target instance for all asset paths in targetRepo.
func (s *ReplicationService) listTargetPaths(ctx context.Context, rule *domain.ReplicationRule, password string) (map[string]struct{}, error) {
	paths := make(map[string]struct{})
	client := &http.Client{Timeout: 30 * time.Second}
	token := ""

	for {
		url := strings.TrimRight(rule.TargetURL, "/") +
			"/service/rest/v1/assets?repository=" + rule.TargetRepo
		if token != "" {
			url += "&continuationToken=" + token
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		if rule.TargetUsername != "" {
			req.SetBasicAuth(rule.TargetUsername, password)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("target returned %d: %s", resp.StatusCode, string(body))
		}

		var page struct {
			Items []struct {
				Path string `json:"path"`
			} `json:"items"`
			ContinuationToken *string `json:"continuationToken"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parse target response: %w", err)
		}

		for _, item := range page.Items {
			paths[item.Path] = struct{}{}
		}

		if page.ContinuationToken == nil || *page.ContinuationToken == "" {
			break
		}
		token = *page.ContinuationToken
	}
	return paths, nil
}

// pushAsset streams one blob to the target. Returns (pushed, bytes, error).
func (s *ReplicationService) pushAsset(ctx context.Context, client *http.Client, rule *domain.ReplicationRule, password string, asset domain.Asset) (bool, int64, error) {
	rc, size, err := s.blobStore.Get(ctx, asset.BlobKey)
	if err != nil {
		return false, 0, fmt.Errorf("fetch blob %s: %w", asset.BlobKey, err)
	}
	defer rc.Close()

	targetPath := strings.TrimRight(rule.TargetURL, "/") +
		"/repository/" + rule.TargetRepo + "/" + strings.TrimPrefix(asset.Path, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetPath, rc)
	if err != nil {
		return false, 0, err
	}
	if size > 0 {
		req.ContentLength = size
	}
	if rule.TargetUsername != "" {
		req.SetBasicAuth(rule.TargetUsername, password)
	}
	if asset.ContentType != "" {
		req.Header.Set("Content-Type", asset.ContentType)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, 0, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		return false, 0, fmt.Errorf("target PUT %s returned %d", asset.Path, resp.StatusCode)
	}
	return true, size, nil
}

// TestConnection verifies connectivity and credentials to a target rule.
func (s *ReplicationService) TestConnection(ctx context.Context, ruleID string) error {
	rule, err := s.repo.GetRule(ctx, ruleID)
	if err != nil {
		return err
	}
	if rule == nil {
		return fmt.Errorf("rule not found")
	}
	password, err := s.DecryptPassword(rule.TargetPasswordEnc)
	if err != nil {
		return err
	}

	url := strings.TrimRight(rule.TargetURL, "/") + "/service/rest/v1/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if rule.TargetUsername != "" {
		req.SetBasicAuth(rule.TargetUsername, password)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("target returned %d", resp.StatusCode)
	}
	return nil
}

// ListRules returns all replication rules (passwords masked).
func (s *ReplicationService) ListRules(ctx context.Context) ([]domain.ReplicationRule, error) {
	rules, err := s.repo.ListRules(ctx)
	if err != nil {
		return nil, err
	}
	for i := range rules {
		rules[i].TargetPasswordEnc = ""
	}
	return rules, nil
}

// GetRule returns a single rule (password masked).
func (s *ReplicationService) GetRule(ctx context.Context, id string) (*domain.ReplicationRule, error) {
	rule, err := s.repo.GetRule(ctx, id)
	if err != nil || rule == nil {
		return rule, err
	}
	rule.TargetPasswordEnc = ""
	return rule, nil
}

// CreateRule encrypts the password and persists the rule.
func (s *ReplicationService) CreateRule(ctx context.Context, rule *domain.ReplicationRule, plainPassword string) error {
	enc, err := s.EncryptPassword(plainPassword)
	if err != nil {
		return err
	}
	rule.TargetPasswordEnc = enc
	return s.repo.CreateRule(ctx, rule)
}

// UpdateRule encrypts the password if provided (non-empty), otherwise keeps existing.
func (s *ReplicationService) UpdateRule(ctx context.Context, rule *domain.ReplicationRule, plainPassword string) error {
	if plainPassword != "" {
		enc, err := s.EncryptPassword(plainPassword)
		if err != nil {
			return err
		}
		rule.TargetPasswordEnc = enc
	} else {
		// Keep existing encrypted password.
		existing, err := s.repo.GetRule(ctx, rule.ID)
		if err != nil {
			return err
		}
		if existing != nil {
			rule.TargetPasswordEnc = existing.TargetPasswordEnc
		}
	}
	return s.repo.UpdateRule(ctx, rule)
}

// DeleteRule removes the rule and its cron entry.
func (s *ReplicationService) DeleteRule(ctx context.Context, id string) error {
	if err := s.repo.DeleteRule(ctx, id); err != nil {
		return err
	}
	s.ReloadRule(ctx, id)
	return nil
}

// ListHistory returns the last N history entries for a rule.
func (s *ReplicationService) ListHistory(ctx context.Context, ruleID string, limit int) ([]domain.ReplicationHistory, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.repo.ListHistory(ctx, ruleID, limit)
}
```

- [ ] **Step 2: Build to verify no errors**

```bash
go build ./internal/service/...
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/service/replication_service.go
git commit -m "feat(service): add ReplicationService with cron scheduler and AES-256-GCM credential encryption"
```

---

## Task 6: Service Tests

**Files:**
- Create: `internal/service/replication_service_test.go`

- [ ] **Step 1: Write failing tests**

```go
package service_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func nopReplLog() *zap.SugaredLogger { return zap.NewNop().Sugar() }

func newTestReplicationService(t *testing.T) *service.ReplicationService {
	t.Helper()
	return service.NewReplicationService(
		testutil.NewReplicationRepo(),
		testutil.NewAssetRepo(),
		testutil.NewBlobStore(),
		"test-jwt-secret-32-bytes-long!!!",
		nopReplLog(),
	)
}

func TestReplicationService_EncryptDecrypt(t *testing.T) {
	svc := newTestReplicationService(t)

	plain := "super-secret-password"
	enc, err := svc.EncryptPassword(plain)
	if err != nil {
		t.Fatalf("EncryptPassword: %v", err)
	}
	if enc == plain {
		t.Fatal("EncryptPassword returned plaintext unchanged")
	}

	got, err := svc.DecryptPassword(enc)
	if err != nil {
		t.Fatalf("DecryptPassword: %v", err)
	}
	if got != plain {
		t.Fatalf("DecryptPassword: want %q got %q", plain, got)
	}
}

func TestReplicationService_EncryptEmpty(t *testing.T) {
	svc := newTestReplicationService(t)
	enc, err := svc.EncryptPassword("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if enc != "" {
		t.Fatalf("want empty enc for empty plain, got %q", enc)
	}
}

func TestReplicationService_RunRule_PushesNewAssets(t *testing.T) {
	// Target server: tracks received PUTs and serves empty asset list.
	var pushed []string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/service/rest/v1/assets") {
			fmt.Fprint(w, `{"items":[],"continuationToken":null}`)
			return
		}
		if r.Method == http.MethodPut {
			pushed = append(pushed, r.URL.Path)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer target.Close()

	assetRepo := testutil.NewAssetRepo()
	compRepo  := testutil.NewComponentRepo()
	blobStore := testutil.NewBlobStore()

	ctx := context.Background()

	// Seed one local component + asset.
	comp := &domain.Component{RepositoryName: "my-repo", Format: "raw", Name: "lib", Version: "1.0"}
	_ = compRepo.Create(ctx, comp)
	asset := &domain.Asset{
		ComponentID: comp.ID,
		Repository:  "my-repo",
		Path:        "lib/1.0/lib.jar",
		BlobKey:     "blobkey-1",
		SizeBytes:   5,
	}
	_ = assetRepo.Create(ctx, asset)
	_ = blobStore.Put(ctx, "blobkey-1", strings.NewReader("hello"), 5)

	replRepo := testutil.NewReplicationRepo()
	svc := service.NewReplicationService(replRepo, assetRepo, blobStore, "test-secret-32-bytes-long!!!", nopReplLog())

	enc, _ := svc.EncryptPassword("pass")
	rule := &domain.ReplicationRule{
		Name:              "test-rule",
		SourceRepo:        "my-repo",
		TargetURL:         target.URL,
		TargetRepo:        "my-repo-mirror",
		TargetUsername:    "admin",
		TargetPasswordEnc: enc,
		CronExpr:          "0 2 * * *",
		Enabled:           true,
	}
	if err := replRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if err := svc.RunRule(ctx, rule.ID); err != nil {
		t.Fatalf("RunRule: %v", err)
	}

	if len(pushed) != 1 {
		t.Fatalf("expected 1 pushed asset, got %d: %v", len(pushed), pushed)
	}

	history, _ := svc.ListHistory(ctx, rule.ID, 10)
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].PushedCount != 1 {
		t.Fatalf("expected PushedCount=1, got %d", history[0].PushedCount)
	}
	if history[0].SkippedCount != 0 {
		t.Fatalf("expected SkippedCount=0, got %d", history[0].SkippedCount)
	}
}

func TestReplicationService_RunRule_SkipsExistingAssets(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/service/rest/v1/assets") {
			// Report the asset as already present.
			fmt.Fprint(w, `{"items":[{"path":"lib/1.0/lib.jar"}],"continuationToken":null}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError) // should not be called
	}))
	defer target.Close()

	assetRepo := testutil.NewAssetRepo()
	compRepo  := testutil.NewComponentRepo()
	blobStore := testutil.NewBlobStore()
	ctx := context.Background()

	comp := &domain.Component{RepositoryName: "repo-a", Format: "raw", Name: "lib", Version: "1.0"}
	_ = compRepo.Create(ctx, comp)
	asset := &domain.Asset{
		ComponentID: comp.ID,
		Repository:  "repo-a",
		Path:        "lib/1.0/lib.jar",
		BlobKey:     "blobkey-2",
		SizeBytes:   3,
	}
	_ = assetRepo.Create(ctx, asset)

	replRepo := testutil.NewReplicationRepo()
	svc := service.NewReplicationService(replRepo, assetRepo, blobStore, "another-secret-32-bytes-long!!", nopReplLog())

	rule := &domain.ReplicationRule{
		Name:       "skip-rule",
		SourceRepo: "repo-a",
		TargetURL:  target.URL,
		TargetRepo: "repo-a-mirror",
		CronExpr:   "0 3 * * *",
		Enabled:    true,
	}
	_ = replRepo.CreateRule(ctx, rule)

	if err := svc.RunRule(ctx, rule.ID); err != nil {
		t.Fatalf("RunRule: %v", err)
	}

	history, _ := svc.ListHistory(ctx, rule.ID, 10)
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry")
	}
	if history[0].SkippedCount != 1 {
		t.Fatalf("expected SkippedCount=1, got %d", history[0].SkippedCount)
	}
	if history[0].PushedCount != 0 {
		t.Fatalf("expected PushedCount=0, got %d", history[0].PushedCount)
	}
}

func TestReplicationService_TestConnection_OK(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/service/rest/v1/status" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer target.Close()

	replRepo := testutil.NewReplicationRepo()
	svc := service.NewReplicationService(replRepo, testutil.NewAssetRepo(), testutil.NewBlobStore(), "secret-key-32-bytes-long-padded!", nopReplLog())
	ctx := context.Background()

	rule := &domain.ReplicationRule{
		Name: "conn-test", SourceRepo: "r", TargetURL: target.URL,
		TargetRepo: "r", CronExpr: "0 2 * * *", Enabled: true,
	}
	_ = replRepo.CreateRule(ctx, rule)

	if err := svc.TestConnection(ctx, rule.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReplicationService_RunRule_NotFound(t *testing.T) {
	svc := newTestReplicationService(t)
	err := svc.RunRule(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent rule")
	}
}

// Verify ListHistory respects limit.
func TestReplicationService_ListHistory_Limit(t *testing.T) {
	svc := newTestReplicationService(t)
	ctx := context.Background()
	rule := &domain.ReplicationRule{
		Name: "hist-rule", SourceRepo: "r", TargetURL: "http://localhost",
		TargetRepo: "r", CronExpr: "0 2 * * *", Enabled: true,
	}
	_ = svc.GetRule(ctx, "") // warm up
	replRepo := testutil.NewReplicationRepo()
	_ = replRepo.CreateRule(ctx, rule)

	now := time.Now()
	fin := now.Add(time.Second)
	for i := 0; i < 5; i++ {
		_ = replRepo.AddHistory(ctx, &domain.ReplicationHistory{
			RuleID: rule.ID, StartedAt: now, FinishedAt: &fin,
		})
	}
	hist, err := replRepo.ListHistory(ctx, rule.ID, 3)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(hist) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(hist))
	}
}

// ensure BlobStore mock satisfies storage.BlobStore — tested implicitly via compile.
var _ io.Closer = io.NopCloser(nil)
```

- [ ] **Step 2: Run tests to verify they compile and pass**

```bash
go test ./internal/service/... -run TestReplication -v
```

Expected: all `TestReplication*` tests PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/service/replication_service_test.go
git commit -m "test(service): add ReplicationService unit tests"
```

---

## Task 7: HTTP Handler

**Files:**
- Create: `internal/api/handlers/replication.go`

- [ ] **Step 1: Create `internal/api/handlers/replication.go`**

```go
package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
)

type ReplicationHandler struct {
	svc *service.ReplicationService
}

func NewReplicationHandler(svc *service.ReplicationService) *ReplicationHandler {
	return &ReplicationHandler{svc: svc}
}

// List handles GET /api/v1/replication/rules
func (h *ReplicationHandler) List(c *gin.Context) {
	rules, err := h.svc.ListRules(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rules)
}

type ruleInput struct {
	Name           string `json:"name"`
	SourceRepo     string `json:"source_repo"`
	TargetURL      string `json:"target_url"`
	TargetRepo     string `json:"target_repo"`
	TargetUsername string `json:"target_username"`
	TargetPassword string `json:"target_password"` // plaintext, never stored
	CronExpr       string `json:"cron_expr"`
	Enabled        bool   `json:"enabled"`
}

// Create handles POST /api/v1/replication/rules
func (h *ReplicationHandler) Create(c *gin.Context) {
	var inp ruleInput
	if err := c.ShouldBindJSON(&inp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if inp.Name == "" || inp.SourceRepo == "" || inp.TargetURL == "" || inp.TargetRepo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name, source_repo, target_url, target_repo are required"})
		return
	}
	if inp.CronExpr == "" {
		inp.CronExpr = "0 2 * * *"
	}
	rule := &domain.ReplicationRule{
		Name:           inp.Name,
		SourceRepo:     inp.SourceRepo,
		TargetURL:      inp.TargetURL,
		TargetRepo:     inp.TargetRepo,
		TargetUsername: inp.TargetUsername,
		CronExpr:       inp.CronExpr,
		Enabled:        inp.Enabled,
	}
	if err := h.svc.CreateRule(c.Request.Context(), rule, inp.TargetPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	go h.svc.ReloadRule(c.Request.Context(), rule.ID)
	c.JSON(http.StatusCreated, rule)
}

// Update handles PUT /api/v1/replication/rules/:id
func (h *ReplicationHandler) Update(c *gin.Context) {
	var inp ruleInput
	if err := c.ShouldBindJSON(&inp); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rule := &domain.ReplicationRule{
		ID:             c.Param("id"),
		Name:           inp.Name,
		SourceRepo:     inp.SourceRepo,
		TargetURL:      inp.TargetURL,
		TargetRepo:     inp.TargetRepo,
		TargetUsername: inp.TargetUsername,
		CronExpr:       inp.CronExpr,
		Enabled:        inp.Enabled,
	}
	if err := h.svc.UpdateRule(c.Request.Context(), rule, inp.TargetPassword); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	go h.svc.ReloadRule(c.Request.Context(), rule.ID)
	c.JSON(http.StatusOK, rule)
}

// Delete handles DELETE /api/v1/replication/rules/:id
func (h *ReplicationHandler) Delete(c *gin.Context) {
	if err := h.svc.DeleteRule(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ManualRun handles POST /api/v1/replication/rules/:id/run
func (h *ReplicationHandler) ManualRun(c *gin.Context) {
	id := c.Param("id")
	rule, err := h.svc.GetRule(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if rule == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "rule not found"})
		return
	}
	go func() {
		if err := h.svc.RunRule(c.Request.Context(), id); err != nil {
			// Error is recorded in history; no further action needed here.
			_ = err
		}
	}()
	c.JSON(http.StatusAccepted, gin.H{"message": "replication started"})
}

// TestConnection handles POST /api/v1/replication/rules/:id/test
func (h *ReplicationHandler) TestConnection(c *gin.Context) {
	err := h.svc.TestConnection(c.Request.Context(), c.Param("id"))
	if err != nil {
		if err.Error() == "rule not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "rule not found"})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ListHistory handles GET /api/v1/replication/rules/:id/history
func (h *ReplicationHandler) ListHistory(c *gin.Context) {
	limit := 20
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	hist, err := h.svc.ListHistory(c.Request.Context(), c.Param("id"), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, hist)
}
```

- [ ] **Step 2: Build to verify no errors**

```bash
go build ./internal/api/handlers/...
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/api/handlers/replication.go
git commit -m "feat(handlers): add ReplicationHandler with CRUD, manual run, test connection, and history endpoints"
```

---

## Task 8: Wire into Router

**Files:**
- Modify: `internal/api/router.go`

- [ ] **Step 1: Add `replicationRepo` and services to router**

In `internal/api/router.go`, after line `rrRepo := postgres.NewRoutingRuleRepo(pool)` (around line 79), add:

```go
replRepo := postgres.NewReplicationRepo(pool)
```

After `cleanupSvc.WithLocker(locker)` (around line 130), add:

```go
replSvc := service.NewReplicationService(replRepo, assetRepo, localBlob, cfg.Auth.JWTSecret, log)
go replSvc.StartCronScheduler(context.Background())
```

- [ ] **Step 2: Add handler and routes**

After `webhookH := handlers.NewWebhookHandler(webhookSvc)` (around line 179), add:

```go
replH := handlers.NewReplicationHandler(replSvc)
```

In the `authed` group section (after the existing webhook reads), add:

```go
// ── Replication rules (read) ──────────────────────────────────
authed.GET("/api/v1/replication/rules", replH.List)
authed.GET("/api/v1/replication/rules/:id/history", replH.ListHistory)
```

In the `admin` group section (after the webhook admin routes), add:

```go
// ── Replication rules (write) ─────────────────────────────────
admin.POST("/api/v1/replication/rules", replH.Create)
admin.PUT("/api/v1/replication/rules/:id", replH.Update)
admin.DELETE("/api/v1/replication/rules/:id", replH.Delete)
admin.POST("/api/v1/replication/rules/:id/run", replH.ManualRun)
admin.POST("/api/v1/replication/rules/:id/test", replH.TestConnection)
```

- [ ] **Step 3: Build the full project**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 4: Run all tests**

```bash
go test ./... 2>&1 | tail -20
```

Expected: all pass, count ≥ previous (387+).

- [ ] **Step 5: Commit**

```bash
git add internal/api/router.go
git commit -m "feat(router): wire ReplicationService and ReplicationHandler with cron scheduler"
```

---

## Task 9: Frontend — API Client

**Files:**
- Modify: `frontend/src/api/client.ts`

- [ ] **Step 1: Add types after `RoutingRule` interface (around line 110)**

```typescript
export interface ReplicationRule {
  id: string
  name: string
  source_repo: string
  target_url: string
  target_repo: string
  target_username: string
  cron_expr: string
  enabled: boolean
  last_run_at: string | null
  last_run_status: string
  created_at: string
}

export interface ReplicationHistory {
  id: string
  rule_id: string
  started_at: string
  finished_at: string | null
  duration_ms: number
  pushed_count: number
  skipped_count: number
  failed_count: number
  transferred_bytes: number
  error: string
}

export interface ReplicationRuleInput {
  name: string
  source_repo: string
  target_url: string
  target_repo: string
  target_username: string
  target_password: string
  cron_expr: string
  enabled: boolean
}
```

- [ ] **Step 2: Add API methods to `nexspenceApi` object (before the closing `}`)**

```typescript
// Replication rules
listReplicationRules: () =>
  apiClient.get<ReplicationRule[]>('/api/v1/replication/rules'),
createReplicationRule: (data: ReplicationRuleInput) =>
  apiClient.post<ReplicationRule>('/api/v1/replication/rules', data),
updateReplicationRule: (id: string, data: ReplicationRuleInput) =>
  apiClient.put<ReplicationRule>(`/api/v1/replication/rules/${id}`, data),
deleteReplicationRule: (id: string) =>
  apiClient.delete(`/api/v1/replication/rules/${id}`),
runReplicationRule: (id: string) =>
  apiClient.post(`/api/v1/replication/rules/${id}/run`),
testReplicationRule: (id: string) =>
  apiClient.post<{ ok: boolean }>(`/api/v1/replication/rules/${id}/test`),
listReplicationHistory: (id: string) =>
  apiClient.get<ReplicationHistory[]>(`/api/v1/replication/rules/${id}/history`),
```

- [ ] **Step 3: Build frontend to verify no type errors**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/api/client.ts
git commit -m "feat(frontend/api): add ReplicationRule types and API methods"
```

---

## Task 10: Frontend — ReplicationTab + AdminPage Tab

**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

- [ ] **Step 1: Update imports at top of `AdminPage.tsx`**

The `GitBranch` icon is already imported (used for routing-rules tab). Add `Share2` to the lucide-react import for the replication tab icon:

```typescript
import { Activity, Archive, ArrowRightLeft, CheckCircle, Database, Download, GitBranch, HardDrive, Info, Network, Paperclip, Pause, Pencil, Play, Plus, RefreshCw, Share2, Trash2, Upload, Wifi, X } from 'lucide-react'
```

Add replication types to the api/client import:

```typescript
import { nexusApi, nexspenceApi, ImportRepoStats, ServiceStatus, RoutingRule, RoutingRuleInput, ReplicationRule, ReplicationHistory, ReplicationRuleInput } from '@/api/client'
```

- [ ] **Step 2: Update `AdminTab` union and `VALID_TABS`**

Change line 26-27:

```typescript
type AdminTab = 'info' | 'blobs' | 'backup' | 'monitoring' | 'migration' | 'routing-rules' | 'replication'
const VALID_TABS: AdminTab[] = ['info', 'blobs', 'backup', 'monitoring', 'migration', 'routing-rules', 'replication']
```

- [ ] **Step 3: Add `ReplicationTab` component before `export default function AdminPage()`**

Insert the following component (around line 241, just before `export default function AdminPage()`):

```typescript
function ReplicationTab() {
  const qc = useQueryClient()
  const { data: rules = [], isLoading } = useQuery<ReplicationRule[]>({
    queryKey: ['replication-rules'],
    queryFn: () => nexspenceApi.listReplicationRules().then(r => r.data),
  })

  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<ReplicationRule | null>(null)
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [history, setHistory] = useState<ReplicationHistory[]>([])
  const [histLoading, setHistLoading] = useState(false)
  const [testResult, setTestResult] = useState<Record<string, string>>({})

  const [form, setForm] = useState<ReplicationRuleInput>({
    name: '', source_repo: '', target_url: '', target_repo: '',
    target_username: '', target_password: '', cron_expr: '0 2 * * *', enabled: true,
  })

  const { data: repos = [] } = useQuery({
    queryKey: ['repos-list'],
    queryFn: () => nexusApi.listRepositories().then(r => r.data as Array<{ name: string }>),
  })

  const createMutation = useMutation({
    mutationFn: (data: ReplicationRuleInput) => nexspenceApi.createReplicationRule(data),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['replication-rules'] }); setModalOpen(false) },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: ReplicationRuleInput }) =>
      nexspenceApi.updateReplicationRule(id, data),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['replication-rules'] }); setModalOpen(false) },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => nexspenceApi.deleteReplicationRule(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['replication-rules'] }),
  })

  const runMutation = useMutation({
    mutationFn: (id: string) => nexspenceApi.runReplicationRule(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['replication-rules'] }),
  })

  function openCreate() {
    setEditing(null)
    setForm({ name: '', source_repo: '', target_url: '', target_repo: '', target_username: '', target_password: '', cron_expr: '0 2 * * *', enabled: true })
    setModalOpen(true)
  }

  function openEdit(rule: ReplicationRule) {
    setEditing(rule)
    setForm({
      name: rule.name, source_repo: rule.source_repo, target_url: rule.target_url,
      target_repo: rule.target_repo, target_username: rule.target_username,
      target_password: '', cron_expr: rule.cron_expr, enabled: rule.enabled,
    })
    setModalOpen(true)
  }

  async function toggleHistory(rule: ReplicationRule) {
    if (expandedId === rule.id) { setExpandedId(null); return }
    setExpandedId(rule.id)
    setHistLoading(true)
    try {
      const r = await nexspenceApi.listReplicationHistory(rule.id)
      setHistory(r.data)
    } finally {
      setHistLoading(false)
    }
  }

  async function testConn(id: string) {
    setTestResult(prev => ({ ...prev, [id]: 'testing…' }))
    try {
      await nexspenceApi.testReplicationRule(id)
      setTestResult(prev => ({ ...prev, [id]: '✓ Connected' }))
    } catch (e: unknown) {
      const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error || 'Failed'
      setTestResult(prev => ({ ...prev, [id]: `✗ ${msg}` }))
    }
    setTimeout(() => setTestResult(prev => { const n = { ...prev }; delete n[id]; return n }), 5000)
  }

  function handleSubmit() {
    if (editing) {
      updateMutation.mutate({ id: editing.id, data: form })
    } else {
      createMutation.mutate(form)
    }
  }

  const fmtDate = (s: string | null) => s ? new Date(s).toLocaleString() : '—'
  const fmtDur = (ms: number) => ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <span style={{ color: '#94a3b8', fontSize: 13 }}>
          Push artifacts from local repositories to remote Nexspence instances on a cron schedule.
        </span>
        <HoloButton onClick={openCreate} size="sm">
          <Plus size={13} style={{ marginRight: 5 }} /> New Rule
        </HoloButton>
      </div>

      {isLoading && <p style={{ color: '#64748b' }}>Loading…</p>}

      {!isLoading && rules.length === 0 && (
        <p style={{ color: '#64748b', fontSize: 13 }}>No replication rules configured.</p>
      )}

      {rules.map(rule => (
        <HoloCard key={rule.id} style={{ marginBottom: 10 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
            <div>
              <div style={{ fontWeight: 600, color: '#e2e8f0', marginBottom: 4 }}>{rule.name}</div>
              <div style={{ fontSize: 12, color: '#64748b' }}>
                <span style={{ color: '#94a3b8' }}>{rule.source_repo}</span>
                {' → '}
                <span style={{ color: '#94a3b8' }}>{rule.target_url}/{rule.target_repo}</span>
              </div>
              <div style={{ fontSize: 11, color: '#475569', marginTop: 4 }}>
                <span>cron: {rule.cron_expr}</span>
                {' · '}
                <span style={{ color: rule.enabled ? '#22c55e' : '#ef4444' }}>
                  {rule.enabled ? 'enabled' : 'disabled'}
                </span>
                {rule.last_run_at && (
                  <>
                    {' · last run: '}
                    <span style={{ color: rule.last_run_status === 'ok' ? '#22c55e' : rule.last_run_status === 'error' ? '#ef4444' : '#f59e0b' }}>
                      {rule.last_run_status}
                    </span>
                    {' '}{fmtDate(rule.last_run_at)}
                  </>
                )}
                {testResult[rule.id] && (
                  <span style={{ marginLeft: 8, color: testResult[rule.id].startsWith('✓') ? '#22c55e' : '#ef4444' }}>
                    {testResult[rule.id]}
                  </span>
                )}
              </div>
            </div>
            <div style={{ display: 'flex', gap: 6 }}>
              <HoloButton size="sm" variant="ghost" onClick={() => testConn(rule.id)} title="Test connection">
                <Wifi size={12} />
              </HoloButton>
              <HoloButton size="sm" variant="ghost" onClick={() => runMutation.mutate(rule.id)} title="Run now">
                <Play size={12} />
              </HoloButton>
              <HoloButton size="sm" variant="ghost" onClick={() => toggleHistory(rule)} title="History">
                <Activity size={12} />
              </HoloButton>
              <HoloButton size="sm" variant="ghost" onClick={() => openEdit(rule)} title="Edit">
                <Pencil size={12} />
              </HoloButton>
              <HoloButton size="sm" variant="ghost" onClick={() => deleteMutation.mutate(rule.id)} title="Delete">
                <Trash2 size={12} />
              </HoloButton>
            </div>
          </div>

          {expandedId === rule.id && (
            <div style={{ marginTop: 12, borderTop: '1px solid rgba(255,255,255,0.06)', paddingTop: 10 }}>
              {histLoading && <p style={{ color: '#64748b', fontSize: 12 }}>Loading history…</p>}
              {!histLoading && history.length === 0 && (
                <p style={{ color: '#64748b', fontSize: 12 }}>No runs recorded yet.</p>
              )}
              {!histLoading && history.length > 0 && (
                <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                  <thead>
                    <tr style={{ color: '#64748b', textAlign: 'left' }}>
                      <th style={{ padding: '4px 8px' }}>Started</th>
                      <th style={{ padding: '4px 8px' }}>Duration</th>
                      <th style={{ padding: '4px 8px' }}>Pushed</th>
                      <th style={{ padding: '4px 8px' }}>Skipped</th>
                      <th style={{ padding: '4px 8px' }}>Failed</th>
                      <th style={{ padding: '4px 8px' }}>Bytes</th>
                      <th style={{ padding: '4px 8px' }}>Error</th>
                    </tr>
                  </thead>
                  <tbody>
                    {history.map(h => (
                      <tr key={h.id} style={{ color: '#94a3b8', borderTop: '1px solid rgba(255,255,255,0.04)' }}>
                        <td style={{ padding: '4px 8px' }}>{fmtDate(h.started_at)}</td>
                        <td style={{ padding: '4px 8px' }}>{fmtDur(h.duration_ms)}</td>
                        <td style={{ padding: '4px 8px', color: '#22c55e' }}>{h.pushed_count}</td>
                        <td style={{ padding: '4px 8px' }}>{h.skipped_count}</td>
                        <td style={{ padding: '4px 8px', color: h.failed_count > 0 ? '#ef4444' : '#94a3b8' }}>{h.failed_count}</td>
                        <td style={{ padding: '4px 8px' }}>{fmtBytes(h.transferred_bytes)}</td>
                        <td style={{ padding: '4px 8px', color: '#ef4444', maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{h.error || '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          )}
        </HoloCard>
      ))}

      <HoloModal
        open={modalOpen}
        onClose={() => setModalOpen(false)}
        title={editing ? 'Edit Replication Rule' : 'New Replication Rule'}
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <HoloInput label="Rule Name" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="prod-mirror" />
          <div>
            <label style={{ fontSize: 12, color: '#94a3b8', display: 'block', marginBottom: 4 }}>Source Repository</label>
            <Select
              value={form.source_repo}
              onChange={v => setForm(f => ({ ...f, source_repo: v }))}
              options={repos.map((r: { name: string }) => ({ value: r.name, label: r.name }))}
              placeholder="Select repository…"
            />
          </div>
          <HoloInput label="Target URL" value={form.target_url} onChange={e => setForm(f => ({ ...f, target_url: e.target.value }))} placeholder="https://nexspence.example.com" />
          <HoloInput label="Target Repository" value={form.target_repo} onChange={e => setForm(f => ({ ...f, target_repo: e.target.value }))} placeholder="my-repo-mirror" />
          <HoloInput label="Target Username" value={form.target_username} onChange={e => setForm(f => ({ ...f, target_username: e.target.value }))} placeholder="admin" />
          <HoloInput
            label={editing ? 'Target Password (leave blank to keep existing)' : 'Target Password'}
            type="password"
            value={form.target_password}
            onChange={e => setForm(f => ({ ...f, target_password: e.target.value }))}
          />
          <HoloInput label="Cron Expression" value={form.cron_expr} onChange={e => setForm(f => ({ ...f, cron_expr: e.target.value }))} placeholder="0 2 * * *" />
          <label style={{ fontSize: 12, color: '#94a3b8', display: 'flex', alignItems: 'center', gap: 8 }}>
            <input type="checkbox" checked={form.enabled} onChange={e => setForm(f => ({ ...f, enabled: e.target.checked }))} />
            Enabled
          </label>
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 4 }}>
            <HoloButton variant="ghost" onClick={() => setModalOpen(false)}>Cancel</HoloButton>
            <HoloButton onClick={handleSubmit} disabled={createMutation.isPending || updateMutation.isPending}>
              {editing ? 'Save' : 'Create'}
            </HoloButton>
          </div>
        </div>
      </HoloModal>
    </div>
  )
}
```

- [ ] **Step 4: Add Replication tab entry to `<HoloTabs>` (around line 372)**

```typescript
{ value: 'replication', label: <><Share2 size={13} style={{ marginRight: 5 }} />Replication</> },
```

- [ ] **Step 5: Add tab render (after `{tab === 'routing-rules' && <RoutingRulesTab />}` around line 793)**

```typescript
{tab === 'replication' && <ReplicationTab />}
```

- [ ] **Step 6: TypeScript check**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -30
```

Expected: no output.

- [ ] **Step 7: Build frontend**

```bash
cd frontend && npm run build 2>&1 | tail -10
```

Expected: build succeeds, no errors.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/pages/AdminPage.tsx
git commit -m "feat(frontend): add ReplicationTab with rule management, run, test connection, and history"
```

---

## Task 11: Update MD Files

**Files:**
- Modify: `NEXT_RELEASE.md`
- Modify: `task_plan.md`
- Modify: `CLAUDE.md`
- Modify: `README.md`
- Modify: `../nexspence-demo/README.md`

- [ ] **Step 1: Update `NEXT_RELEASE.md`** — append:

```markdown
### ✨ Features

**Phase 55 — Content Replication**
Automated push-replication of artifacts from a local repository to a remote Nexspence instance on a configurable cron schedule. Credentials are stored AES-256-GCM encrypted (keyed on the JWT secret). Per-rule run history (pushed/skipped/failed counts, bytes transferred) is recorded in `replication_history`. Admin UI in System Admin → Replication tab: rule management, manual run, Test Connection, and expandable history per rule.
```

- [ ] **Step 2: Mark Phase 55 complete in `task_plan.md`**

Find the line `**Status:** backlog` under `## Phase 55` and change to:

```
**Status:** complete (2026-05-06)
```

Change the task checkboxes:
```
- [x] DB: таблица `replication_rules` ...
- [x] `ReplicationService`: по cron ...
- [x] API: `POST/GET/DELETE /api/v1/replication/rules` ...
- [x] Frontend: AdminPage → Replication tab ...
- [x] Мониторинг: `replication_history` таблица ...
```

- [ ] **Step 3: Update `CLAUDE.md` current phase line**

Find the line starting with `See \`task_plan.md\` for phase status. Currently:` and update to reflect Phase 55 complete.

- [ ] **Step 4: Update `README.md`** — add Replication to the feature list (find the section listing Phase features and add a bullet for Content Replication).

- [ ] **Step 5: Copy same README change to `../nexspence-demo/README.md`**

- [ ] **Step 6: Run full test suite one last time**

```bash
go test ./... 2>&1 | tail -5
```

Expected: all pass.

- [ ] **Step 7: Commit everything**

```bash
git add NEXT_RELEASE.md task_plan.md CLAUDE.md README.md
git commit -m "docs: mark Phase 55 complete; update NEXT_RELEASE.md, task_plan.md, CLAUDE.md, README"
cd ../nexspence-demo && git add README.md && git commit -m "docs: add Content Replication feature (Phase 55)"
```
