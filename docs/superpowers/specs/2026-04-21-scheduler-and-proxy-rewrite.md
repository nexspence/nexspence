# Spec: Per-Policy Cron Scheduler + Proxy URL Rewriting (Helm/NuGet)

**Date:** 2026-04-21
**Status:** approved

---

## Overview

Two independent improvements:

1. **Per-policy cron scheduler** — replace the hardcoded 6-hour global cleanup interval with a `robfig/cron`-based scheduler that respects each policy's `schedule_cron` field.
2. **Proxy URL rewriting** — fix helm and nuget proxy repositories so that index files (`index.yaml`, `index.json`) return URLs pointing to our proxy, not to the upstream. Clients then download artifacts through the cache.

---

## Part 1: Per-Policy Cron Scheduler

### Problem

`CleanupService.StartScheduler` runs all policies every 6 hours regardless of their `schedule_cron` field. The field exists in `domain.CleanupPolicy` and is stored in the DB but is never read.

### Design

**New dependency**: `github.com/robfig/cron/v3`

**`CleanupService` changes** (`internal/service/cleanup_service.go`):

- Add fields:
  ```go
  cron      *cron.Cron
  entryIDs  map[string]cron.EntryID  // policy.ID → cron entry
  mu        sync.Mutex               // guards entryIDs
  ```
- Replace `StartScheduler(ctx, interval)` with `StartCronScheduler(ctx context.Context, defaultSchedule string)`:
  1. Creates `cron.New()` and starts it.
  2. Loads all policies from DB.
  3. For each enabled policy: registers a cron job using `policy.ScheduleCron` if non-empty, else `defaultSchedule`.
  4. Runs until `ctx.Done()`, then calls `cron.Stop()`.
- Add `ReloadPolicy(ctx context.Context, policyID string)`:
  - If policy exists and is enabled: remove old entry (if any), add new cron job.
  - If policy was deleted or disabled: remove entry only.
  - Called by `CleanupHandler` after Create, Update, Delete.

**Config** (`internal/config/config.go` + `config.yaml`):

```yaml
cleanup:
  default_schedule: "0 */6 * * *"   # used when policy.schedule_cron is empty
```

Add `CleanupConfig struct { DefaultSchedule string }` to `Config`.

**`router.go`** changes:
- Remove: `go cleanupSvc.StartScheduler(context.Background(), 6*time.Hour)`
- Add: `go cleanupSvc.StartCronScheduler(context.Background(), cfg.Cleanup.DefaultSchedule)`
- Pass `cleanupSvc` into `CleanupHandler` so it can call `ReloadPolicy`.

**`CleanupHandler`** (`internal/api/handlers/cleanup.go`):
- Gains reference to `*service.CleanupService`.
- After successful Create, Update, Delete of a policy: calls `cleanupSvc.ReloadPolicy(ctx, id)`.

### Error handling

- If `schedule_cron` is an invalid cron expression: log a warning, fall back to `defaultSchedule` for that policy.
- If DB is unavailable at scheduler startup: log and retry on next reload (policies loaded lazily when handler calls ReloadPolicy).

### What is NOT changing

- `RunPolicy` and `RunAll` methods remain unchanged — cron just calls them.
- Existing `StartScheduler` method is removed (no callers outside router.go after the change).

---

## Part 2: Proxy URL Rewriting

### Problem

Helm and NuGet proxy repositories cache upstream index files verbatim. These index files contain URLs pointing to the upstream (e.g., `https://charts.bitnami.com/bitnami/nginx-15.0.0.tgz`). Clients follow these URLs directly, bypassing the proxy cache.

### Helm (`internal/formats/helm/handler.go`)

**Affected path**: `GET /index.yaml` on a proxy repository.

**New function** `fetchAndRewriteHelmIndex(c *gin.Context, repo *domain.Repository, baseURL string)`:
1. Call `repoproxy.RemoteURL(repo)` to get upstream base.
2. HTTP GET `{remoteBase}/index.yaml` via `http.DefaultClient` (short timeout: 30s).
3. Parse response body with `gopkg.in/yaml.v3` into a `map[string]any`.
4. Iterate `entries` map → each entry is `[]chartEntry` → each entry has `urls []string`.
5. For each URL: extract the filename (last path segment), rewrite to `{baseURL}/repository/{repoName}/{filename}`.
6. Marshal back to YAML, write `200 application/yaml` to client.
7. On any upstream error: return 502 with error message.

**All other proxy paths** (e.g., `chart-1.0.0.tgz`): unchanged, use `repoproxy.ServeGET`.

**Index.yaml is not cached** — fetched fresh on every request. It is small (<100 KB typically) and must reflect upstream additions promptly.

### NuGet (`internal/formats/nuget/handler.go`)

**Affected path**: `GET /index.json` on a proxy repository.

**New function** `fetchAndRewriteNuGetIndex(c *gin.Context, repo *domain.Repository, baseURL string)`:
1. Call `repoproxy.RemoteURL(repo)` to get upstream base.
2. HTTP GET `{remoteBase}/index.json`.
3. Parse JSON into `map[string]any`.
4. Iterate `resources` array. Each resource has `@id` (string URL). Parse upstream URL, extract path suffix after the upstream host, rewrite `@id` to `{baseURL}/repository/{repoName}{suffix}`.
5. Marshal back to JSON, write `200 application/json` to client.
6. On upstream error: 502.

**All other proxy paths** (`.nupkg`, `/v3/flatcontainer/...`, `/v3/registration/...`): unchanged, use `repoproxy.ServeGET`.

**Index.json is not cached** — same rationale as helm.

### APT / YUM

No changes needed. These formats already call `repoproxy.ServeGET` for all GET paths. Their metadata files (`Packages`, `repomd.xml`, `primary.xml`) use relative paths, so clients that use the proxy as their base URL will resolve downloads correctly.

### Docker

Already complete — has dedicated manifest/blob proxy logic with Docker Hub token support. No changes.

---

## Files Changed

| File | Change |
|------|--------|
| `go.mod` / `go.sum` | Add `github.com/robfig/cron/v3` |
| `internal/config/config.go` | Add `CleanupConfig.DefaultSchedule` |
| `config.yaml` | Add `cleanup.default_schedule` |
| `internal/service/cleanup_service.go` | Replace `StartScheduler` with `StartCronScheduler`; add `ReloadPolicy`; add cron fields |
| `internal/api/handlers/cleanup.go` | Accept `*service.CleanupService`; call `ReloadPolicy` after mutations |
| `internal/api/router.go` | Wire new scheduler; pass `cleanupSvc` to `CleanupHandler` |
| `internal/formats/helm/handler.go` | Add `fetchAndRewriteHelmIndex` for proxy `/index.yaml` |
| `internal/formats/nuget/handler.go` | Add `fetchAndRewriteNuGetIndex` for proxy `/index.json` |

---

## Testing

- **Scheduler**: unit test `StartCronScheduler` — mock policies with different `schedule_cron` values, verify correct entries registered; test `ReloadPolicy` adds/removes entries.
- **Helm rewrite**: unit test `fetchAndRewriteHelmIndex` with a mock upstream serving a real-shaped `index.yaml`; assert all `urls` in result point to `baseURL`.
- **NuGet rewrite**: unit test `fetchAndRewriteNuGetIndex` with a mock upstream; assert all `@id` values in `resources` rewritten to `baseURL`.
- Existing `repoproxy` tests unaffected.

---

## Out of Scope

- Cargo, Conan proxy: already use `ServeGET`, no index rewriting needed for their protocols.
- Proxy cache TTL / invalidation: index files are always fetched live; binary cache uses existing blob store.
- APT/YUM signed index (`InRelease` GPG signature rewriting): deferred — current pass-through works for unsigned or externally-verified repos.
