// Package pypi implements the PyPI (Python Package Index) repository protocol.
//
// GET  /simple/                  → HTML index of all packages
// GET  /simple/:package/         → HTML links for a package's files
// POST /                         → twine upload (multipart/form-data)
// GET  /packages/:package/:file  → download wheel/sdist
package pypi

import (
	"fmt"
	"html"
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
func (h *Handler) Name() string      { return "pypi" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")
	ctx := c.Request.Context()
	repo, err := h.deps.Repos.Get(ctx, repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	switch {
	// Upload: POST /
	case c.Request.Method == http.MethodPost && (p == "/" || p == ""):
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		h.handleUpload(c, repoName)

	// Package simple index: GET /simple/
	case c.Request.Method == http.MethodGet && p == "/simple/":
		if repo != nil && repo.Type == domain.TypeProxy {
			coords := base.Coords{Name: "_simple", Version: "index"}
			if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "text/html; charset=utf-8"); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			}
			return
		}
		h.serveSimpleIndex(c, repoName)

	// Per-package index: GET /simple/:name/
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/simple/"):
		pkgName := strings.TrimSuffix(strings.TrimPrefix(p, "/simple/"), "/")
		if repo != nil && repo.Type == domain.TypeProxy {
			normalized := normalizePackageName(pkgName)
			coords := base.Coords{Name: normalized, Version: "simple-page"}
			if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "text/html; charset=utf-8"); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			}
			return
		}
		h.servePackageIndex(c, repoName, pkgName)

	// Download: GET /packages/... or any other path
	case c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead:
		h.serveFile(c, repoName, p)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleUpload(c *gin.Context, repoName string) {
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	action := c.Request.FormValue(":action")
	if action != "file_upload" && action != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported action: " + action})
		return
	}

	f, fh, err := c.Request.FormFile("content")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing content file"})
		return
	}
	defer f.Close()

	pkgName := normalizePackageName(c.Request.FormValue("name"))
	version := c.Request.FormValue("version")
	if pkgName == "" || version == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and version are required"})
		return
	}

	filename := fh.Filename
	filePath := "/packages/" + pkgName + "/" + filename
	ct := fh.Header.Get("Content-Type")
	if ct == "" {
		ct = pypiContentType(filename)
	}

	coords := base.Coords{Name: pkgName, Version: version}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, filePath, ct, coords, f, fh.Size); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusOK)
}

func (h *Handler) serveSimpleIndex(c *gin.Context, repoName string) {
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Limit: 500,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Deduplicate package names
	seen := map[string]struct{}{}
	var names []string
	for _, comp := range page.Items {
		n := normalizePackageName(comp.Name)
		if _, ok := seen[n]; !ok {
			seen[n] = struct{}{}
			names = append(names, n)
		}
	}

	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html><head><title>Simple Index</title></head><body><h1>Simple Index</h1>\n")
	for _, n := range names {
		sb.WriteString(fmt.Sprintf(`<a href="/repository/%s/simple/%s/">%s</a><br/>`,
			html.EscapeString(repoName), html.EscapeString(n), html.EscapeString(n)))
	}
	sb.WriteString("</body></html>")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(sb.String()))
}

func (h *Handler) servePackageIndex(c *gin.Context, repoName, pkgName string) {
	normalized := normalizePackageName(pkgName)
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Name: normalized, Limit: 200,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<!DOCTYPE html><html><head><title>Links for %s</title></head><body><h1>Links for %s</h1>\n",
		html.EscapeString(pkgName), html.EscapeString(pkgName)))

	for _, comp := range page.Items {
		// Get assets for this component
		assetPage, err := h.deps.Assets.List(c.Request.Context(), repoName, 100, 0)
		if err != nil {
			continue
		}
		for _, a := range assetPage.Items {
			if a.ComponentID != comp.ID {
				continue
			}
			filename := path.Base(a.Path)
			href := h.deps.BaseURL + "/repository/" + repoName + a.Path
			sha := ""
			if a.SHA256 != "" {
				sha = "#sha256=" + a.SHA256
			}
			sb.WriteString(fmt.Sprintf(`<a href="%s%s" data-requires-python="">%s</a><br/>`,
				html.EscapeString(href), sha, html.EscapeString(filename)))
		}
	}
	sb.WriteString("</body></html>")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(sb.String()))
}

func (h *Handler) serveFile(c *gin.Context, repoName, filePath string) {
	repo, err := h.deps.Repos.Get(c.Request.Context(), repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if repo != nil && repo.Type == domain.TypeProxy {
		pkgGuess := path.Base(path.Dir(filePath))
		coords := base.Coords{Name: normalizePackageName(pkgGuess), Version: "wheel"}
		ct := pypiContentType(path.Base(filePath))
		if err := repoproxy.ServeGET(c, h.deps, repo, filePath, "", coords, ct); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
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

func normalizePackageName(name string) string {
	// PEP 503: normalize to lowercase, replace [-_.] with -
	name = strings.ToLower(name)
	name = strings.NewReplacer("_", "-", ".", "-").Replace(name)
	return name
}

func pypiContentType(filename string) string {
	switch {
	case strings.HasSuffix(filename, ".whl"):
		return "application/zip"
	case strings.HasSuffix(filename, ".tar.gz"), strings.HasSuffix(filename, ".tgz"):
		return "application/gzip"
	case strings.HasSuffix(filename, ".zip"):
		return "application/zip"
	default:
		return "application/octet-stream"
	}
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
