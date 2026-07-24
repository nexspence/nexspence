// Package raw implements the "raw" format handler.
// Raw repositories store arbitrary files at arbitrary paths.
package raw

import (
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// Handler serves the raw file store protocol (arbitrary paths).
type Handler struct{ deps formats.Deps }

// New creates a raw format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "raw" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	repoName := c.Param("repoName")
	filePath := normPath(c.Param("path"))

	switch c.Request.Method {
	case http.MethodGet, http.MethodHead:
		repo, err := h.deps.Repos.Get(c.Request.Context(), repoName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if repo != nil && repo.Type == domain.TypeProxy {
			coords := base.Coords{Group: path.Dir(filePath), Name: path.Base(filePath)}
			ct := mime.TypeByExtension(path.Ext(filePath))
			if ct == "" {
				ct = "application/octet-stream"
			}
			// Raw repositories have no metadata/index concept — treat all paths as
			// immutable cached content (maxAge 0), preserving prior behavior.
			if err := repoproxy.ServeGET(c, h.deps, repo, filePath, "", coords, ct, 0); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			}
			return
		}
		rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, filePath)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		defer func() { _ = rc.Close() }()
		applyChecksumHeaders(c, asset)
		if c.Request.Method == http.MethodHead {
			c.Header("Content-Type", asset.ContentType)
			c.Header("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
			c.Status(http.StatusOK)
			return
		}
		c.DataFromReader(http.StatusOK, asset.SizeBytes, asset.ContentType, rc, nil)

	case http.MethodPut, http.MethodPost:
		repo, err := h.deps.Repos.Get(c.Request.Context(), repoName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		ct := c.GetHeader("Content-Type")
		if ct == "" {
			ct = mime.TypeByExtension(path.Ext(filePath))
		}
		if ct == "" {
			ct = "application/octet-stream"
		}
		coords := base.Coords{Group: path.Dir(filePath), Name: path.Base(filePath)}
		if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
			repoName, filePath, ct, coords,
			c.Request.Body, c.Request.ContentLength); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusCreated)

	case http.MethodDelete:
		repo, err := h.deps.Repos.Get(c.Request.Context(), repoName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, filePath); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func applyChecksumHeaders(c *gin.Context, a *domain.Asset) {
	if a.SHA256 != "" {
		c.Header("X-Checksum-SHA256", a.SHA256)
	}
	if a.SHA1 != "" {
		c.Header("X-Checksum-SHA1", a.SHA1)
	}
	if a.MD5 != "" {
		c.Header("X-Checksum-MD5", a.MD5)
	}
	if a.SHA256 != "" {
		c.Header("ETag", `"`+a.SHA256+`"`)
	}
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
