// Package docker implements the OCI Distribution Spec v2 (Docker registry v2 protocol).
//
// All endpoints under /repository/:repoName/v2/:
//
//	GET  /v2/                                   → API version check (200 OK)
//	GET  /v2/:name/tags/list                    → list tags
//	GET  /v2/:name/manifests/:reference         → pull manifest
//	PUT  /v2/:name/manifests/:reference         → push manifest
//	DELETE /v2/:name/manifests/:reference       → delete manifest
//	GET  /v2/:name/blobs/:digest                → pull blob (content-addressable)
//	HEAD /v2/:name/blobs/:digest                → blob exists check
//	POST /v2/:name/blobs/uploads/               → initiate blob upload
//	PATCH /v2/:name/blobs/uploads/:uuid         → stream blob chunks
//	PUT  /v2/:name/blobs/uploads/:uuid?digest=  → finalize blob upload
//	DELETE /v2/:name/blobs/:digest              → delete blob
package docker

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
)

// uploadSession stores an in-progress blob upload.
type uploadSession struct {
	repoName string
	buf      bytes.Buffer
	mu       sync.Mutex
	created  time.Time
}

// Handler implements the Docker registry v2 / OCI Distribution API.
type Handler struct {
	deps    formats.Deps
	uploads sync.Map // uuid → *uploadSession
}

// New creates a Docker format Handler with the given dependencies.
func New(deps formats.Deps) *Handler { return &Handler{deps: deps} }

// Name returns the format identifier.
func (h *Handler) Name() string { return "docker" }

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

	// Split: :name... / :endpoint
	// name can have multiple path components (e.g. "library/ubuntu")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		c.Status(http.StatusBadRequest)
		return
	}

	// Find the endpoint keyword from the right
	// patterns: .../tags/list | .../manifests/:ref | .../blobs/:digest | .../blobs/uploads/[uuid]
	switch {
	case endsWithSegments(parts, "tags", "list"):
		imageName := strings.Join(parts[:len(parts)-2], "/")
		h.handleTagsList(c, repoName, imageName)

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

func (h *Handler) handleTagsList(c *gin.Context, repoName, imageName string) {
	if c.Request.Method != http.MethodGet {
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	page, err := h.deps.Components.Search(c.Request.Context(), domain.SearchParams{
		Repository: repoName, Name: imageName, Limit: 500,
	})
	if err != nil {
		dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	tags := make([]string, 0, len(page.Items))
	for _, comp := range page.Items {
		tags = append(tags, comp.Version)
	}
	c.JSON(http.StatusOK, gin.H{"name": imageName, "tags": tags})
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
			}
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
	res, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, fp, ct, coords,
		c.Request.Body, c.Request.ContentLength)
	if err != nil {
		dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}

	// Docker pulls always re-fetch the manifest by content digest after getting it by tag.
	// Register a second asset record pointing to the same blob under the digest path so
	// GET /manifests/<img>/sha256:<digest> also resolves correctly.
	digestRef := "sha256:" + res.SHA256
	if reference != digestRef {
		if repo, err2 := h.deps.Repos.Get(c.Request.Context(), repoName); err2 == nil && repo != nil {
			_, _ = base.RegisterStoredBlob(c.Request.Context(), h.deps, repo,
				manifestPath(imageName, digestRef), ct,
				base.Coords{Name: imageName, Version: digestRef},
				res.Asset.BlobKey,
				res.SHA256, res.SHA1, res.MD5, res.Size, "", "")
		}
	}

	digest := "sha256:" + res.SHA256
	c.Header("Docker-Content-Digest", digest)
	c.Header("Location", "/v2/"+imageName+"/manifests/"+digest)
	c.Status(http.StatusCreated)
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
		raw, ok := h.uploads.Load(uuid)
		if !ok {
			dockerError(c, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload unknown")
			return
		}
		sess := raw.(*uploadSession)
		sess.mu.Lock()
		offset := int64(sess.buf.Len())
		sess.mu.Unlock()
		c.Header("Range", fmt.Sprintf("0-%d", offset-1))
		c.Header("Docker-Upload-UUID", uuid)
		c.Status(http.StatusNoContent)

	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) initiateUpload(c *gin.Context, repoName, _ string) {
	if !requireDockerAuth(c) {
		return
	}
	// Cross-repo mount shortcut: ?mount=<digest>&from=<repo>
	// We ignore mount for now and always start a fresh upload
	uuid := newUUID()
	h.uploads.Store(uuid, &uploadSession{
		repoName: repoName,
		created:  time.Now(),
	})
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

func (h *Handler) patchUpload(c *gin.Context, _, _, uuid string) {
	raw, ok := h.uploads.Load(uuid)
	if !ok {
		dockerError(c, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload unknown")
		return
	}
	sess := raw.(*uploadSession)
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if _, err := io.Copy(&sess.buf, c.Request.Body); err != nil {
		dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	offset := int64(sess.buf.Len())
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

	raw, ok := h.uploads.Load(uuid)
	if !ok {
		dockerError(c, http.StatusNotFound, "BLOB_UPLOAD_UNKNOWN", "upload unknown")
		return
	}
	sess := raw.(*uploadSession)

	sess.mu.Lock()
	defer sess.mu.Unlock()

	// Any remaining body data (e.g. monolithic PUT with body)
	if c.Request.ContentLength > 0 {
		_, _ = io.Copy(&sess.buf, c.Request.Body)
	}

	fp := blobPath(imageName, digest)
	data := sess.buf.Bytes()
	coords := base.Coords{Name: imageName, Version: digest}
	if _, err := base.StoreArtifact(c.Request.Context(), h.deps,
		repoName, fp, "application/octet-stream", coords,
		bytes.NewReader(data), int64(len(data))); err != nil {
		dockerError(c, http.StatusInternalServerError, "UNKNOWN", err.Error())
		return
	}
	// Delete session only after successful store — allows retry on failure.
	h.uploads.Delete(uuid)

	c.Header("Docker-Content-Digest", digest)
	// Point at the stored blob under the same /v2/ prefix the client used.
	blobLoc := "/v2/" + imageName + "/blobs/" + digest
	if i := strings.Index(c.Request.URL.Path, "/blobs/"); i >= 0 {
		blobLoc = c.Request.URL.Path[:i] + "/blobs/" + digest
	}
	c.Header("Location", blobLoc)
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

var uuidCounter uint64
var uuidMu sync.Mutex

func newUUID() string {
	uuidMu.Lock()
	uuidCounter++
	n := uuidCounter
	uuidMu.Unlock()
	return fmt.Sprintf("%016x-%d", time.Now().UnixNano(), n)
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
