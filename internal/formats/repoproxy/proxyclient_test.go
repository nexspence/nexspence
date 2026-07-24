package repoproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

func mkProxyRepo(name string, cfg map[string]any) *domain.Repository {
	pc := map[string]any{"remote_url": "http://upstream.invalid"}
	for k, v := range cfg {
		pc[k] = v
	}
	return &domain.Repository{
		ID:          "repo-" + name,
		Name:        name,
		Type:        domain.TypeProxy,
		ProxyConfig: pc,
	}
}

// (a) No proxy configured → ClientFor returns the shared, SSRF-guarded
// UpstreamClient so existing behavior (and the test swap hook) is preserved.
func TestClientFor_NoProxy_ReturnsUpstreamClient(t *testing.T) {
	repo := mkProxyRepo("noproxy", nil)
	got := ClientFor(repo)
	if got != UpstreamClient {
		t.Fatalf("expected ClientFor to return the shared UpstreamClient when no proxy is configured")
	}
}

// (b) Per-repo HTTP proxy is actually used: a loopback httptest server acting
// as an HTTP proxy must receive the (absolute-form) request.
func TestClientFor_HTTPProxy_IsUsed(t *testing.T) {
	var hits int32
	var gotAbsolute atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Host != "" && r.URL.Scheme != "" {
			gotAbsolute.Store(true)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("via-proxy"))
	}))
	defer proxy.Close()

	repo := mkProxyRepo("httpproxy", map[string]any{"http_proxy": proxy.URL})
	client := ClientFor(repo)
	if client == UpstreamClient {
		t.Fatal("expected a dedicated proxied client, got the shared UpstreamClient")
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/some/artifact.txt", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request through proxy failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("proxy server was never contacted")
	}
	if !gotAbsolute.Load() {
		t.Fatal("proxy did not receive an absolute-form (proxied) request")
	}
}

// (c) SOCKS5 selection builds a dialer-based transport (no HTTP Proxy func).
func TestBuildProxyClient_SOCKS5_BuildsDialer(t *testing.T) {
	c, err := buildProxyClient(proxySettings{socks5Proxy: "127.0.0.1:1080"})
	if err != nil {
		t.Fatalf("buildProxyClient socks5: %v", err)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if tr.DialContext == nil {
		t.Fatal("expected a SOCKS5 DialContext to be set")
	}
	if tr.Proxy != nil {
		t.Fatal("SOCKS5 transport must not also set an HTTP Proxy func")
	}
	// The dialer should try to reach the SOCKS proxy address (which is closed here).
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/", nil)
	resp, err := c.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected dial to closed SOCKS proxy to fail")
	}
}

// (d) Proxy auth credentials are applied to HTTP proxy connections.
func TestClientFor_HTTPProxy_Auth(t *testing.T) {
	var gotAuth atomic.Value
	gotAuth.Store("")
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Proxy-Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()

	repo := mkProxyRepo("authproxy", map[string]any{
		"http_proxy":     proxy.URL,
		"proxy_username": "alice",
		"proxy_password": "s3cret",
	})
	client := ClientFor(repo)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/x", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()

	auth, _ := gotAuth.Load().(string)
	if !strings.HasPrefix(auth, "Basic ") {
		t.Fatalf("expected Basic Proxy-Authorization header, got %q", auth)
	}
}

// Global default applies when a repo has no per-repo proxy_config, and a
// per-repo override wins over the global default.
func TestClientFor_GlobalDefaultAndPerRepoOverride(t *testing.T) {
	t.Cleanup(func() { SetGlobalProxy("", "", "", "", "", "") })

	// No global, no per-repo → shared UpstreamClient.
	if got := ClientFor(mkProxyRepo("g0", nil)); got != UpstreamClient {
		t.Fatal("expected UpstreamClient when nothing configured")
	}

	// Global default set → a repo without its own proxy uses a dedicated client.
	SetGlobalProxy("http://global.proxy:3128", "", "", "", "", "")
	globalClient := ClientFor(mkProxyRepo("g1", nil))
	if globalClient == UpstreamClient {
		t.Fatal("expected a proxied client from the global default")
	}

	// Per-repo override differs from the global one and is its own client.
	perRepo := ClientFor(mkProxyRepo("g2", map[string]any{"http_proxy": "http://repo.proxy:3128"}))
	if perRepo == UpstreamClient || perRepo == globalClient {
		t.Fatal("expected per-repo override to produce a distinct client")
	}

	// The effective settings should reflect the per-repo override, not global.
	s := repoProxySettings(mkProxyRepo("g3", map[string]any{"http_proxy": "http://repo.proxy:3128"}))
	if s.httpProxy != "http://repo.proxy:3128" {
		t.Fatalf("expected per-repo http_proxy to win, got %q", s.httpProxy)
	}
}

// (e) An explicitly-configured internal (loopback) proxy is allowed, while a
// direct (no-proxy) dial to an internal upstream is still SSRF-blocked.
func TestClientFor_InternalProxyAllowed_DirectInternalBlocked(t *testing.T) {
	// Loopback proxy is internal but explicitly configured → must be reachable.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer proxy.Close()

	repoProxied := mkProxyRepo("intproxy", map[string]any{"http_proxy": proxy.URL})
	pc := ClientFor(repoProxied)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/whatever", nil)
	resp, err := pc.Do(req)
	if err != nil {
		t.Fatalf("expected internal proxy to be reachable, got: %v", err)
	}
	_ = resp.Body.Close()

	// No proxy → shared guarded client → dialing an internal upstream is blocked.
	// Use the real guarded UpstreamClient (do NOT swap it here).
	repoDirect := mkProxyRepo("direct", nil)
	dc := ClientFor(repoDirect)
	// Point at a loopback address; the SSRF guard must refuse the direct dial.
	blockedReq, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:9/", nil)
	blockedResp, derr := dc.Do(blockedReq)
	if derr == nil {
		_ = blockedResp.Body.Close()
		t.Fatal("expected direct dial to internal address to be blocked")
	}
	if !strings.Contains(derr.Error(), "blocked") {
		t.Fatalf("expected SSRF block error, got: %v", derr)
	}
}
