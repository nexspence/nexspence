# Phase 25 — Audit Log: Detailed Events, NDJSON Export, Retention

**Status:** Approved design — ready for implementation plan
**Date:** 2026-04-24
**Owner:** Phase 25 implementation

## Goal

Three independent improvements to audit logging:

1. **Detailed events** — every audit event records the artifact path / docker reference where applicable; broaden middleware coverage to all admin endpoints currently missing.
2. **NDJSON export** — admins can download an audit-log slice as a streaming `.ndjson` file from the UI.
3. **Retention** — events older than 90 days are automatically dropped via PostgreSQL partition rotation; soft cap on total rows surfaces a warning when exceeded.

## Non-goals

- Per-event-type retention (e.g., login events expiring sooner) — single 90-day window for all events.
- Hard cap row deletion — soft cap is observability only; no row deletion based on count.
- Indexed search on `entity_path` — path is stored in JSONB without index; no path-based filter API.
- Tracking GET/HEAD/OPTIONS requests — downloads use `assets.download_count`; aud iting all reads would dominate the table.
- Per-domain retention configuration in `config.yaml`.

## Architecture

Five units of change, one new package:

| Unit | Type | Description |
|------|------|-------------|
| `internal/domain/types.go` | extend | `AuditEvent.Context` already exists; no struct change needed. |
| `internal/api/audit_middleware.go` | extend | Broaden `isAuditablePath`; extract artifact path into `Context["path"]`. |
| `internal/api/handlers/audit.go` + `internal/repository/postgres/audit_repo.go` | extend | Add `from`, `to`, `username` filters; add `format=ndjson` streaming; return `total` for pagination. |
| `internal/audit/rotator.go` | **new** | Background goroutine: pre-create future partitions, drop old, observe soft cap. |
| `frontend/src/pages/AuditPage.tsx` | extend | Date range / username filter inputs; Export button; Path column; pagination fix. |

Boundaries:
- `internal/audit` package depends only on `*pgxpool.Pool`, `domain.AuditConfig`, `*zap.Logger`. No gin / handler imports.
- Middleware change is additive — no existing call sites break.
- Frontend type changes are additive (`context?: Record<string, any>` is optional).

## Data Flow

### Artifact upload (audit-record path)

```
PUT /repository/maven-hosted/com/example/foo/1.0/foo-1.0.jar
  → maven format handler stores artifact (success)
  → AuditMiddleware (after handler):
      classifyPath() →
        domain="REPOSITORY", action="CREATE",
        entityType="ARTIFACT", entityName="maven-hosted",
        ctxData={"path": "com/example/foo/1.0/foo-1.0.jar"}
      e.Context = ctxData
      go auditRepo.Write(ctx, e)
```

### Server startup (rotator)

```
cmd/server/main.go: serve()
  ↓ (after migrate.Up, before router.Run)
  rotator := audit.NewRotator(pool, cfg.Audit, logger)
  rotator.RunOnce(ctx)   // sync — guarantees current-month partition exists
  go rotator.Run(ctx)    // async — ticks every 24h
```

`RunOnce` performs a single `tick` (ensure-future + drop-old + soft-cap-check). If it errors, log a warn and continue startup (audit is best-effort, not blocking).

## Event Schema and Coverage

### `AuditEvent` struct: unchanged

`entity_path` lives in the existing `Context map[string]any` JSONB column under key `"path"`. Frontend reads `event.context?.path`. No DB migration required for the path field.

### Auditable path prefixes (extended)

```go
prefixes := []string{
    "/service/rest/v1/repositories",
    "/service/rest/v1/security/users",
    "/service/rest/v1/security/roles",                // NEW (Phase 19 RBAC)
    "/service/rest/v1/security/privileges",           // NEW
    "/service/rest/v1/security/content-selectors",    // NEW
    "/service/rest/v1/blobstores",
    "/service/rest/v1/cleanup-policies",
    "/api/v1/webhooks",                               // NEW (Phase 14)
    "/api/v1/login",
    "/repository/",
    "/v2/",                                           // NEW (Docker push/delete)
}
```

### `classifyPath` — return signature

Extends to return `ctxData map[string]any`. Middleware merges into `e.Context` before calling `Write`.

```go
case strings.HasPrefix(path, "/repository/"):
    domainStr  = "REPOSITORY"
    entityType = "ARTIFACT"
    entityName = c.Param("repoName")
    if p := c.Param("path"); p != "" {
        ctxData["path"] = strings.TrimPrefix(p, "/")
    }
case strings.HasPrefix(path, "/v2/"):
    domainStr  = "REPOSITORY"
    entityType = "ARTIFACT"
    entityName = c.Param("repoName")
    if strings.Contains(path, "/manifests/") {
        ctxData["path"] = "manifests/" + lastSegment(path)
    } else if strings.Contains(path, "/blobs/") {
        ctxData["path"] = "blobs/" + lastSegment(path)
    }
```

### IP capture

`c.ClientIP()` already returns the correct IP based on Gin's trusted-proxies configuration. No change needed; test verifies `RemoteIP` is non-empty for requests through middleware.

### Excluded events

- `GET /repository/...` (artifact downloads) — too noisy; `assets.download_count` already tracks this.
- `OPTIONS`, `HEAD` — not mutations.

## Export API

### Endpoint

```
GET /service/rest/v1/audit
  ?domain=REPOSITORY        (existing, optional)
  &action=CREATE            (existing, optional)
  &username=admin           (NEW, optional, exact match)
  &from=2026-04-01          (NEW, optional, ISO date or RFC3339)
  &to=2026-04-24            (NEW, optional, exclusive)
  &limit=50                 (existing, default 100, max 1000)
  &offset=0                 (existing)
  &format=ndjson            (NEW, optional)
```

### Without `format` (default — paginated JSON)

```json
{ "items": [...], "total": 12345 }
```

The new `total` field enables correct pagination on the last page (current code uses `events.length === PAGE_SIZE` which produces a phantom "next" button on full-page boundaries).

### `format=ndjson` (streaming)

```
HTTP/1.1 200 OK
Content-Type: application/x-ndjson
Content-Disposition: attachment; filename="audit-2026-04-24.ndjson"
Transfer-Encoding: chunked

{"id":1,"eventTime":"...","username":"admin",...}\n
{"id":2,...}\n
```

`limit` and `offset` are ignored for NDJSON. A hard safety cap of **100 000 rows per export** is enforced; queries returning more rows respond with HTTP 400 and a hint to narrow the date range. Auth: admin-only (same as listing).

### `AuditRepo` signature change

```go
type AuditQuery struct {
    Domain, Action, Username string
    From, To                 *time.Time
    Limit, Offset            int
}

List(ctx, q AuditQuery) ([]domain.AuditEvent, int /*total*/, error)
Stream(ctx, q AuditQuery, fn func(domain.AuditEvent) error) error
```

`Stream` uses `pgx.Rows` with the same WHERE clause as `List`, no `LIMIT`, with a guard that errors after 100 000 rows. `List` returns `(items, total, error)` — one handler call site updates.

## Retention — Partition Rotator

### Configuration (new section in `config.yaml`)

```yaml
audit:
  retention_days: 90
  soft_cap: 1000000          # 0 = disabled
  rotation_interval: 24h
  lookahead_months: 2
```

```go
type AuditConfig struct {
    RetentionDays    int           `mapstructure:"retention_days"`
    SoftCap          int64         `mapstructure:"soft_cap"`
    RotationInterval time.Duration `mapstructure:"rotation_interval"`
    LookaheadMonths  int           `mapstructure:"lookahead_months"`
}
```

Defaults if unspecified: 90 / 1_000_000 / 24h / 2.

### Partition naming

`audit_events_YYYY_MM` (matches existing convention from `001_initial.sql`).

### Pre-create future partitions

For the **current month** plus the next `LookaheadMonths` months (with default `LookaheadMonths=2`, the rotator ensures partitions for `[current, current+1, current+2]` — 3 total — always exist):

```sql
CREATE TABLE IF NOT EXISTS audit_events_2026_05
  PARTITION OF audit_events
  FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
```

`IF NOT EXISTS` makes it idempotent — safe at every tick and safe alongside the hardcoded `2026_04/05/06` partitions in `001_initial.sql`.

### Drop old partitions

Discover via:

```sql
SELECT inhrelid::regclass::text AS partition_name,
       pg_get_expr(relpartbound, inhrelid) AS bound
  FROM pg_inherits
  JOIN pg_class ON pg_class.oid = inhrelid
 WHERE inhparent = 'audit_events'::regclass;
```

Parse `bound` (e.g., `FOR VALUES FROM ('2026-04-01') TO ('2026-05-01')`) to extract the `to` date. If `to_date < NOW() - retention_days`:

```sql
ALTER TABLE audit_events DETACH PARTITION audit_events_YYYY_MM;
DROP TABLE audit_events_YYYY_MM;
```

Each DETACH+DROP runs in its own transaction so one failure doesn't block the rest. Each successful drop logs `partition`, `to_date`, and pre-drop row count.

### Soft cap

```go
var n int64
pool.QueryRow(ctx, "SELECT COUNT(*) FROM audit_events").Scan(&n)
metrics.AuditEventsCount.Store(n)  // new atomic.Int64 in internal/metrics/metrics.go
if cfg.SoftCap > 0 && n > cfg.SoftCap {
    log.Warn("audit_events soft cap exceeded",
        zap.Int64("count", n), zap.Int64("cap", cfg.SoftCap))
}
```

### Tick loop

```go
func (r *Rotator) Run(ctx context.Context) {
    r.tick(ctx) // first tick immediate
    t := time.NewTicker(r.cfg.RotationInterval)
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

func (r *Rotator) tick(ctx context.Context) {
    if err := r.ensureFuturePartitions(ctx); err != nil {
        r.log.Error("ensure partitions", zap.Error(err))
    }
    if err := r.dropOldPartitions(ctx); err != nil {
        r.log.Error("drop old partitions", zap.Error(err))
    }
    r.checkSoftCap(ctx)
}
```

### Failure modes

- DB unavailable → log error, retry on next tick (24h). No exponential retry (YAGNI).
- Partition CREATE fails for non-`already-exists` reason → log, continue.
- `001_initial.sql` hardcoded partitions remain — `IF NOT EXISTS` handles overlap; eventually they age out and get dropped naturally.

## Frontend Changes

### `AuditPage.tsx`

**New filter inputs** in the existing `S.filters` row:

- Two `<input type="date">` — From and To, styled to match `Select` (`background: rgba(255,255,255,0.06)`, `border: 1px solid rgba(255,255,255,0.1)`, `padding: 8px 10px`, `borderRadius: 8`).
- One `<input type="text" placeholder="username…">` for username filter.
- New **Export** button (lucide `Download` icon) next to the existing RefreshCw button. On click: builds the same query string as the current view but with `&format=ndjson`, then `window.location.href = url` (browser handles download via `Content-Disposition`).

**New "Path" column** in the table, between "Entity" and "IP":

```tsx
<td style={{...S.td, ...S.mono, maxWidth: 320}}>
  {e.context?.path ? (
    <span title={e.context.path}
      style={{display:'inline-block', maxWidth:'100%',
              overflow:'hidden', textOverflow:'ellipsis', whiteSpace:'nowrap'}}>
      {e.context.path}
    </span>
  ) : '—'}
</td>
```

**`AuditEvent` interface** — add `context?: Record<string, any>`.

**Pagination fix** — replace `hasNext = events.length === PAGE_SIZE` with `hasNext = offset + events.length < total`, using `total` from the new response shape.

**`nexusApi.listAuditEvents`** in `client.ts` — extend opts with optional `from`, `to`, `username`. NDJSON not consumed via axios — Export is a plain `<a href>` / `window.location.href`.

## Testing

### Backend (Go)

| File | Tests |
|------|-------|
| `internal/api/audit_middleware_test.go` (extend) | (1) PUT `/repository/maven/x/y/foo.jar` → AuditEvent has `Context["path"] == "x/y/foo.jar"`. (2) PUT `/v2/myrepo/manifests/v1` → `Context["path"] == "manifests/v1"`. (3) POST `/api/v1/webhooks` → event written for new prefix. (4) `RemoteIP` is non-empty. |
| `internal/api/handlers/audit_test.go` (new) | (1) `?from=&to=` filtering — insert 3 events on different dates, only in-range returned. (2) `?username=` exact match. (3) `?format=ndjson` — Content-Type, Content-Disposition correct, each line parses as JSON, count matches. (4) `?format=ndjson` with filter exceeding 100 000 rows → 400. (5) `total` in JSON response is correct across multiple pages. |
| `internal/audit/rotator_test.go` (new) | (1) `ensureFuturePartitions` is idempotent — double call doesn't panic. (2) On empty `audit_events`, creates expected number of partitions. (3) `dropOldPartitions` — manually create `audit_events_2024_01`, set retention=90d, drop runs → partition gone from `pg_inherits`. (4) Partitions inside the window are NOT dropped. (5) `checkSoftCap` log line includes `count` and `cap` when exceeded. |

Rotator tests run against real PostgreSQL (testcontainers — already used in `cleanup_service_test.go`). Mocks would defeat the purpose of testing DDL.

### Frontend (manual)

- [ ] From/To filter narrows results.
- [ ] Username filter works.
- [ ] Export → `audit-YYYY-MM-DD.ndjson` downloads; `jq -c < file | wc -l` matches UI count.
- [ ] Pagination correct on last page (no phantom "next").
- [ ] Path column shows path for PUT artifact, "—" for login / user management.

### Definition of Done

- [ ] `go test ./...` green.
- [ ] `go vet` + `golangci-lint` clean.
- [ ] `vite build` with no TS errors.
- [ ] Smoke test: upload artifact → event in UI with path → Export → open NDJSON, see event.
- [ ] `task_plan.md` Phase 25 → status complete; checklists filled.
- [ ] `progress.md`, `findings.md`, `CLAUDE.md` updated.

## Open Questions

None — all four retention questions answered (90d / soft cap with metric / partition rotation / single window).
