# Phase 49: Change Repository Blob Store — Content Migration Task

**Date:** 2026-05-02  
**Status:** approved  
**Goal:** Move all blob files of a repository from one blob store to another in the background, without downtime, with pause/resume support.

---

## Decisions

| Question | Choice | Rationale |
|----------|--------|-----------|
| Migration approach | Background copy (A) | Standard pattern; new writes during migration are rare; second pass possible |
| Progress storage | New table `blob_store_migrations` (A) | Clean schema; migration_jobs is tailored for Nexus→Nexspence migrations |
| Pause/Resume | Full resume support (A) | Free: already-migrated assets have blob_store_id = target, so they're skipped on re-run |
| UI placement | Edit Repository modal (A) | Natural UX: change store → offer to migrate existing content |

---

## Database

### Migration: `013_blob_store_migrations.sql`

```sql
CREATE TABLE blob_store_migrations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repository_name TEXT    NOT NULL,
    source_store_id UUID    NOT NULL REFERENCES blob_stores(id),
    target_store_id UUID    NOT NULL REFERENCES blob_stores(id),
    status          TEXT    NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','running','paused','cancelled','done','failed')),
    total_assets    INT     NOT NULL DEFAULT 0,
    done_assets     INT     NOT NULL DEFAULT 0,
    total_bytes     BIGINT  NOT NULL DEFAULT 0,
    done_bytes      BIGINT  NOT NULL DEFAULT 0,
    error_message   TEXT,
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ON blob_store_migrations (repository_name);
CREATE INDEX ON blob_store_migrations (status) WHERE status IN ('pending','running');
```

---

## Domain

```go
// internal/domain/types.go
type BlobStoreMigration struct {
    ID             string
    RepositoryName string
    SourceStoreID  string
    TargetStoreID  string
    Status         string // pending | running | paused | cancelled | done | failed
    TotalAssets    int
    DoneAssets     int
    TotalBytes     int64
    DoneBytes      int64
    ErrorMessage   *string
    StartedAt      *time.Time
    FinishedAt     *time.Time
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
```

---

## Repository Layer

### Interface (`internal/repository/interfaces.go`)

```go
type BlobStoreMigrationRepo interface {
    Create(ctx context.Context, m *domain.BlobStoreMigration) error
    Get(ctx context.Context, id string) (*domain.BlobStoreMigration, error)
    // GetActiveByRepo returns pending|running migration for the repo (nil if none).
    // Used to enforce single-active-migration-per-repo invariant.
    GetActiveByRepo(ctx context.Context, repoName string) (*domain.BlobStoreMigration, error)
    // GetLatestByRepo returns the most recent migration for the repo regardless of status.
    // Used by GET API endpoint to show last result (done/failed/cancelled included).
    GetLatestByRepo(ctx context.Context, repoName string) (*domain.BlobStoreMigration, error)
    List(ctx context.Context) ([]domain.BlobStoreMigration, error)
    UpdateProgress(ctx context.Context, id string, doneAssets int, doneBytes int64) error
    UpdateStatus(ctx context.Context, id string, status string, errMsg *string) error
    SetTotals(ctx context.Context, id string, totalAssets int, totalBytes int64) error
    FinishMigration(ctx context.Context, id string, status string, errMsg *string) error
}
```

`GetActiveByRepo` returns a migration with status `pending` or `running` for the given repository, or `nil` if none.

### Postgres implementation: `internal/repository/postgres/blob_store_migration_repo.go`

Standard pgx implementation. `UpdateProgress` and `SetTotals` always touch `updated_at`.

---

## Service Layer

### `BlobStoreMigrationService` (`internal/service/blob_store_migration_service.go`)

```go
type BlobStoreMigrationService struct {
    migrations BlobStoreMigrationRepo
    assets     AssetRepo
    repos      RepositoryRepo
    blobs      BlobStoreRepo
    registry   *storage.Registry
    // cancels holds a cancel func per migration ID (for Cancel)
    mu      sync.Mutex
    cancels map[string]context.CancelFunc
}

func (s *BlobStoreMigrationService) Start(ctx context.Context, repoName, targetStoreID string) (*domain.BlobStoreMigration, error)
func (s *BlobStoreMigrationService) Cancel(ctx context.Context, id string) error
func (s *BlobStoreMigrationService) Get(ctx context.Context, id string) (*domain.BlobStoreMigration, error)
func (s *BlobStoreMigrationService) GetActiveByRepo(ctx context.Context, repoName string) (*domain.BlobStoreMigration, error)
func (s *BlobStoreMigrationService) ResumeAll(ctx context.Context) error  // called on server startup
```

### Migration goroutine (`runMigration`)

```
1. Set status = running, started_at = now
2. Query: SELECT DISTINCT blob_key, blob_store_id, size_bytes FROM assets
          WHERE repository_name = ? AND blob_store_id = source_store_id
3. SetTotals(len, sum(size_bytes))
4. For each unique blob_key:
   a. Check ctx.Done() → if cancelled: set status=cancelled, return
   b. sourceStore = registry.Get(sourceStoreDescriptor)
   c. targetStore = registry.Get(targetStoreDescriptor)
   d. If targetStore.Exists(blob_key): skip copy (resume) — blob already there
   e. Else: rc, size = sourceStore.Get(blob_key)
            targetStore.Put(blob_key, rc, size)
            blobs.UpdateUsedBytes(ctx, targetStoreName, +size)
   f. UPDATE assets SET blob_store_id = target_store_id
      WHERE blob_key = ? AND repository_name = ?
   g. UpdateProgress(doneAssets+1, doneBytes+size)
      // doneAssets/doneBytes incremented regardless of skip in step d —
      // asset migration (step f) always happens; only the blob copy is skipped
5. UPDATE repositories SET blob_store_id = target_store_id WHERE name = ?
6. FinishMigration(id, "done", nil)
```

**Source blobs are not deleted** — existing GC (`ListAllBlobKeys` + `CountByBlobKey`) handles orphan cleanup.

**Error handling:** any error in step 4e/4f sets status=`failed` with `error_message` and stops the goroutine. The migration can be retried from the UI (re-POST with same target); already-migrated assets are skipped via `Exists` check.

### Resume on server startup (`cmd/server/main.go`)

```go
migSvc.ResumeAll(ctx)
// Finds all migrations with status=running|pending, resets to pending, relaunches goroutines
```

---

## API

All endpoints require admin auth (`RequireAdmin` middleware).

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/repositories/:name/migrate-blob-store` | Start migration |
| `GET` | `/api/v1/repositories/:name/blob-store-migration` | Get active/last migration |
| `DELETE` | `/api/v1/repositories/:name/blob-store-migration` | Cancel running migration |

### POST request body
```json
{ "targetStoreId": "uuid" }
```

### Response (all endpoints)
```json
{
  "id": "uuid",
  "repositoryName": "my-repo",
  "sourceStoreId": "uuid",
  "targetStoreId": "uuid",
  "status": "running",
  "totalAssets": 2048,
  "doneAssets": 1247,
  "totalBytes": 7310341120,
  "doneBytes": 4509715046,
  "errorMessage": null,
  "startedAt": "2026-05-02T10:00:00Z",
  "finishedAt": null
}
```

**Error cases:**
- `400` — target store same as current, or target store not found
- `409` — migration already running for this repo
- `404` — no active migration (GET/DELETE)

---

## Frontend

### Edit Repository modal (`RepositoriesPage.tsx`)

**State additions:**
```ts
activeMigration: BlobStoreMigration | null  // loaded on modal open via GET
migrationPolling: ReturnType<typeof setInterval> | null
```

**Flow:**

1. On modal open: `GET /api/v1/repositories/:name/blob-store-migration`
   - If `running`/`pending`: show progress section immediately
   - If `done`/`failed`/`null`: show blob store selector normally

2. When selected store differs from saved `repo.blobStoreId`:
   - Existing amber warning: *"⚠ Existing artifacts stay on the original store..."*
   - New button: **"Migrate Content"** (disabled if `activeMigration != null`)

3. On "Migrate Content" click:
   - `POST /api/v1/repositories/:name/migrate-blob-store` `{targetStoreId}`
   - Start polling GET every 2s
   - Show progress section

4. **Progress section:**
```
Migrating content to "fast-ssd"
[████████████░░░░░░░░░] 61%   1 247 / 2 048 assets   4.2 GB / 6.8 GB
                                                      [Cancel]
```

5. **Terminal states:**
   - `done` → green checkmark, stop polling, refresh repo data (blob_store_id now updated)
   - `failed` → red error message + "Retry" button (re-POST same target)
   - `cancelled` → neutral message, selector re-enabled

6. **RepoCard badge:** if migration running for this repo, show small `⟳ migrating` indicator on the card (loaded alongside repo list).

### New API client helpers (`client.ts`)
```ts
startBlobStoreMigration(repoName: string, targetStoreId: string): Promise<BlobStoreMigration>
getBlobStoreMigration(repoName: string): Promise<BlobStoreMigration | null>
cancelBlobStoreMigration(repoName: string): Promise<void>
```

---

## Testutil Mock

Add `BlobStoreMigrationRepo` mock to `internal/testutil/mocks.go` with standard stub methods.

---

## Tests

| File | Tests |
|------|-------|
| `blob_store_migration_service_test.go` | Start creates record + launches goroutine; Cancel sets cancelled; ResumeAll relaunches pending; blob already in target is skipped (resume); failed copy sets status=failed |
| `blob_store_migration_handler_test.go` | POST 201; POST 409 when active; GET 200 with progress; DELETE 200; DELETE 404 |

---

## Files

| File | Action |
|------|--------|
| `internal/db/migrations/013_blob_store_migrations.sql` | new |
| `internal/domain/types.go` | add `BlobStoreMigration` struct |
| `internal/repository/interfaces.go` | add `BlobStoreMigrationRepo` interface |
| `internal/repository/postgres/blob_store_migration_repo.go` | new |
| `internal/testutil/mocks.go` | add mock |
| `internal/service/blob_store_migration_service.go` | new |
| `internal/service/blob_store_migration_service_test.go` | new |
| `internal/api/handlers/blob_store_migration.go` | new |
| `internal/api/handlers/blob_store_migration_handler_test.go` | new |
| `internal/api/router.go` | wire routes + ResumeAll on startup |
| `cmd/server/main.go` | call `migSvc.ResumeAll(ctx)` |
| `frontend/src/api/client.ts` | 3 new helpers |
| `frontend/src/pages/RepositoriesPage.tsx` | Edit modal UI + polling + RepoCard badge |
