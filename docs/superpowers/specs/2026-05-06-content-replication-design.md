# Phase 55: Content Replication — Design

**Date:** 2026-05-06  
**Status:** Approved

## Summary

Push artifacts from a local Nexspence repository to a remote Nexspence instance on a cron schedule. Targets geographically distributed teams sharing artifact infrastructure.

## Design Decisions

| Decision | Choice | Reason |
|----------|--------|--------|
| Duplicate detection | By `asset.Path` | Paths are unique within a repo; replication targets new artifacts, not content changes |
| Credentials storage | AES-256-GCM encrypted, JWT secret as key | Security without external KMS dependency |
| Error handling | Skip & continue | A single bad blob must not abort a large repo sync |
| Filter glob | None (Phase 55) | YAGNI — replicate whole repo; filtering deferred |

---

## Database

### `replication_rules`

| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID PK | |
| `name` | TEXT UNIQUE NOT NULL | human label |
| `source_repo` | TEXT NOT NULL | local repo name |
| `target_url` | TEXT NOT NULL | base URL of target Nexspence |
| `target_repo` | TEXT NOT NULL | repo name on target |
| `target_username` | TEXT NOT NULL DEFAULT '' | |
| `target_password_enc` | TEXT NOT NULL DEFAULT '' | AES-256-GCM, base64url, nonce prepended |
| `cron_expr` | TEXT NOT NULL DEFAULT `'0 2 * * *'` | standard cron |
| `enabled` | BOOLEAN NOT NULL DEFAULT true | |
| `last_run_at` | TIMESTAMPTZ | updated after each run |
| `last_run_status` | TEXT | `'ok'` / `'error'` / `'running'` |
| `created_at` | TIMESTAMPTZ | |

### `replication_history`

| Column | Type | Notes |
|--------|------|-------|
| `id` | UUID PK | |
| `rule_id` | UUID FK → replication_rules ON DELETE CASCADE | |
| `started_at` | TIMESTAMPTZ | |
| `finished_at` | TIMESTAMPTZ | |
| `duration_ms` | BIGINT | |
| `pushed_count` | INT | new assets successfully transferred |
| `skipped_count` | INT | already present on target |
| `failed_count` | INT | errors during transfer |
| `transferred_bytes` | BIGINT | |
| `error` | TEXT | first/last error message |

Index: `(rule_id, started_at DESC)`

---

## Backend Architecture

### Domain Types (`internal/domain/types.go`)

```go
type ReplicationRule struct {
    ID                 string
    Name               string
    SourceRepo         string
    TargetURL          string
    TargetRepo         string
    TargetUsername     string
    TargetPasswordEnc  string  // stored encrypted
    CronExpr           string
    Enabled            bool
    LastRunAt          *time.Time
    LastRunStatus      string
    CreatedAt          time.Time
}

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

### Repository Interface (`internal/repository/interfaces.go`)

```go
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

### Service (`internal/service/replication_service.go`)

**Encryption helpers:**  
`encryptPassword(plain, jwtSecret string) (string, error)` — SHA-256(jwtSecret) → 32-byte AES key, random 12-byte nonce, GCM seal, `base64.URLEncoding(nonce+ciphertext)`.  
`decryptPassword(enc, jwtSecret string) (string, error)` — inverse.

**Cron scheduler:**  
Same pattern as `CleanupService` — `robfig/cron/v3`, `StartCronScheduler(ctx, defaultExpr)`, `map[ruleID]cron.EntryID` with mutex.  
On `CreateRule`/`UpdateRule`/`DeleteRule` the scheduler entry is re-registered/removed.

**`runRule(ctx, rule)`:**
1. `AssetRepo.ListByRepoAndPath(ctx, rule.SourceRepo, "")` — local assets
2. Paginate `GET {targetURL}/service/rest/v1/assets?repository={targetRepo}` — build `map[path]struct{}`
3. For each local asset whose path is absent in target map:
   - `BlobStore.Fetch(asset.BlobKey)` → `io.Reader`
   - `PUT {targetURL}/repository/{targetRepo}/{asset.Path}` with `Authorization: Basic ...`
   - On error: `failedCount++`, log, continue
4. Write `replication_history` row; update `last_run_at` / `last_run_status` on rule

**`TestConnection(ctx, ruleID)`:**  
`HEAD {targetURL}/service/rest/v1/status` with credentials — returns `nil` on 200.

### Handler (`internal/api/handlers/replication.go`)

```
GET    /api/v1/replication/rules               → List
POST   /api/v1/replication/rules               → Create (admin)
PUT    /api/v1/replication/rules/:id           → Update (admin)
DELETE /api/v1/replication/rules/:id           → Delete (admin)
POST   /api/v1/replication/rules/:id/run       → ManualRun (admin, async goroutine)
POST   /api/v1/replication/rules/:id/test      → TestConnection (admin)
GET    /api/v1/replication/rules/:id/history   → ListHistory
```

Password handling: API accepts `target_password` (plaintext) in Create/Update payload; handler encrypts before storing. `target_password_enc` is never returned in GET responses — replaced with `"*"` sentinel.

---

## Frontend

### AdminPage changes

- `AdminTab` union extended: add `'replication'`
- `VALID_TABS` array updated
- `<HoloTabs>` gets new entry: `GitBranch` icon + label "Replication"
- Tab renders `<ReplicationTab />`

### `ReplicationTab` component (inline in `AdminPage.tsx`)

**Rules table** columns: Name | Source → Target | Schedule | Enabled | Last Run | Actions  
Actions per row: ▶ Run, ✏ Edit, 🗑 Delete  
History: clicking a row expands inline to show last 10 `replication_history` entries (started_at, duration, pushed/skipped/failed, bytes, error).

**Create/Edit modal** (2-step Wizard):
- Step 1: Name, Source Repo (Select from local repos), Target URL, Target Repo, Username, Password
- Step 2: Cron expression + **Test Connection** button (calls `POST /api/v1/replication/rules/:id/test`, shows ✓ or error)

---

## Out of Scope (Phase 55)

- Filter globs (path-based filtering)
- Bi-directional sync
- Conflict resolution for modified artifacts
- Pull mode (target pulls from source)
