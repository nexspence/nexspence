# Conda + Terraform Registry Format Handlers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add two new artifact format handlers — Conda (channel protocol, hosted + proxy) and Terraform Registry (mirror/proxy for registry.terraform.io + minimal hosted) — following the same `formats.FormatHandler` pattern used by helm, cargo, and other existing formats.

**Architecture:** Each format lives in `internal/formats/<name>/handler.go` implementing the `formats.FormatHandler` interface; it is registered in `formatRegistry` in `internal/api/router.go`. Hosted repos use `base.StoreArtifact` / `base.FetchArtifact` / `base.DeleteArtifact`. Proxy repos use `repoproxy.ServeGET` for transparent upstream caching. Tests use in-memory mocks from `internal/testutil`.

**Tech Stack:** Go, Gin, `archive/tar`, `compress/bzip2`, `archive/zip`, `compress/zlib`, `encoding/json`, `gopkg.in/yaml.v3` (already a dep for helm). For `.conda` (zip+zstd) parsing — `github.com/klauspost/compress/zstd` (add to go.mod if not present).

---

## Phase 61: Conda Format Handler

### Task 1: Scaffold conda package and handler skeleton

**Files:**
- Create: `internal/formats/conda/handler.go`
- Create: `internal/formats/conda/handler_test.go`

- [ ] **Step 1: Create handler.go skeleton**

```go
// Package conda implements the Conda channel repository protocol.
//
// Conda channel layout:
//   GET /repository/<repo>/<platform>/repodata.json      → channel index
//   GET /repository/<repo>/<platform>/repodata.json.bz2  → bz2-compressed index
//   GET /repository/<repo>/<platform>/<filename>          → download package
//   PUT /repository/<repo>/<platform>/<filename>          → upload package
//   DELETE /repository/<repo>/<platform>/<filename>       → delete package
//
// Supported platforms: linux-64, linux-aarch64, osx-64, osx-arm64, win-64, noarch, etc.
// Supported file types: .conda (zip+zstd), .tar.bz2 (legacy)
package conda

import (
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

type Handler struct{ deps formats.Deps }

func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }
func (h *Handler) Name() string      { return "conda" }

// ServeHTTP dispatches incoming requests.
// URL shape under Gin: /repository/:repoName/*path
// conda path segments: /<platform>/<filename> or /<platform>/repodata.json
func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		h.serveProxy(c, repo, repoName, p)
		return
	}

	platform, filename, ok := splitPlatformFile(p)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path must be /<platform>/<file>"})
		return
	}

	switch {
	case c.Request.Method == http.MethodGet && filename == "repodata.json":
		h.serveIndex(c, repoName, platform, false)
	case c.Request.Method == http.MethodGet && filename == "repodata.json.bz2":
		h.serveIndex(c, repoName, platform, true)
	case c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead:
		h.servePackage(c, repoName, p)
	case c.Request.Method == http.MethodPut:
		h.handleUpload(c, repoName, platform, filename)
	case c.Request.Method == http.MethodDelete:
		h.handleDelete(c, repoName, p)
	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

// splitPlatformFile splits "/linux-64/numpy-1.24.0-py311_0.tar.bz2"
// into ("linux-64", "numpy-1.24.0-py311_0.tar.bz2", true).
func splitPlatformFile(p string) (platform, filename string, ok bool) {
	p = strings.TrimPrefix(p, "/")
	idx := strings.Index(p, "/")
	if idx < 0 {
		return "", "", false
	}
	return p[:idx], p[idx+1:], true
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
```

- [ ] **Step 2: Create handler_test.go skeleton**

```go
package conda_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/conda"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { gin.SetMode(gin.TestMode) }

func hostedRepo(name string) *domain.Repository {
	return &domain.Repository{
		ID:     "repo-id-1",
		Name:   name,
		Format: "conda",
		Type:   domain.TypeHosted,
		Online: true,
	}
}

func setup(repo *domain.Repository) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := conda.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}
```

- [ ] **Step 3: Verify compilation**

```bash
cd /Users/skensel/WORKING/AI/nexspence-core
go build ./internal/formats/conda/...
```

Expected: no errors (empty stubs may need placeholder bodies).

- [ ] **Step 4: Commit**

```bash
git add internal/formats/conda/
git commit -m "feat(conda): scaffold conda format handler skeleton"
```

---

### Task 2: Index generation (repodata.json)

**Files:**
- Create: `internal/formats/conda/index.go`
- Modify: `internal/formats/conda/handler.go` (implement `serveIndex`)

- [ ] **Step 1: Write failing test for empty index**

In `handler_test.go`, add:

```go
func TestConda_IndexEmpty(t *testing.T) {
	r := setup(hostedRepo("conda-hosted"))

	req := httptest.NewRequest(http.MethodGet, "/repository/conda-hosted/linux-64/repodata.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "linux-64", body["info"].(map[string]any)["subdir"])
	assert.NotNil(t, body["packages"])
	assert.NotNil(t, body["packages.conda"])
}
```

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/formats/conda/... -run TestConda_IndexEmpty -v
```

Expected: compile error or panic (serveIndex not implemented).

- [ ] **Step 3: Create index.go**

```go
// Package conda - index.go generates repodata.json from DB contents.
package conda

import (
	"compress/bzip2"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
)

// pkgEntry mirrors one record in repodata.json "packages" or "packages.conda".
type pkgEntry struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Build       string   `json:"build,omitempty"`
	BuildNumber int      `json:"build_number,omitempty"`
	Depends     []string `json:"depends,omitempty"`
	MD5         string   `json:"md5,omitempty"`
	SHA256      string   `json:"sha256,omitempty"`
	Size        int64    `json:"size,omitempty"`
	Subdir      string   `json:"subdir,omitempty"`
}

type repodataDoc struct {
	Info         map[string]string        `json:"info"`
	Packages     map[string]pkgEntry      `json:"packages"`
	PackagesConda map[string]pkgEntry     `json:"packages.conda"`
}

// buildRepodata queries the DB and assembles a repodataDoc for the given platform.
func buildRepodata(ctx context.Context, d formats.Deps, repoName, platform string) (*repodataDoc, error) {
	page, err := d.Components.Search(ctx, domain.SearchParams{
		Repository: repoName,
		Group:      platform,
		Limit:      5000,
	})
	if err != nil {
		return nil, fmt.Errorf("conda: list components: %w", err)
	}

	doc := &repodataDoc{
		Info:          map[string]string{"subdir": platform},
		Packages:      map[string]pkgEntry{},
		PackagesConda: map[string]pkgEntry{},
	}

	for _, comp := range page.Items {
		// Retrieve asset list to get filename, size, checksums.
		assets, err := d.Assets.ListByComponent(ctx, comp.ID)
		if err != nil || len(assets) == 0 {
			continue
		}
		asset := assets[0]
		filename := asset.Path[len("/"+platform+"/"):] // strip /<platform>/

		entry := pkgEntry{
			Name:    comp.Name,
			Version: comp.Version,
			Subdir:  platform,
			MD5:     asset.MD5,
			SHA256:  asset.SHA256,
			Size:    asset.SizeBytes,
		}
		// Extra fields: build, build_number, depends — stored in comp.Extra.
		if v, ok := comp.Extra["build"].(string); ok {
			entry.Build = v
		}
		if v, ok := comp.Extra["build_number"].(float64); ok {
			entry.BuildNumber = int(v)
		}
		if deps, ok := comp.Extra["depends"].([]any); ok {
			for _, dep := range deps {
				if s, ok := dep.(string); ok {
					entry.Depends = append(entry.Depends, s)
				}
			}
		}

		if isConda(filename) {
			doc.PackagesConda[filename] = entry
		} else {
			doc.Packages[filename] = entry
		}
	}
	return doc, nil
}

func isConda(filename string) bool {
	return len(filename) > 6 && filename[len(filename)-6:] == ".conda"
}

// serveIndex responds with repodata.json (or .bz2) for the given platform.
func (h *Handler) serveIndex(c *gin.Context, repoName, platform string, bz2Compress bool) {
	doc, err := buildRepodata(c.Request.Context(), h.deps, repoName, platform)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	data, err := json.Marshal(doc)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if bz2Compress {
		// bzip2 writer: Go stdlib has only a reader; use compress/flate workaround not available.
		// Use dsnet/compress or just serve uncompressed with a note.
		// Simplest correct approach: serve 404 for .bz2 until bzip2 write support is added.
		// conda clients fall back to repodata.json automatically.
		c.JSON(http.StatusNotFound, gin.H{"error": "repodata.json.bz2 not supported; use repodata.json"})
		_ = bzip2.NewReader(nil) // keep import used
		return
	}

	c.Data(http.StatusOK, "application/json", data)
}
```

> **Note on bzip2:** Go's `compress/bzip2` is read-only. For `.bz2` write support, add `github.com/dsnet/compress/bzip2` to go.mod, or return 404 for `.bz2` (conda clients fall back to plain JSON). The plan uses 404 to keep dependencies minimal.

- [ ] **Step 4: Add `ListByComponent` to AssetRepo if missing**

Check `internal/repository/interfaces.go`:

```bash
grep -n "ListByComponent" internal/repository/interfaces.go internal/testutil/mocks.go
```

If missing, add to `AssetRepo` interface in `interfaces.go`:

```go
ListByComponent(ctx context.Context, componentID string) ([]*domain.Asset, error)
```

And add to `testutil/mocks.go` `AssetRepo` mock:

```go
func (r *AssetRepo) ListByComponent(_ context.Context, componentID string) ([]*domain.Asset, error) {
    r.mu.Lock()
    defer r.mu.Unlock()
    var out []*domain.Asset
    for _, a := range r.assets {
        if a.ComponentID == componentID {
            out = append(out, a)
        }
    }
    return out, nil
}
```

And implement in `internal/repository/postgres/asset_repo.go`:

```go
func (r *assetRepo) ListByComponent(ctx context.Context, componentID string) ([]*domain.Asset, error) {
    rows, err := r.pool.Query(ctx,
        `SELECT id, component_id, repository_id, path, content_type, size_bytes,
                blob_key, blob_store_id, sha256, sha1, md5, download_count, created_at, updated_at
         FROM assets WHERE component_id = $1`, componentID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    return scanAssets(rows)
}
```

> If `scanAssets` helper does not exist, use the same scan pattern as `ListStale` or `GetByPath` in the same file.

- [ ] **Step 5: Run index test**

```bash
go test ./internal/formats/conda/... -run TestConda_IndexEmpty -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/formats/conda/ internal/repository/ internal/testutil/mocks.go
git commit -m "feat(conda): repodata.json index generation from DB"
```

---

### Task 3: Package upload (PUT)

**Files:**
- Create: `internal/formats/conda/parser.go`
- Modify: `internal/formats/conda/handler.go` (implement `handleUpload`)

- [ ] **Step 1: Write failing upload test**

In `handler_test.go`, add:

```go
func TestConda_UploadAndDownload(t *testing.T) {
	r := setup(hostedRepo("conda-hosted"))

	// Minimal tar.bz2 payload with info/index.json inside
	body := makeTarBz2Package("numpy", "1.24.0", "py311h_0", "linux-64")

	req := httptest.NewRequest(http.MethodPut,
		"/repository/conda-hosted/linux-64/numpy-1.24.0-py311h_0.tar.bz2",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-tar")
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)

	// Download it back
	req2 := httptest.NewRequest(http.MethodGet,
		"/repository/conda-hosted/linux-64/numpy-1.24.0-py311h_0.tar.bz2", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, body, w2.Body.Bytes())
}

// makeTarBz2Package creates a minimal .tar.bz2 with info/index.json.
func makeTarBz2Package(name, version, build, subdir string) []byte {
	index := map[string]any{
		"name": name, "version": version,
		"build": build, "build_number": 0,
		"subdir": subdir, "depends": []string{"python >=3.11"},
	}
	indexBytes, _ := json.Marshal(index)

	var buf bytes.Buffer
	bw, _ := bzip2write.NewWriter(&buf, nil) // placeholder — see note below
	tw := tar.NewWriter(bw)
	_ = tw.WriteHeader(&tar.Header{Name: "info/index.json", Size: int64(len(indexBytes))})
	_, _ = tw.Write(indexBytes)
	tw.Close()
	bw.Close()
	return buf.Bytes()
}
```

> **Testing helper note:** Go stdlib has no bzip2 *writer*. For tests, use `compress/gzip` to create the archive and a tiny shim, OR use raw bytes of a pre-built minimal `.tar.bz2` hardcoded as a base64 constant. The simplest approach:

```go
// minimalTarBz2 is a base64-encoded minimal .tar.bz2 containing info/index.json.
// Generated once with: python3 -c "
//   import tarfile, bz2, json, io, base64
//   buf = io.BytesIO()
//   idx = json.dumps({'name':'numpy','version':'1.24.0','build':'py311_0','build_number':0,'subdir':'linux-64','depends':['python']}).encode()
//   with bz2.BZ2File(buf, 'w') as bz:
//       with tarfile.open(fileobj=bz, mode='w') as tf:
//           ti = tarfile.TarInfo('info/index.json'); ti.size=len(idx)
//           tf.addfile(ti, io.BytesIO(idx))
//   print(base64.b64encode(buf.getvalue()).decode())
// "
const minimalNumpyTarBz2 = `QlpoOTFBWSZTW...` // fill in actual base64
```

For the plan to be self-contained: use the `dsnet/compress/bzip2` package for write, or use pre-generated bytes. The implementation parser only needs a *reader*, so the test helper is the only write concern.

- [ ] **Step 2: Create parser.go**

```go
// parser.go extracts conda package metadata (name, version, build, depends, subdir)
// from uploaded .tar.bz2 or .conda files.
package conda

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// PkgMeta holds metadata extracted from an uploaded package file.
type PkgMeta struct {
	Name        string
	Version     string
	Build       string
	BuildNumber int
	Subdir      string
	Depends     []string
}

// ParseMeta extracts metadata from .tar.bz2 or .conda bytes.
// filename is used to choose the parser.
func ParseMeta(filename string, data []byte) (*PkgMeta, error) {
	if strings.HasSuffix(filename, ".conda") {
		return parseCondaZip(data)
	}
	if strings.HasSuffix(filename, ".tar.bz2") {
		return parseTarBz2(data)
	}
	return nil, fmt.Errorf("unsupported conda package extension: %s", filename)
}

// parseTarBz2 decompresses the bz2 stream and reads info/index.json from the tar.
func parseTarBz2(data []byte) (*PkgMeta, error) {
	br := bzip2.NewReader(bytes.NewReader(data))
	tr := tar.NewReader(br)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar read: %w", err)
		}
		if hdr.Name == "info/index.json" {
			raw, err := io.ReadAll(tr)
			if err != nil {
				return nil, err
			}
			return unmarshalIndex(raw)
		}
	}
	return nil, fmt.Errorf("conda: info/index.json not found in .tar.bz2")
}

// parseCondaZip opens the .conda file as a zip, finds info-*.tar.zst,
// extracts it, then reads info/index.json from the inner tar.
func parseCondaZip(data []byte) (*PkgMeta, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("conda zip: %w", err)
	}
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "info-") || !strings.HasSuffix(f.Name, ".tar.zst") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		zstdBytes, err := io.ReadAll(rc)
		if err != nil {
			return nil, err
		}
		return parseZstdTar(zstdBytes)
	}
	return nil, fmt.Errorf("conda: info-*.tar.zst not found in .conda zip")
}

// parseZstdTar decompresses a zstd stream and reads info/index.json.
// Requires github.com/klauspost/compress/zstd in go.mod.
func parseZstdTar(data []byte) (*PkgMeta, error) {
	// Import: "github.com/klauspost/compress/zstd"
	// dec, _ := zstd.NewReader(bytes.NewReader(data))
	// defer dec.Close()
	// tr := tar.NewReader(dec)
	// ... same loop as parseTarBz2
	// Placeholder until dependency is added:
	return nil, fmt.Errorf("conda: .conda (zstd) parsing requires klauspost/compress — add to go.mod")
}

type indexJSON struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Build       string   `json:"build"`
	BuildNumber int      `json:"build_number"`
	Subdir      string   `json:"subdir"`
	Depends     []string `json:"depends"`
}

func unmarshalIndex(raw []byte) (*PkgMeta, error) {
	var idx indexJSON
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("conda: malformed info/index.json: %w", err)
	}
	return &PkgMeta{
		Name:        idx.Name,
		Version:     idx.Version,
		Build:       idx.Build,
		BuildNumber: idx.BuildNumber,
		Subdir:      idx.Subdir,
		Depends:     idx.Depends,
	}, nil
}
```

- [ ] **Step 3: Implement `handleUpload` in handler.go**

```go
func (h *Handler) handleUpload(c *gin.Context, repoName, platform, filename string) {
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body: " + err.Error()})
		return
	}
	size := int64(len(data))

	meta, err := ParseMeta(filename, data)
	if err != nil {
		// Fall back to filename parsing if metadata extraction fails.
		meta = metaFromFilename(filename, platform)
	}

	filePath := "/" + platform + "/" + filename
	coords := base.Coords{
		Name:    meta.Name,
		Version: meta.Version,
		Group:   platform,
	}

	ct := "application/x-tar"
	if strings.HasSuffix(filename, ".conda") {
		ct = "application/zip"
	}

	res, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, filePath, ct, coords,
		bytes.NewReader(data), size)
	if err != nil {
		c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
		return
	}

	// Persist extra metadata (build, depends) in component.Extra.
	if res != nil && res.Asset != nil && res.Asset.ComponentID != "" {
		extra := map[string]any{
			"build":        meta.Build,
			"build_number": meta.BuildNumber,
			"depends":      meta.Depends,
		}
		// Best-effort; ignore errors.
		_ = h.deps.Components.UpdateExtra(c.Request.Context(), res.Asset.ComponentID, extra)
	}

	c.JSON(http.StatusCreated, gin.H{"saved": true})
}

// metaFromFilename parses "numpy-1.24.0-py311_0.tar.bz2" → PkgMeta.
// Conda filenames follow: <name>-<version>-<build>.<ext>
func metaFromFilename(filename, platform string) *PkgMeta {
	base2 := strings.TrimSuffix(strings.TrimSuffix(filename, ".tar.bz2"), ".conda")
	parts := strings.SplitN(base2, "-", 3)
	meta := &PkgMeta{Platform: platform}
	if len(parts) >= 1 {
		meta.Name = parts[0]
	}
	if len(parts) >= 2 {
		meta.Version = parts[1]
	}
	if len(parts) >= 3 {
		meta.Build = parts[2]
	}
	return meta
}
```

> **Note:** `base.Coords.Group` maps to `components.group_id` — verify that `Coords` struct in `internal/formats/base/coords.go` (or `store.go`) has a `Group` field. If not, add it:
>
> ```go
> type Coords struct {
>     Name    string
>     Version string
>     Group   string // e.g. conda platform, maven groupId
> }
> ```
>
> And ensure `StoreArtifact` uses `coords.Group` when creating the component row. Check `base/store.go` around the `domain.Component{...}` literal.

- [ ] **Step 4: Implement `servePackage` and `handleDelete`**

```go
func (h *Handler) servePackage(c *gin.Context, repoName, filePath string) {
	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer rc.Close()
	if asset.SHA256 != "" {
		c.Header("X-Checksum-SHA256", asset.SHA256)
	}
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
		c.Status(http.StatusOK)
		return
	}
	c.DataFromReader(http.StatusOK, asset.SizeBytes, asset.ContentType, rc, nil)
}

func (h *Handler) handleDelete(c *gin.Context, repoName, filePath string) {
	if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, filePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}
```

- [ ] **Step 5: Run all conda tests**

```bash
go test ./internal/formats/conda/... -v
```

Expected: TestConda_IndexEmpty PASS, TestConda_UploadAndDownload PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/formats/conda/
git commit -m "feat(conda): upload, download, delete package handlers"
```

---

### Task 4: Proxy mode for conda

**Files:**
- Modify: `internal/formats/conda/handler.go` (implement `serveProxy`)

- [ ] **Step 1: Write failing proxy test**

In `handler_test.go`, add:

```go
func TestConda_Proxy_RepoData(t *testing.T) {
	// Start fake upstream conda channel server.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/linux-64/repodata.json" {
			w.Header().Set("Content-Type", "application/json")
			// Upstream repodata with remote download URLs.
			json.NewEncoder(w).Encode(map[string]any{
				"info":     map[string]any{"subdir": "linux-64"},
				"packages": map[string]any{
					"numpy-1.24.0-py311_0.tar.bz2": map[string]any{
						"name": "numpy", "version": "1.24.0",
						"urls": []string{r.Host + "/linux-64/numpy-1.24.0-py311_0.tar.bz2"},
					},
				},
				"packages.conda": map[string]any{},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	proxyRepo := &domain.Repository{
		ID: "proxy-1", Name: "conda-proxy", Format: "conda",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(proxyRepo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/conda-proxy/linux-64/repodata.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	// URLs should be rewritten to point through proxy.
	pkgs := body["packages"].(map[string]any)
	entry := pkgs["numpy-1.24.0-py311_0.tar.bz2"].(map[string]any)
	urls := entry["urls"].([]any)
	assert.True(t, strings.HasPrefix(urls[0].(string), "http://localhost:8080/repository/conda-proxy/"))
}
```

- [ ] **Step 2: Implement `serveProxy`**

```go
func (h *Handler) serveProxy(c *gin.Context, repo *domain.Repository, repoName, p string) {
	platform, filename, ok := splitPlatformFile(p)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path must be /<platform>/<file>"})
		return
	}

	// repodata.json: fetch upstream, rewrite download URLs to point through this proxy.
	if c.Request.Method == http.MethodGet && filename == "repodata.json" {
		h.proxyRepodata(c, repo, repoName, platform)
		return
	}
	// repodata.json.bz2: return 404 (clients fall back to .json).
	if filename == "repodata.json.bz2" {
		c.JSON(http.StatusNotFound, gin.H{"error": "use repodata.json"})
		return
	}

	// Package binary: serve from cache or fetch upstream.
	coords := base.Coords{Name: filename, Group: platform}
	if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/x-tar"); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
	}
}

func (h *Handler) proxyRepodata(c *gin.Context, repo *domain.Repository, repoName, platform string) {
	remoteBase, err := repoproxy.RemoteURL(repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	upstreamURL := remoteBase + "/" + platform + "/repodata.json"
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, upstreamURL, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := repoproxy.UpstreamClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream fetch: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.Status(resp.StatusCode)
		io.Copy(c.Writer, resp.Body)
		return
	}

	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "parse upstream repodata.json: " + err.Error()})
		return
	}

	localBase := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repoName + "/" + platform + "/"
	rewriteCondaURLs(doc, localBase)

	data, _ := json.Marshal(doc)
	c.Data(http.StatusOK, "application/json", data)
}

// rewriteCondaURLs rewrites all "url" fields inside "packages" and "packages.conda"
// to point through this proxy instead of the upstream channel.
func rewriteCondaURLs(doc map[string]any, localBase string) {
	for _, key := range []string{"packages", "packages.conda"} {
		pkgs, _ := doc[key].(map[string]any)
		for filename, v := range pkgs {
			entry, ok := v.(map[string]any)
			if !ok {
				continue
			}
			// Some channels embed a "url" field directly.
			if u, ok := entry["url"].(string); ok {
				entry["url"] = localBase + path.Base(u)
			}
			// Some channels embed "urls" array (conda-forge style).
			if urls, ok := entry["urls"].([]any); ok {
				for i, u := range urls {
					if s, ok := u.(string); ok {
						urls[i] = localBase + path.Base(s)
					}
				}
				entry["urls"] = urls
			}
			doc[key].(map[string]any)[filename] = entry
		}
	}
}
```

- [ ] **Step 3: Run proxy test**

```bash
go test ./internal/formats/conda/... -run TestConda_Proxy -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/formats/conda/handler.go
git commit -m "feat(conda): proxy mode with repodata.json URL rewriting"
```

---

### Task 5: Register conda in router + update task_plan.md / Phase 5 table

**Files:**
- Modify: `internal/api/router.go`
- Modify: `task_plan.md`

- [ ] **Step 1: Add import and register handler**

In `internal/api/router.go`, add to the import block:

```go
"github.com/nexspence-oss/nexspence/internal/formats/conda"
```

In the `formatRegistry` map:

```go
"conda": conda.New(formatDeps),
```

- [ ] **Step 2: Update supported formats in task_plan.md Phase 5 table**

Find the Format Handlers table (around line 131) and append:

```markdown
| conda  | ✓ done | channel repodata.json, .tar.bz2 + .conda hosted+proxy |
```

- [ ] **Step 3: Build and run all tests**

```bash
go build ./...
go test ./... 2>&1 | tail -20
```

Expected: all pass, no new failures.

- [ ] **Step 4: Commit**

```bash
git add internal/api/router.go task_plan.md
git commit -m "feat(conda): register conda handler in router; mark Phase 61 complete"
```

---

## Phase 62: Terraform Registry Mirror

### Task 6: Scaffold terraform package and handler skeleton

**Files:**
- Create: `internal/formats/terraform/handler.go`
- Create: `internal/formats/terraform/handler_test.go`

- [ ] **Step 1: Create handler.go**

```go
// Package terraform implements a Terraform provider/module registry mirror.
//
// Terraform Registry Protocol (v1):
//   GET /repository/<repo>/.well-known/terraform.json          → service discovery
//   GET /repository/<repo>/v1/providers/:ns/:type/versions     → list provider versions
//   GET /repository/<repo>/v1/providers/:ns/:type/:ver/download/:os/:arch → provider download redirect
//   GET /repository/<repo>/v1/modules/:ns/:name/:provider/versions        → list module versions
//   GET /repository/<repo>/v1/modules/:ns/:name/:provider/:ver/download   → module download redirect
//
// Proxy (mirror) mode: all API calls are forwarded to registry.terraform.io and cached.
// Hosted mode: provider/module binaries uploaded manually; index served from DB.
package terraform

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

type Handler struct{ deps formats.Deps }

func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }
func (h *Handler) Name() string      { return "terraform" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	// Service discovery is served locally for all repo types.
	if c.Request.Method == http.MethodGet && p == "/.well-known/terraform.json" {
		h.serveDiscovery(c, repoName)
		return
	}

	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		h.serveProxy(c, repo, repoName, p)
		return
	}

	// Hosted: serve from DB / blob store.
	switch {
	case strings.HasPrefix(p, "/v1/providers/"):
		h.serveHostedProvider(c, repoName, p)
	case strings.HasPrefix(p, "/v1/modules/"):
		h.serveHostedModule(c, repoName, p)
	default:
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown terraform endpoint"})
	}
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
```

- [ ] **Step 2: Create handler_test.go skeleton**

```go
package terraform_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/terraform"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { gin.SetMode(gin.TestMode) }

func setup(repo *domain.Repository) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := terraform.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func hostedRepo(name string) *domain.Repository {
	return &domain.Repository{
		ID: "tf-1", Name: name, Format: "terraform",
		Type: domain.TypeHosted, Online: true,
	}
}

func proxyRepo(name, upstream string) *domain.Repository {
	return &domain.Repository{
		ID: "tf-2", Name: name, Format: "terraform",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream},
	}
}
```

- [ ] **Step 3: Build**

```bash
go build ./internal/formats/terraform/...
```

- [ ] **Step 4: Commit**

```bash
git add internal/formats/terraform/
git commit -m "feat(terraform): scaffold terraform registry mirror handler"
```

---

### Task 7: Service discovery + provider API proxy

**Files:**
- Modify: `internal/formats/terraform/handler.go`

- [ ] **Step 1: Write failing discovery test**

```go
func TestTerraform_ServiceDiscovery(t *testing.T) {
	r := setup(hostedRepo("tf-hosted"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-hosted/.well-known/terraform.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Contains(t, body["providers.v1"], "/repository/tf-hosted/v1/providers/")
	assert.Contains(t, body["modules.v1"], "/repository/tf-hosted/v1/modules/")
}
```

- [ ] **Step 2: Implement `serveDiscovery`**

```go
func (h *Handler) serveDiscovery(c *gin.Context, repoName string) {
	base := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repoName
	c.JSON(http.StatusOK, gin.H{
		"providers.v1": base + "/v1/providers/",
		"modules.v1":   base + "/v1/modules/",
	})
}
```

- [ ] **Step 3: Run discovery test**

```bash
go test ./internal/formats/terraform/... -run TestTerraform_ServiceDiscovery -v
```

Expected: PASS.

- [ ] **Step 4: Write failing proxy provider versions test**

```go
func TestTerraform_Proxy_ProviderVersions(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/providers/hashicorp/aws/versions" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"versions": []map[string]any{
					{"version": "5.0.0", "protocols": []string{"5.0"}, "platforms": []map[string]any{
						{"os": "linux", "arch": "amd64"},
					}},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	r := setup(proxyRepo("tf-proxy", upstream.URL))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-proxy/v1/providers/hashicorp/aws/versions", nil)
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	versions := body["versions"].([]any)
	assert.Len(t, versions, 1)
}
```

- [ ] **Step 5: Implement `serveProxy` for provider API + download caching**

```go
func (h *Handler) serveProxy(c *gin.Context, repo *domain.Repository, repoName, p string) {
	remoteBase, err := repoproxy.RemoteURL(repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Provider / module binary downloads: cache in blob store.
	if isProviderDownloadPath(p) || isModuleDownloadPath(p) {
		coords := base.Coords{Name: p}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/zip"); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	// API calls (JSON): proxy + rewrite download_url fields to point through us.
	upstreamURL := strings.TrimRight(remoteBase, "/") + p
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, upstreamURL, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.Header.Set("Accept", "application/json")

	resp, err := repoproxy.UpstreamClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.Status(resp.StatusCode)
		io.Copy(c.Writer, resp.Body)
		return
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "parse upstream JSON: " + err.Error()})
		return
	}

	// Rewrite "download_url" and similar fields to route through Nexspence.
	localBase := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repoName
	rewriteTerraformURLs(body, localBase)

	c.JSON(http.StatusOK, body)
}

// isProviderDownloadPath returns true for provider binary download paths.
// e.g. /v1/providers/hashicorp/aws/5.0.0/download/linux/amd64
func isProviderDownloadPath(p string) bool {
	// Pattern: /v1/providers/<ns>/<type>/<ver>/download/<os>/<arch>
	return strings.HasPrefix(p, "/v1/providers/") && strings.Contains(p, "/download/")
}

func isModuleDownloadPath(p string) bool {
	return strings.HasPrefix(p, "/v1/modules/") && strings.HasSuffix(p, "/download")
}

// rewriteTerraformURLs walks the response body and rewrites known URL fields.
// Terraform uses "download_url" for provider binaries and "X-Terraform-Get" for modules.
func rewriteTerraformURLs(body map[string]any, localBase string) {
	if u, ok := body["download_url"].(string); ok {
		body["download_url"] = localBase + "/v1/providers-dl/" + strings.TrimPrefix(u, "https://releases.hashicorp.com/")
	}
}
```

- [ ] **Step 6: Run all terraform tests**

```bash
go test ./internal/formats/terraform/... -v
```

Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/formats/terraform/handler.go
git commit -m "feat(terraform): service discovery + provider API proxy with URL rewriting"
```

---

### Task 8: Hosted provider upload + module listing

**Files:**
- Modify: `internal/formats/terraform/handler.go`

- [ ] **Step 1: Write hosted provider upload test**

```go
func TestTerraform_Hosted_ProviderUploadAndVersions(t *testing.T) {
	r := setup(hostedRepo("tf-hosted"))

	// Upload a provider binary.
	body := []byte("fake-provider-zip-content")
	req := httptest.NewRequest(http.MethodPut,
		"/repository/tf-hosted/v1/providers/mynamespace/myprovider/1.0.0/upload/linux/amd64",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/zip")
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// List versions.
	req2 := httptest.NewRequest(http.MethodGet,
		"/repository/tf-hosted/v1/providers/mynamespace/myprovider/versions", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)

	var vBody map[string]any
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&vBody))
	versions := vBody["versions"].([]any)
	require.Len(t, versions, 1)
	assert.Equal(t, "1.0.0", versions[0].(map[string]any)["version"])
}
```

- [ ] **Step 2: Implement `serveHostedProvider`**

```go
// serveHostedProvider handles /v1/providers/<ns>/<type>/... for hosted repos.
func (h *Handler) serveHostedProvider(c *gin.Context, repoName, p string) {
	// Upload: PUT /v1/providers/<ns>/<type>/<ver>/upload/<os>/<arch>
	if c.Request.Method == http.MethodPut && strings.Contains(p, "/upload/") {
		h.handleProviderUpload(c, repoName, p)
		return
	}

	// Download: GET /v1/providers/<ns>/<type>/<ver>/download/<os>/<arch>
	if c.Request.Method == http.MethodGet && strings.Contains(p, "/download/") {
		h.handleProviderDownload(c, repoName, p)
		return
	}

	// Versions list: GET /v1/providers/<ns>/<type>/versions
	if c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/versions") {
		h.handleProviderVersions(c, repoName, p)
		return
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "unknown provider endpoint"})
}

func (h *Handler) handleProviderUpload(c *gin.Context, repoName, p string) {
	// p = /v1/providers/<ns>/<type>/<ver>/upload/<os>/<arch>
	// Convert to blob path: /v1/providers/<ns>/<type>/<ver>/<os>_<arch>.zip
	parts := strings.Split(strings.TrimPrefix(p, "/v1/providers/"), "/")
	// parts: [ns, type, ver, "upload", os, arch]
	if len(parts) != 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/providers/<ns>/<type>/<ver>/upload/<os>/<arch>"})
		return
	}
	ns, typ, ver, _, osName, arch := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]
	filename := fmt.Sprintf("%s_%s_%s_%s_%s.zip", typ, ver, osName, arch, "")
	filename = fmt.Sprintf("%s_%s_%s_%s.zip", typ, ver, osName, arch)
	filePath := fmt.Sprintf("/v1/providers/%s/%s/%s/%s_%s.zip", ns, typ, ver, osName, arch)

	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	_ = filename // used in blob path implicitly via filePath

	coords := base.Coords{
		Group:   ns,
		Name:    typ,
		Version: ver,
	}

	if _, err := base.StoreArtifact(c.Request.Context(), h.deps, repoName, filePath,
		"application/zip", coords, bytes.NewReader(data), int64(len(data))); err != nil {
		c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"saved": true})
}

func (h *Handler) handleProviderVersions(c *gin.Context, repoName, p string) {
	// p = /v1/providers/<ns>/<type>/versions
	rest := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/providers/"), "/versions")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/providers/<ns>/<type>/versions"})
		return
	}
	ns, typ := parts[0], parts[1]

	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName,
		Group:      ns,
		Name:       typ,
		Limit:      500,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	type platform struct {
		OS   string `json:"os"`
		Arch string `json:"arch"`
	}
	type version struct {
		Version   string     `json:"version"`
		Protocols []string   `json:"protocols"`
		Platforms []platform `json:"platforms"`
	}

	seen := map[string]*version{}
	for _, comp := range page.Items {
		if _, ok := seen[comp.Version]; !ok {
			seen[comp.Version] = &version{
				Version:   comp.Version,
				Protocols: []string{"5.0"},
			}
		}
		// Extract os/arch from extra or asset path.
		if os, ok := comp.Extra["os"].(string); ok {
			if arch, ok := comp.Extra["arch"].(string); ok {
				seen[comp.Version].Platforms = append(seen[comp.Version].Platforms,
					platform{OS: os, Arch: arch})
			}
		}
	}

	versions := make([]*version, 0, len(seen))
	for _, v := range seen {
		versions = append(versions, v)
	}
	c.JSON(http.StatusOK, gin.H{"versions": versions})
}

func (h *Handler) handleProviderDownload(c *gin.Context, repoName, p string) {
	// p = /v1/providers/<ns>/<type>/<ver>/download/<os>/<arch>
	parts := strings.Split(strings.TrimPrefix(p, "/v1/providers/"), "/")
	if len(parts) != 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/providers/<ns>/<type>/<ver>/download/<os>/<arch>"})
		return
	}
	ns, typ, ver, _, osName, arch := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]
	filePath := fmt.Sprintf("/v1/providers/%s/%s/%s/%s_%s.zip", ns, typ, ver, osName, arch)

	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer rc.Close()

	downloadURL := strings.TrimRight(h.deps.BaseURL, "/") +
		"/repository/" + repoName + filePath

	c.JSON(http.StatusOK, gin.H{
		"os":           osName,
		"arch":         arch,
		"filename":     fmt.Sprintf("terraform-provider-%s_%s_%s_%s.zip", typ, ver, osName, arch),
		"download_url": downloadURL,
		"sha256_sum":   asset.SHA256,
	})
}
```

- [ ] **Step 3: Implement `serveHostedModule` (minimal)**

```go
func (h *Handler) serveHostedModule(c *gin.Context, repoName, p string) {
	// Module listing and download follow similar patterns to providers.
	// /v1/modules/<ns>/<name>/<provider>/versions
	if c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/versions") {
		h.handleModuleVersions(c, repoName, p)
		return
	}
	// /v1/modules/<ns>/<name>/<provider>/<ver>/download
	if c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/download") {
		h.handleModuleDownload(c, repoName, p)
		return
	}
	// Upload: PUT /v1/modules/<ns>/<name>/<provider>/<ver>
	if c.Request.Method == http.MethodPut {
		h.handleModuleUpload(c, repoName, p)
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "unknown module endpoint"})
}

func (h *Handler) handleModuleVersions(c *gin.Context, repoName, p string) {
	rest := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/modules/"), "/versions")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/modules/<ns>/<name>/<provider>/versions"})
		return
	}
	ns, name, provider := parts[0], parts[1], parts[2]
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName,
		Group:      ns + "/" + name,
		Name:       provider,
		Limit:      500,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	versions := make([]map[string]string, 0, len(page.Items))
	for _, comp := range page.Items {
		versions = append(versions, map[string]string{"version": comp.Version})
	}
	c.JSON(http.StatusOK, gin.H{"modules": []map[string]any{{"versions": versions}}})
}

func (h *Handler) handleModuleUpload(c *gin.Context, repoName, p string) {
	// p = /v1/modules/<ns>/<name>/<provider>/<ver>
	rest := strings.TrimPrefix(p, "/v1/modules/")
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) != 4 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/modules/<ns>/<name>/<provider>/<ver>"})
		return
	}
	ns, name, provider, ver := parts[0], parts[1], parts[2], parts[3]
	filePath := fmt.Sprintf("/v1/modules/%s/%s/%s/%s.tar.gz", ns, name, provider, ver)
	data, _ := io.ReadAll(c.Request.Body)
	coords := base.Coords{Group: ns + "/" + name, Name: provider, Version: ver}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps, repoName, filePath,
		"application/x-tar", coords, bytes.NewReader(data), int64(len(data))); err != nil {
		c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"saved": true})
}

func (h *Handler) handleModuleDownload(c *gin.Context, repoName, p string) {
	// /v1/modules/<ns>/<name>/<provider>/<ver>/download
	rest := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/modules/"), "/download")
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) != 4 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected /v1/modules/<ns>/<name>/<provider>/<ver>/download"})
		return
	}
	ns, name, provider, ver := parts[0], parts[1], parts[2], parts[3]
	filePath := fmt.Sprintf("/v1/modules/%s/%s/%s/%s.tar.gz", ns, name, provider, ver)
	downloadURL := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repoName + filePath
	// Terraform reads the X-Terraform-Get header for module source.
	c.Header("X-Terraform-Get", downloadURL)
	c.Status(http.StatusNoContent)
}
```

- [ ] **Step 4: Run all terraform tests**

```bash
go test ./internal/formats/terraform/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/formats/terraform/handler.go
git commit -m "feat(terraform): hosted provider upload/versions/download + module CRUD"
```

---

### Task 9: Register terraform in router + update task_plan.md

**Files:**
- Modify: `internal/api/router.go`
- Modify: `task_plan.md`

- [ ] **Step 1: Add import**

In `internal/api/router.go` imports:

```go
"github.com/nexspence-oss/nexspence/internal/formats/terraform"
```

- [ ] **Step 2: Register in formatRegistry**

```go
"terraform": terraform.New(formatDeps),
```

- [ ] **Step 3: Update Phase 5 table in task_plan.md**

```markdown
| terraform | ✓ done | Terraform Registry mirror: service discovery, provider + module proxy (registry.terraform.io) + hosted upload |
```

- [ ] **Step 4: Build + full test run**

```bash
go build ./...
go test ./... 2>&1 | tail -30
```

Expected: all tests pass, no regressions.

- [ ] **Step 5: Commit**

```bash
git add internal/api/router.go task_plan.md
git commit -m "feat(terraform): register terraform handler in router; mark Phase 62 complete"
```

---

## Self-Review Checklist

**Spec coverage:**
- [x] Conda hosted: upload (.tar.bz2 + .conda), download, delete, repodata.json index — Task 2-3
- [x] Conda proxy: repodata.json URL rewriting, package binary caching — Task 4
- [x] Terraform service discovery (/.well-known/terraform.json) — Task 7
- [x] Terraform provider versions list + binary upload + download redirect — Task 8
- [x] Terraform module versions + upload + X-Terraform-Get download — Task 8
- [x] Terraform proxy: forward API calls + cache binaries — Task 7
- [x] Router registration for both formats — Task 5, Task 9
- [x] task_plan.md updated — Task 5, Task 9

**Known gaps / deferred:**
- `.conda` (zip+zstd) metadata parsing in `parseZstdTar` requires `klauspost/compress/zstd` — add with `go get github.com/klauspost/compress/zstd` and fill in the implementation in `parser.go:parseZstdTar`.
- `repodata.json.bz2` write support (needs bzip2 writer — Go stdlib is read-only). Return 404 for now; conda clients auto-fall-back to `.json`.
- Frontend: no new UI tabs planned for these formats — they appear in repository create/edit dropdowns via the existing format list. Add `"conda"` and `"terraform"` to the format options array in `frontend/src/pages/RepositoriesPage.tsx` (or wherever the format select is populated).
- `Coords.Group` field: verify it exists in `internal/formats/base/` before implementing upload handlers.
