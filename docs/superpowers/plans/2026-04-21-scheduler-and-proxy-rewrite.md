# Cleanup Scheduler + Helm/NuGet Proxy Rewrite — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the hardcoded 6-hour cleanup scheduler with per-policy cron scheduling, and fix helm/nuget proxy repos so their index files rewrite artifact URLs to point through our cache.

**Architecture:** `robfig/cron/v3` drives per-policy scheduling; `CleanupService` gains `StartCronScheduler`/`ReloadPolicy`; handlers call `ReloadPolicy` after every policy mutation. Helm and NuGet proxy handlers intercept their index endpoints, fetch from upstream, rewrite URLs in-memory, and return the patched response.

**Tech Stack:** Go, `github.com/robfig/cron/v3`, `gopkg.in/yaml.v3` (already in deps), `encoding/json` (stdlib), `net/http`, `net/http/httptest` (tests).

---

## File Map

| File | Action | What changes |
|------|--------|--------------|
| `go.mod` / `go.sum` | Modify | Add `github.com/robfig/cron/v3` |
| `internal/config/config.go` | Modify | Add `CleanupConfig` + default |
| `config.yaml` | Modify | Add `cleanup.default_schedule` |
| `internal/service/cleanup_service.go` | Modify | Add cron fields, `StartCronScheduler`, `ReloadPolicy`; remove `StartScheduler` |
| `internal/api/handlers/cleanup.go` | Modify | Add `ReloadPolicy` to `cleanupRunner` interface; call it in Create/Update/Delete |
| `internal/api/router.go` | Modify | Switch to `StartCronScheduler`; no longer passes `6*time.Hour` |
| `internal/formats/helm/handler.go` | Modify | Add `fetchAndRewriteHelmIndex` for proxy `/index.yaml` |
| `internal/formats/nuget/handler.go` | Modify | Add `fetchAndRewriteNuGetIndex` for proxy `/index.json` |
| `internal/formats/helm/handler_test.go` | Modify | Add proxy index rewrite test |
| `internal/formats/nuget/handler_test.go` | Modify | Add proxy index rewrite test |
| `internal/service/cleanup_service_test.go` | Modify | Add `ReloadPolicy` tests |

---

## Task 1: Add `robfig/cron/v3` dependency and `CleanupConfig`

**Files:**
- Modify: `go.mod`
- Modify: `internal/config/config.go`
- Modify: `config.yaml`

- [ ] **Step 1: Add the cron dependency**

```bash
cd /home/skensel/AI/self_nexus
go get github.com/robfig/cron/v3@latest
```

Expected: `go.mod` and `go.sum` updated, no errors.

- [ ] **Step 2: Verify it compiles**

```bash
go build ./...
```

Expected: clean build (0 errors).

- [ ] **Step 3: Add `CleanupConfig` to config.go**

In `internal/config/config.go`, add the struct and the field to `Config`:

```go
type CleanupConfig struct {
	DefaultSchedule string `mapstructure:"default_schedule"`
}
```

Add `Cleanup CleanupConfig \`mapstructure:"cleanup"\`` to `Config` struct (after `Search SearchConfig`):

```go
type Config struct {
	HTTP      HTTPConfig      `mapstructure:"http"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Storage   StorageConfig   `mapstructure:"storage"`
	Auth      AuthConfig      `mapstructure:"auth"`
	LDAP      LDAPConfig      `mapstructure:"ldap"`
	Bootstrap BootstrapConfig `mapstructure:"bootstrap"`
	Log       LogConfig       `mapstructure:"log"`
	Search    SearchConfig    `mapstructure:"search"`
	Cleanup   CleanupConfig   `mapstructure:"cleanup"`
}
```

Add default in `Load` function (after the `search.min_query_len` default):

```go
v.SetDefault("cleanup.default_schedule", "0 */6 * * *")
```

- [ ] **Step 4: Add `cleanup` section to config.yaml**

Append at the end of `config.yaml`:

```yaml

cleanup:
  default_schedule: "0 */6 * * *"   # cron schedule used for policies without schedule_cron
```

- [ ] **Step 5: Build to verify**

```bash
go build ./...
```

Expected: clean build.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/config/config.go config.yaml
git commit -m "feat: add robfig/cron dep and CleanupConfig.DefaultSchedule"
```

---

## Task 2: Rewrite `CleanupService` with per-policy cron

**Files:**
- Modify: `internal/service/cleanup_service.go`

- [ ] **Step 1: Write tests for `ReloadPolicy` before implementing**

Add to `internal/service/cleanup_service_test.go` (at the end of the file):

```go
func TestReloadPolicy_NoopWhenSchedulerNotStarted(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(
		&domain.CleanupPolicy{
			ID: "p10", Name: "policy", Enabled: true, Format: "*",
			ScheduleCron: "* * * * *",
			Criteria:     map[string]any{"artifactAgeDays": float64(1)},
		},
	)
	svc := service.NewCleanupService(policies, testutil.NewRepoRepo(), testutil.NewAssetRepo(), testutil.NewBlobStore(), nopLog())

	// Must not panic before StartCronScheduler is called
	svc.ReloadPolicy(context.Background(), "p10")
	svc.ReloadPolicy(context.Background(), "nonexistent")
}

func TestReloadPolicy_RemovesEntryForDeletedPolicy(t *testing.T) {
	p := &domain.CleanupPolicy{
		ID: "p11", Name: "removable", Enabled: true, Format: "*",
		ScheduleCron: "@yearly", // won't fire during test
		Criteria:     map[string]any{"artifactAgeDays": float64(365)},
	}
	policies := testutil.NewCleanupPolicyRepo(p)
	svc := service.NewCleanupService(policies, testutil.NewRepoRepo(), testutil.NewAssetRepo(), testutil.NewBlobStore(), nopLog())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.StartCronScheduler(ctx, "@yearly")
	time.Sleep(50 * time.Millisecond) // wait for cron to start

	// Simulate deletion: remove from mock repo, then reload
	policies.Delete(context.Background(), "p11")
	// Should not panic — entry removed, policy not found
	svc.ReloadPolicy(context.Background(), "p11")
}

func TestStartCronScheduler_InvalidCronFallsBackToDefault(t *testing.T) {
	p := &domain.CleanupPolicy{
		ID: "p12", Name: "bad-cron", Enabled: true, Format: "*",
		ScheduleCron: "NOT_A_VALID_CRON",
		Criteria:     map[string]any{"artifactAgeDays": float64(1)},
	}
	policies := testutil.NewCleanupPolicyRepo(p)
	svc := service.NewCleanupService(policies, testutil.NewRepoRepo(), testutil.NewAssetRepo(), testutil.NewBlobStore(), nopLog())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Must not panic — falls back to default schedule
	go svc.StartCronScheduler(ctx, "@yearly")
	time.Sleep(50 * time.Millisecond)
}
```

Also add `"time"` to the imports in the test file if not already present.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/service/... -run "TestReloadPolicy|TestStartCronScheduler" -v
```

Expected: compile error — `StartCronScheduler` and `ReloadPolicy` don't exist yet.

- [ ] **Step 3: Rewrite `cleanup_service.go`**

Replace the full file `internal/service/cleanup_service.go` with:

```go
package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/robfig/cron/v3"
)

// CleanupService runs cleanup policies — finds stale assets and removes them.
type CleanupService struct {
	policies  repository.CleanupPolicyRepo
	repos     repository.RepositoryRepo
	assets    repository.AssetRepo
	blobStore storage.BlobStore
	log       logger.Logger

	mu              sync.Mutex
	cronScheduler   *cron.Cron
	entryIDs        map[string]cron.EntryID
	defaultSchedule string
}

func NewCleanupService(
	policies repository.CleanupPolicyRepo,
	repos repository.RepositoryRepo,
	assets repository.AssetRepo,
	blobStore storage.BlobStore,
	log logger.Logger,
) *CleanupService {
	return &CleanupService{
		policies:  policies,
		repos:     repos,
		assets:    assets,
		blobStore: blobStore,
		log:       log,
		entryIDs:  make(map[string]cron.EntryID),
	}
}

// StartCronScheduler starts cron-based per-policy scheduling. Run as a goroutine.
// Policies with a non-empty schedule_cron field use that expression; others use defaultSchedule.
func (s *CleanupService) StartCronScheduler(ctx context.Context, defaultSchedule string) {
	s.mu.Lock()
	s.defaultSchedule = defaultSchedule
	s.cronScheduler = cron.New()
	s.mu.Unlock()

	policies, err := s.policies.List(ctx)
	if err != nil {
		s.log.Error("cleanup: failed to load policies for scheduler", "err", err)
	} else {
		s.mu.Lock()
		for _, p := range policies {
			if p.Enabled {
				s.addEntryLocked(p)
			}
		}
		s.mu.Unlock()
	}

	s.cronScheduler.Start()
	<-ctx.Done()
	s.cronScheduler.Stop()
}

// ReloadPolicy updates the cron schedule for a single policy (call after Create/Update/Delete).
// If the policy is not found or disabled, its cron entry is removed.
func (s *CleanupService) ReloadPolicy(ctx context.Context, policyID string) {
	// Fetch from DB outside the lock to avoid holding it during I/O.
	p, _ := s.policies.Get(ctx, policyID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cronScheduler == nil {
		return // scheduler not started yet
	}

	// Remove existing entry if present.
	if eid, ok := s.entryIDs[policyID]; ok {
		s.cronScheduler.Remove(eid)
		delete(s.entryIDs, policyID)
	}

	if p == nil || !p.Enabled {
		return
	}
	s.addEntryLocked(*p)
}

// addEntryLocked registers a cron job for policy p. Caller must hold s.mu.
func (s *CleanupService) addEntryLocked(p domain.CleanupPolicy) {
	schedule := p.ScheduleCron
	if schedule == "" {
		schedule = s.defaultSchedule
	}

	job := func() {
		if err := s.runPolicy(context.Background(), p); err != nil {
			s.log.Error("cleanup cron error", "policy", p.Name, "err", err)
		}
	}

	id, err := s.cronScheduler.AddFunc(schedule, job)
	if err != nil {
		s.log.Warn("cleanup: invalid schedule_cron, falling back to default",
			"policy", p.Name, "schedule", schedule, "err", err)
		id, _ = s.cronScheduler.AddFunc(s.defaultSchedule, job)
	}
	s.entryIDs[p.ID] = id
}

// RunAll executes all enabled cleanup policies once and returns a summary.
func (s *CleanupService) RunAll(ctx context.Context) error {
	policies, err := s.policies.List(ctx)
	if err != nil {
		return fmt.Errorf("cleanup: list policies: %w", err)
	}
	for _, p := range policies {
		if !p.Enabled {
			continue
		}
		if err := s.runPolicy(ctx, p); err != nil {
			s.log.Error("cleanup policy failed", "policy", p.Name, "err", err)
		}
	}
	return nil
}

// RunPolicy executes a single policy by ID.
func (s *CleanupService) RunPolicy(ctx context.Context, id string) error {
	p, err := s.policies.Get(ctx, id)
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("cleanup policy %q not found", id)
	}
	return s.runPolicy(ctx, *p)
}

func (s *CleanupService) runPolicy(ctx context.Context, p domain.CleanupPolicy) error {
	lastDownloadedDays := intCriteria(p.Criteria, "lastDownloadedDays")
	artifactAgeDays := intCriteria(p.Criteria, "artifactAgeDays")
	pathPrefix := strCriteria(p.Criteria, "pathPrefix")
	nameGlob := strCriteria(p.Criteria, "nameGlob")

	if lastDownloadedDays == 0 && artifactAgeDays == 0 {
		s.log.Info("cleanup: no criteria set, skipping", "policy", p.Name)
		return nil
	}

	repoNames, err := s.repos.ListNamesByCleanupPolicyID(ctx, p.ID)
	if err != nil {
		return fmt.Errorf("cleanup: list repos for policy: %w", err)
	}
	if len(repoNames) == 0 {
		s.log.Info("cleanup: policy not attached to any repository (set cleanup policies on repositories), skipping", "policy", p.Name)
		return nil
	}

	const batchLimit = 500
	var freed int64
	var deleted int
	for {
		stale, err := s.assets.ListStale(ctx, p.Format, repoNames, lastDownloadedDays, artifactAgeDays, pathPrefix, nameGlob, batchLimit)
		if err != nil {
			return fmt.Errorf("cleanup: list stale assets: %w", err)
		}
		if len(stale) == 0 {
			break
		}
		for _, a := range stale {
			if p.DryRun {
				s.log.Info("cleanup dry-run: would delete", "policy", p.Name,
					"asset", a.Path, "repo", a.Repository, "size", a.SizeBytes)
				freed += a.SizeBytes
				deleted++
				continue
			}
			if err := s.blobStore.Delete(ctx, a.BlobKey); err != nil {
				s.log.Warn("cleanup: blob delete failed", "key", a.BlobKey, "err", err)
			}
			if err := s.assets.Delete(ctx, a.ID); err != nil {
				s.log.Warn("cleanup: asset delete failed", "id", a.ID, "err", err)
				continue
			}
			freed += a.SizeBytes
			deleted++
		}
	}

	now := time.Now()
	p.LastRunAt = &now
	p.LastRunFreed = freed
	p.LastRunCount = deleted
	if err := s.policies.Update(ctx, &p); err != nil {
		s.log.Warn("cleanup: failed to update policy stats", "policy", p.Name, "err", err)
	}

	s.log.Info("cleanup policy complete",
		"policy", p.Name,
		"deleted", deleted,
		"freed_bytes", freed,
		"dry_run", p.DryRun)
	return nil
}

func strCriteria(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func intCriteria(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	}
	return 0
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/service/... -v
```

Expected: all existing tests pass + 3 new tests pass.

- [ ] **Step 5: Verify build**

```bash
go build ./...
```

Expected: clean (0 errors).

- [ ] **Step 6: Commit**

```bash
git add internal/service/cleanup_service.go internal/service/cleanup_service_test.go
git commit -m "feat: replace fixed-interval scheduler with per-policy cron (robfig/cron/v3)"
```

---

## Task 3: Wire `ReloadPolicy` into `CleanupHandler` and `router.go`

**Files:**
- Modify: `internal/api/handlers/cleanup.go`
- Modify: `internal/api/router.go`

- [ ] **Step 1: Extend the `cleanupRunner` interface and handler**

Replace `internal/api/handlers/cleanup.go` with:

```go
package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// cleanupRunner is the minimal interface CleanupHandler needs from CleanupService.
type cleanupRunner interface {
	RunPolicy(ctx context.Context, id string) error
	RunAll(ctx context.Context) error
	ReloadPolicy(ctx context.Context, id string)
}

type CleanupHandler struct {
	policies repository.CleanupPolicyRepo
	repos    repository.RepositoryRepo
	runner   cleanupRunner
}

func NewCleanupHandler(
	policies repository.CleanupPolicyRepo,
	repos repository.RepositoryRepo,
	runner cleanupRunner,
) *CleanupHandler {
	return &CleanupHandler{policies: policies, repos: repos, runner: runner}
}

// List GET /service/rest/v1/cleanup-policies
func (h *CleanupHandler) List(c *gin.Context) {
	policies, err := h.policies.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if policies == nil {
		policies = []domain.CleanupPolicy{}
	}
	c.JSON(http.StatusOK, policies)
}

// Get GET /service/rest/v1/cleanup-policies/:id
func (h *CleanupHandler) Get(c *gin.Context) {
	p, err := h.policies.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// Create POST /service/rest/v1/cleanup-policies
func (h *CleanupHandler) Create(c *gin.Context) {
	var p domain.CleanupPolicy
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if p.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if p.Format == "" {
		p.Format = "*"
	}
	if p.Criteria == nil {
		p.Criteria = map[string]any{}
	}
	if err := h.policies.Create(c.Request.Context(), &p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.runner.ReloadPolicy(c.Request.Context(), p.ID)
	c.JSON(http.StatusCreated, p)
}

// Update PUT /service/rest/v1/cleanup-policies/:id
func (h *CleanupHandler) Update(c *gin.Context) {
	var p domain.CleanupPolicy
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	p.ID = c.Param("id")
	if err := h.policies.Update(c.Request.Context(), &p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.runner.ReloadPolicy(c.Request.Context(), p.ID)
	c.JSON(http.StatusOK, p)
}

// Delete DELETE /service/rest/v1/cleanup-policies/:id
func (h *CleanupHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.repos.DetachCleanupPolicyID(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.policies.Delete(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Policy is deleted; ReloadPolicy sees it's gone and removes the cron entry.
	h.runner.ReloadPolicy(c.Request.Context(), id)
	c.Status(http.StatusNoContent)
}

// Run POST /service/rest/v1/cleanup-policies/:id/run — trigger policy immediately
func (h *CleanupHandler) Run(c *gin.Context) {
	id := c.Param("id")
	if id == "_all" {
		go func() { _ = h.runner.RunAll(context.Background()) }()
		c.JSON(http.StatusAccepted, gin.H{"status": "running all policies"})
		return
	}
	go func() { _ = h.runner.RunPolicy(context.Background(), id) }()
	c.JSON(http.StatusAccepted, gin.H{"status": "running", "id": id})
}
```

- [ ] **Step 2: Update `router.go` — switch to `StartCronScheduler`**

In `internal/api/router.go`, find line 84:
```go
go cleanupSvc.StartScheduler(context.Background(), 6*time.Hour)
```

Replace with:
```go
go cleanupSvc.StartCronScheduler(context.Background(), cfg.Cleanup.DefaultSchedule)
```

Also remove the `"time"` import if it becomes unused after this change (check — `time` is likely used elsewhere in router.go for `httpUpstream`, so keep it if so).

- [ ] **Step 3: Build to verify**

```bash
go build ./...
```

Expected: clean (0 errors). The `cleanupRunner` interface now includes `ReloadPolicy` and `*CleanupService` satisfies it.

- [ ] **Step 4: Run all tests**

```bash
go test ./internal/service/... ./internal/api/...
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/api/handlers/cleanup.go internal/api/router.go
git commit -m "feat: wire ReloadPolicy into CleanupHandler and switch router to StartCronScheduler"
```

---

## Task 4: Helm proxy — rewrite `index.yaml` URLs

**Files:**
- Modify: `internal/formats/helm/handler.go`
- Modify: `internal/formats/helm/handler_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/formats/helm/handler_test.go` (at the end, before the last `}`):

```go
func TestHelm_ProxyIndexYaml_RewritesURLs(t *testing.T) {
	// Mock upstream helm repository
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		fmt.Fprint(w, `apiVersion: v1
entries:
  nginx:
  - name: nginx
    version: "15.0.0"
    urls:
    - https://charts.bitnami.com/bitnami/nginx-15.0.0.tgz
generated: "2024-01-01T00:00:00Z"
`)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "rp", Name: "helm-proxy", Format: "helm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo) // uses BaseURL: "http://localhost:8080"

	req := httptest.NewRequest(http.MethodGet, "/repository/helm-proxy/index.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "http://localhost:8080/repository/helm-proxy/nginx-15.0.0.tgz",
		"chart URL should be rewritten to local proxy")
	assert.NotContains(t, body, "charts.bitnami.com",
		"upstream URL must not appear in rewritten index")
}

func TestHelm_ProxyIndexYaml_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusGone)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "rp2", Name: "helm-proxy2", Format: "helm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/helm-proxy2/index.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
}
```

Also add `"fmt"` to the imports of `handler_test.go`.

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/formats/helm/... -run "TestHelm_Proxy" -v
```

Expected: FAIL (no proxy index rewriting exists yet).

- [ ] **Step 3: Implement `fetchAndRewriteHelmIndex` in handler.go**

In `internal/formats/helm/handler.go`, change the proxy branch in `ServeHTTP` and add the new function.

Replace the proxy block (lines 37–46):
```go
	// Proxy: block mutations, proxy reads
	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		coords := base.Coords{Name: strings.TrimSuffix(strings.TrimPrefix(p, "/"), ".tgz")}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/x-tar"); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}
```

With:
```go
	// Proxy: block mutations; rewrite index.yaml; cache chart binaries.
	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		if (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && p == "/index.yaml" {
			h.fetchAndRewriteHelmIndex(c, repo)
			return
		}
		coords := base.Coords{Name: strings.TrimSuffix(strings.TrimPrefix(p, "/"), ".tgz")}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/x-tar"); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}
```

Add the new method at the end of `handler.go` (before `func normPath`):

```go
// fetchAndRewriteHelmIndex fetches index.yaml from upstream, rewrites chart download
// URLs to point to this proxy, and returns the patched YAML to the client.
// The index is not cached — it is always fetched live so new upstream charts appear promptly.
func (h *Handler) fetchAndRewriteHelmIndex(c *gin.Context, repo *domain.Repository) {
	remoteBase, err := repoproxy.RemoteURL(repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(remoteBase, "/")+"/index.yaml", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream fetch failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("upstream returned %d", resp.StatusCode)})
		return
	}

	var index map[string]any
	if err := yaml.NewDecoder(resp.Body).Decode(&index); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "invalid upstream index.yaml: " + err.Error()})
		return
	}

	// Rewrite each chart's download URLs to point through this proxy.
	localBase := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repo.Name + "/"
	if entries, ok := index["entries"].(map[string]any); ok {
		for _, v := range entries {
			charts, ok := v.([]any)
			if !ok {
				continue
			}
			for _, cv := range charts {
				chart, ok := cv.(map[string]any)
				if !ok {
					continue
				}
				if urls, ok := chart["urls"].([]any); ok {
					for i, u := range urls {
						if us, ok := u.(string); ok {
							urls[i] = localBase + path.Base(us)
						}
					}
					chart["urls"] = urls
				}
			}
		}
	}

	data, err := yaml.Marshal(index)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/yaml", data)
}
```

Make sure `handler.go` imports include `"context"`, `"fmt"`, `"net/http"`, `"path"`, `"strings"`, `"time"`, `"gopkg.in/yaml.v3"` — most are already there; add any missing ones.

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/formats/helm/... -v
```

Expected: all pass including the two new proxy tests.

- [ ] **Step 5: Build**

```bash
go build ./...
```

Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/formats/helm/handler.go internal/formats/helm/handler_test.go
git commit -m "feat: helm proxy rewrites index.yaml chart URLs to local proxy"
```

---

## Task 5: NuGet proxy — rewrite `index.json` URLs

**Files:**
- Modify: `internal/formats/nuget/handler.go`
- Modify: `internal/formats/nuget/handler_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/formats/nuget/handler_test.go` (at the end of the file):

```go
func TestNuGet_ProxyServiceIndex_RewritesURLs(t *testing.T) {
	// Mock upstream NuGet v3 service index
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
  "version": "3.0.0",
  "resources": [
    {"@id": "https://api.nuget.org/v3/flatcontainer/", "@type": "PackageBaseAddress/3.0.0"},
    {"@id": "https://api.nuget.org/v3/registration5-gz-semver2/", "@type": "RegistrationsBaseUrl/3.6.0"}
  ]
}`)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "rp3", Name: "nuget-proxy", Format: "nuget",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo) // BaseURL: "http://localhost:8080"

	req := httptest.NewRequest(http.MethodGet, "/repository/nuget-proxy/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "http://localhost:8080/repository/nuget-proxy/v3/flatcontainer/",
		"PackageBaseAddress @id should be rewritten")
	assert.Contains(t, body, "http://localhost:8080/repository/nuget-proxy/v3/registration5-gz-semver2/",
		"RegistrationsBaseUrl @id should be rewritten")
	assert.NotContains(t, body, "api.nuget.org",
		"upstream host must not appear in rewritten index")
}

func TestNuGet_ProxyServiceIndex_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "rp4", Name: "nuget-proxy2", Format: "nuget",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/nuget-proxy2/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
}
```

Also add `"fmt"` to imports of `nuget/handler_test.go` if not present.

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/formats/nuget/... -run "TestNuGet_Proxy" -v
```

Expected: FAIL (no proxy index rewriting).

- [ ] **Step 3: Implement `fetchAndRewriteNuGetIndex` in handler.go**

In `internal/formats/nuget/handler.go`, replace the proxy block (lines 40–50):

```go
	// Proxy: block mutations, proxy reads through to upstream (e.g. nuget.org)
	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		coords := base.Coords{}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/octet-stream"); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}
```

With:

```go
	// Proxy: block mutations; rewrite service index; cache packages.
	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		if (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && p == "/index.json" {
			h.fetchAndRewriteNuGetIndex(c, repo)
			return
		}
		coords := base.Coords{}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/octet-stream"); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}
```

Add the new method at the end of `handler.go` (before the existing `normPath` function):

```go
// fetchAndRewriteNuGetIndex fetches the NuGet v3 service index from upstream,
// rewrites all resource @id URLs to point to this proxy, and returns the result.
// Not cached — fetched live so new resource endpoints appear promptly.
func (h *Handler) fetchAndRewriteNuGetIndex(c *gin.Context, repo *domain.Repository) {
	remoteBase, err := repoproxy.RemoteURL(repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(remoteBase, "/")+"/index.json", nil)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream fetch failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("upstream returned %d", resp.StatusCode)})
		return
	}

	var index map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&index); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "invalid upstream index.json: " + err.Error()})
		return
	}

	// Rewrite each resource's @id to point through this proxy.
	// Strategy: parse the @id URL, strip origin+path, keep only the path portion,
	// prepend our local base. This handles nuget.org and custom upstream paths.
	localBase := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repo.Name

	if resources, ok := index["resources"].([]any); ok {
		for _, r := range resources {
			res, ok := r.(map[string]any)
			if !ok {
				continue
			}
			id, ok := res["@id"].(string)
			if !ok {
				continue
			}
			parsed, err := url.Parse(id)
			if err != nil {
				continue
			}
			res["@id"] = localBase + parsed.RequestURI()
		}
	}

	c.JSON(http.StatusOK, index)
}
```

Ensure `handler.go` imports include `"context"`, `"encoding/json"`, `"fmt"`, `"net/http"`, `"net/url"`, `"strings"`, `"time"`. Add any that are missing.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/formats/nuget/... -v
```

Expected: all pass including the two new proxy tests.

- [ ] **Step 5: Build**

```bash
go build ./...
```

Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/formats/nuget/handler.go internal/formats/nuget/handler_test.go
git commit -m "feat: nuget proxy rewrites service index.json resource URLs to local proxy"
```

---

## Task 6: Run full test suite and update `task_plan.md`

- [ ] **Step 1: Run all tests**

```bash
go test ./...
```

Expected: all pass. If any fail, investigate before continuing.

- [ ] **Step 2: Update `task_plan.md`**

Add a new phase section after the last existing phase:

```markdown
### Phase 11: Cleanup Scheduler + Proxy Improvements
**Status:** complete

**Tasks:**
- [x] Replace hardcoded 6h cleanup scheduler with per-policy cron (`robfig/cron/v3`)
- [x] `CleanupConfig.DefaultSchedule` in config + `config.yaml` (`cleanup.default_schedule`)
- [x] `CleanupService.StartCronScheduler` + `ReloadPolicy` — handlers call `ReloadPolicy` after Create/Update/Delete
- [x] `cleanupRunner` interface extended with `ReloadPolicy`
- [x] Helm proxy: `fetchAndRewriteHelmIndex` — rewrites `index.yaml` chart URLs to proxy
- [x] NuGet proxy: `fetchAndRewriteNuGetIndex` — rewrites `index.json` resource `@id` URLs to proxy
- [x] APT/YUM proxy: already worked via pass-through (relative paths)
- [x] Docker proxy: already complete (manifest/blob proxy with Hub token support)
```

- [ ] **Step 3: Final commit**

```bash
git add task_plan.md
git commit -m "docs: mark Phase 11 complete (cron scheduler + helm/nuget proxy rewrite)"
```
