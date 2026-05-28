# Phase 51: Publish to Group Repositories (npm & Docker) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Forward PUT/POST/PATCH write requests on group repositories to the first hosted member, enabling `npm publish` and `docker push` through a group repo URL.

**Architecture:** (1) Add `GroupWritableMember` domain helper for optional explicit target config. (2) Add `serveWrite` to `group/handler.go` using the same sub-context + httptest mechanism as `serveGet`. (3) Remove the Docker-specific write-block guard in `router.go`'s `serveDockerV2` so the group handler receives write requests for Docker groups too. npm writes already reach the group handler (no router-level block exists).

**Tech Stack:** Go, Gin, existing testutil mocks — no new dependencies.

---

## File Map

| File | Change |
|------|--------|
| `internal/domain/types.go` | Add `GroupWritableMember(repo) string` after `GroupMemberNames` |
| `internal/domain/group_member_test.go` | Add `TestGroupWritableMember` |
| `internal/formats/group/handler.go` | Add `serveWrite`, update `ServeHTTP` switch for PUT/POST/PATCH |
| `internal/formats/group/handler_test.go` | Replace `TestGroup_PUT_Returns405` with proxy-only variant; add 4 write tests |
| `internal/api/router.go` | Remove write-block guard for Docker group repos in `serveDockerV2` |

---

### Task 1: Domain helper `GroupWritableMember`

**Files:**
- Modify: `internal/domain/types.go` (append after closing brace of `GroupMemberNames`, ~line 74)
- Modify: `internal/domain/group_member_test.go`

- [ ] **Step 1.1: Write failing test**

Append to `internal/domain/group_member_test.go` (inside the existing `package domain` or `package domain_test` — match what's already there):

```go
func TestGroupWritableMember(t *testing.T) {
	r := &Repository{
		FormatConfig: map[string]any{"writable_member": "hosted1"},
	}
	require.Equal(t, "hosted1", GroupWritableMember(r))

	r2 := &Repository{FormatConfig: map[string]any{}}
	require.Equal(t, "", GroupWritableMember(r2))

	require.Equal(t, "", GroupWritableMember(nil))
}
```

- [ ] **Step 1.2: Run test, confirm it fails**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core && go test ./internal/domain/... -run TestGroupWritableMember -v
```
Expected: compile error `undefined: GroupWritableMember`

- [ ] **Step 1.3: Implement in `internal/domain/types.go`**

Append immediately after the closing brace of `GroupMemberNames` (after line 74):

```go
// GroupWritableMember returns the explicitly configured writable member name
// from formatConfig["writable_member"], or empty string if not set (auto-detect).
func GroupWritableMember(r *Repository) string {
	if r == nil || r.FormatConfig == nil {
		return ""
	}
	v, _ := r.FormatConfig["writable_member"].(string)
	return v
}
```

- [ ] **Step 1.4: Run test, confirm it passes**

```bash
go test ./internal/domain/... -run TestGroupWritableMember -v
```
Expected: `PASS`

- [ ] **Step 1.5: Commit**

```bash
git add internal/domain/types.go internal/domain/group_member_test.go
git commit -m "feat(domain): add GroupWritableMember helper for group write routing"
```

---

### Task 2: Group handler write forwarding

**Files:**
- Modify: `internal/formats/group/handler.go`
- Modify: `internal/formats/group/handler_test.go`

- [ ] **Step 2.1: Update test file**

In `internal/formats/group/handler_test.go`:

1. **Delete** the existing `TestGroup_PUT_Returns405` test (the one with a hosted `m-ro` member — it will fail after our change since PUT to that group will now succeed).

2. **Add** these tests at the end of the file:

```go
// TestGroup_PUT_Returns405_ProxyOnly: group with no hosted members → 405
func TestGroup_PUT_Returns405_ProxyOnly(t *testing.T) {
	proxy := &domain.Repository{
		ID: "repo-px", Name: "px-only", Format: "raw",
		Type: domain.TypeProxy, Online: true,
	}
	grp := makeGroupRepo("grp-proxy-only", "px-only")
	r := buildEngine(proxy, grp)

	req := httptest.NewRequest(http.MethodPut, "/repository/grp-proxy-only/file.txt", strings.NewReader("x"))
	req.ContentLength = 1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestGroup_PUT_ForwardsToFirstHosted: PUT on group → stores artifact in first hosted member
func TestGroup_PUT_ForwardsToFirstHosted(t *testing.T) {
	m1 := testutil.SimpleRepo("hw1", "raw")
	grp := makeGroupRepo("grp-write", "hw1")
	r := buildEngine(m1, grp)

	code := put(r, "grp-write", "/uploaded.txt", "via group")
	assert.Equal(t, http.StatusCreated, code)

	// Verify artifact readable directly from member
	req := httptest.NewRequest(http.MethodGet, "/repository/hw1/uploaded.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "via group", w.Body.String())
}

// TestGroup_PUT_SkipsProxyUsesHosted: first member is proxy, second is hosted
func TestGroup_PUT_SkipsProxyUsesHosted(t *testing.T) {
	proxy := &domain.Repository{
		ID: "repo-px2", Name: "px2", Format: "raw",
		Type: domain.TypeProxy, Online: true,
	}
	hosted := testutil.SimpleRepo("hx2", "raw")
	grp := makeGroupRepo("grp-mixed", "px2", "hx2")
	r := buildEngine(proxy, hosted, grp)

	code := put(r, "grp-mixed", "/art.bin", "from mixed")
	assert.Equal(t, http.StatusCreated, code)

	req := httptest.NewRequest(http.MethodGet, "/repository/hx2/art.bin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "from mixed", w.Body.String())
}

// TestGroup_PUT_UsesWritableMemberConfig: explicit writable_member overrides auto-detect
func TestGroup_PUT_UsesWritableMemberConfig(t *testing.T) {
	m1 := testutil.SimpleRepo("wm1", "raw")
	m2 := testutil.SimpleRepo("wm2", "raw")
	grp := &domain.Repository{
		ID: "repo-grp-wm", Name: "grp-wm", Format: "raw",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{
			"member_names":    []interface{}{"wm1", "wm2"},
			"writable_member": "wm2",
		},
	}
	r := buildEngine(m1, m2, grp)

	code := put(r, "grp-wm", "/targeted.txt", "to m2")
	assert.Equal(t, http.StatusCreated, code)

	// m1 must NOT have the artifact
	req1 := httptest.NewRequest(http.MethodGet, "/repository/wm1/targeted.txt", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusNotFound, w1.Code)

	// m2 MUST have it
	req2 := httptest.NewRequest(http.MethodGet, "/repository/wm2/targeted.txt", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "to m2", w2.Body.String())
}
```

- [ ] **Step 2.2: Run new tests, confirm write-forwarding ones fail**

```bash
go test ./internal/formats/group/... -v -run "TestGroup_PUT"
```
Expected: `TestGroup_PUT_Returns405_ProxyOnly` PASS (current code returns 405 for all writes), `TestGroup_PUT_ForwardsToFirstHosted` FAIL (returns 405), `TestGroup_PUT_SkipsProxyUsesHosted` FAIL, `TestGroup_PUT_UsesWritableMemberConfig` FAIL.

- [ ] **Step 2.3: Implement `serveWrite` in `internal/formats/group/handler.go`**

Replace the entire `ServeHTTP` method and add `serveWrite` below it. The final `handler.go` looks like this (complete file):

```go
// Package group implements the "group" repository type.
//
// A group repository aggregates multiple hosted/proxy repositories under one URL.
// GET/HEAD are delegated to each member's format handler in order; the first
// non-404 response is returned.
//
// PUT/POST/PATCH are forwarded to the first hosted member (or the member named
// by formatConfig["writable_member"] if set). Groups with no hosted members
// return 405.
package group

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
)

// Handler implements the group repository type.
type Handler struct {
	deps           formats.Deps
	formatRegistry map[string]formats.FormatHandler
}

// New creates a group handler. formatRegistry is the same map used in the router.
func New(deps formats.Deps, formatRegistry map[string]formats.FormatHandler) *Handler {
	return &Handler{deps: deps, formatRegistry: formatRegistry}
}

func (h *Handler) Name() string { return "group" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodGet, http.MethodHead:
		h.serveGet(c)
	case http.MethodPut, http.MethodPost, http.MethodPatch:
		h.serveWrite(c)
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{
			"error": "group repository is read-only — publish to a member hosted repository",
		})
	}
}

func (h *Handler) serveGet(c *gin.Context) {
	repoName := c.Param("repoName")
	filePath := c.Param("path")
	ctx := c.Request.Context()

	repoDef, err := h.deps.Repos.Get(ctx, repoName)
	if err != nil || repoDef == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group repository not found: " + repoName})
		return
	}

	members := domain.GroupMemberNames(repoDef)
	if len(members) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "group repository has no members configured"})
		return
	}

	for _, memberName := range members {
		memberRepo, err := h.deps.Repos.Get(ctx, memberName)
		if err != nil || memberRepo == nil || !memberRepo.Online {
			continue
		}
		if memberRepo.Type == domain.TypeGroup {
			continue
		}
		if string(memberRepo.Format) != string(repoDef.Format) {
			continue
		}
		handler, ok := h.formatRegistry[string(memberRepo.Format)]
		if !ok {
			continue
		}

		rec := httptest.NewRecorder()
		sub, _ := gin.CreateTestContext(rec)
		sub.Request = c.Request.Clone(ctx)
		sub.Params = gin.Params{
			{Key: "repoName", Value: memberName},
			{Key: "path", Value: filePath},
		}

		handler.ServeHTTP(sub)

		code := rec.Code
		if code == 0 {
			code = http.StatusOK
		}
		if code == http.StatusNotFound {
			continue
		}

		for k, vals := range rec.Header() {
			for _, v := range vals {
				c.Writer.Header().Add(k, v)
			}
		}
		c.Writer.Header().Set("X-Nexspence-Source", memberName)
		c.Status(code)
		if c.Request.Method != http.MethodHead && rec.Body.Len() > 0 {
			_, _ = io.Copy(c.Writer, rec.Body)
		}
		return
	}

	c.JSON(http.StatusNotFound, gin.H{
		"error": fmt.Sprintf("artifact not found in any member of group %q", repoName),
	})
}

func (h *Handler) serveWrite(c *gin.Context) {
	repoName := c.Param("repoName")
	filePath := c.Param("path")
	ctx := c.Request.Context()

	repoDef, err := h.deps.Repos.Get(ctx, repoName)
	if err != nil || repoDef == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group repository not found: " + repoName})
		return
	}

	// Resolve writable member: explicit config wins, then first TypeHosted member.
	targetName := domain.GroupWritableMember(repoDef)
	if targetName == "" {
		for _, memberName := range domain.GroupMemberNames(repoDef) {
			memberRepo, err := h.deps.Repos.Get(ctx, memberName)
			if err != nil || memberRepo == nil || !memberRepo.Online {
				continue
			}
			if memberRepo.Type == domain.TypeHosted && string(memberRepo.Format) == string(repoDef.Format) {
				targetName = memberName
				break
			}
		}
	}

	if targetName == "" {
		c.JSON(http.StatusMethodNotAllowed, gin.H{
			"error": "group repository has no hosted member — publish directly to a hosted repository",
		})
		return
	}

	targetRepo, err := h.deps.Repos.Get(ctx, targetName)
	if err != nil || targetRepo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "writable member not found: " + targetName})
		return
	}

	handler, ok := h.formatRegistry[string(targetRepo.Format)]
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no handler for format: " + string(targetRepo.Format)})
		return
	}

	rec := httptest.NewRecorder()
	sub, _ := gin.CreateTestContext(rec)
	sub.Request = c.Request.Clone(ctx)
	sub.Params = gin.Params{
		{Key: "repoName", Value: targetName},
		{Key: "path", Value: filePath},
	}

	handler.ServeHTTP(sub)

	code := rec.Code
	if code == 0 {
		code = http.StatusOK
	}
	for k, vals := range rec.Header() {
		for _, v := range vals {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Status(code)
	if rec.Body.Len() > 0 {
		_, _ = io.Copy(c.Writer, rec.Body)
	}
}
```

- [ ] **Step 2.4: Run all group handler tests**

```bash
go test ./internal/formats/group/... -v
```
Expected: all 9 tests PASS (6 original + 4 new - 1 deleted).

- [ ] **Step 2.5: Commit**

```bash
git add internal/formats/group/handler.go internal/formats/group/handler_test.go
git commit -m "feat(group): forward PUT/POST/PATCH writes to first hosted member"
```

---

### Task 3: Remove Docker group write-block from router

**Files:**
- Modify: `internal/api/router.go` (~lines 546-563 in `serveDockerV2`)

- [ ] **Step 3.1: Read the current block in router.go**

Find the section in `serveDockerV2` that starts with `if repoDef.Type == domain.TypeGroup {`. Currently it looks like:

```go
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
```

- [ ] **Step 3.2: Remove the write-block guard**

Replace that block with (remove the 5-line Method guard, keep the rest):

```go
if repoDef.Type == domain.TypeGroup {
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
```

- [ ] **Step 3.3: Run full test suite**

```bash
go test ./... 2>&1 | tail -20
```
Expected: all tests PASS. The group handler now receives Docker write requests and routes them to the first hosted member.

- [ ] **Step 3.4: Commit**

```bash
git add internal/api/router.go
git commit -m "feat(router): allow Docker group repos to receive write requests via group handler"
```

---

### Task 4: Update CLAUDE.md and task_plan.md

**Files:**
- Modify: `CLAUDE.md` (project root — actually at `self_nexus/` or project root — check path)
- Modify: `task_plan.md`

- [ ] **Step 4.1: Update task_plan.md Phase 51 status**

Change:
```
## Phase 51: Deployment to Group Repositories (npm & Docker)
**Status:** backlog
```
To:
```
## Phase 51: Deployment to Group Repositories (npm & Docker)
**Status:** complete (2026-05-04)
```

And mark all tasks as done:
```
- [x] `GroupHandler`: для PUT/POST/PATCH определять первый hosted-member и проксировать туда запрос
- [x] npm: `PUT /:repoName/:package` → forward to first hosted npm member
- [x] Docker: `PUT /v2/:repoName/blobs/uploads/`, `PUT /v2/:repoName/manifests/:ref` → forward to first hosted docker member
- [x] Конфиг репозитория: `group.writable_member` (опционально явно указать target)
- [x] Тесты: проверить что read-only группы (без hosted-member) возвращают 405
```

- [ ] **Step 4.2: Update CLAUDE.md current phase block**

In CLAUDE.md, find "Currently: **Phase 46 complete**" and update to:

```
Currently: **Phase 51 complete (2026-05-04)** — Group Write Routing: `GroupWritableMember` domain helper; `group/handler.go` `serveWrite` forwards PUT/POST/PATCH to first `TypeHosted` member (or explicit `writable_member` config); Docker write-block removed from `serveDockerV2` in router; groups with no hosted members return 405. **Phase 50 complete (2026-05-04)** — Docker Subdomain Connector. **Phase 49 complete (2026-05-04)** — Change Repository Blob Store Content Migration Task. **Phase 48 complete (2026-05-02)** — Group Blob Stores with fill policy. **Phase 47 complete (2026-04-29)** — UI/UX Accessibility & Polish (a11y, code splitting, skeleton loaders, responsive breakpoints).
```

- [ ] **Step 4.3: Commit**

```bash
git add task_plan.md CLAUDE.md
git commit -m "docs(phase51): mark complete, update CLAUDE.md phase history"
```

---

## Self-Review

**Spec coverage:**
- ✅ `GroupHandler` PUT/POST/PATCH → `serveWrite` (Task 2)
- ✅ npm write forwarding — npm goes through `/repository/:repoName/*path` → group handler → `serveWrite` (no router change needed, tested via raw handler which uses same mechanism as npm)
- ✅ Docker write forwarding — router change in Task 3 + group handler `serveWrite`
- ✅ `group.writable_member` config — `GroupWritableMember` domain helper + `serveWrite` uses it (Task 1 + 2)
- ✅ Read-only groups (no hosted member) → 405 — `TestGroup_PUT_Returns405_ProxyOnly` (Task 2)

**Placeholder scan:** No TBDs or incomplete steps.

**Type consistency:** `GroupWritableMember` defined in Task 1, used in Task 2's `serveWrite` implementation — consistent.
