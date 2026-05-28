# Group Blob Stores Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `type="group"` blob stores that distribute writes across member stores via `round_robin` or `write_to_first_fill` fill policy.

**Architecture:** Group blob stores live in the existing `blob_stores` table (type="group", config jsonb stores fill_policy + member_ids). On PUT, `resolveBlobStoreRef` detects type="group", calls `Registry.PickMember` to select a physical member, and returns the **member's** ID — so `assets.blob_store_id` always points to a real physical store, leaving GET/DELETE untouched.

**Tech Stack:** Go 1.23, PostgreSQL, React+TypeScript, Vite. Test runner: `PATH="/usr/local/go/bin:$PATH" go test -count=1 ./...` from project root `/Users/skensel/WORKING/AI/nexspence-core`.

---

## File Map

| File | Action |
|------|--------|
| `internal/db/migrations/014_blob_store_group.sql` | **CREATE** — extend type CHECK constraint |
| `internal/storage/registry.go` | **MODIFY** — add `MemberInfo`, `rrCounters sync.Map`, `PickMember` |
| `internal/storage/registry_test.go` | **CREATE** — unit tests for PickMember |
| `internal/formats/base/store.go` | **MODIFY** — fix double-call, group routing in `resolveBlobStoreRef`, group quota in `checkQuota` |
| `internal/formats/base/store_test.go` | **MODIFY** — add group store routing tests |
| `internal/formats/docker/handler.go` | **MODIFY** — update `RegisterStoredBlob` call (add `"", ""` params) |
| `internal/formats/repoproxy/repoproxy.go` | **MODIFY** — update `RegisterStoredBlob` call (add `"", ""` params) |
| `internal/api/handlers/blobstores.go` | **MODIFY** — group validation in Create/Update/Delete; group Usage aggregation |
| `internal/api/handlers/blobstores_group_test.go` | **CREATE** — handler validation tests |
| `frontend/src/pages/AdminPage.tsx` | **MODIFY** — Group type in create modal, GROUP badge, detail modal member list |
| `docs/blob-store-groups.md` | **CREATE** — user-facing guide |

---

## Task 1: Database migration

**Files:**
- Create: `internal/db/migrations/014_blob_store_group.sql`

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up
ALTER TABLE blob_stores DROP CONSTRAINT IF EXISTS blob_stores_type_check;
ALTER TABLE blob_stores ADD CONSTRAINT blob_stores_type_check
    CHECK (type IN ('local', 's3', 'group'));

-- +goose Down
ALTER TABLE blob_stores DROP CONSTRAINT IF EXISTS blob_stores_type_check;
ALTER TABLE blob_stores ADD CONSTRAINT blob_stores_type_check
    CHECK (type IN ('local', 's3'));
```

- [ ] **Step 2: Verify the file exists and compiles with the server**

```bash
PATH="/usr/local/go/bin:$PATH" go build ./cmd/server/...
```
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/db/migrations/014_blob_store_group.sql
git commit -m "feat(db): migration 014 — extend blob_stores type check to include 'group'"
```

---

## Task 2: Registry.PickMember

**Files:**
- Modify: `internal/storage/registry.go`
- Create: `internal/storage/registry_test.go`

- [ ] **Step 1: Write the failing tests**

Create `/Users/skensel/WORKING/AI/nexspence-core/internal/storage/registry_test.go`:

```go
package storage_test

import (
	"testing"

	"github.com/nexspence-oss/nexspence/internal/storage"
)

func int64p(v int64) *int64 { return &v }

func members(ids ...string) []storage.MemberInfo {
	out := make([]storage.MemberInfo, len(ids))
	for i, id := range ids {
		out[i] = storage.MemberInfo{ID: id}
	}
	return out
}

func TestPickMember_RoundRobin_Cycles(t *testing.T) {
	r := storage.NewRegistry(nil)
	ms := members("a", "b", "c")
	got := []string{
		r.PickMember("g1", "round_robin", ms),
		r.PickMember("g1", "round_robin", ms),
		r.PickMember("g1", "round_robin", ms),
		r.PickMember("g1", "round_robin", ms),
	}
	if got[0] != "a" || got[1] != "b" || got[2] != "c" || got[3] != "a" {
		t.Errorf("expected a,b,c,a cycle, got %v", got)
	}
}

func TestPickMember_RoundRobin_IndependentCountersPerGroup(t *testing.T) {
	r := storage.NewRegistry(nil)
	ms := members("x", "y")
	r.PickMember("g1", "round_robin", ms)
	// g2 counter starts fresh
	got := r.PickMember("g2", "round_robin", ms)
	if got != "x" {
		t.Errorf("want x (fresh counter), got %s", got)
	}
}

func TestPickMember_RoundRobin_Empty_ReturnsEmpty(t *testing.T) {
	r := storage.NewRegistry(nil)
	got := r.PickMember("g1", "round_robin", nil)
	if got != "" {
		t.Errorf("want empty string for empty members, got %q", got)
	}
}

func TestPickMember_WriteToFirstFill_NoQuota_AlwaysFirst(t *testing.T) {
	r := storage.NewRegistry(nil)
	ms := []storage.MemberInfo{
		{ID: "a", QuotaBytes: nil, UsedBytes: 999},
		{ID: "b"},
	}
	got := r.PickMember("g1", "write_to_first_fill", ms)
	if got != "a" {
		t.Errorf("nil quota = unlimited, want a, got %s", got)
	}
}

func TestPickMember_WriteToFirstFill_FirstFull_PicksSecond(t *testing.T) {
	r := storage.NewRegistry(nil)
	ms := []storage.MemberInfo{
		{ID: "a", QuotaBytes: int64p(100), UsedBytes: 100},
		{ID: "b", QuotaBytes: int64p(100), UsedBytes: 50},
	}
	got := r.PickMember("g1", "write_to_first_fill", ms)
	if got != "b" {
		t.Errorf("want b (first not full), got %s", got)
	}
}

func TestPickMember_WriteToFirstFill_AllFull_ReturnsEmpty(t *testing.T) {
	r := storage.NewRegistry(nil)
	ms := []storage.MemberInfo{
		{ID: "a", QuotaBytes: int64p(100), UsedBytes: 100},
		{ID: "b", QuotaBytes: int64p(200), UsedBytes: 200},
	}
	got := r.PickMember("g1", "write_to_first_fill", ms)
	if got != "" {
		t.Errorf("want empty (all full), got %s", got)
	}
}

func TestPickMember_UnknownPolicy_FallsBackToFirst(t *testing.T) {
	r := storage.NewRegistry(nil)
	ms := members("x", "y")
	got := r.PickMember("g1", "unknown_policy", ms)
	if got != "x" {
		t.Errorf("want x (fallback to first), got %s", got)
	}
}
```

- [ ] **Step 2: Run to confirm tests fail**

```bash
PATH="/usr/local/go/bin:$PATH" go test -count=1 ./internal/storage/... 2>&1 | tail -5
```
Expected: compilation error — `MemberInfo` and `PickMember` not defined.

- [ ] **Step 3: Implement MemberInfo and PickMember in registry.go**

Add to `internal/storage/registry.go` after the existing imports block — add `"sync/atomic"` import, then add these declarations after the `Registry` struct:

```go
import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// MemberInfo carries the blob-store fields needed for fill-policy member selection.
// Callers convert []domain.BlobStore to []MemberInfo before calling PickMember.
type MemberInfo struct {
	ID         string
	QuotaBytes *int64
	UsedBytes  int64
}
```

Add `rrCounters sync.Map` field to the `Registry` struct:

```go
type Registry struct {
	mu           sync.RWMutex
	instances    map[string]BlobStore
	defaultStore BlobStore
	rrCounters   sync.Map // groupID → *atomic.Uint64
}
```

Add `PickMember` method after the `Invalidate` method:

```go
// PickMember selects a member blob store ID according to the fill policy.
// Returns "" if members is empty or all members are at capacity (write_to_first_fill).
func (r *Registry) PickMember(groupID, policy string, members []MemberInfo) string {
	if len(members) == 0 {
		return ""
	}
	switch policy {
	case "round_robin":
		v, _ := r.rrCounters.LoadOrStore(groupID, new(atomic.Uint64))
		ctr := v.(*atomic.Uint64)
		idx := ctr.Add(1) - 1
		return members[idx%uint64(len(members))].ID
	case "write_to_first_fill":
		for _, m := range members {
			if m.QuotaBytes == nil || m.UsedBytes < *m.QuotaBytes {
				return m.ID
			}
		}
		return ""
	default:
		return members[0].ID
	}
}
```

- [ ] **Step 4: Run tests — expect PASS**

```bash
PATH="/usr/local/go/bin:$PATH" go test -count=1 ./internal/storage/... 2>&1 | tail -5
```
Expected: all 7 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/registry.go internal/storage/registry_test.go
git commit -m "feat(storage): add MemberInfo and Registry.PickMember for group blob store fill policies"
```

---

## Task 3: Fix RegisterStoredBlob double-call + update callers

**Files:**
- Modify: `internal/formats/base/store.go`
- Modify: `internal/formats/docker/handler.go`
- Modify: `internal/formats/repoproxy/repoproxy.go`

- [ ] **Step 1: Change RegisterStoredBlob signature in store.go**

Find the existing `RegisterStoredBlob` function signature (line ~154):

```go
func RegisterStoredBlob(ctx context.Context, d formats.Deps, repo *domain.Repository,
	filePath, contentType string, coords Coords,
	blobKey string,
	sha256sum, sha1sum, md5sum string,
	size int64,
) (*domain.Asset, error) {
	blobStoreID, blobStoreName, err := resolveBlobStoreRef(ctx, d, repo)
	if err != nil {
		return nil, err
	}
```

Replace with:

```go
// RegisterStoredBlob upserts component + asset after a blob was written to blobKey with known checksums.
// blobStoreID and blobStoreName may be pre-resolved by the caller (e.g. StoreArtifact) to avoid
// calling resolveBlobStoreRef twice. Pass empty strings to resolve internally.
func RegisterStoredBlob(ctx context.Context, d formats.Deps, repo *domain.Repository,
	filePath, contentType string, coords Coords,
	blobKey string,
	sha256sum, sha1sum, md5sum string,
	size int64,
	blobStoreID, blobStoreName string,
) (*domain.Asset, error) {
	if blobStoreID == "" {
		var err error
		blobStoreID, blobStoreName, err = resolveBlobStoreRef(ctx, d, repo)
		if err != nil {
			return nil, err
		}
	}
```

- [ ] **Step 2: Update StoreArtifact to resolve once and pass result**

In `StoreArtifact`, find the block that resolves physStore (around line 72-84):

```go
	var physStore storage.BlobStore
	{
		bsID, _, refErr := resolveBlobStoreRef(ctx, d, repo)
		if refErr == nil && bsID != "" {
			if bsMeta, getErr := d.Blobs.GetByID(ctx, bsID); getErr == nil {
				physStore = physicalStore(ctx, d, bsMeta)
			}
		}
		if physStore == nil {
			physStore = d.BlobStore
		}
	}
```

Replace with:

```go
	// Resolve once — result passed to RegisterStoredBlob to avoid double-call.
	// For group stores, double-call would advance the round-robin counter twice.
	resolvedBlobStoreID, resolvedBlobStoreName, _ := resolveBlobStoreRef(ctx, d, repo)

	var physStore storage.BlobStore
	if resolvedBlobStoreID != "" {
		if bsMeta, getErr := d.Blobs.GetByID(ctx, resolvedBlobStoreID); getErr == nil {
			physStore = physicalStore(ctx, d, bsMeta)
		}
	}
	if physStore == nil {
		physStore = d.BlobStore
	}
```

Then find the `RegisterStoredBlob` call inside `StoreArtifact` (around line 120):

```go
	asset, err := RegisterStoredBlob(ctx, d, repo, filePath, contentType, coords, blobKey, sha256sum, sha1sum, md5sum, size)
```

Replace with:

```go
	asset, err := RegisterStoredBlob(ctx, d, repo, filePath, contentType, coords, blobKey, sha256sum, sha1sum, md5sum, size, resolvedBlobStoreID, resolvedBlobStoreName)
```

- [ ] **Step 3: Update docker/handler.go caller**

Find the call at line ~213 in `internal/formats/docker/handler.go`:

```go
			_, _ = base.RegisterStoredBlob(c.Request.Context(), h.deps, repo,
				manifestPath(imageName, digestRef), ct,
				base.Coords{Name: imageName, Version: digestRef},
				res.Asset.BlobKey,
				res.SHA256, res.SHA1, res.MD5, res.Size)
```

Replace with:

```go
			_, _ = base.RegisterStoredBlob(c.Request.Context(), h.deps, repo,
				manifestPath(imageName, digestRef), ct,
				base.Coords{Name: imageName, Version: digestRef},
				res.Asset.BlobKey,
				res.SHA256, res.SHA1, res.MD5, res.Size, "", "")
```

- [ ] **Step 4: Update repoproxy/repoproxy.go caller**

Find the call around line 280 in `internal/formats/repoproxy/repoproxy.go`:

```go
	regAsset, regErr := base.RegisterStoredBlob(context.Background(), d, repo, repoRelativePath, ct, coords, blobKey, sha256sum, sha1sum, md5sum, size)
```

Replace with:

```go
	regAsset, regErr := base.RegisterStoredBlob(context.Background(), d, repo, repoRelativePath, ct, coords, blobKey, sha256sum, sha1sum, md5sum, size, "", "")
```

- [ ] **Step 5: Build to verify no compilation errors**

```bash
PATH="/usr/local/go/bin:$PATH" go build ./... 2>&1
```
Expected: no output (clean build).

- [ ] **Step 6: Run all tests**

```bash
PATH="/usr/local/go/bin:$PATH" go test -count=1 ./... 2>&1 | tail -5
```
Expected: 335 tests pass (same as baseline).

- [ ] **Step 7: Commit**

```bash
git add internal/formats/base/store.go internal/formats/docker/handler.go internal/formats/repoproxy/repoproxy.go
git commit -m "refactor(base): fix RegisterStoredBlob double-call — resolve blob store once in StoreArtifact"
```

---

## Task 4: resolveBlobStoreRef handles group stores

**Files:**
- Modify: `internal/formats/base/store.go`
- Modify: `internal/formats/base/store_test.go`

- [ ] **Step 1: Write failing tests — add to store_test.go**

Add these test functions to the end of `internal/formats/base/store_test.go`:

```go
// ── Group blob store routing ──────────────────────────────────

func depsWithGroup(repo *domain.Repository, groupStore, memberA, memberB *domain.BlobStore) (formats.Deps, *testutil.BlobStore, *testutil.BlobStore) {
	repos := testutil.NewRepoRepo(repo)
	blobs := testutil.NewBlobStoreRepo(groupStore, memberA, memberB)
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	storeA := testutil.NewBlobStore()
	storeB := testutil.NewBlobStore()

	reg := storage.NewRegistry(storeA) // default = storeA
	// Pre-populate registry so PickMember can work without real physical stores.
	// For routing tests we only care which member ID is selected, not physical I/O.

	return formats.Deps{
		Repos:      repos,
		Blobs:      blobs,
		Components: comps,
		Assets:     assets,
		BlobStore:  storeA,
		Registry:   reg,
		BaseURL:    "http://localhost:8080",
	}, storeA, storeB
}

func groupBlobStore(id, name string, policy string, memberIDs ...string) *domain.BlobStore {
	ids := make([]interface{}, len(memberIDs))
	for i, m := range memberIDs {
		ids[i] = m
	}
	return &domain.BlobStore{
		ID:   id,
		Name: name,
		Type: "group",
		Config: map[string]any{
			"fill_policy": policy,
			"member_ids":  ids,
		},
	}
}

func physicalBlobStore(id, name string) *domain.BlobStore {
	return &domain.BlobStore{ID: id, Name: name, Type: "local",
		Config: map[string]any{"path": t.TempDir()}}
}

func TestStoreArtifact_GroupStore_AssetBlobStoreIDIsPhysicalMember(t *testing.T) {
	memberA := &domain.BlobStore{ID: "member-a", Name: "store-a", Type: "local",
		Config: map[string]any{"path": t.TempDir()}}
	memberB := &domain.BlobStore{ID: "member-b", Name: "store-b", Type: "local",
		Config: map[string]any{"path": t.TempDir()}}
	group := groupBlobStore("group-1", "my-group", "round_robin", "member-a", "member-b")

	bsID := "group-1"
	repo := &domain.Repository{
		ID: "repo-1", Name: "testrepo", Format: "raw", Type: "hosted",
		Online: true, BlobStoreID: &bsID,
	}

	d, _, _ := depsWithGroup(repo, group, memberA, memberB)

	result, err := base.StoreArtifact(context.Background(), d,
		"testrepo", "/file.txt", "text/plain",
		base.Coords{Name: "file.txt"},
		strings.NewReader("hello"), 5)
	require.NoError(t, err)
	require.NotNil(t, result)

	// asset.BlobStoreID must be a physical member ID, never the group ID
	if result.Asset.BlobStoreID == "group-1" {
		t.Fatal("asset.BlobStoreID must not be the group ID — it must be a physical member ID")
	}
	if result.Asset.BlobStoreID != "member-a" && result.Asset.BlobStoreID != "member-b" {
		t.Errorf("expected member-a or member-b, got %q", result.Asset.BlobStoreID)
	}
}

func TestStoreArtifact_GroupStore_RoundRobin_AlternatesMembers(t *testing.T) {
	memberA := &domain.BlobStore{ID: "member-a", Name: "store-a", Type: "local",
		Config: map[string]any{"path": t.TempDir()}}
	memberB := &domain.BlobStore{ID: "member-b", Name: "store-b", Type: "local",
		Config: map[string]any{"path": t.TempDir()}}
	group := groupBlobStore("group-rr", "rr-group", "round_robin", "member-a", "member-b")

	bsID := "group-rr"
	repo := &domain.Repository{
		ID: "repo-rr", Name: "rr-repo", Format: "raw", Type: "hosted",
		Online: true, BlobStoreID: &bsID,
	}
	d, _, _ := depsWithGroup(repo, group, memberA, memberB)

	var selected []string
	for i := 0; i < 4; i++ {
		path := fmt.Sprintf("/file%d.txt", i)
		res, err := base.StoreArtifact(context.Background(), d,
			"rr-repo", path, "text/plain",
			base.Coords{Name: fmt.Sprintf("file%d.txt", i)},
			strings.NewReader("data"), 4)
		require.NoError(t, err)
		selected = append(selected, res.Asset.BlobStoreID)
	}
	// Round-robin should alternate: a, b, a, b
	if selected[0] == selected[1] {
		t.Errorf("round-robin should alternate, got same member twice: %v", selected)
	}
	if selected[0] != selected[2] || selected[1] != selected[3] {
		t.Errorf("round-robin pattern broken: %v", selected)
	}
}
```

- [ ] **Step 2: Run to confirm tests fail**

```bash
PATH="/usr/local/go/bin:$PATH" go test -count=1 ./internal/formats/base/... 2>&1 | tail -8
```
Expected: compilation errors or test failures about group store handling.

- [ ] **Step 3: Add group routing helpers and extend resolveBlobStoreRef in store.go**

Add these helper functions after `resolveBlobStoreRef` in `internal/formats/base/store.go`:

```go
// groupMemberIDs extracts the member_ids array from a group blob store's config.
// Handles both []string (from Go code) and []interface{} (from JSON unmarshal).
func groupMemberIDs(bs *domain.BlobStore) []string {
	if bs.Config == nil {
		return nil
	}
	raw := bs.Config["member_ids"]
	switch v := raw.(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// groupFillPolicy returns the fill_policy from a group blob store's config.
// Defaults to "round_robin" if not set.
func groupFillPolicy(bs *domain.BlobStore) string {
	if bs.Config == nil {
		return "round_robin"
	}
	if p, ok := bs.Config["fill_policy"].(string); ok && p != "" {
		return p
	}
	return "round_robin"
}
```

Now extend `resolveBlobStoreRef`. Find the function and replace it entirely:

```go
// resolveBlobStoreRef returns the blob store UUID for assets.blob_store_id (FK)
// and the store name for BlobStoreRepo.UpdateUsedBytes (keyed by name).
// For group stores, it picks a physical member using the configured fill policy.
func resolveBlobStoreRef(ctx context.Context, d formats.Deps, repo *domain.Repository) (id string, name string, err error) {
	var bs *domain.BlobStore
	if repo.BlobStoreID != nil {
		ref := strings.TrimSpace(*repo.BlobStoreID)
		if ref != "" {
			bs, err = d.Blobs.GetByID(ctx, ref)
			if err != nil {
				return "", "", fmt.Errorf("blob store: %w", err)
			}
			if bs == nil {
				return "", "", fmt.Errorf("blob store id %q not found", ref)
			}
		}
	}
	if bs == nil {
		bs, err = d.Blobs.Get(ctx, "default")
		if err != nil {
			return "", "", fmt.Errorf("blob store: %w", err)
		}
		if bs == nil {
			return "", "", fmt.Errorf("default blob store not found (seed blob_stores or assign repository.blobStoreId)")
		}
	}

	if bs.Type != "group" {
		return bs.ID, bs.Name, nil
	}

	// Group store: pick a physical member via fill policy.
	memberIDs := groupMemberIDs(bs)
	if len(memberIDs) == 0 {
		return "", "", fmt.Errorf("group blob store %q has no members", bs.Name)
	}
	if d.Registry == nil {
		return "", "", fmt.Errorf("group blob store %q requires Registry to be configured", bs.Name)
	}

	var members []storage.MemberInfo
	var memberMap = make(map[string]domain.BlobStore, len(memberIDs))
	for _, mid := range memberIDs {
		m, getErr := d.Blobs.GetByID(ctx, mid)
		if getErr != nil || m == nil {
			continue
		}
		members = append(members, storage.MemberInfo{
			ID:         m.ID,
			QuotaBytes: m.QuotaBytes,
			UsedBytes:  m.UsedBytes,
		})
		memberMap[m.ID] = *m
	}
	if len(members) == 0 {
		return "", "", fmt.Errorf("group blob store %q: no valid members found", bs.Name)
	}

	policy := groupFillPolicy(bs)
	memberID := d.Registry.PickMember(bs.ID, policy, members)
	if memberID == "" {
		return "", "", fmt.Errorf("%w: all members of group blob store %q are at capacity", ErrQuotaExceeded, bs.Name)
	}

	m := memberMap[memberID]
	return m.ID, m.Name, nil
}
```

Also add `"github.com/nexspence-oss/nexspence/internal/storage"` to the imports in store.go (it should already be there — verify it is).

- [ ] **Step 4: Run tests — expect PASS**

```bash
PATH="/usr/local/go/bin:$PATH" go test -count=1 ./internal/formats/base/... 2>&1 | tail -8
```
Expected: all tests pass.

- [ ] **Step 5: Run full suite**

```bash
PATH="/usr/local/go/bin:$PATH" go test -count=1 ./... 2>&1 | tail -3
```
Expected: 335+ tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/formats/base/store.go internal/formats/base/store_test.go
git commit -m "feat(base): resolveBlobStoreRef routes writes through group blob store fill policy"
```

---

## Task 5: checkQuota handles group stores

**Files:**
- Modify: `internal/formats/base/store.go`
- Modify: `internal/formats/base/store_test.go`

- [ ] **Step 1: Write failing test — add to store_test.go**

```go
func TestStoreArtifact_GroupStore_AllMembersFull_Returns507(t *testing.T) {
	quota := int64(10)
	memberA := &domain.BlobStore{ID: "full-a", Name: "full-store-a", Type: "local",
		QuotaBytes: &quota, UsedBytes: 10,
		Config: map[string]any{"path": t.TempDir()}}
	memberB := &domain.BlobStore{ID: "full-b", Name: "full-store-b", Type: "local",
		QuotaBytes: &quota, UsedBytes: 10,
		Config: map[string]any{"path": t.TempDir()}}
	group := groupBlobStore("group-full", "full-group", "write_to_first_fill", "full-a", "full-b")

	bsID := "group-full"
	repo := &domain.Repository{
		ID: "repo-full", Name: "full-repo", Format: "raw", Type: "hosted",
		Online: true, BlobStoreID: &bsID,
	}
	d, _, _ := depsWithGroup(repo, group, memberA, memberB)

	_, err := base.StoreArtifact(context.Background(), d,
		"full-repo", "/file.txt", "text/plain",
		base.Coords{Name: "file.txt"},
		strings.NewReader("hello"), 5)
	if !errors.Is(err, base.ErrQuotaExceeded) {
		t.Errorf("want ErrQuotaExceeded when all members full, got %v", err)
	}
}
```

- [ ] **Step 2: Run to confirm test fails**

```bash
PATH="/usr/local/go/bin:$PATH" go test -count=1 -run TestStoreArtifact_GroupStore_AllMembersFull ./internal/formats/base/... 2>&1 | tail -5
```
Expected: FAIL — group store currently falls through to default store.

- [ ] **Step 3: Update checkQuota in store.go to skip group-level quota check**

Find `checkQuota` in `internal/formats/base/store.go`. The group's quota enforcement happens in `resolveBlobStoreRef` (PickMember returns "" → ErrQuotaExceeded). The early `checkQuota` call should skip the blob-store quota check for group stores, since we don't know which member will be selected yet.

Replace `checkQuota`:

```go
// checkQuota verifies that writing `size` bytes won't exceed either the blob store
// quota or the repository-level quota. Returns ErrQuotaExceeded if either is breached.
// For group stores, the blob-store quota check is deferred to resolveBlobStoreRef
// (PickMember returns "" when all members are at capacity).
func checkQuota(ctx context.Context, d formats.Deps, repo *domain.Repository, size int64) error {
	bs, err := resolveBlobStoreObj(ctx, d, repo)
	if err != nil {
		return err
	}
	if bs.Type != "group" && bs.QuotaBytes != nil && bs.UsedBytes+size > *bs.QuotaBytes {
		return fmt.Errorf("%w: blob store %q usage %d + %d > limit %d",
			ErrQuotaExceeded, bs.Name, bs.UsedBytes, size, *bs.QuotaBytes)
	}
	if repo.QuotaBytes != nil {
		used, err := d.Assets.SumSizeByRepo(ctx, repo.Name)
		if err != nil {
			return fmt.Errorf("quota check: %w", err)
		}
		if used+size > *repo.QuotaBytes {
			return fmt.Errorf("%w: repository %q usage %d + %d > limit %d",
				ErrQuotaExceeded, repo.Name, used, size, *repo.QuotaBytes)
		}
	}
	return nil
}
```

Also update `resolveBlobStoreObj` to return the group store itself (not a member) — it's only used for quota checking and should not recurse into group routing:

The existing `resolveBlobStoreObj` is fine as-is. It returns the group store with `Type="group"` and the check above skips quota enforcement for groups.

- [ ] **Step 4: Run the new test — expect PASS**

```bash
PATH="/usr/local/go/bin:$PATH" go test -count=1 ./internal/formats/base/... 2>&1 | tail -5
```
Expected: all pass.

- [ ] **Step 5: Run full suite**

```bash
PATH="/usr/local/go/bin:$PATH" go test -count=1 ./... 2>&1 | tail -3
```
Expected: 335+ tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/formats/base/store.go internal/formats/base/store_test.go
git commit -m "feat(base): checkQuota defers group blob store quota to PickMember; all-full returns 507"
```

---

## Task 6: Handler validation for group Create/Update/Delete

**Files:**
- Modify: `internal/api/handlers/blobstores.go`
- Create: `internal/api/handlers/blobstores_group_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/api/handlers/blobstores_group_test.go`:

```go
package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { gin.SetMode(gin.TestMode) }

func blobStoreHandler(stores ...*domain.BlobStore) *handlers.BlobStoreHandler {
	return handlers.NewBlobStoreHandler(testutil.NewBlobStoreRepo(stores...))
}

func postGroup(h *handlers.BlobStoreHandler, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)
	r.POST("/blobstores/:type", h.Create)
	c.Request = httptest.NewRequest(http.MethodPost, "/blobstores/group", bytes.NewReader(b))
	c.Request.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, c.Request)
	return w
}

func TestBlobStoreHandler_Create_Group_Valid(t *testing.T) {
	memberA := &domain.BlobStore{ID: "aaa", Name: "store-a", Type: "local"}
	memberB := &domain.BlobStore{ID: "bbb", Name: "store-b", Type: "local"}
	h := blobStoreHandler(memberA, memberB)

	w := postGroup(h, map[string]any{
		"name": "my-group",
		"config": map[string]any{
			"fill_policy": "round_robin",
			"member_ids":  []string{"aaa", "bbb"},
		},
	})
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestBlobStoreHandler_Create_Group_InvalidPolicy(t *testing.T) {
	memberA := &domain.BlobStore{ID: "aaa", Name: "store-a", Type: "local"}
	h := blobStoreHandler(memberA)

	w := postGroup(h, map[string]any{
		"name": "bad-group",
		"config": map[string]any{
			"fill_policy": "teleport",
			"member_ids":  []string{"aaa"},
		},
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBlobStoreHandler_Create_Group_NoMembers(t *testing.T) {
	h := blobStoreHandler()
	w := postGroup(h, map[string]any{
		"name": "empty-group",
		"config": map[string]any{
			"fill_policy": "round_robin",
			"member_ids":  []string{},
		},
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBlobStoreHandler_Create_Group_UnknownMember(t *testing.T) {
	h := blobStoreHandler()
	w := postGroup(h, map[string]any{
		"name": "ghost-group",
		"config": map[string]any{
			"fill_policy": "round_robin",
			"member_ids":  []string{"does-not-exist"},
		},
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBlobStoreHandler_Create_Group_NestedGroup_Rejected(t *testing.T) {
	inner := &domain.BlobStore{ID: "inner-g", Name: "inner-group", Type: "group",
		Config: map[string]any{"fill_policy": "round_robin", "member_ids": []string{}}}
	h := blobStoreHandler(inner)

	w := postGroup(h, map[string]any{
		"name": "outer-group",
		"config": map[string]any{
			"fill_policy": "round_robin",
			"member_ids":  []string{"inner-g"},
		},
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBlobStoreHandler_Delete_MemberOfGroup_Rejected(t *testing.T) {
	member := &domain.BlobStore{ID: "mem-1", Name: "store-a", Type: "local"}
	group := &domain.BlobStore{
		ID: "grp-1", Name: "my-group", Type: "group",
		Config: map[string]any{
			"fill_policy": "round_robin",
			"member_ids":  []interface{}{"mem-1"},
		},
	}
	h := blobStoreHandler(member, group)

	w := httptest.NewRecorder()
	c, router := gin.CreateTestContext(w)
	router.DELETE("/blobstores/:name", h.Delete)
	c.Request = httptest.NewRequest(http.MethodDelete, "/blobstores/store-a", nil)
	router.ServeHTTP(w, c.Request)

	require.Equal(t, http.StatusConflict, w.Code)
}
```

- [ ] **Step 2: Run to confirm tests fail**

```bash
PATH="/usr/local/go/bin:$PATH" go test -count=1 ./internal/api/handlers/... 2>&1 | tail -8
```
Expected: failures on group validation tests.

- [ ] **Step 3: Add validateGroupConfig and update Create/Update/Delete in blobstores.go**

Add these helpers before the `Create` method in `internal/api/handlers/blobstores.go`:

```go
var validFillPolicies = map[string]bool{
	"round_robin":        true,
	"write_to_first_fill": true,
}

// validateGroupConfig validates the config for a group blob store.
// Returns a non-nil error string suitable for HTTP 400 responses.
func (h *BlobStoreHandler) validateGroupConfig(ctx context.Context, cfg map[string]any) string {
	if cfg == nil {
		return "group blob store requires config with fill_policy and member_ids"
	}
	policy, _ := cfg["fill_policy"].(string)
	if !validFillPolicies[policy] {
		return "fill_policy must be 'round_robin' or 'write_to_first_fill'"
	}
	memberIDs := extractMemberIDs(cfg)
	if len(memberIDs) == 0 {
		return "group blob store must have at least one member_id"
	}
	for _, mid := range memberIDs {
		m, err := h.repo.GetByID(ctx, mid)
		if err != nil || m == nil {
			return fmt.Sprintf("member blob store %q not found", mid)
		}
		if m.Type == "group" {
			return fmt.Sprintf("member %q is itself a group — nested groups are not allowed", m.Name)
		}
	}
	return ""
}

// extractMemberIDs pulls member_ids from config, handling both []string and []interface{}.
func extractMemberIDs(cfg map[string]any) []string {
	if cfg == nil {
		return nil
	}
	raw := cfg["member_ids"]
	switch v := raw.(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
```

Add `"fmt"` to imports if not already present.

Update `Create` to call validation when type is "group". Find the `if bs.Name == ""` block and add after it:

```go
	if bs.Type == "group" {
		if msg := h.validateGroupConfig(c.Request.Context(), bs.Config); msg != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
	}
```

Update `Delete` to reject deletion of a store that is referenced as a member. Find the `Delete` handler, add before the actual delete call:

```go
	// Reject if this store is a member of any group.
	if msg := h.checkNotGroupMember(c.Request.Context(), name); msg != "" {
		c.JSON(http.StatusConflict, gin.H{"error": msg})
		return
	}
```

Add the helper:

```go
// checkNotGroupMember returns a non-empty error message if the named store
// is a member of any group blob store.
func (h *BlobStoreHandler) checkNotGroupMember(ctx context.Context, name string) string {
	bs, err := h.repo.Get(ctx, name)
	if err != nil || bs == nil {
		return ""
	}
	all, err := h.repo.List(ctx)
	if err != nil {
		return ""
	}
	for _, g := range all {
		if g.Type != "group" {
			continue
		}
		for _, mid := range extractMemberIDs(g.Config) {
			if mid == bs.ID {
				return fmt.Sprintf("blob store %q is a member of group %q — remove it from the group first", name, g.Name)
			}
		}
	}
	return ""
}
```

- [ ] **Step 4: Run the new tests — expect PASS**

```bash
PATH="/usr/local/go/bin:$PATH" go test -count=1 ./internal/api/handlers/... 2>&1 | tail -8
```
Expected: all new tests pass.

- [ ] **Step 5: Run full suite**

```bash
PATH="/usr/local/go/bin:$PATH" go test -count=1 ./... 2>&1 | tail -3
```
Expected: 340+ tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/api/handlers/blobstores.go internal/api/handlers/blobstores_group_test.go
git commit -m "feat(handlers): group blob store validation — fill_policy, members, no nesting, delete guard"
```

---

## Task 7: Usage endpoint aggregates across group members

**Files:**
- Modify: `internal/api/handlers/blobstores.go`

- [ ] **Step 1: Update the Usage handler**

Find the `Usage` handler in `blobstores.go`. Replace the entire handler with:

```go
// Usage handles GET /api/v1/blob-stores/:name/usage
func (h *BlobStoreHandler) Usage(c *gin.Context) {
	if h.repos == nil || h.assets == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage deps not configured"})
		return
	}
	name := c.Param("name")
	ctx := c.Request.Context()

	bs, err := h.repo.Get(ctx, name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if bs == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "blob store not found"})
		return
	}

	// For group stores: aggregate across all member stores.
	if bs.Type == "group" {
		h.usageGroup(c, ctx, bs)
		return
	}

	linked, err := h.repos.ListByBlobStoreID(ctx, bs.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	info := make([]LinkedRepoInfo, 0, len(linked))
	var total int64
	for _, r := range linked {
		used, _ := h.assets.SumSizeByRepo(ctx, r.Name)
		info = append(info, LinkedRepoInfo{
			Name:      r.Name,
			Format:    string(r.Format),
			Type:      string(r.Type),
			BytesUsed: used,
		})
		total += used
	}

	resp := gin.H{
		"store":              bs,
		"linkedRepositories": info,
		"totalAssetBytes":    total,
	}
	if bs.QuotaBytes != nil {
		resp["quotaRemaining"] = *bs.QuotaBytes - bs.UsedBytes
	}
	c.JSON(http.StatusOK, resp)
}

type memberUsage struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	UsedBytes  int64  `json:"usedBytes"`
	QuotaBytes *int64 `json:"quotaBytes,omitempty"`
}

func (h *BlobStoreHandler) usageGroup(c *gin.Context, ctx context.Context, group *domain.BlobStore) {
	memberIDs := extractMemberIDs(group.Config)
	var members []memberUsage
	var totalUsed, totalQuota int64
	hasQuota := true

	var allRepos []LinkedRepoInfo
	var totalAssetBytes int64

	for _, mid := range memberIDs {
		m, err := h.repo.GetByID(ctx, mid)
		if err != nil || m == nil {
			continue
		}
		mu := memberUsage{ID: m.ID, Name: m.Name, UsedBytes: m.UsedBytes, QuotaBytes: m.QuotaBytes}
		members = append(members, mu)
		totalUsed += m.UsedBytes
		if m.QuotaBytes == nil {
			hasQuota = false
		} else {
			totalQuota += *m.QuotaBytes
		}

		linked, err := h.repos.ListByBlobStoreID(ctx, m.ID)
		if err != nil {
			continue
		}
		for _, r := range linked {
			used, _ := h.assets.SumSizeByRepo(ctx, r.Name)
			allRepos = append(allRepos, LinkedRepoInfo{
				Name:      r.Name,
				Format:    string(r.Format),
				Type:      string(r.Type),
				BytesUsed: used,
			})
			totalAssetBytes += used
		}
	}

	resp := gin.H{
		"store":              group,
		"members":            members,
		"memberTotalUsed":    totalUsed,
		"linkedRepositories": allRepos,
		"totalAssetBytes":    totalAssetBytes,
	}
	if hasQuota {
		resp["memberTotalQuota"] = totalQuota
		resp["quotaRemaining"] = totalQuota - totalUsed
	}
	c.JSON(http.StatusOK, resp)
}
```

- [ ] **Step 2: Build and run full suite**

```bash
PATH="/usr/local/go/bin:$PATH" go build ./... && PATH="/usr/local/go/bin:$PATH" go test -count=1 ./... 2>&1 | tail -3
```
Expected: clean build, 340+ tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/api/handlers/blobstores.go
git commit -m "feat(handlers): Usage endpoint aggregates across group blob store members"
```

---

## Task 8: Frontend — Group type in AdminPage

**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

This task modifies the Blob Stores section of AdminPage. Find the existing blob store create modal and detail modal sections.

- [ ] **Step 1: Add GROUP badge to blob store list**

In the blob store list rows, find where the `S3` badge is rendered (look for `type === 's3'` or similar). Add a GROUP badge alongside:

```tsx
{bs.type === 'group' && (
  <span style={{
    fontSize: 10, fontWeight: 700, padding: '2px 6px',
    borderRadius: 4, background: 'rgba(139,92,246,0.15)',
    color: '#a78bfa', border: '1px solid rgba(139,92,246,0.3)',
    marginLeft: 6, letterSpacing: '0.05em',
  }}>GROUP</span>
)}
{bs.type === 's3' && (
  /* existing S3 badge */
)}
```

- [ ] **Step 2: Add "Group" option to create modal type dropdown**

Find the blob store type select/radio in the create modal. Add `group` as an option:

```tsx
<option value="group">Group</option>
```

- [ ] **Step 3: Add group-specific form fields (fill_policy + members multiselect)**

When type === "group" is selected in the create form, show group-specific fields. Add state for group fields near the existing modal state:

```tsx
const [groupFillPolicy, setGroupFillPolicy] = useState<'round_robin' | 'write_to_first_fill'>('round_robin')
const [groupMemberIds, setGroupMemberIds] = useState<string[]>([])
```

Render below the type selector when type === "group":

```tsx
{newBlobStoreType === 'group' && (
  <div style={{ marginTop: 12 }}>
    <label style={{ display: 'block', marginBottom: 6, color: 'var(--text-secondary)', fontSize: 13 }}>
      Fill Policy
    </label>
    <div style={{ display: 'flex', gap: 16, marginBottom: 16 }}>
      {(['round_robin', 'write_to_first_fill'] as const).map(p => (
        <label key={p} style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer', fontSize: 13 }}>
          <input type="radio" name="fillPolicy" value={p}
            checked={groupFillPolicy === p}
            onChange={() => setGroupFillPolicy(p)} />
          {p === 'round_robin' ? 'Round Robin' : 'Write to First Fill'}
        </label>
      ))}
    </div>
    <label style={{ display: 'block', marginBottom: 6, color: 'var(--text-secondary)', fontSize: 13 }}>
      Members (select non-group stores)
    </label>
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, maxHeight: 160, overflowY: 'auto',
      background: 'rgba(255,255,255,0.04)', borderRadius: 8, padding: 8 }}>
      {blobStores.filter(s => s.type !== 'group').map(s => (
        <label key={s.id} style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', fontSize: 13 }}>
          <input type="checkbox"
            checked={groupMemberIds.includes(s.id)}
            onChange={e => setGroupMemberIds(prev =>
              e.target.checked ? [...prev, s.id] : prev.filter(id => id !== s.id)
            )} />
          {s.name}
          <span style={{ color: 'var(--text-muted)', fontSize: 11 }}>{s.type}</span>
        </label>
      ))}
    </div>
  </div>
)}
```

- [ ] **Step 4: Wire group config into create payload**

In the create handler that calls `POST /service/rest/v1/blobstores/${type}`, add group config when type === "group":

```tsx
const payload: any = { name: newBlobStoreName }
if (newBlobStoreType === 'local') payload.config = { path: newBlobStorePath }
if (newBlobStoreType === 's3') payload.config = { /* existing s3 fields */ }
if (newBlobStoreType === 'group') payload.config = {
  fill_policy: groupFillPolicy,
  member_ids: groupMemberIds,
}
```

- [ ] **Step 5: Add member list to detail modal for group stores**

In the blob store detail/edit modal, add a members section when `selectedBlobStore?.type === 'group'`:

```tsx
{selectedBlobStore?.type === 'group' && (
  <div style={{ marginTop: 16 }}>
    <div style={{ color: 'var(--text-secondary)', fontSize: 12, marginBottom: 8 }}>
      Fill Policy: <strong style={{ color: 'var(--text-primary)' }}>
        {selectedBlobStore.config?.fill_policy === 'write_to_first_fill' ? 'Write to First Fill' : 'Round Robin'}
      </strong>
    </div>
    <div style={{ color: 'var(--text-secondary)', fontSize: 12, marginBottom: 4 }}>Members:</div>
    {(extractMemberIds(selectedBlobStore.config?.member_ids) || []).map((mid: string) => {
      const m = blobStores.find(s => s.id === mid)
      return m ? (
        <div key={mid} style={{ display: 'flex', justifyContent: 'space-between',
          fontSize: 12, padding: '4px 8px', background: 'rgba(255,255,255,0.04)', borderRadius: 6, marginBottom: 4 }}>
          <span>{m.name}</span>
          <span style={{ color: 'var(--text-muted)' }}>
            {m.usedBytes != null ? `${(m.usedBytes / 1024 / 1024).toFixed(1)} MB used` : ''}
            {m.quotaBytes != null ? ` / ${(m.quotaBytes / 1024 / 1024).toFixed(1)} MB` : ''}
          </span>
        </div>
      ) : <div key={mid} style={{ fontSize: 12, color: 'var(--text-muted)' }}>{mid} (not found)</div>
    })}
  </div>
)}
```

Add helper above the component:

```tsx
function extractMemberIds(raw: any): string[] {
  if (!Array.isArray(raw)) return []
  return raw.filter((x: any) => typeof x === 'string')
}
```

- [ ] **Step 6: TypeScript check**

```bash
cd frontend && node node_modules/typescript/bin/tsc --noEmit 2>&1 | head -20
```
Expected: no errors. Fix any type errors before committing.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/pages/AdminPage.tsx
git commit -m "feat(frontend): group blob store UI — GROUP badge, create form, detail member list"
```

---

## Task 9: User documentation

**Files:**
- Create: `docs/blob-store-groups.md`

- [ ] **Step 1: Write the documentation**

Create `/Users/skensel/WORKING/AI/nexspence-core/docs/blob-store-groups.md`:

```markdown
# Group Blob Stores

A **group blob store** combines two or more physical blob stores (local or S3) into a single logical store. Repositories assigned to a group store distribute writes automatically across members according to a **fill policy**. Reads and deletes are unaffected — every artifact tracks exactly which physical store holds it.

## When to use

| Use case | Recommended policy |
|----------|-------------------|
| Horizontal scaling — spread writes evenly | `round_robin` |
| Tiered storage — fill fast SSD first, overflow to HDD | `write_to_first_fill` |
| Capacity extension without downtime | `write_to_first_fill` |

## Fill policies

### `round_robin`
Distributes writes evenly across members in a rotating cycle. Member order is the order in `member_ids`. The counter resets on server restart (acceptable — purpose is load distribution, not strict ordering). Quota per member is not checked — use `write_to_first_fill` if you need quota-aware routing.

### `write_to_first_fill`
Writes to the **first** member until its quota is exhausted, then moves to the second, and so on. A member with no quota set is treated as unlimited. If all members are at capacity, uploads return **507 Insufficient Storage**.

## Create via UI

1. Open **Admin → Blob Stores**.
2. Click **Create**, select type **Group**.
3. Choose a fill policy (Round Robin / Write to First Fill).
4. Check one or more non-group stores as members.
5. Click **Save**.

## Create via API

```bash
curl -u admin:admin123 -X POST http://localhost:8081/service/rest/v1/blobstores/group \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "fast-group",
    "config": {
      "fill_policy": "round_robin",
      "member_ids": ["<uuid-of-store-a>", "<uuid-of-store-b>"]
    }
  }'
```

Assign the group to a repository just like any other blob store — use the group's name or ID in the repository create/update payload.

## Constraints

- **No nesting** — a group cannot be a member of another group.
- **Member deletion blocked** — you cannot delete a blob store that is currently a member of a group. Remove it from the group first.
- **`used_bytes` is per-member** — the group's `usedBytes` field in the list API is 0; use the `/api/v1/blob-stores/:name/usage` endpoint to see aggregate and per-member usage.
- **Migration** — existing artifacts in member stores are not rebalanced when a group is created or when members are added/removed.

## Quota and 507 behaviour

`write_to_first_fill`: if the currently active member's quota is full, the next member is tried. If **all** members are at capacity, the upload is rejected with HTTP **507 Insufficient Storage**.

`round_robin`: quota is not checked during member selection. Individual member quotas are still enforced by the underlying store — an upload may fail with 507 if the selected member happens to be full.
```

- [ ] **Step 2: Commit**

```bash
git add docs/blob-store-groups.md
git commit -m "docs: add blob-store-groups.md user guide for group blob stores"
```

---

## Task 10: Update task_plan.md

**Files:**
- Modify: `task_plan.md`

- [ ] **Step 1: Mark Phase 48 complete**

Find `## Phase 48: Group Blob Stores` and change its status line:

```markdown
**Status:** complete (2026-05-02)
```

Mark all task checkboxes as done:

```markdown
- [x] DB: таблица `blob_store_groups` (id, name, fill_policy, member_ids[]) (migration)
- [x] `BlobStoreGroupService`: маршрутизация PUT по fill policy; GET из любого члена по blob_key
- [x] Обновить `BlobStore` интерфейс или добавить `GroupBlobStore` реализацию в `internal/storage/`
- [x] API: `POST/GET/DELETE /api/v1/blob-stores/group`
- [x] Frontend: AdminPage → Blob Stores tab — тип "Group" при создании
```

- [ ] **Step 2: Commit**

```bash
git add task_plan.md
git commit -m "chore: mark Phase 48 Group Blob Stores as complete"
```

---

## Final verification

- [ ] **Run full test suite one last time**

```bash
PATH="/usr/local/go/bin:$PATH" go test -count=1 ./... 2>&1 | tail -3
```
Expected: all tests pass (340+).

- [ ] **TypeScript check**

```bash
cd frontend && node node_modules/typescript/bin/tsc --noEmit 2>&1 | head -10
```
Expected: no errors.
