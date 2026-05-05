# Routing Rules — Design Spec

**Date:** 2026-05-05  
**Phase:** 14C  
**Status:** approved

## Overview

Routing rules control which artifact paths are allowed or blocked when a group repository delegates requests to its members. A rule with `mode=ALLOW` passes a request only if the path matches at least one regex matcher; `mode=BLOCK` skips the member if the path matches any matcher.

## What Is Already Done

The following code exists and requires no changes:

- `domain.RoutingRule` struct (`internal/domain/types.go`)
- `routing_rules` table in migration `001_initial.sql`
- `repository.RoutingRuleRepo` interface (`internal/repository/interfaces.go`)
- `postgres.RoutingRuleRepo` — full CRUD (`internal/repository/postgres/routing_rule_repo.go`)
- `service.RoutingRuleService` — CRUD + `Allow(rule, path)` + `validateMatchers` (`internal/service/routing_rule_service.go`)
- `handlers.RoutingRuleHandler` — List, Get, Create, Update, Delete (`internal/api/handlers/routing_rules.go`)

## Backend Changes

### 1. `formats/deps.go`

Add one field:

```go
RoutingRules repository.RoutingRuleRepo // optional: nil disables routing rule enforcement
```

### 2. `internal/api/router.go`

Instantiate and wire:

```go
rrRepo   := postgres.NewRoutingRuleRepo(pool)
rrSvc    := service.NewRoutingRuleService(rrRepo)
rrH      := handlers.NewRoutingRuleHandler(rrSvc)
```

Replace the single `stubHandler("routing")` line with five admin-only routes:

```
GET    /service/rest/v1/routing-rules          → rrH.List
GET    /service/rest/v1/routing-rules/:id      → rrH.Get
POST   /service/rest/v1/routing-rules          → rrH.Create
PUT    /service/rest/v1/routing-rules/:id      → rrH.Update
DELETE /service/rest/v1/routing-rules/:id      → rrH.Delete
```

Also add `RoutingRules: rrRepo` to `formatDeps`.

### 3. `internal/formats/group/handler.go`

In `serveGet`, after loading `repoDef` and `members`, add routing rule enforcement before the member loop:

```go
var rule *domain.RoutingRule
if repoDef.RoutingRuleID != nil {
    rule, _ = h.deps.RoutingRules.Get(ctx, *repoDef.RoutingRuleID)
}
```

Inside the member loop, skip members that fail the rule:

```go
if !service.Allow(rule, filePath) {
    continue
}
```

`serveWrite` is not filtered — routing rules apply to reads only (Nexus semantics).

**Error handling:** if `RoutingRules` is nil (deps not wired) or rule fetch fails, `Allow(nil, path)` returns `true` — fail-open, no disruption.

## Frontend Changes

### 1. `AdminPage.tsx` — new "Routing Rules" tab

Tab position: after Backup, before or after Migration (existing tabs).

**Tab contents:**

- Header: title + "Create Routing Rule" button (admin-only)
- Table columns: Name | Mode (colored badge: blue=ALLOW, orange=BLOCK) | Matchers | Actions (Edit, Delete)
- Empty state: "No routing rules configured"

**Create / Edit modal fields:**

| Field | Type | Validation |
|-------|------|-----------|
| Name | text input | required, unique |
| Description | text input | optional |
| Mode | select: ALLOW / BLOCK | required |
| Matchers | dynamic list of text inputs | each must be a valid regex; at least one entry recommended (warn if empty) |

Matchers list: "+ Add matcher" button appends an input; each row has an × to remove it.

**Delete:** confirmation dialog. If the rule is referenced by any repository, show a warning: "Used by: repo-a, repo-b. Deleting will remove the rule from those repositories." (backend sets `routing_rule_id = NULL` via `ON DELETE SET NULL`).

**API calls:** `GET/POST/PUT/DELETE /service/rest/v1/routing-rules`

### 2. `RepositoriesPage.tsx` — routing rule selector in Create/Edit repo modals

- Show the "Routing Rule" select field **only when `type === "group"`**
- Options: "None" (sends `routingRuleId: null`) + list from `GET /service/rest/v1/routing-rules`
- Placement: after the members list, before Blob Store selector
- On save: include `routingRuleId` in the repo payload (already supported by `RepositoryRepo.Update`)

### 3. `client.ts` — API helpers

Add to `nexspenceApi` (or `nexusApi`):

```ts
listRoutingRules(): Promise<RoutingRule[]>
createRoutingRule(r: RoutingRuleInput): Promise<RoutingRule>
updateRoutingRule(id: string, r: RoutingRuleInput): Promise<RoutingRule>
deleteRoutingRule(id: string): Promise<void>
```

And the TypeScript types:

```ts
interface RoutingRule {
  id: string
  name: string
  description?: string
  mode: 'ALLOW' | 'BLOCK'
  matchers: string[]
  createdAt: string
  updatedAt: string
}

type RoutingRuleInput = Omit<RoutingRule, 'id' | 'createdAt' | 'updatedAt'>
```

## Data Flow

```
Client GET /repository/my-group/com/example/foo-1.0.jar
  → group.serveGet
    → load repoDef (has routingRuleId = "uuid-abc")
    → load RoutingRule{mode=BLOCK, matchers=[".*-SNAPSHOT.*"]}
    → filePath = "/com/example/foo-1.0.jar"
    → service.Allow(rule, filePath) → true (no SNAPSHOT match)
    → delegate to members in order → first non-404 wins
```

```
Client GET /repository/my-group/com/example/foo-1.0-SNAPSHOT.jar
  → service.Allow(rule, filePath) → false (matches BLOCK pattern)
  → member skipped → 404 if all members skipped
```

## Error Cases

| Scenario | Behaviour |
|----------|-----------|
| `RoutingRules` dep is nil | `Allow(nil, path)` → `true`, rule not enforced |
| Rule fetch fails (DB error) | `Allow(nil, path)` → `true`, fail-open |
| All members blocked by rule | 404 with `"artifact not found in any member of group"` |
| Invalid matcher regex on create | `validateMatchers` returns 400 |
| Delete rule used by repo | DB `ON DELETE SET NULL` removes FK; no cascade delete of repos |

## Files Modified

| File | Change |
|------|--------|
| `internal/formats/deps.go` | add `RoutingRules` field |
| `internal/api/router.go` | wire rrRepo/rrSvc/rrH; replace stub; add `RoutingRules` to formatDeps |
| `internal/formats/group/handler.go` | load rule in `serveGet`, filter members via `service.Allow` |
| `frontend/src/api/client.ts` | add `RoutingRule` types + API helpers |
| `frontend/src/pages/AdminPage.tsx` | add Routing Rules tab |
| `frontend/src/pages/RepositoriesPage.tsx` | add routing rule selector in group repo create/edit |
