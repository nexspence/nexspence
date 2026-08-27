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
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
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

	// dist-tag API. Matched before anything else: these paths also contain
	// "/-/", which would otherwise route them to the tarball handler (#101).
	if pkgName, tag, ok := parseDistTags(p); ok {
		h.handleDistTags(c, repoName, pkgName, tag)
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
		// PUT /:pkg/-rev/:rev rewrites the packument — that is how the npm
		// client unpublishes a single version (#101).
		if target, ok := splitRev(p); ok {
			h.handleRevPut(c, repoName, target)
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
		h.handleUnpublish(c, repoName, p)

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
		if minAge := repoproxy.MinimumPackageAge(repo); minAge > 0 {
			if !h.tarballAgeAllowed(c, repo, pkg, ver, minAge) {
				return
			}
		}
		coords := base.Coords{Name: pkg, Version: ver}
		if coords.Version == "" {
			coords.Version = "1"
		}
		// npm tarballs are immutable (name+version) — never revalidate.
		if err := repoproxy.ServeGET(c, h.deps, repo, filePath, "", coords, "application/octet-stream", 0); err != nil {
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
		// The packument is mutable metadata (new versions appear over time).
		// dist.tarball URLs are rewritten on serve so installs pull tarballs
		// through this proxy instead of upstream (#98); the cache keeps the
		// upstream original.
		localBase := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + repo.Name
		rewrite := func(b []byte) []byte { return RewritePackument(b, localBase) }
		if minAge := repoproxy.MinimumPackageAge(repo); minAge > 0 {
			cutoff := time.Now().Add(-minAge)
			inner := rewrite
			rewrite = func(b []byte) []byte {
				filtered, applied := FilterPackumentByAge(b, cutoff)
				if !applied {
					log.Printf("nexspence: minimum_package_age not applied for %s%s — upstream metadata carries no publish dates", repo.Name, pkgPath)
				}
				return inner(filtered)
			}
		}
		if err := repoproxy.ServeGETRewritten(c, h.deps, repo, pkgPath, up, coords, "application/json", repoproxy.MetadataMaxAge(repo), rewrite); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	comps, err := h.packageComponents(ctx, repoName, pkgName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(comps) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		return
	}

	versions := map[string]any{}
	for _, comp := range comps {
		baseName := path.Base(pkgName)
		tarball := h.deps.BaseURL + "/repository/" + repoName +
			"/" + pkgName + "/-/" + baseName + "-" + comp.Version + ".tgz"
		manifest := manifestOf(comp)
		if manifest == nil {
			manifest = h.backfillManifest(ctx, repoName, comp)
		}
		versions[comp.Version] = versionDocument(manifest, pkgName, comp.Version, tarball)
	}
	c.JSON(http.StatusOK, gin.H{
		"name":      pkgName,
		"versions":  versions,
		"dist-tags": distTagsOf(comps),
	})
}

// backfillManifest recovers the package.json of a version stored before the
// manifest was persisted (#131) by reading it out of the tarball, and caches it
// on the component so the archive is opened once. Best effort: a version whose
// manifest cannot be recovered keeps the bare document it had before.
func (h *Handler) backfillManifest(ctx context.Context, repoName string, comp domain.Component) map[string]any {
	assets, err := h.deps.Assets.ListByComponentID(ctx, comp.ID)
	if err != nil {
		return nil
	}
	for _, asset := range assets {
		if !strings.HasSuffix(asset.Path, ".tgz") {
			continue
		}
		rc, _, err := base.FetchArtifact(ctx, h.deps, repoName, asset.Path)
		if err != nil {
			continue
		}
		manifest := manifestFromTarball(rc)
		_ = rc.Close()
		if manifest == nil {
			continue
		}
		manifest = withDistShasum(manifest, asset.SHA1)
		_ = h.deps.Components.UpdateExtra(ctx, comp.ID, map[string]any{extraManifestKey: manifest})
		return manifest
	}
	return nil
}

func (h *Handler) handlePublish(c *gin.Context, repoName, pkgPath string) {
	var doc map[string]json.RawMessage
	if err := c.ShouldBindJSON(&doc); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	h.publishDoc(c, repoName, strings.TrimPrefix(pkgPath, "/"), doc)
}

// publishDoc stores the tarballs carried by an already-parsed packument and
// applies its dist-tags. Shared with the `-rev` PUT, which carries the same
// document shape.
func (h *Handler) publishDoc(c *gin.Context, repoName, pkgName string, doc map[string]json.RawMessage) {
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
		// npm names the attachment "@scope/name-ver.tgz" but requests the
		// tarball at /@scope/name/-/name-ver.tgz (#113) — drop the scope.
		filePath := "/" + pkgName + "/-/" + path.Base(filename)
		coords := base.Coords{Name: pkgName, Version: version}
		res, err := base.StoreArtifact(c.Request.Context(), h.deps,
			repoName, filePath, ct, coords,
			strings.NewReader(string(data)), int64(len(data)))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Keep the published package.json — it is the only place the dependency
		// lists exist outside the tarball (#131).
		if res == nil || res.Asset == nil || res.Asset.ComponentID == "" {
			continue
		}
		manifest := withDistShasum(manifestFromPublish(doc, version), res.SHA1)
		if manifest == nil {
			continue
		}
		if err := h.deps.Components.UpdateExtra(c.Request.Context(),
			res.Asset.ComponentID, map[string]any{extraManifestKey: manifest}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	// Honor the tags the client published under (`npm publish --tag beta`);
	// a plain publish carries "latest" (#101).
	if raw, ok := doc["dist-tags"]; ok {
		var tags map[string]string
		if json.Unmarshal(raw, &tags) == nil {
			for tag, ver := range tags {
				if tag == "" || ver == "" {
					continue
				}
				if err := h.setDistTag(c.Request.Context(), repoName, pkgName, tag, ver); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
			}
		}
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true})
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
