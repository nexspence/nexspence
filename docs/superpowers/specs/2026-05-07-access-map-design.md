# Phase 58 — Interactive Security Access Map

## Overview

New "Access Map" tab in SecurityPage (admin-only). Shows the full RBAC graph: Users → Roles → Privileges → Content Selectors. The user picks a starting node via type pills + searchable combobox; the SVG graph highlights the full chain in both directions from the selected node; a sidebar shows node details.

## Layout

Hierarchical columns left-to-right: **Users | Roles | Privileges | Content Selectors**. Implemented as plain SVG (`<svg>`, no external graph libraries) with `<rect>` nodes, `<line>` + `<marker>` arrows, fixed column x-positions, evenly spaced rows per column.

## Node Selection

Above the graph:
- **Type pills** — four toggle buttons: User / Role / Privilege / Content Selector. Only one active at a time.
- **Searchable combobox** — text input that filters entities of the selected type by name. Dropdown shows matching items with a secondary hint (e.g. "3 privileges" for roles). Graph builds only on explicit click/selection from the dropdown, not on hover.

Initial state: empty graph, prompt text in center ("Select a node to explore the access graph").

## Interaction

1. User selects type pill + picks entity from combobox → graph renders all 4 columns.
2. Selected node is highlighted (full color, thicker border). All nodes in its chain (both directions) are highlighted at normal opacity. Unrelated nodes are dimmed (`opacity: 0.15`).
3. Chain direction is always full: selecting any node type shows its upstream and downstream connections.
   - Role selected → left: Users that have this role; right: Privileges + their CSs
   - Privilege selected → left: Roles containing it; right: its CS (if any)
   - CS selected → left: Privileges using it → their Roles → their Users
4. Clicking a different node in the graph changes selection (same behavior as combobox pick).
5. Hovering any node highlights its immediate neighbors at 50% (soft preview, not full chain).
6. **Reset** button — clears selection, returns to empty state.
7. **Fit** button — recalculates SVG `viewBox` to fit all rendered nodes within the container.

## Sidebar (right panel)

Appears when a node is selected. Content by type:

| Type | Shown |
|---|---|
| User | username, email, status, source, role list |
| Role | name, description, privilege count, nested roles |
| Privilege | name, type, attrs (format/repository/actions), linked CS name |
| Content Selector | name, expression (monospace) |

## Backend

### New endpoint

```
GET /api/v1/security/access-graph
```

- Admin-only (requires `nx-admin` role check via existing `AdminOnly` middleware).
- Returns all 4 entity collections in one response; joins are resolved on the frontend.
- Queries existing repos: `UserRepo.List`, `RoleRepo.List`, `PrivilegeRepo.List`, `ContentSelectorRepo.List`, plus `RoleRepo.GetUserRoles` for the user→role edges.

Response shape:
```json
{
  "users": [
    { "id": "...", "username": "alice", "email": "...", "status": "active", "source": "local", "roleIds": ["role-uuid-1"] }
  ],
  "roles": [
    { "id": "...", "name": "dev-read", "description": "...", "privilegeIds": ["priv-uuid-1"], "roleIds": [] }
  ],
  "privileges": [
    { "id": "...", "name": "mvn-read", "type": "repository-content-selector", "attrs": {}, "contentSelectorId": "cs-uuid-1" }
  ],
  "selectors": [
    { "id": "...", "name": "cs-maven", "expression": "format == \"maven2\"" }
  ]
}
```

### Handler location

`internal/api/handlers/access_graph.go` — new file, single handler `GetAccessGraph`.
Wired in `router.go` under `api.GET("/security/access-graph", adminOnly, accessGraphHandler.Get)`.

## Frontend

### Component

New function `AccessMapTab()` in `SecurityPage.tsx`.

State:
- `selectedType: 'user' | 'role' | 'privilege' | 'selector' | null`
- `selectedId: string | null`
- `hoveredId: string | null`
- `search: string`
- `comboOpen: boolean`

Data: `useQuery(['access-graph'], () => nexspenceApi.get('/security/access-graph'))` — fetched once on mount, cached.

Graph rendering (`renderGraph()`):
- Compute column positions (fixed x: users=80, roles=220, privileges=380, selectors=540)
- Compute y per node: evenly space within column height
- Build edge list from data relationships
- On render: for each node, compute `isInChain(node)` → full opacity or 0.15
- SVG markers for arrowheads (one `<defs>` block, 4 colored markers)

Tab registration in `allTabs` array (admin-only, after Webhooks).

## Files Changed

| File | Change |
|---|---|
| `internal/api/handlers/access_graph.go` | New handler |
| `internal/api/router.go` | Wire new route |
| `frontend/src/pages/SecurityPage.tsx` | Add `AccessMapTab` + tab entry |

## Out of Scope

- Pagination (assume ≤500 nodes total — acceptable for admin graph)
- Export to PNG/SVG
- Nested role expansion beyond one level
- Non-admin users seeing the tab
