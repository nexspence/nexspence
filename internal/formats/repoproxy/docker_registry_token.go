package repoproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// ociBearerChallenge reports a Bearer realm an OCI Distribution repository may
// follow. Docker Hub is decided separately, by host. Helm, Maven and the rest
// do not follow a realm an upstream chose.
func ociBearerChallenge(repo *domain.Repository, parsed bool, realm string) bool {
	return parsed && realm != "" && repo != nil && repo.Format.IsOCIRegistry()
}

// bearerRealmAllowed reports whether the token URL may be fetched for a
// non-Hub registry. An http realm under an https remote_url is the cleartext
// hop SetUpstreamAuth already refuses for Basic.
func bearerRealmAllowed(realm, baseRemote string) bool {
	u, err := url.Parse(realm)
	if err != nil || u.Hostname() == "" {
		return false
	}
	return !httpsBaseBlocksHTTP(u, baseRemote)
}

// isDockerRegistryRemote reports whether remote_url points at Docker Hub registry
// (anonymous pulls require a Bearer token from auth.docker.io).
func isDockerRegistryRemote(baseRemote string) bool {
	u, err := url.Parse(baseRemote)
	if err != nil {
		return false
	}
	h := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	switch h {
	case "registry-1.docker.io", "registry.docker.io", "docker.io":
		return true
	default:
		return false
	}
}

func grabQuotedAttr(s, key string) string {
	needle := key + `="`
	i := strings.Index(s, needle)
	if i < 0 {
		return ""
	}
	start := i + len(needle)
	end := strings.IndexByte(s[start:], '"')
	if end < 0 {
		return ""
	}
	return s[start : start+end]
}

func parseDockerBearerChallenge(h http.Header) (realm, service, scope string, ok bool) {
	for _, raw := range h.Values("WWW-Authenticate") {
		raw = strings.TrimSpace(raw)
		if len(raw) < 7 || !strings.HasPrefix(strings.ToLower(raw), "bearer ") {
			continue
		}
		body := strings.TrimSpace(raw[7:])
		realm = grabQuotedAttr(body, "realm")
		service = grabQuotedAttr(body, "service")
		scope = grabQuotedAttr(body, "scope")
		if realm != "" {
			return realm, service, scope, true
		}
	}
	return "", "", "", false
}

// scopeFromRegistryV2URL builds "repository:<name>:pull" from a registry URL
// path /v2/<name>/manifests/..., /v2/<name>/blobs/... or
// /v2/<name>/referrers/....
//
// referrers belongs here for the same reason the other two do: without a scope
// no Bearer token is fetched and the request is retried anonymously, which Hub
// answers with another 401 — and at the referrers endpoint that reads as an
// upstream refusal for a repository the proxy can otherwise pull.
func scopeFromRegistryV2URL(u *url.URL) string {
	p := u.Path
	if !strings.HasPrefix(p, "/v2/") {
		return ""
	}
	rest := strings.TrimPrefix(p, "/v2/")
	if rest == "" {
		return ""
	}
	parts := strings.Split(rest, "/")
	split := -1
	for i, seg := range parts {
		if seg == "blobs" || seg == "manifests" || seg == "referrers" {
			split = i
			break
		}
	}
	if split <= 0 {
		return ""
	}
	name := strings.ToLower(strings.Join(parts[:split], "/"))
	return "repository:" + name + ":pull"
}

func fetchDockerRegistryToken(ctx context.Context, client *http.Client, realm, service, scope string) (string, error) {
	// Realm and service are the caller's. Docker Hub is the registry that omits
	// them; filling registry.docker.io in here would aim a GHCR challenge that
	// forgot service at Docker Hub's token service.
	if realm == "" {
		return "", fmt.Errorf("empty token realm")
	}
	if scope == "" {
		return "", fmt.Errorf("empty token scope")
	}
	q := url.Values{}
	if service != "" {
		q.Set("service", service)
	}
	q.Set("scope", scope)

	tokenURL := realm
	if strings.Contains(tokenURL, "?") {
		tokenURL = tokenURL + "&" + q.Encode()
	} else {
		tokenURL = tokenURL + "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Nexspence/1.0 (docker-proxy)")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("token endpoint %s: %s", resp.Status, string(b))
	}

	var out struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Token != "" {
		return out.Token, nil
	}
	if out.AccessToken != "" {
		return out.AccessToken, nil
	}
	return "", fmt.Errorf("empty token in response")
}

// fetchUpstreamWithDockerHubAuth performs the HTTP request with the repo's
// upstream credentials (SetUpstreamAuth). On 401 it takes an anonymous Bearer
// token from the registry's own challenge and retries once, so a public OCI
// registry (GHCR, Docker Hub) can be pulled through the proxy without the
// client following that challenge itself — the challenge names the upstream
// token realm, and a client talking to Nexspence would present the token to
// the wrong host.
//
// Only a Docker Hub remote, or a repository whose format speaks OCI
// Distribution, follows that challenge. Any other format keeps the 401: a Helm
// index can name an absolute URL, and a Bearer realm on it must not become a
// GET this process makes. The token request carries no remote_username. An
// http realm is not fetched when remote_url is https — same rule as Basic on
// a scheme downgrade. Docker Hub is the registry that omits realm or service;
// every other registry must spell the realm out.
func fetchUpstreamWithDockerHubAuth(ctx context.Context, repo *domain.Repository, client *http.Client, method, upstreamURL, baseRemote string, hdr http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, upstreamURL, nil)
	if err != nil {
		return nil, err
	}
	if hdr != nil {
		req.Header = hdr.Clone()
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Nexspence/1.0 (proxy)")
	}
	SetUpstreamAuth(req, repo)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	realm, service, scope, ok := parseDockerBearerChallenge(resp.Header)
	hub := isDockerRegistryRemote(baseRemote)
	if !hub && !ociBearerChallenge(repo, ok, realm) {
		return resp, nil
	}

	upu, parseErr := url.Parse(upstreamURL)
	if parseErr != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return nil, parseErr
	}
	// remote_url is https and this hop is http: do not attach a Bearer either.
	// The body stays with the caller, same as the other early returns.
	if httpsBaseBlocksHTTP(upu, baseRemote) {
		return resp, nil
	}
	if scope == "" {
		scope = scopeFromRegistryV2URL(upu)
	}
	if hub {
		if realm == "" {
			realm = "https://auth.docker.io/token"
		}
		if service == "" {
			service = "registry.docker.io"
		}
	} else if !bearerRealmAllowed(realm, baseRemote) {
		return resp, nil
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if scope == "" {
		return redoUpstreamWithoutAuth(ctx, client, method, upstreamURL, hdr)
	}

	tok, errTok := fetchDockerRegistryToken(ctx, client, realm, service, scope)
	if errTok != nil || tok == "" {
		return redoUpstreamWithoutAuth(ctx, client, method, upstreamURL, hdr)
	}

	req2, err := http.NewRequestWithContext(ctx, method, upstreamURL, nil)
	if err != nil {
		return nil, err
	}
	if hdr != nil {
		req2.Header = hdr.Clone()
	}
	if req2.Header.Get("User-Agent") == "" {
		req2.Header.Set("User-Agent", "Nexspence/1.0 (proxy)")
	}
	req2.Header.Set("Authorization", "Bearer "+tok)
	return client.Do(req2)
}

func redoUpstreamWithoutAuth(ctx context.Context, client *http.Client, method, upstreamURL string, hdr http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, upstreamURL, nil)
	if err != nil {
		return nil, err
	}
	if hdr != nil {
		req.Header = hdr.Clone()
	}
	req.Header.Del("Authorization")
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Nexspence/1.0 (proxy)")
	}
	return client.Do(req)
}
