# Phase 45: Per-Repository Export / Import — Design Spec

**Date:** 2026-04-28  
**Status:** approved

## Goal

Allow operators to export a single repository (metadata + blobs) to a tar.gz archive and import it into the same or a different Nexspence instance. Use cases: moving repos between instances (dev → prod) and single-repo backup/restore.

---

## Architecture

Extend the existing `BackupService` (Variant A) with two new methods. Reuse `writeJSONEntry`, gzip/tar setup, and blob-iteration patterns already in `backup_service.go`. No new service type.

---

## Backend — BackupService

### ExportRepo

```go
func (s *BackupService) ExportRepo(ctx context.Context, repoName string, w io.Writer) error
```

Writes a gzip-compressed tar archive scoped to one repository:

| Entry | Content |
|-------|---------|
| `manifest.json` | `{ version: "1", created: RFC3339, repoName: "<name>" }` |
| `repository.json` | Single `domain.Repository` record |
| `components.json` | All components for this repository (paginated 500/page) |
| `assets.json` | All assets for this repository (paginated 500/page) |
| `blobs/<key>` | Blob bytes, deduplicated by key |

Users, roles, and cleanup policies are **not included** — they are system-wide.

Returns error if the repository does not exist.

### ImportRepo

```go
func (s *BackupService) ImportRepo(ctx context.Context, r io.Reader, targetName string, conflictMode string) (*ImportRepoStats, error)
```

**Parameters:**
- `targetName` — if non-empty, use this name for the repository instead of the archived name
- `conflictMode` — `"skip"` | `"merge"` | `"rename"`

**Conflict modes:**
| Mode | Behaviour |
|------|-----------|
| `skip` | If repo already exists, skip creating components/assets that already exist (assets matched by `path`, components by `name`+`version`+`group`). Default. |
| `merge` | Same as `skip` — alias kept for UI clarity. |
| `rename` | `targetName` must be non-empty (`400` if blank) and not already taken (`409` if taken). Creates a new repo under `targetName`; all components and assets are imported fresh. |

**Import sequence:** archive read → repo create/resolve → components → assets → blobs.

```go
type ImportRepoStats struct {
    Repository   string `json:"repository"`
    Components   int    `json:"components"`
    Assets       int    `json:"assets"`
    Blobs        int    `json:"blobs"`
    ConflictMode string `json:"conflictMode"`
}
```

Blobs are buffered in memory during the first (and only) tar pass, consistent with `Restore`.

---

## API Endpoints

### Export

```
GET /api/v1/repositories/:name/export
```

- **Auth:** admin-only  
- **Response:** streaming tar.gz  
  - `Content-Disposition: attachment; filename=nexspence-repo-{name}-{ts}.tar.gz`  
  - `Content-Type: application/x-tar`  
  - `Transfer-Encoding: chunked`  
- **Errors:** `404` if repo not found (detected before streaming starts)

### Import

```
POST /api/v1/repositories/import
```

- **Auth:** admin-only  
- **Request:** `multipart/form-data`
  - `file` — tar.gz archive  
  - `targetName` — string, optional  
  - `conflictMode` — `skip` | `merge` | `rename`, default `skip`  
- **Response 200:**
  ```json
  { "imported": { "repository": "my-repo", "components": 42, "assets": 87, "blobs": 87, "conflictMode": "skip" } }
  ```
- **Errors:** `400` invalid archive or missing `file` field or `rename` with blank `targetName`, `409` rename conflict (name taken)

### Router wiring (`router.go`)

```go
admin.GET("/api/v1/repositories/:name/export", backupH.ExportRepo)
admin.POST("/api/v1/repositories/import",       backupH.ImportRepo)
```

Note: the export route uses `:name` which conflicts with the existing `GET /api/v1/repositories` list route only if Gin confuses them — it won't because `/repositories/import` is a fixed segment and `/repositories/:name/export` has a suffix `/export`.

---

## Handler (`backup.go`)

Two new methods on `BackupHandler`:

**`ExportRepo`** — reads `:name` param, calls `svc.ExportRepo`, sets headers, streams. If repo not found, returns 404 before writing body (check via `Repos.Get` first).

**`ImportRepo`** — parses multipart form (`r.FormFile("file")`, `r.FormValue("targetName")`, `r.FormValue("conflictMode")`), calls `svc.ImportRepo`, returns JSON stats. Falls back to raw body if content-type is not multipart.

---

## Frontend

### RepositoriesPage — Export button on RepoRow

Add `<HoloButton icon={<Download size={14} />} title="Export repository" />` in the admin actions group (before Settings button). On click:

```ts
const res = await nexspenceApi.exportRepo(repo.name)
// blob download — same pattern as exportBackup
const url = URL.createObjectURL(new Blob([res.data]))
const a = document.createElement('a')
a.href = url
a.download = `nexspence-repo-${repo.name}-${ts}.tar.gz`
a.click()
URL.revokeObjectURL(url)
```

Uses axios (not `window.location.href`) so the JWT Authorization header is sent.

### AdminPage — Import section in "Backup" tab

Add a new section below the existing Export/Restore buttons, separated by a thin divider:

```
── Import Repository ──────────────────────
File: [ choose .tar.gz file ]
Target name: [ input, optional ]
Conflict: [ Select: Skip / Merge / Rename ]
[ Import ]
```

On success, show inline stats card: `Imported {components} components, {assets} assets into "{repository}"`.  
On error, show inline error message.

No `<Wizard>` component — inline form in the card (same pattern as Restore section above it).

### API client additions (`client.ts`)

```ts
exportRepo: (name: string) =>
  apiClient.get(`/api/v1/repositories/${encodeURIComponent(name)}/export`, { responseType: 'blob' }),

importRepo: (file: File, targetName: string, conflictMode: string) => {
  const fd = new FormData()
  fd.append('file', file)
  fd.append('targetName', targetName)
  fd.append('conflictMode', conflictMode)
  return apiClient.post<{ imported: ImportRepoStats }>('/api/v1/repositories/import', fd)
},
```

---

## Files Touched

| File | Change |
|------|--------|
| `internal/service/backup_service.go` | Add `ExportRepo`, `ImportRepo`, `ImportRepoStats` |
| `internal/service/backup_service_test.go` | Tests for both new methods |
| `internal/api/handlers/backup.go` | Add `ExportRepo`, `ImportRepo` handler methods |
| `internal/api/router.go` | Wire two new routes |
| `frontend/src/api/client.ts` | Add `exportRepo`, `importRepo` |
| `frontend/src/pages/RepositoriesPage.tsx` | Export button on `RepoRow` |
| `frontend/src/pages/AdminPage.tsx` | Import section in backup tab |

---

## Error Handling

- Export: 404 if repo not found (check before streaming). Body already committed on streaming error — log only.
- Import: 400 for gzip/tar parse errors, 400 for missing `file` field, 409 for rename conflict (repo name taken), 500 for DB errors.

---

## Testing

- `ExportRepo` — test that archive contains correct entries; test 404 on unknown repo
- `ImportRepo skip` — import into empty instance; import again (skip, counts stay 0)
- `ImportRepo merge` — same as skip (alias)
- `ImportRepo rename` — creates repo under new name; 409 if new name taken
- Handler tests — `ExportRepo` sets correct headers; `ImportRepo` parses form fields correctly
