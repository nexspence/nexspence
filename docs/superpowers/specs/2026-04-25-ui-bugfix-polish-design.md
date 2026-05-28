# UI Bug-fix & Polish — Design Spec
**Date:** 2026-04-25

## Scope

Seven areas across the frontend. All changes are visual/layout only — no API contracts, no routing, no state logic changes.

---

## 1. Dropdowns — unified Glass Pill style

**Applies to:** `frontend/src/components/Select.tsx`, `frontend/src/components/MultiSelect.tsx`

### Trigger
- Shape: full pill (`border-radius: 999px`)
- Background: `rgba(124,92,255,0.08)`
- Border: `1px solid rgba(124,92,255,0.35)`
- Ring when open: `box-shadow: 0 0 0 3px rgba(124,92,255,0.12)`

### Dropdown panel
- Full `border-radius: 14px` (detached from trigger, `margin-top: 4px`)
- `padding: 6px` with `gap: 2px` between items
- Box shadow: `0 12px 40px rgba(0,0,0,0.6)`

### Selected item inside dropdown
- Rounded rect `border-radius: 10px`
- Background: `rgba(124,92,255,0.18)`, border: `1px solid rgba(124,92,255,0.35)`
- Color: `#c4b5fd`, font-weight 600
- Purple dot `6×6px` (`border-radius: 50%`, `background: #7c5cff`, glow) on the left

### Hover item
- Background: `rgba(124,92,255,0.08)`, `border-radius: 8px`

### Overflow fix (modals)
The `.holo-modal` CSS has `overflow: hidden`, which clips dropdown panels rendered inside modals. **Fix:** use `ReactDOM.createPortal` to render the dropdown panel to `document.body` with `position: fixed` coordinates from `getBoundingClientRect()` of the trigger element. This fixes the clip universally for all dropdowns inside modals without touching `.holo-modal` CSS.

---

## 2. Repositories page — compact list rows

**Files:** `RepositoriesPage.tsx`, `RepositoriesPage.module.css`

### Layout
Replace `TiltCard` + `HoloCard` grid with flat list rows. No tilt, no glow on hover.

Each row: `display: grid; grid-template-columns: 8px 100px 1fr 110px 80px auto; align-items: center; gap: 14px; padding: 11px 16px`

Columns:
1. Status dot (green = online, dim = offline)
2. Format badge (colored, uppercase, `border-radius: 4px`)
3. Name (bold 13px) + description (11px, faint) — stacked, `min-width: 0` for truncation
4. Type pill (Hosted / Proxy / Group)
5. Storage: size text (monospace) + 3px progress bar beneath
6. Action buttons (ghost icon style — Settings, Delete)

### Hover
`border-color: rgba(124,92,255,0.45)`, `background: rgba(124,92,255,0.04)` — no glow, no transform.

### Create modal
`min-width: 640px` (currently ~420px). Ensures all form fields fit on one screen.

---

## 3. Browse — ghost icon action buttons

**File:** `BrowsePage.tsx`

Replace inline `<button style={{ background: 'none', ... }}>` on all action buttons (Download, Copy link, Delete, Update info) with the **ghost icon** style:

```
width: 24px; height: 24px; border-radius: 6px;
border: 1px solid rgba(124,92,255,0.25);
background: rgba(124,92,255,0.08);
color: rgba(124,92,255,0.9);
```

Hover: `background: rgba(124,92,255,0.2); border-color: rgba(124,92,255,0.5)`

Delete button: `border-color: rgba(255,107,107,0.25); background: rgba(255,107,107,0.07); color: #ff6b6b`
Delete hover: `background: rgba(255,107,107,0.18); border-color: rgba(255,107,107,0.5)`

Apply to every `hovered || selected` button group in the tree (file nodes, tag nodes, folder/group nodes).

---

## 4. Search — row hover highlight

**File:** `SearchPage.tsx`

Add hover state to `S.trow` (result rows). Use `useState<string|null>` for `hoveredId`, or inline `onMouseEnter`/`onMouseLeave`:

```
onMouseEnter: background = 'rgba(124,92,255,0.05)', borderBottom-color = 'rgba(124,92,255,0.15)'
onMouseLeave: revert to original
```

---

## 5. Security page

### 5a. Privileges table — layout fix
**Bug:** `<td style={{ display: 'flex', ... }}>` breaks table column widths and pushes Delete button outside bounds.

**Fix:**
- Remove `display: flex` from the action `<td>` → wrap buttons in `<div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>` inside the `<td>`
- Wrap `<table>` in `<div style={{ overflowX: 'auto' }}>` 
- Add explicit column widths: Name 20%, Type 15%, Actions 12%, Description auto, Used in Roles 15%, Buttons 80px (fixed)
- HoloCard padding: `padding: '0 16px'` (horizontal padding so table doesn't touch card edges)

Same fix applies to **ContentSelectors table** (same bug on its action `<td>`).

### 5b. Role creation — privilege selection
**Replace** `MultiSelect` in `RoleModal` with an inline two-panel transfer list:

- **Left panel** "Available" — scrollable list (`max-height: 180px`), click to move right. Search input at top.
- **Right panel** "Selected" — scrollable list, click to move left.
- Arrow buttons `→` / `←` between panels.
- Both panels: `border: 1px solid rgba(124,92,255,0.2); border-radius: 10px; overflow: hidden`
- Selected-row style: `background: rgba(124,92,255,0.12); color: #c4b5fd`

The transfer list replaces the `MultiSelect` component entirely in `RoleModal`. Applies to both Create and Edit role flows (both use the same `RoleModal` component). The `onChange` callback signature stays the same (`(ids: string[]) => void`).

### 5c. Privilege creation — Content Selector dropdown
The single `<Select>` for Content Selector is already fixed by the portal change in §1 — dropdown will no longer clip inside the modal.

---

## 6. System Admin — "Info" tab

**File:** `AdminPage.tsx`

Move System Status card + System Info card into a new first tab.

- Add `'info'` to `type AdminTab` and `VALID_TABS`
- New tab item: `{ value: 'info', label: <><Info size={13} />Info</> }` — inserted first
- Default tab: `'info'` (was `'blobs'`)
- Remove the 2-column grid with Status + Info from the top of the page
- Render them inside `{tab === 'info' && <InfoTab status={status} info={info} />}` (extract to a small inline function)

---

## 7. Profile modal — token list layout

**File:** `Layout.tsx`

Fix: when many tokens exist, the modal grows infinitely.

- Wrap the token list (`tokens.map(...)`) in `<div style={{ maxHeight: 240, overflowY: 'auto', display: 'flex', flexDirection: 'column' }}>` 
- Each token row: `display: flex; align-items: flex-start; gap: 10px; padding: 8px 0; borderBottom`
- Token name: `fontWeight: 600; fontSize: 13px; overflow: hidden; textOverflow: ellipsis; whiteSpace: nowrap`
- Meta line (dates): `fontSize: 11px; color: var(--holo-text-dim); marginTop: 2px`
- Delete button: aligned right, `flexShrink: 0`
- New-token reveal box: stays outside scroll area (above it), so it's always visible

---

## Files changed

| File | Changes |
|------|---------|
| `frontend/src/components/Select.tsx` | Pill trigger, portal dropdown, rounded-dot selected item |
| `frontend/src/components/MultiSelect.tsx` | Same trigger/dropdown style, portal |
| `frontend/src/pages/RepositoriesPage.tsx` | List rows, no TiltCard, wider modal |
| `frontend/src/pages/RepositoriesPage.module.css` | New `.list` + `.row` styles |
| `frontend/src/pages/BrowsePage.tsx` | Ghost icon button style on action buttons |
| `frontend/src/pages/SearchPage.tsx` | Row hover state |
| `frontend/src/pages/SecurityPage.tsx` | Table fix, transfer list, portal fixes overflow |
| `frontend/src/pages/AdminPage.tsx` | Info tab added first |
| `frontend/src/components/Layout.tsx` | Profile modal token list max-height + layout |
