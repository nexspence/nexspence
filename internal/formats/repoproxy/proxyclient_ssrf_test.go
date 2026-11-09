package repoproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// `no_proxy` tells the transport to skip the configured proxy and dial the
// upstream directly. That direct dial reaches the user-configured upstream, so
// it belongs behind the SSRF guard like every other direct dial — the fact
// that a proxy is configured says nothing about the host the request ends up
// at. The dial to the proxy itself stays unguarded: it is admin-configured
// and may legitimately sit on an internal address.
func TestBuildProxyClient_NoProxyDirectDial_IsGuarded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	c, err := buildProxyClient(proxySettings{
		httpProxy: "http://127.0.0.1:1", // a proxy that is not there
		noProxy:   "*",                  // every host is dialed directly
	})
	if err != nil {
		t.Fatalf("buildProxyClient: %v", err)
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, upstream.URL, nil)
	resp, err := c.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("a direct dial to an internal upstream must be blocked even when a proxy is configured")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected an SSRF block error, got: %v", err)
	}
}

// The proxy itself is admin-configured and stays reachable, however internal
// it is: the guard covers the upstream, not the way out.
func TestBuildProxyClient_ProxyDialStaysReachable(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer proxy.Close()

	c, err := buildProxyClient(proxySettings{httpProxy: proxy.URL})
	if err != nil {
		t.Fatalf("buildProxyClient: %v", err)
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://upstream.example/whatever", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("expected the configured proxy to be reachable, got: %v", err)
	}
	_ = resp.Body.Close()
}
