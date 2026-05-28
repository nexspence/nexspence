# Sidebar Footer Redesign

**Date:** 2026-05-13  
**Status:** Approved

## Problem

The sidebar footer is visually cluttered: Documentation nav link, user info block (with separate key icon button), Sign Out button, version text, and collapse arrow are all stacked without clear hierarchy or grouping.

## Approved Design

### Navigation structure

Three sections in the scrollable nav area (unchanged order):

```
BROWSE
  Repositories
  Browse
  Search

SYSTEM          (admin only)
  Security
  System Admin
  Audit Log
  Cleanup Policies

DOCS
  Documentation
```

`Documentation` moves from the footer into the nav as its own **DOCS** section — same visual pattern as BROWSE / SYSTEM (section label + single nav item with `BookOpen` icon). The section label uses a cyan accent color (`rgba(34,211,238,0.45)`) to distinguish it. The nav item uses a subtle cyan-tinted background (`rgba(34,211,238,0.05)`) with a matching border.

### Command bar (replaces footer)

A single pill-shaped bar pinned at the bottom of the sidebar, outside the scroll area:

```
[ avatar ] [ name / role ] | [ key icon ] | [ logout icon ]
```

- **Avatar** — 34×34px gradient tile (`#7c5cff → #22d3ee`), shows first letter of username. On click: opens existing `ProfileModal`.
- **Name + role** — flex-grows to fill space. Username truncated with ellipsis. Role shown below in purple.
- **Separator** — 1px vertical line (`rgba(124,92,255,0.22)`), 20px tall.
- **Key icon** — `Key` lucide icon, `rgba(229,231,235,0.4)`. On click: opens `ProfileModal` (same as avatar). Tooltip: "API Tokens & Profile".
- **Separator**
- **Logout icon** — `LogOut` lucide icon, `rgba(239,68,68,0.65)`. Background zone: `rgba(239,68,68,0.06)`. Tooltip: "Sign Out". On click: existing logout logic.

**Collapsed state:** The entire command bar collapses to a single 36×36px rounded avatar button (gradient tile, same colors). Clicking it opens `ProfileModal`. Key and logout icons are hidden in collapsed mode.

### Version text

Tiny text (`8px`, `rgba(229,231,235,0.17)`) centered below the command bar. **Hidden in collapsed mode** (same pattern as other labels — `opacity: 0; max-height: 0`).

### Collapse control

Replaces the current `<ChevronLeft>/<ChevronRight>` button with a **drag-handle strip**:

- `3px` tall, `border-radius: 2px`, centered pill highlight via `::after` pseudo-element
- **Expanded:** `margin: 2px 20px 0` — pill spans most of the 260px width
- **Collapsed:** `margin: 2px 6px 0` — pill spans most of the 48px width (same visual proportion)
- On click: toggles sidebar collapsed/expanded (same logic as before)
- Cursor: `pointer`

No text, no arrow icon.

## What is NOT changing

- Sidebar width: 260px expanded / 48px collapsed
- Grid transition animation
- `localStorage` persistence of collapsed state
- `ProfileModal` internals
- OIDC logout logic
- RBAC visibility (SYSTEM section admin-only)
- `navScrollArea` internal scroll introduced in the sticky-fix session

## Files affected

| File | Change |
|------|--------|
| `frontend/src/components/Layout.tsx` | Restructure footer JSX: move Documentation to nav, add command bar, replace collapse button with handle strip |
| `frontend/src/components/Layout.module.css` | Add `.commandBar`, `.commandBarAvatar`, `.commandBarUser`, `.commandBarSep`, `.commandBarAction`, `.commandBarActionDanger`, `.docsSection`, `.collapseHandle` styles; remove `.collapseBtn`, `.footer` (or repurpose) |
