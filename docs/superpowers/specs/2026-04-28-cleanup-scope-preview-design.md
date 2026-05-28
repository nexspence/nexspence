# Cleanup Policy — Scope Picker + Dry Run Preview

**Date:** 2026-04-28
**Status:** Approved

## Summary

Replace the free-text `pathPrefix` / `nameGlob` criteria fields with a visual scope selector: repository picker (dropdown filtered by format) + path browser (tree UI). Add a `scope` JSONB column to `cleanup_policies` to persist the selection. Add a `POST .../preview` endpoint that returns what would be deleted without making any changes. Surface the result in a dry-run preview modal in the UI.

---

## 1. Data Model

### Migration: `013_cleanup_scope.sql`

```sql
ALTER TABLE cleanup_policies
  ADD COLUMN scope JSONB NOT NULL DEFAULT '{}';
```

### `scope` structure

```json
{ "repositoryName": "maven2-releases", "pathPrefix": "/com/acme" }
```

Both fields are optional. Empty object = no explicit scope (falls back to the existing attachment model via `repositories.cleanup_policy_ids`).

### Domain change (`internal/domain/types.go`)

```go
type CleanupScope struct {
    RepositoryName string `json:"repositoryName,omitempty"`
    PathPrefix     string `json:"pathPrefix,omitempty"`
}

type CleanupPolicy struct {
    // ... existing fields unchanged ...
    Scope CleanupScope `json:"scope"`
}
```

---

## 2. Backend

### Service logic (`cleanup_service.go` — `runPolicy`)

Priority rule:

```
if scope.RepositoryName != "" {
    repoNames = []string{scope.RepositoryName}
    if scope.PathPrefix != "" {
        pathPrefix = scope.PathPrefix   // overrides criteria.pathPrefix if set
    }
} else {
    repoNames = ListNamesByCleanupPolicyID(ctx, p.ID)  // existing attachment model
}
```

The existing `pathPrefix` / `nameGlob` criteria keys remain supported for backward compatibility with edit-modal and existing policies.

### Preview endpoint (`internal/api/handlers/cleanup.go`)

```
POST /api/v1/cleanup-policies/:id/preview
```

- Runs the same query logic as `runPolicy` but returns results instead of deleting.
- Does **not** update `lastRunAt` / `lastRunFreed` / `lastRunCount`.
- Caps response at **200 assets**.
- Response:

```json
{
  "assets": [
    {
      "path": "/com/acme/backend/1.0.0/backend-1.0.0.jar",
      "repository": "maven2-releases",
      "sizeBytes": 4404019,
      "lastDownloaded": null,
      "createdAt": "2024-01-15T10:00:00Z",
      "reason": "not dl 30d"
    }
  ],
  "totalCount": 47,
  "totalBytes": 1288490188
}
```

- `reason` is a human-readable string derived from which criteria matched first: `"not dl Nd"`, `"age Nd"`.

### Repository: `cleanup_repo.go`

- `Get` / `List` / `Create` / `Update` read/write the new `scope` column.

---

## 3. Frontend

### Wizard Step 2 changes (`CleanupPage.tsx`)

When `form.format !== '*'`, show a **Scope** section below the time criteria fields:

**Repository picker**
- `<select>` populated from `GET /service/rest/v1/repositories`, filtered to repos matching `form.format`.
- On selection: shows a removable chip (format colour dot + repo name + ×).

**Path field**
- Text input showing the selected path.
- **Browse…** button opens the Path Browser modal (see below).
- Shows hint: "Leave empty to match all paths in the repository."

**Info banner** (shown when both repo + path are set):
> "This policy will target **{repo}** at path `/com/acme` and all sub-paths."

When `form.format === '*'`: the Scope section is hidden entirely (global cleanup by time criteria).

### Path Browser modal

- Triggered by **Browse…** button in Step 2.
- Calls the existing `GET /api/v1/browse/repositories/:name/path-tree` to load the directory tree (already in the router).
- Filter input at the top for quick search.
- Clicking a folder row selects it (highlighted); clicking again on an already-selected folder deselects it.
- **Select {path}** button in the footer writes the path back to the form and closes the modal.
- Cancel discards the selection.

### Form state additions

```ts
interface PolicyForm {
  // ... existing fields ...
  scopeRepository: string   // repo name or ''
  scopePath: string         // path prefix or ''
}
```

`payload()` maps these to `scope: { repositoryName, pathPrefix }` (omitting empty strings).

### Policy card changes

- Add `🔍 Preview` button next to `▶ Run`.
- New chip style `holo-pill--info` (cyan) for the scope display: `📁 {repo} / {path}`.

### Preview modal

Triggered by `🔍 Preview` on a policy card. Calls `POST .../preview`, shows:

- **Header**: "Dry Run Preview · {policyName} · no actual deletes"
- **Stats row**: assets to delete (red), bytes to free (green), capped indicator
- **Table**: Path (mono, blue), Repository, Size (amber), Last downloaded (danger pill if never), Created, Reason pill
- **Footer**: "Showing N of M · No actual deletes occurred" + **Close** + **▶ Run for real** (calls existing `POST .../run`)

Loading state: spinner in place of the table.
Empty state: "Nothing matches the policy criteria."
Error state: red banner.

---

## 4. Backward Compatibility

- Existing policies with `scope = {}` continue to work via the attachment model.
- The `criteria.pathPrefix` and `criteria.nameGlob` keys remain functional in the edit modal for existing policies; they are not removed from the schema or service.
- The `dryRun` flag on the policy is kept but hidden from the create wizard (replaced by the explicit Preview flow). It remains visible in the edit modal for backward compat.

---

## 5. Out of Scope

- Multi-repo selection per policy (single repo + path is the target for this phase).
- Removing the `cleanup_policy_ids` attachment model from repositories.
- Regex-based path filtering (the visual browser covers the primary use case).
