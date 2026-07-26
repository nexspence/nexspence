package conan

// Conan v2 revisions API (Conan 2.x clients, and 1.x with revisions enabled):
//
//	PUT  /v2/conans/:name/:ver/:user/:channel/revisions/:rrev/files/:file → upload recipe file
//	GET  /v2/conans/:name/:ver/:user/:channel/revisions/:rrev/files/:file → download recipe file
//	GET  /v2/conans/:name/:ver/:user/:channel/revisions/:rrev/files       → list recipe files
//	GET  /v2/conans/:name/:ver/:user/:channel/latest                      → latest recipe revision
//	GET  /v2/conans/:name/:ver/:user/:channel/revisions                   → list recipe revisions
//
// ...and the same shapes under /revisions/:rrev/packages/:pkgid/... for
// package binaries. Files are stored verbatim under their full
// /v2/conans/... path (the same verbatim-path convention as the v1 /files/
// API), so listings and revision resolution are derived from asset path
// prefixes.

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func (h *Handler) serveV2(c *gin.Context, repoName, p string) {
	m := c.Request.Method
	switch {
	// File upload/download: .../files/<path>
	case m == http.MethodPut && strings.Contains(p, "/files/"):
		h.handleUpload(c, repoName, p)
	case m == http.MethodGet && strings.Contains(p, "/files/"):
		h.handleDownload(c, repoName, p)

	// File listing: .../files
	case m == http.MethodGet && strings.HasSuffix(p, "/files"):
		h.v2ListFiles(c, repoName, p)

	// Latest revision (recipe or package): .../latest
	case m == http.MethodGet && strings.HasSuffix(p, "/latest"):
		h.v2Latest(c, repoName, p)

	// Revision list (recipe or package): .../revisions
	case m == http.MethodGet && strings.HasSuffix(p, "/revisions"):
		h.v2ListRevisions(c, repoName, p)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

// pathsUnder returns stored asset paths (relative to prefix) and their
// timestamps for every asset under the given path prefix. The second return
// is false when listing failed and an error response was already written.
func (h *Handler) pathsUnder(c *gin.Context, repoName, prefix string) (map[string]time.Time, bool) {
	page, err := h.deps.Assets.List(c.Request.Context(), repoName, 1000, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, false
	}
	out := map[string]time.Time{}
	for _, a := range page.Items {
		if strings.HasPrefix(a.Path, prefix) {
			out[strings.TrimPrefix(a.Path, prefix)] = a.LastModified
		}
	}
	return out, true
}

func (h *Handler) v2ListFiles(c *gin.Context, repoName, p string) {
	rel, ok := h.pathsUnder(c, repoName, p+"/")
	if !ok {
		return
	}
	if len(rel) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no files found"})
		return
	}
	files := gin.H{}
	for f := range rel {
		files[f] = gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{"files": files})
}

// revisionTimes reduces relative paths ("<rev>/<file>") under a revisions/
// prefix to revision → newest-file-time.
func revisionTimes(rel map[string]time.Time) map[string]time.Time {
	revs := map[string]time.Time{}
	for f, ts := range rel {
		rev, _, ok := strings.Cut(f, "/")
		if !ok || rev == "" {
			continue
		}
		if cur, seen := revs[rev]; !seen || ts.After(cur) {
			revs[rev] = ts
		}
	}
	return revs
}

func (h *Handler) v2Latest(c *gin.Context, repoName, p string) {
	base := strings.TrimSuffix(p, "/latest")
	rel, ok := h.pathsUnder(c, repoName, base+"/revisions/")
	if !ok {
		return
	}
	var bestRev string
	var bestTime time.Time
	for rev, ts := range revisionTimes(rel) {
		if bestRev == "" || ts.After(bestTime) {
			bestRev, bestTime = rev, ts
		}
	}
	if bestRev == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"revision": bestRev,
		"time":     bestTime.UTC().Format(time.RFC3339),
	})
}

func (h *Handler) v2ListRevisions(c *gin.Context, repoName, p string) {
	rel, ok := h.pathsUnder(c, repoName, p+"/")
	if !ok {
		return
	}
	revs := revisionTimes(rel)
	if len(revs) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	type revEntry struct {
		Revision string `json:"revision"`
		Time     string `json:"time"`
	}
	list := make([]revEntry, 0, len(revs))
	for rev, ts := range revs {
		list = append(list, revEntry{Revision: rev, Time: ts.UTC().Format(time.RFC3339)})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Time > list[j].Time })
	c.JSON(http.StatusOK, gin.H{"revisions": list})
}
