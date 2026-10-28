// Package alpine implements the Alpine Linux "apk" repository protocol.
//
// Layout under /repository/:repoName/, mirroring how apk resolves a bare
// repository URL configured in /etc/apk/repositories (it appends /<arch>/
// itself — Alpine has no dist/component split like apt):
//
//	GET  /:arch/APKINDEX.tar.gz     → index (generated on the fly)
//	GET  /:arch/:file.apk           → package download
//	PUT|POST /:arch/:file.apk       → upload (hosted; not part of any real apk protocol, same as apt/yum's own upload conventions)
//	DELETE /:arch/:file.apk         → delete
package alpine

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // apk protocol checksum (Q1 = SHA-1 prefix), not security
	"encoding/base64"
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

// maxApkBytes bounds the in-memory buffer needed to isolate an .apk's control
// tar.gz segment for the APKINDEX C: checksum (checksum.go) — same tradeoff
// already accepted for RubyGems' gemspec parsing (rubygems/handler.go).
const maxApkBytes = 256 << 20 // 256 MiB

// Handler serves the Alpine apk repository protocol.
type Handler struct{ deps formats.Deps }

// New creates an Alpine format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "alpine" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)

	if repo != nil && repo.Type == domain.TypeProxy {
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		h.serveProxy(c, repo, p)
		return
	}
	h.serveHosted(c, repoName, p)
}

// serveHosted routes the hosted (non-proxy) surface of the apk protocol.
func (h *Handler) serveHosted(c *gin.Context, repoName, p string) {
	switch {
	case c.Request.Method == http.MethodGet && strings.HasSuffix(p, "/APKINDEX.tar.gz"):
		h.serveIndex(c, repoName, p)

	case (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && strings.HasSuffix(p, ".apk"):
		h.serveFile(c, repoName, p)

	case (c.Request.Method == http.MethodPut || c.Request.Method == http.MethodPost) && strings.HasSuffix(p, ".apk"):
		h.handleUpload(c, repoName, p)

	case c.Request.Method == http.MethodDelete && strings.HasSuffix(p, ".apk"):
		if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, p); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) serveIndex(c *gin.Context, repoName, p string) {
	plain, err := h.buildIndex(c.Request.Context(), repoName, p)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	packed, err := packIndexTarGz(plain)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/gzip", packed)
}

// apkCoords parses the Alpine "<name>-<version>-r<rel>.apk" filename
// convention. The name may itself contain hyphens, so the split point is the
// first hyphen-separated segment that starts with a digit — the same
// heuristic already verified for the PyPI wheel/sdist filename fix
// (UPSTREAM_ISSUES.md). Non-conforming names keep the whole filename as the
// package name, same fallback apt's debCoords uses.
func apkCoords(filename string) (name, version string) {
	stem := strings.TrimSuffix(filename, ".apk")
	parts := strings.Split(stem, "-")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" && parts[i][0] >= '0' && parts[i][0] <= '9' {
			return strings.Join(parts[:i], "-"), strings.Join(parts[i:], "-")
		}
	}
	return stem, "0.0.0"
}

func (h *Handler) handleUpload(c *gin.Context, repoName, p string) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxApkBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(body) > maxApkBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "apk exceeds the maximum size"})
		return
	}
	compressed, rawControl, err := controlSegment(body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid .apk: %s", err.Error())})
		return
	}
	name, version, extra := apkMetadata(path.Base(p), compressed, rawControl)

	// The index directory is part of the coordinates: the checksum, sizes and
	// dependencies above describe this one file, and the same name and version
	// built for another architecture or branch is a different file whose
	// index entry must not read them (#671).
	coords := base.Coords{Group: indexDir(p), Name: name, Version: version}
	ctx := c.Request.Context()
	res, err := base.StoreArtifact(ctx, h.deps,
		repoName, p, "application/x-alpine-apk", coords,
		bytes.NewReader(body), int64(len(body)))
	if err != nil {
		c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
		return
	}
	repo, err := h.deps.Repos.Get(ctx, repoName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.placeInIndexDir(ctx, repo, res.Asset, "", coords, extra); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

// apkMetadata derives an .apk's coordinates and APKINDEX metadata from its
// control segment. .PKGINFO (real abuild metadata) is authoritative for
// name/version, same as the real `apk index` tool — the filename convention is
// only a fallback for a control segment that failed to parse.
func apkMetadata(filename string, compressed, rawControl []byte) (name, version string, extra map[string]any) {
	sum := sha1.Sum(compressed) //nolint:gosec // apk protocol checksum, not security
	extra = map[string]any{"checksum": "Q1" + base64.StdEncoding.EncodeToString(sum[:])}
	name, version = apkCoords(filename)
	if info, pErr := parsePKGInfo(rawControl); pErr == nil {
		name, version = info.Name, info.Version
		if info.Description != "" {
			extra["description"] = info.Description
		}
		if info.License != "" {
			extra["license"] = info.License
		}
		if info.InstalledSize > 0 {
			extra["installedSize"] = info.InstalledSize
		}
		if len(info.Depends) > 0 {
			extra["depends"] = info.Depends
		}
		if len(info.Provides) > 0 {
			extra["provides"] = info.Provides
		}
	}
	return name, version, extra
}

// indexDir is the directory an .apk is indexed under, without the leading
// slash: "x86_64" or "v3.20/main/x86_64".
func indexDir(p string) string {
	return strings.TrimPrefix(path.Dir(p), "/")
}

// placeInIndexDir records extra on the component coords name and moves asset
// there if it sits elsewhere. A path first uploaded before the index directory
// was part of the coordinates keeps its old, directory-less component on
// upsert; once the last such path has moved, that component is dropped. A
// non-empty sha256 is the content extra was read from: if the path holds
// other bytes by now, nothing is written — the upload that replaced them
// recorded their own.
func (h *Handler) placeInIndexDir(ctx context.Context, repo *domain.Repository, asset *domain.Asset, sha256 string, coords base.Coords, extra map[string]any) error {
	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name, Format: string(repo.Format),
		Group: coords.Group, Name: coords.Name, Version: coords.Version,
	}
	if err := h.deps.Components.Create(ctx, comp); err != nil {
		return fmt.Errorf("component for %s: %w", asset.Path, err)
	}
	if sha256 != "" {
		cur, err := h.deps.Assets.GetByPath(ctx, repo.Name, asset.Path)
		if err != nil {
			return fmt.Errorf("look up %s: %w", asset.Path, err)
		}
		if cur.SHA256 != sha256 {
			return nil
		}
		asset = cur
	}
	if asset.ComponentID != comp.ID {
		if err := h.deps.Assets.SetComponent(ctx, asset.ID, comp.ID); err != nil {
			return fmt.Errorf("move %s to its index directory's component: %w", asset.Path, err)
		}
		asset.ComponentID = comp.ID
		// Atomic per component, so it cannot drop one another upload is
		// storing into right now.
		if err := h.deps.Components.DeleteOrphans(ctx, repo.Name); err != nil {
			return err
		}
	}
	return h.deps.Components.UpdateExtra(ctx, comp.ID, extra)
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
	c.DataFromReader(http.StatusOK, asset.SizeBytes, "application/x-alpine-apk", rc, nil)
}

// serveProxy passes reads through to the configured upstream (e.g.
// dl-cdn.alpinelinux.org), caching the result. Packages are immutable
// (maxAge 0); the index is mutable metadata revalidated on a TTL, same
// distinction apt/yum make between /pool/ and /dists/.
func (h *Handler) serveProxy(c *gin.Context, repo *domain.Repository, p string) {
	ct := "application/octet-stream"
	var maxAge time.Duration
	if strings.HasSuffix(p, "/APKINDEX.tar.gz") {
		ct = "application/gzip"
		maxAge = repoproxy.MetadataMaxAge(repo)
	}
	if err := repoproxy.ServeGET(c, h.deps, repo, p, "", proxyCoords(p), ct, maxAge); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
	}
}

// proxyCoords derives component coordinates for a file cached from upstream —
// real coordinates for packages (browsable like hosted ones), path-keyed for
// the index (a distinct document per architecture, not a package version).
func proxyCoords(p string) base.Coords {
	if strings.HasSuffix(p, ".apk") {
		name, version := apkCoords(path.Base(p))
		return base.Coords{Name: name, Version: version}
	}
	return base.Coords{Name: strings.TrimPrefix(p, "/"), Version: "metadata"}
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
