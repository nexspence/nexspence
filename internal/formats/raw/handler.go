// Package raw implements the "raw" format handler.
// Raw repositories store arbitrary files at arbitrary paths.
package raw

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
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
			//
			// An upstream directory index links its files with absolute paths
			// rooted at the upstream's own base path ("/dist/…"). Served
			// verbatim they escape "/repository/<repo>/" and resolve against
			// the local origin — the repository segment is lost and the
			// upstream prefix leaks, so the links 404 and nothing can be
			// downloaded (#774). An HTML listing is re-rooted at this
			// repository's URL; every other body is served as it came.
			opts := repoproxy.ServeOptions{}
			if rewrite := h.listingRewrite(c, repo, filePath); rewrite != nil {
				opts.Rewrite = rewrite
				opts.RewriteContentType = "text/html"
			}
			if err := repoproxy.ServeGETWithOptions(c, h.deps, repo, filePath, "", coords, ct, 0, opts); err != nil {
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
			c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
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

// listingRewrite builds the rewrite that re-roots the links of a proxied
// upstream directory index at this repository, or nil when the upstream
// cannot be described well enough to do it (a malformed remote URL), in
// which case the upstream's bytes are served as they came.
func (h *Handler) listingRewrite(c *gin.Context, repo *domain.Repository, filePath string) func([]byte) []byte {
	remote, err := repoproxy.RemoteURL(repo)
	if err != nil {
		return nil
	}
	upstream, err := url.Parse(remote)
	if err != nil || upstream.Host == "" {
		return nil
	}
	// Served through a group, the links stay on the group — as the group's
	// own service index does.
	served := repo.Name
	if g := c.GetString(formats.GroupRepoKey); g != "" {
		served = g
	}
	rewrite := ListingRewrite{
		// The links are re-rooted at a ROOT-RELATIVE path: the browser
		// resolves them against the origin it reached us on. An absolute
		// URL would have to name a host, and the only host this server
		// knows is the one in http.base_url — which ships as
		// http://localhost:8081 and, on a default install, names a host
		// that is not the one the user is browsing: the link would carry
		// the user off the site they opened (#774).
		LocalPath:        "/repository/" + served,
		UpstreamHost:     upstream.Host,
		UpstreamBasePath: upstream.Path,
		RequestPath:      filePath,
	}
	return rewrite.Apply
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
