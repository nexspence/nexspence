// Package npm implements the npm registry protocol.
//
// GET  /-/ping               → {"ok":true}
// GET  /:name                → package metadata JSON (built from DB)
// GET  /@scope/:name         → scoped package metadata
// GET  /:name/-/:file.tgz    → tarball download
// PUT  /:name                → publish (tarball embedded as base64 in JSON body)
// DELETE /:name              → deprecate / delete
package npm

import (
	"encoding/base64"
	"encoding/json"
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

// Handler serves the npm registry protocol.
type Handler struct{ deps formats.Deps }

// New creates an npm format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "npm" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	// Health ping
	if p == "/-/ping" {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}

	switch c.Request.Method {
	case http.MethodGet, http.MethodHead:
		// Tarball: contains "/-/" in path
		if strings.Contains(p, "/-/") {
			h.serveTarball(c, repoName, p)
			return
		}
		h.serveMetadata(c, repoName, p)

	case http.MethodPut:
		repo, err := h.deps.Repos.Get(c.Request.Context(), repoName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		h.handlePublish(c, repoName, p)

	case http.MethodDelete:
		repo, err := h.deps.Repos.Get(c.Request.Context(), repoName)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, p); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) serveTarball(c *gin.Context, repoName, filePath string) {
	repo, err := h.deps.Repos.Get(c.Request.Context(), repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if repo != nil && repo.Type == domain.TypeProxy {
		baseName := path.Base(filePath)
		ver := ""
		if i := strings.LastIndex(baseName, "-"); i > 0 {
			if ext := path.Ext(baseName); ext == ".tgz" {
				ver = strings.TrimSuffix(baseName[i+1:], ext)
			}
		}
		pkg := strings.TrimPrefix(strings.Split(filePath, "/-/")[0], "/")
		coords := base.Coords{Name: pkg, Version: ver}
		if coords.Version == "" {
			coords.Version = "1"
		}
		if err := repoproxy.ServeGET(c, h.deps, repo, filePath, "", coords, "application/octet-stream"); err != nil {
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
	if asset.SHA1 != "" {
		c.Header("X-Checksum-SHA1", asset.SHA1)
	}
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
		c.Status(http.StatusOK)
		return
	}
	c.DataFromReader(http.StatusOK, asset.SizeBytes, "application/octet-stream", rc, nil)
}

func (h *Handler) serveMetadata(c *gin.Context, repoName, pkgPath string) {
	pkgName := strings.TrimPrefix(pkgPath, "/")
	ctx := c.Request.Context()

	repo, err := h.deps.Repos.Get(ctx, repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if repo != nil && repo.Type == domain.TypeProxy {
		trim := strings.Trim(strings.TrimPrefix(pkgPath, "/"), "/")
		up := "/" + repoproxy.NPMMetadataPath(trim)
		coords := base.Coords{Name: pkgName, Version: "metadata"}
		if err := repoproxy.ServeGET(c, h.deps, repo, pkgPath, up, coords, "application/json"); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	page, err := h.deps.Components.Search(ctx, domain.SearchParams{
		Repository: repoName, Name: pkgName, Limit: 200,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(page.Items) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}

	versions := map[string]any{}
	latest := ""
	for _, comp := range page.Items {
		baseName := path.Base(pkgName)
		tarball := h.deps.BaseURL + "/repository/" + repoName +
			"/" + pkgName + "/-/" + baseName + "-" + comp.Version + ".tgz"
		versions[comp.Version] = map[string]any{
			"name":    pkgName,
			"version": comp.Version,
			"dist":    map[string]any{"tarball": tarball},
		}
		latest = comp.Version
	}
	c.JSON(http.StatusOK, gin.H{
		"name":      pkgName,
		"versions":  versions,
		"dist-tags": gin.H{"latest": latest},
	})
}

func (h *Handler) handlePublish(c *gin.Context, repoName, pkgPath string) {
	pkgName := strings.TrimPrefix(pkgPath, "/")

	var doc map[string]json.RawMessage
	if err := c.ShouldBindJSON(&doc); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Parse dist-tags for version
	version := ""
	if raw, ok := doc["dist-tags"]; ok {
		var tags map[string]string
		_ = json.Unmarshal(raw, &tags)
		version = tags["latest"]
	}
	if version == "" {
		if raw, ok := doc["versions"]; ok {
			var vers map[string]json.RawMessage
			_ = json.Unmarshal(raw, &vers)
			for v := range vers {
				version = v
				break
			}
		}
	}
	if version == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot determine version"})
		return
	}

	// _attachments: { "pkg-ver.tgz": { "data": "<base64>", "length": N } }
	attachmentsRaw, ok := doc["_attachments"]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing _attachments"})
		return
	}
	var attachments map[string]struct {
		Data        string `json:"data"`
		ContentType string `json:"content_type"`
		Length      int64  `json:"length"`
	}
	if err := json.Unmarshal(attachmentsRaw, &attachments); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid _attachments"})
		return
	}

	// Determine scope from scoped package name (@scope/name)
	scope := ""
	if strings.HasPrefix(pkgName, "@") {
		parts := strings.SplitN(pkgName, "/", 2)
		if len(parts) == 2 {
			scope = parts[0]
		}
	}
	_ = scope

	for filename, att := range attachments {
		data, err := base64.StdEncoding.DecodeString(att.Data)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid base64 in attachment"})
			return
		}
		ct := att.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		filePath := "/" + pkgName + "/-/" + filename
		coords := base.Coords{Name: pkgName, Version: version}
		if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
			repoName, filePath, ct, coords,
			strings.NewReader(string(data)), int64(len(data))); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true})
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
