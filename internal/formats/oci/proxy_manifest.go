package oci

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// A tag caches the last negotiated representation, not every possible Accept
// string. An incompatible request goes upstream with its own Accept; every
// representation already served remains independently addressable by digest.
// Both rows point to an immutable digest object. No writer overwrites a tag's
// blob key, so replacing the tag cannot mutate bytes pinned by an earlier pull.
// Existing tag objects are verified and copied to a pinned digest object before
// being served or replaced. No migration or process-local state is required.
func (h *Handler) proxyManifest(c *gin.Context, repo *domain.Repository, image, ref string) error {
	if strings.Contains(ref, ":") && (!validDigest(ref) || !strings.HasPrefix(ref, "sha256:")) {
		dockerError(c, http.StatusBadRequest, "DIGEST_INVALID", "invalid manifest digest")
		return nil
	}
	ctx := c.Request.Context()
	var cached *domain.Asset
	var body *manifestBody
	var repairErr error
	err := h.withManifestLock(ctx, repo, image, ref, func(ctx context.Context) error {
		var err error
		cached, body, err = h.cachedManifest(ctx, repo.Name, manifestPath(image, ref), true)
		if err != nil {
			return err
		}
		if cached != nil && !strings.Contains(ref, ":") {
			_, repairErr = h.persistManifest(ctx, repo, image, ref, cached.ContentType, body, cached)
		}
		return nil
	})
	defer body.Close()
	if err != nil {
		return err
	}
	// Network verification must not retain an advisory-lock connection.
	if repairErr != nil && !h.manifestAvailableUpstream(ctx, repo, image, cached.ContentType, body) {
		return repairErr
	}
	compatible := cached != nil && acceptsManifest(manifestAccept(c), cached.ContentType)
	if cached != nil && strings.Contains(ref, ":") && !compatible {
		dockerError(c, http.StatusNotAcceptable, "MANIFEST_UNKNOWN", "manifest media type is not acceptable")
		return nil
	}
	if compatible && (strings.Contains(ref, ":") || (prefersCachedManifest(manifestAccept(c), cached.ContentType) && time.Since(cached.LastModified) <= repoproxy.MetadataMaxAge(repo))) {
		h.respondManifest(c, repo, image, ref, cached, body)
		return nil
	}
	hdr := http.Header{}
	hdr.Set("Accept", manifestAccept(c))
	if compatible && prefersCachedManifest(manifestAccept(c), cached.ContentType) && !cached.LastModified.IsZero() {
		hdr.Set("If-Modified-Since", cached.LastModified.UTC().Format(http.TimeFormat))
	}
	upPath := "/v2/" + image + "/manifests/" + ref
	resp, err := repoproxy.FetchUpstreamOnce(ctx, repo, upPath, "", hdr)
	if err != nil {
		repoproxy.DispatchProxyError(h.deps, repo.Name, manifestPath(image, ref), upPath, err)
		if compatible {
			h.respondManifest(c, repo, image, ref, cached, body)
			return nil
		}
		return fmt.Errorf("fetch manifest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified && compatible {
		if err := h.withManifestLock(ctx, repo, image, ref, func(ctx context.Context) error {
			current, err := h.deps.Assets.GetByPath(ctx, repo.Name, manifestPath(image, ref))
			if err != nil {
				return err
			}
			// A concurrent tag replacement was not revalidated by this response.
			if current.SHA256 != cached.SHA256 || !current.LastModified.Equal(cached.LastModified) {
				return nil
			}
			return h.deps.Assets.TouchLastModified(ctx, current.ID)
		}); err != nil {
			// The verified bytes remain valid; leave freshness stale so the next
			// request can revalidate again if the timestamp update failed.
			repoproxy.LogCacheRefreshError(ctx, repo.Name, cached.Path, err)
		}
		h.respondManifest(c, repo, image, ref, cached, body)
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return h.manifestUpstreamStatus(c, repo, image, ref, resp, cached, body, compatible)
	}
	return h.serveFetchedManifest(ctx, c, repo, image, ref, resp)
}

func (h *Handler) withManifestLock(ctx context.Context, repo *domain.Repository, image, ref string, fn func(context.Context) error) error {
	return h.deps.Assets.WithBlobKeyLock(ctx, "oci:manifest:"+repo.Name+":"+manifestPath(image, ref), fn)
}

func (h *Handler) serveFetchedManifest(ctx context.Context, c *gin.Context, repo *domain.Repository, image, ref string, resp *http.Response) error {
	body, err := spoolManifest(resp.Body)
	if err != nil {
		return err
	}
	defer body.Close()
	if resp.ContentLength >= 0 && resp.ContentLength != body.size {
		return fmt.Errorf("truncated manifest response")
	}
	ct, err := body.contentType(resp.Header.Get("Content-Type"))
	if err != nil {
		return err
	}
	if strings.Contains(ref, ":") && ref != "sha256:"+body.sha256 {
		return fmt.Errorf("upstream manifest does not match requested digest")
	}
	if !acceptsManifest(manifestAccept(c), ct) {
		dockerError(c, http.StatusNotAcceptable, "MANIFEST_UNKNOWN", "upstream manifest media type is not acceptable")
		return nil
	}
	var asset *domain.Asset
	storeErr := h.withManifestLock(ctx, repo, image, ref, func(ctx context.Context) error {
		// Another instance may have filled or repaired the tag during the fetch.
		// Pin its current bytes before replacing it, never the earlier snapshot.
		if !strings.Contains(ref, ":") {
			current, held, err := h.cachedManifest(ctx, repo.Name, manifestPath(image, ref), true)
			if err != nil {
				return err
			}
			defer held.Close()
			if current != nil {
				if _, err := h.persistManifest(ctx, repo, image, ref, current.ContentType, held, current); err != nil {
					return err
				}
			}
		}
		var err error
		asset, err = h.persistManifest(ctx, repo, image, ref, ct, body, nil)
		return err
	})
	if storeErr != nil {
		// Preserve proxy pass-through at full quota, but only if these exact bytes
		// can be fetched by digest upstream. A tag-only variant must be persisted
		// before acknowledging it, or the client's next request would fail.
		if !h.manifestAvailableUpstream(ctx, repo, image, ct, body) {
			return fmt.Errorf("persist manifest: %w", storeErr)
		}
		asset = &domain.Asset{ContentType: ct, SHA256: body.sha256, SizeBytes: body.size}
	}
	h.respondManifest(c, repo, image, ref, asset, body)
	return nil
}

func (h *Handler) manifestAvailableUpstream(ctx context.Context, repo *domain.Repository, image, ct string, body *manifestBody) bool {
	ref := "sha256:" + body.sha256
	resp, err := repoproxy.FetchUpstreamOnce(ctx, repo, "/v2/"+image+"/manifests/"+ref, "", http.Header{"Accept": []string{ct}})
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	original, err := spoolManifest(resp.Body)
	if err != nil {
		return false
	}
	defer original.Close()
	if original.sha256 != body.sha256 || original.size != body.size {
		return false
	}
	actual, err := original.contentType(resp.Header.Get("Content-Type"))
	return err == nil && actual == ct
}

func (h *Handler) respondManifest(c *gin.Context, repo *domain.Repository, image, ref string, asset *domain.Asset, body *manifestBody) {
	// Recheck after selecting and registering: a newly created digest alias must
	// inherit the tag's scan gate, including a stricter group policy.
	if !h.pullGateAllows(c, repo, image, ref) || !h.pullGateAllows(c, repo, image, "sha256:"+asset.SHA256) {
		return
	}
	c.Header("Vary", "Accept")
	c.Header("Docker-Content-Digest", "sha256:"+asset.SHA256)
	c.Header("Docker-Distribution-API-Version", "registry/2.0")
	c.Header("Content-Type", asset.ContentType)
	c.Header("Content-Length", strconv.FormatInt(body.size, 10))
	etag := `"sha256:` + asset.SHA256 + `"`
	c.Header("ETag", etag)
	c.Header("X-Checksum-Sha256", asset.SHA256)
	if asset.SHA1 != "" {
		c.Header("X-Checksum-Sha1", asset.SHA1)
	}
	if asset.MD5 != "" {
		c.Header("X-Checksum-Md5", asset.MD5)
	}
	for _, candidate := range strings.Split(strings.Join(c.Request.Header.Values("If-None-Match"), ","), ",") {
		if strings.TrimSpace(candidate) == "*" || strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == etag {
			c.Writer.Header().Del("Content-Length")
			c.Status(http.StatusNotModified)
			return
		}
	}
	if c.Request.Method == http.MethodHead {
		c.Status(http.StatusOK)
		return
	}
	if asset.ID != "" && h.deps.Downloads != nil {
		h.deps.Downloads.Add(asset.ID)
	}
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, body.reader())
}

func (h *Handler) manifestUpstreamStatus(c *gin.Context, repo *domain.Repository, image, ref string, resp *http.Response, cached *domain.Asset, body *manifestBody, compatible bool) error {
	if compatible {
		repoproxy.DispatchProxyError(h.deps, repo.Name, manifestPath(image, ref), "/v2/"+image+"/manifests/"+ref, fmt.Errorf("manifest upstream status %d", resp.StatusCode))
		h.respondManifest(c, repo, image, ref, cached, body)
		return nil
	}
	// Bound error bodies too; never relay an upstream success without bytes.
	if resp.StatusCode == http.StatusNotModified || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return fmt.Errorf("unexpected manifest upstream status %d", resp.StatusCode)
	}
	if challenge := resp.Header.Get("WWW-Authenticate"); challenge != "" {
		c.Header("WWW-Authenticate", challenge)
	}
	c.Status(resp.StatusCode)
	if c.Request.Method != http.MethodHead {
		_, _ = io.Copy(c.Writer, io.LimitReader(resp.Body, maxManifestBytes))
	}
	return nil
}
