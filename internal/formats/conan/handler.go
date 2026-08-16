// Package conan implements the Conan C/C++ package manager repository protocol.
//
// Conan v1 REST API (under /repository/:repoName/):
//
//	GET  /ping                                    → {"ok": true}
//	GET  /v1/ping                                 → {"ok": true}
//	GET  /v1/conans/:name/:version/:user/:channel/search → search matching refs
//	GET  /v1/conans/:name/:version/:user/:channel  → recipe manifest
//	GET  /v1/conans/:name/:version/:user/:channel/download_urls → download URLs map
//	POST /v1/conans/:name/:version/:user/:channel/upload_urls   → request upload URLs
//	PUT  /files/:name/:version/:user/:channel/:revision/export/:file → upload recipe file
//	PUT  /files/:name/:version/:user/:channel/:revision/package/:pkgid/:prevision/:file → upload package file
//	GET  /files/:name/:version/:user/:channel/:revision/export/:file → download recipe file
//	GET  /files/:name/:version/:user/:channel/:revision/package/:pkgid/:prevision/:file → download package file
//
// The Conan v2 revisions API (Conan 2.x clients) is served under
// /v2/conans/... — see v2.go for the route list — and the credential
// handshake those clients perform before uploading under /v2/users/... — see
// users.go.
package conan

import (
	"fmt"
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

// Handler serves the Conan C/C++ package registry protocol.
type Handler struct{ deps formats.Deps }

// New creates a Conan format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "conan" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	// Proxy: block uploads, proxy file downloads and metadata reads
	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		// Ping is always local
		if p == "/ping" || p == "/v1/ping" {
			servePing(c)
			return
		}
		// So is the credential handshake — it authenticates against this
		// server, not against the upstream.
		if isUsersPath(p) {
			h.serveUsers(c, p)
			return
		}
		// Conan revision files are content-addressed (immutable); the "/latest"
		// endpoints resolve to a moving recipe/package revision, so revalidate those.
		var maxAge time.Duration
		if strings.HasSuffix(p, "/latest") {
			maxAge = repoproxy.MetadataMaxAge(repo)
		}
		coords := base.Coords{}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, "application/octet-stream", maxAge); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	switch {
	// Ping
	case (p == "/ping" || p == "/v1/ping") && c.Request.Method == http.MethodGet:
		servePing(c)

	// Upload recipe/package file: PUT /files/...
	case c.Request.Method == http.MethodPut && strings.HasPrefix(p, "/files/"):
		h.handleUpload(c, repoName, p)

	// Download recipe/package file: GET /files/...
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/files/"):
		h.handleDownload(c, repoName, p)

	// Upload URLs (Conan v1 upload handshake): POST /v1/conans/.../upload_urls
	case c.Request.Method == http.MethodPost && strings.HasSuffix(p, "/upload_urls"):
		h.handleUploadURLs(c, repoName, p)

	// Download URLs: GET /v1/conans/.../download_urls
	case c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/download_urls"):
		h.handleDownloadURLs(c, repoName, p)

	// Recipe manifest: GET /v1/conans/:name/:ver/:user/:channel
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/v1/conans/"):
		h.handleManifest(c, repoName, p)

	// Conan v2 credential handshake (Conan 2.x clients): /v2/users/... (#244)
	case isUsersPath(p):
		h.serveUsers(c, p)

	// Conan v2 revisions API (Conan 2.x clients): /v2/conans/... (#95)
	case strings.HasPrefix(p, "/v2/conans/"):
		h.serveV2(c, repoName, p)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

// servePing answers the Conan ping and advertises server capabilities.
// Conan 2.x (and 1.x with revisions enabled) requires the "revisions"
// capability before it will talk v2 endpoints to us.
func servePing(c *gin.Context) {
	c.Header("X-Conan-Server-Capabilities", "revisions")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// parseRef extracts (name, version, user, channel) from a /v1/conans/:name/:ver/:user/:channel[/...] path.
func parseRef(p string) (name, version, user, channel string, ok bool) {
	rest := strings.TrimPrefix(p, "/v1/conans/")
	parts := strings.SplitN(rest, "/", 5)
	if len(parts) < 4 {
		return
	}
	return parts[0], parts[1], parts[2], parts[3], true
}

// fileStorePath returns the canonical storage path for a /files/... upload/download path.
// We store it as-is under the files/ prefix.
func fileStorePath(p string) string {
	return p // already normalized
}

// coordsFromRef builds base.Coords from Conan ref components.
func coordsFromRef(name, version, user, channel string) base.Coords {
	return base.Coords{
		Group:   user + "/" + channel,
		Name:    name,
		Version: version,
	}
}

// uploadCoords derives component coordinates from a v1 /files/... or a
// v2 /v2/conans/... upload path (both start with name/version/user/channel).
func uploadCoords(p string) base.Coords {
	var parts []string
	switch {
	case strings.HasPrefix(p, "/files/"):
		parts = strings.SplitN(strings.TrimPrefix(p, "/files/"), "/", 6)
	case strings.HasPrefix(p, "/v2/conans/"):
		parts = strings.SplitN(strings.TrimPrefix(p, "/v2/conans/"), "/", 5)
	}
	if len(parts) >= 4 {
		return coordsFromRef(parts[0], parts[1], parts[2], parts[3])
	}
	return base.Coords{}
}

func (h *Handler) handleUpload(c *gin.Context, repoName, p string) {
	coords := uploadCoords(p)

	ct := c.GetHeader("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, p, ct, coords,
		c.Request.Body, c.Request.ContentLength); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

func (h *Handler) handleDownload(c *gin.Context, repoName, p string) {
	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, fileStorePath(p))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = rc.Close() }()
	c.DataFromReader(http.StatusOK, asset.SizeBytes, asset.ContentType, rc, nil)
}

func (h *Handler) handleUploadURLs(c *gin.Context, repoName, p string) {
	// Body is a JSON map of filename → size. We respond with upload URLs.
	// Strip /upload_urls suffix to get the ref path, then map to PUT /files/...
	refPath := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/conans/"), "/upload_urls")
	parts := strings.SplitN(refPath, "/", 4)
	if len(parts) < 4 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid ref"})
		return
	}
	name, version, user, channel := parts[0], parts[1], parts[2], parts[3]
	// Decode file list from body
	var files map[string]int64
	if err := c.ShouldBindJSON(&files); err != nil {
		// files map not required — some clients send empty body
		files = map[string]int64{}
	}

	base2 := h.deps.BaseURL + "/repository/" + repoName
	urls := make(map[string]string, len(files))
	for filename := range files {
		// Use revision "0" as default since Conan v1 clients may not send revision
		urls[filename] = fmt.Sprintf("%s/files/%s/%s/%s/%s/0/export/%s",
			base2, name, version, user, channel, filename)
	}
	c.JSON(http.StatusOK, urls)
}

func (h *Handler) handleDownloadURLs(c *gin.Context, repoName, p string) {
	name, version, user, channel, ok := parseRef(p)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid ref"})
		return
	}
	base2 := h.deps.BaseURL + "/repository/" + repoName
	// Standard Conan recipe files
	exportFiles := []string{"conanfile.py", "conanmanifest.txt", "conan_export.tgz"}
	urls := make(map[string]string, len(exportFiles))
	for _, f := range exportFiles {
		urls[f] = fmt.Sprintf("%s/files/%s/%s/%s/%s/0/export/%s",
			base2, name, version, user, channel, f)
	}
	c.JSON(http.StatusOK, urls)
}

func (h *Handler) handleManifest(c *gin.Context, _, p string) {
	name, version, user, channel, ok := parseRef(p)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	// Return a minimal recipe reference response
	c.JSON(http.StatusOK, gin.H{
		"reference": fmt.Sprintf("%s/%s@%s/%s", name, version, user, channel),
	})
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
