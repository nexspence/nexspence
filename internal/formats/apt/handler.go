// Package apt implements the Debian APT repository protocol.
//
// Layout under /repository/:repoName/:
//
//	GET  /dists/:dist/:component/binary-:arch/Packages[.gz] → packages index
//	GET  /dists/:dist/Release                               → Release file
//	GET  /pool/:component/:prefix/:name_ver_arch.deb        → deb download
//	PUT  /pool/:component/:name_ver_arch.deb                → upload .deb
//	DELETE /pool/:component/:name_ver_arch.deb              → delete .deb
package apt

import (
	"bytes"
	"compress/gzip"
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

// Handler serves the Debian APT repository protocol.
type Handler struct{ deps formats.Deps }

// New creates an APT format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "apt" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	// Proxy: block uploads/deletes, pass reads through to upstream (e.g. archive.ubuntu.com)
	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		ct := "application/octet-stream"
		if strings.HasSuffix(p, "/Release") || strings.HasSuffix(p, "/InRelease") {
			ct = "text/plain"
		} else if strings.Contains(p, "/Packages") {
			ct = "text/plain"
		}
		// /pool/ holds immutable .deb artifacts; everything under /dists/
		// (Release/InRelease/Packages and other indexes) is mutable metadata that
		// upstreams re-sign with an expiry, so it must be revalidated on a TTL.
		var maxAge time.Duration
		if !strings.HasPrefix(p, "/pool/") {
			maxAge = repoproxy.MetadataMaxAge(repo)
		}
		coords := base.Coords{}
		if err := repoproxy.ServeGET(c, h.deps, repo, p, "", coords, ct, maxAge); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		}
		return
	}

	switch {
	// Packages index (plain or gzip)
	case c.Request.Method == http.MethodGet && strings.HasPrefix(p, "/dists/") && strings.Contains(p, "/Packages"):
		h.servePackagesIndex(c, repoName, p)

	// Release file
	case c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/Release"):
		h.serveRelease(c, repoName, p)

	// InRelease (signed — serve same as Release for compatibility)
	case c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/InRelease"):
		h.serveRelease(c, repoName, p)

	// Download .deb
	case (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && strings.HasPrefix(p, "/pool/"):
		h.serveFile(c, repoName, p)

	// Upload .deb: PUT /pool/:component/:file.deb, or a root-level PUT of a .deb
	// (apt clients and `curl --upload-file foo.deb .../repository/<repo>/` upload
	// to the repository root rather than an explicit pool path).
	case c.Request.Method == http.MethodPut && (strings.HasPrefix(p, "/pool/") || strings.HasSuffix(p, ".deb")):
		h.handleUpload(c, repoName, p)

	// Delete .deb
	case c.Request.Method == http.MethodDelete && strings.HasPrefix(p, "/pool/"):
		if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, p); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) servePackagesIndex(c *gin.Context, repoName, p string) {
	gzipped := strings.HasSuffix(p, ".gz")

	// Parse: /dists/:dist/:component/binary-:arch/Packages[.gz]
	// We use all components regardless of the specific path for now
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Limit: 1000,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// List assets to get file details
	assetPage, err := h.deps.Assets.List(c.Request.Context(), repoName, 1000, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Build component → asset map
	compMap := map[string]*domain.Component{}
	for i := range page.Items {
		compMap[page.Items[i].ID] = &page.Items[i]
	}

	var sb strings.Builder
	for _, a := range assetPage.Items {
		if !strings.HasSuffix(a.Path, ".deb") {
			continue
		}
		comp := compMap[a.ComponentID]
		if comp == nil {
			continue
		}
		// Minimal Packages stanza
		pkgName := comp.Name
		version := comp.Version
		arch := "amd64" // default; ideally parsed from filename
		filename := path.Base(a.Path)
		if parts := strings.Split(strings.TrimSuffix(filename, ".deb"), "_"); len(parts) >= 3 {
			arch = parts[len(parts)-1]
		}
		fmt.Fprintf(&sb, "Package: %s\n", pkgName)
		fmt.Fprintf(&sb, "Version: %s\n", version)
		fmt.Fprintf(&sb, "Architecture: %s\n", arch)
		fmt.Fprintf(&sb, "Filename: %s\n", a.Path)
		fmt.Fprintf(&sb, "Size: %d\n", a.SizeBytes)
		if a.SHA256 != "" {
			fmt.Fprintf(&sb, "SHA256: %s\n", a.SHA256)
		}
		if a.MD5 != "" {
			fmt.Fprintf(&sb, "MD5sum: %s\n", a.MD5)
		}
		sb.WriteString("\n")
	}

	data := []byte(sb.String())
	if gzipped {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		_, _ = gw.Write(data)
		_ = gw.Close()
		c.Data(http.StatusOK, "application/x-gzip", buf.Bytes())
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", data)
}

func (h *Handler) serveRelease(c *gin.Context, _, p string) {
	// Parse distribution from path: /dists/:dist/Release
	parts := strings.Split(strings.TrimPrefix(p, "/dists/"), "/")
	dist := "stable"
	if len(parts) > 0 {
		dist = parts[0]
	}

	now := time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 UTC")
	release := fmt.Sprintf(`Origin: Nexspence
Label: Nexspence
Suite: %s
Codename: %s
Date: %s
Architectures: amd64 arm64 all
Components: main contrib non-free
Description: Nexspence APT Repository
`, dist, dist, now)
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(release))
}

func (h *Handler) handleUpload(c *gin.Context, repoName, p string) {
	filename := path.Base(p)
	// Parse: name_version_arch.deb
	parts := strings.Split(strings.TrimSuffix(filename, ".deb"), "_")
	pkgName, version := filename, "0.0.0"
	if len(parts) >= 2 {
		pkgName = parts[0]
		version = parts[1]
	}

	// Normalize root-level uploads into the canonical pool layout so the
	// Packages index (which lists /pool/ assets) still finds them.
	storePath := p
	if !strings.HasPrefix(storePath, "/pool/") {
		storePath = poolPath(pkgName, filename)
	}

	coords := base.Coords{Name: pkgName, Version: version}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, storePath, "application/vnd.debian.binary-package",
		coords, c.Request.Body, c.Request.ContentLength); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

func (h *Handler) serveFile(c *gin.Context, repoName, p string) {
	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, p)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer func() { _ = rc.Close() }()
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
		c.Status(http.StatusOK)
		return
	}
	c.DataFromReader(http.StatusOK, asset.SizeBytes, asset.ContentType, rc, nil)
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}

// poolPath builds the canonical Debian pool location for a root-level upload:
// /pool/main/<prefix>/<pkg>/<file>.deb, where <prefix> follows Debian's
// convention (the first letter, or "lib<x>" for lib* packages).
func poolPath(pkgName, filename string) string {
	prefix := "_"
	if pkgName != "" {
		if strings.HasPrefix(pkgName, "lib") && len(pkgName) > 3 {
			prefix = pkgName[:4]
		} else {
			prefix = pkgName[:1]
		}
	}
	return "/pool/main/" + prefix + "/" + pkgName + "/" + filename
}
