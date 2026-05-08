// Package conda implements the Conda channel repository protocol.
//
// Conda channel layout:
//   GET /repository/<repo>/<platform>/repodata.json      → channel index
//   GET /repository/<repo>/<platform>/repodata.json.bz2  → bz2-compressed index (returns 404)
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
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

type Handler struct{ deps formats.Deps }

func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }
func (h *Handler) Name() string      { return "conda" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		// proxy implementation comes in Task 4
		c.JSON(http.StatusNotImplemented, gin.H{"error": "conda proxy not yet implemented"})
		return
	}

	platform, filename, ok := splitPlatformFile(p)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path must be /<platform>/<file>"})
		return
	}

	switch {
	case c.Request.Method == http.MethodGet && filename == "repodata.json":
		// index generation — Task 2
		c.JSON(http.StatusNotImplemented, gin.H{"error": "not yet implemented"})
	case c.Request.Method == http.MethodGet && filename == "repodata.json.bz2":
		c.JSON(http.StatusNotFound, gin.H{"error": "repodata.json.bz2 not supported; use repodata.json"})
	case c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead:
		// download — Task 3
		_ = platform
		c.JSON(http.StatusNotImplemented, gin.H{"error": "not yet implemented"})
	case c.Request.Method == http.MethodPut:
		// upload — Task 3
		c.JSON(http.StatusNotImplemented, gin.H{"error": "not yet implemented"})
	case c.Request.Method == http.MethodDelete:
		// delete — Task 3
		c.JSON(http.StatusNotImplemented, gin.H{"error": "not yet implemented"})
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
