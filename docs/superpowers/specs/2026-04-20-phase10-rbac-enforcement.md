# Phase 10: RBAC Enforcement + Anonymous Repository Access

**Date:** 2026-04-20  
**Status:** approved

## Decisions

- `allow_anonymous DEFAULT FALSE` — all repos private by default (option A)
- No access = complete invisibility — users see only repos they can access (option A)
- Middleware-first approach — central `RBACMiddleware` + shared `RBACService` for listings

## Architecture

### Data Layer

**Migration `006_repo_allow_anonymous.sql`:**
```sql
ALTER TABLE repositories ADD COLUMN allow_anonymous BOOLEAN NOT NULL DEFAULT FALSE;
```

**`PrivilegeWithSelector`** struct (returned by `RBACRepo`):
```go
type PrivilegeWithSelector struct {
    Actions    []string // ["read","browse","write","delete"]
    Expression string   // CEL expression from content_selector
}
```

**`RBACRepo.GetUserPrivilegesWithSelectors(ctx, userID)`** — JOIN:
`user_roles → role_privileges → privileges → content_selectors`

### Service Layer (`internal/service/rbac_service.go`)

`RBACService.CanAccess(ctx, user, repoName, path, action) bool`:
1. user has `nx-admin` role → **true** (bypass)
2. repo has `allow_anonymous = true` AND action in `{read, browse}` → **true**
3. load `GetUserPrivilegesWithSelectors(user.ID)` → eval each:
   - action matches privilege actions (empty actions list = all allowed)
   - CEL expression matches `{repository, path}` via simple regexp parser
4. any match → **true**, else → **false**

**CEL patterns supported (no external lib):**
- `repository == "X"` — exact repo name match
- `path.startsWith("Y")` — path prefix match  
- `repository == "X" && path.startsWith("Y")` — both conditions
- unknown pattern → **false** (safe deny)

`RBACService.FilterRepos(ctx, user, repos) []Repository`:
- keeps repos where `CanAccess(repo.Name, "/", "read")` → true

### Middleware (`internal/api/middleware/rbac.go`)

`RBACMiddleware(rbacSvc, repoRepo)` — applied to `/repository/:repoName/*`:
- reads `repoName` from param, `path` from `*path`
- maps HTTP method → action: `GET/HEAD→read`, `PUT/POST→write`, `DELETE→delete`
- resolves user from `c.MustGet("user")` (set by OptionalAuth) or nil for anon
- calls `CanAccess` → 403 on deny
- skips check if `user == nil && repo.allow_anonymous`

### Filtered Listings

Handlers that call `repo.List()` wrap result through `rbacSvc.FilterRepos`:
- `GET /service/rest/v1/repositories`
- `GET /api/v1/repositories`
- `GET /api/v1/browse/repositories`
- `GET /service/rest/v1/components` and `/search` — filter by accessible repo names

### UI

**RepositoriesPage:**
- Create modal: "Allow anonymous access" toggle (default off)
- Settings gear modal: same toggle
- Repos list: `anon` badge on repos with `allowAnonymous: true`

**BrowsePage:**
- 403 on load → "Access denied" placeholder instead of empty list

## Files Created/Modified

| File | Action |
|------|--------|
| `internal/db/migrations/006_repo_allow_anonymous.sql` | new |
| `internal/domain/types.go` | add `AllowAnonymous bool` to `Repository` |
| `internal/repository/interfaces.go` | add `RBACRepo` interface |
| `internal/repository/postgres/repository_repo.go` | include `allow_anonymous` in all queries |
| `internal/repository/postgres/rbac_repo.go` | new — implements `RBACRepo` |
| `internal/service/rbac_service.go` | new — `RBACService` |
| `internal/api/middleware/rbac.go` | new — `RBACMiddleware` |
| `internal/api/handlers/repositories.go` | filter listings via `RBACService` |
| `internal/api/router.go` | wire middleware + inject rbacSvc |
| `frontend/src/pages/RepositoriesPage.tsx` | toggle + badge |
| `frontend/src/pages/BrowsePage.tsx` | 403 handling |
| `frontend/src/api/client.ts` | `allowAnonymous` in repo payloads |
