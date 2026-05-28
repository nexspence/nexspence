# Phase 12: UI/UX Polish — Security Page + Repositories + Artifact Deletion

**Date:** 2026-04-21  
**Status:** approved  

---

## Scope

Five sub-tasks implementing UI polish across Security, Repositories, and Browse pages plus a backend bugfix and new delete capability.

**Priority order:** 12.1 → 12.2 → 12.3 → 12.4 → 12.5

---

## 12.1 — RolesTab: list layout + search + searchable privilege picker

### Current state
Roles are displayed as `auto-fill grid` of cards. The create/edit modal uses a plain checkbox list for privilege selection (scrollable div, no filtering).

### Changes

**List layout:** Replace `S.grid` with a vertical flex-column list. Each row:
- Shield icon + role name (bold, flex-1)
- `built-in` badge if `readOnly`
- First 3 privilege name badges + `+N` overflow
- Edit button (admin only)

**Search:** `<input placeholder="Search roles…">` above the list. State: `roleSearch: string`. Filter: `roles.filter(r => r.name.toLowerCase().includes(roleSearch.toLowerCase()))`.

**RoleModal — privilege picker:** Replace the checkbox list with `MultiSelect` from `frontend/src/components/MultiSelect.tsx`. Props:
```tsx
<MultiSelect
  options={allPrivs.map(p => ({ value: p.id, label: p.name }))}
  value={selectedPrivIds}
  onChange={setSelectedPrivIds}
  placeholder="Search and select privileges…"
/>
```
MultiSelect already supports text filtering, tags with ×, and Select all / Clear all.

---

## 12.2 — PrivilegesTab: "Used in Roles" column + "Select all" actions

### Data flow
`role.privileges` is always `[]` in the List API response (populated lazily). To avoid N+1 requests, add a single new backend endpoint:

**`GET /api/v1/security/privilege-role-map`** — one SQL query:
```sql
SELECT rp.privilege_id, r.name
FROM role_privileges rp JOIN roles r ON r.id = rp.role_id
```
Returns `Record<string, string[]>` — privilege ID → array of role names.

Handler: `PrivilegeHandler.RoleMap` in `privilege.go`. Route in `authed` group.

**Frontend:** `PrivilegesTab` fetches `['privilege-role-map']` query once. No prop changes needed.

**New table column "Used in Roles":** inserted after the Actions column. Shows role name badges (cyan, small). Empty → `—`.

**"Select all" toggle in modal:** A small text-button next to the "Actions" label.
- All 4 selected → click deselects all
- Otherwise → selects all 4

---

## 12.3 — ContentSelectorsTab: "Privilege" column

### Data flow
`ContentSelectorsTab` receives new prop `privs: Privilege[]` (passed from SecurityPage).

**Queries lifted to SecurityPage:** The `['privileges']` and `['content-selectors']` queries currently duplicated in both `PrivilegesTab` and `ContentSelectorsTab` are moved to `SecurityPage` level and passed as props. This eliminates duplicate network requests.

**Computed mapping:**
```ts
const selectorToPriv = useMemo(() =>
  new Map(privs.map(p => [p.contentSelectorId ?? '', p.name])),
  [privs]
)
```

**New table column "Privilege":** inserted after Expression. Shows `selectorToPriv.get(s.id) ?? '—'`.

---

## 12.4 — RepositoriesPage: click → Browse + fix NaN GB

### NaN GB bug fix
`RepoCard` calls `nexspenceApi.getRepositoryQuota(repo.name)` → `GET /api/v1/repositories/:name/quota`. The handler `ComponentHandler.GetQuota` exists at `internal/api/handlers/components.go:292` but is **not registered** in the router.

**Fix:** Add one line to `internal/api/router.go` in the `authed` group:
```go
authed.GET("/api/v1/repositories/:name/quota", componentH.GetQuota)
```

### Click to Browse
`RepoCard` receives new prop `onClick: () => void`. In `RepositoriesPage`, `useNavigate()` from react-router-dom:
```tsx
onClick={() => navigate(`/browse?repo=${repo.name}`)}
```

Edit (gear) and Delete buttons call `e.stopPropagation()` to prevent card click from firing.

Add cursor pointer style to `RepoCard` wrapper div.

---

## 12.5 — Browse: artifact deletion

### Backend

#### `DELETE /api/v1/browse/repositories/:name/path`
Query param: `path=<url-encoded-path>` (prefix or exact).

Handler (new file `internal/api/handlers/browse.go` or added to `browse_docker.go`):
1. Load repository by name, 403 if not found
2. `AssetRepo.ListByRepoAndPath(ctx, repoName, path)` — returns all assets where `asset.path` starts with the given prefix
3. For each asset: `BlobStore.Delete(blobKey)` + `AssetRepo.Delete(assetID)`
4. `ComponentRepo.DeleteOrphans(repoName)` — delete components with no remaining assets
5. Return `204 No Content`

RBAC: protected by existing `RBACMiddleware` with action `delete`.

#### `GET /api/v1/me/privileges`
Returns the current user's effective privileges (via their roles).

Handler in `internal/api/handlers/auth.go`:
1. Get `userID` from context
2. Load user's roles → for each role, load its privileges via `RBACRepo` or `PrivilegeRepo`
3. Return `[]Privilege` with `id, name, type, attrs, contentSelectorId`

Route in `authed` group:
```go
authed.GET("/api/v1/me/privileges", authH.MyPrivileges)
```

### Frontend

**Permission check in BrowsePage:**
```ts
const { data: myPrivs = [] } = useQuery({
  queryKey: ['me-privileges'],
  queryFn: () => apiClient.get('/api/v1/me/privileges').then(r => r.data),
})

const canDeleteRepo = useCallback((repoName: string): boolean => {
  if (isAdmin()) return true
  return myPrivs.some(p =>
    (p.attrs?.actions as string[] | undefined)?.includes('delete') &&
    p.contentSelectorId  // selector expression evaluated backend-side; show button optimistically
  )
}, [myPrivs])
```

For simplicity, if a user has any privilege with `delete` action, show the Delete button (backend enforces actual RBAC; a 403 from the backend is caught and shown as an error toast/message).

**Delete button placement:**
- Non-docker browse: on leaf file rows (table row trailing button with `Trash2` icon, 13px)
- Docker browse: on tag leaf rows in the tree
- Shown only when `canDeleteRepo(selectedRepo)`

**Confirmation modal** (inline state, not a separate component):
```
Delete "{path}"?
This action cannot be undone.
[Cancel]  [Delete]
```

**After deletion:**
- Non-docker: `refetch()` the components list
- Docker: `refetchDockerTree()`

**Delete API call:**
```ts
await apiClient.delete(
  `/api/v1/browse/repositories/${encodeURIComponent(repo)}/path`,
  { params: { path } }
)
```

---

## New repository interfaces needed

```go
// AssetRepo
ListByRepoAndPath(ctx context.Context, repoName, pathPrefix string) ([]*domain.Asset, error)

// ComponentRepo  
DeleteOrphans(ctx context.Context, repoName string) error
```

---

## Testing

- Manual QA: admin sees Delete on all repos; non-admin without delete privilege sees no Delete button; non-admin with delete privilege sees Delete button but backend still enforces CEL expression
- Quota fix verified: RepoCard shows actual bytes used instead of NaN
- Roles search filters correctly; MultiSelect in modal shows/filters/removes privileges
- "Used in Roles" shows correct role names; updates immediately when a role is edited

---

## Files changed

| File | Change |
|------|--------|
| `frontend/src/pages/SecurityPage.tsx` | 12.1–12.3: list layout, search, MultiSelect, role→priv map, selector→priv map, lifted queries |
| `frontend/src/pages/RepositoriesPage.tsx` | 12.4: onClick prop, navigate, stopPropagation |
| `frontend/src/pages/BrowsePage.tsx` | 12.5: me-privileges query, canDeleteRepo, Delete button, confirm modal, delete mutation |
| `internal/api/router.go` | 12.4: wire `/api/v1/repositories/:name/quota`; 12.5: wire `DELETE browse/path` + `GET /api/v1/me/privileges` |
| `internal/api/handlers/auth.go` | 12.5: `MyPrivileges` handler |
| `internal/api/handlers/privilege.go` | 12.2: `RoleMap` handler (`GET /api/v1/security/privilege-role-map`) |
| `internal/api/handlers/browse.go` (new) | 12.5: `DeleteByPath` handler |
| `internal/repository/interfaces.go` | 12.5: `ListByRepoAndPath`, `DeleteOrphans` |
| `internal/repository/postgres/asset_repo.go` | 12.5: implement `ListByRepoAndPath` |
| `internal/repository/postgres/component_repo.go` | 12.5: implement `DeleteOrphans` |
| `internal/testutil/mocks.go` | 12.5: stub new methods |
