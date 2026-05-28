# Select Component — Design Spec
**Date:** 2026-04-21  
**Phase:** 13 — UI Polish: Custom Dropdown Components  
**Status:** Approved

---

## Goal

Replace all native `<select>` elements and the ad-hoc `RepoSelect` component in `BrowsePage.tsx` with a single reusable `Select` component that matches the VMSManager dark glassmorphism theme. Reference design: `frontend/example_drop_down.jpg`.

---

## Component

**File:** `frontend/src/components/Select.tsx`

### Types

```ts
interface SelectOption {
  value: string
  label: string
  badge?: ReactNode   // optional format badge (e.g. docker/maven2)
  tag?: ReactNode     // optional type tag (hosted/proxy/group)
}

interface SelectProps {
  options: SelectOption[]
  value: string
  onChange: (value: string) => void
  placeholder?: string       // default: "— Select —"
  disabled?: boolean
  style?: CSSProperties      // outer wrapper style (e.g. minWidth)
}
```

### Behaviour

- **Click-outside close:** `useRef<HTMLDivElement>` + `mousedown` listener via `useEffect`
- **Escape close:** `keydown` listener when open
- **Chevron:** rotates 180° when open (`transform: rotate(180deg)`, transition 0.2s)
- **Selected highlight:** option matching `value` receives selected style
- No external dependencies

---

## Visual Tokens

| Property | Value |
|---|---|
| Trigger background | `rgba(15,20,40,0.8)` |
| Trigger border (default) | `1.5px solid rgba(255,255,255,0.1)` |
| Trigger border (open) | `1.5px solid #3b82f6` |
| Trigger focus ring (open) | `box-shadow: 0 0 0 3px rgba(59,130,246,0.12)` |
| Trigger border-radius (closed) | `8px` |
| Trigger border-radius (open) | `8px 8px 0 0` |
| Dropdown background | `rgba(8,13,28,0.98)` |
| Dropdown border | `1.5px solid #3b82f6`, `border-top: none` |
| Dropdown border-radius | `0 0 8px 8px` |
| Dropdown max-height | `260px` |
| Dropdown box-shadow | `0 12px 40px rgba(0,0,0,0.6)` |
| Option padding | `10px 14px` |
| Option hover | `background: rgba(59,130,246,0.10)` |
| Option selected | `background: rgba(59,130,246,0.15)`, color `#93c5fd` |
| Scrollbar width | `6px` |
| Scrollbar thumb | `linear-gradient(#3b82f6, #1d4ed8)` |
| Font size | `13px` |

---

## Replacements — 10 dropdowns across 7 files

| File | Location | Options | badge/tag? |
|---|---|---|---|
| `BrowsePage.tsx` | `RepoSelect` component (delete entirely) | repository list | ✓ format + type |
| `RepositoriesPage.tsx` | Format filter (toolbar) | formats + "All" | — |
| `RepositoriesPage.tsx` | Format (create modal) | formats | — |
| `RepositoriesPage.tsx` | Type (create modal) | hosted / proxy / group | — |
| `SearchPage.tsx` | Format filter | formats + "any" | — |
| `AuditPage.tsx` | Domain filter | domains | — |
| `AuditPage.tsx` | Action filter | actions | — |
| `CleanupPage.tsx` | Format (policy form) | formats + "All" | — |
| `SecurityPage.tsx` | Content Selector (privilege modal) | content selectors | — |
| `UsersPage.tsx` | Status (create user modal) | active / disabled | — |

### Cleanup after replacement

- `RepositoriesPage.module.css` — remove `.selectWrapper`, `.select`, `.selectIcon`
- `SearchPage.tsx` — remove `S.selectWrap`, `S.select`, `S.selectIcon` from styles object

---

## Out of scope

- `MultiSelect` (multi-value selection) — deferred to Phase 13.2 when a concrete use case arises
- Searchable/filterable dropdown — not needed for any current use case
- Keyboard navigation (↑↓ + Enter) — nice-to-have, add if straightforward

---

## Success criteria

- `npx tsc --noEmit` → 0 errors
- `npm run build` → clean
- All 10 dropdowns visually match `example_drop_down.jpg`
- No native `<select>` remains in the app (except `<textarea>`, `<input>`)
