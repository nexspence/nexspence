// Package repoproxy implements read-through caching for proxy-type repositories.
package repoproxy

import (
	"context"
	"crypto/md5"  //nolint:gosec // md5/sha1 required for artifact-protocol checksums (Maven .md5/.sha1, npm shasum), not security
	"crypto/sha1" //nolint:gosec // md5/sha1 required for artifact-protocol checksums (Maven .md5/.sha1, npm shasum), not security
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/http/httpproxy"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/netguard"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// UpstreamClient is the shared HTTP client used to fetch artifacts from upstream
// remotes on cache miss when no explicit per-repo/global proxy is configured.
// Its dialer is SSRF-guarded (remote_url is user-configured): connections that
// resolve to internal addresses are refused. It honors the standard
// HTTP_PROXY/HTTPS_PROXY/NO_PROXY environment variables via Transport.Proxy;
// for env-configured proxies the guard still applies, so internal proxies must
// be set via per-repo proxy_config or SetGlobalProxy (see proxyclient.go),
// which route through a client that permits the trusted proxy address.
var UpstreamClient = &http.Client{
	Transport: &http.Transport{
		Proxy: envProxyFromRequest,
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: netguard.DialControl,
		}).DialContext,
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

// envProxyFromRequest resolves the proxy for a request from the process
// environment (HTTP_PROXY/HTTPS_PROXY/NO_PROXY), read fresh each call.
func envProxyFromRequest(req *http.Request) (*url.URL, error) {
	return httpproxy.FromEnvironment().ProxyFunc()(req.URL)
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

// DefaultMetadataMaxAge is the freshness window applied to proxied repository
// metadata (indexes, Release/InRelease, packuments, repodata, simple-index
// pages, …) when a repository does not override it via
// proxy_config.metadata_max_age. Immutable artifacts (.deb, .tgz, .jar, blobs
// addressed by digest) are never revalidated — callers pass maxAge == 0 for those.
const DefaultMetadataMaxAge = 10 * time.Minute

// MetadataMaxAge returns the freshness TTL to use for proxied metadata on this
// repository. It reads proxy_config["metadata_max_age"], interpreted as a number
// of seconds (JSON numbers arrive as float64; strings are parsed). Any unset,
// non-positive, or invalid value falls back to DefaultMetadataMaxAge.
//
// Handlers call this for metadata/index paths and pass the result as the maxAge
// argument to ServeGET; for immutable artifact paths they pass 0 instead.
func MetadataMaxAge(repo *domain.Repository) time.Duration {
	if repo == nil || repo.ProxyConfig == nil {
		return DefaultMetadataMaxAge
	}
	raw, ok := repo.ProxyConfig["metadata_max_age"]
	if !ok {
		return DefaultMetadataMaxAge
	}
	var secs float64
	switch v := raw.(type) {
	case float64:
		secs = v
	case float32:
		secs = float64(v)
	case int:
		secs = float64(v)
	case int64:
		secs = float64(v)
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return DefaultMetadataMaxAge
		}
		secs = f
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return DefaultMetadataMaxAge
		}
		secs = f
	default:
		return DefaultMetadataMaxAge
	}
	if secs <= 0 {
		return DefaultMetadataMaxAge
	}
	return time.Duration(secs * float64(time.Second))
}

// cacheFetchStore resolves the physical blob store that holds a cached asset.
func cacheFetchStore(ctx context.Context, d formats.Deps, asset *domain.Asset) storage.BlobStore {
	if asset.BlobStoreID != "" {
		if bsMeta, getErr := d.Blobs.GetByID(ctx, asset.BlobStoreID); getErr == nil && bsMeta != nil {
			return base.PhysicalStore(ctx, d, bsMeta)
		}
	}
	return d.BlobStore
}

// serveCachedAsset streams (or, for HEAD, describes) a cached asset to the client.
// It takes ownership of rc and closes it.
func serveCachedAsset(c *gin.Context, d formats.Deps, asset *domain.Asset, rc io.ReadCloser) {
	defer func() { _ = rc.Close() }()
	// Count only real GETs so a HEAD probe + GET pull don't double-count.
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
		return
	}
	c.DataFromReader(http.StatusOK, asset.SizeBytes, asset.ContentType, rc, nil)
}

// ServeGET serves a cached asset or fetches upstream, streaming to the client
// and persisting to the blob store on success. repo must be TypeProxy.
// upstreamPath, when non-empty, is used only for the upstream URL (e.g. npm scoped metadata);
// the cache key and DB asset path remain repoRelativePath.
//
// maxAge controls metadata freshness. maxAge == 0 means the content is immutable
// (artifacts, blobs addressed by digest): a cache hit is served forever without
// contacting upstream. maxAge > 0 marks the path as mutable metadata (apt
// Release/InRelease/Packages, npm packuments, yum repodata, pypi simple pages, …):
// when the cached copy is older than maxAge it is revalidated against upstream with
// a conditional request before being served. See revalidateAndServe.
func ServeGET(c *gin.Context, d formats.Deps, repo *domain.Repository, repoRelativePath, upstreamPath string,
	coords base.Coords, defaultContentType string, maxAge time.Duration,
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
		rc, _, blobErr := cacheFetchStore(ctx, d, asset).Get(ctx, asset.BlobKey)
		if blobErr == nil {
			// Metadata freshness: a stale cached copy of mutable metadata is
			// revalidated against upstream before serving. Immutable content
			// (maxAge == 0) and HEAD probes always serve straight from cache.
			if maxAge > 0 && c.Request.Method == http.MethodGet && time.Since(asset.LastModified) > maxAge {
				return revalidateAndServe(c, d, repo, asset, repoRelativePath, upJoin, coords, defaultContentType, rc)
			}
			serveCachedAsset(c, d, asset, rc)
			return nil
		}
		// Blob file missing from cache storage (storage path changed or file deleted).
		// Fall through to upstream fetch so the client gets content and the cache is repaired.
	}

	return fetchAndCache(c, d, repo, repoRelativePath, upJoin, coords, defaultContentType)
}

// fetchAndCache handles a cache miss (or a cache entry whose blob went missing):
// it fetches from upstream, forwarding the client's own conditional headers, and
// on success stores the blob and serves it.
func fetchAndCache(c *gin.Context, d formats.Deps, repo *domain.Repository,
	repoRelativePath, upJoin string, coords base.Coords, defaultContentType string,
) error {
	ctx := c.Request.Context()

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

	resp, err := fetchUpstreamWithDockerHubAuth(ctx, ClientFor(repo), upstreamMethod, upstream, baseRemote, upHdr)
	if err != nil {
		dispatchProxyError(d, repo.Name, repoRelativePath, upstream, err)
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

	return storeAndServeResponse(c, d, repo, repoRelativePath, defaultContentType, coords, resp)
}

// revalidateAndServe is invoked when a cached metadata asset is older than its
// TTL. It asks upstream for a fresh copy with a conditional request and:
//   - 304 Not Modified → refresh the asset's freshness timestamp, serve the cache;
//   - 2xx             → replace the cached blob and serve the new copy;
//   - upstream error / other status → serve the stale cache so clients (e.g.
//     `apt update`) keep working, and record a proxy_error for the failure.
//
// It takes ownership of rc (the open cache blob) and always closes it.
func revalidateAndServe(c *gin.Context, d formats.Deps, repo *domain.Repository, asset *domain.Asset,
	repoRelativePath, upJoin string, coords base.Coords, defaultContentType string, rc io.ReadCloser,
) error {
	ctx := c.Request.Context()

	baseRemote, err := RemoteURL(repo)
	if err != nil {
		serveCachedAsset(c, d, asset, rc)
		return nil
	}
	upstream, err := JoinURL(baseRemote, upJoin)
	if err != nil {
		serveCachedAsset(c, d, asset, rc)
		return nil
	}

	upHdr := http.Header{}
	if ac := c.GetHeader("Accept"); ac != "" {
		upHdr.Set("Accept", ac)
	}
	// Conditional revalidation: request a fresh body only if the resource changed
	// since we cached it. We seed If-Modified-Since from the asset's stored
	// timestamp (the moment we last fetched/validated). We deliberately do NOT
	// derive If-None-Match from our SHA256: upstreams don't recognize our content
	// hash as their ETag, and per RFC 7232 If-None-Match would take precedence over
	// If-Modified-Since, forcing a 200 on every request and defeating revalidation.
	if !asset.LastModified.IsZero() {
		upHdr.Set("If-Modified-Since", asset.LastModified.UTC().Format(http.TimeFormat))
	}

	resp, err := fetchUpstreamWithDockerHubAuth(ctx, http.MethodGet, upstream, baseRemote, upHdr)
	if err != nil {
		// Upstream unreachable → serve stale cache so metadata consumers keep working.
		dispatchProxyError(d, repo.Name, repoRelativePath, upstream, err)
		serveCachedAsset(c, d, asset, rc)
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotModified:
		// Upstream confirms our copy is current: refresh freshness and serve cache.
		// Use context.Background so the touch survives request cancellation.
		if d.Assets != nil {
			_ = d.Assets.TouchLastModified(context.Background(), asset.ID)
		}
		serveCachedAsset(c, d, asset, rc)
		return nil
	case resp.StatusCode >= 200 && resp.StatusCode <= 299:
		// Upstream returned a newer copy: replace the cached blob and serve it.
		_ = rc.Close()
		return storeAndServeResponse(c, d, repo, repoRelativePath, defaultContentType, coords, resp)
	default:
		// Any other status (404/410/5xx): don't discard a good cache on a transient
		// upstream hiccup — serve stale and record the anomaly.
		dispatchProxyError(d, repo.Name, repoRelativePath, upstream,
			fmt.Errorf("revalidation returned status %d", resp.StatusCode))
		serveCachedAsset(c, d, asset, rc)
		return nil
	}
}

// storeAndServeResponse streams a successful (2xx) upstream response to the
// client while persisting it to the blob store and registering the DB asset,
// replacing any prior cache entry for repoRelativePath.
func storeAndServeResponse(c *gin.Context, d formats.Deps, repo *domain.Repository,
	repoRelativePath, defaultContentType string, coords base.Coords, resp *http.Response,
) error {
	ctx := c.Request.Context()

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
	// Count a download only for GET. Otherwise a HEAD probe + GET hit would
	// double-count the same pull.
	if regAsset != nil && regAsset.ID != "" && c.Request.Method == http.MethodGet && d.Downloads != nil {
		d.Downloads.Add(regAsset.ID)
	}
	return nil
}

// dispatchProxyError records an upstream fetch/revalidation failure via the
// webhook bus (the package's proxy-error reporting channel), if configured.
func dispatchProxyError(d formats.Deps, repoName, repoRelativePath, upstream string, cause error) {
	if d.Webhooks == nil {
		return
	}
	d.Webhooks.Dispatch(domain.WebhookPayload{
		Event:      domain.EventProxyError,
		Timestamp:  time.Now(),
		Repository: repoName,
		Asset: map[string]any{
			"path":     repoRelativePath,
			"upstream": upstream,
			"error":    cause.Error(),
		},
	})
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
