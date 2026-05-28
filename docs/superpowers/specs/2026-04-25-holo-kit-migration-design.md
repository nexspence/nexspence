# Holo-Kit UI Migration Design

**Date:** 2026-04-25  
**Scope:** Replace visual layer of self_nexus frontend with Holographic Depth (holo-kit). Preserve all routing, queries, auth, and API contracts.

---

## Decisions

| Question | Decision |
|----------|----------|
| Theme | Holographic Depth (holo-kit) — #080612, purple/cyan/magenta |
| Strategy | Shell first, then pages |
| TiltCard scope | RepositoriesPage + BrowsePage leaf cards only |
| Approach | Sequential grouped — Shell → Content → Admin |

---

## Architecture

### What moves

- `frontend/src/holo-kit/` → `frontend/src/components/holo/` (copy, rename)
- `main.tsx` — add CSS import at top
- `Layout.tsx` — wrap with `<HoloApp>`, replace `ProfileModal` inline styles with holo components
- `Layout.module.css` — rewrite with `--holo-*` tokens
- `LoginPage.tsx` / `LoginPage.module.css` — wrap with `<HoloApp>`, holo components
- 9 page files — page hero + holo components throughout

### What does not change

- `App.tsx` routing
- `apiClient`, `authStore`, `useQuery`/`useMutation`, axios interceptor
- `OIDCCallbackPage.tsx`, `MonitoringPage.tsx`
- lucide-react icons
- No new npm dependencies

---

## Phase 1 — Shell (Layout + Login)

**Files:** `main.tsx`, `Layout.tsx`, `Layout.module.css`, `LoginPage.tsx`, `LoginPage.module.css`

Steps:
1. Copy `holo-kit/` → `components/holo/`
2. `main.tsx`: add `import './components/holo/holo.css'` before other imports
3. `Layout.tsx`: wrap root div with `<HoloApp>`. Replace `ProfileModal` `S.overlay`/`S.modal` with `<HoloModal>`, all `S.btn()` calls with `<HoloButton>`, all `S.input` with `<HoloInput>`
4. `Layout.module.css`: rewrite `.root`, `.sidebar`, `.navBtn`, `.navBtn.active`, `.divider`, `.danger`, `.version` using `--holo-*` tokens per README Step 3
5. `LoginPage.tsx`: wrap with `<HoloApp>`, use `<HoloText as="h1">` for title, `<HoloButton variant="primary" type="submit">` for submit, `<HoloInput>` for fields, OIDC button keeps existing structure but gets `holo-btn holo-btn--primary` class
6. `LoginPage.module.css`: `.root { background: var(--holo-bg) }`, `.card` → glass card styles via `--holo-*` tokens

**Verify after Phase 1:**
- `/login` renders holographic gradient title + glass card
- Sidebar nav active state uses gradient background
- ProfileModal opens with gradient top edge
- No console errors; OIDC logout and token CRUD work

---

## Phase 2 — Content Pages (Repos · Browse · Search)

**Files:** `RepositoriesPage.tsx`, `RepositoriesPage.module.css`, `BrowsePage.tsx`, `SearchPage.tsx`, `Select.tsx`, `MultiSelect.tsx`

Steps:
1. **Page hero** on all 3 pages: `holo-section-label` breadcrumb + `<HoloText as="h1">` title + subtitle div
2. **RepositoriesPage**: repo grid cards → `<TiltCard intensity={10}><HoloCard edge>`, format/type badges → `<HoloPill>`, action buttons → `<HoloButton>`, search/filter inputs → `<HoloInput>`, item counts → `<CountUp>`, create/edit modals → `<HoloModal>`
3. **BrowsePage**: artifact/directory cards → `<TiltCard><HoloCard edge>`, action buttons → `<HoloButton>`, modals → `<HoloModal>`
4. **SearchPage**: result rows — flat `<HoloCard>` (no tilt — list view), badges → `<HoloPill>`, search input → `<HoloInput>`
5. **Select / MultiSelect**: trigger element gets `className="holo-input"`, dropdown panel gets `className="holo-card"`

---

## Phase 3 — Admin Pages

**Files:** `UsersPage.tsx`, `UsersPage.module.css`, `SecurityPage.tsx`, `AdminPage.tsx`, `AuditPage.tsx`, `CleanupPage.tsx`, `MigrationPage.tsx`

All 6 pages follow the same pattern:
1. Page hero (section label + HoloText h1 + subtitle)
2. Tab strips → `<HoloTabs>` (SecurityPage, AdminPage, AuditPage)
3. All `<table>` → add `className="holo-table"`, status cells → `<HoloPill tone=...>`
4. All modals → `<HoloModal>`
5. All buttons → `<HoloButton>`, all inputs → `<HoloInput>`

Page-specific notes:
- **UsersPage** `AssignRolesModal`: role chips → `<HoloPill>`, search input → `<HoloInput>`
- **AuditPage** Export: keep `fetch` + blob + `<a download>` pattern unchanged (JWT auth); wrap button in `<HoloButton>`
- **SecurityPage**: non-admin read-only view preserved as-is (logic untouched)

---

## Token Reference

```css
--holo-bg: #080612
--holo-text: #f4f0ff
--holo-text-dim: rgba(244,240,255,0.55)
--holo-a: #7c5cff  /* purple */
--holo-b: #22d3ee  /* cyan */
--holo-c: #ff5cf0  /* magenta */
--holo-green: #5effb8
--holo-amber: #ffc857
--holo-red:   #ff6b6b
```

## Component Map

| Old pattern | New component |
|-------------|--------------|
| `S.overlay` + `S.modal` | `<HoloModal>` |
| `S.btn('primary')` | `<HoloButton variant="primary">` |
| `S.btn('danger')` | `<HoloButton variant="danger">` |
| `S.btn('ghost')` | `<HoloButton>` |
| `S.input` / inline input styles | `<HoloInput>` |
| Tab strip `<button>` array | `<HoloTabs items={...}>` |
| Status `<span>` badge | `<HoloPill tone="success|warn|danger">` |
| `<table>` | `<table className="holo-table">` |
| Repo/artifact card div | `<TiltCard><HoloCard edge>` (Phase 2 only) |
| Page `<h1>` | `<HoloText as="h1">` |
| Numeric stat | `<CountUp to={n}>` |
