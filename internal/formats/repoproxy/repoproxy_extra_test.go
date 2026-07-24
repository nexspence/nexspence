package repoproxy_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ── stubDispatcher ───────────────────────────────────────────────────────────

type stubDispatcher struct {
	events []domain.WebhookPayload
}

func (s *stubDispatcher) Dispatch(p domain.WebhookPayload) {
	s.events = append(s.events, p)
}

// ── helper ───────────────────────────────────────────────────────────────────

func makeDeps(repo *domain.Repository) formats.Deps {
	return formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
	}
}

// ── RejectMutation edge cases ────────────────────────────────────────────────

func TestRejectMutation_NilRepo(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/", nil)
	assert.False(t, repoproxy.RejectMutation(c, nil))
}

func TestRejectMutation_ProxyGET(t *testing.T) {
	repo := proxyRepo("p", "https://example.com")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	assert.False(t, repoproxy.RejectMutation(c, repo))
}

func TestRejectMutation_ProxyPOST(t *testing.T) {
	repo := proxyRepo("p", "https://example.com")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	assert.True(t, repoproxy.RejectMutation(c, repo))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestRejectMutation_ProxyPATCH(t *testing.T) {
	repo := proxyRepo("p", "https://example.com")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPatch, "/", nil)
	assert.True(t, repoproxy.RejectMutation(c, repo))
}

func TestRejectMutation_ProxyDELETE(t *testing.T) {
	repo := proxyRepo("p", "https://example.com")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/", nil)
	assert.True(t, repoproxy.RejectMutation(c, repo))
}

// ── RemoteURL ────────────────────────────────────────────────────────────────

func TestRemoteURL_NilProxyConfig(t *testing.T) {
	repo := &domain.Repository{Type: domain.TypeProxy, ProxyConfig: nil}
	_, err := repoproxy.RemoteURL(repo)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote_url")
}

func TestRemoteURL_MissingKey(t *testing.T) {
	repo := &domain.Repository{Type: domain.TypeProxy, ProxyConfig: map[string]any{}}
	_, err := repoproxy.RemoteURL(repo)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote_url")
}

func TestRemoteURL_EmptyString(t *testing.T) {
	repo := &domain.Repository{
		Type:        domain.TypeProxy,
		ProxyConfig: map[string]any{"remote_url": "   "},
	}
	_, err := repoproxy.RemoteURL(repo)
	require.Error(t, err)
}

func TestRemoteURL_NonStringValue(t *testing.T) {
	repo := &domain.Repository{
		Type:        domain.TypeProxy,
		ProxyConfig: map[string]any{"remote_url": 12345},
	}
	_, err := repoproxy.RemoteURL(repo)
	require.Error(t, err)
}

func TestRemoteURL_Valid_StripsTrailingSlash(t *testing.T) {
	repo := &domain.Repository{
		Type:        domain.TypeProxy,
		ProxyConfig: map[string]any{"remote_url": "https://repo.example.com/"},
	}
	got, err := repoproxy.RemoteURL(repo)
	require.NoError(t, err)
	assert.Equal(t, "https://repo.example.com", got)
}

// ── JoinURL edge cases ────────────────────────────────────────────────────────

func TestJoinURL_InvalidBase(t *testing.T) {
	_, err := repoproxy.JoinURL("://bad-url", "/path")
	require.Error(t, err)
}

// ── NPMMetadataPath ──────────────────────────────────────────────────────────

func TestNPMMetadataPath_Simple(t *testing.T) {
	assert.Equal(t, "lodash", repoproxy.NPMMetadataPath("lodash"))
	assert.Equal(t, "lodash", repoproxy.NPMMetadataPath("/lodash"))
	assert.Equal(t, "lodash", repoproxy.NPMMetadataPath("/lodash/"))
}

func TestNPMMetadataPath_Scoped(t *testing.T) {
	assert.Equal(t, "@babel%2Fcore", repoproxy.NPMMetadataPath("@babel/core"))
	assert.Equal(t, "@types%2Fnode", repoproxy.NPMMetadataPath("/@types/node"))
}

func TestNPMMetadataPath_ScopedNoSlash(t *testing.T) {
	// @scope with no slash — returned as-is (no slash found after @)
	assert.Equal(t, "@noscope", repoproxy.NPMMetadataPath("@noscope"))
}

// ── ServeGET error paths ─────────────────────────────────────────────────────

func TestServeGET_NotProxy(t *testing.T) {
	repo := &domain.Repository{
		ID: "h1", Name: "hosted1", Format: "raw", Type: domain.TypeHosted,
	}
	d := makeDeps(repo)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/file", nil)
	err := repoproxy.ServeGET(c, d, repo, "/file", "", base.Coords{}, "", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a proxy")
}

func TestServeGET_UnsupportedMethod(t *testing.T) {
	repo := proxyRepo("p", "https://example.com")
	d := makeDeps(repo)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/file", nil)
	err := repoproxy.ServeGET(c, d, repo, "/file", "", base.Coords{}, "", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported method")
}

func TestServeGET_MissingRemoteURL(t *testing.T) {
	repo := &domain.Repository{
		ID: "p1", Name: "pnorurl", Format: "raw", Type: domain.TypeProxy,
		ProxyConfig: nil,
	}
	d := makeDeps(repo)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/file", nil)
	err := repoproxy.ServeGET(c, d, repo, "/file", "", base.Coords{}, "", 0)
	require.NoError(t, err) // handled internally with 400
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestServeGET_Upstream404(t *testing.T) {
	useUnguardedUpstream(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, "not found")
	}))
	defer upstream.Close()

	repo := proxyRepo("p404", upstream.URL)
	d := makeDeps(repo)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/missing.jar", nil)
	err := repoproxy.ServeGET(c, d, repo, "/missing.jar", "", base.Coords{}, "", 0)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestServeGET_UpstreamError(t *testing.T) {
	// Use a server that immediately closes the connection.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Force a TCP-level rejection by hijacking and closing.
		h, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, _, _ := h.Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	repo := proxyRepo("perr", srv.URL)
	d := makeDeps(repo)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/file.bin", nil)
	err := repoproxy.ServeGET(c, d, repo, "/file.bin", "", base.Coords{}, "", 0)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestServeGET_UpstreamError_FiresWebhook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, _, _ := h.Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	repo := proxyRepo("pwh", srv.URL)
	disp := &stubDispatcher{}
	d := makeDeps(repo)
	d.Webhooks = disp

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/file.bin", nil)
	_ = repoproxy.ServeGET(c, d, repo, "/file.bin", "", base.Coords{}, "", 0)

	// Give the goroutine a moment — Dispatch is synchronous in stubDispatcher but
	// ServeGET calls it directly (not in a goroutine), so no wait needed.
	assert.Equal(t, http.StatusBadGateway, w.Code)
	// Webhook may be fired after the handler returns (async dispatch in production);
	// stubDispatcher is synchronous, so it should be populated immediately.
	require.Len(t, disp.events, 1)
	assert.Equal(t, domain.EventProxyError, disp.events[0].Event)
}

func TestServeGET_UpstreamNotModified(t *testing.T) {
	useUnguardedUpstream(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer upstream.Close()

	repo := proxyRepo("pnm", upstream.URL)
	d := makeDeps(repo)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/file", nil)
	req.Header.Set("If-None-Match", `"abc123"`)
	c.Request = req
	err := repoproxy.ServeGET(c, d, repo, "/file", "", base.Coords{}, "", 0)
	require.NoError(t, err)
	// gin's c.Status() flushes the code only on first write; 304 has no body so we
	// check via the writer's status (gin exposes it on the response writer).
	assert.Equal(t, http.StatusNotModified, c.Writer.Status())
}

func TestServeGET_HEAD_CacheHit(t *testing.T) {
	repo := proxyRepo("headcache", "https://upstream.example.com")
	blobStore := testutil.NewBlobStore()
	assets := testutil.NewAssetRepo()
	content := "some cached data"
	key := "head-cache-key"
	_ = blobStore.Put(context.TODO(), key, strings.NewReader(content), int64(len(content)))
	a := &domain.Asset{
		Repository:   repo.Name,
		RepositoryID: repo.ID,
		Path:         "/data.bin",
		BlobKey:      key,
		ContentType:  "application/octet-stream",
		SizeBytes:    int64(len(content)),
	}
	_ = assets.Create(context.TODO(), a)

	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     assets,
		BlobStore:  blobStore,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodHead, "/data.bin", nil)
	err := repoproxy.ServeGET(c, d, repo, "/data.bin", "", base.Coords{}, "application/octet-stream", 0)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "", w.Body.String()) // HEAD has no body
	assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))
}

func TestServeGET_HEAD_CacheMiss_FetchesUpstream(t *testing.T) {
	useUnguardedUpstream(t)
	const body = "upstream content for head"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, body)
	}))
	defer upstream.Close()

	repo := proxyRepo("headmiss", upstream.URL)
	d := makeDeps(repo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodHead, "/headfile.txt", nil)
	err := repoproxy.ServeGET(c, d, repo, "/headfile.txt", "", base.Coords{Name: "headfile.txt"}, "text/plain", 0)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestServeGET_DefaultContentType(t *testing.T) {
	useUnguardedUpstream(t)
	// Upstream returns no Content-Type; defaultContentType should be used.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Explicitly clear Content-Type so Go doesn't auto-detect
		w.Header()["Content-Type"] = nil
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "raw bytes")
	}))
	defer upstream.Close()

	repo := proxyRepo("pdefct", upstream.URL)
	d := makeDeps(repo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/data", nil)
	err := repoproxy.ServeGET(c, d, repo, "/data", "", base.Coords{Name: "data"}, "application/octet-stream", 0)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))
}

func TestServeGET_UpstreamPathOverride(t *testing.T) {
	useUnguardedUpstream(t)
	// When upstreamPath is set, that path should be fetched but cache key uses repoRelativePath.
	const body = "scoped npm meta"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Should be called with the upstream path, not the repo-relative path.
		assert.Equal(t, "/@babel%2Fcore", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, body)
	}))
	defer upstream.Close()

	repo := proxyRepo("npmp", upstream.URL)
	d := makeDeps(repo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/@babel/core", nil)
	err := repoproxy.ServeGET(c, d, repo, "/@babel/core", "/@babel%2Fcore", base.Coords{Name: "@babel/core"}, "application/json", 0)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, body, w.Body.String())
}

func TestServeGET_Accept_Header_Forwarded(t *testing.T) {
	useUnguardedUpstream(t)
	var capturedAccept string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAccept = r.Header.Get("Accept")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	repo := proxyRepo("pacct", upstream.URL)
	d := makeDeps(repo)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/file", nil)
	req.Header.Set("Accept", "application/vnd.npm.install-v1+json")
	c.Request = req
	err := repoproxy.ServeGET(c, d, repo, "/file", "", base.Coords{Name: "file"}, "", 0)
	require.NoError(t, err)
	assert.Equal(t, "application/vnd.npm.install-v1+json", capturedAccept)
}

// ── docker_registry_token.go — unit tests ────────────────────────────────────

// fetchUpstreamWithDockerHubAuth is not exported; we drive it indirectly via
// ServeGET with a fake upstream that mimics Docker Hub 401 → token → 200 flow.

func TestServeGET_DockerHubLike_401_Token_200(t *testing.T) {
	// Phase 1: serve a 401 with WWW-Authenticate Bearer challenge pointing to the token server.
	// Phase 2 (token server): return a valid token JSON.
	// Phase 3: second request with Authorization: Bearer <token> → 200.
	calls := 0

	// Token server: must be started separately.
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope := r.URL.Query().Get("scope")
		assert.NotEmpty(t, scope)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "test-bearer-token"})
	}))
	defer tokenServer.Close()

	// Upstream Docker registry.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") == "" {
			// First call: challenge with Bearer realm pointing to our token server.
			challenge := fmt.Sprintf(
				`Bearer realm="%s/token",service="registry.test.io",scope="repository:library/alpine:pull"`,
				tokenServer.URL,
			)
			w.Header().Set("WWW-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Second call: authorized → return 200.
		assert.Equal(t, "Bearer test-bearer-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"schemaVersion":2}`)
	}))
	defer upstream.Close()

	// Build a repo whose remote_url looks like Docker Hub so isDockerRegistryRemote is true.
	// We point remote_url to our upstream but override UpstreamClient to avoid real DNS.
	// Since we can't easily override isDockerRegistryRemote without touching source, we build
	// an approach: we use a custom transport that maps "registry-1.docker.io" → upstream.
	origClient := repoproxy.UpstreamClient

	transport := &proxyRoundTripper{
		upstreamHost: upstream.URL,
		tokenHost:    tokenServer.URL,
	}
	repoproxy.UpstreamClient = &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
	}
	defer func() { repoproxy.UpstreamClient = origClient }()

	// Repo with remote_url = Docker Hub (so isDockerRegistryRemote returns true).
	repo := proxyRepo("dockerhub", "https://registry-1.docker.io")
	d := makeDeps(repo)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v2/library/alpine/manifests/latest", nil)
	err := repoproxy.ServeGET(c, d, repo, "/v2/library/alpine/manifests/latest", "", base.Coords{Name: "alpine"}, "application/json", 0)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.GreaterOrEqual(t, calls, 2, "should have made at least 2 upstream calls (401 + retry)")
}

// proxyRoundTripper redirects requests with Docker Hub host to the fake upstream server,
// and token server requests to the fake token server.
type proxyRoundTripper struct {
	upstreamHost string
	tokenHost    string
}

func (p *proxyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Redirect Docker Hub requests to fake upstream.
	if strings.Contains(req.URL.Host, "docker.io") {
		newURL := *req.URL
		newURL.Scheme = "http"
		newURL.Host = strings.TrimPrefix(p.upstreamHost, "http://")
		cloned := req.Clone(req.Context())
		cloned.URL = &newURL
		return http.DefaultTransport.RoundTrip(cloned)
	}
	// Token server requests pass through as-is (they already point at tokenHost).
	return http.DefaultTransport.RoundTrip(req)
}

// ── isDockerRegistryRemote / parseDockerBearerChallenge (internal) ───────────
// These are exercised via fetchUpstreamWithDockerHubAuth which is driven through
// ServeGET above.  We add direct unit tests for the exported behavior that is
// visible from the package boundary.

// TestServeGET_DockerHub_NoScope_RedoWithoutAuth: when upstream returns 401 but
// the WWW-Authenticate has no scope and the URL has no /v2/ scope, it should
// fall back to a second plain request.
func TestServeGET_DockerHub_NoScopeRedoWithoutAuth(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			// No scope in WWW-Authenticate, non-/v2/ URL so scopeFromRegistryV2URL also empty.
			w.Header().Set("WWW-Authenticate", `Bearer realm="https://auth.docker.io/token",service="registry.docker.io"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Second attempt without auth.
		assert.Empty(t, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer upstream.Close()

	origClient := repoproxy.UpstreamClient
	transport := &dockerHubTransport{upstream: upstream}
	repoproxy.UpstreamClient = &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
	}
	defer func() { repoproxy.UpstreamClient = origClient }()

	repo := proxyRepo("dockernoscope", "https://registry-1.docker.io")
	d := makeDeps(repo)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// Use a non-/v2/ path so scopeFromRegistryV2URL returns "".
	c.Request = httptest.NewRequest(http.MethodGet, "/ping", nil)
	err := repoproxy.ServeGET(c, d, repo, "/ping", "", base.Coords{Name: "ping"}, "", 0)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 2, calls)
}

// dockerHubTransport routes "registry-1.docker.io" requests to a local test server.
type dockerHubTransport struct {
	upstream *httptest.Server
}

func (d *dockerHubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Host, "docker.io") {
		newURL := *req.URL
		newURL.Scheme = "http"
		newURL.Host = strings.TrimPrefix(d.upstream.URL, "http://")
		cloned := req.Clone(req.Context())
		cloned.URL = &newURL
		return http.DefaultTransport.RoundTrip(cloned)
	}
	return http.DefaultTransport.RoundTrip(req)
}

// ── applyChecksumHeaders (via cache hit) ─────────────────────────────────────

func TestServeGET_CacheHit_WithChecksums(t *testing.T) {
	repo := proxyRepo("checksum-proxy", "https://upstream.example.com")
	blobStore := testutil.NewBlobStore()
	assets := testutil.NewAssetRepo()
	content := "checksum test content"
	key := "checksum-key"
	_ = blobStore.Put(context.TODO(), key, strings.NewReader(content), int64(len(content)))
	a := &domain.Asset{
		Repository:   repo.Name,
		RepositoryID: repo.ID,
		Path:         "/checksummed.jar",
		BlobKey:      key,
		ContentType:  "application/java-archive",
		SizeBytes:    int64(len(content)),
		SHA256:       "abc123sha256",
		SHA1:         "abc123sha1",
		MD5:          "abc123md5",
	}
	_ = assets.Create(context.TODO(), a)

	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     assets,
		BlobStore:  blobStore,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/checksummed.jar", nil)
	err := repoproxy.ServeGET(c, d, repo, "/checksummed.jar", "", base.Coords{}, "application/java-archive", 0)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "abc123sha256", w.Header().Get("X-Checksum-SHA256"))
	assert.Equal(t, "abc123sha1", w.Header().Get("X-Checksum-SHA1"))
	assert.Equal(t, "abc123md5", w.Header().Get("X-Checksum-MD5"))
	assert.Equal(t, `"abc123sha256"`, w.Header().Get("ETag"))
}

// TestServeGET_DockerHub_NoScopeInChallenge_V2URL_DerivesScopeFromURL tests that when
// the 401 WWW-Authenticate has no scope but the URL is a /v2/ path, scopeFromRegistryV2URL
// is used to derive the scope for token fetching.
func TestServeGET_DockerHub_ScopeFromV2URL(t *testing.T) {
	tokenServerCalled := false
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenServerCalled = true
		scope := r.URL.Query().Get("scope")
		// scopeFromRegistryV2URL should produce "repository:library/nginx:pull"
		assert.Equal(t, "repository:library/nginx:pull", scope)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "nginx-token"})
	}))
	defer tokenServer.Close()

	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			// 401 with no scope in WWW-Authenticate, but URL is /v2/library/nginx/manifests/latest
			// so scopeFromRegistryV2URL should provide the scope.
			challenge := fmt.Sprintf(`Bearer realm="%s/token",service="registry.docker.io"`, tokenServer.URL)
			w.Header().Set("WWW-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		assert.Equal(t, "Bearer nginx-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"schemaVersion":2}`)
	}))
	defer upstream.Close()

	origClient := repoproxy.UpstreamClient
	repoproxy.UpstreamClient = &http.Client{
		Timeout:   5 * time.Second,
		Transport: &dockerHubTransport{upstream: upstream},
	}
	defer func() { repoproxy.UpstreamClient = origClient }()

	repo := proxyRepo("dockerscoped", "https://registry-1.docker.io")
	d := makeDeps(repo)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v2/library/nginx/manifests/latest", nil)
	err := repoproxy.ServeGET(c, d, repo, "/v2/library/nginx/manifests/latest", "", base.Coords{Name: "nginx"}, "application/json", 0)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, tokenServerCalled, "token server should have been called with scope from URL")
	assert.Equal(t, 2, calls)
}
