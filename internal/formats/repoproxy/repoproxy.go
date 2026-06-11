// Package repoproxy implements read-through caching for proxy-type repositories.
package repoproxy

import (
	"context"
	"crypto/md5"  //nolint:gosec // md5/sha1 required for artifact-protocol checksums (Maven .md5/.sha1, npm shasum), not security
	"crypto/sha1" //nolint:gosec // md5/sha1 required for artifact-protocol checksums (Maven .md5/.sha1, npm shasum), not security
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// UpstreamClient is the shared HTTP client used to fetch artifacts from upstream remotes on cache miss.
var UpstreamClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        128,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
	},
	Timeout: 5 * time.Minute,
	CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 12 {
			return fmt.Errorf("stopped after 12 redirects")
		}
		return nil
	},
}

var hopHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailers":            true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

// RejectMutation responds 405 for mutating methods on a proxy repository.
func RejectMutation(c *gin.Context, repo *domain.Repository) bool {
	if repo == nil || repo.Type != domain.TypeProxy {
		return false
	}
	switch c.Request.Method {
	case http.MethodPut, http.MethodPost, http.MethodPatch, http.MethodDelete:
		c.JSON(http.StatusMethodNotAllowed, gin.H{
			"error": "proxy repository is read-only (use a hosted repository to publish)",
		})
		return true
	default:
		return false
	}
}

// RemoteURL extracts proxy_config.remote_url.
func RemoteURL(repo *domain.Repository) (string, error) {
	if repo.ProxyConfig == nil {
		return "", fmt.Errorf("proxy_config.remote_url is required for proxy repositories")
	}
	raw, ok := repo.ProxyConfig["remote_url"]
	if !ok {
		return "", fmt.Errorf("proxy_config.remote_url is required for proxy repositories")
	}
	s, ok := raw.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("proxy_config.remote_url must be a non-empty string")
	}
	return strings.TrimRight(s, "/"), nil
}

// JoinURL joins the remote base URL with the repository-relative artifact path.
func JoinURL(remoteBase, repoRelativePath string) (string, error) {
	u, err := url.Parse(remoteBase)
	if err != nil {
		return "", fmt.Errorf("invalid remote_url: %w", err)
	}
	suffix := strings.Trim(repoRelativePath, "/")
	merged := path.Join(strings.TrimSuffix(u.Path, "/"), suffix)
	u.Path = "/" + strings.TrimPrefix(merged, "/")
	return u.String(), nil
}

func copyRespHeaders(dst http.Header, src http.Header) {
	for k, vv := range src {
		if hopHeaders[strings.ToLower(k)] {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func applyChecksumHeaders(c *gin.Context, a *domain.Asset) {
	if a.SHA256 != "" {
		c.Header("X-Checksum-SHA256", a.SHA256)
		c.Header("ETag", `"`+a.SHA256+`"`)
	}
	if a.SHA1 != "" {
		c.Header("X-Checksum-SHA1", a.SHA1)
	}
	if a.MD5 != "" {
		c.Header("X-Checksum-MD5", a.MD5)
	}
}

// ServeGET serves a cached asset or fetches upstream, streaming to the client
// and persisting to the blob store on success. repo must be TypeProxy.
// upstreamPath, when non-empty, is used only for the upstream URL (e.g. npm scoped metadata);
// the cache key and DB asset path remain repoRelativePath.
//
//nolint:gocyclo // large protocol-dispatch function (proxy cache-miss/upstream handling); splitting would hurt readability
func ServeGET(c *gin.Context, d formats.Deps, repo *domain.Repository, repoRelativePath, upstreamPath string,
	coords base.Coords, defaultContentType string,
) error {
	ctx := c.Request.Context()
	if repo.Type != domain.TypeProxy {
		return fmt.Errorf("repoproxy: repository %q is not a proxy", repo.Name)
	}

	upJoin := upstreamPath
	if upJoin == "" {
		upJoin = repoRelativePath
	}

	switch c.Request.Method {
	case http.MethodGet, http.MethodHead:
	default:
		return fmt.Errorf("repoproxy: unsupported method %s", c.Request.Method)
	}

	asset, err := d.Assets.GetByPath(ctx, repo.Name, repoRelativePath)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return fmt.Errorf("repoproxy: asset lookup: %w", err)
	}
	if asset != nil {
		var fetchStore storage.BlobStore
		if asset.BlobStoreID != "" {
			if bsMeta, getErr := d.Blobs.GetByID(ctx, asset.BlobStoreID); getErr == nil && bsMeta != nil {
				fetchStore = base.PhysicalStore(ctx, d, bsMeta)
			}
		}
		if fetchStore == nil {
			fetchStore = d.BlobStore
		}
		rc, _, blobErr := fetchStore.Get(ctx, asset.BlobKey)
		if blobErr == nil {
			defer func() { _ = rc.Close() }()
			// Count only real GETs so HEAD probe + GET pulls don't double-count.
			if c.Request.Method == http.MethodGet && d.Downloads != nil {
				d.Downloads.Add(asset.ID)
			}
			applyChecksumHeaders(c, asset)
			if c.Request.Method == http.MethodHead {
				c.Header("Content-Type", asset.ContentType)
				if asset.SizeBytes > 0 {
					c.Header("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
				}
				c.Status(http.StatusOK)
				return nil
			}
			c.DataFromReader(http.StatusOK, asset.SizeBytes, asset.ContentType, rc, nil)
			return nil
		}
		// Blob file missing from cache storage (storage path changed or file deleted).
		// Fall through to upstream fetch so the client gets content and the cache is repaired.
	}

	baseRemote, err := RemoteURL(repo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil
	}
	upstream, err := JoinURL(baseRemote, upJoin)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil
	}

	upHdr := http.Header{}
	if ac := c.GetHeader("Accept"); ac != "" {
		upHdr.Set("Accept", ac)
	}
	if inm := c.GetHeader("If-None-Match"); inm != "" {
		upHdr.Set("If-None-Match", inm)
	}
	// Do not forward client Authorization to Docker Hub — Docker sends Nexspence Basic
	// credentials; Hub would reject them. Docker Hub anonymous pulls use auth.docker.io token.

	// Docker/registry clients often probe with HEAD. Upstream HEAD has no body, so we cannot
	// cache — always use GET upstream when we need to populate the blob (HEAD or GET miss).
	upstreamMethod := c.Request.Method
	if upstreamMethod == http.MethodHead {
		upstreamMethod = http.MethodGet
	}

	resp, err := fetchUpstreamWithDockerHubAuth(ctx, upstreamMethod, upstream, baseRemote, upHdr)
	if err != nil {
		if d.Webhooks != nil {
			d.Webhooks.Dispatch(domain.WebhookPayload{
				Event:      domain.EventProxyError,
				Timestamp:  time.Now(),
				Repository: repo.Name,
				Asset: map[string]any{
					"path":     repoRelativePath,
					"upstream": upstream,
					"error":    err.Error(),
				},
			})
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream fetch failed: " + err.Error()})
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		copyRespHeaders(c.Writer.Header(), resp.Header)
		c.Status(http.StatusNotModified)
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		copyRespHeaders(c.Writer.Header(), resp.Header)
		c.Status(resp.StatusCode)
		_, _ = io.Copy(c.Writer, resp.Body)
		return nil
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = defaultContentType
	}

	copyRespHeaders(c.Writer.Header(), resp.Header)
	c.Header("Content-Type", ct)
	c.Status(resp.StatusCode)

	blobKey := base.BlobKey(repo.Name, repoRelativePath)
	sha256h := sha256.New()
	sha1h := sha1.New() //nolint:gosec // protocol checksum, not security
	md5h := md5.New()   //nolint:gosec // protocol checksum, not security

	// Resolve the physical blob store for this repo so the write location matches
	// what RegisterStoredBlob will record in the DB asset row.
	resolvedID, resolvedName, physStore := base.ResolveBlobStore(ctx, d, repo)

	pr, pw := io.Pipe()
	putErrCh := make(chan error, 1)
	go func() {
		putErrCh <- physStore.Put(ctx, blobKey, pr, resp.ContentLength)
	}()

	hashes := io.MultiWriter(sha256h, sha1h, md5h)
	clientSink := io.Writer(c.Writer)
	if c.Request.Method == http.MethodHead {
		clientSink = io.Discard
	}
	mw := io.MultiWriter(pw, hashes, clientSink)

	written, copyErr := io.Copy(mw, resp.Body)
	_ = pw.CloseWithError(copyErr)
	putErr := <-putErrCh

	if copyErr != nil || putErr != nil {
		_ = physStore.Delete(ctx, blobKey)
		return fmt.Errorf("proxy cache write: %w", errors.Join(copyErr, putErr))
	}

	sha256sum := hex.EncodeToString(sha256h.Sum(nil))
	sha1sum := hex.EncodeToString(sha1h.Sum(nil))
	md5sum := hex.EncodeToString(md5h.Sum(nil))

	size := written
	if size <= 0 {
		if s, e := physStore.Size(ctx, blobKey); e == nil {
			size = s
		}
	}

	// Use context.Background so DB registration survives request context cancellation
	// after streaming (client closes connection once all bytes are received).
	regAsset, regErr := base.RegisterStoredBlob(context.Background(), d, repo, repoRelativePath, ct, coords, blobKey, sha256sum, sha1sum, md5sum, size, resolvedID, resolvedName)
	if regErr != nil {
		return regErr
	}
	// Count a download only for GET (see HEAD branch above). Otherwise a HEAD probe + GET
	// hit would double-count the same pull.
	if regAsset != nil && regAsset.ID != "" && c.Request.Method == http.MethodGet && d.Downloads != nil {
		d.Downloads.Add(regAsset.ID)
	}
	return nil
}

// NPMMetadataPath returns the path segment npmjs.org uses for metadata (scoped packages use %2F).
func NPMMetadataPath(pkgPath string) string {
	pkg := strings.Trim(strings.TrimPrefix(pkgPath, "/"), "/")
	if strings.HasPrefix(pkg, "@") {
		slash := strings.Index(pkg, "/")
		if slash > 0 {
			return pkg[:slash] + "%2F" + pkg[slash+1:]
		}
	}
	return pkg
}
