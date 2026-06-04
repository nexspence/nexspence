# Track B — Phase 2: `internal/api/handlers` ≥80% Coverage — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring `internal/api/handlers` from ~26% to ≥80% line coverage with table-driven HTTP handler tests (httptest + Gin + the existing in-memory mocks), fixing any real bugs the tests surface along the way.

**Architecture:** Each handler is tested in isolation as an external test package (`package handlers_test`). A per-handler `mount…(t)` helper wires the handler's `New…Handler(...)` constructor to fresh `testutil` mock repos and mounts its routes on a bare `gin.New()` engine. Tests drive `httptest.NewRequest`/`Recorder` through `r.ServeHTTP` and assert status code, JSON body, and headers. Auth/RBAC is NOT exercised here (it lives in router middleware, covered in a later api-layer phase) — handlers that self-read `c.Get("roles")`/`c.Get("userID")` get those values injected via a tiny test middleware. This is Phase 2 of Track B; storage, service, formats, and the global per-package ≥80% CI gate are Phases 3–5.

**Tech Stack:** Go 1.26.3, Gin, `net/http/httptest`, `github.com/stretchr/testify` (assert/require — already a dependency, used by existing handler tests), `internal/testutil` in-memory mocks, `internal/domain` types.

**Branch:** Create and work on `track-b2-handlers-coverage` (worktree), branched from `main` (Track B Phase 1 is already merged). Do NOT commit to `main` directly.

**Reused project facts (verified 2026-06-04):**
- The default unit suite already passes (474 tests) and is Docker-free; these handler tests are plain unit tests (no build tag, no Docker).
- `internal/testutil/mocks.go` provides in-memory mock implementations of every repository interface, constructed as `testutil.NewAuditRepo()`, `testutil.NewUserRepo()`, `testutil.NewRepoRepo()`, `testutil.NewRoleRepo()`, `testutil.NewPrivilegeRepo()`, `testutil.NewComponentRepo()`, `testutil.NewAssetRepo()`, `testutil.NewBlobStoreRepo()`, `testutil.NewBlobStoreMigrationRepo()`, `testutil.NewCleanupPolicyRepo()`, `testutil.NewContentSelectorRepo()`, `testutil.NewRoutingRuleRepo()`, `testutil.NewPromotionRepo()`, `testutil.NewReplicationRepo()`, `testutil.NewScanResultRepo()`, `testutil.NewUserTokenRepo()`, `testutil.NewWebhookRepo()`, `testutil.NewBlobStore()`. Read `internal/testutil/mocks.go` for the exact constructor + seam (most expose the backing slice/map or `Write/Create` so tests can seed and can force errors). **The constructors are variadic** — `testutil.NewUserRepo()` builds an empty mock, `testutil.NewUserRepo(u1, u2)` builds one pre-seeded with those rows; you can also seed after construction via the mock's `Create`/`Write` (the handler holds a pointer to the same mock, so post-mount seeding is visible).
- Established handler-test pattern lives in `internal/api/handlers/audit_test.go`, `auth_test.go`, `access_graph_test.go`, `oidc_logout_test.go` — READ `audit_test.go` first; it is the canonical reference for this plan.
- Auth-context injection (only for handlers that self-read context): `r.Use(func(c *gin.Context){ c.Set("userID", "user-1"); c.Set("username", "admin"); c.Set("roles", []string{"nx-admin"}) })` mounted before the route. Admin self-gating uses `hasRole(roles, "nx-admin")`; inject `[]string{"nx-admin"}` for the admin branch and a non-admin slice (or omit) for the 403 branch.
- Handlers read request context via `c.Request.Context()`; a default `httptest.NewRequest` carries `context.Background()`, which is fine.
- The `rtk` hook collapses `go test` stdout — rely on exit codes; read coverage from a profile with `go tool cover -func`; use `rtk proxy go test …` when you need full failure output.
- Handlers without a dedicated `_test.go` today (the work list): `audit_jsonline.go`, `backup.go`, `blob_store_migration.go`, `blobstores.go`, `browse_docker.go`, `browse_raw.go`, `cleanup.go`, `components.go`, `errors.go` (helpers — covered transitively), `ldap.go`, `migration.go`, `privileges.go`, `promotion.go`, `replication.go`, `repositories.go`, `repository_expand.go`, `roles.go`, `routing_rules.go`, `scan.go`, `system.go`, `users.go`.

**The uniform per-method coverage target** (apply to every exported handler method): exercise the **success path** plus **each early-return branch** the method actually contains — typically: malformed JSON body → 400; failed validation (e.g. empty required field) → 400; missing path/query param or not-found dependency → 404; underlying repo/service error → 500; and any auth/role self-gate → 403. Only assert branches that exist in the method (READ the handler first). Force the repo-error branch by configuring the corresponding mock to return an error (see the mock's error seam in `mocks.go`).

---

## Pre-flight

- [ ] **Step 0: Branch**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core
git checkout main && git pull --ff-only 2>/dev/null; git checkout -b track-b2-handlers-coverage
git status   # clean on the new branch
export PATH="$(go env GOPATH)/bin:$PATH"
go test -count=1 ./internal/api/handlers/ -coverprofile=/tmp/h0.out >/dev/null 2>&1
go tool cover -func=/tmp/h0.out | tail -1   # baseline ~26%
```

---

## Task 1 (EXEMPLAR — full code): `RoleHandler` tests

This task is the worked reference. Every later task follows its shape: a `mount…` helper, success + every error branch per method, seeding through the mock, forcing the repo-error branch through the mock.

**Files:**
- Read: `internal/api/handlers/roles.go`, `internal/testutil/mocks.go` (the `RoleRepo`/`UserRepo` mocks)
- Create: `internal/api/handlers/roles_test.go`

`RoleHandler` (from `roles.go`) — constructor `NewRoleHandler(roles repository.RoleRepo, users repository.UserRepo)`; methods/routes: `List` `GET /service/rest/v1/security/roles`; `Create` `POST …/roles`; `Update` `PUT …/roles/:id`; `Delete` `DELETE …/roles/:id`; `SetUserRoles` `PUT …/security/users/:userId/roles` (body `{"roleIds":[…]}`, looks the user up by username via `users.Get`, 404 if absent). No in-handler auth gate.

- [ ] **Step 1: Write the test file**

```go
package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func mountRoles(t *testing.T) (*gin.Engine, *testutil.RoleRepo, *testutil.UserRepo) {
	t.Helper()
	roles := testutil.NewRoleRepo()
	users := testutil.NewUserRepo()
	h := handlers.NewRoleHandler(roles, users)
	r := gin.New()
	r.GET("/service/rest/v1/security/roles", h.List)
	r.POST("/service/rest/v1/security/roles", h.Create)
	r.PUT("/service/rest/v1/security/roles/:id", h.Update)
	r.DELETE("/service/rest/v1/security/roles/:id", h.Delete)
	r.PUT("/service/rest/v1/security/users/:userId/roles", h.SetUserRoles)
	return r, roles, users
}

// do is a tiny request helper: marshals body (if any), runs it, returns the recorder.
func do(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRoleHandler_List_Empty(t *testing.T) {
	r, _, _ := mountRoles(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/roles", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []domain.Role
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got) // nil normalized to []
}

func TestRoleHandler_Create_Then_List(t *testing.T) {
	r, _, _ := mountRoles(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/roles",
		map[string]any{"name": "dev", "description": "developers"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var created domain.Role
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Equal(t, "dev", created.Name)
	assert.Equal(t, "default", created.Source)

	rec = do(t, r, http.MethodGet, "/service/rest/v1/security/roles", nil)
	var got []domain.Role
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 1)
}

func TestRoleHandler_Create_BadJSON_400(t *testing.T) {
	r, _, _ := mountRoles(t)
	req := httptest.NewRequest(http.MethodPost, "/service/rest/v1/security/roles",
		bytes.NewReader([]byte(`{not json`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRoleHandler_Create_EmptyName_400(t *testing.T) {
	r, _, _ := mountRoles(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/roles",
		map[string]any{"description": "no name"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRoleHandler_Update(t *testing.T) {
	r, roles, _ := mountRoles(t)
	ro := &domain.Role{Name: "ops"}
	require.NoError(t, roles.Create(testContext(), ro))
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/roles/"+ro.ID,
		map[string]any{"name": "ops2", "description": "renamed"})
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRoleHandler_Delete(t *testing.T) {
	r, roles, _ := mountRoles(t)
	ro := &domain.Role{Name: "temp"}
	require.NoError(t, roles.Create(testContext(), ro))
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/security/roles/"+ro.ID, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestRoleHandler_SetUserRoles_UserNotFound_404(t *testing.T) {
	r, _, _ := mountRoles(t)
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/users/ghost/roles",
		map[string]any{"roleIds": []string{"r1"}})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRoleHandler_SetUserRoles_OK(t *testing.T) {
	r, _, users := mountRoles(t)
	u := &domain.User{Username: "alice", Email: "alice@test.com"}
	require.NoError(t, users.Create(testContext(), u))
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/users/alice/roles",
		map[string]any{"roleIds": []string{"r1", "r2"}})
	assert.Equal(t, http.StatusNoContent, rec.Code)
}
```

- [ ] **Step 2: Add the shared `testContext` + gin-mode helper (once per package)**

Create `internal/api/handlers/helpers_test.go` (shared by every test file in this task group — define it ONCE; later tasks reuse `do`, `testContext`, and the gin mode init from here):

```go
package handlers_test

import (
	"context"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func testContext() context.Context { return context.Background() }
```

> NOTE: `audit_test.go` already declares `func init() { gin.SetMode(gin.TestMode) }`. Go allows multiple `init` funcs in a package, so this is safe. If a duplicate-symbol error appears for `do`/`testContext`/`seed`, it means a helper of that name already exists in another `_test.go` in the package — rename yours or reuse the existing one. Before adding a helper, `grep -rn 'func do\|func testContext\|func seed' internal/api/handlers/*_test.go`.

- [ ] **Step 3: Run the exemplar**

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
go test -count=1 -run 'TestRoleHandler' ./internal/api/handlers/ ; echo "exit=$?"   # expect 0
```

If a test fails because the real handler behaves differently than asserted, READ the handler and decide: align the test to real behavior, OR — if the handler is genuinely wrong — fix it surgically and note it in `NEXT_RELEASE.md` (handler/service tests are expected to surface real bugs).

- [ ] **Step 4: Commit**

```bash
git add internal/api/handlers/roles_test.go internal/api/handlers/helpers_test.go
git commit -m "test(handlers): RoleHandler tests + shared test helpers"
```

---

## Tasks 2–11: remaining handlers (follow Task 1's pattern)

> **Concrete instruction for every task below:** create `internal/api/handlers/<name>_test.go`, `package handlers_test`. For each handler: READ its `.go` file to learn the constructor `New…Handler(...)` arg list, every exported method, its route + params + request body shape, and its exact early-return branches. Write a `mount<Name>(t)` helper that builds the handler from `testutil` mocks and mounts its routes; reuse `do`/`testContext` from `helpers_test.go`. Then write tests hitting the **uniform per-method coverage target** (success + every branch that exists). Seed state through the mock's `Create`/`Write`; force the 500 branch through the mock's error seam. Inject auth context (`c.Set("roles", …)` etc.) ONLY for handlers that self-read it. End each task:
> ```bash
> go test -count=1 -run '<HandlerName>' ./internal/api/handlers/ ; echo "exit=$?"   # expect 0
> git add internal/api/handlers/<name>_test.go && git commit -m "test(handlers): <HandlerName> tests"
> ```

- [ ] **Task 2 — Privileges + RoutingRules** (`privileges.go`, `routing_rules.go`). Mocks: `PrivilegeRepo`, `ContentSelectorRepo`, `RoutingRuleRepo`. Cover privilege CRUD (content-selector type validation), routing-rule CRUD + ordering, bad-JSON/validation/not-found/repo-error branches.
- [ ] **Task 3 — Repositories** (`repositories.go`, `repository_expand.go`). Mocks: `RepoRepo`, `BlobStoreRepo`, `CleanupPolicyRepo` (+ whatever the constructor takes — read it). Cover list/get/create/update/delete, format/type validation, blob-store-not-found → 400/404, group member validation, and the group-expansion helper. This is the largest handler — expect the most tests.
- [ ] **Task 4 — Components** (`components.go`). Mocks: `ComponentRepo`, `RepoRepo`, `AssetRepo`. Cover list-by-repo (incl. group expansion), get, delete, `SetTags`, search params, not-found/repo-error.
- [ ] **Task 5 — Users** (`users.go`). Mocks: `UserRepo`, `RoleRepo`. Cover list/get/create/update/delete, password handling presence (do not assert hash internals — assert behavior), role assignment, duplicate/validation/not-found branches.
- [ ] **Task 6 — Browse** (`browse_docker.go`, `browse_raw.go`). Mocks: `AssetRepo`, `ComponentRepo`, `RepoRepo`. Cover docker-tree + raw-tree row building, repo-not-found, empty-tree, query params.
- [ ] **Task 7 — Cleanup + Scan** (`cleanup.go`, `scan.go`). Mocks: `CleanupPolicyRepo`, `RepoRepo`, `ComponentRepo`, `ScanResultRepo`. Cover policy CRUD + run-now, retain-N field, scan summary/list/bulk endpoints, validation/not-found/error.
- [ ] **Task 8 — Promotion + Replication** (`promotion.go`, `replication.go`). Mocks: `PromotionRepo`, `ReplicationRepo`, `RepoRepo`, `ComponentRepo`. Cover rule CRUD, request queue + approve/reject transitions, replication run/test-connection/history, validation/not-found/error.
- [ ] **Task 9 — BlobStores + BlobStoreMigration** (`blobstores.go`, `blob_store_migration.go`). Mocks: `BlobStoreRepo`, `BlobStoreMigrationRepo`, `RepoRepo`. Cover blob-store CRUD + test-connection probe, migration start/status/cancel, quota validation, not-found/error.
- [ ] **Task 10 — System + Backup + Audit NDJSON** (`system.go`, `backup.go`, `audit_jsonline.go`). Cover system info/services endpoints, backup export/import wiring (assert headers/streaming shape, not full archive bytes), audit NDJSON streaming (mirror the existing `audit_test.go` NDJSON assertions). Read constructors for the exact mock/service args; where a handler depends on a concrete service rather than a repo interface, construct the service over mock repos (follow how `router.go` wires it).
- [ ] **Task 11 — Migration + LDAP** (`migration.go`, `ldap.go`). Cover migration job list/create/history (mock the migration repo/service), LDAP test-connection/config endpoints. For LDAP, assert the request/validation/response shape without a live LDAP server (the network call path may be unreachable in unit tests — cover the reachable branches and `log()`/note what is not, per spec "no silent caps").

> `errors.go` contains shared error-classification helpers (`isNotFound`, `isInvalidInput`, etc.) — these are exercised transitively by the handler tests above; no dedicated test file is required unless coverage shows a gap.

---

## Task 12: Verify the ≥80% floor for the handlers package + finalize

**Files:**
- Modify: `NEXT_RELEASE.md`

- [ ] **Step 1: Coverage run**

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
go test -count=1 -coverprofile=/tmp/h_full.out ./internal/api/handlers/ ; echo "exit=$?"
go tool cover -func=/tmp/h_full.out | tail -1   # expect total ≥ 80.0%
```

If below 80%, find the lowest-covered handlers and add the missing-branch tests:
```bash
go tool cover -func=/tmp/h_full.out | sort -t$'\t' -k3 -n | head -20
```

- [ ] **Step 2: Confirm nothing else regressed**

```bash
go test -count=1 ./... ; echo "unit=$?"     # all packages still green
make lint ; echo "lint=$?"                    # Track A gate still 0
```

Expected: both `=0`.

- [ ] **Step 3: Append to `NEXT_RELEASE.md`** under `### 🔧 Quality / Tooling`:

```markdown
- **API handler test coverage (Track B Phase 2)** — added table-driven HTTP tests (httptest + Gin + the in-memory `internal/testutil` mocks) for every REST handler, taking `internal/api/handlers` from ~26% to ≥80% line coverage. Tests cover success, validation (400), not-found (404), and repo-error (500) branches per endpoint. <If bugs were found, list each here with a one-line description.>
```

- [ ] **Step 4: Commit**

```bash
git add NEXT_RELEASE.md && git commit -m "docs: record handler test coverage (Track B Phase 2)"
```

---

## Self-Review checklist (before declaring Phase 2 done)

- [ ] `go test -count=1 ./internal/api/handlers/` passes
- [ ] `internal/api/handlers` total coverage ≥ 80% (`go tool cover -func`)
- [ ] Default `go test ./...` still green; `make lint` = 0
- [ ] Every new test file is `package handlers_test`; shared helpers (`do`, `testContext`) defined once, no duplicate-symbol errors
- [ ] Any real bug found is fixed surgically and recorded in `NEXT_RELEASE.md`
- [ ] No handler logic changed except where a test proved it wrong

---

## Track B roadmap — phases after this one

Each is its own plan (independently shippable, same dockertest/mocks infrastructure):

- **Phase 3 — `internal/storage` ≥80%** (`local.go`, `s3.go`, `factory.go`; `registry.go` already tested). LocalBlobStore store/fetch/delete + error paths against `t.TempDir()`; S3 adapter behind the `integration` tag against MinIO (or unit-test the URL/key logic that doesn't need a live bucket).
- **Phase 4 — `internal/service` ≥80%** (≈59% now; `rbac_service.go`, `repository_service.go`, `user_service.go` have no dedicated tests). Fill validation, error-mapping, and scheduler branches using the `testutil` mocks; reuse the BUG-discovery discipline.
- **Phase 5 — `internal/formats/*` to ≥80% each + the global per-package CI gate.** Fill per-format gaps (npm 54%, conda/terraform 57%, yum/maven/pypi/gomod/docker 62–66% are the priorities). Then add the Track B B3 deliverable: a `coverage` CI job that merges the unit + integration profiles and enforces **per-package ≥80%** with the documented exclusion list (`cmd/server`, `internal/db`, `internal/logger`, `internal/redisclient`, vendored/generated). Also fold in the deferred `internal/api` middleware/RBAC and `internal/audit`/`auth`/`metrics`/`config` gaps so the gate can go green across the board.
- **BUG-33** (the `TestAuditMiddleware` test-only race in `internal/api`) is addressed in the Phase 5 api-layer work.

Spec: `docs/superpowers/specs/2026-06-03-lint-and-coverage-design.md` (Track B, sections B2/B3).
