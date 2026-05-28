# Phase 45: Per-Repository Export / Import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-repository export (tar.gz streaming download) and import (multipart upload with skip/merge/rename conflict modes) to the existing BackupService, wire two new API routes, and add UI to RepositoriesPage + AdminPage.

**Architecture:** Extend `BackupService` in `internal/service/backup_service.go` with `ExportRepo` and `ImportRepo` methods; add two methods to `BackupHandler`; wire all backup routes (existing full-backup routes are also missing from the router — fix that here); update frontend.

**Tech Stack:** Go (`archive/tar`, `compress/gzip`, `encoding/json`), Gin, React + TypeScript, axios blob download.

---

## File Map

| File | Change |
|------|--------|
| `internal/service/backup_service.go` | Add `ErrRepoNotFound`, `ErrRepoConflict`, `ImportRepoStats`, `ExportRepo`, `ImportRepo` |
| `internal/service/backup_service_test.go` | Add 6 new tests |
| `internal/api/handlers/backup.go` | Add `ExportRepo`, `ImportRepo` handler methods |
| `internal/api/router.go` | Wire existing backup routes + 2 new per-repo routes |
| `frontend/src/api/client.ts` | Add `exportRepo`, `importRepo` |
| `frontend/src/pages/RepositoriesPage.tsx` | Export button on `RepoRow` |
| `frontend/src/pages/AdminPage.tsx` | Import section in Backup tab |

---

## Task 1: Add ExportRepo to BackupService

**Files:**
- Modify: `internal/service/backup_service.go`
- Modify: `internal/service/backup_service_test.go`

- [ ] **Step 1.1: Write the failing test**

Add to `internal/service/backup_service_test.go` (after `TestBackup_InvalidArchive`):

```go
func TestBackupRepo_ExportRoundtrip(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("myrepo", "raw")
	svc := buildBackupSvc(repo)

	// Add component + asset + blob.
	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name,
		Format: "raw", Name: "pkg.tar.gz", Version: "1.0",
	}
	require.NoError(t, svc.Components.Create(ctx, comp))

	blobKey := "ab/cd/abcdef"
	blobData := []byte("artifact-bytes")
	require.NoError(t, svc.BlobStore.Put(ctx, blobKey, bytes.NewReader(blobData), int64(len(blobData))))

	bs := &domain.BlobStore{Name: "default", Type: "local", Config: map[string]any{"path": "/tmp"}}
	require.NoError(t, svc.BlobStores.Create(ctx, bs))

	asset := &domain.Asset{
		ComponentID: comp.ID, RepositoryID: repo.ID, Repository: repo.Name,
		Path: "/pkg.tar.gz", BlobKey: blobKey, BlobStoreID: bs.ID,
		SizeBytes: int64(len(blobData)), ContentType: "application/gzip",
	}
	require.NoError(t, svc.Assets.Create(ctx, asset))

	var buf bytes.Buffer
	require.NoError(t, svc.ExportRepo(ctx, "myrepo", &buf))
	assert.Greater(t, buf.Len(), 0)

	// Archive must contain manifest, repository, components, assets, blobs entries.
	gr, err := gzip.NewReader(&buf)
	require.NoError(t, err)
	tr := tar.NewReader(gr)
	entries := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF { break }
		require.NoError(t, err)
		entries[hdr.Name] = true
	}
	assert.True(t, entries["manifest.json"])
	assert.True(t, entries["repository.json"])
	assert.True(t, entries["components.json"])
	assert.True(t, entries["assets.json"])
	assert.True(t, entries["blobs/"+blobKey])
}

func TestBackupRepo_ExportNotFound(t *testing.T) {
	svc := buildBackupSvc(testutil.SimpleRepo("exists", "raw"))
	var buf bytes.Buffer
	err := svc.ExportRepo(context.Background(), "no-such-repo", &buf)
	assert.ErrorIs(t, err, service.ErrRepoNotFound)
}
```

Add required imports at the top of the test file (`compress/gzip`, `archive/tar`, `io`):

```go
import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

- [ ] **Step 1.2: Run tests to confirm they fail**

```bash
cd /home/skensel/AI/self_nexus && go test ./internal/service/... -run "TestBackupRepo_Export" -v 2>&1 | tail -20
```

Expected: FAIL — `svc.ExportRepo undefined`, `service.ErrRepoNotFound undefined`.

- [ ] **Step 1.3: Add ErrRepoNotFound sentinel + ExportRepo to backup_service.go**

Add after the `BackupService` struct definition (around line 20 of `internal/service/backup_service.go`):

```go
// Sentinel errors for per-repository operations.
var (
	ErrRepoNotFound = errors.New("repository not found")
	ErrRepoConflict = errors.New("repository already exists")
)
```

Add the `errors` import to the import block in `backup_service.go`:
```go
import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)
```

Add after the `Export` method (before `Restore`):

```go
// ExportRepo writes a gzip-compressed tar archive scoped to one repository.
// Archive contains: manifest.json, repository.json, components.json, assets.json, blobs/<key>.
// Returns ErrRepoNotFound if repoName does not exist.
func (s *BackupService) ExportRepo(ctx context.Context, repoName string, w io.Writer) error {
	repo, err := s.Repos.Get(ctx, repoName)
	if err != nil || repo == nil {
		return fmt.Errorf("%w: %s", ErrRepoNotFound, repoName)
	}

	gw, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return err
	}
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	manifest := map[string]any{
		"version":  "1",
		"created":  time.Now().UTC().Format(time.RFC3339),
		"repoName": repoName,
	}
	if err := writeJSONEntry(tw, "manifest.json", manifest); err != nil {
		return err
	}
	if err := writeJSONEntry(tw, "repository.json", *repo); err != nil {
		return err
	}

	// Components (paginated).
	var allComponents []domain.Component
	for offset := 0; ; offset += 500 {
		page, err := s.Components.List(ctx, repoName, 500, offset)
		if err != nil {
			break
		}
		allComponents = append(allComponents, page.Items...)
		if len(page.Items) < 500 {
			break
		}
	}
	if err := writeJSONEntry(tw, "components.json", allComponents); err != nil {
		return err
	}

	// Assets (paginated).
	var allAssets []domain.Asset
	for offset := 0; ; offset += 500 {
		page, err := s.Assets.List(ctx, repoName, 500, offset)
		if err != nil {
			break
		}
		allAssets = append(allAssets, page.Items...)
		if len(page.Items) < 500 {
			break
		}
	}
	if err := writeJSONEntry(tw, "assets.json", allAssets); err != nil {
		return err
	}

	// Blobs (deduplicated by key).
	seen := map[string]bool{}
	for _, a := range allAssets {
		if a.BlobKey == "" || seen[a.BlobKey] {
			continue
		}
		seen[a.BlobKey] = true
		rc, size, err := s.BlobStore.Get(ctx, a.BlobKey)
		if err != nil {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:    "blobs/" + a.BlobKey,
			Size:    size,
			Mode:    0o644,
			ModTime: time.Now(),
		}); err != nil {
			rc.Close()
			return err
		}
		_, _ = io.Copy(tw, rc)
		rc.Close()
	}

	return nil
}
```

- [ ] **Step 1.4: Run tests to confirm they pass**

```bash
cd /home/skensel/AI/self_nexus && go test ./internal/service/... -run "TestBackupRepo_Export" -v 2>&1 | tail -20
```

Expected: PASS — both `TestBackupRepo_ExportRoundtrip` and `TestBackupRepo_ExportNotFound`.

- [ ] **Step 1.5: Run all service tests to confirm no regressions**

```bash
cd /home/skensel/AI/self_nexus && go test ./internal/service/... 2>&1 | tail -5
```

Expected: all pass.

- [ ] **Step 1.6: Commit**

```bash
cd /home/skensel/AI/self_nexus && git add internal/service/backup_service.go internal/service/backup_service_test.go && git commit -m "feat(phase45): add BackupService.ExportRepo + ErrRepoNotFound sentinel"
```

---

## Task 2: Add ImportRepo to BackupService

**Files:**
- Modify: `internal/service/backup_service.go`
- Modify: `internal/service/backup_service_test.go`

- [ ] **Step 2.1: Write the failing tests**

Add to `internal/service/backup_service_test.go` (after Task 1 tests):

```go
func TestBackupRepo_ImportRoundtrip(t *testing.T) {
	ctx := context.Background()
	src := buildBackupSvc(testutil.SimpleRepo("srcrepo", "raw"))

	bs := &domain.BlobStore{Name: "default", Type: "local", Config: map[string]any{"path": "/tmp"}}
	require.NoError(t, src.BlobStores.Create(ctx, bs))

	comp := &domain.Component{
		RepositoryID: src.Repos.(*testutil.RepoRepo).First().ID,
		Repository:   "srcrepo", Format: "raw", Name: "file.txt", Version: "1",
	}
	require.NoError(t, src.Components.Create(ctx, comp))

	blobData := []byte("hello")
	blobKey := "aa/bb/aabb"
	require.NoError(t, src.BlobStore.Put(ctx, blobKey, bytes.NewReader(blobData), int64(len(blobData))))

	asset := &domain.Asset{
		ComponentID: comp.ID, RepositoryID: comp.RepositoryID, Repository: "srcrepo",
		Path: "/file.txt", BlobKey: blobKey, BlobStoreID: bs.ID, SizeBytes: int64(len(blobData)), ContentType: "text/plain",
	}
	require.NoError(t, src.Assets.Create(ctx, asset))

	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "srcrepo", &buf))

	// Import into a fresh instance with no repos.
	dst := buildBackupSvc()
	require.NoError(t, dst.BlobStores.Create(ctx, &domain.BlobStore{Name: "default", Type: "local", Config: map[string]any{"path": "/tmp"}}))

	stats, err := dst.ImportRepo(ctx, &buf, "", "skip")
	require.NoError(t, err)
	assert.Equal(t, "srcrepo", stats.Repository)
	assert.Equal(t, 1, stats.Components)
	assert.Equal(t, 1, stats.Assets)
	assert.Equal(t, 1, stats.Blobs)

	// Blob should be present in destination.
	rc, _, err := dst.BlobStore.Get(ctx, blobKey)
	require.NoError(t, err)
	rc.Close()
}

func TestBackupRepo_ImportSkipIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("repo1", "raw")
	src := buildBackupSvc(repo)

	comp := &domain.Component{RepositoryID: repo.ID, Repository: "repo1", Format: "raw", Name: "f.txt", Version: "1"}
	require.NoError(t, src.Components.Create(ctx, comp))
	asset := &domain.Asset{ComponentID: comp.ID, RepositoryID: repo.ID, Repository: "repo1", Path: "/f.txt", SizeBytes: 0, ContentType: "text/plain"}
	require.NoError(t, src.Assets.Create(ctx, asset))

	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "repo1", &buf))
	archived := buf.Bytes()

	// First import.
	dst := buildBackupSvc()
	stats1, err := dst.ImportRepo(ctx, bytes.NewReader(archived), "", "skip")
	require.NoError(t, err)
	assert.Equal(t, 1, stats1.Components)
	assert.Equal(t, 1, stats1.Assets)

	// Second import (same archive) — everything already exists.
	stats2, err := dst.ImportRepo(ctx, bytes.NewReader(archived), "", "skip")
	require.NoError(t, err)
	assert.Equal(t, 0, stats2.Components, "second import should skip existing components")
	assert.Equal(t, 0, stats2.Assets, "second import should skip existing assets")
}

func TestBackupRepo_ImportRename(t *testing.T) {
	ctx := context.Background()
	src := buildBackupSvc(testutil.SimpleRepo("original", "raw"))

	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "original", &buf))

	dst := buildBackupSvc()
	stats, err := dst.ImportRepo(ctx, &buf, "renamed", "rename")
	require.NoError(t, err)
	assert.Equal(t, "renamed", stats.Repository)
}

func TestBackupRepo_ImportRenameConflict(t *testing.T) {
	ctx := context.Background()
	src := buildBackupSvc(testutil.SimpleRepo("repo", "raw"))

	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "repo", &buf))

	// Destination already has "newname".
	dst := buildBackupSvc(testutil.SimpleRepo("newname", "raw"))
	_, err := dst.ImportRepo(ctx, bytes.NewReader(buf.Bytes()), "newname", "rename")
	assert.ErrorIs(t, err, service.ErrRepoConflict)
}

func TestBackupRepo_ImportRenameMissingTargetName(t *testing.T) {
	ctx := context.Background()
	src := buildBackupSvc(testutil.SimpleRepo("repo", "raw"))

	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "repo", &buf))

	dst := buildBackupSvc()
	_, err := dst.ImportRepo(ctx, &buf, "", "rename")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, service.ErrRepoConflict)
}
```

Also update `buildBackupSvc` to accept optional repos (variadic), so tests can call `buildBackupSvc()` with no repos:

```go
func buildBackupSvc(repos ...*domain.Repository) *service.BackupService {
	return &service.BackupService{
		BlobStores: testutil.NewBlobStoreRepo(),
		Repos:      testutil.NewRepoRepo(repos...),
		Users:      testutil.NewUserRepo(),
		Roles:      testutil.NewRoleRepo(),
		Policies:   testutil.NewCleanupPolicyRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
	}
}
```

Also add a helper to testutil's RepoRepo to get the first repo (used in the roundtrip test):

Look at `internal/testutil/mocks.go` to see the `RepoRepo` struct. Add a `First()` helper — OR avoid accessing it by using the known `repo.ID` from `testutil.SimpleRepo`. Rewrite `TestBackupRepo_ImportRoundtrip` to not need `First()`:

```go
func TestBackupRepo_ImportRoundtrip(t *testing.T) {
	ctx := context.Background()
	srcRepo := testutil.SimpleRepo("srcrepo", "raw")
	src := buildBackupSvc(srcRepo)

	bs := &domain.BlobStore{Name: "default", Type: "local", Config: map[string]any{"path": "/tmp"}}
	require.NoError(t, src.BlobStores.Create(ctx, bs))

	comp := &domain.Component{
		RepositoryID: srcRepo.ID, Repository: "srcrepo",
		Format: "raw", Name: "file.txt", Version: "1",
	}
	require.NoError(t, src.Components.Create(ctx, comp))

	blobData := []byte("hello")
	blobKey := "aa/bb/aabb"
	require.NoError(t, src.BlobStore.Put(ctx, blobKey, bytes.NewReader(blobData), int64(len(blobData))))

	asset := &domain.Asset{
		ComponentID: comp.ID, RepositoryID: srcRepo.ID, Repository: "srcrepo",
		Path: "/file.txt", BlobKey: blobKey, BlobStoreID: bs.ID,
		SizeBytes: int64(len(blobData)), ContentType: "text/plain",
	}
	require.NoError(t, src.Assets.Create(ctx, asset))

	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "srcrepo", &buf))

	dst := buildBackupSvc()
	require.NoError(t, dst.BlobStores.Create(ctx, &domain.BlobStore{Name: "default", Type: "local", Config: map[string]any{"path": "/tmp"}}))

	stats, err := dst.ImportRepo(ctx, &buf, "", "skip")
	require.NoError(t, err)
	assert.Equal(t, "srcrepo", stats.Repository)
	assert.Equal(t, 1, stats.Components)
	assert.Equal(t, 1, stats.Assets)
	assert.Equal(t, 1, stats.Blobs)

	rc, _, err := dst.BlobStore.Get(ctx, blobKey)
	require.NoError(t, err)
	rc.Close()
}
```

- [ ] **Step 2.2: Run tests to confirm they fail**

```bash
cd /home/skensel/AI/self_nexus && go test ./internal/service/... -run "TestBackupRepo_Import" -v 2>&1 | tail -20
```

Expected: FAIL — `svc.ImportRepo undefined`.

- [ ] **Step 2.3: Add ImportRepoStats + ImportRepo to backup_service.go**

Add after `ExportRepo` (before `Restore`):

```go
// ImportRepoStats reports what was imported.
type ImportRepoStats struct {
	Repository   string `json:"repository"`
	Components   int    `json:"components"`
	Assets       int    `json:"assets"`
	Blobs        int    `json:"blobs"`
	ConflictMode string `json:"conflictMode"`
}

// ImportRepo reads a per-repository archive (as produced by ExportRepo) and
// creates the repository, components, assets, and blobs in the current instance.
//
// targetName — if non-empty, override the repository name from the archive.
// conflictMode — "skip" (default) | "merge" | "rename":
//   - skip/merge: if repo exists, add only absent components (by name+version+group) and assets (by path).
//   - rename: targetName must be non-empty; returns ErrRepoConflict if targetName is taken.
func (s *BackupService) ImportRepo(ctx context.Context, r io.Reader, targetName, conflictMode string) (*ImportRepoStats, error) {
	if conflictMode == "" {
		conflictMode = "skip"
	}
	if conflictMode == "rename" && targetName == "" {
		return nil, fmt.Errorf("conflictMode=rename requires non-empty targetName")
	}

	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)

	var archivedRepo domain.Repository
	var components []domain.Component
	var assets []domain.Asset
	blobs := map[string][]byte{}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read entry %s: %w", hdr.Name, err)
		}
		switch hdr.Name {
		case "repository.json":
			_ = json.Unmarshal(data, &archivedRepo)
		case "components.json":
			_ = json.Unmarshal(data, &components)
		case "assets.json":
			_ = json.Unmarshal(data, &assets)
		default:
			if strings.HasPrefix(hdr.Name, "blobs/") {
				key := strings.TrimPrefix(hdr.Name, "blobs/")
				blobs[key] = data
			}
		}
	}

	if archivedRepo.Name == "" {
		return nil, fmt.Errorf("invalid archive: missing or empty repository.json")
	}

	finalName := archivedRepo.Name
	if targetName != "" {
		finalName = targetName
	}

	stats := &ImportRepoStats{ConflictMode: conflictMode, Repository: finalName}

	// Resolve or create destination repository.
	destRepo, _ := s.Repos.Get(ctx, finalName)
	if destRepo == nil {
		newRepo := archivedRepo
		newRepo.ID = ""
		newRepo.Name = finalName
		newRepo.BlobStoreID = nil // will be picked up from available blob stores below
		if err := s.Repos.Create(ctx, &newRepo); err != nil {
			return nil, fmt.Errorf("create repository: %w", err)
		}
		destRepo, _ = s.Repos.Get(ctx, finalName)
	} else if conflictMode == "rename" {
		return nil, fmt.Errorf("%w: %q", ErrRepoConflict, finalName)
	}
	if destRepo == nil {
		return nil, fmt.Errorf("repository %q not available after creation", finalName)
	}

	// Pick blob store ID for imported assets.
	blobStoreID := ""
	if destRepo.BlobStoreID != nil {
		blobStoreID = *destRepo.BlobStoreID
	}
	if blobStoreID == "" {
		bss, _ := s.BlobStores.List(ctx)
		if len(bss) > 0 {
			blobStoreID = bss[0].ID
		}
	}

	// Build existing-components map (group+name+version → new ID) for skip/merge dedup.
	existingCompIDs := map[string]string{}
	if conflictMode == "skip" || conflictMode == "merge" {
		page, _ := s.Components.Search(ctx, domain.SearchParams{Repository: finalName})
		if page != nil {
			for _, c := range page.Items {
				k := c.Group + "\x00" + c.Name + "\x00" + c.Version
				existingCompIDs[k] = c.ID
			}
		}
	}

	// Import components.
	compIDMap := map[string]string{} // archived ID → new/existing ID
	for i := range components {
		comp := &components[i]
		oldID := comp.ID
		k := comp.Group + "\x00" + comp.Name + "\x00" + comp.Version

		if id, found := existingCompIDs[k]; found {
			compIDMap[oldID] = id
			continue
		}

		comp.ID = ""
		comp.RepositoryID = destRepo.ID
		comp.Repository = finalName
		if err := s.Components.Create(ctx, comp); err != nil {
			continue
		}
		compIDMap[oldID] = comp.ID
		stats.Components++
	}

	// Import assets.
	for i := range assets {
		a := &assets[i]

		newCompID, ok := compIDMap[a.ComponentID]
		if !ok {
			continue
		}

		// Dedup by path for skip/merge.
		if conflictMode == "skip" || conflictMode == "merge" {
			if existing, _ := s.Assets.GetByPath(ctx, finalName, a.Path); existing != nil {
				continue
			}
		}

		// Restore blob bytes.
		if a.BlobKey != "" {
			if data, ok := blobs[a.BlobKey]; ok {
				_ = s.BlobStore.Put(ctx, a.BlobKey, bytes.NewReader(data), int64(len(data)))
				stats.Blobs++
			}
		}

		a.ID = ""
		a.ComponentID = newCompID
		a.RepositoryID = destRepo.ID
		a.Repository = finalName
		if blobStoreID != "" {
			a.BlobStoreID = blobStoreID
		}
		if err := s.Assets.Create(ctx, a); err != nil {
			continue
		}
		stats.Assets++
	}

	return stats, nil
}
```

- [ ] **Step 2.4: Run ImportRepo tests**

```bash
cd /home/skensel/AI/self_nexus && go test ./internal/service/... -run "TestBackupRepo_Import" -v 2>&1 | tail -30
```

Expected: all 5 import tests PASS.

- [ ] **Step 2.5: Run full test suite**

```bash
cd /home/skensel/AI/self_nexus && go test ./... 2>&1 | tail -10
```

Expected: all pass (≥ 323 tests).

- [ ] **Step 2.6: Commit**

```bash
cd /home/skensel/AI/self_nexus && git add internal/service/backup_service.go internal/service/backup_service_test.go && git commit -m "feat(phase45): add BackupService.ImportRepo + ImportRepoStats"
```

---

## Task 3: Handler methods + Router wiring

**Files:**
- Modify: `internal/api/handlers/backup.go`
- Modify: `internal/api/router.go`

- [ ] **Step 3.1: Add ExportRepo + ImportRepo to BackupHandler**

Add to `internal/api/handlers/backup.go` (after the `Restore` method):

```go
// ExportRepo streams a per-repository backup archive (gzipped tar) to the client.
// GET /api/v1/repositories/:name/export
func (h *BackupHandler) ExportRepo(c *gin.Context) {
	name := c.Param("name")
	ctx := c.Request.Context()

	// Pre-check existence before committing to streaming headers.
	repo, _ := h.svc.Repos.Get(ctx, name)
	if repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found: " + name})
		return
	}

	ts := time.Now().UTC().Format("20060102-150405")
	filename := fmt.Sprintf("nexspence-repo-%s-%s.tar.gz", name, ts)
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Header("Content-Type", "application/x-tar")
	c.Header("Transfer-Encoding", "chunked")
	c.Status(http.StatusOK)

	if err := h.svc.ExportRepo(ctx, name, c.Writer); err != nil {
		_ = err // headers already sent; log if logger available
	}
}

// ImportRepo accepts a per-repository backup archive (multipart field "file")
// and re-creates the repository, components, assets, and blobs.
// POST /api/v1/repositories/import
func (h *BackupHandler) ImportRepo(c *gin.Context) {
	var reader = c.Request.Body
	if c.ContentType() == "multipart/form-data" {
		if err := c.Request.ParseMultipartForm(512 << 20); err == nil {
			if f, _, err := c.Request.FormFile("file"); err == nil {
				defer f.Close()
				reader = f
			}
		}
	}

	targetName := c.Request.FormValue("targetName")
	conflictMode := c.Request.FormValue("conflictMode")
	if conflictMode == "" {
		conflictMode = "skip"
	}

	stats, err := h.svc.ImportRepo(c.Request.Context(), reader, targetName, conflictMode)
	if err != nil {
		if errors.Is(err, service.ErrRepoConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"imported": stats})
}
```

Add required imports to `backup.go`:

```go
import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/service"
)
```

- [ ] **Step 3.2: Wire all backup routes in router.go**

The existing `GET /api/v1/backup/export` and `POST /api/v1/backup/restore` routes are missing from the router. Add them along with the new per-repo routes.

In `internal/api/router.go`, add `backupH` construction near the other handlers (around line 157, after `migrationH`):

```go
backupSvc := &service.BackupService{
    BlobStores: blobRepo,
    Repos:      repoRepo,
    Users:      userRepo,
    Roles:      roleRepo,
    Policies:   cleanupRepo,
    Components: componentRepo,
    Assets:     assetRepo,
    BlobStore:  localBlob,
}
backupH := handlers.NewBackupHandler(backupSvc)
```

Then in the `admin` group (after the migration routes, around line 379):

```go
// ── Backup / Restore (full system) ───────────────────────
admin.GET("/api/v1/backup/export", backupH.Export)
admin.POST("/api/v1/backup/restore", backupH.Restore)

// ── Per-repository Export / Import ───────────────────────
admin.GET("/api/v1/repositories/:name/export", backupH.ExportRepo)
admin.POST("/api/v1/repositories/import", backupH.ImportRepo)
```

**Important:** The route `/api/v1/repositories/:name/export` must be added BEFORE the generic `/api/v1/repositories` list route in the `authed` group, but since these are in different groups (`admin` vs `authed`) and have different depths (`/:name/export` vs `/`), Gin will not conflict them.

Add `service` to the router.go imports if not already present (it already is).

- [ ] **Step 3.3: Verify build**

```bash
cd /home/skensel/AI/self_nexus && go build ./... 2>&1
```

Expected: no errors.

- [ ] **Step 3.4: Run full test suite**

```bash
cd /home/skensel/AI/self_nexus && go test ./... 2>&1 | tail -10
```

Expected: all pass.

- [ ] **Step 3.5: Commit**

```bash
cd /home/skensel/AI/self_nexus && git add internal/api/handlers/backup.go internal/api/router.go && git commit -m "feat(phase45): add ExportRepo/ImportRepo handlers + wire all backup routes"
```

---

## Task 4: Frontend — API client methods

**Files:**
- Modify: `frontend/src/api/client.ts`

- [ ] **Step 4.1: Add exportRepo and importRepo to client.ts**

In `frontend/src/api/client.ts`, find the existing `exportBackup` and `restoreBackup` entries (around line 262). Add two new methods immediately after `restoreBackup`:

```ts
  exportRepo: (name: string) =>
    apiClient.get(`/api/v1/repositories/${encodeURIComponent(name)}/export`, { responseType: 'blob' }),

  importRepo: (file: File, targetName: string, conflictMode: string) => {
    const fd = new FormData()
    fd.append('file', file)
    fd.append('targetName', targetName)
    fd.append('conflictMode', conflictMode)
    return apiClient.post<{ imported: ImportRepoStats }>('/api/v1/repositories/import', fd)
  },
```

Also add the `ImportRepoStats` interface near the other interfaces at the top of the file (around line 60, after `CleanupPreviewResponse`):

```ts
export interface ImportRepoStats {
  repository: string
  components: number
  assets: number
  blobs: number
  conflictMode: string
}
```

- [ ] **Step 4.2: TypeScript check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors.

- [ ] **Step 4.3: Commit**

```bash
cd /home/skensel/AI/self_nexus && git add frontend/src/api/client.ts && git commit -m "feat(phase45): add exportRepo/importRepo API client methods"
```

---

## Task 5: Frontend — Export button in RepositoriesPage

**Files:**
- Modify: `frontend/src/pages/RepositoriesPage.tsx`

- [ ] **Step 5.1: Add Export button to RepoRow**

In `frontend/src/pages/RepositoriesPage.tsx`:

1. Add `Download` to the lucide-react import. The current import line looks like:
   ```ts
   import { Power, Settings2, Trash2, ... } from 'lucide-react'
   ```
   Add `Download` to it.

2. Add `onExport` prop to `RepoRow`:

Find the `RepoRow` function signature (around line 227):
```ts
function RepoRow({
  repo, isAdmin, storeName, onClick, onEdit, onDelete, onToggleOnline,
}: {
  repo: Repository
  isAdmin: boolean
  storeName?: string
  onClick?: () => void
  onEdit: () => void
  onDelete: () => void
  onToggleOnline: (online: boolean) => void
})
```

Change to:
```ts
function RepoRow({
  repo, isAdmin, storeName, onClick, onEdit, onDelete, onToggleOnline, onExport,
}: {
  repo: Repository
  isAdmin: boolean
  storeName?: string
  onClick?: () => void
  onEdit: () => void
  onDelete: () => void
  onToggleOnline: (online: boolean) => void
  onExport: () => void
})
```

3. Add the Export button in the admin actions group (around line 290, before the Settings button):

Find:
```tsx
<HoloButton icon={<Settings2 size={14} />} onClick={e => { e.stopPropagation(); onEdit() }} title="Settings" />
```

Add before it:
```tsx
<HoloButton
  icon={<Download size={14} />}
  onClick={e => { e.stopPropagation(); onExport() }}
  title="Export repository"
/>
```

4. Add `exportBusy` state and `handleExport` in the parent `RepositoriesPage` component. Find where `RepoRow` is rendered (around line 177) and add `onExport` prop:

```tsx
onExport={async () => {
  try {
    const res = await nexspenceApi.exportRepo(repo.name)
    const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)
    const url = URL.createObjectURL(new Blob([res.data]))
    const a = document.createElement('a')
    a.href = url
    a.download = `nexspence-repo-${repo.name}-${ts}.tar.gz`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  } catch {
    // silent — could show toast in future
  }
}}
```

- [ ] **Step 5.2: TypeScript check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors.

- [ ] **Step 5.3: Commit**

```bash
cd /home/skensel/AI/self_nexus && git add frontend/src/pages/RepositoriesPage.tsx && git commit -m "feat(phase45): add Export button to repository card row"
```

---

## Task 6: Frontend — Import section in AdminPage

**Files:**
- Modify: `frontend/src/pages/AdminPage.tsx`

- [ ] **Step 6.1: Add import state and handler**

In `frontend/src/pages/AdminPage.tsx`, near the top of the component (after `exportBusy` state), add:

```ts
const [importFile, setImportFile]           = useState<File | null>(null)
const [importTargetName, setImportTargetName] = useState('')
const [importConflict, setImportConflict]   = useState('skip')
const [importBusy, setImportBusy]           = useState(false)
const [importResult, setImportResult]       = useState<{ imported: ImportRepoStats } | null>(null)
const [importError, setImportError]         = useState<string | null>(null)
```

Add `ImportRepoStats` to the import from `@/api/client`:
```ts
import { nexusApi, nexspenceApi, ServiceStatus, ImportRepoStats } from '@/api/client'
```

Add the import handler after `handleExport`:

```ts
const handleImportRepo = async () => {
  if (!importFile) return
  setImportBusy(true)
  setImportResult(null)
  setImportError(null)
  try {
    const res = await nexspenceApi.importRepo(importFile, importTargetName, importConflict)
    setImportResult(res.data)
  } catch (e: any) {
    setImportError(e.response?.data?.error ?? e.message ?? 'Import failed')
  } finally {
    setImportBusy(false)
  }
}
```

- [ ] **Step 6.2: Add Import UI section to the Backup tab**

In the Backup tab JSX (around line 243, inside `{tab === 'backup' && ( ... )}`), after the existing Restore section, add a divider and Import section:

```tsx
{/* Import Repository */}
<div style={{ marginTop: 24, paddingTop: 20, borderTop: '1px solid rgba(124,92,255,0.15)' }}>
  <span style={{ fontSize: 15, fontWeight: 600, color: 'var(--holo-text)' }}>Import Repository</span>
  <p style={{ fontSize: 12, color: 'var(--holo-text-faint)', margin: '6px 0 16px' }}>
    Import a single repository from a <code>.tar.gz</code> archive exported by Nexspence.
  </p>
  <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
    <div>
      <label style={{ fontSize: 12, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 4 }}>Archive file</label>
      <input
        type="file"
        accept=".tar.gz,.tgz"
        style={{ fontSize: 13, color: 'var(--holo-text)' }}
        onChange={e => {
          const f = e.target.files?.[0] ?? null
          setImportFile(f)
          setImportResult(null)
          setImportError(null)
        }}
      />
    </div>
    <div>
      <label style={{ fontSize: 12, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 4 }}>
        Target name <span style={{ color: 'rgba(229,231,235,0.4)' }}>(optional — overrides name in archive)</span>
      </label>
      <HoloInput
        placeholder="leave blank to use archived name"
        value={importTargetName}
        onChange={e => setImportTargetName(e.target.value)}
        style={{ width: 280 }}
      />
    </div>
    <div>
      <label style={{ fontSize: 12, color: 'var(--holo-text-faint)', display: 'block', marginBottom: 4 }}>
        Conflict mode
      </label>
      <Select
        value={importConflict}
        onChange={setImportConflict}
        options={[
          { value: 'skip',   label: 'Skip — add only absent components/assets' },
          { value: 'merge',  label: 'Merge — same as skip' },
          { value: 'rename', label: 'Rename — create under target name (error if taken)' },
        ]}
        style={{ width: 340 }}
      />
    </div>
    <div>
      <HoloButton
        variant="primary"
        disabled={!importFile || importBusy}
        onClick={handleImportRepo}
      >
        {importBusy ? 'Importing…' : 'Import repository'}
      </HoloButton>
    </div>
    {importResult && (
      <div style={{
        padding: '10px 14px', borderRadius: 8,
        background: 'rgba(34,197,94,0.1)', border: '1px solid rgba(34,197,94,0.3)',
        fontSize: 13, color: 'var(--holo-text)',
      }}>
        Imported <strong>{importResult.imported.components}</strong> components,{' '}
        <strong>{importResult.imported.assets}</strong> assets into{' '}
        <code style={{ color: '#93c5fd' }}>{importResult.imported.repository}</code>
        {importResult.imported.blobs > 0 && <> ({importResult.imported.blobs} blobs)</>}.
      </div>
    )}
    {importError && (
      <div style={{
        padding: '10px 14px', borderRadius: 8,
        background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)',
        fontSize: 13, color: '#fca5a5',
      }}>
        {importError}
      </div>
    )}
  </div>
</div>
```

- [ ] **Step 6.3: TypeScript check**

```bash
cd /home/skensel/AI/self_nexus/frontend && npx tsc --noEmit 2>&1 | head -20
```

Expected: 0 errors.

- [ ] **Step 6.4: Build frontend**

```bash
cd /home/skensel/AI/self_nexus/frontend && npm run build 2>&1 | tail -15
```

Expected: build succeeds, no errors.

- [ ] **Step 6.5: Commit**

```bash
cd /home/skensel/AI/self_nexus && git add frontend/src/pages/AdminPage.tsx && git commit -m "feat(phase45): add Import Repository section to AdminPage Backup tab"
```

---

## Task 7: Update task_plan.md

**Files:**
- Modify: `task_plan.md`

- [ ] **Step 7.1: Mark Phase 45 complete in task_plan.md**

Find the Phase 45 section in `task_plan.md` and update:

```markdown
## Phase 45: Repository Export — Import/Export Round-trip
**Status:** complete (2026-04-28)
```

Also update the Tasks list to checked:
```markdown
- [x] `ExportService`: streaming tar.gz (metadata.json + blobs) via `archive/tar` — extended BackupService
- [x] API: `GET /api/v1/repositories/{name}/export` → streaming download; `POST /api/v1/repositories/import` (multipart tar.gz)
- [x] Frontend: кнопка Export на карточке репозитория (RepositoriesPage); Import wizard в AdminPage
- [x] Full-system backup routes wired: `GET /api/v1/backup/export`, `POST /api/v1/backup/restore`
```

- [ ] **Step 7.2: Final full test run**

```bash
cd /home/skensel/AI/self_nexus && go test ./... 2>&1 | tail -10
```

Expected: all pass.

- [ ] **Step 7.3: Final commit**

```bash
cd /home/skensel/AI/self_nexus && git add task_plan.md && git commit -m "docs(phase45): mark Phase 45 complete"
```
