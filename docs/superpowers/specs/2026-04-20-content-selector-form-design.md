# Content Selector Form Redesign

**Date:** 2026-04-20  
**Status:** Approved

## Problem

The current Content Selector UI exposes a raw CEL expression textarea. Users encounter:
- Syntax errors using `and` instead of `&&` or `=~` instead of `.matches()`
- No discoverability — users must know CEL syntax
- No way to browse what paths actually exist in a repository

## Goal

Replace the free-text CEL field with two searchable dropdowns: repository + directory path. The CEL expression is generated automatically and never shown to the user during creation/editing.

---

## Backend: New API Endpoint

### `GET /api/v1/browse/repositories/:name/path-tree`

Returns unique directory-level path segments extracted from `assets.path` for the given repository.

**Query params:**
- `q` — optional search string, filters returned paths (server-side, case-insensitive contains match)
- `depth` — optional max directory depth to return (default: unlimited)

**Response:**
```json
{
  "paths": [
    "/",
    "/da/",
    "/da/devops/",
    "/da/devops/apps/",
    "/da/devops/infra/"
  ]
}
```

**Implementation:**
1. `SELECT DISTINCT path FROM assets WHERE repository_name = $1`
2. For each asset path, extract all directory prefixes (e.g. `/da/devops/myapp-1.0.jar` → `/da/`, `/da/devops/`)
3. Deduplicate, sort, filter by `q` if present
4. Return as flat list — frontend renders as indented tree

**Handler:** add `PathTree(c *gin.Context)` to existing `BrowseHandler` in `internal/api/handlers/browse_docker.go` (or a separate `browse.go`).  
**Route:** wire in `router.go` under `authed.GET("/api/v1/browse/repositories/:name/path-tree", browseH.PathTree)`

---

## Frontend: Content Selector Modal

### Form fields

| Field | Type | Required |
|-------|------|----------|
| Name | text input | yes |
| Description | text input | no |
| Repository | searchable dropdown | no |
| Path | searchable dropdown (loads after repo selected, or independently) | no |

At least one of Repository or Path must be filled to enable Save.

### Repository dropdown
- Populated from existing `GET /service/rest/v1/repositories`
- Searchable: filter list client-side as user types (repos list is small)
- Option "Any repository" = no filter applied

### Path dropdown
- After repository is selected: `GET /api/v1/browse/repositories/:name/path-tree`
- If no repository selected: field is disabled with hint "Select a repository first"
- Renders paths as indented list based on depth (count `/` separators)
- Built-in text search: sends `?q=<term>` to backend or filters client-side if result set ≤ 200 items
- Option "Any path" = no path filter
- Selected value is the full prefix string, e.g. `/da/devops/`

### CEL expression generation (invisible to user)

| Repository | Path | Generated expression |
|------------|------|----------------------|
| `prod-docker` | `/da/devops/` | `repository == "prod-docker" && path.startsWith("/da/devops/")` |
| `prod-docker` | any | `repository == "prod-docker"` |
| any | `/da/devops/` | `path.startsWith("/da/devops/")` |

The `expression` field sent to the API is always valid CEL — generated programmatically, never typed by hand.

### Table display

Replace raw CEL column with human-readable summary:

| Name | Scope | Description |
|------|-------|-------------|
| devops-only | `da/devops/*` in `prod-docker` | ... |
| all-staging | all paths in `staging-npm` | ... |

Parse the stored expression on display:
- `repository == "X" && path.startsWith("Y")` → `Y*` in `X`
- `repository == "X"` → all paths in `X`
- `path.startsWith("Y")` → `Y*` in all repos
- anything else → show raw expression (legacy/manual entries)

### Edit mode
When editing an existing selector, parse the expression back into form fields if it matches a known pattern; otherwise fall back to raw CEL textarea (backward compatibility for hand-written selectors).

---

## Data flow

```
User opens modal
  → repos dropdown populated from /service/rest/v1/repositories
  → user selects repo
  → path-tree loaded from /api/v1/browse/repositories/:name/path-tree
  → user selects path (or searches)
  → Save clicked
  → frontend generates CEL expression
  → POST /service/rest/v1/security/content-selectors { name, description, expression }
  → backend validates CEL → 400 if invalid (shouldn't happen with generated expressions)
  → selector stored, modal closes
```

---

## Files changed

**Backend:**
- `internal/api/handlers/browse_docker.go` (or new `browse.go`) — add `PathTree` handler
- `internal/api/router.go` — wire new route

**Frontend:**
- `frontend/src/pages/SecurityPage.tsx` — replace `ContentSelectorsTab` modal form fields and table column

**No schema changes** — `content_selectors.expression` stays as a plain text CEL string.

---

## Out of scope

- Format-based filtering (not needed per user requirements)
- Visual tree expand/collapse in dropdown (flat indented list is sufficient)
- Glob syntax (`*`) in path input — converted to `startsWith` automatically
