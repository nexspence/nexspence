# S3 Blob Store Routing + UX Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route artifact reads/writes to the physical BlobStore assigned to each repository (instead of always using the global default), and improve S3 blob store UX: connection test on create, config display in edit/detail modal, S3 connections in Service Connections panel.

**Architecture:** A `storage.Registry` caches physical `BlobStore` instances keyed by domain blob store ID; it creates them lazily from `domain.BlobStore.Config`. `formats.Deps` gains a `Registry` field. `base.StoreArtifact`, `FetchArtifact`, `DeleteArtifact` use a helper `physicalStore(d, bs)` that returns the right instance. Existing `d.BlobStore` is kept as fallback for the "default" store case. For UX: a new `POST /api/v1/blobstores/test` endpoint probes connectivity without persisting; `SystemHandler` queries the DB for S3 blob stores and adds them to service checks grouped by endpoint.

**Tech Stack:** Go (Gin, pgx), React + TypeScript, aws-sdk-go-v2

---

## File Map

| File | Change |
|------|--------|
| `internal/storage/registry.go` | **Create** — Registry struct + lazy factory |
| `internal/formats/deps.go` | **Modify** — add `Registry *storage.Registry` |
| `internal/formats/base/store.go` | **Modify** — `physicalStore` helper; use in Store/Fetch/Delete |
| `internal/api/handlers/blobstores.go` | **Modify** — add `TestConnection` handler |
| `internal/api/handlers/system.go` | **Modify** — query DB blob stores for S3 service checks |
| `internal/api/router.go` | **Modify** — create registry, set on formatDeps, wire test route + system handler blobRepo |
| `frontend/src/api/client.ts` | **Modify** — add `testBlobStore` call |
| `frontend/src/pages/AdminPage.tsx` | **Modify** — test button, S3 config display, edit form, service connections |

---

## Task 1: storage.Registry

**Files:**
- Create: `internal/storage/registry.go`

- [ ] **Step 1: Write the file**

```go
package storage

import (
	"context"
	"fmt"
	"sync"
)

// BlobStoreDescriptor carries the minimal DB data needed to instantiate a physical BlobStore.
type BlobStoreDescriptor struct {
	ID     string
	Type   string         // "local" | "s3"
	Config map[string]any
}

// Registry creates and caches physical BlobStore instances keyed by blob store ID.
// Safe for concurrent use. The default store is returned when Get is called with
// an empty/unrecognised descriptor.
type Registry struct {
	mu           sync.RWMutex
	instances    map[string]BlobStore
	defaultStore BlobStore
}

func NewRegistry(defaultStore BlobStore) *Registry {
	return &Registry{
		instances:    make(map[string]BlobStore),
		defaultStore: defaultStore,
	}
}

// Get returns a cached or newly-created BlobStore for desc.
// Falls back to the default store when desc.ID is empty.
func (r *Registry) Get(ctx context.Context, desc BlobStoreDescriptor) (BlobStore, error) {
	if desc.ID == "" {
		return r.defaultStore, nil
	}

	r.mu.RLock()
	if bs, ok := r.instances[desc.ID]; ok {
		r.mu.RUnlock()
		return bs, nil
	}
	r.mu.RUnlock()

	bs, err := newFromDescriptor(ctx, desc)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	// Double-check after acquiring write lock.
	if existing, ok := r.instances[desc.ID]; ok {
		r.mu.Unlock()
		return existing, nil
	}
	r.instances[desc.ID] = bs
	r.mu.Unlock()
	return bs, nil
}

// Invalidate removes a cached instance so the next Get recreates it.
// Call after updating a blob store's config.
func (r *Registry) Invalidate(id string) {
	r.mu.Lock()
	delete(r.instances, id)
	r.mu.Unlock()
}

// NewFromConfig creates a BlobStore directly from type + config map (no caching).
// Used by the test-connection endpoint.
func NewFromConfig(ctx context.Context, bsType string, cfg map[string]any) (BlobStore, error) {
	return newFromDescriptor(ctx, BlobStoreDescriptor{Type: bsType, Config: cfg})
}

func newFromDescriptor(ctx context.Context, desc BlobStoreDescriptor) (BlobStore, error) {
	switch desc.Type {
	case "s3":
		opts := S3Options{
			Bucket:          strVal(desc.Config, "bucket"),
			Region:          strVal(desc.Config, "region"),
			Endpoint:        strVal(desc.Config, "endpoint"),
			AccessKeyID:     strVal(desc.Config, "access_key"),
			SecretAccessKey: strVal(desc.Config, "secret_key"),
		}
		// Force path style when a custom endpoint is provided (standard for MinIO/Ceph).
		if opts.Endpoint != "" {
			opts.ForcePathStyle = true
		}
		if bv, ok := desc.Config["force_path_style"].(bool); ok {
			opts.ForcePathStyle = bv
		}
		if opts.Bucket == "" {
			return nil, fmt.Errorf("s3 blob store: bucket is required")
		}
		return NewS3BlobStore(ctx, opts)
	case "local", "":
		path := strVal(desc.Config, "path")
		if path == "" {
			path = "./data/blobs"
		}
		return NewLocalBlobStore(path)
	default:
		return nil, fmt.Errorf("unknown blob store type %q", desc.Type)
	}
}

func strVal(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}
```

- [ ] **Step 2: Build to verify no compile errors**

```bash
cd /home/skensel/AI/self_nexus && go build ./internal/storage/...
```
Expected: no output (clean build).

- [ ] **Step 3: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add internal/storage/registry.go
git commit -m "feat(storage): add Registry for per-blob-store physical instance caching"
```

---

## Task 2: Add Registry to formats.Deps

**Files:**
- Modify: `internal/formats/deps.go`

- [ ] **Step 1: Add the field**

In `internal/formats/deps.go` replace:
```go
// Deps holds all dependencies injected into every format handler.
type Deps struct {
	Repos      repository.RepositoryRepo
	Components repository.ComponentRepo
	Assets     repository.AssetRepo
	Blobs      repository.BlobStoreRepo
	BlobStore  storage.BlobStore
	BaseURL    string
	// Webhooks is optional — nil disables event delivery.
	Webhooks domain.WebhookDispatcher
}
```
with:
```go
// Deps holds all dependencies injected into every format handler.
type Deps struct {
	Repos      repository.RepositoryRepo
	Components repository.ComponentRepo
	Assets     repository.AssetRepo
	Blobs      repository.BlobStoreRepo
	BlobStore  storage.BlobStore    // default / fallback store
	Registry   *storage.Registry   // optional: per-blob-store routing; nil disables
	BaseURL    string
	// Webhooks is optional — nil disables event delivery.
	Webhooks domain.WebhookDispatcher
}
```

- [ ] **Step 2: Build**

```bash
cd /home/skensel/AI/self_nexus && go build ./internal/formats/...
```
Expected: no output.

- [ ] **Step 3: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add internal/formats/deps.go
git commit -m "feat(formats): add Registry field to Deps for per-blob-store routing"
```

---

## Task 3: Route store/fetch/delete to the correct physical BlobStore

**Files:**
- Modify: `internal/formats/base/store.go`

The key insight: `resolveBlobStoreRef` already fetches the `domain.BlobStore` for the repo. We add a `physicalStore` helper that uses the Registry if set, otherwise falls back to `d.BlobStore`.

- [ ] **Step 1: Add `physicalStore` helper and update StoreArtifact**

Add the helper just before `resolveBlobStoreRef` (near line 336):

```go
// physicalStore returns the physical BlobStore for the given domain blob store.
// If the registry is set and the descriptor is valid, it returns the cached/created instance.
// Falls back to d.BlobStore (the global default) on any error or missing registry.
func physicalStore(ctx context.Context, d formats.Deps, bs *domain.BlobStore) storage.BlobStore {
	if d.Registry == nil || bs == nil {
		return d.BlobStore
	}
	store, err := d.Registry.Get(ctx, storage.BlobStoreDescriptor{
		ID:     bs.ID,
		Type:   bs.Type,
		Config: bs.Config,
	})
	if err != nil {
		return d.BlobStore
	}
	return store
}
```

In `StoreArtifact` (around line 47), after the `blobKey` line and before the pipe setup, insert a call to resolve the blob store early so we have the domain.BlobStore to pass to `physicalStore`. The function currently calls `resolveBlobStoreRef` inside `RegisterStoredBlob`. We need to resolve earlier.

Replace the `d.BlobStore.Put(ctx, blobKey, pr, declaredSize)` call:
```go
	if err := d.BlobStore.Put(ctx, blobKey, pr, declaredSize); err != nil {
```
with:
```go
	// Resolve the physical blob store for this repository.
	// resolveBlobStoreRef is also called in RegisterStoredBlob; the second call
	// hits the DB again but both calls are cheap (single-row PK lookup, cached by pgxpool).
	var physStore storage.BlobStore
	{
		bsRef, _, refErr := resolveBlobStoreRef(ctx, d, repo)
		if refErr == nil {
			if bsMeta, getErr := d.Blobs.GetByID(ctx, bsRef); getErr == nil {
				physStore = physicalStore(ctx, d, bsMeta)
			}
		}
		if physStore == nil {
			physStore = d.BlobStore
		}
	}
	if err := physStore.Put(ctx, blobKey, pr, declaredSize); err != nil {
```

Also update the post-write quota size check and rollback delete (lines ~92-101) to use `physStore`:
```go
	if declaredSize == 0 {
		if s, err := physStore.Size(ctx, blobKey); err == nil {
			size = s
		}
	}

	if err := checkQuota(ctx, d, repo, size); err != nil {
		_ = physStore.Delete(ctx, blobKey)
		return nil, err
	}
```

- [ ] **Step 2: Update FetchArtifact to use the correct store**

`FetchArtifact` (around line 193) currently does `d.BlobStore.Get(ctx, asset.BlobKey)`. The asset has `asset.BlobStoreID` — use that to pick the right store.

Replace:
```go
	rc, _, err := d.BlobStore.Get(ctx, asset.BlobKey)
	if err != nil {
		return nil, nil, fmt.Errorf("blob missing: %w", err)
	}
```
with:
```go
	var fetchStore storage.BlobStore
	if asset.BlobStoreID != "" {
		if bsMeta, getErr := d.Blobs.GetByID(ctx, asset.BlobStoreID); getErr == nil {
			fetchStore = physicalStore(ctx, d, bsMeta)
		}
	}
	if fetchStore == nil {
		fetchStore = d.BlobStore
	}
	rc, _, err := fetchStore.Get(ctx, asset.BlobKey)
	if err != nil {
		return nil, nil, fmt.Errorf("blob missing: %w", err)
	}
```

- [ ] **Step 3: Update DeleteArtifact to use the correct store**

`DeleteArtifact` currently does `d.BlobStore.Delete(ctx, asset.BlobKey)`. Replace with:

```go
	var delStore storage.BlobStore
	if asset.BlobStoreID != "" {
		if bsMeta, getErr := d.Blobs.GetByID(ctx, asset.BlobStoreID); getErr == nil {
			delStore = physicalStore(ctx, d, bsMeta)
		}
	}
	if delStore == nil {
		delStore = d.BlobStore
	}
	_ = delStore.Delete(ctx, asset.BlobKey)
```

- [ ] **Step 4: Build**

```bash
cd /home/skensel/AI/self_nexus && go build ./internal/formats/...
```
Expected: no output.

- [ ] **Step 5: Run tests**

```bash
cd /home/skensel/AI/self_nexus && go test ./internal/formats/... ./internal/service/... 2>&1 | tail -20
```
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add internal/formats/base/store.go
git commit -m "feat(base): route store/fetch/delete to per-repository physical BlobStore via Registry"
```

---

## Task 4: Wire Registry into router.go

**Files:**
- Modify: `internal/api/router.go`

- [ ] **Step 1: Create registry and add to formatDeps**

After line 70 (`localBlob, err := storage.NewBlobStoreFromConfig(...)`), add:

```go
	blobRegistry := storage.NewRegistry(localBlob)
```

In the `formatDeps` struct literal (around line 113), add the Registry field:
```go
	formatDeps := formats.Deps{
		Repos:      repoRepo,
		Components: componentRepo,
		Assets:     assetRepo,
		Blobs:      blobRepo,
		BlobStore:  localBlob,
		Registry:   blobRegistry,
		BaseURL:    cfg.HTTP.BaseURL,
		Webhooks:   webhookSvc,
	}
```

- [ ] **Step 2: Build**

```bash
cd /home/skensel/AI/self_nexus && go build ./...
```
Expected: no output.

- [ ] **Step 3: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add internal/api/router.go
git commit -m "feat(router): create BlobStore Registry and inject into format handlers"
```

---

## Task 5: Test-connection endpoint (backend)

**Files:**
- Modify: `internal/api/handlers/blobstores.go`
- Modify: `internal/api/router.go`

- [ ] **Step 1: Add TestConnection handler to blobstores.go**

Add after the `Compact` method at the bottom of `blobstores.go`:

```go
// TestConnection handles POST /api/v1/blobstores/test.
// Body: {"type": "s3"|"local", "config": {...}}
// Tries to connect and returns {"ok": true} or {"ok": false, "error": "..."}.
func (h *BlobStoreHandler) TestConnection(c *gin.Context) {
	var req struct {
		Type   string         `json:"type"`
		Config map[string]any `json:"config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Type == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type is required"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	bs, err := storage.NewFromConfig(ctx, req.Type, req.Config)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}

	// Probe the store: list with a small cap is a cheap connectivity check.
	_, listErr := bs.ListKeys(ctx)
	if listErr != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": listErr.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}
```

Also add `"context"` and `"time"` to imports in `blobstores.go` if not already present.

- [ ] **Step 2: Register the route in router.go**

Find the admin blob store routes block (around line 314):
```go
adminAPI.POST("/service/rest/v1/blobstores/:type", blobH.Create)
adminAPI.PUT("/service/rest/v1/blobstores/:type/:name", blobH.Update)
adminAPI.DELETE("/service/rest/v1/blobstores/:name", blobH.Delete)
```

Add the test route **before** those three lines (it must come before `/:type` to avoid routing conflicts):
```go
adminAPI.POST("/api/v1/blobstores/test", blobH.TestConnection)
```

- [ ] **Step 3: Build**

```bash
cd /home/skensel/AI/self_nexus && go build ./...
```
Expected: no output.

- [ ] **Step 4: Run all tests**

```bash
cd /home/skensel/AI/self_nexus && go test ./... 2>&1 | tail -20
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add internal/api/handlers/blobstores.go internal/api/router.go
git commit -m "feat(blobstores): add POST /api/v1/blobstores/test connection probe endpoint"
```

---

## Task 6: S3 blob stores in Service Connections (backend)

**Files:**
- Modify: `internal/api/handlers/system.go`
- Modify: `internal/api/router.go`

The goal: `GET /api/v1/system/services` currently returns PostgreSQL + global storage + LDAP + OIDC. We need to also return one entry per **unique S3 endpoint** found in the `blob_stores` table, listing which blob stores use it.

- [ ] **Step 1: Add blobStoreRepo to SystemHandler**

In `system.go`, change the struct and constructor:

```go
type SystemHandler struct {
	cfg        *config.Config
	pool       *pgxpool.Pool
	ldap       auth.LDAPAuthenticator
	oidc       auth.OIDCAuthenticator
	blobStores repository.BlobStoreRepo // may be nil
}

func NewSystemHandler(cfg *config.Config, pool *pgxpool.Pool, ldap auth.LDAPAuthenticator, oidc auth.OIDCAuthenticator) *SystemHandler {
	return &SystemHandler{cfg: cfg, pool: pool, ldap: ldap, oidc: oidc}
}

func (h *SystemHandler) WithBlobStores(r repository.BlobStoreRepo) *SystemHandler {
	h.blobStores = r
	return h
}
```

Add `"github.com/nexspence-oss/nexspence/internal/repository"` to imports.

- [ ] **Step 2: Add S3 checks in Services()**

In the `Services` method, after the existing checks slice is built, add S3 blob store checks:

```go
	// Add one check per unique S3 endpoint found in blob_stores table.
	if h.blobStores != nil {
		if stores, err := h.blobStores.List(ctx); err == nil {
			// Group by endpoint (empty endpoint = AWS S3 itself).
			type endpointGroup struct {
				endpoint string
				names    []string
				buckets  []string
			}
			groups := map[string]*endpointGroup{}
			for _, bs := range stores {
				if bs.Type != "s3" {
					continue
				}
				ep, _ := bs.Config["endpoint"].(string)
				bkt, _ := bs.Config["bucket"].(string)
				g, ok := groups[ep]
				if !ok {
					g = &endpointGroup{endpoint: ep}
					groups[ep] = g
				}
				g.names = append(g.names, bs.Name)
				if bkt != "" {
					g.buckets = append(g.buckets, bkt)
				}
			}
			for ep, g := range groups {
				ep := ep
				g := g
				checks = append(checks, func(ctx context.Context) ServiceStatus {
					return h.checkS3Endpoint(ctx, ep, g.names, g.buckets)
				})
			}
		}
	}
```

- [ ] **Step 3: Add checkS3Endpoint method**

Add after `checkStorage`:

```go
// checkS3Endpoint probes one unique S3 endpoint by creating a temporary BlobStore
// from the first blob store that uses it and listing keys (cheap HeadBucket equivalent).
func (h *SystemHandler) checkS3Endpoint(ctx context.Context, endpoint string, names []string, buckets []string) ServiceStatus {
	now := time.Now().UTC().Format(time.RFC3339)
	displayName := "S3"
	if endpoint != "" {
		displayName = "S3 · " + endpoint
	} else {
		displayName = "S3 · AWS"
	}

	storeNames := strings.Join(names, ", ")
	bucketList := strings.Join(buckets, ", ")
	detail := fmt.Sprintf("stores: %s · buckets: %s", storeNames, bucketList)

	// Load the first matching store config to probe the endpoint.
	if h.blobStores == nil {
		return ServiceStatus{Name: displayName, Status: "ok", Detail: detail, CheckedAt: now}
	}
	stores, err := h.blobStores.List(ctx)
	if err != nil {
		return ServiceStatus{Name: displayName, Status: "error", Detail: err.Error(), CheckedAt: now}
	}

	start := time.Now()
	var probeErr error
	for _, bs := range stores {
		if bs.Type != "s3" {
			continue
		}
		ep, _ := bs.Config["endpoint"].(string)
		if ep != endpoint {
			continue
		}
		physical, err := storage.NewFromConfig(ctx, "s3", bs.Config)
		if err != nil {
			probeErr = err
			break
		}
		_, probeErr = physical.ListKeys(ctx)
		break
	}
	lat := int(time.Since(start).Milliseconds())

	if probeErr != nil {
		return ServiceStatus{Name: displayName, Status: "error", LatencyMs: lat, Detail: fmt.Sprintf("%s · %s", detail, probeErr.Error()), CheckedAt: now}
	}
	return ServiceStatus{Name: displayName, Status: "ok", LatencyMs: lat, Detail: detail, CheckedAt: now}
}
```

Add `"github.com/nexspence-oss/nexspence/internal/storage"` to imports in system.go.

- [ ] **Step 4: Wire WithBlobStores in router.go**

Find where `systemHandler` is created (it's wired somewhere in router.go). Add `.WithBlobStores(blobRepo)`:

```go
systemH := handlers.NewSystemHandler(cfg, pool, ldapSvc, oidcSvc).WithBlobStores(blobRepo)
```

- [ ] **Step 5: Build**

```bash
cd /home/skensel/AI/self_nexus && go build ./...
```
Expected: no output.

- [ ] **Step 6: Run tests**

```bash
cd /home/skensel/AI/self_nexus && go test ./... 2>&1 | tail -20
```
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add internal/api/handlers/system.go internal/api/router.go
git commit -m "feat(system): add S3 blob store endpoints to Service Connections health checks"
```

---

## Task 7: Invalidate registry on blob store update (backend)

When a blob store's config is updated via the API, the cached physical store is stale. We need to invalidate it.

**Files:**
- Modify: `internal/api/handlers/blobstores.go`
- Modify: `internal/api/router.go`

- [ ] **Step 1: Add Registry field to BlobStoreHandler and wire invalidation**

In `blobstores.go`, add `registry *storage.Registry` field to the handler:

```go
type BlobStoreHandler struct {
	repo      repository.BlobStoreRepo
	repos     repository.RepositoryRepo
	assets    repository.AssetRepo
	gcSvc     *service.BlobGCService
	blobStore storage.BlobStore
	registry  *storage.Registry
}
```

Add a wiring method:
```go
func (h *BlobStoreHandler) WithRegistry(r *storage.Registry) *BlobStoreHandler {
	h.registry = r
	return h
}
```

In the `Update` handler, after `h.repo.Update(ctx, &updates)` succeeds, add:
```go
	if h.registry != nil && existing.ID != "" {
		h.registry.Invalidate(existing.ID)
	}
```

- [ ] **Step 2: Wire in router.go**

```go
blobH := handlers.NewBlobStoreHandler(blobRepo).
    WithUsageDeps(repoRepo, assetRepo).
    WithGC(gcSvc).
    WithBlobStore(localBlob).
    WithRegistry(blobRegistry)
```

(Add `.WithRegistry(blobRegistry)` to the existing chain.)

- [ ] **Step 3: Build + test**

```bash
cd /home/skensel/AI/self_nexus && go build ./... && go test ./... 2>&1 | tail -20
```

- [ ] **Step 4: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add internal/api/handlers/blobstores.go internal/api/router.go
git commit -m "feat(blobstores): invalidate Registry cache when blob store config is updated"
```

---

## Task 8: Frontend — API client additions

**Files:**
- Modify: `frontend/src/api/client.ts`

- [ ] **Step 1: Add testBlobStore call**

Find the blob store section in `client.ts` (around the `listBlobStores`, `createBlobStore` lines). Add:

```typescript
  testBlobStore: (type: string, config: Record<string, unknown>) =>
    apiClient.post<{ ok: boolean; error?: string }>('/api/v1/blobstores/test', { type, config }),
```

- [ ] **Step 2: Verify TypeScript compilation**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit 2>&1 | head -30
```
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add frontend/src/api/client.ts
git commit -m "feat(frontend/api): add testBlobStore call"
```

---

## Task 9: Frontend — Test Connection button in CreateBlobStoreModal

**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

- [ ] **Step 1: Add testResult state and Test button to CreateBlobStoreModal**

In `CreateBlobStoreModal`, add two state vars after `const [err, setErr] = useState('')`:

```typescript
const [testResult, setTestResult] = useState<{ ok: boolean; error?: string } | null>(null)
const [testBusy, setTestBusy] = useState(false)
```

Add a `handleTest` function inside the component:

```typescript
const handleTest = async () => {
  setTestBusy(true)
  setTestResult(null)
  try {
    const cfg: Record<string, unknown> = type === 'local'
      ? { path }
      : { bucket, region, endpoint, prefix, access_key: accessKey, secret_key: secretKey }
    const res = await nexusApi.testBlobStore(type, cfg)
    setTestResult(res.data)
  } catch {
    setTestResult({ ok: false, error: 'Request failed' })
  } finally {
    setTestBusy(false)
  }
}
```

In the JSX, between the quota field and the error display, add:

```tsx
{/* Test result feedback */}
{testResult && (
  <div style={{
    padding: '8px 12px', borderRadius: 8, fontSize: 13,
    background: testResult.ok ? 'rgba(34,197,94,0.08)' : 'rgba(239,68,68,0.08)',
    border: `1px solid ${testResult.ok ? 'rgba(34,197,94,0.3)' : 'rgba(239,68,68,0.3)'}`,
    color: testResult.ok ? 'var(--holo-green)' : 'var(--holo-red)',
  }}>
    {testResult.ok ? 'Connection successful' : `Connection failed: ${testResult.error}`}
  </div>
)}
```

In the button row, add a Test Connection button between Cancel and Create:

```tsx
<HoloButton onClick={handleTest} disabled={testBusy || !name.trim()}>
  {testBusy ? 'Testing…' : 'Test Connection'}
</HoloButton>
```

- [ ] **Step 2: TypeScript check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit 2>&1 | head -30
```

- [ ] **Step 3: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add frontend/src/pages/AdminPage.tsx
git commit -m "feat(admin): add Test Connection button to CreateBlobStoreModal"
```

---

## Task 10: Frontend — S3 config display + edit in BlobStoreDetailModal

Currently the detail modal shows: Type, Used, Quota, Remaining, linked repos. For S3 stores, it should also show connection config (endpoint, bucket, region) and allow editing.

**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

- [ ] **Step 1: Add S3 config rows to detail grid**

In `BlobStoreDetailModal`, after the `remaining` rows in the grid, add:

```tsx
{bs.type === 's3' && bs.config && (
  <>
    <span style={{ color: 'var(--holo-text-dim)' }}>Endpoint</span>
    <span style={{ color: 'var(--holo-text)', fontFamily: 'monospace', fontSize: 12 }}>
      {(bs.config.endpoint as string) || 'AWS S3'}
    </span>
    <span style={{ color: 'var(--holo-text-dim)' }}>Bucket</span>
    <span style={{ color: 'var(--holo-text)', fontFamily: 'monospace', fontSize: 12 }}>
      {(bs.config.bucket as string) || '—'}
    </span>
    <span style={{ color: 'var(--holo-text-dim)' }}>Region</span>
    <span style={{ color: 'var(--holo-text)', fontFamily: 'monospace', fontSize: 12 }}>
      {(bs.config.region as string) || '—'}
    </span>
  </>
)}
{bs.type === 'local' && bs.config && (
  <>
    <span style={{ color: 'var(--holo-text-dim)' }}>Path</span>
    <span style={{ color: 'var(--holo-text)', fontFamily: 'monospace', fontSize: 12 }}>
      {(bs.config.path as string) || '—'}
    </span>
  </>
)}
```

- [ ] **Step 2: Add inline edit support**

Add an `editing` state at the top of `BlobStoreDetailModal`:

```typescript
const [editing, setEditing] = useState(false)
const [editBucket, setEditBucket]     = useState('')
const [editRegion, setEditRegion]     = useState('')
const [editEndpoint, setEditEndpoint] = useState('')
const [editAccessKey, setEditAccessKey] = useState('')
const [editSecretKey, setEditSecretKey] = useState('')
const [editPath, setEditPath]         = useState('')
const [editErr, setEditErr]           = useState('')
```

Add a mutation for saving the edit:

```typescript
const editMut = useMutation({
  mutationFn: () => {
    if (!bs) return Promise.reject('no store')
    const config: Record<string, unknown> = bs.type === 's3'
      ? { bucket: editBucket, region: editRegion, endpoint: editEndpoint,
          access_key: editAccessKey, secret_key: editSecretKey || undefined }
      : { path: editPath }
    return nexusApi.updateBlobStore(bs.type, bs.name, { config, quotaBytes: bs.quotaBytes ?? null })
  },
  onSuccess: () => {
    qc.invalidateQueries({ queryKey: ['blobstore-usage', name] })
    qc.invalidateQueries({ queryKey: ['blobstores'] })
    setEditing(false)
  },
  onError: (e: unknown) => {
    const msg = (e as { response?: { data?: { error?: string } } })?.response?.data?.error ?? 'Save failed'
    setEditErr(msg)
  },
})
```

Add a function to enter edit mode that pre-fills from current values:

```typescript
const startEdit = () => {
  if (!bs?.config) return
  setEditBucket((bs.config.bucket as string) ?? '')
  setEditRegion((bs.config.region as string) ?? 'us-east-1')
  setEditEndpoint((bs.config.endpoint as string) ?? '')
  setEditAccessKey((bs.config.access_key as string) ?? '')
  setEditSecretKey('')
  setEditPath((bs.config.path as string) ?? '')
  setEditErr('')
  setEditing(true)
}
```

Add an edit form that shows when `editing === true`, placed just before the linked repos section:

```tsx
{editing && bs && (
  <div style={{ marginBottom: 16, padding: '12px 14px', background: 'rgba(255,255,255,0.03)', borderRadius: 10, border: '1px solid var(--holo-border)' }}>
    <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--holo-text-dim)', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: 10 }}>Edit Configuration</div>
    {bs.type === 's3' ? (
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
          <div>
            <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Bucket</label>
            <HoloInput value={editBucket} onChange={e => setEditBucket(e.target.value)} />
          </div>
          <div>
            <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Region</label>
            <HoloInput value={editRegion} onChange={e => setEditRegion(e.target.value)} />
          </div>
        </div>
        <div>
          <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Endpoint</label>
          <HoloInput value={editEndpoint} onChange={e => setEditEndpoint(e.target.value)} placeholder="leave empty for AWS S3" />
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
          <div>
            <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Access Key</label>
            <HoloInput value={editAccessKey} onChange={e => setEditAccessKey(e.target.value)} />
          </div>
          <div>
            <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Secret Key (leave blank to keep)</label>
            <HoloInput type="password" value={editSecretKey} onChange={e => setEditSecretKey(e.target.value)} placeholder="unchanged" />
          </div>
        </div>
      </div>
    ) : (
      <div>
        <label style={{ fontSize: 11, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 3 }}>Path</label>
        <HoloInput value={editPath} onChange={e => setEditPath(e.target.value)} />
      </div>
    )}
    {editErr && <div style={{ marginTop: 8, color: 'var(--holo-red)', fontSize: 12 }}>{editErr}</div>}
    <div style={{ display: 'flex', gap: 8, marginTop: 10 }}>
      <HoloButton variant="primary" disabled={editMut.isPending} onClick={() => editMut.mutate()}>
        {editMut.isPending ? 'Saving…' : 'Save'}
      </HoloButton>
      <HoloButton onClick={() => setEditing(false)}>Cancel</HoloButton>
    </div>
  </div>
)}
```

Add an Edit button in the modal action row (next to the Delete button):

```tsx
{!editing && bs && (
  <HoloButton icon={<Pencil size={13} />} onClick={startEdit}>Edit Config</HoloButton>
)}
```

- [ ] **Step 3: TypeScript check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit 2>&1 | head -30
```

- [ ] **Step 4: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add frontend/src/pages/AdminPage.tsx
git commit -m "feat(admin): show S3/local config in blob store detail modal with edit form"
```

---

## Task 11: Frontend — S3 connections in Service Connections

The backend now returns S3 endpoint entries in the `/api/v1/system/services` response. The frontend renders these generically (dot + name + detail + latency). This already works without frontend changes since the service connections list renders all items uniformly.

However, we should verify the display is correct and add a small S3-specific icon badge to distinguish S3 entries.

**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

- [ ] **Step 1: Add S3 visual distinction in service connections list**

In the service connections `.map(svc => ...)` block (around line 241 in the original), detect S3 entries and add a badge:

```tsx
return (
  <div key={svc.name} style={{ display: 'grid', gridTemplateColumns: '8px 1fr auto', alignItems: 'center', gap: 12, padding: '10px 12px', background: 'rgba(255,255,255,0.03)', borderRadius: 8, border: '1px solid rgba(255,255,255,0.06)' }}>
    <span style={{ width: 7, height: 7, borderRadius: '50%', background: color, boxShadow: glow, flexShrink: 0, display: 'inline-block' }} />
    <div style={{ minWidth: 0 }}>
      <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--holo-text)', display: 'flex', alignItems: 'center', gap: 6 }}>
        {svc.name}
        {svc.name.startsWith('S3') && (
          <span style={{ fontSize: 10, padding: '1px 6px', borderRadius: 4, background: 'rgba(245,158,11,0.15)', color: '#f59e0b', fontWeight: 700 }}>S3</span>
        )}
      </div>
      <div style={{ fontSize: 11, color: 'var(--holo-text-faint)', marginTop: 2, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' as const }}>{svc.detail}</div>
    </div>
    <div style={{ textAlign: 'right' as const, fontSize: 11, color: 'var(--holo-text-faint)', whiteSpace: 'nowrap' as const }}>
      {svc.latency_ms != null && <span style={{ color: svc.latency_ms < 50 ? 'var(--holo-green)' : svc.latency_ms < 200 ? 'var(--holo-amber)' : 'var(--holo-red)' }}>{svc.latency_ms}ms</span>}
      <div style={{ marginTop: 2 }}>{new Date(svc.checked_at).toLocaleTimeString()}</div>
    </div>
  </div>
)
```

- [ ] **Step 2: TypeScript check + build**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit 2>&1 | head -20 && npm run build 2>&1 | tail -10
```
Expected: 0 TS errors, build succeeds.

- [ ] **Step 3: Commit**

```bash
cd /home/skensel/AI/self_nexus
git add frontend/src/pages/AdminPage.tsx
git commit -m "feat(admin): add S3 badge to service connections entries"
```

---

## Self-Review

**Spec coverage:**
- ✅ Routing: `base.StoreArtifact`, `FetchArtifact`, `DeleteArtifact` use Registry (Tasks 1-4)
- ✅ S3 config in edit/detail modal (Task 10)
- ✅ Test connection on create (Tasks 5, 8, 9)
- ✅ S3 in Service Connections (Tasks 6, 11)
- ✅ Registry cache invalidation on update (Task 7)

**Placeholder scan:** No TBDs or TODOs.

**Type consistency:**
- `BlobStoreDescriptor` defined in Task 1, used in Tasks 2-3
- `NewFromConfig` defined in Task 1, used in Tasks 5, 6
- `Registry.Invalidate` defined in Task 1, used in Task 7
- `WithBlobStores` defined in Task 6, wired in Task 6
- `testBlobStore` defined in Task 8, used in Task 9
- `nexusApi.updateBlobStore` already exists in client.ts (Task 10 uses it)

**Note on secret_key in edit:** The update handler in `blobstores.go` passes the full config to `h.repo.Update`. If `secret_key` is empty string, it will overwrite the stored value. In `startEdit` we set `editSecretKey('')` and in the mutationFn we pass `secret_key: editSecretKey || undefined` — this means if left blank the key is omitted from config. However, `blobstore_repo.Update` replaces the entire config JSON. To preserve the secret key when blank: the backend `Update` handler should merge config rather than replace, OR the frontend must pass the existing secret. Since the secret is stored in DB (not ideal for production, but acceptable for dev), the safest fix is: if `editSecretKey` is empty, read the existing secret from `bs.config.secret_key` and pass it through. Add this to the mutationFn:

```typescript
const secret = editSecretKey || (bs?.config?.secret_key as string) || ''
const config = { bucket: editBucket, region: editRegion, endpoint: editEndpoint,
                 access_key: editAccessKey, secret_key: secret }
```
