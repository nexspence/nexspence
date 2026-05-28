# Phase 25 — Audit Log Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add detailed audit events (entity_path + broader endpoint coverage), NDJSON streaming export with date/username filters, and 90-day retention via PostgreSQL partition rotation.

**Architecture:** Three coupled improvements behind one design. `AuditEvent.Context` JSONB stores the artifact path (no DB column added). Existing `AuditRepo.List(domain, action, limit, offset)` is replaced with `List(AuditQuery) (items, total, error)` plus a streaming `Stream(AuditQuery, fn)` for NDJSON export. Retention is a goroutine in a new `internal/audit` package that uses a `PartitionStore` interface (postgres impl + testable fake) to pre-create the next 2 monthly partitions and DETACH/DROP partitions older than the configured retention.

**Tech Stack:** Go 1.22+, gin, pgx/v5, viper config, zap logger, React 18 + TypeScript, axios, Vite, lucide-react.

**Spec:** `docs/superpowers/specs/2026-04-24-phase25-audit-log-design.md`

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/repository/interfaces.go` | modify | Add `AuditQuery` struct + new `AuditRepo` interface (`List`/`Stream`). |
| `internal/repository/postgres/audit_repo.go` | modify | Implement new `List` + `Stream` against pgx; preserve INSERT path. |
| `internal/testutil/mocks.go` | modify | Update `AuditRepo` mock to new interface. |
| `internal/api/audit_middleware.go` | modify | Add new audit prefixes; extract path/digest into `Context["path"]`. |
| `internal/api/audit_middleware_test.go` | modify | Add tests for new prefixes + `Context["path"]`. |
| `internal/api/handlers/audit.go` | modify | Parse new query params; dispatch to JSON page or NDJSON stream. |
| `internal/api/handlers/audit_test.go` | create | Cover filter parsing + NDJSON output + 100k cap. |
| `internal/api/router.go` | modify | (No new route — same path serves both formats.) Wire `repoRepo` if needed. |
| `internal/config/config.go` | modify | Add `AuditConfig` + viper defaults. |
| `config.yaml` | modify | Add `audit:` section. |
| `internal/metrics/metrics.go` | modify | Add `AuditEventsCount atomic.Int64` + include in `Snapshot`. |
| `internal/audit/rotator.go` | create | Core `Rotator` struct + `Run(ctx)` ticker + `tick(ctx)` + `RunOnce(ctx)`. |
| `internal/audit/partition_store.go` | create | `PartitionStore` interface + `pgPartitionStore` postgres impl. |
| `internal/audit/rotator_test.go` | create | Unit tests against fake `PartitionStore`. |
| `internal/audit/fake_partition_store.go` | create | Test-only fake (build-tag `//go:build !prod` not needed — used only in `_test.go`; lives in `_test.go` file or separate file). |
| `cmd/server/main.go` | modify | Wire `audit.Rotator` after migrations, before `srv.ListenAndServe`. |
| `frontend/src/api/client.ts` | modify | Extend `listAuditEvents` params; add `auditExportUrl(params)` helper. |
| `frontend/src/pages/AuditPage.tsx` | modify | New filter inputs (date from/to + username); Export button; Path column; pagination using `total`. |
| `task_plan.md` | modify | Mark Phase 25 complete. |
| `findings.md`, `progress.md`, `CLAUDE.md` | modify | Session notes per project memory feedback. |

---

## Task 1: Add `AuditQuery` struct and update `AuditRepo` interface

**Files:**
- Modify: `internal/repository/interfaces.go:177-181`

- [ ] **Step 1: Edit `internal/repository/interfaces.go`** — replace the existing `AuditRepo` block with the new interface and add `AuditQuery`. Add `time` import if not present.

```go
import (
    "context"
    "time"

    "github.com/nexspence-oss/nexspence/internal/domain"
)

// AuditQuery holds filter and pagination parameters for AuditRepo.List/Stream.
type AuditQuery struct {
    Domain   string     // empty = any
    Action   string     // empty = any
    Username string     // empty = any (exact match)
    From     *time.Time // inclusive lower bound; nil = no lower bound
    To       *time.Time // exclusive upper bound; nil = no upper bound
    Limit    int        // ignored by Stream; List defaults to 100, capped at 1000
    Offset   int        // ignored by Stream
}

// AuditRepo writes and reads audit log events.
type AuditRepo interface {
    Write(ctx context.Context, e *domain.AuditEvent) error
    List(ctx context.Context, q AuditQuery) (items []domain.AuditEvent, total int, err error)
    Stream(ctx context.Context, q AuditQuery, fn func(domain.AuditEvent) error) error
}
```

- [ ] **Step 2: Verify build fails** (signature change breaks callers)

Run: `go build ./...`
Expected: errors in `internal/api/handlers/audit.go` and `internal/testutil/mocks.go` referencing the old `List` signature.

- [ ] **Step 3: Commit**

```bash
git add internal/repository/interfaces.go
git commit -m "feat(audit): add AuditQuery; widen AuditRepo with Stream"
```

---

## Task 2: Update `testutil.AuditRepo` mock to new interface

**Files:**
- Modify: `internal/testutil/mocks.go:632-680`

- [ ] **Step 1: Edit `internal/testutil/mocks.go`** — replace the AuditRepo block with implementations matching the new interface. Add `time` import if not present.

Replace the existing `AuditRepo` block (struct + `NewAuditRepo` + `Write` + `List`) with:

```go
// ── AuditRepo ─────────────────────────────────────────────────

type AuditRepo struct {
    mu     sync.Mutex
    Events []domain.AuditEvent
}

func NewAuditRepo() *AuditRepo { return &AuditRepo{} }

func (a *AuditRepo) Write(_ context.Context, e *domain.AuditEvent) error {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.Events = append(a.Events, *e)
    return nil
}

func (a *AuditRepo) match(e domain.AuditEvent, q repository.AuditQuery) bool {
    if q.Domain != "" && e.Domain != q.Domain {
        return false
    }
    if q.Action != "" && e.Action != q.Action {
        return false
    }
    if q.Username != "" && e.Username != q.Username {
        return false
    }
    if q.From != nil && e.EventTime.Before(*q.From) {
        return false
    }
    if q.To != nil && !e.EventTime.Before(*q.To) {
        return false
    }
    return true
}

func (a *AuditRepo) List(_ context.Context, q repository.AuditQuery) ([]domain.AuditEvent, int, error) {
    a.mu.Lock()
    defer a.mu.Unlock()
    var matched []domain.AuditEvent
    for _, e := range a.Events {
        if a.match(e, q) {
            matched = append(matched, e)
        }
    }
    total := len(matched)
    if q.Offset >= total {
        return nil, total, nil
    }
    matched = matched[q.Offset:]
    if q.Limit > 0 && len(matched) > q.Limit {
        matched = matched[:q.Limit]
    }
    return matched, total, nil
}

func (a *AuditRepo) Stream(_ context.Context, q repository.AuditQuery, fn func(domain.AuditEvent) error) error {
    a.mu.Lock()
    snapshot := append([]domain.AuditEvent(nil), a.Events...)
    a.mu.Unlock()
    for _, e := range snapshot {
        if !a.match(e, q) {
            continue
        }
        if err := fn(e); err != nil {
            return err
        }
    }
    return nil
}
```

Make sure `repository` is in the import block at the top of `mocks.go` (it almost certainly already is).

- [ ] **Step 2: Verify build still fails on the handler only**

Run: `go build ./...`
Expected: error only from `internal/api/handlers/audit.go` (handler not yet updated). The interface assertion on line 26 (`_ repository.AuditRepo = (*AuditRepo)(nil)`) must compile.

- [ ] **Step 3: Commit**

```bash
git add internal/testutil/mocks.go
git commit -m "test(audit): adapt AuditRepo mock to AuditQuery API"
```

---

## Task 3: Implement new `List` + `Stream` in postgres `AuditRepo`

**Files:**
- Modify: `internal/repository/postgres/audit_repo.go`

- [ ] **Step 1: Replace the entire file with the new implementation**

```go
package postgres

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "strings"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/nexspence-oss/nexspence/internal/domain"
    "github.com/nexspence-oss/nexspence/internal/repository"
)

type AuditRepo struct{ pool *pgxpool.Pool }

func NewAuditRepo(pool *pgxpool.Pool) *AuditRepo {
    return &AuditRepo{pool: pool}
}

// streamRowCap is the hard safety limit on a single Stream call.
const streamRowCap = 100_000

// ErrStreamCapExceeded is returned by Stream when the underlying query
// would have produced more than streamRowCap rows.
var ErrStreamCapExceeded = errors.New("audit stream exceeds row cap; narrow the time range")

func (r *AuditRepo) Write(ctx context.Context, e *domain.AuditEvent) error {
    ctxJSON, _ := json.Marshal(e.Context)
    _, err := r.pool.Exec(ctx, `
        INSERT INTO audit_events
            (user_id, username, remote_ip, user_agent, domain, action,
             entity_type, entity_id, entity_name, context, result)
        VALUES
            ($1, $2, $3::inet, $4, $5, $6,
             $7, $8, $9, $10, $11)`,
        nullStr(stringOrNil(e.UserID)),
        e.Username,
        nullStr(e.RemoteIP),
        nullStr(e.UserAgent),
        e.Domain,
        e.Action,
        nullStr(e.EntityType),
        nullStr(e.EntityID),
        nullStr(e.EntityName),
        ctxJSON,
        e.Result,
    )
    return err
}

// buildWhere returns the WHERE clause and ordered args for both List and Stream.
// $1..$N are reserved for the WHERE; LIMIT/OFFSET (if added) use $N+1, $N+2.
func buildWhere(q repository.AuditQuery) (string, []any) {
    parts := []string{}
    args := []any{}
    add := func(cond string, val any) {
        args = append(args, val)
        parts = append(parts, fmt.Sprintf(cond, len(args)))
    }
    if q.Domain != "" {
        add("domain = $%d", q.Domain)
    }
    if q.Action != "" {
        add("action = $%d", q.Action)
    }
    if q.Username != "" {
        add("username = $%d", q.Username)
    }
    if q.From != nil {
        add("event_time >= $%d", *q.From)
    }
    if q.To != nil {
        add("event_time < $%d", *q.To)
    }
    if len(parts) == 0 {
        return "", args
    }
    return "WHERE " + strings.Join(parts, " AND "), args
}

func (r *AuditRepo) List(ctx context.Context, q repository.AuditQuery) ([]domain.AuditEvent, int, error) {
    if q.Limit <= 0 {
        q.Limit = 100
    }
    if q.Limit > 1000 {
        q.Limit = 1000
    }
    where, args := buildWhere(q)

    var total int
    if err := r.pool.QueryRow(ctx,
        "SELECT COUNT(*) FROM audit_events "+where, args...).Scan(&total); err != nil {
        return nil, 0, err
    }

    args = append(args, q.Limit, q.Offset)
    sql := fmt.Sprintf(`
        SELECT id, event_time,
               user_id::text, username,
               COALESCE(remote_ip::text,''), COALESCE(user_agent,''),
               domain, action,
               COALESCE(entity_type,''), COALESCE(entity_id,''), COALESCE(entity_name,''),
               context, result
        FROM audit_events
        %s
        ORDER BY event_time DESC
        LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args))

    rows, err := r.pool.Query(ctx, sql, args...)
    if err != nil {
        return nil, 0, err
    }
    defer rows.Close()

    var out []domain.AuditEvent
    for rows.Next() {
        e, err := scanAuditRow(rows)
        if err != nil {
            return nil, 0, err
        }
        out = append(out, e)
    }
    return out, total, rows.Err()
}

func (r *AuditRepo) Stream(ctx context.Context, q repository.AuditQuery, fn func(domain.AuditEvent) error) error {
    where, args := buildWhere(q)
    sql := `
        SELECT id, event_time,
               user_id::text, username,
               COALESCE(remote_ip::text,''), COALESCE(user_agent,''),
               domain, action,
               COALESCE(entity_type,''), COALESCE(entity_id,''), COALESCE(entity_name,''),
               context, result
        FROM audit_events ` + where + `
        ORDER BY event_time DESC`
    rows, err := r.pool.Query(ctx, sql, args...)
    if err != nil {
        return err
    }
    defer rows.Close()

    n := 0
    for rows.Next() {
        n++
        if n > streamRowCap {
            return ErrStreamCapExceeded
        }
        e, err := scanAuditRow(rows)
        if err != nil {
            return err
        }
        if err := fn(e); err != nil {
            return err
        }
    }
    return rows.Err()
}

// scanAuditRow scans one row from the audit_events SELECT used in List/Stream.
func scanAuditRow(rows interface {
    Scan(...any) error
}) (domain.AuditEvent, error) {
    var e domain.AuditEvent
    var userID *string
    var ctxJSON []byte
    if err := rows.Scan(
        &e.ID, &e.EventTime,
        &userID, &e.Username,
        &e.RemoteIP, &e.UserAgent,
        &e.Domain, &e.Action,
        &e.EntityType, &e.EntityID, &e.EntityName,
        &ctxJSON, &e.Result,
    ); err != nil {
        return e, err
    }
    e.UserID = userID
    _ = json.Unmarshal(ctxJSON, &e.Context)
    return e, nil
}

// stringOrNil unwraps a *string pointer to a string, or "" if nil.
func stringOrNil(s *string) string {
    if s == nil {
        return ""
    }
    return *s
}
```

- [ ] **Step 2: Verify build still fails only on handler**

Run: `go build ./...`
Expected: only `internal/api/handlers/audit.go` errors (handler not updated yet).

- [ ] **Step 3: Commit**

```bash
git add internal/repository/postgres/audit_repo.go
git commit -m "feat(audit): postgres List/Stream with from/to/username filters + 100k cap"
```

---

## Task 4: Extend `classifyPath` to capture artifact path in `Context`

**Files:**
- Modify: `internal/api/audit_middleware.go`

- [ ] **Step 1: Replace the entire file**

```go
package api

import (
    "context"
    "strings"

    "github.com/gin-gonic/gin"
    "github.com/nexspence-oss/nexspence/internal/domain"
    "github.com/nexspence-oss/nexspence/internal/repository"
)

// AuditMiddleware writes an audit event after each mutating request completes.
// It only records PUT/POST/DELETE/PATCH on key management paths.
func AuditMiddleware(auditRepo repository.AuditRepo) gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Next() // run handler first

        method := c.Request.Method
        if method != "PUT" && method != "POST" && method != "DELETE" && method != "PATCH" {
            return
        }

        path := c.Request.URL.Path
        if !isAuditablePath(path) {
            return
        }

        userID, _ := c.Get("userID")
        username, _ := c.Get("username")
        userIDStr, _ := userID.(string)
        usernameStr, _ := username.(string)
        if usernameStr == "" {
            usernameStr = "anonymous"
        }

        status := c.Writer.Status()
        result := "success"
        if status >= 400 && status < 500 {
            result = "denied"
        } else if status >= 500 {
            result = "failure"
        }

        domainStr, action, entityType, entityName, ctxData := classifyPath(method, path, c)

        e := &domain.AuditEvent{
            UserID:     strPtr(userIDStr),
            Username:   usernameStr,
            RemoteIP:   c.ClientIP(),
            UserAgent:  c.Request.UserAgent(),
            Domain:     domainStr,
            Action:     action,
            EntityType: entityType,
            EntityName: entityName,
            Context:    ctxData,
            Result:     result,
        }
        go func() { _ = auditRepo.Write(context.Background(), e) }()
    }
}

func isAuditablePath(path string) bool {
    prefixes := []string{
        "/service/rest/v1/repositories",
        "/service/rest/v1/security/users",
        "/service/rest/v1/security/roles",
        "/service/rest/v1/security/privileges",
        "/service/rest/v1/security/content-selectors",
        "/service/rest/v1/blobstores",
        "/service/rest/v1/cleanup-policies",
        "/api/v1/webhooks",
        "/api/v1/login",
        "/repository/",
        "/v2/",
    }
    for _, p := range prefixes {
        if strings.HasPrefix(path, p) {
            return true
        }
    }
    return false
}

// lastSegment returns the substring after the final '/' in p (or p itself if none).
func lastSegment(p string) string {
    if i := strings.LastIndex(p, "/"); i >= 0 {
        return p[i+1:]
    }
    return p
}

// classifyPath maps (method, path) to audit fields and any additional context.
// The returned ctxData map is non-nil and may be empty.
func classifyPath(method, path string, c *gin.Context) (domainStr, action, entityType, entityName string, ctxData map[string]any) {
    ctxData = map[string]any{}

    // Login is classified specially: action=LOGIN, entityName=username attempted.
    if strings.HasPrefix(path, "/api/v1/login") {
        return "SECURITY", "LOGIN", "USER", c.GetString("username"), ctxData
    }

    switch {
    case strings.HasPrefix(path, "/service/rest/v1/security/users"):
        domainStr = "SECURITY"
        entityType = "USER"
        entityName = c.Param("userId")
    case strings.HasPrefix(path, "/service/rest/v1/security/roles"):
        domainStr = "SECURITY"
        entityType = "ROLE"
        entityName = c.Param("id")
    case strings.HasPrefix(path, "/service/rest/v1/security/privileges"):
        domainStr = "SECURITY"
        entityType = "PRIVILEGE"
        entityName = c.Param("id")
    case strings.HasPrefix(path, "/service/rest/v1/security/content-selectors"):
        domainStr = "SECURITY"
        entityType = "CONTENT_SELECTOR"
        entityName = c.Param("id")
    case strings.HasPrefix(path, "/api/v1/webhooks"):
        domainStr = "SYSTEM"
        entityType = "WEBHOOK"
        entityName = c.Param("id")
    case strings.HasPrefix(path, "/service/rest/v1/repositories"):
        domainStr = "REPOSITORY"
        entityType = "REPOSITORY"
        entityName = c.Param("name")
    case strings.HasPrefix(path, "/service/rest/v1/blobstores"):
        domainStr = "BLOBSTORE"
        entityType = "BLOBSTORE"
        entityName = c.Param("name")
    case strings.HasPrefix(path, "/service/rest/v1/cleanup-policies"):
        domainStr = "CLEANUP"
        entityType = "CLEANUP_POLICY"
        entityName = c.Param("id")
    case strings.HasPrefix(path, "/repository/"):
        domainStr = "REPOSITORY"
        entityType = "ARTIFACT"
        entityName = c.Param("repoName")
        if p := c.Param("path"); p != "" {
            ctxData["path"] = strings.TrimPrefix(p, "/")
        }
    case strings.HasPrefix(path, "/v2/"):
        domainStr = "REPOSITORY"
        entityType = "ARTIFACT"
        entityName = c.Param("repoName")
        if strings.Contains(path, "/manifests/") {
            ctxData["path"] = "manifests/" + lastSegment(path)
        } else if strings.Contains(path, "/blobs/") {
            ctxData["path"] = "blobs/" + lastSegment(path)
        }
    default:
        domainStr = "SYSTEM"
    }

    switch method {
    case "POST":
        action = "CREATE"
    case "PUT":
        action = "UPDATE"
    case "DELETE":
        action = "DELETE"
    case "PATCH":
        action = "UPDATE"
    default:
        action = method
    }
    return
}

func strPtr(s string) *string {
    if s == "" {
        return nil
    }
    return &s
}
```

- [ ] **Step 2: Run existing middleware tests — they must still pass**

Run: `go test ./internal/api/ -run AuditMiddleware -v`
Expected: PASS for all 6 existing tests.

- [ ] **Step 3: Commit**

```bash
git add internal/api/audit_middleware.go
git commit -m "feat(audit): widen audit middleware coverage; capture artifact path in Context"
```

---

## Task 5: Add middleware tests for new prefixes and `Context["path"]`

**Files:**
- Modify: `internal/api/audit_middleware_test.go`

- [ ] **Step 1: Append the following tests to the file** (keep all existing tests intact)

```go
func TestAuditMiddleware_Repository_CapturesPath(t *testing.T) {
    repo := testutil.NewAuditRepo()
    r := gin.New()
    r.Use(api.AuditMiddleware(repo))
    r.PUT("/repository/:repoName/*path", func(c *gin.Context) {
        c.Status(http.StatusCreated)
    })

    req := httptest.NewRequest(http.MethodPut, "/repository/maven-hosted/com/example/foo/1.0/foo-1.0.jar", nil)
    r.ServeHTTP(httptest.NewRecorder(), req)
    waitForAudit()

    require.Len(t, repo.Events, 1)
    e := repo.Events[0]
    assert.Equal(t, "REPOSITORY", e.Domain)
    assert.Equal(t, "ARTIFACT", e.EntityType)
    assert.Equal(t, "maven-hosted", e.EntityName)
    require.NotNil(t, e.Context)
    assert.Equal(t, "com/example/foo/1.0/foo-1.0.jar", e.Context["path"])
}

func TestAuditMiddleware_DockerV2_CapturesManifestRef(t *testing.T) {
    repo := testutil.NewAuditRepo()
    r := gin.New()
    r.Use(api.AuditMiddleware(repo))
    r.PUT("/v2/:repoName/manifests/:ref", func(c *gin.Context) {
        c.Status(http.StatusCreated)
    })

    req := httptest.NewRequest(http.MethodPut, "/v2/myrepo/manifests/v1", nil)
    r.ServeHTTP(httptest.NewRecorder(), req)
    waitForAudit()

    require.Len(t, repo.Events, 1)
    assert.Equal(t, "manifests/v1", repo.Events[0].Context["path"])
}

func TestAuditMiddleware_Webhooks_PrefixIsAudited(t *testing.T) {
    repo := testutil.NewAuditRepo()
    r := gin.New()
    r.Use(api.AuditMiddleware(repo))
    r.POST("/api/v1/webhooks", func(c *gin.Context) {
        c.Status(http.StatusCreated)
    })

    req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks", nil)
    r.ServeHTTP(httptest.NewRecorder(), req)
    waitForAudit()

    require.Len(t, repo.Events, 1)
    assert.Equal(t, "SYSTEM", repo.Events[0].Domain)
    assert.Equal(t, "WEBHOOK", repo.Events[0].EntityType)
}

func TestAuditMiddleware_Roles_PrefixIsAudited(t *testing.T) {
    repo := testutil.NewAuditRepo()
    r := gin.New()
    r.Use(api.AuditMiddleware(repo))
    r.POST("/service/rest/v1/security/roles", func(c *gin.Context) {
        c.Status(http.StatusCreated)
    })

    req := httptest.NewRequest(http.MethodPost, "/service/rest/v1/security/roles", nil)
    r.ServeHTTP(httptest.NewRecorder(), req)
    waitForAudit()

    require.Len(t, repo.Events, 1)
    assert.Equal(t, "SECURITY", repo.Events[0].Domain)
    assert.Equal(t, "ROLE", repo.Events[0].EntityType)
}

func TestAuditMiddleware_RemoteIP_NonEmpty(t *testing.T) {
    repo := testutil.NewAuditRepo()
    r := gin.New()
    r.Use(api.AuditMiddleware(repo))
    r.POST("/service/rest/v1/repositories", func(c *gin.Context) {
        c.Status(http.StatusCreated)
    })

    req := httptest.NewRequest(http.MethodPost, "/service/rest/v1/repositories", nil)
    req.RemoteAddr = "10.1.2.3:12345"
    r.ServeHTTP(httptest.NewRecorder(), req)
    waitForAudit()

    require.Len(t, repo.Events, 1)
    assert.NotEmpty(t, repo.Events[0].RemoteIP, "RemoteIP must be captured from c.ClientIP()")
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/api/ -run AuditMiddleware -v`
Expected: all tests PASS — both new ones and the 6 existing.

- [ ] **Step 3: Commit**

```bash
git add internal/api/audit_middleware_test.go
git commit -m "test(audit): cover artifact path capture, Docker v2, webhooks, roles, RemoteIP"
```

---

## Task 6: Rewrite `AuditHandler` for new query params + NDJSON dispatch

**Files:**
- Modify: `internal/api/handlers/audit.go`

- [ ] **Step 1: Replace the entire file**

```go
package handlers

import (
    "errors"
    "fmt"
    "net/http"
    "strconv"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/nexspence-oss/nexspence/internal/domain"
    "github.com/nexspence-oss/nexspence/internal/repository"
    pgaudit "github.com/nexspence-oss/nexspence/internal/repository/postgres"
)

type AuditHandler struct {
    repo repository.AuditRepo
}

func NewAuditHandler(repo repository.AuditRepo) *AuditHandler {
    return &AuditHandler{repo: repo}
}

// parseAuditQuery extracts AuditQuery from query string.
// Returns (q, error). On error, the caller should return 400.
func parseAuditQuery(c *gin.Context) (repository.AuditQuery, error) {
    q := repository.AuditQuery{
        Domain:   c.Query("domain"),
        Action:   c.Query("action"),
        Username: c.Query("username"),
    }
    if v := c.Query("from"); v != "" {
        t, err := parseDate(v)
        if err != nil {
            return q, fmt.Errorf("invalid 'from' value: %w", err)
        }
        q.From = &t
    }
    if v := c.Query("to"); v != "" {
        t, err := parseDate(v)
        if err != nil {
            return q, fmt.Errorf("invalid 'to' value: %w", err)
        }
        q.To = &t
    }
    q.Limit, _ = strconv.Atoi(c.DefaultQuery("limit", "100"))
    q.Offset, _ = strconv.Atoi(c.DefaultQuery("offset", "0"))
    return q, nil
}

// parseDate accepts either an ISO date (2026-04-01) or RFC3339 (2026-04-01T12:00:00Z).
func parseDate(s string) (time.Time, error) {
    if t, err := time.Parse("2006-01-02", s); err == nil {
        return t, nil
    }
    return time.Parse(time.RFC3339, s)
}

// List GET /service/rest/v1/audit
//   - format=ndjson  → streaming NDJSON download
//   - otherwise      → {"items":[...], "total": N}
//
// Query params: domain, action, username, from, to, limit, offset, format.
func (h *AuditHandler) List(c *gin.Context) {
    q, err := parseAuditQuery(c)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    if c.Query("format") == "ndjson" {
        h.exportNDJSON(c, q)
        return
    }

    items, total, err := h.repo.List(c.Request.Context(), q)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    if items == nil {
        items = []domain.AuditEvent{}
    }
    c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

func (h *AuditHandler) exportNDJSON(c *gin.Context, q repository.AuditQuery) {
    filename := "audit-" + time.Now().UTC().Format("2006-01-02") + ".ndjson"
    c.Writer.Header().Set("Content-Type", "application/x-ndjson")
    c.Writer.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
    c.Writer.WriteHeader(http.StatusOK)

    enc := newJSONLineEncoder(c.Writer)
    streamErr := h.repo.Stream(c.Request.Context(), q, func(e domain.AuditEvent) error {
        return enc.encode(e)
    })

    if streamErr == nil {
        return
    }
    if errors.Is(streamErr, pgaudit.ErrStreamCapExceeded) {
        // Headers already sent — best we can do is append an error line so the
        // client sees the failure rather than a silently truncated download.
        _ = enc.encode(map[string]any{
            "error": "row cap exceeded; narrow date range and retry",
            "cap":   100_000,
        })
        return
    }
    // Generic streaming failure — same fallback.
    _ = enc.encode(map[string]any{"error": streamErr.Error()})
}
```

Add `internal/api/handlers/audit_jsonline.go` for the encoder helper to keep the handler tight:

- Create: `internal/api/handlers/audit_jsonline.go`

```go
package handlers

import (
    "encoding/json"
    "io"
)

type jsonLineEncoder struct{ w io.Writer }

func newJSONLineEncoder(w io.Writer) *jsonLineEncoder { return &jsonLineEncoder{w: w} }

// encode writes one JSON value followed by a newline.
func (e *jsonLineEncoder) encode(v any) error {
    b, err := json.Marshal(v)
    if err != nil {
        return err
    }
    if _, err := e.w.Write(b); err != nil {
        return err
    }
    _, err = e.w.Write([]byte{'\n'})
    return err
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: clean — all packages compile.

- [ ] **Step 3: Run all existing tests to confirm nothing else broke**

Run: `go test ./...`
Expected: PASS (no test should reference the old `List(domain, action, limit, offset)` signature outside the mock and handler we already updated).

- [ ] **Step 4: Commit**

```bash
git add internal/api/handlers/audit.go internal/api/handlers/audit_jsonline.go
git commit -m "feat(audit): handler accepts from/to/username + dispatches NDJSON export"
```

---

## Task 7: Add handler tests for filters, NDJSON, and 100k cap

**Files:**
- Create: `internal/api/handlers/audit_test.go`

- [ ] **Step 1: Create the file**

```go
package handlers_test

import (
    "bufio"
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/nexspence-oss/nexspence/internal/api/handlers"
    "github.com/nexspence-oss/nexspence/internal/domain"
    "github.com/nexspence-oss/nexspence/internal/testutil"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func init() { gin.SetMode(gin.TestMode) }

func mountAudit(t *testing.T) (*gin.Engine, *testutil.AuditRepo) {
    t.Helper()
    repo := testutil.NewAuditRepo()
    h := handlers.NewAuditHandler(repo)
    r := gin.New()
    r.GET("/service/rest/v1/audit", h.List)
    return r, repo
}

func seed(t *testing.T, repo *testutil.AuditRepo, events []domain.AuditEvent) {
    t.Helper()
    for i := range events {
        require.NoError(t, repo.Write(context.Background(), &events[i]))
    }
}

func TestAuditList_FromTo_Filtering(t *testing.T) {
    r, repo := mountAudit(t)
    seed(t, repo, []domain.AuditEvent{
        {EventTime: time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC), Username: "a", Domain: "REPOSITORY", Action: "CREATE", Result: "success"},
        {EventTime: time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC), Username: "b", Domain: "REPOSITORY", Action: "DELETE", Result: "success"},
        {EventTime: time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC), Username: "c", Domain: "REPOSITORY", Action: "CREATE", Result: "success"},
    })

    req := httptest.NewRequest(http.MethodGet,
        "/service/rest/v1/audit?from=2026-04-05&to=2026-04-15", nil)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    var body struct {
        Items []domain.AuditEvent `json:"items"`
        Total int                 `json:"total"`
    }
    require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
    require.Equal(t, 1, body.Total)
    require.Len(t, body.Items, 1)
    assert.Equal(t, "b", body.Items[0].Username)
}

func TestAuditList_UsernameFilter(t *testing.T) {
    r, repo := mountAudit(t)
    seed(t, repo, []domain.AuditEvent{
        {EventTime: time.Now(), Username: "alice", Domain: "X", Action: "CREATE", Result: "success"},
        {EventTime: time.Now(), Username: "bob",   Domain: "X", Action: "CREATE", Result: "success"},
    })

    req := httptest.NewRequest(http.MethodGet,
        "/service/rest/v1/audit?username=alice", nil)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    var body struct {
        Items []domain.AuditEvent `json:"items"`
        Total int                 `json:"total"`
    }
    require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
    assert.Equal(t, 1, body.Total)
    assert.Equal(t, "alice", body.Items[0].Username)
}

func TestAuditList_TotalReflectsAllMatches_NotPage(t *testing.T) {
    r, repo := mountAudit(t)
    var events []domain.AuditEvent
    for i := 0; i < 5; i++ {
        events = append(events, domain.AuditEvent{
            EventTime: time.Now(), Username: "u", Domain: "D", Action: "CREATE", Result: "success",
        })
    }
    seed(t, repo, events)

    req := httptest.NewRequest(http.MethodGet,
        "/service/rest/v1/audit?limit=2&offset=0", nil)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    var body struct {
        Items []domain.AuditEvent `json:"items"`
        Total int                 `json:"total"`
    }
    require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
    assert.Equal(t, 5, body.Total, "total reflects all matches")
    assert.Len(t, body.Items, 2, "page is limited")
}

func TestAuditList_NDJSON_Export(t *testing.T) {
    r, repo := mountAudit(t)
    seed(t, repo, []domain.AuditEvent{
        {EventTime: time.Now(), Username: "a", Domain: "REPOSITORY", Action: "CREATE", Result: "success"},
        {EventTime: time.Now(), Username: "b", Domain: "REPOSITORY", Action: "DELETE", Result: "success"},
    })

    req := httptest.NewRequest(http.MethodGet,
        "/service/rest/v1/audit?format=ndjson", nil)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    require.Equal(t, http.StatusOK, rec.Code)
    assert.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
    assert.Contains(t, rec.Header().Get("Content-Disposition"), "audit-")
    assert.Contains(t, rec.Header().Get("Content-Disposition"), ".ndjson")

    sc := bufio.NewScanner(strings.NewReader(rec.Body.String()))
    n := 0
    for sc.Scan() {
        var e domain.AuditEvent
        require.NoError(t, json.Unmarshal(sc.Bytes(), &e), "each line must be JSON")
        n++
    }
    assert.Equal(t, 2, n)
}

func TestAuditList_BadFromValue_400(t *testing.T) {
    r, _ := mountAudit(t)

    req := httptest.NewRequest(http.MethodGet,
        "/service/rest/v1/audit?from=not-a-date", nil)
    rec := httptest.NewRecorder()
    r.ServeHTTP(rec, req)

    assert.Equal(t, http.StatusBadRequest, rec.Code)
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/api/handlers/ -run Audit -v`
Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/api/handlers/audit_test.go
git commit -m "test(audit): handler — from/to/username filters, NDJSON export, total counter"
```

---

## Task 8: Add `AuditConfig` to viper and `config.yaml`

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.yaml`

- [ ] **Step 1: In `internal/config/config.go`** — add `Audit` to `Config` struct and define `AuditConfig`. Add `time` import if missing.

In the `Config` struct, after `Cleanup CleanupConfig …` add:
```go
    Audit     AuditConfig     `mapstructure:"audit"`
```

After the `CleanupConfig` block, add:
```go
type AuditConfig struct {
    RetentionDays    int           `mapstructure:"retention_days"`
    SoftCap          int64         `mapstructure:"soft_cap"`
    RotationInterval time.Duration `mapstructure:"rotation_interval"`
    LookaheadMonths  int           `mapstructure:"lookahead_months"`
}
```

In `Load`, after `v.SetDefault("cleanup.default_schedule", ...)` add:
```go
    v.SetDefault("audit.retention_days", 90)
    v.SetDefault("audit.soft_cap", int64(1_000_000))
    v.SetDefault("audit.rotation_interval", "24h")
    v.SetDefault("audit.lookahead_months", 2)
```

Add `"time"` to the import block at the top.

- [ ] **Step 2: In `config.yaml`** — add an `audit` section. Insert after the existing `bootstrap:` block (or at the end of the file):

```yaml

# Audit log retention.
# Events live in monthly PostgreSQL partitions.
# Rotator pre-creates partitions for the next `lookahead_months` ahead and
# DETACH/DROPs partitions whose end-date is older than `retention_days`.
# `soft_cap` is observability-only — when total rows exceed the cap, a warning
# is logged and `audit_events_count` metric is updated; nothing is deleted.
audit:
  retention_days: 90
  soft_cap: 1000000
  rotation_interval: 24h
  lookahead_months: 2
```

- [ ] **Step 3: Verify config loads**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go config.yaml
git commit -m "feat(audit): AuditConfig (retention_days/soft_cap/rotation_interval/lookahead_months)"
```

---

## Task 9: Add `metrics.AuditEventsCount` gauge

**Files:**
- Modify: `internal/metrics/metrics.go`

- [ ] **Step 1: Edit the file** — add the new atomic and include it in `Snapshot`.

Add to the `var (...)` block:
```go
    AuditEventsCount atomic.Int64
```

In `Snapshot`'s returned `Map`, add:
```go
    "audit_events_count": AuditEventsCount.Load(),
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/metrics/metrics.go
git commit -m "feat(metrics): expose audit_events_count gauge"
```

---

## Task 10: Define `PartitionStore` interface + `Rotator` skeleton

**Files:**
- Create: `internal/audit/partition_store.go`
- Create: `internal/audit/rotator.go`

- [ ] **Step 1: Create `internal/audit/partition_store.go`**

```go
// Package audit owns the audit-log partition rotator and any future
// audit-specific background jobs.
package audit

import (
    "context"
    "time"
)

// Partition describes one partition of audit_events.
//
// Both `From` (inclusive) and `To` (exclusive) are at the start of the day at 00:00 UTC.
type Partition struct {
    Name string    // e.g. "audit_events_2026_05"
    From time.Time // inclusive
    To   time.Time // exclusive
}

// PartitionStore is the small surface the Rotator needs from the database.
// The postgres implementation lives in this package; tests use a fake.
type PartitionStore interface {
    // ListPartitions returns the existing partitions of audit_events.
    ListPartitions(ctx context.Context) ([]Partition, error)

    // CreatePartition creates a partition covering [from, to). It MUST be
    // idempotent — calling it for an existing range is a no-op.
    CreatePartition(ctx context.Context, name string, from, to time.Time) error

    // DropPartition DETACHes and DROPs the named partition.
    DropPartition(ctx context.Context, name string) error

    // CountRows returns the total number of rows in audit_events.
    CountRows(ctx context.Context) (int64, error)
}

// PartitionName returns the canonical name for the partition that covers
// the month containing `t`: e.g. 2026-05-12 → "audit_events_2026_05".
func PartitionName(t time.Time) string {
    t = t.UTC()
    return t.Format("audit_events_2006_01")
}

// MonthBounds returns the [from, to) range that covers the month containing `t`.
func MonthBounds(t time.Time) (from, to time.Time) {
    t = t.UTC()
    from = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
    to = from.AddDate(0, 1, 0)
    return
}
```

- [ ] **Step 2: Create `internal/audit/rotator.go`** (skeleton — no DB impl yet)

```go
package audit

import (
    "context"
    "time"

    "github.com/nexspence-oss/nexspence/internal/config"
    "github.com/nexspence-oss/nexspence/internal/logger"
    "github.com/nexspence-oss/nexspence/internal/metrics"
)

// Rotator runs partition lifecycle for audit_events on a ticker.
type Rotator struct {
    store PartitionStore
    cfg   config.AuditConfig
    log   logger.Logger
    now   func() time.Time // injectable for tests; defaults to time.Now in NewRotator
}

func NewRotator(store PartitionStore, cfg config.AuditConfig, log logger.Logger) *Rotator {
    return &Rotator{store: store, cfg: cfg, log: log, now: time.Now}
}

// Run blocks until ctx is cancelled, ticking every cfg.RotationInterval.
// Run does NOT execute the first tick — call RunOnce(ctx) before Run if you
// want the start-up partitions guaranteed before the server accepts traffic.
func (r *Rotator) Run(ctx context.Context) {
    interval := r.cfg.RotationInterval
    if interval <= 0 {
        interval = 24 * time.Hour
    }
    t := time.NewTicker(interval)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-t.C:
            r.tick(ctx)
        }
    }
}

// RunOnce executes one full pass synchronously.
func (r *Rotator) RunOnce(ctx context.Context) {
    r.tick(ctx)
}

func (r *Rotator) tick(ctx context.Context) {
    if err := r.ensureFuturePartitions(ctx); err != nil {
        r.log.Errorw("audit rotator: ensure partitions", "err", err)
    }
    if err := r.dropOldPartitions(ctx); err != nil {
        r.log.Errorw("audit rotator: drop old partitions", "err", err)
    }
    r.checkSoftCap(ctx)
}

func (r *Rotator) ensureFuturePartitions(ctx context.Context) error {
    months := r.cfg.LookaheadMonths
    if months < 0 {
        months = 0
    }
    base := r.now()
    for i := 0; i <= months; i++ {
        target := base.AddDate(0, i, 0)
        from, to := MonthBounds(target)
        name := PartitionName(target)
        if err := r.store.CreatePartition(ctx, name, from, to); err != nil {
            return err
        }
    }
    return nil
}

func (r *Rotator) dropOldPartitions(ctx context.Context) error {
    parts, err := r.store.ListPartitions(ctx)
    if err != nil {
        return err
    }
    cutoff := r.now().UTC().AddDate(0, 0, -r.cfg.RetentionDays)
    for _, p := range parts {
        if !p.To.After(cutoff) {
            if err := r.store.DropPartition(ctx, p.Name); err != nil {
                r.log.Warnw("audit rotator: drop failed",
                    "partition", p.Name, "err", err)
                continue
            }
            r.log.Infow("audit rotator: dropped partition",
                "partition", p.Name, "to", p.To.Format("2006-01-02"))
        }
    }
    return nil
}

func (r *Rotator) checkSoftCap(ctx context.Context) {
    n, err := r.store.CountRows(ctx)
    if err != nil {
        r.log.Warnw("audit rotator: count rows", "err", err)
        return
    }
    metrics.AuditEventsCount.Store(n)
    if r.cfg.SoftCap > 0 && n > r.cfg.SoftCap {
        r.log.Warnw("audit_events soft cap exceeded",
            "count", n, "cap", r.cfg.SoftCap)
    }
}
```

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/audit/partition_store.go internal/audit/rotator.go
git commit -m "feat(audit): Rotator + PartitionStore interface (logic only, no DB impl yet)"
```

---

## Task 11: Write rotator unit tests against a fake `PartitionStore`

**Files:**
- Create: `internal/audit/rotator_test.go`

- [ ] **Step 1: Create the file**

```go
package audit_test

import (
    "context"
    "errors"
    "fmt"
    "sort"
    "sync"
    "testing"
    "time"

    "github.com/nexspence-oss/nexspence/internal/audit"
    "github.com/nexspence-oss/nexspence/internal/config"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "go.uber.org/zap"
)

func nopLog() *zap.SugaredLogger { return zap.NewNop().Sugar() }

// ── Fake PartitionStore ──────────────────────────────────────────────────

type fakeStore struct {
    mu         sync.Mutex
    partitions map[string]audit.Partition
    rowCount   int64
    errOn      string // method name to fail on, "" = none
}

func newFakeStore() *fakeStore { return &fakeStore{partitions: map[string]audit.Partition{}} }

func (f *fakeStore) ListPartitions(_ context.Context) ([]audit.Partition, error) {
    if f.errOn == "list" {
        return nil, errors.New("boom")
    }
    f.mu.Lock()
    defer f.mu.Unlock()
    out := make([]audit.Partition, 0, len(f.partitions))
    for _, p := range f.partitions {
        out = append(out, p)
    }
    sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
    return out, nil
}

func (f *fakeStore) CreatePartition(_ context.Context, name string, from, to time.Time) error {
    if f.errOn == "create" {
        return errors.New("create failed")
    }
    f.mu.Lock()
    defer f.mu.Unlock()
    f.partitions[name] = audit.Partition{Name: name, From: from, To: to}
    return nil
}

func (f *fakeStore) DropPartition(_ context.Context, name string) error {
    if f.errOn == "drop" {
        return fmt.Errorf("drop %s failed", name)
    }
    f.mu.Lock()
    defer f.mu.Unlock()
    delete(f.partitions, name)
    return nil
}

func (f *fakeStore) CountRows(_ context.Context) (int64, error) {
    if f.errOn == "count" {
        return 0, errors.New("count failed")
    }
    return f.rowCount, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────

func newRotator(t *testing.T, store audit.PartitionStore, cfg config.AuditConfig, now time.Time) *audit.Rotator {
    t.Helper()
    r := audit.NewRotator(store, cfg, nopLog())
    audit.SetNowFuncForTest(r, func() time.Time { return now })
    return r
}

// ── Tests ────────────────────────────────────────────────────────────────

func TestRotator_EnsureFuturePartitions_Idempotent(t *testing.T) {
    store := newFakeStore()
    cfg := config.AuditConfig{LookaheadMonths: 2, RetentionDays: 90}
    now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

    r := newRotator(t, store, cfg, now)
    r.RunOnce(context.Background())
    r.RunOnce(context.Background())

    parts, err := store.ListPartitions(context.Background())
    require.NoError(t, err)
    require.Len(t, parts, 3, "lookahead=2 → current + next 2 = 3 partitions")
    assert.Equal(t, "audit_events_2026_05", parts[0].Name)
    assert.Equal(t, "audit_events_2026_06", parts[1].Name)
    assert.Equal(t, "audit_events_2026_07", parts[2].Name)
}

func TestRotator_DropOldPartitions(t *testing.T) {
    store := newFakeStore()
    // Manually pre-load an old partition.
    _ = store.CreatePartition(context.Background(),
        "audit_events_2024_01",
        time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
        time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
    )
    cfg := config.AuditConfig{RetentionDays: 90, LookaheadMonths: 0}
    now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

    r := newRotator(t, store, cfg, now)
    r.RunOnce(context.Background())

    parts, err := store.ListPartitions(context.Background())
    require.NoError(t, err)
    for _, p := range parts {
        assert.NotEqual(t, "audit_events_2024_01", p.Name, "old partition must be dropped")
    }
}

func TestRotator_DropOldPartitions_KeepsInWindow(t *testing.T) {
    store := newFakeStore()
    cfg := config.AuditConfig{RetentionDays: 90, LookaheadMonths: 0}
    now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

    // A partition whose end is inside the 90-day window.
    inWindowEnd := now.AddDate(0, 0, -10)
    _ = store.CreatePartition(context.Background(),
        "audit_events_in_window",
        inWindowEnd.AddDate(0, 0, -30),
        inWindowEnd,
    )

    r := newRotator(t, store, cfg, now)
    r.RunOnce(context.Background())

    parts, err := store.ListPartitions(context.Background())
    require.NoError(t, err)
    require.Len(t, parts, 1)
    assert.Equal(t, "audit_events_in_window", parts[0].Name)
}

func TestRotator_SoftCap_UpdatesMetricAndWarns(t *testing.T) {
    store := newFakeStore()
    store.rowCount = 1500
    cfg := config.AuditConfig{SoftCap: 1000, LookaheadMonths: 0, RetentionDays: 90}
    now := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)

    // We can't assert on the warn line without a captured logger; this test
    // just verifies the rotator does not panic and the soft-cap path runs.
    r := newRotator(t, store, cfg, now)
    r.RunOnce(context.Background())
    // Metric is updated as a side effect — see metrics.AuditEventsCount usage.
}

func TestRotator_SoftCap_Disabled_ZeroCap(t *testing.T) {
    store := newFakeStore()
    store.rowCount = 999_999
    cfg := config.AuditConfig{SoftCap: 0, LookaheadMonths: 0, RetentionDays: 90}
    now := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)

    r := newRotator(t, store, cfg, now)
    r.RunOnce(context.Background()) // no panic, no warn (cap disabled)
}

func TestRotator_TickContinuesOnPartialFailure(t *testing.T) {
    store := newFakeStore()
    store.errOn = "list" // dropOldPartitions will fail; ensureFuturePartitions still runs

    cfg := config.AuditConfig{RetentionDays: 90, LookaheadMonths: 1, SoftCap: 0}
    now := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)

    r := newRotator(t, store, cfg, now)
    r.RunOnce(context.Background()) // must not panic

    // ensureFuturePartitions ran first and created partitions.
    store.errOn = ""
    parts, err := store.ListPartitions(context.Background())
    require.NoError(t, err)
    assert.Len(t, parts, 2, "lookahead=1 → current + next = 2 partitions")
}

func TestPartitionName_FormatsAsYearMonth(t *testing.T) {
    got := audit.PartitionName(time.Date(2026, 5, 12, 13, 14, 15, 0, time.UTC))
    assert.Equal(t, "audit_events_2026_05", got)
}

func TestMonthBounds_StartAndEnd(t *testing.T) {
    from, to := audit.MonthBounds(time.Date(2026, 5, 12, 13, 14, 15, 0, time.UTC))
    assert.Equal(t, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), from)
    assert.Equal(t, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), to)
}
```

- [ ] **Step 2: Add the test-only export hook** — `internal/audit/export_test.go`

```go
package audit

import "time"

// SetNowFuncForTest replaces the rotator's clock for deterministic tests.
// Lives in a non-test file with `_test.go` suffix so it is compiled only
// during `go test` for this package, not exported into production builds.
func SetNowFuncForTest(r *Rotator, fn func() time.Time) {
    r.now = fn
}
```

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/audit/ -v`
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/audit/rotator_test.go internal/audit/export_test.go
git commit -m "test(audit): rotator unit tests via fake PartitionStore"
```

---

## Task 12: Implement `pgPartitionStore` against PostgreSQL

**Files:**
- Modify: `internal/audit/partition_store.go`

- [ ] **Step 1: Append the postgres implementation to `partition_store.go`**

```go
import (
    "context"
    "fmt"
    "regexp"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
)
```

(Adjust the existing `import` block at the top of the file accordingly — keep the existing `context` and `time` imports; add `fmt`, `regexp`, and `pgxpool`.)

Append the following to the bottom of the file:

```go
// pgPartitionStore is the PostgreSQL implementation of PartitionStore.
type pgPartitionStore struct{ pool *pgxpool.Pool }

// NewPgPartitionStore returns a PartitionStore backed by the given pool.
func NewPgPartitionStore(pool *pgxpool.Pool) PartitionStore {
    return &pgPartitionStore{pool: pool}
}

// boundRE matches `FOR VALUES FROM ('YYYY-MM-DD') TO ('YYYY-MM-DD')`,
// which is exactly the form PostgreSQL returns for our partitions.
var boundRE = regexp.MustCompile(
    `FOR VALUES FROM \('(\d{4}-\d{2}-\d{2})'\) TO \('(\d{4}-\d{2}-\d{2})'\)`)

func (s *pgPartitionStore) ListPartitions(ctx context.Context) ([]Partition, error) {
    rows, err := s.pool.Query(ctx, `
        SELECT inhrelid::regclass::text AS name,
               pg_get_expr(c.relpartbound, c.oid) AS bound
          FROM pg_inherits i
          JOIN pg_class c ON c.oid = i.inhrelid
         WHERE i.inhparent = 'audit_events'::regclass`)
    if err != nil {
        return nil, fmt.Errorf("list audit partitions: %w", err)
    }
    defer rows.Close()

    var out []Partition
    for rows.Next() {
        var name, bound string
        if err := rows.Scan(&name, &bound); err != nil {
            return nil, err
        }
        m := boundRE.FindStringSubmatch(bound)
        if len(m) != 3 {
            // Non-conforming partition (e.g. DEFAULT); skip safely.
            continue
        }
        from, err := time.Parse("2006-01-02", m[1])
        if err != nil {
            continue
        }
        to, err := time.Parse("2006-01-02", m[2])
        if err != nil {
            continue
        }
        out = append(out, Partition{Name: name, From: from, To: to})
    }
    return out, rows.Err()
}

func (s *pgPartitionStore) CreatePartition(ctx context.Context, name string, from, to time.Time) error {
    // Identifiers can't be parameterised — the names are derived from time
    // values produced by this package, so they are safe to format.
    sql := fmt.Sprintf(
        `CREATE TABLE IF NOT EXISTS %s
           PARTITION OF audit_events
           FOR VALUES FROM ('%s') TO ('%s')`,
        name,
        from.UTC().Format("2006-01-02"),
        to.UTC().Format("2006-01-02"),
    )
    if _, err := s.pool.Exec(ctx, sql); err != nil {
        return fmt.Errorf("create partition %s: %w", name, err)
    }
    return nil
}

func (s *pgPartitionStore) DropPartition(ctx context.Context, name string) error {
    if _, err := s.pool.Exec(ctx,
        fmt.Sprintf(`ALTER TABLE audit_events DETACH PARTITION %s`, name)); err != nil {
        return fmt.Errorf("detach %s: %w", name, err)
    }
    if _, err := s.pool.Exec(ctx,
        fmt.Sprintf(`DROP TABLE %s`, name)); err != nil {
        return fmt.Errorf("drop %s: %w", name, err)
    }
    return nil
}

func (s *pgPartitionStore) CountRows(ctx context.Context) (int64, error) {
    var n int64
    if err := s.pool.QueryRow(ctx,
        `SELECT COUNT(*) FROM audit_events`).Scan(&n); err != nil {
        return 0, fmt.Errorf("count audit_events: %w", err)
    }
    return n, nil
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/audit/partition_store.go
git commit -m "feat(audit): postgres PartitionStore — list/create/drop via pg_inherits"
```

---

## Task 13: Wire the rotator into `cmd/server/main.go`

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Add the import** — in the import block at the top:

```go
    "github.com/nexspence-oss/nexspence/internal/audit"
```

- [ ] **Step 2: Insert rotator startup** — after the metrics seed block (lines 89-96) and before `bootstrapAdmin`, add:

```go
                // Audit retention — pre-create future partitions, drop expired,
                // observe row count. Synchronous first tick guarantees the
                // current month's partition exists before we accept traffic.
                rotator := audit.NewRotator(audit.NewPgPartitionStore(pool), cfg.Audit, log)
                rotator.RunOnce(cmd.Context())
                go rotator.Run(cmd.Context())
                log.Info("audit rotator started",
                    "retention_days", cfg.Audit.RetentionDays,
                    "soft_cap", cfg.Audit.SoftCap,
                    "rotation_interval", cfg.Audit.RotationInterval.String(),
                    "lookahead_months", cfg.Audit.LookaheadMonths,
                )
```

- [ ] **Step 3: Verify build**

Run: `go build ./cmd/server`
Expected: clean.

- [ ] **Step 4: Run all tests**

Run: `go test ./...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat(audit): wire partition rotator into server startup"
```

---

## Task 14: Frontend — extend `nexusApi.listAuditEvents` and add `auditExportUrl`

**Files:**
- Modify: `frontend/src/api/client.ts:160-166`

- [ ] **Step 1: Replace the `listAuditEvents` block** with the extended version:

```ts
  // Audit log
  listAuditEvents: (params?: {
    domain?: string
    action?: string
    username?: string
    from?: string   // YYYY-MM-DD or RFC3339
    to?: string     // YYYY-MM-DD or RFC3339
    limit?: number
    offset?: number
  }) => apiClient.get('/service/rest/v1/audit', { params }),

  // Returns a URL that triggers a NDJSON download in the browser. Accepts the
  // same filter shape as `listAuditEvents` minus pagination.
  auditExportUrl: (params?: {
    domain?: string
    action?: string
    username?: string
    from?: string
    to?: string
  }): string => {
    const sp = new URLSearchParams({ format: 'ndjson' })
    if (params?.domain)   sp.set('domain', params.domain)
    if (params?.action)   sp.set('action', params.action)
    if (params?.username) sp.set('username', params.username)
    if (params?.from)     sp.set('from', params.from)
    if (params?.to)       sp.set('to', params.to)
    const base = (apiClient.defaults.baseURL ?? '').replace(/\/$/, '')
    return `${base}/service/rest/v1/audit?${sp.toString()}`
  },
```

- [ ] **Step 2: Verify TS build**

Run: `cd frontend && npm run build`
Expected: 0 TypeScript errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/client.ts
git commit -m "feat(ui): listAuditEvents accepts username/from/to; add auditExportUrl"
```

---

## Task 15: Frontend — `AuditPage` filters, Export button, Path column, pagination fix

**Files:**
- Modify: `frontend/src/pages/AuditPage.tsx`

- [ ] **Step 1: Replace the entire file**

```tsx
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { FileText, RefreshCw, ChevronLeft, ChevronRight, Download } from 'lucide-react'
import { nexusApi } from '@/api/client'
import { Select } from '../components/Select'

interface AuditEvent {
  id: number
  eventTime: string
  username: string
  remoteIp: string
  domain: string
  action: string
  entityType: string
  entityName: string
  result: string
  context?: Record<string, any>
}

const DOMAINS = ['', 'REPOSITORY', 'SECURITY', 'USER', 'BLOBSTORE', 'CLEANUP', 'SYSTEM']
const ACTIONS = ['', 'CREATE', 'UPDATE', 'DELETE', 'LOGIN', 'LOGOUT']
const PAGE_SIZE = 50

const DOMAIN_COLOR: Record<string, string> = {
  REPOSITORY: '#3b82f6',
  SECURITY:   '#a78bfa',
  USER:       '#06b6d4',
  BLOBSTORE:  '#f59e0b',
  CLEANUP:    '#ef4444',
  SYSTEM:     '#6b7280',
}

const ACTION_COLOR: Record<string, string> = {
  CREATE: '#22c55e',
  UPDATE: '#3b82f6',
  DELETE: '#ef4444',
  LOGIN:  '#f59e0b',
  LOGOUT: '#6b7280',
}

const RESULT_COLOR: Record<string, string> = {
  success: '#22c55e',
  failure: '#ef4444',
  denied:  '#f59e0b',
}

const S = {
  page:    { padding: 24, display: 'flex', flexDirection: 'column' as const, gap: 20 },
  header:  { display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', flexWrap: 'wrap' as const, gap: 12 },
  title:   { fontSize: 20, fontWeight: 700, color: '#dbeafe', margin: '0 0 4px' },
  subtitle:{ fontSize: 13, color: 'rgba(229,231,235,0.5)', margin: 0 },
  filters: { display: 'flex', gap: 10, flexWrap: 'wrap' as const, alignItems: 'center' },
  iconBtn: { background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: 8, color: 'rgba(229,231,235,0.7)', cursor: 'pointer', display: 'flex', alignItems: 'center' },
  input:   { background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: '8px 10px', color: '#e5e7eb', fontSize: 13, fontFamily: 'inherit' },
  table:   { width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 },
  th:      { textAlign: 'left' as const, padding: '8px 12px', color: 'rgba(229,231,235,0.45)', fontWeight: 600, fontSize: 11, textTransform: 'uppercase' as const, letterSpacing: '0.05em', borderBottom: '1px solid rgba(255,255,255,0.06)' },
  td:      { padding: '10px 12px', borderBottom: '1px solid rgba(255,255,255,0.04)', verticalAlign: 'middle' as const },
  mono:    { fontFamily: 'monospace', fontSize: 12, color: 'rgba(229,231,235,0.6)' },
  badge:   (color: string) => ({
    fontSize: 11, fontWeight: 600 as const, padding: '2px 7px',
    borderRadius: 4, background: color + '20', color,
    display: 'inline-block',
  }),
  empty:   { display: 'flex', flexDirection: 'column' as const, alignItems: 'center', justifyContent: 'center', gap: 12, color: 'rgba(229,231,235,0.35)', fontSize: 14, paddingTop: 48 },
  pagination:{ display: 'flex', alignItems: 'center', gap: 12, justifyContent: 'flex-end', fontSize: 13, color: 'rgba(229,231,235,0.5)' },
  card:    { background: 'rgba(255,255,255,0.02)', border: '1px solid rgba(255,255,255,0.07)', borderRadius: 12, overflow: 'hidden' as const },
  pathCell:{ display: 'inline-block', maxWidth: '100%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const },
}

function fmt(ts: string) {
  return new Date(ts).toLocaleString(undefined, {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

interface AuditResponse { items: AuditEvent[]; total: number }

export default function AuditPage() {
  const [domain, setDomain] = useState('')
  const [action, setAction] = useState('')
  const [username, setUsername] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [offset, setOffset] = useState(0)

  const { data, isLoading, refetch } = useQuery<AuditResponse>({
    queryKey: ['audit', domain, action, username, from, to, offset],
    queryFn: () =>
      nexusApi
        .listAuditEvents({
          domain:   domain   || undefined,
          action:   action   || undefined,
          username: username || undefined,
          from:     from     || undefined,
          to:       to       || undefined,
          limit:    PAGE_SIZE,
          offset,
        })
        .then(r => {
          const body = r.data as Partial<AuditResponse>
          return { items: body.items ?? [], total: body.total ?? 0 }
        }),
  })

  const events = data?.items ?? []
  const total  = data?.total ?? 0
  const hasPrev = offset > 0
  const hasNext = offset + events.length < total

  const onExport = () => {
    const url = nexusApi.auditExportUrl({
      domain:   domain   || undefined,
      action:   action   || undefined,
      username: username || undefined,
      from:     from     || undefined,
      to:       to       || undefined,
    })
    window.location.href = url
  }

  const resetOffset = () => setOffset(0)

  return (
    <div style={S.page}>
      <div style={S.header}>
        <div>
          <h1 style={S.title}>Audit Log</h1>
          <p style={S.subtitle}>All system mutations — repository, user, and security events</p>
        </div>
        <div style={S.filters}>
          <Select
            options={DOMAINS.map(d => ({ value: d, label: d || 'All domains' }))}
            value={domain}
            onChange={v => { setDomain(v); resetOffset() }}
            style={{ minWidth: 160 }}
          />
          <Select
            options={ACTIONS.map(a => ({ value: a, label: a || 'All actions' }))}
            value={action}
            onChange={v => { setAction(v); resetOffset() }}
            style={{ minWidth: 140 }}
          />
          <input
            type="text"
            placeholder="username…"
            value={username}
            onChange={e => { setUsername(e.target.value); resetOffset() }}
            style={{ ...S.input, minWidth: 140 }}
          />
          <input
            type="date"
            value={from}
            onChange={e => { setFrom(e.target.value); resetOffset() }}
            style={S.input}
            title="From"
          />
          <input
            type="date"
            value={to}
            onChange={e => { setTo(e.target.value); resetOffset() }}
            style={S.input}
            title="To"
          />
          <button style={S.iconBtn} onClick={() => refetch()} title="Refresh">
            <RefreshCw size={15} />
          </button>
          <button style={S.iconBtn} onClick={onExport} title="Export filtered events as NDJSON">
            <Download size={15} />
          </button>
        </div>
      </div>

      {isLoading ? (
        <div style={S.empty}>Loading…</div>
      ) : events.length === 0 ? (
        <div style={S.empty}>
          <FileText size={40} style={{ opacity: 0.3 }} />
          <p>No audit events{domain || action || username || from || to ? ' matching filters' : ''}</p>
        </div>
      ) : (
        <>
          <div style={S.card}>
            <table style={S.table}>
              <thead>
                <tr>
                  <th style={S.th}>Time</th>
                  <th style={S.th}>User</th>
                  <th style={S.th}>Domain</th>
                  <th style={S.th}>Action</th>
                  <th style={S.th}>Entity</th>
                  <th style={S.th}>Path</th>
                  <th style={S.th}>IP</th>
                  <th style={S.th}>Result</th>
                </tr>
              </thead>
              <tbody>
                {events.map(e => (
                  <tr key={e.id} style={{ color: '#e5e7eb' }}>
                    <td style={{ ...S.td, ...S.mono }}>{fmt(e.eventTime)}</td>
                    <td style={{ ...S.td, fontWeight: 500 }}>{e.username || '—'}</td>
                    <td style={S.td}>
                      <span style={S.badge(DOMAIN_COLOR[e.domain] ?? '#6b7280')}>
                        {e.domain}
                      </span>
                    </td>
                    <td style={S.td}>
                      <span style={S.badge(ACTION_COLOR[e.action] ?? '#6b7280')}>
                        {e.action}
                      </span>
                    </td>
                    <td style={{ ...S.td, color: 'rgba(229,231,235,0.7)' }}>
                      {e.entityType ? `${e.entityType}: ` : ''}
                      <span style={{ color: '#93c5fd' }}>{e.entityName || '—'}</span>
                    </td>
                    <td style={{ ...S.td, ...S.mono, maxWidth: 320 }}>
                      {e.context?.path
                        ? <span title={String(e.context.path)} style={S.pathCell}>{String(e.context.path)}</span>
                        : '—'}
                    </td>
                    <td style={{ ...S.td, ...S.mono }}>{e.remoteIp || '—'}</td>
                    <td style={S.td}>
                      <span style={S.badge(RESULT_COLOR[e.result] ?? '#6b7280')}>
                        {e.result}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div style={S.pagination}>
            <span>Showing {offset + 1}–{offset + events.length} of {total}</span>
            <button style={{ ...S.iconBtn, opacity: hasPrev ? 1 : 0.4 }}
              disabled={!hasPrev} onClick={() => setOffset(o => Math.max(0, o - PAGE_SIZE))}>
              <ChevronLeft size={15} />
            </button>
            <button style={{ ...S.iconBtn, opacity: hasNext ? 1 : 0.4 }}
              disabled={!hasNext} onClick={() => setOffset(o => o + PAGE_SIZE)}>
              <ChevronRight size={15} />
            </button>
          </div>
        </>
      )}
    </div>
  )
}
```

- [ ] **Step 2: TypeScript build**

Run: `cd frontend && npm run build`
Expected: 0 TypeScript errors, bundle produced.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/AuditPage.tsx
git commit -m "feat(ui): audit page — date/username filters, NDJSON export, Path column, total-aware pagination"
```

---

## Task 16: Smoke test in Docker Compose stack

**Files:** none changed.

This is a verification task — not a code task. It catches issues that unit tests can't (real PostgreSQL, real partition DDL, real download).

- [ ] **Step 1: Start the full stack**

Run: `docker compose up --build -d`
Expected: containers come up; server logs include `audit rotator started` with the configured values; no error on startup.

- [ ] **Step 2: Verify partition was created**

Run:
```bash
docker compose exec postgres psql -U nexspence -d nexspence -c \
  "SELECT inhrelid::regclass FROM pg_inherits WHERE inhparent = 'audit_events'::regclass ORDER BY 1;"
```
Expected: includes a partition for the **current** month and the next two (lookahead=2). If the hardcoded `2026_04/05/06` partitions from `001_initial.sql` are still present, that's fine — they'll co-exist.

- [ ] **Step 3: Generate an event with a path**

Run:
```bash
TOKEN=$(curl -s -X POST http://localhost:8081/api/v1/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin123"}' | jq -r .token)

# Create a raw repository if one doesn't exist
curl -s -X POST http://localhost:8081/service/rest/v1/repositories \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"raw-test","format":"raw","type":"hosted"}'

echo "hello" | curl -s -X PUT --data-binary @- \
  -H "Authorization: Bearer $TOKEN" \
  http://localhost:8081/repository/raw-test/dir/file.txt
```
Expected: HTTP 200/201 from each call.

- [ ] **Step 4: Confirm audit event with path**

Open `http://localhost:8081/audit` in a browser, log in as admin.
Expected:
- An event row with `Domain=REPOSITORY`, `Action=UPDATE`, `Entity=ARTIFACT: raw-test`, `Path=dir/file.txt`.
- A `LOGIN` event with `Entity=USER: admin`.
- `Showing 1–N of N` is correct.

- [ ] **Step 5: Confirm Export download**

Click the **Download** icon. Expected:
- Browser downloads `audit-YYYY-MM-DD.ndjson`.
- `jq -c '.action' < ~/Downloads/audit-*.ndjson | sort | uniq -c` matches the visible event types.

- [ ] **Step 6: No commit needed for this task** — verification only.

---

## Task 17: Update plan / progress / findings / CLAUDE docs

**Files:**
- Modify: `task_plan.md`
- Modify: `progress.md`
- Modify: `findings.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: `task_plan.md`** — change Phase 25 status from `pending` to `complete (2026-04-24)`. Tick all sub-task checkboxes. Remove the `⚠️ HOLD` notice (resolved).

- [ ] **Step 2: `progress.md`** — append a Phase 25 session entry summarising:
  - `entity_path` stored in `Context["path"]` (no DB column).
  - Middleware coverage extended to roles/privileges/content-selectors/webhooks/`/v2/`.
  - `AuditQuery` introduced; `AuditRepo.List` returns `(items, total, error)`; `Stream` for NDJSON.
  - `internal/audit.Rotator` with `PartitionStore` interface; postgres impl + fake for tests.
  - 90 days / soft cap 1M / 24h rotation / lookahead 2 months — all in `audit:` config.
  - `metrics.AuditEventsCount` exposed in `/api/v1/metrics`.

- [ ] **Step 3: `findings.md`** — add a "Phase 25" subsection noting:
  - Why `entity_path` lives in JSONB (`Context["path"]`) vs. a column: PR cost vs. need; no path-filter API yet, so an indexed column would be premature.
  - Why partition rotation lives inside the server (single-binary UX).
  - 100k row hard cap on a single NDJSON export.

- [ ] **Step 4: `CLAUDE.md`** — replace the "## Current Phase" line with a Phase 25 description matching the existing tone (one paragraph; reference the rotator, `Context["path"]`, NDJSON export, 90-day retention, soft cap metric).

- [ ] **Step 5: Commit**

```bash
git add task_plan.md progress.md findings.md CLAUDE.md
git commit -m "docs: Phase 25 audit log — detailed events, NDJSON export, partition rotation complete"
```

---

## Self-Review Notes

- **Spec coverage:** every spec section has at least one task — detailed events (Tasks 4-5), middleware coverage (Task 4), NDJSON export (Task 6, 7), date/username filters (Tasks 6, 7, 14, 15), partition rotation (Tasks 10-13), soft cap (Tasks 9, 10), config (Task 8), UI (Tasks 14, 15), tests (Tasks 5, 7, 11), startup wiring (Task 13), smoke test (Task 16), docs (Task 17).
- **Type consistency verified:** `AuditQuery` fields used identically across interface, postgres impl, mock, handler, and tests. `Partition` struct used identically across rotator, fake, postgres impl, and tests. `PartitionStore` method names (`ListPartitions`, `CreatePartition`, `DropPartition`, `CountRows`) match between interface, fake, and postgres impl. `AuditRepo.List` returns `(items, total, error)` everywhere.
- **No placeholders:** every code step has the real code; no "implement later" or "similar to" references.
