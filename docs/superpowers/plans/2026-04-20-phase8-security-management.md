# Phase 8: Security Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire content-selector routes, add full Privilege CRUD (typed: wildcard + repository-view), upgrade RolesTab with modal privilege assignment.

**Architecture:** New `PrivilegeRepo` interface + postgres implementation + `PrivilegeHandler`. `RoleRepo` gains `SetPrivileges` / `ListPrivilegesByRole`. `ContentSelectorHandler` routes added to `router.go`. SecurityPage gains two new tabs (Privileges, Content Selectors) and inline role edit modal. Enforcement (gate in format handlers) is **not** part of this phase.

**Tech Stack:** Go 1.22, Gin, pgx v5, React 18 + TypeScript, Zustand / React Query, VMSManager K3S dark theme

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/domain/types.go` | Modify | Add `Privilege` struct |
| `internal/repository/interfaces.go` | Modify | Add `PrivilegeRepo` interface; extend `RoleRepo` with `SetPrivileges` + `ListPrivilegesByRole` |
| `internal/repository/postgres/privilege_repo.go` | Create | `PrivilegeRepo` postgres implementation |
| `internal/repository/postgres/role_repo.go` | Modify | `List` loads privilege IDs; add `SetPrivileges` + `ListPrivilegesByRole` |
| `internal/api/handlers/privileges.go` | Create | `PrivilegeHandler` CRUD |
| `internal/api/router.go` | Modify | Wire content-selector + privilege routes |
| `internal/testutil/mocks.go` | Modify | Add `PrivilegeRepo` mock; extend `RoleRepo` mock |
| `frontend/src/api/client.ts` | Modify | Add privilege/content-selector/role-privilege API helpers |
| `frontend/src/pages/SecurityPage.tsx` | Modify | Add Privileges tab, Content Selectors tab; upgrade RolesTab with edit modal |

---

## Task 1: `domain.Privilege` type

**Files:**
- Modify: `internal/domain/types.go`

- [ ] **Step 1: Add Privilege struct after the Role type (line ~304)**

Open `internal/domain/types.go` and insert after the `Role` struct closing brace:

```go
// ── Privilege ─────────────────────────────────────────────────

// PrivilegeType maps to the CHECK constraint in the privileges table.
type PrivilegeType string

const (
	PrivilegeTypeWildcard       PrivilegeType = "wildcard"
	PrivilegeTypeRepositoryView PrivilegeType = "repository-view"
	PrivilegeTypeRepositoryAdmin PrivilegeType = "repository-admin"
	PrivilegeTypeApplication    PrivilegeType = "application"
	PrivilegeTypeScript         PrivilegeType = "script"
)

// Privilege grants a user (via a Role) access to a set of actions.
// Attrs meaning per type:
//
//	wildcard          → {"pattern": "nexus:*:read"}
//	repository-view   → {"format": "maven2", "repository": "*", "actions": ["read"]}
//	repository-admin  → {"format": "*", "repository": "*", "actions": ["read","write","delete"]}
//	application       → {"domain": "users", "actions": ["read"]}
//	script            → {"name": "my-script", "actions": ["run"]}
type Privilege struct {
	ID                string        `json:"id"`
	Name              string        `json:"name"`
	Description       string        `json:"description,omitempty"`
	Type              PrivilegeType `json:"type"`
	Attrs             map[string]any `json:"attrs,omitempty"`
	ContentSelectorID *string       `json:"contentSelectorId,omitempty"`
	Builtin           bool          `json:"readOnly"`
	CreatedAt         time.Time     `json:"createdAt"`
}
```

- [ ] **Step 2: Build to verify no compile errors**

```bash
go build ./internal/domain/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/domain/types.go
git commit -m "feat(domain): add Privilege type"
```

---

## Task 2: `PrivilegeRepo` interface + extend `RoleRepo`

**Files:**
- Modify: `internal/repository/interfaces.go`

- [ ] **Step 1: Add PrivilegeRepo interface**

In `internal/repository/interfaces.go`, add after the `ContentSelectorRepo` block:

```go
// PrivilegeRepo manages privilege definitions.
type PrivilegeRepo interface {
	List(ctx context.Context) ([]domain.Privilege, error)
	Get(ctx context.Context, id string) (*domain.Privilege, error)
	GetByName(ctx context.Context, name string) (*domain.Privilege, error)
	Create(ctx context.Context, p *domain.Privilege) error
	Update(ctx context.Context, p *domain.Privilege) error
	Delete(ctx context.Context, id string) error
	// ListByRole returns privileges assigned to a role via role_privileges.
	ListByRole(ctx context.Context, roleID string) ([]domain.Privilege, error)
}
```

- [ ] **Step 2: Extend RoleRepo interface**

Replace the `RoleRepo` interface in `interfaces.go` with:

```go
// RoleRepo manages roles and privileges.
type RoleRepo interface {
	List(ctx context.Context) ([]domain.Role, error)
	Get(ctx context.Context, id string) (*domain.Role, error)
	Create(ctx context.Context, r *domain.Role) error
	Update(ctx context.Context, r *domain.Role) error
	Delete(ctx context.Context, id string) error
	GetUserRoles(ctx context.Context, userID string) ([]domain.Role, error)
	SetUserRoles(ctx context.Context, userID string, roleIDs []string) error
	// SetPrivileges replaces all role_privileges rows for the role.
	SetPrivileges(ctx context.Context, roleID string, privilegeIDs []string) error
	// ListPrivilegeIDsByRole returns privilege IDs for a role (lightweight, for JWT building).
	ListPrivilegeIDsByRole(ctx context.Context, roleID string) ([]string, error)
}
```

- [ ] **Step 3: Build (will fail until implementations exist)**

```bash
go build ./... 2>&1 | head -30
```

Expected: errors about missing methods on `*roleRepo` and missing `PrivilegeRepo` implementations — that's correct, Tasks 3 and 4 fix them.

- [ ] **Step 4: Commit**

```bash
git add internal/repository/interfaces.go
git commit -m "feat(repository): PrivilegeRepo interface + extend RoleRepo"
```

---

## Task 3: `postgres/privilege_repo.go`

**Files:**
- Create: `internal/repository/postgres/privilege_repo.go`

- [ ] **Step 1: Create the file**

```go
package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexspence-oss/nexspence/internal/domain"
)

type privilegeRepo struct{ db *pgxpool.Pool }

func NewPrivilegeRepo(db *pgxpool.Pool) *privilegeRepo {
	return &privilegeRepo{db: db}
}

func (r *privilegeRepo) List(ctx context.Context) ([]domain.Privilege, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, name, description, type, attrs, content_selector_id, builtin, created_at
		FROM privileges ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Privilege
	for rows.Next() {
		p, err := scanPrivilege(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (r *privilegeRepo) Get(ctx context.Context, id string) (*domain.Privilege, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, name, description, type, attrs, content_selector_id, builtin, created_at
		FROM privileges WHERE id = $1`, id)
	p, err := scanPrivilege(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (r *privilegeRepo) GetByName(ctx context.Context, name string) (*domain.Privilege, error) {
	row := r.db.QueryRow(ctx, `
		SELECT id, name, description, type, attrs, content_selector_id, builtin, created_at
		FROM privileges WHERE name = $1`, name)
	p, err := scanPrivilege(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

func (r *privilegeRepo) Create(ctx context.Context, p *domain.Privilege) error {
	attrsJSON, err := json.Marshal(p.Attrs)
	if err != nil {
		return err
	}
	return r.db.QueryRow(ctx, `
		INSERT INTO privileges (name, description, type, attrs)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`,
		p.Name, p.Description, string(p.Type), attrsJSON,
	).Scan(&p.ID, &p.CreatedAt)
}

func (r *privilegeRepo) Update(ctx context.Context, p *domain.Privilege) error {
	attrsJSON, err := json.Marshal(p.Attrs)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `
		UPDATE privileges SET name=$1, description=$2, type=$3, attrs=$4
		WHERE id=$5`,
		p.Name, p.Description, string(p.Type), attrsJSON, p.ID,
	)
	return err
}

func (r *privilegeRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM privileges WHERE id=$1 AND builtin=false`, id)
	return err
}

func (r *privilegeRepo) ListByRole(ctx context.Context, roleID string) ([]domain.Privilege, error) {
	rows, err := r.db.Query(ctx, `
		SELECT p.id, p.name, p.description, p.type, p.attrs, p.content_selector_id, p.builtin, p.created_at
		FROM privileges p
		JOIN role_privileges rp ON rp.privilege_id = p.id
		WHERE rp.role_id = $1
		ORDER BY p.name`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Privilege
	for rows.Next() {
		p, err := scanPrivilege(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func scanPrivilege(row scanner) (*domain.Privilege, error) {
	var p domain.Privilege
	var attrsRaw []byte
	var ptype string
	err := row.Scan(&p.ID, &p.Name, &p.Description, &ptype, &attrsRaw,
		&p.ContentSelectorID, &p.Builtin, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	p.Type = domain.PrivilegeType(ptype)
	if len(attrsRaw) > 0 {
		_ = json.Unmarshal(attrsRaw, &p.Attrs)
	}
	if p.Attrs == nil {
		p.Attrs = map[string]any{}
	}
	return &p, nil
}
```

- [ ] **Step 2: Build to verify**

```bash
go build ./internal/repository/postgres/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/repository/postgres/privilege_repo.go
git commit -m "feat(postgres): PrivilegeRepo implementation"
```

---

## Task 4: `role_repo.go` — add `SetPrivileges` + `ListPrivilegeIDsByRole`, load privileges in `List`

**Files:**
- Modify: `internal/repository/postgres/role_repo.go`

- [ ] **Step 1: Add SetPrivileges method**

Append to `internal/repository/postgres/role_repo.go`:

```go
func (r *roleRepo) SetPrivileges(ctx context.Context, roleID string, privilegeIDs []string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM role_privileges WHERE role_id = $1`, roleID); err != nil {
		return err
	}
	for _, pid := range privilegeIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO role_privileges (role_id, privilege_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			roleID, pid,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *roleRepo) ListPrivilegeIDsByRole(ctx context.Context, roleID string) ([]string, error) {
	rows, err := r.db.Query(ctx,
		`SELECT privilege_id FROM role_privileges WHERE role_id = $1`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, rows.Err()
}
```

- [ ] **Step 2: Build to verify**

```bash
go build ./...
```

Expected: no errors (all interfaces now satisfied).

- [ ] **Step 3: Commit**

```bash
git add internal/repository/postgres/role_repo.go
git commit -m "feat(postgres): RoleRepo.SetPrivileges + ListPrivilegeIDsByRole"
```

---

## Task 5: `testutil/mocks.go` — add `PrivilegeRepo` mock + extend `RoleRepo` mock

**Files:**
- Modify: `internal/testutil/mocks.go`

- [ ] **Step 1: Add compile-time assertion for PrivilegeRepo**

In the `var (...)` block at the top of `mocks.go`, add:

```go
_ repository.PrivilegeRepo = (*PrivilegeRepo)(nil)
```

- [ ] **Step 2: Add PrivilegeRepo mock**

Append to `mocks.go`:

```go
// ── PrivilegeRepo ─────────────────────────────────────────────

type PrivilegeRepo struct {
	mu   sync.Mutex
	data map[string]*domain.Privilege
}

func NewPrivilegeRepo(items ...*domain.Privilege) *PrivilegeRepo {
	r := &PrivilegeRepo{data: make(map[string]*domain.Privilege)}
	for _, p := range items {
		r.data[p.ID] = p
	}
	return r
}

func (r *PrivilegeRepo) List(_ context.Context) ([]domain.Privilege, error) {
	r.mu.Lock(); defer r.mu.Unlock()
	out := make([]domain.Privilege, 0, len(r.data))
	for _, p := range r.data { out = append(out, *p) }
	return out, nil
}
func (r *PrivilegeRepo) Get(_ context.Context, id string) (*domain.Privilege, error) {
	r.mu.Lock(); defer r.mu.Unlock()
	p, ok := r.data[id]
	if !ok { return nil, nil }
	cp := *p; return &cp, nil
}
func (r *PrivilegeRepo) GetByName(_ context.Context, name string) (*domain.Privilege, error) {
	r.mu.Lock(); defer r.mu.Unlock()
	for _, p := range r.data {
		if p.Name == name { cp := *p; return &cp, nil }
	}
	return nil, nil
}
func (r *PrivilegeRepo) Create(_ context.Context, p *domain.Privilege) error {
	r.mu.Lock(); defer r.mu.Unlock()
	if p.ID == "" { p.ID = fmt.Sprintf("priv-%d", len(r.data)+1) }
	cp := *p; r.data[p.ID] = &cp; return nil
}
func (r *PrivilegeRepo) Update(_ context.Context, p *domain.Privilege) error {
	r.mu.Lock(); defer r.mu.Unlock()
	if _, ok := r.data[p.ID]; !ok { return fmt.Errorf("privilege not found: %s", p.ID) }
	cp := *p; r.data[p.ID] = &cp; return nil
}
func (r *PrivilegeRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock(); defer r.mu.Unlock()
	delete(r.data, id); return nil
}
func (r *PrivilegeRepo) ListByRole(_ context.Context, _ string) ([]domain.Privilege, error) {
	return []domain.Privilege{}, nil
}
```

- [ ] **Step 3: Add SetPrivileges + ListPrivilegeIDsByRole to RoleRepo mock**

Find the `RoleRepo` mock in `mocks.go` and append these methods:

```go
func (r *RoleRepo) SetPrivileges(_ context.Context, roleID string, privilegeIDs []string) error {
	return nil
}

func (r *RoleRepo) ListPrivilegeIDsByRole(_ context.Context, roleID string) ([]string, error) {
	return []string{}, nil
}
```

- [ ] **Step 4: Build and run tests**

```bash
go build ./... && go test ./...
```

Expected: all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/testutil/mocks.go
git commit -m "feat(testutil): PrivilegeRepo mock + extend RoleRepo mock"
```

---

## Task 6: `PrivilegeHandler`

**Files:**
- Create: `internal/api/handlers/privileges.go`

- [ ] **Step 1: Create the handler**

```go
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

type PrivilegeHandler struct {
	repo     repository.PrivilegeRepo
	roleRepo repository.RoleRepo
}

func NewPrivilegeHandler(repo repository.PrivilegeRepo, roleRepo repository.RoleRepo) *PrivilegeHandler {
	return &PrivilegeHandler{repo: repo, roleRepo: roleRepo}
}

// List handles GET /service/rest/v1/security/privileges
func (h *PrivilegeHandler) List(c *gin.Context) {
	items, err := h.repo.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []domain.Privilege{}
	}
	c.JSON(http.StatusOK, items)
}

// Get handles GET /service/rest/v1/security/privileges/:id
func (h *PrivilegeHandler) Get(c *gin.Context) {
	p, err := h.repo.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "privilege not found"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// Create handles POST /service/rest/v1/security/privileges
func (h *PrivilegeHandler) Create(c *gin.Context) {
	var p domain.Privilege
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if p.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if p.Type == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type is required"})
		return
	}
	if err := h.repo.Create(c.Request.Context(), &p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, p)
}

// Update handles PUT /service/rest/v1/security/privileges/:id
func (h *PrivilegeHandler) Update(c *gin.Context) {
	var p domain.Privilege
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p.ID = c.Param("id")
	if err := h.repo.Update(c.Request.Context(), &p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, p)
}

// Delete handles DELETE /service/rest/v1/security/privileges/:id
func (h *PrivilegeHandler) Delete(c *gin.Context) {
	if err := h.repo.Delete(c.Request.Context(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// SetRolePrivileges handles PUT /service/rest/v1/security/roles/:id/privileges
// Body: {"privilegeIds": ["uuid1", "uuid2"]}
func (h *PrivilegeHandler) SetRolePrivileges(c *gin.Context) {
	roleID := c.Param("id")
	var req struct {
		PrivilegeIDs []string `json:"privilegeIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.roleRepo.SetPrivileges(c.Request.Context(), roleID, req.PrivilegeIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ListRolePrivileges handles GET /service/rest/v1/security/roles/:id/privileges
func (h *PrivilegeHandler) ListRolePrivileges(c *gin.Context) {
	items, err := h.repo.ListByRole(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []domain.Privilege{}
	}
	c.JSON(http.StatusOK, items)
}
```

- [ ] **Step 2: Build**

```bash
go build ./internal/api/handlers/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/api/handlers/privileges.go
git commit -m "feat(handlers): PrivilegeHandler CRUD + SetRolePrivileges"
```

---

## Task 7: Wire all new routes in `router.go`

**Files:**
- Modify: `internal/api/router.go`

- [ ] **Step 1: Add privilegeRepo + csRepo + selectorSvc instantiation**

In `router.go`, inside `NewRouter`, after the `webhookRepo` line:

```go
privilegeRepo := postgres.NewPrivilegeRepo(pool)
csRepo        := postgres.NewContentSelectorRepo(pool)
selectorSvc, err := service.NewContentSelectorService(csRepo)
if err != nil {
    panic("content selector service init: " + err.Error())
}
```

- [ ] **Step 2: Instantiate new handlers**

After `roleH := handlers.NewRoleHandler(roleRepo)` add:

```go
privH := handlers.NewPrivilegeHandler(privilegeRepo, roleRepo)
csH   := handlers.NewContentSelectorHandler(selectorSvc)
```

- [ ] **Step 3: Add authed read routes**

In the `authed` group, after the roles read route:

```go
// ── Privileges (read) ─────────────────────────────────────
authed.GET("/service/rest/v1/security/privileges", privH.List)
authed.GET("/service/rest/v1/security/privileges/:id", privH.Get)
authed.GET("/service/rest/v1/security/roles/:id/privileges", privH.ListRolePrivileges)

// ── Content Selectors (read) ──────────────────────────────
authed.GET("/service/rest/v1/security/content-selectors", csH.List)
authed.GET("/service/rest/v1/security/content-selectors/:id", csH.Get)
```

- [ ] **Step 4: Add admin write routes**

In the `admin` group, after the roles write routes:

```go
// ── Privileges (write) ────────────────────────────────────
admin.POST("/service/rest/v1/security/privileges", privH.Create)
admin.PUT("/service/rest/v1/security/privileges/:id", privH.Update)
admin.DELETE("/service/rest/v1/security/privileges/:id", privH.Delete)
admin.PUT("/service/rest/v1/security/roles/:id/privileges", privH.SetRolePrivileges)

// ── Content Selectors (write) ─────────────────────────────
admin.POST("/service/rest/v1/security/content-selectors", csH.Create)
admin.PUT("/service/rest/v1/security/content-selectors/:id", csH.Update)
admin.DELETE("/service/rest/v1/security/content-selectors/:id", csH.Delete)
admin.PUT("/service/rest/v1/security/privileges/:name/content-selector/:selectorId", csH.AttachToPrivilege)
admin.DELETE("/service/rest/v1/security/privileges/:name/content-selector", csH.DetachFromPrivilege)
```

- [ ] **Step 5: Build and test**

```bash
go build ./... && go test ./...
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/api/router.go
git commit -m "feat(router): wire content-selector + privilege routes"
```

---

## Task 8: Frontend API helpers in `client.ts`

**Files:**
- Modify: `frontend/src/api/client.ts`

- [ ] **Step 1: Add privilege + content-selector + role-privileges helpers to `nexusApi`**

In `frontend/src/api/client.ts`, inside the `nexusApi` object, add after the `setUserRoles` entry:

```ts
  // Role privileges
  listRolePrivileges: (roleId: string) =>
    apiClient.get(`/service/rest/v1/security/roles/${roleId}/privileges`),
  setRolePrivileges: (roleId: string, privilegeIds: string[]) =>
    apiClient.put(`/service/rest/v1/security/roles/${roleId}/privileges`, { privilegeIds }),
  updateRole: (id: string, data: { name: string; description?: string }) =>
    apiClient.put(`/service/rest/v1/security/roles/${id}`, data),

  // Privileges
  listPrivileges: () =>
    apiClient.get('/service/rest/v1/security/privileges'),
  createPrivilege: (data: unknown) =>
    apiClient.post('/service/rest/v1/security/privileges', data),
  updatePrivilege: (id: string, data: unknown) =>
    apiClient.put(`/service/rest/v1/security/privileges/${id}`, data),
  deletePrivilege: (id: string) =>
    apiClient.delete(`/service/rest/v1/security/privileges/${id}`),

  // Content selectors
  listContentSelectors: () =>
    apiClient.get('/service/rest/v1/security/content-selectors'),
  createContentSelector: (data: unknown) =>
    apiClient.post('/service/rest/v1/security/content-selectors', data),
  updateContentSelector: (id: string, data: unknown) =>
    apiClient.put(`/service/rest/v1/security/content-selectors/${id}`, data),
  deleteContentSelector: (id: string) =>
    apiClient.delete(`/service/rest/v1/security/content-selectors/${id}`),
  attachContentSelector: (privilegeName: string, selectorId: string) =>
    apiClient.put(`/service/rest/v1/security/privileges/${privilegeName}/content-selector/${selectorId}`),
  detachContentSelector: (privilegeName: string) =>
    apiClient.delete(`/service/rest/v1/security/privileges/${privilegeName}/content-selector`),
```

- [ ] **Step 2: Check TypeScript**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/api/client.ts
git commit -m "feat(frontend/api): privilege + content-selector + role-privilege helpers"
```

---

## Task 9: SecurityPage — Privileges tab

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx`

- [ ] **Step 1: Add Privilege types at the top of the types section**

After the `WebhookDef` type, add:

```tsx
interface Privilege {
  id: string
  name: string
  description: string
  type: 'wildcard' | 'repository-view' | 'repository-admin' | 'application' | 'script'
  attrs: Record<string, unknown>
  contentSelectorId?: string
  readOnly: boolean
}
```

- [ ] **Step 2: Add PrivilegesTab component**

Insert before the `/* ─── Main page ──────────────────────── */` comment:

```tsx
const PRIV_TYPES = ['wildcard', 'repository-view', 'repository-admin', 'application', 'script'] as const
type PrivType = typeof PRIV_TYPES[number]

const PRIV_TYPE_COLOR: Record<PrivType, string> = {
  'wildcard': '#3b82f6',
  'repository-view': '#22c55e',
  'repository-admin': '#f59e0b',
  'application': '#a78bfa',
  'script': '#f97316',
}

function PrivilegeAttrFields({ type, attrs, onChange }: {
  type: PrivType
  attrs: Record<string, unknown>
  onChange: (key: string, value: unknown) => void
}) {
  const inp = (key: string, placeholder: string) => (
    <input
      key={key}
      style={{ ...S.input, flex: 1 }}
      placeholder={placeholder}
      value={(attrs[key] as string) ?? ''}
      onChange={e => onChange(key, e.target.value)}
    />
  )
  if (type === 'wildcard') return inp('pattern', 'Pattern (e.g. nexus:*:read)')
  if (type === 'repository-view' || type === 'repository-admin') return (
    <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' as const }}>
      {inp('format', 'Format (e.g. maven2 or *)')}
      {inp('repository', 'Repository name or *')}
    </div>
  )
  if (type === 'application') return inp('domain', 'Domain (e.g. users)')
  if (type === 'script') return inp('name', 'Script name')
  return null
}

function PrivilegesTab() {
  const qc = useQueryClient()
  const { data: privs = [], isLoading } = useQuery<Privilege[]>({
    queryKey: ['privileges'],
    queryFn: () => nexusApi.listPrivileges().then(r => r.data),
  })
  const [showModal, setShowModal] = useState(false)
  const [editing, setEditing] = useState<Privilege | null>(null)
  const [form, setForm] = useState<{ name: string; description: string; type: PrivType; attrs: Record<string, unknown> }>({
    name: '', description: '', type: 'wildcard', attrs: {},
  })

  function openCreate() {
    setEditing(null)
    setForm({ name: '', description: '', type: 'wildcard', attrs: {} })
    setShowModal(true)
  }

  function openEdit(p: Privilege) {
    setEditing(p)
    setForm({ name: p.name, description: p.description, type: p.type, attrs: { ...p.attrs } })
    setShowModal(true)
  }

  const save = useMutation({
    mutationFn: async () => {
      const payload = { name: form.name, description: form.description, type: form.type, attrs: form.attrs }
      if (editing) return nexusApi.updatePrivilege(editing.id, payload)
      return nexusApi.createPrivilege(payload)
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['privileges'] }); setShowModal(false) },
  })

  const del = useMutation({
    mutationFn: (id: string) => nexusApi.deletePrivilege(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['privileges'] }),
  })

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <button style={S.btn('primary')} onClick={openCreate}><Plus size={14} /> New Privilege</button>
      </div>

      {isLoading ? <div style={S.empty}>Loading…</div> : privs.length === 0 ? <div style={S.empty}>No privileges</div> : (
        <div style={S.card}>
          <table style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
            <thead>
              <tr style={{ color: 'rgba(229,231,235,0.5)', textAlign: 'left' as const }}>
                <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Name</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Type</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Description</th>
                <th style={{ padding: '0 0 10px', fontWeight: 600, width: 80 }}></th>
              </tr>
            </thead>
            <tbody>
              {privs.map(p => (
                <tr key={p.id} style={{ borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                  <td style={{ padding: '9px 0', color: '#dbeafe', fontWeight: 600 }}>{p.name}</td>
                  <td style={{ padding: '9px 8px' }}>
                    <span style={S.badge(PRIV_TYPE_COLOR[p.type] ?? '#6b7280')}>{p.type}</span>
                    {p.readOnly && <span style={{ ...S.badge('#6b7280'), marginLeft: 4 }}>built-in</span>}
                  </td>
                  <td style={{ padding: '9px 8px', color: 'rgba(229,231,235,0.55)' }}>{p.description || '—'}</td>
                  <td style={{ padding: '9px 0', textAlign: 'right' as const, display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                    {!p.readOnly && (
                      <>
                        <button style={{ ...S.btn('ghost'), padding: '4px 8px' }} onClick={() => openEdit(p)}>Edit</button>
                        <button style={{ ...S.btn('danger'), padding: '4px 8px' }} onClick={() => { if (confirm(`Delete ${p.name}?`)) del.mutate(p.id) }}><Trash2 size={13} /></button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showModal && (
        <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
          <div style={{ background: '#0f172a', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 14, padding: 24, width: 480, display: 'flex', flexDirection: 'column', gap: 12 }}>
            <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: '#dbeafe' }}>{editing ? 'Edit Privilege' : 'New Privilege'}</h3>
            <input style={S.input} placeholder="Name" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
            <input style={S.input} placeholder="Description" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />
            <select
              style={{ ...S.input }}
              value={form.type}
              onChange={e => setForm(f => ({ ...f, type: e.target.value as PrivType, attrs: {} }))}
            >
              {PRIV_TYPES.map(t => <option key={t} value={t}>{t}</option>)}
            </select>
            <PrivilegeAttrFields
              type={form.type}
              attrs={form.attrs}
              onChange={(k, v) => setForm(f => ({ ...f, attrs: { ...f.attrs, [k]: v } }))}
            />
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 4 }}>
              <button style={S.btn('ghost')} onClick={() => setShowModal(false)}>Cancel</button>
              <button style={S.btn('primary')} onClick={() => save.mutate()} disabled={save.isPending || !form.name.trim()}>
                {save.isPending ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 3: TypeScript check**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx
git commit -m "feat(security): PrivilegesTab with typed create/edit modal"
```

---

## Task 10: SecurityPage — Content Selectors tab

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx`

- [ ] **Step 1: Add ContentSelectorsTab component**

Insert before the `PrivilegesTab` function (after the `WebhooksTab` function):

```tsx
function ContentSelectorsTab() {
  const qc = useQueryClient()
  const { data: selectors = [], isLoading } = useQuery<{ id: string; name: string; description: string; expression: string }[]>({
    queryKey: ['content-selectors'],
    queryFn: () => nexusApi.listContentSelectors().then(r => r.data),
  })
  const [showModal, setShowModal] = useState(false)
  const [editing, setEditing] = useState<{ id: string; name: string; description: string; expression: string } | null>(null)
  const [form, setForm] = useState({ name: '', description: '', expression: '' })
  const [saveError, setSaveError] = useState('')

  function openCreate() {
    setEditing(null)
    setForm({ name: '', description: '', expression: 'format == "maven2"' })
    setSaveError('')
    setShowModal(true)
  }

  function openEdit(s: { id: string; name: string; description: string; expression: string }) {
    setEditing(s)
    setForm({ name: s.name, description: s.description, expression: s.expression })
    setSaveError('')
    setShowModal(true)
  }

  const save = useMutation({
    mutationFn: async () => {
      if (editing) return nexusApi.updateContentSelector(editing.id, form)
      return nexusApi.createContentSelector(form)
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['content-selectors'] }); setShowModal(false) },
    onError: (e: unknown) => {
      let msg = 'Error'
      if (axios.isAxiosError(e)) {
        const d = e.response?.data
        if (typeof d === 'object' && d !== null && 'error' in d) msg = String((d as { error: unknown }).error)
      } else if (e instanceof Error) { msg = e.message }
      setSaveError(msg)
    },
  })

  const del = useMutation({
    mutationFn: (id: string) => nexusApi.deleteContentSelector(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['content-selectors'] }),
  })

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <button style={S.btn('primary')} onClick={openCreate}><Plus size={14} /> New Selector</button>
      </div>

      {isLoading ? <div style={S.empty}>Loading…</div> : selectors.length === 0 ? <div style={S.empty}>No content selectors</div> : (
        <div style={S.card}>
          <table style={{ width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 }}>
            <thead>
              <tr style={{ color: 'rgba(229,231,235,0.5)', textAlign: 'left' as const }}>
                <th style={{ padding: '0 0 10px', fontWeight: 600 }}>Name</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Expression</th>
                <th style={{ padding: '0 8px 10px', fontWeight: 600 }}>Description</th>
                <th style={{ padding: '0 0 10px', width: 80 }}></th>
              </tr>
            </thead>
            <tbody>
              {selectors.map(s => (
                <tr key={s.id} style={{ borderTop: '1px solid rgba(255,255,255,0.05)' }}>
                  <td style={{ padding: '9px 0', color: '#dbeafe', fontWeight: 600 }}>{s.name}</td>
                  <td style={{ padding: '9px 8px' }}>
                    <code style={{ ...S.mono, fontSize: 12, color: '#a5b4fc' }}>{s.expression}</code>
                  </td>
                  <td style={{ padding: '9px 8px', color: 'rgba(229,231,235,0.55)' }}>{s.description || '—'}</td>
                  <td style={{ padding: '9px 0', display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
                    <button style={{ ...S.btn('ghost'), padding: '4px 8px' }} onClick={() => openEdit(s)}>Edit</button>
                    <button style={{ ...S.btn('danger'), padding: '4px 8px' }} onClick={() => { if (confirm(`Delete ${s.name}?`)) del.mutate(s.id) }}><Trash2 size={13} /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showModal && (
        <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
          <div style={{ background: '#0f172a', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 14, padding: 24, width: 520, display: 'flex', flexDirection: 'column', gap: 12 }}>
            <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: '#dbeafe' }}>{editing ? 'Edit Content Selector' : 'New Content Selector'}</h3>
            <input style={S.input} placeholder="Name" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
            <input style={S.input} placeholder="Description (optional)" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />
            <div>
              <div style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', marginBottom: 4 }}>CEL Expression — variables: <code>format</code>, <code>path</code>, <code>repository</code></div>
              <textarea
                style={{ ...S.input, fontFamily: 'monospace', fontSize: 12, height: 80, resize: 'vertical' as const }}
                placeholder='format == "maven2" && path.startsWith("/com/acme")'
                value={form.expression}
                onChange={e => setForm(f => ({ ...f, expression: e.target.value }))}
              />
            </div>
            {saveError && (
              <div style={{ padding: '8px 12px', background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)', borderRadius: 8, color: '#ef4444', fontSize: 12 }}>{saveError}</div>
            )}
            <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
              <button style={S.btn('ghost')} onClick={() => setShowModal(false)}>Cancel</button>
              <button style={S.btn('primary')} onClick={() => save.mutate()} disabled={save.isPending || !form.name.trim() || !form.expression.trim()}>
                {save.isPending ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: TypeScript check**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx
git commit -m "feat(security): ContentSelectorsTab with CEL create/edit"
```

---

## Task 11: SecurityPage — RolesTab upgrade with edit modal + wire new tabs

**Files:**
- Modify: `frontend/src/pages/SecurityPage.tsx`

- [ ] **Step 1: Replace RolesTab with edit-capable version**

Replace the existing `RolesTab` function entirely:

```tsx
function RolesTab({ roles, loading, onRefresh }: { roles: Role[]; loading: boolean; onRefresh: () => void }) {
  const qc = useQueryClient()
  const [editRole, setEditRole] = useState<Role | null>(null)
  const [form, setForm] = useState({ name: '', description: '' })
  const [allPrivs, setAllPrivs] = useState<Privilege[]>([])
  const [selectedPrivIds, setSelectedPrivIds] = useState<string[]>([])
  const [loadingPrivs, setLoadingPrivs] = useState(false)

  async function openEdit(r: Role) {
    setEditRole(r)
    setForm({ name: r.name, description: r.description })
    setLoadingPrivs(true)
    try {
      const [privList, rolePrivs] = await Promise.all([
        nexusApi.listPrivileges().then(res => res.data as Privilege[]),
        nexusApi.listRolePrivileges(r.id).then(res => res.data as Privilege[]),
      ])
      setAllPrivs(privList)
      setSelectedPrivIds(rolePrivs.map(p => p.id))
    } finally { setLoadingPrivs(false) }
  }

  const save = useMutation({
    mutationFn: async () => {
      if (!editRole) return
      await nexusApi.updateRole(editRole.id, form)
      await nexusApi.setRolePrivileges(editRole.id, selectedPrivIds)
    },
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['roles'] }); onRefresh(); setEditRole(null) },
  })

  const del = useMutation({
    mutationFn: (id: string) => nexusApi.deleteRole(id),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['roles'] }); onRefresh() },
  })

  if (loading) return <div style={S.empty}>Loading…</div>
  if (!roles.length) return <div style={S.empty}>No roles found</div>

  return (
    <>
      <div style={S.grid}>
        {roles.map(r => (
          <div key={r.id} style={S.card}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
              <Shield size={15} style={{ color: '#3b82f6' }} />
              <span style={{ fontSize: 14, fontWeight: 600, color: '#dbeafe', flex: 1 }}>{r.name}</span>
              {r.readOnly && <span style={S.badge('#6b7280')}>built-in</span>}
              {!r.readOnly && (
                <button style={{ ...S.btn('ghost'), padding: '3px 8px', fontSize: 12 }} onClick={() => openEdit(r)}>Edit</button>
              )}
            </div>
            {r.description && <p style={{ fontSize: 12, color: 'rgba(229,231,235,0.5)', margin: '0 0 8px' }}>{r.description}</p>}
            <div style={{ display: 'flex', flexWrap: 'wrap' as const, gap: 4 }}>
              {(r.privileges ?? []).slice(0, 6).map(p => (
                <span key={p} style={{ fontSize: 10, padding: '2px 6px', borderRadius: 4, background: 'rgba(99,102,241,0.12)', color: '#a5b4fc', fontFamily: 'monospace' }}>{p}</span>
              ))}
              {(r.privileges ?? []).length > 6 && <span style={{ fontSize: 10, color: 'rgba(229,231,235,0.4)' }}>+{(r.privileges ?? []).length - 6}</span>}
            </div>
          </div>
        ))}
      </div>

      {editRole && (
        <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 }}>
          <div style={{ background: '#0f172a', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 14, padding: 24, width: 520, maxHeight: '80vh', overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 12 }}>
            <h3 style={{ margin: 0, fontSize: 16, fontWeight: 700, color: '#dbeafe' }}>Edit Role: {editRole.name}</h3>
            <input style={S.input} placeholder="Name" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} />
            <input style={S.input} placeholder="Description" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />

            <div style={{ fontSize: 13, fontWeight: 600, color: 'rgba(229,231,235,0.7)', marginTop: 4 }}>Privileges</div>
            {loadingPrivs ? <div style={S.empty}>Loading privileges…</div> : (
              <div style={{ maxHeight: 220, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 4 }}>
                {allPrivs.map(p => (
                  <label key={p.id} style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', padding: '4px 0' }}>
                    <input
                      type="checkbox"
                      checked={selectedPrivIds.includes(p.id)}
                      onChange={e => setSelectedPrivIds(prev =>
                        e.target.checked ? [...prev, p.id] : prev.filter(id => id !== p.id)
                      )}
                    />
                    <span style={{ fontSize: 13, color: '#dbeafe' }}>{p.name}</span>
                    <span style={S.badge(PRIV_TYPE_COLOR[p.type as PrivType] ?? '#6b7280')}>{p.type}</span>
                  </label>
                ))}
              </div>
            )}

            <div style={{ display: 'flex', gap: 8, justifyContent: 'space-between', marginTop: 4 }}>
              <button style={S.btn('danger')} onClick={() => { if (confirm(`Delete role ${editRole.name}?`)) { del.mutate(editRole.id); setEditRole(null) } }}>Delete</button>
              <div style={{ display: 'flex', gap: 8 }}>
                <button style={S.btn('ghost')} onClick={() => setEditRole(null)}>Cancel</button>
                <button style={S.btn('primary')} onClick={() => save.mutate()} disabled={save.isPending || !form.name.trim()}>
                  {save.isPending ? 'Saving…' : 'Save'}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </>
  )
}
```

- [ ] **Step 2: Update Tab type and tab bar**

Replace the `Tab` type definition and update `SecurityPage`:

```tsx
type Tab = 'roles' | 'scan' | 'tokens' | 'webhooks' | 'privileges' | 'selectors'
```

In the `SecurityPage` component, replace the tabs bar:

```tsx
<div style={S.tabs}>
  {([
    ['roles', 'Roles'],
    ['privileges', 'Privileges'],
    ['selectors', 'Content Selectors'],
    ['scan', 'CVE Scan'],
    ['tokens', 'API Tokens'],
    ['webhooks', 'Webhooks'],
  ] as [Tab, string][]).map(([id, label]) => (
    <button key={id} style={S.tab(tab === id)} onClick={() => setTab(id)}>{label}</button>
  ))}
</div>
```

Add new tab renders after the existing ones:

```tsx
{tab === 'roles'      && <RolesTab roles={roles} loading={isLoading} onRefresh={refetch} />}
{tab === 'privileges' && <PrivilegesTab />}
{tab === 'selectors'  && <ContentSelectorsTab />}
{tab === 'scan'       && <ScanTab />}
{tab === 'tokens'     && <TokensTab />}
{tab === 'webhooks'   && <WebhooksTab />}
```

Also update `RolesTab` call signature (add `onRefresh={refetch}`) and fix the existing render line to remove the old `RolesTab` render.

- [ ] **Step 3: TypeScript check**

```bash
cd frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors.

- [ ] **Step 4: Build**

```bash
cd frontend && npm run build 2>&1 | tail -10
```

Expected: ✓ built successfully.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/pages/SecurityPage.tsx
git commit -m "feat(security): RolesTab edit modal, Privileges tab, Content Selectors tab"
```

---

## Task 12: Final verification

- [ ] **Step 1: Backend full build + tests**

```bash
go build ./... && go test ./...
```

Expected: all pass.

- [ ] **Step 2: Frontend type check + build**

```bash
cd frontend && npx tsc --noEmit && npm run build
```

Expected: 0 TS errors, build succeeds.

- [ ] **Step 3: Update task_plan.md**

In `task_plan.md`, change Phase 8 status from `pending` to `in_progress` (will be set to `complete` after QA):

Mark all Phase 8 tasks as complete:
```
- [x] Content Selectors CRUD — routes wired, frontend tab
- [x] Privileges management — PrivilegeRepo + handler + typed forms frontend
- [x] Role → privilege assignment — RolesTab modal + SetPrivileges endpoint
- [x] Content selector testing with real CEL expressions — expression validated server-side on create/update
```

Update status line:
```
**Status:** complete
```

- [ ] **Step 4: Update progress.md**

Add a new session entry at the top of `progress.md`:

```markdown
## Session: April 20, 2026 — Phase 8 Security Management (complete)

### Done
- [x] `domain.Privilege` type with typed attrs
- [x] `PrivilegeRepo` interface + `postgres/privilege_repo.go`
- [x] `RoleRepo.SetPrivileges` + `ListPrivilegeIDsByRole` in interface + postgres
- [x] `PrivilegeHandler` — List/Get/Create/Update/Delete/SetRolePrivileges/ListRolePrivileges
- [x] `ContentSelectorHandler` routes wired in `router.go`
- [x] `PrivilegeRepo` mock in `testutil/mocks.go`; `RoleRepo` mock extended
- [x] Frontend `client.ts` — privilege + content-selector + role-privilege API helpers
- [x] `SecurityPage` — Privileges tab with typed create/edit modal
- [x] `SecurityPage` — Content Selectors tab with CEL expression editor (error shown on bad CEL)
- [x] `SecurityPage` — RolesTab upgraded: Edit button on non-builtin roles, modal with name/description/privilege checkboxes, Delete in modal
- [x] `go build ./...` + `go test ./...` — ✓ clean
- [x] `npx tsc --noEmit` + `npm run build` — ✓ 0 errors
```

- [ ] **Step 5: Commit docs**

```bash
git add task_plan.md progress.md
git commit -m "docs: Phase 8 complete — security management"
```
