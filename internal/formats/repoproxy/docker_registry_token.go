package repoproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
)

// tokenLog receives the warnings the Bearer token flow emits. formats.Deps
// carries no logger, so the server installs one with SetLogger at startup.
var tokenLog atomic.Pointer[zap.SugaredLogger]

// SetLogger installs the logger repoproxy warns through when an upstream token
// request fails. nil restores the default, which discards. Intended to be
// called once from server startup, like SetGlobalProxy.
func SetLogger(l logger.Logger) {
	tokenLog.Store(l)
}

func proxyLogger() logger.Logger {
	if l := tokenLog.Load(); l != nil {
		return l
	}
	return zap.NewNop().Sugar()
}

// tokenEndpointError is a non-200 answer from a token realm. It keeps the
// status alone: the body is the upstream's to word, and it goes to a log line.
// withCredentials records whether the request presented remote_username.
type tokenEndpointError struct {
	status          int
	withCredentials bool
}

// credentialsRejected reports a token realm that turned remote_username down.
// That alone is worth one anonymous token request: a public image on GHCR,
// Quay or Harbor still gets an anonymous pull token when the stored
// credentials have gone stale. A network error, a 5xx or a malformed answer
// is not a verdict on the credentials and gets no second request.
func credentialsRejected(err error) bool {
	var se *tokenEndpointError
	if !errors.As(err, &se) || !se.withCredentials {
		return false
	}
	return se.status == http.StatusUnauthorized || se.status == http.StatusForbidden
}

func (e *tokenEndpointError) Error() string {
	return fmt.Sprintf("token endpoint answered %d %s", e.status, http.StatusText(e.status))
}

// warnTokenFailure records why a token request failed before the anonymous
// retry hides it — otherwise a private registry whose token endpoint refuses
// remote_username reads as a bare 401 with nothing in the log, and a public
// pull that only works through the anonymous token never says the stored
// credentials are stale. It names the realm host and status, never the
// realm's query (it carries the scope), the token or the password.
func warnTokenFailure(ctx context.Context, repo *domain.Repository, realm string, err error) {
	var realmHost string
	if u, perr := url.Parse(realm); perr == nil {
		realmHost = u.Host
	}
	var repoName string
	if repo != nil {
		repoName = repo.Name
	}
	var status int
	var withCredentials bool
	var se *tokenEndpointError
	if errors.As(err, &se) {
		status = se.status
		withCredentials = se.withCredentials
	}
	// *url.Error repeats the full token URL; its cause is what went wrong.
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	logger.WithTraceContext(ctx, proxyLogger()).Warnw("upstream token request failed; retrying anonymously",
		"repository", repoName, "realm_host", realmHost, "status", status,
		"with_credentials", withCredentials, "error", err)
}

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

// fetchDockerRegistryToken requests a pull token from realm. repo's
// remote_username goes along under SetUpstreamAuth's rules: only when realm is
// on remote_url's host and the hop is not a downgrade from https. ACR, Harbor,
// GHCR and Quay issue tokens on the registry's own host and refuse an
// anonymous request for a private repository. The realm is the upstream's
// choice of URL, so a realm on any other host — Docker Hub's auth.docker.io,
// GitLab's gitlab.com/jwt/auth — is asked anonymously, which is what a public
// pull needs anyway. A nil repo asks anonymously everywhere.
func fetchDockerRegistryToken(ctx context.Context, repo *domain.Repository, client *http.Client, realm, service, scope string) (string, error) {
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
	SetUpstreamAuth(req, repo)

	resp, err := tokenRedirectClient(client).Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", &tokenEndpointError{
			status:          resp.StatusCode,
			withCredentials: resp.Request.Header.Get("Authorization") != "",
		}
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
// upstream credentials (SetUpstreamAuth). On 401 it takes a Bearer token from
// the registry's own challenge and retries once, so an OCI registry can be
// pulled through the proxy without the client following that challenge
// itself — the challenge names the upstream token realm, and a client talking
// to Nexspence would present the token to the wrong host.
//
// Only a Docker Hub remote, or a repository whose format speaks OCI
// Distribution, follows that challenge. Any other format keeps the 401: a Helm
// index can name an absolute URL, and a Bearer realm on it must not become a
// GET this process makes. The token request carries remote_username only when
// the realm is on remote_url's host (fetchDockerRegistryToken): that is how a
// private ACR, Harbor, GHCR or Quay repository is pulled. A realm elsewhere —
// Docker Hub, GitLab — is asked anonymously. The retry carries the Bearer
// alone, never Basic. An http realm is not fetched when remote_url is https —
// same rule as Basic on a scheme downgrade. Docker Hub is the registry that
// omits realm or service; every other registry must spell the realm out.
//
// A token realm that rejects remote_username (401/403) is asked once more
// without it: stale credentials must not break a public image the realm hands
// an anonymous token for. A private repository gets no usable token that way,
// so the client still sees 401. Every failed token request is logged; when
// none yields a token the request is retried without any Authorization, which
// is what a registry that serves the path anonymously needs, and one that
// does not answers the client 401 again.
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

	tok, errTok := fetchDockerRegistryToken(ctx, repo, client, realm, service, scope)
	if errTok != nil && credentialsRejected(errTok) {
		warnTokenFailure(ctx, repo, realm, errTok)
		// One anonymous attempt. It carries no credentials, so it cannot be
		// rejected as credentials and come round again.
		tok, errTok = fetchDockerRegistryToken(ctx, nil, client, realm, service, scope)
	}
	if errTok != nil {
		warnTokenFailure(ctx, repo, realm, errTok)
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
