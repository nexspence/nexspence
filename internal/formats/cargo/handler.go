// Package cargo implements the Cargo (Rust) registry protocol.
//
// Cargo Alternate Registry API (RFC 2141):
//
//	GET  /api/v1/crates                      → search (JSON)
//	GET  /api/v1/crates/:name/:version/download → download .crate
//	PUT  /api/v1/crates/new                  → publish
//	DELETE /api/v1/crates/:name/:version/yank → yank
//	PUT  /api/v1/crates/:name/:version/unyank → unyank (not implemented)
//
// Index (sparse, RFC 3130):
//
//	GET  /index/config.json                  → {"dl":"...", "api":"..."}
//	GET  /index/:prefix1/:prefix2/:name      → newline-delimited JSON records
package cargo

import (
	"encoding/json"
	"fmt"
	"io"
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

// Handler serves the Rust Cargo registry protocol.
type Handler struct{ deps formats.Deps }

// New creates a Cargo format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "cargo" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	// Proxy: block mutations (publish/yank), proxy index and crate downloads
	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		// Sparse index config.json is always served locally (points to this server)
		if c.Request.Method == http.MethodGet && p == "/index/config.json" {
			h.serveIndexConfig(c, repoName)
			return
		}
		// Sparse-index entries under /index/ are mutable metadata (they list a
		// crate's versions); /api/v1/crates/.../download .crate files are immutable.
		var maxAge time.Duration
		if strings.HasPrefix(p, "/index/") {
			maxAge = repoproxy.MetadataMaxAge(repo)
		}
		coords := base.Coords{}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/octet-stream", maxAge); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	switch {
	// Sparse index config
	case c.Request.Method == http.MethodGet && p == "/index/config.json":
		h.serveIndexConfig(c, repoName)

	// Sparse index entry: GET /index/:prefix1/:prefix2/:name
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/index/"):
		h.serveIndexEntry(c, repoName, p)

	// Search: GET /api/v1/crates?q=name
	case c.Request.Method == http.MethodGet && p == "/api/v1/crates":
		h.handleSearch(c, repoName)

	// Publish: PUT /api/v1/crates/new
	case c.Request.Method == http.MethodPut && p == "/api/v1/crates/new":
		h.handlePublish(c, repoName)

	// Download: GET /api/v1/crates/:name/:version/download
	case c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/download"):
		h.handleDownload(c, repoName, p)

	// Yank: DELETE /api/v1/crates/:name/:version/yank
	case c.Request.Method == http.MethodDelete && strings.HasSuffix(p, "/yank"):
		h.handleYank(c, repoName, p)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) serveIndexConfig(c *gin.Context, repoName string) {
	baseURL := h.deps.BaseURL + "/repository/" + repoName
	c.JSON(http.StatusOK, gin.H{
		"dl":            baseURL + "/api/v1/crates/{crate}/{version}/download",
		"api":           baseURL,
		"auth-required": false,
	})
}

func (h *Handler) serveIndexEntry(c *gin.Context, repoName, p string) {
	// /index/:p1/:p2/:name  or  /index/:name  (for 1-2 char names)
	parts := strings.Split(strings.TrimPrefix(p, "/index/"), "/")
	if len(parts) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "invalid index path"})
		return
	}
	crateName := parts[len(parts)-1]

	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Name: crateName, Limit: 200,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(page.Items) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	// Sparse index: newline-delimited JSON records
	var sb strings.Builder
	for _, comp := range page.Items {
		asset, _ := h.deps.Assets.GetByPath(c.Request.Context(), repoName,
			"/api/v1/crates/"+crateName+"/"+comp.Version+"/"+crateName+"-"+comp.Version+".crate")
		checksum := ""
		if asset != nil {
			checksum = asset.SHA256
		}
		rec := map[string]any{
			"name":     comp.Name,
			"vers":     comp.Version,
			"deps":     []any{},
			"cksum":    checksum,
			"features": map[string]any{},
			"yanked":   false,
		}
		b, _ := json.Marshal(rec)
		sb.Write(b)
		sb.WriteByte('\n')
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(sb.String()))
}

func (h *Handler) handleSearch(c *gin.Context, repoName string) {
	q := c.Query("q")
	perPage := 10
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Name: q, Limit: perPage,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	crates := make([]gin.H, 0, len(page.Items))
	for _, comp := range page.Items {
		crates = append(crates, gin.H{
			"name":           comp.Name,
			"newest_version": comp.Version,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"crates": crates,
		"meta":   gin.H{"total": len(crates)},
	})
}

func (h *Handler) handlePublish(c *gin.Context, repoName string) {
	// Cargo publish wire format:
	// 4 bytes LE = length of JSON metadata
	// N bytes JSON
	// 4 bytes LE = length of .crate tarball
	// M bytes .crate
	var metaLen uint32
	if err := readU32LE(c.Request.Body, &metaLen); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid publish format"})
		return
	}
	metaBytes := make([]byte, metaLen)
	if _, err := io.ReadFull(c.Request.Body, metaBytes); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot read metadata"})
		return
	}
	var meta struct {
		Name    string `json:"name"`
		Version string `json:"vers"`
	}
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid metadata JSON"})
		return
	}

	var crateLen uint32
	if err := readU32LE(c.Request.Body, &crateLen); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot read crate length"})
		return
	}

	name := strings.ToLower(meta.Name)
	version := meta.Version
	filename := name + "-" + version + ".crate"
	filePath := "/api/v1/crates/" + name + "/" + version + "/" + filename

	coords := base.Coords{Name: name, Version: version}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, filePath, "application/x-tar", coords,
		io.LimitReader(c.Request.Body, int64(crateLen)), int64(crateLen)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"warnings": gin.H{"invalid_categories": []string{}, "invalid_badges": []string{}, "other": []string{}}})
}

func (h *Handler) handleDownload(c *gin.Context, repoName, p string) {
	// /api/v1/crates/:name/:version/download
	rest := strings.TrimSuffix(strings.TrimPrefix(p, "/api/v1/crates/"), "/download")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}
	name, version := parts[0], parts[1]
	filename := name + "-" + version + ".crate"
	filePath := "/api/v1/crates/" + name + "/" + version + "/" + filename

	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = rc.Close() }()
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.DataFromReader(http.StatusOK, asset.SizeBytes, "application/x-tar", rc, nil)
}

func (h *Handler) handleYank(c *gin.Context, repoName, p string) {
	// DELETE /api/v1/crates/:name/:version/yank
	rest := strings.TrimSuffix(strings.TrimPrefix(p, "/api/v1/crates/"), "/yank")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}
	name, version := parts[0], parts[1]
	filePath := "/api/v1/crates/" + name + "/" + version + "/" + name + "-" + version + ".crate"
	if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, filePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}

// readU32LE reads a 4-byte little-endian uint32 from r.
func readU32LE(r io.Reader, v *uint32) error {
	b := make([]byte, 4)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	*v = uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	return nil
}
