# Phase 57: Role Privilege Detail View — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** In the Roles tab of SecurityPage, each role card gets an expandable inline panel showing full privilege details (type badge, name, Content Selector name, action chips) — so admins see what a role actually grants without opening the Edit modal.

**Architecture:** Pure frontend change — no new backend endpoints needed. `GET /service/rest/v1/security/roles/:id/privileges` already returns full `Privilege` objects; we fetch lazily on first expand and cache per-role in a `Map` stored in component state. The PrivilegesTab "Used by roles" section already works via `privRoleMap`; Phase 57 adds nothing there.

**Tech Stack:** React, TypeScript, React Query (existing), `nexusApi.listRolePrivileges()` (existing)

---

## File Map

| File | Change |
|------|--------|
| `frontend/src/pages/SecurityPage.tsx` | Add `expandedRoles` Set + `rolePrivCache` Map state; replace privilege chip row with expand-toggle button + inline detail panel |

---

### Task 1: Add expand state and lazy privilege loader to RolesTab

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx` — `RolesTab` function (lines ~237–429)

This task adds two pieces of state and a loader function — no UI change yet.

- [ ] **Step 1: Add state inside `RolesTab`**

Locate the existing state declarations in `RolesTab` (around line 240). Add after `const [roleSearch, setRoleSearch] = useState('')`:

```tsx
const [expandedRoles, setExpandedRoles] = useState<Set<string>>(new Set())
const [rolePrivCache, setRolePrivCache] = useState<Map<string, Privilege[]>>(new Map())
const [loadingExpand, setLoadingExpand] = useState<Set<string>>(new Set())
```

- [ ] **Step 2: Add toggle function**

Add after the new state declarations:

```tsx
async function toggleExpand(roleId: string) {
  setExpandedRoles(prev => {
    const next = new Set(prev)
    if (next.has(roleId)) { next.delete(roleId) } else { next.add(roleId) }
    return next
  })
  if (!rolePrivCache.has(roleId)) {
    setLoadingExpand(prev => new Set(prev).add(roleId))
    try {
      const privs = await nexusApi.listRolePrivileges(roleId).then(r => r.data as Privilege[])
      setRolePrivCache(prev => new Map(prev).set(roleId, privs))
    } finally {
      setLoadingExpand(prev => { const next = new Set(prev); next.delete(roleId); return next })
    }
  }
}
```

- [ ] **Step 3: Build the app to verify no TypeScript errors**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core/frontend && npm run build 2>&1 | tail -20
```

Expected: build succeeds (exit 0), no type errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx
git commit -m "feat(security): add expand state and lazy privilege loader to RolesTab"
```

---

### Task 2: Replace privilege chip row with expand toggle button

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx` — role card render block (lines ~372–381)

Replace the current static chip row (which shows up to 4 privilege ID strings + "+N more") with a clickable badge that shows count and triggers expand.

- [ ] **Step 1: Replace the privilege chip section in the role card**

Find this block inside the `{filtered.map(r => (` render:

```tsx
{(r.privileges ?? []).length > 0 && (
  <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' as const, marginTop: 4 }}>
    {(r.privileges ?? []).slice(0, 4).map(p => (
      <span key={p} style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: 'rgba(99,102,241,0.12)', color: '#a5b4fc', fontFamily: 'monospace' }}>{p}</span>
    ))}
    {(r.privileges ?? []).length > 4 && (
      <span style={{ fontSize: 10, color: 'var(--holo-text-faint)' }}>+{(r.privileges ?? []).length - 4} more</span>
    )}
  </div>
)}
```

Replace with:

```tsx
{(r.privileges ?? []).length > 0 && (
  <button
    onClick={e => { e.stopPropagation(); toggleExpand(r.id) }}
    style={{
      marginTop: 4, display: 'inline-flex', alignItems: 'center', gap: 5,
      background: 'rgba(99,102,241,0.12)', border: '1px solid rgba(99,102,241,0.25)',
      borderRadius: 6, padding: '2px 8px', cursor: 'pointer', fontSize: 11,
      color: '#a5b4fc', transition: 'background 0.15s',
    }}
    onMouseEnter={e => (e.currentTarget.style.background = 'rgba(99,102,241,0.22)')}
    onMouseLeave={e => (e.currentTarget.style.background = 'rgba(99,102,241,0.12)')}
  >
    {loadingExpand.has(r.id)
      ? <Loader size={11} style={{ animation: 'spin 1s linear infinite' }} />
      : <span style={{ fontSize: 10 }}>{expandedRoles.has(r.id) ? '▲' : '▼'}</span>
    }
    {(r.privileges ?? []).length} privilege{(r.privileges ?? []).length !== 1 ? 's' : ''}
  </button>
)}
```

- [ ] **Step 2: Add the `Loader` import if not already present**

Check line 4 of SecurityPage.tsx — `Loader` is already imported from `lucide-react`. No change needed.

- [ ] **Step 3: Build to verify no type errors**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core/frontend && npm run build 2>&1 | tail -20
```

Expected: build succeeds, no errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx
git commit -m "feat(security): replace privilege chip row with expand-toggle badge on role cards"
```

---

### Task 3: Render inline privilege detail panel

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx` — role card, after the expand button, inside the `<div style={{ minWidth: 0 }}>` block

Add the expandable inline panel below the privilege count badge. The panel shows one row per privilege: type badge + name + Content Selector name (if any) + action chips.

- [ ] **Step 1: Fetch selectors for CS name lookup**

In `RolesTab`, add a query for selectors (reuses cached data from ContentSelectorsTab via React Query):

Add after the existing `allPrivs` state:

```tsx
const { data: allSelectors = [] } = useQuery<{ id: string; name: string }[]>({
  queryKey: ['content-selectors'],
  queryFn: () => nexusApi.listContentSelectors().then(r => r.data),
  staleTime: 60_000,
})
```

- [ ] **Step 2: Add the detail panel to the role card**

Inside the role card's `<div style={{ minWidth: 0 }}>` block, add this immediately after the privilege toggle button:

```tsx
{expandedRoles.has(r.id) && (
  <div style={{
    marginTop: 8, borderTop: '1px solid rgba(124,92,255,0.15)',
    paddingTop: 8, display: 'flex', flexDirection: 'column', gap: 4,
  }}>
    {(rolePrivCache.get(r.id) ?? []).length === 0 && !loadingExpand.has(r.id) && (
      <span style={{ fontSize: 11, color: 'var(--holo-text-faint)' }}>No privileges assigned</span>
    )}
    {(rolePrivCache.get(r.id) ?? []).map(p => {
      const actions = (p.attrs?.actions as string[] | undefined) ?? []
      const typeColor = PRIV_TYPE_COLOR[p.type] ?? '#6b7280'
      const csName = allSelectors.find(s => s.id === p.contentSelectorId)?.name
      return (
        <div key={p.id} style={{
          display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' as const,
          padding: '4px 0',
        }}>
          <span style={{
            fontSize: 9, fontWeight: 700, padding: '1px 5px', borderRadius: 3,
            textTransform: 'uppercase' as const, letterSpacing: '0.4px',
            background: typeColor + '22', color: typeColor, whiteSpace: 'nowrap' as const,
          }}>
            {(p.type as string) === 'repository-content-selector' ? 'cs' : p.type.replace('repository-', '')}
          </span>
          <span style={{ fontSize: 12, color: 'var(--holo-text)', fontWeight: 500 }}>{p.name}</span>
          {csName && (
            <span style={{ fontSize: 10, color: '#67e8f9', padding: '1px 5px', background: 'rgba(6,182,212,0.1)', borderRadius: 3 }}>
              {csName}
            </span>
          )}
          {actions.map(a => {
            const ac = (a === 'write' || a === 'delete') ? '#f59e0b' : '#22c55e'
            return (
              <span key={a} style={{ fontSize: 9, padding: '1px 5px', borderRadius: 3, background: ac + '22', color: ac }}>
                {a}
              </span>
            )
          })}
        </div>
      )
    })}
  </div>
)}
```

- [ ] **Step 3: Import `useQuery` — already imported at line 2, no change needed**

- [ ] **Step 4: Build to verify no type errors**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core/frontend && npm run build 2>&1 | tail -20
```

Expected: exit 0, no TypeScript errors, bundle size under 500 kB.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx
git commit -m "feat(security): add inline privilege detail panel to role cards"
```

---

### Task 4: Update task_plan.md + NEXT_RELEASE.md

**Files:**
- Modify: `task_plan.md`
- Modify: `NEXT_RELEASE.md`

- [ ] **Step 1: Mark Phase 57 complete in task_plan.md**

Find the Phase 57 block and change `**Status:** backlog` to `**Status:** complete (2026-05-07)`.

Mark all task checkboxes as done:
```
- [x] Frontend: расширить карточку роли ...
- [x] Frontend: догрузить объекты привилегий ...
- [x] Frontend: показывать badge с кол-вом ...
- [x] Frontend: PrivilegesTab — добавить колонку "Used by roles" (already implemented)
```

- [ ] **Step 2: Append entry to NEXT_RELEASE.md**

Add to the `### ✨ Features` section:

```markdown
* **Phase 57 — Role Privilege Detail View**: Role cards in SecurityPage now show a clickable privilege-count badge (e.g. "3 privileges ▼"). Expanding a card reveals an inline panel per privilege: type badge, name, linked Content Selector name, and action chips (read/browse/write/delete). Privilege details are fetched lazily on first expand and cached client-side.
```

- [ ] **Step 3: Commit**

```bash
git add task_plan.md NEXT_RELEASE.md
git commit -m "docs: mark Phase 57 complete, update NEXT_RELEASE"
```
