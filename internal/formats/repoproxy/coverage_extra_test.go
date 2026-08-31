package repoproxy

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

func proxyRepoWith(cfg map[string]any) *domain.Repository {
	return &domain.Repository{Name: "p", Type: domain.TypeProxy, Online: true, ProxyConfig: cfg}
}

// MetadataMaxAge parses every supported numeric encoding and falls back to the
// default for missing/invalid/non-positive values.
func TestMetadataMaxAge_Parsing(t *testing.T) {
	cases := []struct {
		name string
		repo *domain.Repository
		want time.Duration
	}{
		{"nil repo", nil, DefaultMetadataMaxAge},
		{"nil config", &domain.Repository{}, DefaultMetadataMaxAge},
		{"missing key", proxyRepoWith(map[string]any{"remote_url": "x"}), DefaultMetadataMaxAge},
		{"float64", proxyRepoWith(map[string]any{"metadata_max_age": float64(30)}), 30 * time.Second},
		{"int", proxyRepoWith(map[string]any{"metadata_max_age": 45}), 45 * time.Second},
		{"int64", proxyRepoWith(map[string]any{"metadata_max_age": int64(60)}), 60 * time.Second},
		{"json.Number", proxyRepoWith(map[string]any{"metadata_max_age": json.Number("90")}), 90 * time.Second},
		{"string", proxyRepoWith(map[string]any{"metadata_max_age": "120"}), 120 * time.Second},
		{"bad json.Number", proxyRepoWith(map[string]any{"metadata_max_age": json.Number("nope")}), DefaultMetadataMaxAge},
		{"bad string", proxyRepoWith(map[string]any{"metadata_max_age": "abc"}), DefaultMetadataMaxAge},
		{"zero", proxyRepoWith(map[string]any{"metadata_max_age": 0}), DefaultMetadataMaxAge},
		{"negative", proxyRepoWith(map[string]any{"metadata_max_age": -5}), DefaultMetadataMaxAge},
		{"wrong type", proxyRepoWith(map[string]any{"metadata_max_age": true}), DefaultMetadataMaxAge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MetadataMaxAge(tc.repo); got != tc.want {
				t.Fatalf("MetadataMaxAge = %v, want %v", got, tc.want)
			}
		})
	}
}

// redirectPolicy allows redirects up to the cap and rejects beyond it.
func TestRedirectPolicy(t *testing.T) {
	if err := redirectPolicy(nil, make([]*http.Request, proxyMaxRedirects-1)); err != nil {
		t.Fatalf("under cap should be allowed: %v", err)
	}
	if err := redirectPolicy(nil, make([]*http.Request, proxyMaxRedirects)); err == nil {
		t.Fatal("at/over cap should be rejected")
	}
}

// hostPort accepts host:port and URL forms and rejects malformed input.
func TestHostPort(t *testing.T) {
	ok := []struct{ in, want string }{
		{"127.0.0.1:8899", "127.0.0.1:8899"},
		{"http://proxy.local:3128", "proxy.local:3128"},
		{"socks5://10.0.0.1:1080", "10.0.0.1:1080"},
		{" 127.0.0.1:1 ", "127.0.0.1:1"},
	}
	for _, tc := range ok {
		got, err := hostPort(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("hostPort(%q) = %q,%v want %q", tc.in, got, err, tc.want)
		}
	}
	bad := []string{"", "no-port", "http://", "://bad"}
	for _, in := range bad {
		if _, err := hostPort(in); err == nil {
			t.Fatalf("hostPort(%q) expected error", in)
		}
	}
}

// buildProxyClient builds a working client for each proxy mode and rejects
// malformed proxy addresses.
func TestBuildProxyClient_Modes(t *testing.T) {
	// HTTP proxy with auth
	c, err := buildProxyClient(proxySettings{httpProxy: "http://127.0.0.1:3128", username: "u", password: "p"})
	if err != nil || c == nil {
		t.Fatalf("http proxy client: %v", err)
	}
	if c.CheckRedirect == nil {
		t.Fatal("client must set redirect policy")
	}
	// SOCKS5 with auth
	if _, err := buildProxyClient(proxySettings{socks5Proxy: "127.0.0.1:1080", username: "u", password: "p"}); err != nil {
		t.Fatalf("socks5 client: %v", err)
	}
	// invalid socks5 address
	if _, err := buildProxyClient(proxySettings{socks5Proxy: "not-a-hostport"}); err == nil {
		t.Fatal("invalid socks5_proxy should error")
	}
	// no_proxy honored (client still builds)
	if _, err := buildProxyClient(proxySettings{httpProxy: "http://127.0.0.1:3128", noProxy: "example.com"}); err != nil {
		t.Fatalf("no_proxy client: %v", err)
	}
}

// A Docker Hub pull needs a Bearer token scoped to the repository, and the scope
// is derived from the request path when the 401 challenge does not carry one. The
// splitter recognized only "blobs" and "manifests", so a referrers request
// yielded an empty scope, no token was fetched, and the request was retried
// anonymously — which Hub answers with another 401. That reads at the referrers
// endpoint as an upstream refusal for a repository the proxy can otherwise pull.
func TestScopeFromRegistryV2URL_Endpoints(t *testing.T) {
	cases := map[string]string{
		"/v2/library/nginx/manifests/latest":     "repository:library/nginx:pull",
		"/v2/library/nginx/blobs/sha256:abc":     "repository:library/nginx:pull",
		"/v2/library/nginx/referrers/sha256:abc": "repository:library/nginx:pull",
		"/v2/nginx/referrers/sha256:abc":         "repository:nginx:pull",
		"/v2/Library/NGINX/referrers/sha256:abc": "repository:library/nginx:pull",
		// No endpoint keyword at all, and the keyword in first position (which
		// would leave an empty repository name): neither yields a scope.
		"/v2/library/nginx/tags/list":            "",
		"/v2/referrers/sha256:abc":               "",
		"/v1/library/nginx/referrers/sha256:abc": "",
	}
	for path, want := range cases {
		u, err := url.Parse("https://registry-1.docker.io" + path)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if got := scopeFromRegistryV2URL(u); got != want {
			t.Errorf("scopeFromRegistryV2URL(%s) = %q, want %q", path, got, want)
		}
	}
}

// MinimumPackageAge: absent/zero/invalid means DISABLED (0) — the policy is
// opt-in, unlike MetadataMaxAge which falls back to a positive default.
func TestMinimumPackageAge_Parsing(t *testing.T) {
	cases := []struct {
		name string
		repo *domain.Repository
		want time.Duration
	}{
		{"nil repo", nil, 0},
		{"nil config", &domain.Repository{}, 0},
		{"missing key", proxyRepoWith(map[string]any{"remote_url": "x"}), 0},
		{"seconds float64", proxyRepoWith(map[string]any{"minimum_package_age": float64(604800)}), 7 * 24 * time.Hour},
		{"seconds int", proxyRepoWith(map[string]any{"minimum_package_age": 86400}), 24 * time.Hour},
		{"seconds string", proxyRepoWith(map[string]any{"minimum_package_age": "3600"}), time.Hour},
		{"json.Number", proxyRepoWith(map[string]any{"minimum_package_age": json.Number("60")}), time.Minute},
		{"zero disables", proxyRepoWith(map[string]any{"minimum_package_age": 0}), 0},
		{"negative disables", proxyRepoWith(map[string]any{"minimum_package_age": -5}), 0},
		{"bad string disables", proxyRepoWith(map[string]any{"minimum_package_age": "week"}), 0},
		{"wrong type disables", proxyRepoWith(map[string]any{"minimum_package_age": true}), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MinimumPackageAge(tc.repo); got != tc.want {
				t.Fatalf("MinimumPackageAge = %v, want %v", got, tc.want)
			}
		})
	}
}

// CargoIndexUpstreamPath: the local /index/ prefix is this codebase's own
// route, not part of any real registry's URL scheme (#347) — index.crates.io
// keys are "se/rd/serde", not "index/se/rd/serde".
func TestCargoIndexUpstreamPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/index/se/rd/serde", "/se/rd/serde"},
		{"/index/2/cc", "/2/cc"},
		{"/index/3/u/url", "/3/u/url"},
		{"/index/config.json", "/config.json"},
		{"/not-index/x", "/not-index/x"},
	}
	for _, tc := range cases {
		if got := CargoIndexUpstreamPath(tc.in); got != tc.want {
			t.Errorf("CargoIndexUpstreamPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// JoinURL passes an absolute upstream URL through untouched: format handlers
// hand one over when the real registry splits its API across hosts (the
// crates.io download endpoint lives on a different host than its sparse
// index). Only handler code ever sets upstreamPath — client paths are
// normalized relative paths — so this is not reachable from request input.
func TestJoinURL_AbsoluteUpstreamPassesThrough(t *testing.T) {
	got, err := JoinURL("https://index.crates.io", "https://crates.io/api/v1/crates/serde/1.0.0/download")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://crates.io/api/v1/crates/serde/1.0.0/download" {
		t.Fatalf("got %q", got)
	}
}
