// Package gomod implements the GOPROXY protocol for Go module repositories.
//
// Endpoints (all under /repository/:repoName/):
//
//	GET /<module>/@v/list               → newline-separated version list
//	GET /<module>/@v/<version>.info     → {"Version":"v1.0.0","Time":"2024-01-01T00:00:00Z"}
//	GET /<module>/@v/<version>.mod      → go.mod content
//	GET /<module>/@v/<version>.zip      → module source zip
//	GET /<module>/@latest               → latest version info JSON
//	PUT /<module>/@v/<version>.zip      → upload zip (Nexspence extension)
//	PUT /<module>/@v/<version>.mod      → upload go.mod (Nexspence extension)
package gomod

import (
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// Handler serves the Go module proxy (GOPROXY) protocol.
type Handler struct{ deps formats.Deps }

// New creates a Go module format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "go" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	switch c.Request.Method {
	case http.MethodGet, http.MethodHead:
		h.handleGet(c, repoName, p)
	case http.MethodPut:
		repo, err := h.deps.Repos.Get(c.Request.Context(), repoName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		h.handlePut(c, repoName, p)
	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleGet(c *gin.Context, repoName, p string) {
	repo, err := h.deps.Repos.Get(c.Request.Context(), repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if repo != nil && repo.Type == domain.TypeProxy {
		// @latest and @v/list resolve to a moving target (newest version / the full
		// version list); @v/<ver>.{info,mod,zip} are immutable per version.
		var maxAge time.Duration
		if strings.HasSuffix(p, "/@latest") || strings.HasSuffix(p, "/@v/list") {
			maxAge = repoproxy.MetadataMaxAge(repo)
		}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", goProxyCoords(p), goContentType(p), maxAge); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	// GET /<module>/@latest
	if strings.HasSuffix(p, "/@latest") {
		modulePath := strings.TrimSuffix(strings.TrimPrefix(p, "/"), "/@latest")
		h.serveLatest(c, repoName, modulePath)
		return
	}

	// Find /@v/ split
	atv := strings.Index(p, "/@v/")
	if atv < 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "invalid Go module path"})
		return
	}
	modulePath := strings.TrimPrefix(p[:atv], "/")
	rest := p[atv+4:] // after /@v/

	switch {
	case rest == "list":
		h.serveList(c, repoName, modulePath)
	case strings.HasSuffix(rest, ".info"):
		version := strings.TrimSuffix(rest, ".info")
		h.serveInfo(c, repoName, modulePath, version)
	case strings.HasSuffix(rest, ".mod"):
		version := strings.TrimSuffix(rest, ".mod")
		h.serveFile(c, repoName, p, modulePath, version, "go.mod")
	case strings.HasSuffix(rest, ".zip"):
		version := strings.TrimSuffix(rest, ".zip")
		h.serveFile(c, repoName, p, modulePath, version, "zip")
	default:
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown endpoint"})
	}
}

func (h *Handler) serveList(c *gin.Context, repoName, modulePath string) {
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName,
		Group:      modulePath,
		Limit:      200,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var sb strings.Builder
	for _, comp := range page.Items {
		sb.WriteString(comp.Version + "\n")
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(sb.String()))
}

func (h *Handler) serveInfo(c *gin.Context, repoName, modulePath, version string) {
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName,
		Group:      modulePath,
		Version:    version,
		Limit:      1,
	})
	if err != nil || len(page.Items) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "version not found"})
		return
	}
	comp := page.Items[0]
	t := comp.CreatedAt
	c.JSON(http.StatusOK, gin.H{
		"Version": version,
		"Time":    t.UTC().Format(time.RFC3339),
	})
}

func (h *Handler) serveFile(c *gin.Context, repoName, filePath, _, _, kind string) {
	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = rc.Close() }()
	ct := "application/octet-stream"
	if kind == "go.mod" {
		ct = "text/plain; charset=utf-8"
	}
	c.DataFromReader(http.StatusOK, asset.SizeBytes, ct, rc, nil)
}

func (h *Handler) serveLatest(c *gin.Context, repoName, modulePath string) {
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName,
		Group:      modulePath,
		Limit:      200,
	})
	if err != nil || len(page.Items) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no versions found"})
		return
	}
	latest := page.Items[len(page.Items)-1]
	c.JSON(http.StatusOK, gin.H{
		"Version": latest.Version,
		"Time":    latest.CreatedAt.UTC().Format(time.RFC3339),
	})
}

func (h *Handler) handlePut(c *gin.Context, repoName, p string) {
	ct := goContentType(p)
	atv := strings.Index(p, "/@v/")
	coords := base.Coords{}
	if atv >= 0 {
		modulePath := strings.TrimPrefix(p[:atv], "/")
		rest := p[atv+4:]
		coords.Group = modulePath
		coords.Name = path.Base(modulePath)
		for _, suf := range []string{".zip", ".mod", ".info"} {
			if strings.HasSuffix(rest, suf) {
				coords.Version = strings.TrimSuffix(rest, suf)
				break
			}
		}
	}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, p, ct, coords, c.Request.Body, c.Request.ContentLength); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true})
}

func goContentType(p string) string {
	switch {
	case strings.HasSuffix(p, ".zip"):
		return "application/zip"
	case strings.HasSuffix(p, ".mod"):
		return "text/plain; charset=utf-8"
	case strings.HasSuffix(p, ".info"):
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}

// goProxyCoords builds stable component coordinates for a GOPROXY path under a proxy repo.
func goProxyCoords(pathStr string) base.Coords {
	pathStr = strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(pathStr, "/")), "/")
	if strings.HasSuffix(pathStr, "/@latest") {
		mod := strings.TrimSuffix(pathStr, "/@latest")
		return base.Coords{Group: mod, Name: "latest", Version: "0"}
	}
	i := strings.Index(pathStr, "/@v/")
	if i < 0 {
		return base.Coords{Group: pathStr, Name: "unknown", Version: "0"}
	}
	mod := pathStr[:i]
	rest := pathStr[i+len("/@v/"):]
	if rest == "list" {
		return base.Coords{Group: mod, Name: "list", Version: "0"}
	}
	for _, suf := range []string{".info", ".mod", ".zip"} {
		if strings.HasSuffix(rest, suf) {
			ver := strings.TrimSuffix(rest, suf)
			return base.Coords{Group: mod, Name: strings.TrimPrefix(suf, "."), Version: ver}
		}
	}
	return base.Coords{Group: mod, Name: rest, Version: "0"}
}
