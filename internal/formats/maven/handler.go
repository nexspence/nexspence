// Package maven implements the Maven 2/3 repository format.
//
// Maven path: /<groupId/as/path>/<artifactId>/<version>/<file>
// e.g.  /org/springframework/spring-core/5.3.0/spring-core-5.3.0.jar
package maven

import (
	"bytes"
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
func (h *Handler) Name() string      { return "maven2" }

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
			coords := parsePath(filePath)
			if isChecksum(filePath) {
				main := strings.TrimSuffix(filePath, path.Ext(filePath))
				coords = parsePath(main)
			}
			ct := mavenContentType(filePath)
			if err := repoproxy.ServeGET(c, h.deps, repo, filePath, "", coords, ct); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			}
			return
		}
		// Serve .sha1/.md5/.sha256 checksums from DB without separate blob
		if isChecksum(filePath) {
			h.serveChecksum(c, repoName, filePath)
			return
		}
		rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, filePath)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		defer rc.Close()
		applyChecksumHeaders(c, asset)
		if c.Request.Method == http.MethodHead {
			c.Header("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
			c.Header("Content-Type", asset.ContentType)
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
		// Silently accept checksum sidecars — we compute them ourselves
		if isChecksum(filePath) {
			c.Status(http.StatusCreated)
			return
		}
		ct := mavenContentType(filePath)
		coords := parsePath(filePath)
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

func (h *Handler) serveChecksum(c *gin.Context, repoName, checksumPath string) {
	ext := path.Ext(checksumPath)
	mainPath := strings.TrimSuffix(checksumPath, ext)
	asset, err := h.deps.Assets.GetByPath(c.Request.Context(), repoName, mainPath)
	if err != nil || asset == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "checksum not available"})
		return
	}
	var v string
	switch ext {
	case ".sha1":
		v = asset.SHA1
	case ".md5":
		v = asset.MD5
	case ".sha256":
		v = asset.SHA256
	}
	if v == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "checksum not stored"})
		return
	}
	data := []byte(v)
	c.DataFromReader(http.StatusOK, int64(len(data)), "text/plain", bytes.NewReader(data), nil)
}

func parsePath(p string) base.Coords {
	p = strings.TrimPrefix(p, "/")
	parts := strings.Split(p, "/")
	if len(parts) < 3 {
		return base.Coords{Name: p}
	}
	return base.Coords{
		Group:   strings.Join(parts[:len(parts)-3], "."),
		Name:    safeAt(parts, len(parts)-3),
		Version: safeAt(parts, len(parts)-2),
	}
}

func isChecksum(p string) bool {
	return strings.HasSuffix(p, ".sha1") || strings.HasSuffix(p, ".md5") || strings.HasSuffix(p, ".sha256")
}

func mavenContentType(p string) string {
	switch path.Ext(p) {
	case ".jar", ".war", ".ear":
		return "application/java-archive"
	case ".pom", ".xml":
		return "application/xml"
	case ".zip":
		return "application/zip"
	default:
		return "application/octet-stream"
	}
}

func applyChecksumHeaders(c *gin.Context, a *domain.Asset) {
	if a.SHA256 != "" {
		c.Header("X-Checksum-SHA256", a.SHA256)
		c.Header("ETag", `"`+a.SHA256+`"`)
	}
	if a.SHA1 != "" {
		c.Header("X-Checksum-SHA1", a.SHA1)
	}
	if a.MD5 != "" {
		c.Header("X-Checksum-MD5", a.MD5)
	}
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}

func safeAt(s []string, i int) string {
	if i >= 0 && i < len(s) {
		return s[i]
	}
	return ""
}
