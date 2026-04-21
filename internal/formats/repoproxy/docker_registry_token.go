package repoproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

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

// scopeFromRegistryV2URL builds "repository:<name>:pull" from a registry URL path /v2/<name>/manifests/... or /v2/<name>/blobs/...
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
		if seg == "blobs" || seg == "manifests" {
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

func fetchDockerRegistryToken(ctx context.Context, realm, service, scope string) (string, error) {
	if realm == "" {
		realm = "https://auth.docker.io/token"
	}
	if service == "" {
		service = "registry.docker.io"
	}
	if scope == "" {
		return "", fmt.Errorf("empty token scope")
	}
	q := url.Values{}
	q.Set("service", service)
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

	resp, err := UpstreamClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

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

// fetchUpstreamWithDockerHubAuth performs the HTTP request; on 401 from Docker Hub it obtains
// an anonymous Bearer token and retries once. This prevents the registry client from
// following Hub's WWW-Authenticate challenge with Nexspence credentials (wrong host).
func fetchUpstreamWithDockerHubAuth(ctx context.Context, method, upstreamURL, baseRemote string, hdr http.Header) (*http.Response, error) {
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

	resp, err := UpstreamClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized || !isDockerRegistryRemote(baseRemote) {
		return resp, nil
	}

	realm, service, scope, ok := parseDockerBearerChallenge(resp.Header)
	upu, parseErr := url.Parse(upstreamURL)
	if parseErr != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return nil, parseErr
	}
	if scope == "" {
		scope = scopeFromRegistryV2URL(upu)
	}
	if !ok || realm == "" {
		realm = "https://auth.docker.io/token"
	}
	if service == "" {
		service = "registry.docker.io"
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if scope == "" {
		return redoUpstreamWithoutAuth(ctx, method, upstreamURL, hdr)
	}

	tok, errTok := fetchDockerRegistryToken(ctx, realm, service, scope)
	if errTok != nil || tok == "" {
		return redoUpstreamWithoutAuth(ctx, method, upstreamURL, hdr)
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
	return UpstreamClient.Do(req2)
}

func redoUpstreamWithoutAuth(ctx context.Context, method, upstreamURL string, hdr http.Header) (*http.Response, error) {
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
	return UpstreamClient.Do(req)
}
