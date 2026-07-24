package repoproxy

import (
	"encoding/json"
	"net/http"
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
