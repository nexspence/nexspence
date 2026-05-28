# Phase 16: Simplified Docker URLs (without /repository/ prefix) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose Docker v2 API at `/v2/:repoName/*` in addition to the existing `/v2/repository/:repoName/*`, so clients can reference images as `localhost:8081/myrepo/alpine:3.22` without the literal `repository` segment.

**Architecture:** Add a second route group `/v2/:repoName/*dockerpath` that uses an extracted shared handler (`serveDockerV2`). Gin's static-segment priority guarantees `/v2/repository/...` continues to match the original route first, so backward compatibility is preserved. The NoRoute fallback that currently rejects short `/v2/...` paths is updated to reflect the new capability.

**Tech Stack:** Go, Gin, existing `RBACMiddleware` + `OptionalAuth`, no new dependencies.

---

## File Map

| File | Change |
|------|--------|
| `internal/api/router.go` | Extract `serveDockerV2` helper; add `/v2/:repoName/*dockerpath` group; update NoRoute |
| `internal/api/handlers/rbac_middleware.go` | No change needed — already handles both `path` and `dockerpath` params |
| `internal/api/router_test.go` | Add tests for the new short-path routes |

---

## Task 1: Extract `serveDockerV2` shared handler

**Files:**
- Modify: `internal/api/router.go:389-445`

The existing inline closure for `v2docker.Any("/:repoName/*dockerpath", ...)` is ~50 lines. Extract it into a named function so both old and new routes share the same logic with zero duplication.

- [ ] **Step 1.1: Extract handler into a named function**

In `router.go`, before the `v2docker` group registration, add:

```go
// serveDockerV2 handles Docker OCI v2 API requests for both
// /v2/repository/:repoName/*dockerpath and /v2/:repoName/*dockerpath routes.
// pathParam is "dockerpath" for both routes.
func serveDockerV2(
    repoRepo repository.RepositoryRepo,
    groupH http.Handler,
    fmtRegistry map[string]formats.FormatHandler,
    pathParam string,
) gin.HandlerFunc {
    return func(c *gin.Context) {
        repoName := c.Param("repoName")
        dockerPath := c.Param(pathParam) // e.g. /alpine/manifests/3.22.1

        ctx := c.Request.Context()
        if uid, ok := c.Get("userID"); ok {
            if id, ok2 := uid.(string); ok2 && id != "" {
                uname, _ := c.Get("username")
                uStr, _ := uname.(string)
                ctx = requestctx.WithUser(ctx, id, uStr)
            }
        }
        c.Request = c.Request.WithContext(ctx)

        repoDef, err := repoRepo.Get(ctx, repoName)
        if err != nil || repoDef == nil {
            c.JSON(http.StatusNotFound, gin.H{"error": "repository not found: " + repoName})
            return
        }
        if !repoDef.Online {
            c.JSON(http.StatusServiceUnavailable, gin.H{"error": "repository is offline"})
            return
        }

        if repoDef.Type == domain.TypeGroup {
            if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
                c.JSON(http.StatusMethodNotAllowed, gin.H{
                    "error": "group repository is read-only — publish to a member hosted repository",
                })
                return
            }
            if string(repoDef.Format) != "docker" {
                c.JSON(http.StatusBadRequest, gin.H{"error": "repository is not a docker registry"})
                return
            }
            c.Params = gin.Params{
                {Key: "repoName", Value: repoName},
                {Key: "path", Value: "/v2" + dockerPath},
            }
            groupH.ServeHTTP(c)
            return
        }

        if string(repoDef.Format) != "docker" {
            c.JSON(http.StatusBadRequest, gin.H{"error": "repository is not a docker registry"})
            return
        }

        c.Params = gin.Params{
            {Key: "repoName", Value: repoName},
            {Key: "path", Value: "/v2" + dockerPath},
        }
        fmtRegistry["docker"].ServeHTTP(c)
    }
}
```

- [ ] **Step 1.2: Replace existing inline handler with the extracted function**

Replace the current `v2docker.Any(...)` block:

```go
// BEFORE:
v2docker := r.Group("/v2/repository", handlers.OptionalAuth(userSvc, tokenSvc), handlers.RBACMiddleware(rbacSvc, repoRepo))
v2docker.Any("/:repoName/*dockerpath", func(c *gin.Context) {
    // ... 50-line closure ...
})

// AFTER:
dockerHandler := serveDockerV2(repoRepo, groupHandler, formatRegistry, "dockerpath")
v2docker := r.Group("/v2/repository", handlers.OptionalAuth(userSvc, tokenSvc), handlers.RBACMiddleware(rbacSvc, repoRepo))
v2docker.Any("/:repoName/*dockerpath", dockerHandler)
```

- [ ] **Step 1.3: Verify build is clean**

```bash
go build ./...
```

Expected: no output (no errors).

- [ ] **Step 1.4: Run existing tests**

```bash
go test ./internal/api/... -v -count=1 2>&1 | tail -30
```

Expected: all existing tests pass.

- [ ] **Step 1.5: Commit**

```bash
git add internal/api/router.go
git commit -m "refactor(router): extract serveDockerV2 shared handler — no behavior change"
```

---

## Task 2: Register short-path `/v2/:repoName/*dockerpath` route

**Files:**
- Modify: `internal/api/router.go` — add new group; update NoRoute

- [ ] **Step 2.1: Add the short-path group immediately after `v2docker`**

After the `v2docker` block (around line 445), add:

```go
// ── Docker short-path: /v2/:repoName/* (no "repository/" segment) ──────────
// Gin static segments take priority, so /v2/repository/:repoName/... still
// matches the group above; this catches all other /v2/:repoName/... patterns.
v2short := r.Group("/v2", handlers.OptionalAuth(userSvc, tokenSvc), handlers.RBACMiddleware(rbacSvc, repoRepo))
v2short.Any("/:repoName/*dockerpath", dockerHandler)
```

Note: `dockerHandler` is the same variable defined in Task 1.

- [ ] **Step 2.2: Update the NoRoute `/v2/` special case**

The current NoRoute block (lines ~449–465) explicitly rejects short Docker paths because they weren't routed. Now they are routed, so the special-case JSON error is no longer needed. Replace only the `/v2/` guard — leave the rest of NoRoute (SPA fallback) intact:

```go
// BEFORE:
r.NoRoute(func(c *gin.Context) {
    p := c.Request.URL.Path
    if strings.HasPrefix(p, "/v2/") && p != "/v2/" && !strings.HasPrefix(p, "/v2/repository/") {
        c.Header("Content-Type", "application/json")
        c.JSON(http.StatusNotFound, gin.H{
            "errors": []gin.H{{
                "code":    "NAME_UNKNOWN",
                "message": "Nexspence Docker v2 API is only at /v2/repository/<repoName>/... ...",
            }},
        })
        return
    }
    ui(c)
})

// AFTER:
r.NoRoute(func(c *gin.Context) {
    ui(c)
})
```

The `strings` import may become unused — check and remove if so.

- [ ] **Step 2.3: Verify build**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 2.4: Manual smoke test**

Start the server:
```bash
go run ./cmd/server serve
```

In a second terminal — verify `/v2/` discovery still works:
```bash
curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/v2/
```
Expected: `200`

Verify a non-existent repo returns 404 JSON (not HTML):
```bash
curl -s http://localhost:8081/v2/no-such-repo/tags/list
```
Expected:
```json
{"error":"repository not found: no-such-repo"}
```

- [ ] **Step 2.5: Commit**

```bash
git add internal/api/router.go
git commit -m "feat(docker): add /v2/:repoName/* short-path alias — backward compat with /v2/repository/:repoName/*"
```

---

## Task 3: Tests for the new short-path routes

**Files:**
- Modify: `internal/api/router_test.go` (add new test cases alongside existing Docker tests)

If `router_test.go` does not yet have Docker v2 route tests, add a new `TestDockerShortPath` test function. If it does, add cases to the existing table.

- [ ] **Step 3.1: Write failing tests**

Add to `internal/api/router_test.go`:

```go
func TestDockerShortPath(t *testing.T) {
    // Shared repo definition used by all sub-tests.
    dockerRepo := &domain.Repository{
        ID:     "repo-1",
        Name:   "myrepo",
        Format: "docker",
        Type:   domain.TypeHosted,
        Online: true,
    }
    nonDockerRepo := &domain.Repository{
        ID:     "repo-2",
        Name:   "mavenrepo",
        Format: "maven2",
        Type:   domain.TypeHosted,
        Online: true,
    }

    tests := []struct {
        name       string
        method     string
        path       string
        mockRepo   *domain.Repository
        wantStatus int
    }{
        {
            name:       "short path GET tags/list returns 200 for docker repo",
            method:     http.MethodGet,
            path:       "/v2/myrepo/tags/list",
            mockRepo:   dockerRepo,
            wantStatus: http.StatusOK,
        },
        {
            name:       "short path returns 404 for unknown repo",
            method:     http.MethodGet,
            path:       "/v2/no-such-repo/tags/list",
            mockRepo:   nil,
            wantStatus: http.StatusNotFound,
        },
        {
            name:       "short path returns 400 for non-docker repo",
            method:     http.MethodGet,
            path:       "/v2/mavenrepo/tags/list",
            mockRepo:   nonDockerRepo,
            wantStatus: http.StatusBadRequest,
        },
        {
            name:       "long path /v2/repository/myrepo/tags/list still works",
            method:     http.MethodGet,
            path:       "/v2/repository/myrepo/tags/list",
            mockRepo:   dockerRepo,
            wantStatus: http.StatusOK,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Build a mock router wired with the test repo.
            r := setupTestRouter(t, tt.mockRepo)
            w := httptest.NewRecorder()
            req := httptest.NewRequest(tt.method, tt.path, nil)
            r.ServeHTTP(w, req)
            assert.Equal(t, tt.wantStatus, w.Code)
        })
    }
}
```

`setupTestRouter` must:
- Use `testutil.MockRepositoryRepo` (already in `internal/testutil/mocks.go`)
- Wire a minimal docker format handler (can be a stub that returns 200)
- Wire `RBACMiddleware` with an allow-all `RBACService` for these tests

If `setupTestRouter` already exists in the test file, adapt to its signature. If not, add:

```go
func setupTestRouter(t *testing.T, repo *domain.Repository) *gin.Engine {
    t.Helper()
    gin.SetMode(gin.TestMode)

    repoRepo := &testutil.MockRepositoryRepo{}
    if repo != nil {
        repoRepo.Repos = map[string]*domain.Repository{repo.Name: repo}
    }

    // Stub docker handler: always 200 for GET /v2/*/tags/list
    stubDocker := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    })

    cfg := &config.Config{HTTP: config.HTTPConfig{BaseURL: "http://localhost:8081"}}
    rbacSvc := service.NewRBACService(nil) // allow-all when no repo passed
    return buildRouterForTest(cfg, repoRepo, stubDocker, rbacSvc)
}
```

Adapt `buildRouterForTest` as needed to match your existing test helpers.

- [ ] **Step 3.2: Run tests — expect failures**

```bash
go test ./internal/api/... -run TestDockerShortPath -v
```

Expected: FAIL (new routes not wired yet — this step is intentionally run before commit in case router changes need revision based on test design).

- [ ] **Step 3.3: Run tests after Task 2's changes**

```bash
go test ./internal/api/... -run TestDockerShortPath -v
```

Expected:
```
--- PASS: TestDockerShortPath/short_path_GET_tags/list_returns_200_for_docker_repo
--- PASS: TestDockerShortPath/short_path_returns_404_for_unknown_repo
--- PASS: TestDockerShortPath/short_path_returns_400_for_non-docker_repo
--- PASS: TestDockerShortPath/long_path_/v2/repository/myrepo/tags/list_still_works
PASS
```

- [ ] **Step 3.4: Run full test suite**

```bash
go test ./... 2>&1 | tail -20
```

Expected: all tests pass (same count as before + new tests).

- [ ] **Step 3.5: Commit**

```bash
git add internal/api/router_test.go
git commit -m "test(docker): verify short-path /v2/:repoName/* routes and backward compat"
```

---

## Task 4: Update documentation and config comments

**Files:**
- Modify: `internal/api/router.go` — update comment block at `/v2/` section
- Modify: `README.md` (if it documents Docker image reference format)

- [ ] **Step 4.1: Update router comment**

Replace the comment block at line ~376:

```go
// BEFORE:
// ── Docker registry v2 API ────────────────────────────────
// Docker clients tag images as localhost:8081/repository/<repoName>/<image>:<tag>
// and send all API requests to /v2/repository/<repoName>/...
// The version check must be public (Docker does GET /v2/ before sending credentials).

// AFTER:
// ── Docker registry v2 API ────────────────────────────────
// Two URL styles are supported (both fully functional):
//   Long:  localhost:8081/repository/<repoName>/<image>:<tag>
//          API: /v2/repository/:repoName/*
//   Short: localhost:8081/<repoName>/<image>:<tag>
//          API: /v2/:repoName/*   (gin static-segment priority keeps "repository" unambiguous)
// GET /v2/ is public — Docker checks this before sending credentials.
```

- [ ] **Step 4.2: Check README.md for Docker image reference examples**

```bash
grep -n "docker\|/repository/\|image.*tag\|pull\|push" README.md | head -30
```

If examples show only the long form, add a note that the short form is also supported. If README.md has no Docker section, skip.

- [ ] **Step 4.3: Verify build + tests still clean**

```bash
go build ./... && go test ./... 2>&1 | tail -5
```

Expected: clean.

- [ ] **Step 4.4: Commit**

```bash
git add internal/api/router.go README.md
git commit -m "docs(docker): document both /v2/repository/:repo/* and /v2/:repo/* URL styles"
```

---

## Self-Review Checklist

### Spec Coverage

| IDEA-TODO requirement | Covered by |
|----------------------|-----------|
| `localhost:8081/docker/...` short path | Task 2 — `/v2/:repoName/*dockerpath` route |
| `localhost:8081/repository/docker/...` still works | Task 1 refactor preserves existing behavior; Task 3 tests verify |
| Group repo GET/HEAD delegated to members | `serveDockerV2` handles `TypeGroup` identically to current code |
| Group repo PUT/POST/PATCH/DELETE → 405 | `serveDockerV2` returns `MethodNotAllowed` for non-GET/HEAD on groups |
| Non-docker repo → 400 | `serveDockerV2` format check |
| Unknown repo → 404 | `serveDockerV2` repo lookup |
| `/v2/` discovery still works | Existing `r.GET("/v2/", ...)` unchanged |
| RBAC enforced on new route | `RBACMiddleware` is group middleware on `v2short`; already handles `dockerpath` param |

### Out of scope (deferred to a future RFC)

The IDEA-TODO mentions "Group routing: distinguish proxy-path (no namespace slash) vs hosted namespace-path". This discrimination is **not implemented in Phase 16** because:
1. The existing group handler fan-out (first non-404 member wins) already works correctly — the image name is whatever the client requests regardless of slashes.
2. Implementing path-based proxy/hosted discrimination would require defining a naming convention that doesn't currently exist in the codebase and could break existing group repos.
3. Phase 16 goal is URL shape — removing the `/repository/` segment — not changing group routing semantics.

Open a separate IDEA-TODO or Phase 17 if explicit group routing discrimination is wanted.
