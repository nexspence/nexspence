// Package conda implements the Conda channel repository protocol.
//
// Conda channel layout:
//
//	GET /repository/<repo>/<platform>/repodata.json      → channel index
//	GET /repository/<repo>/<platform>/repodata.json.bz2  → bz2-compressed index (returns 404)
//	GET /repository/<repo>/<platform>/<filename>          → download package
//	PUT /repository/<repo>/<platform>/<filename>          → upload package
//	DELETE /repository/<repo>/<platform>/<filename>       → delete package
//
// Supported platforms: linux-64, linux-aarch64, osx-64, osx-arm64, win-64, noarch, etc.
// Supported file types: .conda (zip+zstd), .tar.bz2 (legacy)
package conda

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// Handler serves the Conda channel repository protocol.
type Handler struct{ deps formats.Deps }

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
	var (
		meta *PkgMeta
		body io.Reader = c.Request.Body
		size           = c.Request.ContentLength
	)
	if strings.HasSuffix(filename, ".tar.bz2") {
		// Coords come from in-archive metadata, which must be parsed before
		// StoreArtifact — spool to a temp file so memory stays O(1).
		tmp, err := os.CreateTemp("", "conda-upload-*")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "spool upload: " + err.Error()})
			return
		}
		defer func() {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}()
		n, err := io.Copy(tmp, c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "read body: " + err.Error()})
			return
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if m, err := ParseMeta(filename, tmp); err == nil && m != nil {
			meta = m
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		body, size = tmp, n
	}
	if meta == nil {
		meta = metaFromFilename(filename)
	}

	filePath := "/" + platform + "/" + filename
	ct := "application/x-tar"
	if strings.HasSuffix(filename, ".conda") {
		ct = "application/zip"
	}

	coords := base.Coords{
		Group:   platform,
		Name:    meta.Name,
		Version: meta.Version,
	}

	res, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, filePath, ct, coords, body, size)
	if err != nil {
		c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
		return
	}

	// Persist build/depends in component Extra — best-effort
	if res != nil && res.Asset != nil && res.Asset.ComponentID != "" {
		extra := map[string]any{
			"build":        meta.Build,
			"build_number": meta.BuildNumber,
			"depends":      meta.Depends,
		}
		_ = h.deps.Components.UpdateExtra(c.Request.Context(), res.Asset.ComponentID, extra)
	}

	c.JSON(http.StatusCreated, gin.H{"saved": true})
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

	if c.Request.Method == http.MethodGet && filename == "repodata.json" {
		h.proxyRepodata(c, repo, repoName, platform)
		return
	}
	if filename == "repodata.json.bz2" {
		c.JSON(http.StatusNotFound, gin.H{"error": "use repodata.json"})
		return
	}

	// Package binary: cache via repoproxy. Conda packages are immutable
	// (repodata.json — the mutable index — is handled by proxyRepodata above).
	coords := base.Coords{Name: filename, Group: platform}
	if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/x-tar", 0); err != nil {
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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		c.Status(resp.StatusCode)
		_, _ = io.Copy(c.Writer, resp.Body) // best-effort relay of upstream error body; nothing actionable on copy failure
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

// rewriteCondaURLs rewrites "url" and "urls" fields inside "packages" and "packages.conda"
// so downloads route through this proxy.
func rewriteCondaURLs(doc map[string]any, localBase string) {
	for _, key := range []string{"packages", "packages.conda"} {
		pkgs, _ := doc[key].(map[string]any)
		for filename, v := range pkgs {
			entry, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if u, ok := entry["url"].(string); ok {
				entry["url"] = localBase + path.Base(u)
			}
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
