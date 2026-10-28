// Package conda implements the Conda channel repository protocol.
//
// Conda channel layout:
//
//	GET /repository/<repo>/<platform>/repodata.json      → channel index
//	GET /repository/<repo>/<platform>/current_repodata.json → trimmed channel index (proxy; 404 under minimum_package_age)
//	GET /repository/<repo>/<platform>/repodata.json.bz2  → compressed index (returns 404)
//	GET /repository/<repo>/<platform>/<filename>          → download package
//	PUT /repository/<repo>/<platform>/<filename>          → upload package
//	DELETE /repository/<repo>/<platform>/<filename>       → delete package
//
// Supported platforms: linux-64, linux-aarch64, osx-64, osx-arm64, win-64, noarch, etc.
// Supported file types: .conda (zip+zstd), .tar.bz2 (legacy)
package conda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// Handler serves the Conda channel repository protocol.
type Handler struct {
	deps formats.Deps
	// ages caches per-subdir package timestamps for the minimum-package-age
	// policy (#643); see age_filter.go.
	ages ageTables
}

// New creates a Conda format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "conda" }

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
		h.serveIndex(c, repoName, platform)
	case c.Request.Method == http.MethodGet && filename == "repodata.json.bz2":
		c.JSON(http.StatusNotFound, gin.H{"error": "repodata.json.bz2 not supported; use repodata.json"})
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

func (h *Handler) handleUpload(c *gin.Context, repoName, platform, filename string) {
	// Coordinates come from in-archive metadata, which must be read before
	// StoreArtifact, and a .conda is a zip read from its end — spool to a temp
	// file so memory stays O(1).
	tmp, size, err := spool(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body: " + err.Error()})
		return
	}
	defer cleanupSpool(tmp)
	meta, err := ParseMeta(filename, tmp, size)
	if err != nil {
		log.Printf("nexspence: conda %s/%s/%s: unreadable package metadata, using the filename: %v", repoName, platform, filename, err)
	}
	if meta == nil {
		meta = metaFromFilename(filename)
	}

	filePath := "/" + platform + "/" + filename
	ct := "application/x-tar"
	if isCondaFile(filename) {
		ct = "application/zip"
	}
	coords := componentCoords(platform, meta)
	ctx := c.Request.Context()
	if _, err := base.StoreArtifact(ctx, h.deps, repoName, filePath, ct, coords,
		io.NewSectionReader(tmp, 0, size), size); err != nil {
		c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
		return
	}
	repo, err := h.deps.Repos.Get(ctx, repoName)
	if err == nil {
		err = placeFile(ctx, h.deps, repo, filePath, "", coords, indexFromMeta(meta, platform))
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"saved": true})
}

// componentCoords keys a hosted package by its build as well as its version
// (#677): builds of one version — py310 and py311, say — are different files
// with different dependencies, while the .conda and .tar.bz2 of one build are
// one package and share a component.
func componentCoords(platform string, m *PkgMeta) base.Coords {
	version := m.Version
	if m.Build != "" {
		version += "-" + m.Build
	}
	return base.Coords{Group: platform, Name: m.Name, Version: version}
}

// placeFile records index on the component coords name and moves the file at
// filePath there if it sits elsewhere: a path first stored before builds were
// part of the coordinates keeps its old component on upsert. The old component
// goes once the last such file has left it. A non-empty sha256 is the content
// index was read from; if the path holds other bytes by now, nothing is
// written — they were stored, and described, by the upload that replaced them.
func placeFile(ctx context.Context, d formats.Deps, repo *domain.Repository, filePath, sha256 string, coords base.Coords, index map[string]any) error {
	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name, Format: string(repo.Format),
		Group: coords.Group, Name: coords.Name, Version: coords.Version,
	}
	if err := d.Components.Create(ctx, comp); err != nil {
		return fmt.Errorf("component for %s: %w", filePath, err)
	}
	asset, err := d.Assets.GetByPath(ctx, repo.Name, filePath)
	if err != nil {
		return fmt.Errorf("look up %s: %w", filePath, err)
	}
	if sha256 != "" && asset.SHA256 != sha256 {
		return nil
	}
	if old := asset.ComponentID; old != comp.ID {
		if err := d.Assets.SetComponent(ctx, asset.ID, comp.ID); err != nil {
			return fmt.Errorf("move %s to its build's component: %w", filePath, err)
		}
		if left, err := d.Assets.ListByComponentID(ctx, old); err == nil && len(left) == 0 {
			if err := d.Components.Delete(ctx, old); err != nil && !errors.Is(err, repository.ErrNotFound) {
				return err
			}
		}
	}
	return d.Components.UpdateExtra(ctx, comp.ID, map[string]any{condaIndexKey: index})
}

// spool copies r to a temp file, rewound, for random access. cleanupSpool
// closes and removes it.
func spool(r io.Reader) (*os.File, int64, error) {
	tmp, err := os.CreateTemp("", "conda-*")
	if err != nil {
		return nil, 0, err
	}
	n, err := io.Copy(tmp, r)
	if err == nil {
		_, err = tmp.Seek(0, io.SeekStart)
	}
	if err != nil {
		cleanupSpool(tmp)
		return nil, 0, err
	}
	return tmp, n, nil
}

func cleanupSpool(f *os.File) {
	_ = f.Close()
	_ = os.Remove(f.Name())
}

func (h *Handler) servePackage(c *gin.Context, repoName, filePath string) {
	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = rc.Close() }()
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
	c.Status(http.StatusNoContent)
}

func (h *Handler) serveProxy(c *gin.Context, repo *domain.Repository, repoName, p string) {
	platform, filename, ok := splitPlatformFile(p)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path must be /<platform>/<file>"})
		return
	}

	if c.Request.Method == http.MethodGet && isIndexDocument(filename) {
		h.proxyIndex(c, repo, repoName, platform, filename)
		return
	}
	if want, ok := refusedIndexVariant(filename); ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "use " + want})
		return
	}

	// Package binary: cache via repoproxy. Conda packages are immutable
	// (the index documents — which are not — are handled by proxyIndex above).
	// A repodata.json entry may point below the subdir, so filename can carry a
	// directory; the component is named after the package file alone.
	coords := base.Coords{Name: path.Base(filename), Group: platform}
	// Before ServeGET, which would answer a cached file without asking: the
	// age policy refuses a cached one too (#643).
	if minAge := repoproxy.MinimumPackageAge(repo); minAge > 0 && isPackageFile(filename) &&
		!h.packageAgeAllowed(c, repo, p, platform, minAge) {
		return
	}
	if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/x-tar", 0); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
	}
}

// indexDocuments are the channel documents that carry package download URLs. All
// three share repodata.json's schema and resolve their entries against the subdir
// they were fetched from, so one rewriter serves them all, and none of them is
// immutable — each is read from upstream on every request (#145).
//
// current_repodata.json is repodata.json trimmed to the newest build of each
// package and is what a recent conda client asks for first. The third is the
// unpatched index conda-index writes beside them, which a client reaches by naming
// it in repodata_fns.
var indexDocuments = []string{
	"repodata.json",
	"current_repodata.json",
	"repodata_from_packages.json",
}

// isIndexDocument reports whether filename is an index document this proxy rewrites.
func isIndexDocument(filename string) bool {
	for _, name := range indexDocuments {
		if filename == name {
			return true
		}
	}
	return false
}

// refusedIndexVariant reports whether filename is an alternate encoding of an index
// document and, if so, names the document the client should ask for instead.
//
// Conda publishes each index in several encodings — .json.bz2, .json.zst, and the
// .jlap patch stream — and every one of them carries the package URLs that have to
// be rewritten onto this proxy. Serving one means decompressing it, rewriting it and
// re-encoding it on every request (and, for zstd, carrying a codec this module does
// not otherwise need), all for an index that runs to hundreds of megabytes on a
// channel the size of conda-forge and that exists only as a transfer optimization: a
// client denied the variant falls back to the plain document, which is served
// rewritten and re-read from upstream. That is what repodata.json.bz2 has answered
// since this handler was written, and the fallback is why it works.
func refusedIndexVariant(filename string) (string, bool) {
	for _, ext := range []string{".bz2", ".zst"} {
		if base := strings.TrimSuffix(filename, ext); base != filename && isIndexDocument(base) {
			return base, true
		}
	}
	// The patch stream drops the ".json" instead of appending: "repodata.jlap".
	if base := strings.TrimSuffix(filename, ".jlap"); base != filename && isIndexDocument(base+".json") {
		return base + ".json", true
	}
	return "", false
}

// proxyIndex fetches one index document from upstream and serves it with its
// package URLs rewritten onto this proxy. It deliberately does not go through
// repoproxy's blob cache: the document is rebuilt whenever the channel changes, and
// a cached copy of it would be served as though it were an immutable package.
func (h *Handler) proxyIndex(c *gin.Context, repo *domain.Repository, repoName, platform, filename string) {
	remoteBase, err := repoproxy.RemoteURL(repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	minAge := repoproxy.MinimumPackageAge(repo)
	if minAge > 0 && filename == "current_repodata.json" {
		// Only the newest build of each package: once that one is too young
		// there is no older build to offer. conda falls back to repodata.json
		// on a 404 (#643; see age_filter.go).
		c.JSON(http.StatusNotFound, gin.H{"error": "use repodata.json: this repository enforces a minimum package age"})
		return
	}

	upstreamURL := remoteBase + "/" + platform + "/" + filename
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, upstreamURL, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	repoproxy.SetUpstreamAuth(req, repo)
	resp, err := repoproxy.ClientFor(repo).Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream fetch: " + err.Error()})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		c.Status(resp.StatusCode)
		_, _ = io.Copy(c.Writer, resp.Body) // best-effort relay of upstream error body; nothing actionable on copy failure
		return
	}

	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "parse upstream " + filename + ": " + err.Error()})
		return
	}

	// Builds younger than the minimum package age are not offered (#643). The
	// download gate's table is taken first, while the urls are upstream's.
	if minAge > 0 {
		if filename == "repodata.json" {
			h.storeAgeTable(repo, remoteBase, platform, ageTableFromDoc(doc, remoteBase, platform))
		}
		logUndatedPackages(repo, platform, filterRepodataByAge(doc, time.Now().Add(-minAge).UnixMilli()))
	}

	// The proxy base is the CHANNEL root, not the subdir: an entry may name a sibling
	// subdir, and rewritePackageURL resolves each one against the subdir it came from.
	localBase := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repoName + "/"
	rewriteCondaURLs(doc, remoteBase, platform, localBase)

	data, _ := json.Marshal(doc)
	c.Data(http.StatusOK, "application/json", data)
}
