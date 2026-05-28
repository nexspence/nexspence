# Raw Browse & Upload UX — Design Spec

**Date:** 2026-04-22  
**Status:** Approved  
**Scope:** Backlog items — Raw repository Browse tree, file/folder actions, Upload via UI

---

## Overview

Add a hierarchical browse tree for raw-format repositories in `BrowsePage`, mirroring the existing Docker tree pattern. Users can expand folders, click files to see metadata in a detail panel, download/copy-link/delete individual files, delete folders, and upload files via a drag-and-drop modal.

---

## Layout (approved: Option C)

```
[Repo selector] [Upload button] [Refresh button]

┌─────────────────────────┐  ┌──────────────────────┐
│  Raw tree               │  │  File details        │
│                         │  │                      │
│  ▼ 📁 releases          │  │  Name: myapp-1.0...  │
│    ▼ 📁 myapp    🗑      │  │  Path: releases/...  │
│      📄 myapp-1.0.tar.gz│  │  Size: 4.2 MB        │
│         ↓ 🔗 🗑         │  │  SHA256: a3f1…        │
│      📄 myapp-1.0.sha256│  │  Uploaded: Apr 20    │
│  ▶ 📁 tools      🗑      │  │  Downloads: 12       │
│                         │  │                      │
│                         │  │  [↓ Download] [🔗 Copy]│
└─────────────────────────┘  └──────────────────────┘
```

- Upload button visible only for **hosted** repos (group repos are read-only aggregates — PUT on a group returns 405).
- Folder delete button (🗑) appears on hover on folder rows.
- File action buttons (↓ 🔗 🗑) appear on hover on file rows; always visible on selected file.
- All UI text in **English**.

---

## Backend

### New domain type — `domain.RawBrowseAsset` (`internal/domain/types.go`)

```go
type RawBrowseAsset struct {
    Path        string
    SizeBytes   int64
    SHA256      string
    ContentType string
    UpdatedAt   time.Time
    ComponentID string
    RepoName    string
}
```

### New repository method — `AssetRepo.ListRawBrowseAssets`

```go
ListRawBrowseAssets(ctx context.Context, repoNames []string) ([]domain.RawBrowseAsset, error)
```

SQL: `JOIN assets → components → repositories WHERE rep.name = ANY($1) AND lower(trim(rep.format)) = 'raw' ORDER BY a.path`

Returns all assets for the given repo names with path, size, sha256, content_type, updated_at, component_id.

### New handler — `BrowseHandler.RawTree` (`internal/api/handlers/browse_raw.go`)

`GET /api/v1/browse/repositories/:name/raw-tree`

1. Validate repo exists and `format == "raw"`.
2. For `type == group`: expand to `member_names` (same pattern as `DockerTree`). Empty group → return empty root.
3. Call `ListRawBrowseAssets(ctx, repoNames)`.
4. Apply `rbac.FilterPaths` to filter asset paths by user privileges.
5. Build nested tree in Go: split each `asset.Path` on `/`, recursively get-or-create folder nodes, attach file leaf with metadata.
6. Sort: folders first, then files, both alphabetically (case-insensitive).
7. Return:

```json
{
  "repository": "raw-artifacts",
  "format": "raw",
  "root": { "kind": "folder", "label": "/", "path": "/", "children": [...] }
}
```

### Response node shape

```go
type rawBrowseNode struct {
    Kind        string           `json:"kind"` // "folder" | "file"
    Label       string           `json:"label"`
    Path        string           `json:"path"`
    Size        int64            `json:"size,omitempty"`
    SHA256      string           `json:"sha256,omitempty"`
    ContentType string           `json:"contentType,omitempty"`
    UpdatedAt   string           `json:"updatedAt,omitempty"`   // RFC3339
    ComponentID string           `json:"componentId,omitempty"`
    Children    []*rawBrowseNode `json:"children,omitempty"`
}
```

### Route — `internal/api/router.go`

```go
browse.GET("/repositories/:name/raw-tree", browseH.RawTree)
```

### Delete (no new endpoints needed)

- **File delete**: reuses `DELETE /api/v1/browse/repositories/:name/path?path=<exact_path>` (already implemented).
- **Folder delete**: same endpoint with path prefix — `DeleteByPath` already does cascade delete on prefix match.

### Upload (no new endpoints needed)

- Reuses `PUT /repository/:repoName/*path` (raw handler, already implemented).
- Frontend sends XHR with `Content-Type` header and tracks `upload.onprogress`.
- If user has no privilege with `write` action → Upload button is hidden/disabled (checked via `GET /api/v1/me/privileges`).
- Path-level validation is backend-only: if path is forbidden, backend returns 403, modal shows error.

---

## Frontend (`frontend/src/pages/BrowsePage.tsx`)

### New interfaces

```ts
interface RawTreeNode {
  kind: 'folder' | 'file'
  label: string
  path: string
  size?: number
  sha256?: string
  contentType?: string
  updatedAt?: string
  componentId?: string
  children?: RawTreeNode[]
}

interface RawFileSelection {
  path: string
  node: RawTreeNode
}
```

### Detection

```ts
const isRaw = selectedRepo?.format?.toLowerCase() === 'raw'
```

### Query

```ts
useQuery(['rawBrowseTree', repoName], () =>
  nexspenceApi.getRawBrowseTree(repoName).then(r => r.data as { root: RawTreeNode }),
  { enabled: !!repoName && isRaw }
)
```

Add `getRawBrowseTree` to `nexspenceApi` in `client.ts`.

### New component — `RawTreeRows`

Recursive component mirroring `DockerTreeRows`:
- **Folder row**: chevron toggle, folder icon, label, hover-reveal 🗑 button.
- **File row**: file icon (green), monospace label, file size (right-aligned), hover-reveal action buttons (Download ↓, Copy link 🔗, Delete 🗑). Selected file gets `background: rgba(59,130,246,0.12)` outline.
- Click on file → sets `rawSelection` state → loads detail panel.

### Detail panel

Appears to the right of the tree (same `dockerLayout` flex pattern). Shows:
- Name, Path, Content type, Size (human-readable via `formatBytes`), SHA256 (monospace, full), Uploaded, Last downloaded, Downloads, Repository.
- Action buttons: **↓ Download** (programmatic `<a download>` to `GET /repository/:repo/<path>` — browser handles `Content-Disposition: attachment` from the raw handler), **🔗 Copy link** (copies `${window.location.origin}/repository/${repoName}/${node.path}` to clipboard via `navigator.clipboard.writeText`).

### Upload modal

Triggered by **Upload** button in toolbar (visible when `isRaw && selectedRepo.type === 'hosted'`).

States:
1. **Idle / file selected**: drag-and-drop zone (dashed blue border) + destination path field (pre-filled with current tree path + filename) + Cancel / Upload buttons.
2. **Uploading**: progress bar (XHR `onprogress`), Upload button disabled, Cancel cancels the XHR.
3. **Success**: green success message, Done button closes modal, `queryClient.invalidateQueries(['rawBrowseTree', repoName])`.
4. **Error**: red error message with backend message (403 → "Access denied for this path").

Upload uses `XMLHttpRequest` (not `fetch`) to track progress. Path field is editable — user can change the destination path before submitting.

### Delete confirmation modal

Reuses existing `deleteTarget` state pattern in `BrowsePage`.

- **File**: "Delete file?" + monospace path + "This action cannot be undone." + Cancel / Delete buttons.
- **Folder**: "Delete folder?" + monospace path + scrollable list of affected file paths (built from local tree state by traversing children) + "N files affected" header + "This action cannot be undone." + Cancel / **Delete N files** button (count in label).

On confirm: call `nexspenceApi.deleteByPath(repo, path)`, invalidate `rawBrowseTree` query.

---

## RBAC

- **Read/browse**: `rbac.FilterPaths` already applied in `RawTree` handler — files the user can't access are excluded from the tree.
- **Delete**: `canDeleteRepo` check (isAdmin or has privilege with `delete` action) — same as existing Docker delete.
- **Upload**: Upload button hidden if `myPrivs` contains no privilege with `write` action. Path-level check by backend (403 → shown in modal).

---

## Out of scope

- Text file preview in detail panel.
- Multi-file upload (batch).
- Rename / move.
- Phase 15D Docker uploader / `last_downloaded` in UI.

---

## Files changed

| File | Change |
|------|--------|
| `internal/domain/types.go` | Add `RawBrowseAsset` struct |
| `internal/repository/interfaces.go` | Add `ListRawBrowseAssets` to `AssetRepo` |
| `internal/repository/postgres/asset_repo.go` | Implement `ListRawBrowseAssets` |
| `internal/testutil/mocks.go` | Add stub for `ListRawBrowseAssets` |
| `internal/api/handlers/browse_raw.go` | New file: `RawTree` handler + tree builder |
| `internal/api/router.go` | Register `GET /api/v1/browse/repositories/:name/raw-tree` |
| `frontend/src/api/client.ts` | Add `nexspenceApi.getRawBrowseTree` |
| `frontend/src/pages/BrowsePage.tsx` | `isRaw` branch, `RawTreeRows`, detail panel, upload modal, delete modal updates |
