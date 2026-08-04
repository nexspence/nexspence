// Package oci implements the OCI Distribution Spec v2 (Docker registry v2 protocol).
//
// All endpoints under /repository/:repoName/v2/:
//
//	GET  /v2/                                   → API version check (200 OK)
//	GET  /v2/_catalog                           → list the image names in the repository
//	GET  /v2/:name/tags/list                    → list tags
//	GET  /v2/:name/referrers/:digest            → list manifests referring to :digest
//	GET  /v2/:name/manifests/:reference         → pull manifest
//	PUT  /v2/:name/manifests/:reference         → push manifest
//	DELETE /v2/:name/manifests/:reference       → delete manifest
//	GET  /v2/:name/blobs/:digest                → pull blob (content-addressable)
//	HEAD /v2/:name/blobs/:digest                → blob exists check
//	POST /v2/:name/blobs/uploads/               → initiate blob upload
//	POST /v2/:name/blobs/uploads/?mount=&from=  → mount an existing blob
//	PATCH /v2/:name/blobs/uploads/:uuid         → stream blob chunks
//	PUT  /v2/:name/blobs/uploads/:uuid?digest=  → finalize blob upload
//	DELETE /v2/:name/blobs/:digest              → delete blob
package oci

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
)

// Handler implements the Docker registry v2 / OCI Distribution API.
type Handler struct {
	deps    formats.Deps
	uploads uploadStore
}

// New creates a Docker format Handler with the given dependencies.
func New(deps formats.Deps) *Handler {
	return &Handler{deps: deps, uploads: uploadStore{deps: deps}}
}

// Name returns the format identifier. Dispatch happens through the router's
// format registry, which maps both "docker" and "oci" to this handler.
func (h *Handler) Name() string { return "oci" }

func (h *Handler) ServeHTTP(c *gin.Context) {
	p := normPath(c.Param("path"))
	repoName := c.Param("repoName")

	// /v2/ version check
	if p == "/v2/" || p == "/v2" {
		c.Header("Docker-Distribution-API-Version", "registry/2.0")
		c.Status(http.StatusOK)
		return
	}

	// Trim leading /v2/
	rest := strings.TrimPrefix(p, "/v2/")
	if rest == p { // no /v2/ prefix
		c.Status(http.StatusNotFound)
		return
	}

	// The catalog is the one endpoint with no image name in front of it, so it is
	// matched before the split below rejects a single-segment path. Matching the
	// whole rest rather than a keyword segment keeps it unambiguous: "_catalog"
	// is not a legal image name — the OCI grammar starts every path component
	// with an alphanumeric — so no image can be shadowed by this case, and an
	// image whose name merely ends in "_catalog" still has its own endpoint
	// segment behind it and never reaches here.
	if rest == "_catalog" {
		h.handleCatalog(c, repoName)
		return
	}

	// Split: :name... / :endpoint
	// name can have multiple path components (e.g. "library/ubuntu")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		c.Status(http.StatusBadRequest)
		return
	}

	// Find the endpoint keyword from the right
	// patterns: .../tags/list | .../referrers/:digest | .../manifests/:ref |
	//           .../blobs/:digest | .../blobs/uploads/[uuid]
	switch {
	case endsWithSegments(parts, "tags", "list"):
		imageName := strings.Join(parts[:len(parts)-2], "/")
		h.handleTagsList(c, repoName, imageName)

	// Before manifests and blobs: the shape is <name>/referrers/<digest>, and
	// referrersIndex only matches the keyword in that exact position, so no
	// existing path can be re-routed by this case.
	case referrersIndex(parts) >= 0:
		idx := referrersIndex(parts)
		imageName := strings.Join(parts[:idx], "/")
		subjectDigest := strings.Join(parts[idx+1:], "/")
		h.handleReferrers(c, repoName, imageName, subjectDigest)

	case hasSegment(parts, "manifests"):
		idx := segmentIndex(parts, "manifests")
		imageName := strings.Join(parts[:idx], "/")
		reference := strings.Join(parts[idx+1:], "/")
		h.handleManifests(c, repoName, imageName, reference)

	case hasSegment(parts, "blobs"):
		idx := segmentIndex(parts, "blobs")
		imageName := strings.Join(parts[:idx], "/")
		blobParts := parts[idx+1:]
		if len(blobParts) > 0 && blobParts[0] == "uploads" {
			uuid := ""
			if len(blobParts) > 1 {
				uuid = strings.Join(blobParts[1:], "/")
			}
			h.handleBlobUploads(c, repoName, imageName, uuid)
		} else {
			digest := strings.Join(blobParts, "/")
			h.handleBlobs(c, repoName, imageName, digest)
		}

	default:
		c.Status(http.StatusNotFound)
	}
}

// ─── Tags ──────────────────────────────────────────────────────────────────

// searchPageSize is how many components one tag-list search asks for. It is not
// a cap on the answer: the component search clamps a Limit above 500 back down
// to 50 (see internal/repository/postgres/component_repo.go), so raising it
// would silently return FEWER tags. The list is assembled by paging over the
// search with a growing offset instead, which keeps a repository with more tags
// than one search page fully enumerable.
const searchPageSize = 500

func (h *Handler) handleTagsList(c *gin.Context, repoName, imageName string) {
	if c.Request.Method != http.MethodGet {
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	tags, err := h.collectTags(c.Request.Context(), repoName, imageName)
	if err != nil {
		dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	// The spec requires the tag list sorted; the search orders by (name,
	// version) across every image in the repository, which is not the same
	// thing, and an unsorted list would make the ?last= cursor meaningless.
	sort.Strings(tags)

	params := parsePageParams(c)
	page, more := paginate(tags, params)
	setNextLink(c, params, page, more)
	if page == nil {
		page = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"name": imageName, "tags": page})
}

// collectTags returns every tag of one image, paging over the component search
// until it is exhausted. The loop terminates because a continuation token is
// only handed back for a full page, and each round advances the offset by one
// full page.
func (h *Handler) collectTags(ctx context.Context, repoName, imageName string) ([]string, error) {
	var tags []string
	for offset := 0; ; offset += searchPageSize {
		page, err := h.deps.Components.Search(ctx, domain.SearchParams{
			Repository: repoName, Name: imageName, Limit: searchPageSize, Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		for _, comp := range page.Items {
			// The search matches the name with a substring ILIKE, so a sibling
			// image whose name merely contains this one would otherwise donate
			// its tags to this list.
			if comp.Name != imageName {
				continue
			}
			// A manifest pushed by tag also registers an alias under its content
			// digest so a pull by digest resolves (see pushManifest). Those are
			// references, not tags, and the spec's tag grammar has no ':', so
			// the digest form is what identifies them.
			if strings.Contains(comp.Version, ":") {
				continue
			}
			tags = append(tags, comp.Version)
		}
		if page.ContinuationToken == nil || len(page.Items) == 0 {
			return tags, nil
		}
	}
}

// ─── Manifests ─────────────────────────────────────────────────────────────

func (h *Handler) handleManifests(c *gin.Context, repoName, imageName, reference string) {
	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)
	switch c.Request.Method {
	case http.MethodGet, http.MethodHead:
		if repo != nil && repo.Type == domain.TypeProxy {
			cachePath := manifestPath(imageName, reference)
			// Upstream OCI path: /v2/{image}/manifests/{ref}
			upPath := "/v2/" + imageName + "/manifests/" + reference
			coords := base.Coords{Name: imageName, Version: reference}
			ct := "application/vnd.docker.distribution.manifest.v2+json"
			// A manifest referenced by digest is immutable; a tag (e.g. :latest)
			// is a moving pointer, so revalidate it on a TTL.
			var maxAge time.Duration
			if !strings.HasPrefix(reference, "sha256:") {
				maxAge = repoproxy.MetadataMaxAge(repo)
			}
			if err := repoproxy.ServeGET(c, h.deps, repo, cachePath, upPath, coords, ct, maxAge); err != nil {
				dockerError(c, http.StatusBadGateway, "UNKNOWN", err.Error())
				return
			}
			// Post-response work: drop cancellation (the client may already have
			// hung up) while keeping request values, as repoproxy does for its
			// own after-the-fact freshness update.
			h.recordCachedManifestMeta(context.WithoutCancel(c.Request.Context()), repo, cachePath)
			return
		}
		h.pullManifest(c, repoName, imageName, reference)
	case http.MethodPut:
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		h.pushManifest(c, repoName, imageName, reference)
	case http.MethodDelete:
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		h.deleteManifest(c, repoName, imageName, reference)
	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func manifestPath(imageName, reference string) string {
	return "/manifests/" + imageName + "/" + reference
}

func (h *Handler) pullManifest(c *gin.Context, repoName, imageName, reference string) {
	fp := manifestPath(imageName, reference)
	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, fp)
	if err != nil {
		dockerError(c, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown")
		return
	}
	defer func() { _ = rc.Close() }()
	if asset.SHA256 != "" {
		c.Header("Docker-Content-Digest", "sha256:"+asset.SHA256)
	}
	c.Header("Content-Type", asset.ContentType)
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
		c.Status(http.StatusOK)
		return
	}
	c.DataFromReader(http.StatusOK, asset.SizeBytes, asset.ContentType, rc, nil)
}

func (h *Handler) pushManifest(c *gin.Context, repoName, imageName, reference string) {
	if !requireDockerAuth(c) {
		return
	}
	ct := c.GetHeader("Content-Type")
	if ct == "" {
		ct = "application/vnd.docker.distribution.manifest.v2+json"
	}
	fp := manifestPath(imageName, reference)
	coords := base.Coords{Name: imageName, Version: reference}

	// The body is buffered because it is needed twice: stored verbatim, and
	// parsed for the artifact type. Manifests are capped at 4 MiB by the spec.
	// Reading one byte past the cap is what makes an overflow visible: a reader
	// limited to exactly the cap returns the trimmed prefix and a nil error, so
	// an oversized manifest would be stored corrupt under a digest of bytes the
	// client never sent.
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxManifestBytes+1))
	if err != nil {
		dockerError(c, http.StatusBadRequest, "MANIFEST_INVALID", err.Error())
		return
	}
	if len(body) > maxManifestBytes {
		dockerError(c, http.StatusRequestEntityTooLarge, "MANIFEST_INVALID",
			"manifest exceeds the 4MiB limit")
		return
	}

	res, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, fp, ct, coords,
		bytes.NewReader(body), int64(len(body)))
	if err != nil {
		dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}

	// Held outside the block because the digest alias below is typed from the
	// same metadata. Recording it is best effort throughout: the manifest is
	// already stored and nothing in the protocol reads these keys.
	var extra map[string]any
	if meta, ok := parseManifestMeta(body); ok {
		extra = extraFrom(meta)
	}
	if len(extra) > 0 {
		_ = h.deps.Components.UpdateExtra(c.Request.Context(), res.Asset.ComponentID, extra)
	}

	// Docker pulls always re-fetch the manifest by content digest after getting it by tag.
	// Register a second asset record pointing to the same blob under the digest path so
	// GET /manifests/<img>/sha256:<digest> also resolves correctly.
	digestRef := "sha256:" + res.SHA256
	if reference != digestRef {
		if repo, err2 := h.deps.Repos.Get(c.Request.Context(), repoName); err2 == nil && repo != nil {
			alias, aerr := base.RegisterStoredBlob(c.Request.Context(), h.deps, repo,
				manifestPath(imageName, digestRef), ct,
				base.Coords{Name: imageName, Version: digestRef},
				res.Asset.BlobKey,
				res.SHA256, res.SHA1, res.MD5, res.Size, "", "")
			// The alias carries the same metadata: the referrers API resolves a
			// subject by digest, not by tag.
			if aerr == nil && alias != nil && len(extra) > 0 {
				_ = h.deps.Components.UpdateExtra(c.Request.Context(), alias.ComponentID, extra)
			}
		}
	}

	digest := "sha256:" + res.SHA256
	c.Header("Docker-Content-Digest", digest)
	c.Header("Location", "/v2/"+imageName+"/manifests/"+digest)
	c.Status(http.StatusCreated)
}

// recordCachedManifestMeta types a manifest that repoproxy has just written to
// the cache. ServeGET does not expose the body, so it is read back once per
// distinct manifest: the recorded source digest keeps a steady-state cache hit
// off the blob store, while a tag re-pointed upstream re-types because the
// cached content — and so its digest — changed.
func (h *Handler) recordCachedManifestMeta(ctx context.Context, repo *domain.Repository, cachePath string) {
	asset, err := h.deps.Assets.GetByPath(ctx, repo.Name, cachePath)
	if err != nil || asset == nil {
		return
	}
	comp, err := h.deps.Components.Get(ctx, asset.ComponentID)
	if err != nil || comp == nil {
		return
	}
	// Keyed on WHICH manifest was typed, not on whether anything was typed. A
	// re-pointed tag changes the cached blob's digest and re-types; an unchanged
	// one skips the read even when the manifest carries no mediaType of its own.
	if dg, ok := comp.Extra[extraSourceDigestKey].(string); ok && dg == asset.SHA256 {
		return
	}

	store := h.deps.BlobStore
	if asset.BlobStoreID != "" {
		if bsMeta, getErr := h.deps.Blobs.GetByID(ctx, asset.BlobStoreID); getErr == nil {
			store = base.PhysicalStore(ctx, h.deps, bsMeta)
		}
	}
	rc, _, err := store.Get(ctx, asset.BlobKey)
	if err != nil {
		return
	}
	defer func() { _ = rc.Close() }()

	// One byte past the cap, as the push path does, so an oversized body is
	// visible rather than silently truncated into unparsable JSON. Unlike a push
	// there is nothing to reject here — the blob is already cached and already
	// served — so an overflow only skips the parse.
	body, err := io.ReadAll(io.LimitReader(rc, maxManifestBytes+1))
	if err != nil {
		return
	}
	var extra map[string]any
	if len(body) <= maxManifestBytes {
		if meta, ok := parseManifestMeta(body); ok {
			extra = extraFrom(meta)
		}
	}
	if extra == nil {
		extra = make(map[string]any, 1)
	}
	// The source digest is written unconditionally: it is what arms the guard
	// above, including for a manifest that yields no metadata at all — one that
	// is not JSON, or one too large to parse. Without it every pull would read
	// the whole blob back from the store forever.
	extra[extraSourceDigestKey] = asset.SHA256
	_ = h.deps.Components.UpdateExtra(ctx, comp.ID, extra)
}

func (h *Handler) deleteManifest(c *gin.Context, repoName, imageName, reference string) {
	fp := manifestPath(imageName, reference)
	if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, fp); err != nil {
		dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	c.Status(http.StatusAccepted)
}

// ─── Blobs ─────────────────────────────────────────────────────────────────

func blobPath(imageName, digest string) string {
	return "/blobs/" + imageName + "/" + digest
}

// referrersPath is the repository-relative form of a referrers request, in the
// same shape as manifestPath and blobPath. Nothing is stored under it — the
// index is computed per request — but a proxy failure has to be reported against
// the path on this side, as every other repoproxy caller does.
func referrersPath(imageName, subjectDigest string) string {
	return "/referrers/" + imageName + "/" + subjectDigest
}

func (h *Handler) handleBlobs(c *gin.Context, repoName, imageName, digest string) {
	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)
	switch c.Request.Method {
	case http.MethodGet, http.MethodHead:
		if repo != nil && repo.Type == domain.TypeProxy {
			cachePath := blobPath(imageName, digest)
			// Upstream OCI path: /v2/{image}/blobs/{digest}
			upPath := "/v2/" + imageName + "/blobs/" + digest
			coords := base.Coords{Name: imageName, Version: digest}
			// Blobs are content-addressed by digest — immutable, never revalidate.
			if err := repoproxy.ServeGET(c, h.deps, repo, cachePath, upPath, coords, "application/octet-stream", 0); err != nil {
				dockerError(c, http.StatusBadGateway, "UNKNOWN", err.Error())
			}
			return
		}
		h.pullBlob(c, repoName, imageName, digest)
	case http.MethodDelete:
		// A proxy repository is read-only: deleting here would evict the cached
		// copy, which manifest DELETE and the upload flow already reject (#104).
		if repoproxy.RejectMutation(c, repo) {
			return
		}
		fp := blobPath(imageName, digest)
		if err := base.DeleteArtifact(c.Request.Context(), h.deps, repoName, fp); err != nil {
			dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
			return
		}
		c.Status(http.StatusAccepted)
	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) pullBlob(c *gin.Context, repoName, imageName, digest string) {
	fp := blobPath(imageName, digest)
	rc, asset, err := base.FetchArtifact(c.Request.Context(), h.deps, repoName, fp)
	if err != nil {
		if c.Request.Method == http.MethodHead {
			c.Status(http.StatusNotFound)
		} else {
			dockerError(c, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown")
		}
		return
	}
	defer func() { _ = rc.Close() }()
	c.Header("Docker-Content-Digest", digest)
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
		c.Status(http.StatusOK)
		return
	}
	c.DataFromReader(http.StatusOK, asset.SizeBytes, "application/octet-stream", rc, nil)
}

// ─── Blob Upload (chunked / monolithic) ────────────────────────────────────

func (h *Handler) handleBlobUploads(c *gin.Context, repoName, imageName, uuid string) {
	repo, _ := h.deps.Repos.Get(c.Request.Context(), repoName)
	if repoproxy.RejectMutation(c, repo) {
		return
	}
	switch c.Request.Method {
	case http.MethodPost:
		// Initiate upload or cross-repo mount
		h.initiateUpload(c, repoName, imageName)

	case http.MethodPatch:
		// Append chunk to in-progress upload
		if uuid == "" {
			dockerError(c, http.StatusBadRequest, "BLOB_UPLOAD_INVALID", "missing uuid")
			return
		}
		h.patchUpload(c, repoName, imageName, uuid)

	case http.MethodPut:
		// Finalize upload
		if uuid == "" {
			dockerError(c, http.StatusBadRequest, "BLOB_UPLOAD_INVALID", "missing uuid")
			return
		}
		h.finalizeUpload(c, repoName, imageName, uuid)

	case http.MethodGet:
		// Upload progress
		if uuid == "" {
			c.Status(http.StatusNotFound)
			return
		}
		offset, ok := h.uploads.size(c.Request.Context(), uuid)
		if !ok {
			dockerError(c, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload unknown")
			return
		}
		c.Header("Range", fmt.Sprintf("0-%d", offset-1))
		c.Header("Docker-Upload-UUID", uuid)
		c.Status(http.StatusNoContent)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) initiateUpload(c *gin.Context, repoName, imageName string) {
	if !requireDockerAuth(c) {
		return
	}
	// Cross-repository blob mount: ?mount=<digest>&from=<name>. When it cannot be
	// served the request falls through to a normal upload session, which the spec
	// explicitly allows and which costs the client only the bandwidth it would
	// have spent anyway.
	if dgst, from := c.Query("mount"), c.Query("from"); dgst != "" && from != "" {
		if h.mountBlob(c, repoName, imageName, dgst, from) {
			return
		}
	}
	uuid, err := h.uploads.create(c.Request.Context())
	if err != nil {
		dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	// The Location must stay under the same /v2/ prefix (and short/long path
	// form) the client authenticated against. Deriving it from the request's
	// own URL keeps the blob PATCH/PUT on the authenticated /v2/ surface; a
	// hardcoded /repository/... URL routed the finalize PUT to a different
	// auth surface and returned 401 at 100% (issue #47).
	uploadURL := strings.TrimRight(c.Request.URL.Path, "/") + "/" + uuid
	c.Header("Location", uploadURL)
	c.Header("Docker-Upload-UUID", uuid)
	c.Header("Range", "0-0")
	c.Status(http.StatusAccepted)
}

// mountBlob serves POST .../blobs/uploads/?mount=<digest>&from=<name> by
// registering a second asset over the bytes the source already stores. It
// reports whether it answered the request; false means "start a normal upload".
//
// Mounting is the digest-alias registration pushManifest does after a tagged
// push, with the image name changing instead of the reference.
//
// Access control: `from` is client-supplied and names a path RBACMiddleware
// never saw — it checked the repository and path of the request URL, which is
// the mount's target, not its source. Two things close that.
//
// The source is resolved strictly inside repoName, so no mount reads out of a
// repository — or, since format is a property of the repository, a format — the
// request was not authorized against at all.
//
// Within repoName, callerMayRead puts the source path through the same check a
// direct blob GET would face, which is what makes a content selector narrower
// than the repository (`repository == "R" && path.startsWith("/public/")`) hold
// here too. Without it a selector would stop a pull but not a mount, and anyone
// holding the digest could copy the blob into a path they can read and pull it
// from there.
//
// A refused mount falls through to a normal upload session rather than 403: the
// fallback is spec-legal, costs the client only bandwidth it was going to spend,
// and does not disclose whether that digest is in the registry.
func (h *Handler) mountBlob(c *gin.Context, repoName, imageName, dgst, from string) bool {
	// The digest lands in an asset path, so a value that is not a digest is not
	// a lookup key — it is someone else's path.
	if !validDigest(dgst) {
		return false
	}
	ctx := c.Request.Context()
	repo, err := h.deps.Repos.Get(ctx, repoName)
	if err != nil || repo == nil {
		return false
	}

	for _, sourceImage := range mountSourceImages(repoName, from) {
		if sourceImage == imageName {
			continue // mounting an image onto itself has nothing to alias
		}
		sourcePath := blobPath(sourceImage, dgst)
		src, err := h.deps.Assets.GetByPath(ctx, repoName, sourcePath)
		if err != nil || src == nil {
			continue
		}
		// Nothing about the source is acted on before the caller is shown to be
		// allowed to read it.
		if !h.callerMayRead(c, repo, sourcePath) {
			continue
		}
		// An asset row outlives its bytes: a manual blob delete or a GC pass can
		// leave the record behind. Answering 201 off one of those would hand the
		// client a layer that 404s on pull, and the client — told the registry
		// already has it — would never upload the copy it still holds.
		storeName, present := h.locateBlob(ctx, src)
		if !present {
			continue
		}
		blobStoreID := src.BlobStoreID
		if storeName == "" {
			// The store the source names could not be resolved, so let the
			// registration fall back to the repository's own default, exactly as
			// a fetch of that asset would.
			blobStoreID = ""
		}
		if _, err := base.RegisterStoredBlob(ctx, h.deps, repo,
			blobPath(imageName, dgst), "application/octet-stream",
			base.Coords{Name: imageName, Version: dgst},
			src.BlobKey, src.SHA256, src.SHA1, src.MD5, src.SizeBytes,
			blobStoreID, storeName); err != nil {
			return false
		}
		c.Header("Docker-Content-Digest", dgst)
		c.Header("Location", blobLocation(c, imageName, dgst))
		c.Status(http.StatusCreated)
		return true
	}
	return false
}

// callerMayRead asks of an asset path the question a direct GET of that path
// would ask: may this caller read it, in this repository?
//
// The path handed over is the stored asset path, the same one FilterAssets
// checks, so CanAccessRepo normalizes it through assetSamplePath and the
// comparison happens against the form content selectors are written in.
//
// The caller's identity comes off the gin context, where OptionalAuth and
// RBACMiddleware leave it — the route browse_docker.go takes to the same values.
// An error is a denial: an access check that could not be completed has not
// granted anything.
func (h *Handler) callerMayRead(c *gin.Context, repo *domain.Repository, assetPath string) bool {
	if h.deps.RBAC == nil {
		return true
	}
	userID, _ := c.Get("userID")
	roles, _ := c.Get("roles")
	uid, _ := userID.(string)
	roleList, _ := roles.([]string)
	ok, err := h.deps.RBAC.CanAccessRepo(c.Request.Context(), uid, roleList, repo, assetPath, "read")
	return err == nil && ok
}

// mountSourceImages returns the image names inside repoName that a client's
// `from` value can name.
//
// A client composes `from` as the repository name it pushed the source under
// minus the registry host — reference.Path of the source reference — so its
// spelling follows whichever URL shape the push used:
//
//	host/<repo>/<image>             → from=<repo>/<image>
//	host/repository/<repo>/<image>  → from=repository/<repo>/<image>
//	<repo>.<baseDomain>/<image>     → from=<image>
//
// The last one is bare because the subdomain connector rewrites only the path;
// the query string the client built never sees the repository name. Every form
// therefore resolves inside the repository the request was already authorized
// against, and a `from` naming any other Nexspence repository resolves to
// nothing — see the access-control note on mountBlob.
//
// The two readings of "<repo>/<image>" are both returned because they are
// genuinely ambiguous: an image may legitimately be named after the repository
// it lives in. The digest pins the content either way, so whichever exists wins.
func mountSourceImages(repoName, from string) []string {
	from = strings.Trim(from, "/")
	if from == "" {
		return nil
	}
	images := []string{from}
	for _, prefix := range []string{repoName + "/", "repository/" + repoName + "/"} {
		if rest := strings.TrimPrefix(from, prefix); rest != from && rest != "" {
			images = append(images, rest)
		}
	}
	return images
}

// locateBlob reports whether an asset's bytes are really in the store, and
// returns the name of the store holding them (empty when the asset names no
// store, or names one that no longer resolves).
func (h *Handler) locateBlob(ctx context.Context, asset *domain.Asset) (storeName string, present bool) {
	store := h.deps.BlobStore
	if asset.BlobStoreID != "" {
		if meta, err := h.deps.Blobs.GetByID(ctx, asset.BlobStoreID); err == nil && meta != nil {
			store = base.PhysicalStore(ctx, h.deps, meta)
			storeName = meta.Name
		}
	}
	exists, err := store.Exists(ctx, asset.BlobKey)
	if err != nil || !exists {
		return "", false
	}
	return storeName, true
}

// blobLocation is the URL of a stored blob under the same /v2/ prefix — and the
// same short or long path form — the client authenticated against. A hardcoded
// /repository/... URL sent the follow-up request to a different auth surface and
// returned 401 (issue #47).
func blobLocation(c *gin.Context, imageName, digest string) string {
	if i := strings.Index(c.Request.URL.Path, "/blobs/"); i >= 0 {
		return c.Request.URL.Path[:i] + "/blobs/" + digest
	}
	return "/v2/" + imageName + "/blobs/" + digest
}

func (h *Handler) patchUpload(c *gin.Context, _, _, uuid string) {
	offset, ok, err := h.uploads.append(c.Request.Context(), uuid, c.Request.Body)
	if !ok {
		dockerError(c, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload unknown")
		return
	}
	if err != nil {
		dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	// The request URL already is the upload location — echo it verbatim so the
	// finalizing PUT stays on the same authenticated /v2/ path (see #47).
	c.Header("Location", c.Request.URL.Path)
	c.Header("Range", fmt.Sprintf("0-%d", offset-1))
	c.Header("Docker-Upload-UUID", uuid)
	c.Status(http.StatusAccepted)
}

func (h *Handler) finalizeUpload(c *gin.Context, repoName, imageName, uuid string) {
	digest := c.Query("digest") // e.g. "sha256:abc123..."
	if digest == "" {
		dockerError(c, http.StatusBadRequest, "DIGEST_INVALID", "digest required")
		return
	}

	ctx := c.Request.Context()

	// Any remaining body data (e.g. monolithic PUT with body)
	if c.Request.ContentLength > 0 {
		if _, ok, err := h.uploads.append(ctx, uuid, c.Request.Body); !ok {
			dockerError(c, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload unknown")
			return
		} else if err != nil {
			dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
			return
		}
	}

	data, ok, err := h.uploads.read(ctx, uuid)
	if !ok {
		dockerError(c, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload unknown")
		return
	}
	if err != nil {
		dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}

	fp := blobPath(imageName, digest)
	coords := base.Coords{Name: imageName, Version: digest}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, fp, "application/octet-stream", coords,
		bytes.NewReader(data), int64(len(data))); err != nil {
		dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	// Delete session only after successful store — allows retry on failure.
	h.uploads.remove(ctx, uuid)

	c.Header("Docker-Content-Digest", digest)
	c.Header("Location", blobLocation(c, imageName, digest))
	c.Header("Content-Range", fmt.Sprintf("0-%d", len(data)-1))
	c.Status(http.StatusCreated)
}

// ─── Helpers ───────────────────────────────────────────────────────────────

// requireDockerAuth returns true if the request carries a recognized user identity
// (set by OptionalAuth / AuthMiddleware upstream). When the identity is absent it
// challenges the Docker client with 401 + WWW-Authenticate: Basic so the client
// retries the request with credentials from its credential store.
func requireDockerAuth(c *gin.Context) bool {
	if requestctx.UserID(c.Request.Context()) != "" {
		return true
	}
	c.Header("Docker-Distribution-API-Version", "registry/2.0")
	c.Header("WWW-Authenticate", `Basic realm="Nexspence"`)
	dockerError(c, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
	return false
}

func dockerError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"errors": []gin.H{
			{"code": code, "message": message},
		},
	})
}

func endsWithSegments(parts []string, segs ...string) bool {
	if len(parts) < len(segs) {
		return false
	}
	tail := parts[len(parts)-len(segs):]
	for i, s := range segs {
		if tail[i] != s {
			return false
		}
	}
	return true
}

func hasSegment(parts []string, seg string) bool {
	return segmentIndex(parts, seg) >= 0
}

func segmentIndex(parts []string, seg string) int {
	for i, p := range parts {
		if p == seg {
			return i
		}
	}
	return -1
}

// referrersIndex reports where the "referrers" keyword sits in a split path, or
// -1 when the path is not a referrers request. The keyword is only recognized as
// the second-to-last segment — the spec's shape is {name}/referrers/{digest} and
// a digest is always exactly one segment. Matching it anywhere (as the manifests
// and blobs cases do) would let an image legitimately named ".../referrers"
// swallow its own manifest and blob requests.
func referrersIndex(parts []string) int {
	idx := len(parts) - 2
	if idx < 1 || parts[idx] != "referrers" {
		return -1
	}
	return idx
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
