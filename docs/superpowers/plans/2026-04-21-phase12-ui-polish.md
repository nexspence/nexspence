# Phase 12: UI/UX Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Polish Security page (Roles/Privileges/ContentSelectors tabs), fix NaN GB in Repositories, add click-to-browse on repo cards, and implement artifact deletion from Browse page.

**Architecture:** Five sequential sub-tasks. Tasks 1–4 are backend changes; Tasks 5–9 are frontend changes. Each task is self-contained. Backend tasks must be done before corresponding frontend tasks (quota fix before frontend assumes it works; privilege-role-map before 12.2 frontend; me/privileges + browse-delete before 12.5 frontend).

**Tech Stack:** Go (Gin, pgx), React + TypeScript, Zustand, React Query, `internal/testutil` in-memory mocks for backend unit tests, `npx tsc --noEmit` for frontend type checks.

---

## File Map

| File | Task | Change |
|------|------|--------|
| `internal/api/router.go` | 1, 2, 3, 4 | Wire new routes |
| `internal/repository/interfaces.go` | 2, 4 | Add `PrivilegeRoleMap`, `ListByRepoAndPath`, `DeleteOrphans` |
| `internal/repository/postgres/privilege_repo.go` | 2 | Implement `PrivilegeRoleMap` |
| `internal/repository/postgres/asset_repo.go` | 4 | Implement `ListByRepoAndPath` |
| `internal/repository/postgres/component_repo.go` | 4 | Implement `DeleteOrphans` |
| `internal/api/handlers/privileges.go` | 2, 3 | Add `RoleMap` + `MyPrivileges` |
| `internal/api/handlers/browse_docker.go` | 4 | Add `DeleteByPath` |
| `internal/testutil/mocks.go` | 2, 4 | Stub new interface methods |
| `frontend/src/pages/SecurityPage.tsx` | 5, 6, 7 | RolesTab list+search, PrivilegesTab "Used in Roles", ContentSelectorsTab "Privilege" |
| `frontend/src/pages/RepositoriesPage.tsx` | 8 | Click-to-browse |
| `frontend/src/pages/BrowsePage.tsx` | 9 | Delete button + confirm modal |
| `frontend/src/api/client.ts` | 6, 9 | New API calls |

---

## Task 1: Wire quota route (fixes NaN GB in Repositories)

**Files:**
- Modify: `internal/api/router.go`

The handler `ComponentHandler.GetQuota` already exists at `internal/api/handlers/components.go:292` but is not registered. This is the entire fix.

- [ ] **Step 1: Add route in router.go**

In `internal/api/router.go`, find the `authed` block after the line:
```go
authed.GET("/api/v1/repositories", repoH.List)
```
Add immediately after it:
```go
authed.GET("/api/v1/repositories/:name/quota", componentH.GetQuota)
```

- [ ] **Step 2: Verify build**

```bash
go build ./...
```
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/api/router.go
git commit -m "fix: wire /api/v1/repositories/:name/quota route (NaN GB in UI)"
```

---

## Task 2: Backend — privilege-role-map endpoint (for "Used in Roles" column)

**Files:**
- Modify: `internal/repository/interfaces.go`
- Modify: `internal/repository/postgres/privilege_repo.go`
- Modify: `internal/api/handlers/privileges.go`
- Modify: `internal/api/router.go`
- Modify: `internal/testutil/mocks.go`

The frontend needs to know which roles use each privilege. One SQL join is more efficient than N calls.

- [ ] **Step 1: Add method to PrivilegeRepo interface**

In `internal/repository/interfaces.go`, add to the `PrivilegeRepo` interface after `ListByRole`:
```go
// PrivilegeRoleMap returns a map of privilege ID → role names that include it.
// Used by the UI to display "Used in Roles" for each privilege.
PrivilegeRoleMap(ctx context.Context) (map[string][]string, error)
```

- [ ] **Step 2: Implement in postgres**

In `internal/repository/postgres/privilege_repo.go`, add at the end of the file:
```go
func (r *privilegeRepo) PrivilegeRoleMap(ctx context.Context) (map[string][]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT rp.privilege_id, ro.name
		 FROM role_privileges rp
		 JOIN roles ro ON ro.id = rp.role_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string][]string)
	for rows.Next() {
		var privID, roleName string
		if err := rows.Scan(&privID, &roleName); err != nil {
			return nil, err
		}
		m[privID] = append(m[privID], roleName)
	}
	return m, rows.Err()
}
```

- [ ] **Step 3: Verify build**

```bash
go build ./...
```
Expected: no errors. If the mock doesn't implement the new method yet, the build will fail at the compile-time assertion in `testutil/mocks.go` — that's fine, fix in Step 4.

- [ ] **Step 4: Add stub to mocks**

In `internal/testutil/mocks.go`, find the `PrivilegeRepo` struct and add the method after the existing ones:
```go
func (p *PrivilegeRepo) PrivilegeRoleMap(_ context.Context) (map[string][]string, error) {
	return map[string][]string{}, nil
}
```

- [ ] **Step 5: Add RoleMap handler**

In `internal/api/handlers/privileges.go`, add at the end:
```go
// RoleMap handles GET /api/v1/security/privilege-role-map
// Returns map of privilege ID → role names that include it.
func (h *PrivilegeHandler) RoleMap(c *gin.Context) {
	m, err := h.repo.PrivilegeRoleMap(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, m)
}
```

- [ ] **Step 6: Wire route**

In `internal/api/router.go`, in the `authed` block near the other privilege routes (around line 220), add:
```go
authed.GET("/api/v1/security/privilege-role-map", privH.RoleMap)
```

- [ ] **Step 7: Build and test**

```bash
go build ./...
go test ./...
```
Expected: all tests pass.

- [ ] **Step 8: Commit**

```bash
git add internal/repository/interfaces.go \
        internal/repository/postgres/privilege_repo.go \
        internal/api/handlers/privileges.go \
        internal/api/router.go \
        internal/testutil/mocks.go
git commit -m "feat: add privilege-role-map endpoint for Security page"
```

---

## Task 3: Backend — GET /api/v1/me/privileges

**Files:**
- Modify: `internal/api/handlers/privileges.go`
- Modify: `internal/api/router.go`

The `PrivilegeHandler` already has `repo` (PrivilegeRepo) and `roleRepo` (RoleRepo). `RoleRepo.GetUserRoles` returns the user's roles; `PrivilegeRepo.ListByRole` returns privileges for a role. We add `MyPrivileges` to `PrivilegeHandler`.

- [ ] **Step 1: Add MyPrivileges handler**

In `internal/api/handlers/privileges.go`, add at the end of the file:
```go
// MyPrivileges handles GET /api/v1/me/privileges
// Returns the current user's effective privileges (via their roles).
func (h *PrivilegeHandler) MyPrivileges(c *gin.Context) {
	var userID string
	if uid, ok := c.Get("userID"); ok {
		userID, _ = uid.(string)
	}
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	roles, err := h.roleRepo.GetUserRoles(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	seen := make(map[string]struct{})
	var result []domain.Privilege
	for _, role := range roles {
		privs, err := h.repo.ListByRole(c.Request.Context(), role.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, p := range privs {
			if _, dup := seen[p.ID]; !dup {
				seen[p.ID] = struct{}{}
				result = append(result, p)
			}
		}
	}
	if result == nil {
		result = []domain.Privilege{}
	}
	c.JSON(http.StatusOK, result)
}
```

- [ ] **Step 2: Wire route**

In `internal/api/router.go`, in the `authed` block near `/api/v1/me`, add:
```go
authed.GET("/api/v1/me/privileges", privH.MyPrivileges)
```

- [ ] **Step 3: Build and test**

```bash
go build ./...
go test ./...
```
Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add internal/api/handlers/privileges.go internal/api/router.go
git commit -m "feat: add GET /api/v1/me/privileges for browse delete RBAC check"
```

---

## Task 4: Backend — DELETE browse/path (artifact deletion)

**Files:**
- Modify: `internal/repository/interfaces.go`
- Modify: `internal/repository/postgres/asset_repo.go`
- Modify: `internal/repository/postgres/component_repo.go`
- Modify: `internal/api/handlers/browse_docker.go`
- Modify: `internal/api/router.go`
- Modify: `internal/testutil/mocks.go`

Strategy: delete asset DB rows (blobs are cleaned by existing GC). Then delete orphan components (components with no remaining assets in the repo).

- [ ] **Step 1: Add ListByRepoAndPath to AssetRepo interface**

In `internal/repository/interfaces.go`, add to `AssetRepo` after `ListByComponentID`:
```go
// ListByRepoAndPath returns all assets in repoName whose path starts with pathPrefix.
// Use pathPrefix="" to list all assets in the repo.
ListByRepoAndPath(ctx context.Context, repoName, pathPrefix string) ([]domain.Asset, error)
```

- [ ] **Step 2: Add DeleteOrphans to ComponentRepo interface**

In `internal/repository/interfaces.go`, add to `ComponentRepo` after `UpdateExtra`:
```go
// DeleteOrphans removes components in repoName that have no remaining assets.
DeleteOrphans(ctx context.Context, repoName string) error
```

- [ ] **Step 3: Implement ListByRepoAndPath in postgres**

In `internal/repository/postgres/asset_repo.go`, add at the end of the file:
```go
func (r *assetRepo) ListByRepoAndPath(ctx context.Context, repoName, pathPrefix string) ([]domain.Asset, error) {
	var q string
	var args []any
	if pathPrefix == "" {
		q = fmt.Sprintf(`SELECT %s %s WHERE rep.name = $1 ORDER BY a.path`,
			assetSelectCols, assetFromJoin)
		args = []any{repoName}
	} else {
		q = fmt.Sprintf(`SELECT %s %s WHERE rep.name = $1 AND a.path LIKE $2 ORDER BY a.path`,
			assetSelectCols, assetFromJoin)
		args = []any{repoName, pathPrefix + "%"}
	}
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Asset
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Implement DeleteOrphans in postgres**

In `internal/repository/postgres/component_repo.go`, add at the end of the file:
```go
func (r *componentRepo) DeleteOrphans(ctx context.Context, repoName string) error {
	_, err := r.db.Exec(ctx, `
		DELETE FROM components c
		WHERE c.id IN (
			SELECT c2.id
			FROM components c2
			JOIN repositories rep ON rep.id = c2.repository_id
			WHERE rep.name = $1
			  AND NOT EXISTS (
				  SELECT 1 FROM assets a WHERE a.component_id = c2.id
			  )
		)`, repoName)
	return err
}
```

- [ ] **Step 5: Add DeleteByPath handler to BrowseHandler**

In `internal/api/handlers/browse_docker.go`, add at the end of the file:
```go
// DeleteByPath handles DELETE /api/v1/browse/repositories/:name/path
// Query param: path=<prefix> (required). Deletes all assets whose path starts with
// the prefix, then removes orphan components. Blobs are cleaned by the GC scheduler.
func (h *BrowseHandler) DeleteByPath(c *gin.Context) {
	repoName := c.Param("name")
	pathPrefix := c.Query("path")
	if pathPrefix == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path query param required"})
		return
	}

	ctx := c.Request.Context()
	assets, err := h.assets.ListByRepoAndPath(ctx, repoName, pathPrefix)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, a := range assets {
		if err := h.assets.Delete(ctx, a.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if err := h.components.DeleteOrphans(ctx, repoName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
```

- [ ] **Step 6: Wire route**

In `internal/api/router.go`, in the `authed` block near the other browse routes (around line 174), add:
```go
authed.DELETE("/api/v1/browse/repositories/:name/path", browseH.DeleteByPath)
```

- [ ] **Step 7: Add stubs to mocks**

In `internal/testutil/mocks.go`, find the `AssetRepo` mock and add after `ListRawAssetPaths`:
```go
func (a *AssetRepo) ListByRepoAndPath(_ context.Context, _, _ string) ([]domain.Asset, error) {
	return nil, nil
}
```

Find the `ComponentRepo` mock and add after `UpdateExtra`:
```go
func (c *ComponentRepo) DeleteOrphans(_ context.Context, _ string) error {
	return nil
}
```

- [ ] **Step 8: Build and test**

```bash
go build ./...
go test ./...
```
Expected: all pass.

- [ ] **Step 9: Commit**

```bash
git add internal/repository/interfaces.go \
        internal/repository/postgres/asset_repo.go \
        internal/repository/postgres/component_repo.go \
        internal/api/handlers/browse_docker.go \
        internal/api/router.go \
        internal/testutil/mocks.go
git commit -m "feat: add DELETE browse/path endpoint for artifact deletion"
```

---

## Task 5: Frontend 12.1 — RolesTab: list layout + search + MultiSelect privilege picker

**Files:**
- Create: `frontend/src/components/MultiSelect.tsx`
- Modify: `frontend/src/pages/SecurityPage.tsx`

Current: `RolesTab` renders cards in a `auto-fill grid`; `RoleModal` uses a scrollable checkbox list for privileges.
Target: vertical list rows + search field + new `MultiSelect` component.

- [ ] **Step 1: Create MultiSelect component**

Create `frontend/src/components/MultiSelect.tsx`:
```tsx
import { useEffect, useRef, useState } from 'react'
import { ChevronDown, X } from 'lucide-react'

export interface MultiSelectOption {
  value: string
  label: string
}

interface MultiSelectProps {
  options: MultiSelectOption[]
  value: string[]
  onChange: (values: string[]) => void
  placeholder?: string
}

export function MultiSelect({ options, value, onChange, placeholder = '— Select —' }: MultiSelectProps) {
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState('')
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  const filtered = options.filter(o => o.label.toLowerCase().includes(search.toLowerCase()))
  const allSelected = filtered.length > 0 && filtered.every(o => value.includes(o.value))

  function toggle(v: string) {
    onChange(value.includes(v) ? value.filter(x => x !== v) : [...value, v])
  }

  function toggleAll() {
    if (allSelected) {
      onChange(value.filter(v => !filtered.some(o => o.value === v)))
    } else {
      const toAdd = filtered.map(o => o.value).filter(v => !value.includes(v))
      onChange([...value, ...toAdd])
    }
  }

  const selectedLabels = value
    .map(v => options.find(o => o.value === v)?.label)
    .filter(Boolean) as string[]

  const dropStyle: React.CSSProperties = {
    position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 999, marginTop: 4,
    background: 'rgba(10,15,28,0.97)', border: '1px solid rgba(59,130,246,0.4)',
    borderRadius: 8, backdropFilter: 'blur(12px)', maxHeight: 240, display: 'flex', flexDirection: 'column',
  }

  return (
    <div ref={ref} style={{ position: 'relative', userSelect: 'none' }}>
      <div
        onClick={() => setOpen(o => !o)}
        style={{
          minHeight: 36, padding: '6px 10px', background: 'rgba(255,255,255,0.06)',
          border: `1px solid ${open ? 'rgba(59,130,246,0.5)' : 'rgba(255,255,255,0.12)'}`,
          borderRadius: 8, cursor: 'pointer', display: 'flex', alignItems: 'flex-start',
          flexWrap: 'wrap', gap: 4, color: '#e5e7eb', fontSize: 13,
        }}
      >
        {selectedLabels.length === 0 ? (
          <span style={{ color: 'rgba(229,231,235,0.35)', lineHeight: '22px' }}>{placeholder}</span>
        ) : (
          selectedLabels.map(label => (
            <span key={label} style={{
              display: 'flex', alignItems: 'center', gap: 4, padding: '1px 6px',
              background: 'rgba(59,130,246,0.15)', borderRadius: 4, fontSize: 12, color: '#93c5fd',
            }}>
              {label}
              <X size={10} style={{ cursor: 'pointer' }} onClick={e => {
                e.stopPropagation()
                const opt = options.find(o => o.label === label)
                if (opt) toggle(opt.value)
              }} />
            </span>
          ))
        )}
        <ChevronDown size={14} style={{ marginLeft: 'auto', color: 'rgba(229,231,235,0.4)', alignSelf: 'center', flexShrink: 0 }} />
      </div>
      {open && (
        <div style={dropStyle}>
          <div style={{ padding: '6px 8px', borderBottom: '1px solid rgba(255,255,255,0.06)' }}>
            <input
              autoFocus
              placeholder="Filter…"
              value={search}
              onChange={e => setSearch(e.target.value)}
              onClick={e => e.stopPropagation()}
              style={{ width: '100%', background: 'none', border: 'none', outline: 'none', color: '#e5e7eb', fontSize: 13, boxSizing: 'border-box' as const }}
            />
          </div>
          {filtered.length > 0 && (
            <div
              onClick={e => { e.stopPropagation(); toggleAll() }}
              style={{ padding: '6px 12px', fontSize: 12, color: '#3b82f6', cursor: 'pointer', borderBottom: '1px solid rgba(255,255,255,0.06)' }}
            >
              {allSelected ? 'Deselect all' : 'Select all'}
            </div>
          )}
          <div style={{ overflowY: 'auto' as const, flex: 1 }}>
            {filtered.length === 0 ? (
              <div style={{ padding: '8px 12px', fontSize: 13, color: 'rgba(229,231,235,0.35)' }}>No options</div>
            ) : filtered.map(o => (
              <div
                key={o.value}
                onClick={e => { e.stopPropagation(); toggle(o.value) }}
                style={{
                  padding: '7px 12px', fontSize: 13, cursor: 'pointer',
                  color: value.includes(o.value) ? '#93c5fd' : '#e5e7eb',
                  background: value.includes(o.value) ? 'rgba(59,130,246,0.1)' : 'transparent',
                }}
              >
                {o.value.includes(o.label) || true ? o.label : o.label}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Add search state and filter to RolesTab**

In `SecurityPage.tsx`, inside `function RolesTab(...)`, add state after the existing useState calls:
```tsx
const [roleSearch, setRoleSearch] = useState('')
const filtered = roles.filter(r =>
  r.name.toLowerCase().includes(roleSearch.toLowerCase())
)
```

- [ ] **Step 3: Replace grid with search + list**

Replace the entire JSX block:
```tsx
{!roles.length ? <div style={S.empty}>No roles found</div> : (
  <div style={S.grid}>
    {roles.map(r => (
      <div key={r.id} style={S.card}>
        ...
      </div>
    ))}
  </div>
)}
```

With:
```tsx
<input
  style={{ ...S.input, marginBottom: 12 }}
  placeholder="Search roles…"
  value={roleSearch}
  onChange={e => setRoleSearch(e.target.value)}
/>
{!filtered.length ? <div style={S.empty}>No roles found</div> : (
  <div style={S.card}>
    {filtered.map((r, idx) => (
      <div key={r.id} style={{
        display: 'flex', alignItems: 'center', gap: 10, padding: '10px 0',
        borderBottom: idx < filtered.length - 1 ? '1px solid rgba(255,255,255,0.06)' : 'none',
      }}>
        <Shield size={15} style={{ color: '#3b82f6', flexShrink: 0 }} />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' as const }}>
            <span style={{ fontSize: 14, fontWeight: 600, color: '#dbeafe' }}>{r.name}</span>
            {r.readOnly && <span style={S.badge('#6b7280')}>built-in</span>}
          </div>
          {r.description && (
            <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.45)', marginTop: 2 }}>{r.description}</div>
          )}
          <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' as const, marginTop: 4 }}>
            {(r.privileges ?? []).slice(0, 4).map(p => (
              <span key={p} style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: 'rgba(99,102,241,0.12)', color: '#a5b4fc', fontFamily: 'monospace' }}>{p}</span>
            ))}
            {(r.privileges ?? []).length > 4 && (
              <span style={{ fontSize: 10, color: 'rgba(229,231,235,0.35)' }}>+{(r.privileges ?? []).length - 4} more</span>
            )}
          </div>
        </div>
        {!r.readOnly && admin && (
          <button style={{ ...S.btn('ghost'), padding: '4px 10px', fontSize: 12 }} onClick={() => openEdit(r)}>Edit</button>
        )}
      </div>
    ))}
  </div>
)}
```

- [ ] **Step 4: Replace checkbox list in RoleModal with MultiSelect**

At the top of `SecurityPage.tsx`, the `MultiSelect` import needs to be added. Find the existing import line:
```tsx
import { Select } from '../components/Select'
```
Replace with:
```tsx
import { Select } from '../components/Select'
import { MultiSelect } from '../components/MultiSelect'
```

In `RoleModal`, replace the `<div style={{ fontSize: 13, fontWeight: 600 ... }}>Privileges</div>` block and the scrollable checkbox list:
```tsx
<div style={{ fontSize: 13, fontWeight: 600, color: 'rgba(229,231,235,0.7)', marginTop: 4 }}>Privileges</div>
{loadingPrivs ? <div style={S.empty}>Loading privileges…</div> : allPrivs.length === 0 ? (
  <div style={{ fontSize: 13, color: 'rgba(229,231,235,0.35)' }}>No privileges defined</div>
) : (
  <div style={{ maxHeight: 220, overflowY: 'auto' as const, display: 'flex', flexDirection: 'column', gap: 4 }}>
    {allPrivs.map(p => (
      <label key={p.id} style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', padding: '4px 0' }}>
        <input
          type="checkbox"
          checked={selectedPrivIds.includes(p.id)}
          onChange={e => onPrivToggle(p.id, e.target.checked)}
        />
        <span style={{ fontSize: 13, color: '#dbeafe', flex: 1 }}>{p.name}</span>
        <span style={S.badge(PRIV_TYPE_COLOR[p.type] ?? '#6b7280')}>{p.type}</span>
      </label>
    ))}
  </div>
)}
```

With:
```tsx
<div style={{ fontSize: 13, fontWeight: 600, color: 'rgba(229,231,235,0.7)', marginTop: 4 }}>Privileges</div>
{loadingPrivs ? (
  <div style={S.empty}>Loading privileges…</div>
) : (
  <MultiSelect
    options={allPrivs.map(p => ({ value: p.id, label: p.name }))}
    value={selectedPrivIds}
    onChange={onPrivToggle}
    placeholder="Search and select privileges…"
  />
)}
```

Note: `MultiSelect.onChange` receives the full new array `string[]`, not individual toggles. Update the `onPrivToggle` prop type in `RoleModal` accordingly:

Change the prop type:
```tsx
onPrivToggle: (id: string, checked: boolean) => void
```
to:
```tsx
onPrivToggle: (ids: string[]) => void
```

And update callers in `RolesTab`:
```tsx
// Edit modal:
onPrivToggle={setEditPrivIds}
// Create modal:
onPrivToggle={setCreatePrivIds}
```

- [ ] **Step 5: Type-check**

```bash
cd frontend && npx tsc --noEmit
```
Expected: 0 errors.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/MultiSelect.tsx frontend/src/pages/SecurityPage.tsx
git commit -m "feat(12.1): RolesTab — list layout, search, MultiSelect privilege picker"
```

---

## Task 6: Frontend 12.2 — PrivilegesTab: "Used in Roles" column + "Select all" actions

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx`
- Modify: `frontend/src/api/client.ts`

- [ ] **Step 1: Add privilege-role-map API call to client.ts**

In `frontend/src/api/client.ts`, in the `nexusApi` object, add after `listPrivileges`:
```ts
privilegeRoleMap: () =>
  apiClient.get<Record<string, string[]>>('/api/v1/security/privilege-role-map'),
```

- [ ] **Step 2: Fetch privilege-role-map in PrivilegesTab**

In `SecurityPage.tsx`, inside `function PrivilegesTab(...)`, add a new query after the existing `privs` query:
```tsx
const { data: privRoleMap = {} } = useQuery<Record<string, string[]>>({
  queryKey: ['privilege-role-map'],
  queryFn: () => nexusApi.privilegeRoleMap().then(r => r.data),
})
```

- [ ] **Step 3: Add "Used in Roles" column header**

In the `<thead>` of the privileges table, add after the "Description" `<th>`:
```tsx
<th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Used in Roles</th>
```
Move the empty admin `<th>` (edit/delete buttons) to stay last.

- [ ] **Step 4: Add "Used in Roles" cell in tbody**

In the `privs.map(p => ...)` row, add a `<td>` after the description cell:
```tsx
<td style={{ padding: '9px 8px' }}>
  {(privRoleMap[p.id] ?? []).length > 0 ? (
    <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' as const }}>
      {(privRoleMap[p.id] ?? []).map(rName => (
        <span key={rName} style={S.badge('#06b6d4')}>{rName}</span>
      ))}
    </div>
  ) : (
    <span style={{ color: 'rgba(229,231,235,0.3)', fontSize: 12 }}>—</span>
  )}
</td>
```

- [ ] **Step 5: Add "Select all" toggle in create/edit modal**

In the modal JSX (the `showModal` block), find:
```tsx
<div style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', marginBottom: 6 }}>Actions</div>
```
Replace with:
```tsx
<div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 6 }}>
  <span style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)' }}>Actions</span>
  <button
    type="button"
    style={{ fontSize: 11, color: '#3b82f6', background: 'none', border: 'none', cursor: 'pointer', padding: 0 }}
    onClick={() => setForm(f => ({
      ...f,
      actions: f.actions.length === PRIV_ACTIONS.length ? [] : [...PRIV_ACTIONS],
    }))}
  >
    {form.actions.length === PRIV_ACTIONS.length ? 'Deselect all' : 'Select all'}
  </button>
</div>
```

- [ ] **Step 6: Invalidate privilege-role-map after role saves**

In `RolesTab`, the `saveEdit` and `create` mutations already call `qc.invalidateQueries({ queryKey: ['roles'] })`. Add `privilege-role-map` invalidation to both:
```tsx
// saveEdit onSuccess:
onSuccess: () => {
  qc.invalidateQueries({ queryKey: ['roles'] })
  qc.invalidateQueries({ queryKey: ['privilege-role-map'] })
  onRefresh()
  setEditRole(null)
},
// create onSuccess:
onSuccess: () => {
  qc.invalidateQueries({ queryKey: ['roles'] })
  qc.invalidateQueries({ queryKey: ['privilege-role-map'] })
  onRefresh()
  setShowCreate(false)
},
```

- [ ] **Step 7: Type-check**

```bash
cd frontend && npx tsc --noEmit
```
Expected: 0 errors.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx frontend/src/api/client.ts
git commit -m "feat(12.2): PrivilegesTab — Used in Roles column, Select all actions toggle"
```

---

## Task 7: Frontend 12.3 — ContentSelectorsTab: "Privilege" column

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx`

Currently `ContentSelectorsTab` and `PrivilegesTab` each independently fetch `['content-selectors']` and `['privileges']`. We lift the `['privileges']` query to `SecurityPage` so `ContentSelectorsTab` can build the `selectorToPriv` map without an extra fetch.

- [ ] **Step 1: Lift privileges query to SecurityPage**

In `SecurityPage` (the main component at the bottom of the file), find the `privileges` query if it's rendered there — it's actually inside `PrivilegesTab`. We'll add a shared query at the top level.

In the `SecurityPage` function body (where `roles`, `isLoading` are fetched), add:
```tsx
const { data: allPrivileges = [] } = useQuery<Privilege[]>({
  queryKey: ['privileges'],
  queryFn: () => nexusApi.listPrivileges().then(r => r.data),
})
```

- [ ] **Step 2: Pass privs to ContentSelectorsTab**

Update the `ContentSelectorsTab` call in the render:
```tsx
{tab === 'selectors' && <ContentSelectorsTab admin={admin} privs={allPrivileges} />}
```

- [ ] **Step 3: Add privs prop to ContentSelectorsTab**

Change the function signature:
```tsx
function ContentSelectorsTab({ admin }: { admin: boolean }) {
```
to:
```tsx
function ContentSelectorsTab({ admin, privs }: { admin: boolean; privs: Privilege[] }) {
```

- [ ] **Step 4: Build selectorToPriv map in ContentSelectorsTab**

Inside `ContentSelectorsTab`, add after the existing state declarations:
```tsx
const selectorToPriv = useMemo(
  () => new Map(privs.map(p => [p.contentSelectorId ?? '', p.name])),
  [privs]
)
```

Add the `useMemo` import if not present — it's already imported via React in the file's import at the top.

- [ ] **Step 5: Add "Privilege" column to ContentSelectorsTab table**

`ContentSelectorsTab` renders a `<table>` with columns: Name | Scope | Description | (admin actions).

In the `<thead>`, add after the Description `<th>` and before the admin `<th>`:
```tsx
<th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Privilege</th>
```

In the `selectors.map(s => ...)` tbody rows, add a `<td>` after the description cell and before the admin cell:
```tsx
<td style={{ padding: '9px 8px', color: 'rgba(229,231,235,0.55)' }}>
  {selectorToPriv.get(s.id) ?? '—'}
</td>
```

- [ ] **Step 6: Type-check**

```bash
cd frontend && npx tsc --noEmit
```
Expected: 0 errors.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx
git commit -m "feat(12.3): ContentSelectorsTab — show linked privilege per selector"
```

---

## Task 8: Frontend 12.4 — Repository cards: click to Browse

**Files:**
- Modify: `frontend/src/pages/RepositoriesPage.tsx`

- [ ] **Step 1: Add useNavigate import**

In `RepositoriesPage.tsx`, check existing imports. Add `useNavigate` to the react-router-dom import:
```tsx
import { useNavigate } from 'react-router-dom'
```

- [ ] **Step 2: Add onClick prop to RepoCard**

Change the `RepoCard` prop type:
```tsx
function RepoCard({
  repo,
  isAdmin,
  onEdit,
  onDelete,
}: {
  repo: Repository
  isAdmin: boolean
  onEdit: () => void
  onDelete: () => void
})
```
to:
```tsx
function RepoCard({
  repo,
  isAdmin,
  onEdit,
  onDelete,
  onClick,
}: {
  repo: Repository
  isAdmin: boolean
  onEdit: () => void
  onDelete: () => void
  onClick: () => void
})
```

- [ ] **Step 3: Wire onClick to the card wrapper div**

In `RepoCard`'s return, find the outermost `<div className={styles.card}>` and add:
```tsx
<div className={styles.card} onClick={onClick} style={{ cursor: 'pointer' }}>
```

- [ ] **Step 4: Stop propagation on Edit and Delete buttons**

Inside `RepoCard`, find the Edit (gear) button and the Delete button. Add `e.stopPropagation()` to their onClick handlers:

For the settings/gear button (search for `onEdit` in the card JSX):
```tsx
onClick={e => { e.stopPropagation(); /* open settings */ setEditRepo(repo) }}
```

For the delete button:
```tsx
onClick={e => { e.stopPropagation(); onDelete() }}
```

Note: `onEdit` and `onDelete` are called from the parent `RepositoriesPage`, not inside `RepoCard` directly. Find where `RepoCard` is rendered (inside `RepositoriesPage`) and update those call sites to pass `onClick`:

```tsx
<RepoCard
  key={repo.id}
  repo={repo}
  isAdmin={isAdmin}
  onEdit={() => setEditRepo(repo)}
  onDelete={() => handleDelete(repo.name)}
  onClick={() => navigate(`/browse?repo=${encodeURIComponent(repo.name)}`)}
/>
```

- [ ] **Step 5: Add navigate to RepositoriesPage**

Inside `RepositoriesPage` component, add:
```tsx
const navigate = useNavigate()
```

- [ ] **Step 6: Type-check**

```bash
cd frontend && npx tsc --noEmit
```
Expected: 0 errors.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/pages/RepositoriesPage.tsx
git commit -m "feat(12.4): repo cards click to browse, fix NaN GB (quota route wired in Task 1)"
```

---

## Task 9: Frontend 12.5 — Browse: artifact deletion

**Files:**
- Modify: `frontend/src/pages/BrowsePage.tsx`
- Modify: `frontend/src/api/client.ts`

- [ ] **Step 1: Add delete API calls to client.ts**

In `frontend/src/api/client.ts`, in the `nexspenceApi` object (where `getDockerBrowseTree` lives), add:
```ts
deleteByPath: (repoName: string, path: string) =>
  apiClient.delete(
    `/api/v1/browse/repositories/${encodeURIComponent(repoName)}/path`,
    { params: { path } }
  ),
myPrivileges: () =>
  apiClient.get<Array<{ id: string; name: string; type: string; attrs: Record<string, unknown>; contentSelectorId?: string }>>('/api/v1/me/privileges'),
```

Note: `nexusApi.deleteComponent(id)` already exists in `client.ts` — use it for non-docker component deletion (see Step 5 below).

- [ ] **Step 2: Add Trash2 to lucide imports in BrowsePage**

In `BrowsePage.tsx`, add `Trash2` to the lucide-react import:
```tsx
import {
  ChevronDown,
  ChevronRight,
  FileText,
  FolderOpen,
  Layers,
  Package,
  RefreshCw,
  ShieldAlert,
  Tag,
  Trash2,
} from 'lucide-react'
```

- [ ] **Step 3: Add me-privileges query and canDeleteRepo helper**

In `BrowsePage.tsx`, add imports for `useCallback` (already imported) and `useAuthStore`:
```tsx
import { useAuthStore } from '@/store/authStore'
```

Inside the `BrowsePage` component (after existing queries), add:
```tsx
const { isAdmin } = useAuthStore()

const { data: myPrivs = [] } = useQuery({
  queryKey: ['me-privileges'],
  queryFn: () => nexspenceApi.myPrivileges().then(r => r.data),
})

const canDelete = useCallback((): boolean => {
  if (isAdmin()) return true
  return myPrivs.some(p =>
    Array.isArray((p.attrs?.actions as unknown)) &&
    (p.attrs.actions as string[]).includes('delete')
  )
}, [myPrivs, isAdmin])
```

- [ ] **Step 4: Add delete state and mutation**

Inside `BrowsePage`, add state for confirm modal:
```tsx
const [deleteTarget, setDeleteTarget] = useState<{ path: string } | null>(null)
const [deleting, setDeleting] = useState(false)
const [deleteError, setDeleteError] = useState('')
```

Add delete handler:
```tsx
async function handleDelete() {
  if (!deleteTarget || !selectedRepo) return
  setDeleting(true)
  setDeleteError('')
  try {
    await nexspenceApi.deleteByPath(selectedRepo, deleteTarget.path)
    setDeleteTarget(null)
    refetch()
    if (refetchDockerTree) refetchDockerTree()
  } catch (e: unknown) {
    const ax = e as { response?: { data?: { error?: string } } }
    setDeleteError(ax.response?.data?.error ?? 'Delete failed')
  } finally {
    setDeleting(false)
  }
}
```

Note: `refetchDockerTree` is the refetch returned by the docker tree query — check whether it exists in the current BrowsePage and expose it if needed. The non-docker components list refetch is already available as `refetch` from the components query.

- [ ] **Step 5: Add Delete button to non-docker browse rows**

Non-docker browse renders components from `items.map((c) => ...)` at `BrowsePage.tsx` around line 897. Each component `c` has `c.id`. Use the existing `nexusApi.deleteComponent(c.id)` — no path needed.

Change the delete state type to support both id-based and path-based deletion:
```tsx
const [deleteTarget, setDeleteTarget] = useState<{ id?: string; path?: string; label: string } | null>(null)
```

Update `handleDelete`:
```tsx
async function handleDelete() {
  if (!deleteTarget) return
  setDeleting(true)
  setDeleteError('')
  try {
    if (deleteTarget.id) {
      await nexusApi.deleteComponent(deleteTarget.id)
      refetch()
    } else if (deleteTarget.path && selectedRepo) {
      await nexspenceApi.deleteByPath(selectedRepo, deleteTarget.path)
      refetchDockerTree()
    }
    setDeleteTarget(null)
  } catch (e: unknown) {
    const ax = e as { response?: { data?: { error?: string } } }
    setDeleteError(ax.response?.data?.error ?? 'Delete failed')
  } finally {
    setDeleting(false)
  }
}
```

In the non-docker `items.map((c) => ...)` row (inside the `<div style={S.trow}>` block after the Assets cell), add a sixth cell:
```tsx
<div style={{ display: 'flex', justifyContent: 'flex-end' }}>
  {canDelete() && (
    <button
      onClick={e => { e.stopPropagation(); setDeleteTarget({ id: c.id, label: `${c.name}${c.version ? '@' + c.version : ''}` }) }}
      style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(239,68,68,0.6)', padding: '2px 6px', display: 'flex', alignItems: 'center' }}
      title="Delete component"
    >
      <Trash2 size={13} />
    </button>
  )}
</div>
```

Also add a header cell in `<div style={S.thead}>` to match:
```tsx
<div></div>
```

- [ ] **Step 6: Add Delete button to docker tree tag leaves**

In the Docker tree render, find where tag/manifest leaf nodes are rendered (look for `kind === 'tag'` or similar). Add:
```tsx
{canDelete() && node.kind === 'tag' && (
  <button
    onClick={e => { e.stopPropagation(); setDeleteTarget({ path: node.path, label: node.label ?? node.path }) }}
    style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(239,68,68,0.6)', padding: '2px 6px', marginLeft: 4, display: 'flex', alignItems: 'center' }}
    title="Delete tag"
  >
    <Trash2 size={13} />
  </button>
)}
```

- [ ] **Step 7: Add confirmation modal**

At the end of the `BrowsePage` JSX (before the closing `</div>`), add the modal:
```tsx
{deleteTarget && (
  <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
    <div style={{ background: '#0f172a', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 14, padding: 24, width: 420, display: 'flex', flexDirection: 'column', gap: 14 }}>
      <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: '#dbeafe' }}>Delete artifact?</h3>
      <p style={{ margin: 0, fontSize: 13, color: 'rgba(229,231,235,0.65)' }}>
        <code style={{ fontFamily: 'monospace', color: '#fca5a5' }}>{deleteTarget.label}</code>
        <br />This action cannot be undone.
      </p>
      {deleteError && (
        <div style={{ padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, color: '#ef4444', fontSize: 12 }}>
          {deleteError}
        </div>
      )}
      <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
        <button
          onClick={() => { setDeleteTarget(null); setDeleteError('') }}
          style={{ padding: '7px 14px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 13, fontWeight: 600, background: 'rgba(255,255,255,0.06)', color: '#fff' }}
        >
          Cancel
        </button>
        <button
          onClick={handleDelete}
          disabled={deleting}
          style={{ padding: '7px 14px', borderRadius: 8, border: 'none', cursor: 'pointer', fontSize: 13, fontWeight: 600, background: 'rgba(239,68,68,0.15)', color: '#ef4444' }}
        >
          {deleting ? 'Deleting…' : 'Delete'}
        </button>
      </div>
    </div>
  </div>
)}
```

- [ ] **Step 8: Type-check**

```bash
cd frontend && npx tsc --noEmit
```
Expected: 0 errors.

- [ ] **Step 9: Commit**

```bash
git add frontend/src/pages/BrowsePage.tsx frontend/src/api/client.ts
git commit -m "feat(12.5): browse artifact deletion with RBAC check and confirm modal"
```

---

## Final verification

- [ ] **Backend tests**

```bash
go test ./...
```
Expected: all tests pass (same count as before + no regressions).

- [ ] **Frontend type check**

```bash
cd frontend && npx tsc --noEmit
```
Expected: 0 errors.

- [ ] **Frontend build**

```bash
cd frontend && npm run build
```
Expected: clean build, no warnings about unused variables.

- [ ] **Update task_plan.md**

In `task_plan.md`, change Phase 12 status from `pending` to `complete` and check all sub-task boxes.

- [ ] **Final commit**

```bash
git add task_plan.md
git commit -m "docs: mark Phase 12 complete — Security polish, repo click-to-browse, artifact deletion"
```
