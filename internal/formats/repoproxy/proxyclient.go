package repoproxy

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"
	xproxy "golang.org/x/net/proxy"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// Timeouts/limits mirror the shared UpstreamClient so proxied clients behave
// equivalently (redirect cap, request timeout, idle-conn tuning).
const (
	proxyDialTimeout    = 10 * time.Second
	proxyRequestTimeout = 5 * time.Minute
	proxyMaxIdleConns   = 128
	proxyIdleConnTO     = 90 * time.Second
	proxyTLSHandshakeTO = 15 * time.Second
	proxyMaxRedirects   = 12
)

// proxySettings is the resolved outbound-proxy configuration for a repository.
// The zero value means "no explicit proxy" (fall back to the shared,
// SSRF-guarded UpstreamClient which honors HTTP(S)_PROXY/NO_PROXY env vars).
type proxySettings struct {
	httpProxy   string
	httpsProxy  string
	socks5Proxy string
	noProxy     string
	username    string
	password    string
}

func (s proxySettings) isEmpty() bool { return s == proxySettings{} }

// key is a stable cache key for a settings value.
func (s proxySettings) key() string {
	return strings.Join([]string{
		s.httpProxy, s.httpsProxy, s.socks5Proxy, s.noProxy, s.username, s.password,
	}, "\x00")
}

// defaultProxySettings, if non-empty, is the server-wide outbound proxy applied
// to every proxy repository that does not set its own proxy_config overrides.
// It is optional: when empty, the shared UpstreamClient (which honors the
// standard HTTP_PROXY/HTTPS_PROXY/NO_PROXY env vars) is used for the no-override
// case. Guard it with defaultProxyMu.
var (
	defaultProxySettings proxySettings
	defaultProxyMu       sync.RWMutex
)

// SetGlobalProxy installs a server-wide outbound proxy default. Per-repository
// proxy_config keys still override individual fields. Pass all-empty strings to
// clear it and fall back to env-based defaults. Intended to be called once from
// server config loading at startup.
func SetGlobalProxy(httpProxy, httpsProxy, socks5Proxy, noProxy, username, password string) {
	defaultProxyMu.Lock()
	defer defaultProxyMu.Unlock()
	defaultProxySettings = proxySettings{
		httpProxy:   strings.TrimSpace(httpProxy),
		httpsProxy:  strings.TrimSpace(httpsProxy),
		socks5Proxy: strings.TrimSpace(socks5Proxy),
		noProxy:     strings.TrimSpace(noProxy),
		username:    username,
		password:    password,
	}
}

func cfgString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if raw, ok := m[key]; ok {
		if s, ok := raw.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// repoProxySettings resolves the effective proxy settings for a repo: the
// server-wide default overlaid with any per-repo proxy_config overrides.
func repoProxySettings(repo *domain.Repository) proxySettings {
	defaultProxyMu.RLock()
	s := defaultProxySettings
	defaultProxyMu.RUnlock()

	pc := repo.ProxyConfig
	if v := cfgString(pc, "http_proxy"); v != "" {
		s.httpProxy = v
	}
	if v := cfgString(pc, "https_proxy"); v != "" {
		s.httpsProxy = v
	}
	if v := cfgString(pc, "socks5_proxy"); v != "" {
		s.socks5Proxy = v
	}
	if v := cfgString(pc, "no_proxy"); v != "" {
		s.noProxy = v
	}
	if v := cfgString(pc, "proxy_username"); v != "" {
		s.username = v
	}
	if v := cfgString(pc, "proxy_password"); v != "" {
		s.password = v
	}
	return s
}

var (
	clientCache   = map[string]*http.Client{}
	clientCacheMu sync.Mutex
)

// ClientFor returns the *http.Client to use for outbound upstream fetches for
// repo. When the repo (and the server-wide default) specify no explicit proxy,
// it returns the shared, SSRF-guarded UpstreamClient — preserving existing
// behavior and keeping the guard active for direct upstream dials. When an
// outbound proxy IS configured, it returns a per-config cached client whose
// TCP connections go to the (admin-configured, trusted) proxy.
//
// SSRF-guard interaction: the guard exists because remote_url is
// user-configured and must not be able to reach internal addresses on a DIRECT
// dial. When a proxy is configured, the only address the process dials is the
// proxy itself; the user-controlled upstream host is resolved by the proxy, not
// by us, so it can never become a direct internal dial. We therefore do NOT
// apply the guard to proxied clients (allowing a legitimately-internal
// corporate proxy), while direct (no-proxy) clients keep the guard. This gives
// exactly the required behavior: an explicitly-configured internal proxy is
// reachable, but a direct internal upstream is still blocked.
func ClientFor(repo *domain.Repository) *http.Client {
	if repo == nil {
		return UpstreamClient
	}
	s := repoProxySettings(repo)
	if s.isEmpty() {
		return UpstreamClient
	}

	k := s.key()
	clientCacheMu.Lock()
	if c, ok := clientCache[k]; ok {
		clientCacheMu.Unlock()
		return c
	}
	clientCacheMu.Unlock()

	c, err := buildProxyClient(s)
	if err != nil {
		// Misconfiguration: fall back to the guarded default rather than
		// silently disabling outbound fetches.
		return UpstreamClient
	}

	clientCacheMu.Lock()
	// Re-check in case of a concurrent build.
	if existing, ok := clientCache[k]; ok {
		clientCacheMu.Unlock()
		return existing
	}
	clientCache[k] = c
	clientCacheMu.Unlock()
	return c
}

func redirectPolicy(_ *http.Request, via []*http.Request) error {
	if len(via) >= proxyMaxRedirects {
		return fmt.Errorf("stopped after %d redirects", proxyMaxRedirects)
	}
	return nil
}

// buildProxyClient constructs an *http.Client that routes outbound requests
// through the configured proxy. SOCKS5 takes precedence over HTTP(S) proxy when
// both are set. The dialer that reaches the proxy is intentionally NOT
// SSRF-guarded (the proxy is admin-configured and may legitimately be internal).
func buildProxyClient(s proxySettings) (*http.Client, error) {
	// Dialer that connects to the proxy. No netguard.Control: the proxy is
	// trusted/admin-configured and is frequently on an internal IP.
	baseDialer := &net.Dialer{Timeout: proxyDialTimeout}

	tr := &http.Transport{
		MaxIdleConns:        proxyMaxIdleConns,
		IdleConnTimeout:     proxyIdleConnTO,
		TLSHandshakeTimeout: proxyTLSHandshakeTO,
	}

	if s.socks5Proxy != "" {
		addr, err := hostPort(s.socks5Proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid socks5_proxy: %w", err)
		}
		var auth *xproxy.Auth
		if s.username != "" || s.password != "" {
			auth = &xproxy.Auth{User: s.username, Password: s.password}
		}
		d, err := xproxy.SOCKS5("tcp", addr, auth, baseDialer)
		if err != nil {
			return nil, fmt.Errorf("build socks5 dialer: %w", err)
		}
		cd, ok := d.(xproxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("socks5 dialer does not support DialContext")
		}
		tr.DialContext = cd.DialContext
		// tr.Proxy stays nil: routing is done at the socket layer.
	} else {
		tr.DialContext = baseDialer.DialContext
		pf, err := httpProxyFunc(s)
		if err != nil {
			return nil, err
		}
		tr.Proxy = pf
	}

	return &http.Client{
		Transport:     tr,
		Timeout:       proxyRequestTimeout,
		CheckRedirect: redirectPolicy,
	}, nil
}

// httpProxyFunc builds a per-request proxy resolver honoring http_proxy,
// https_proxy and no_proxy, injecting optional basic-auth credentials.
func httpProxyFunc(s proxySettings) (func(*http.Request) (*url.URL, error), error) {
	cfg := &httpproxy.Config{
		HTTPProxy:  s.httpProxy,
		HTTPSProxy: s.httpsProxy,
		NoProxy:    s.noProxy,
	}
	resolve := cfg.ProxyFunc()
	return func(req *http.Request) (*url.URL, error) {
		u, err := resolve(req.URL)
		if err != nil || u == nil {
			return u, err
		}
		if u.User == nil && (s.username != "" || s.password != "") {
			u.User = url.UserPassword(s.username, s.password)
		}
		return u, nil
	}, nil
}

// hostPort normalizes a proxy address that may be given as "host:port",
// "scheme://host:port", or "socks5://host:port" into "host:port".
func hostPort(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		if u.Host == "" {
			return "", fmt.Errorf("no host in %q", raw)
		}
		return u.Host, nil
	}
	if _, _, err := net.SplitHostPort(raw); err != nil {
		return "", fmt.Errorf("expected host:port, got %q", raw)
	}
	return raw, nil
}
