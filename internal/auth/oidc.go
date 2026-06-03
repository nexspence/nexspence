package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/nexspence-oss/nexspence/internal/config"
)

// ErrOIDCVerification is returned by OIDCService.ExchangeAndVerify for any
// validation failure (bad sig, wrong aud, expired, nonce mismatch). Callers
// should NOT distinguish sub-causes to clients — log server-side, return 401.
var ErrOIDCVerification = errors.New("oidc verification failed")

// OIDCClaims is the normalized user info extracted from a validated id_token.
// Field population is driven by OIDCConfig's *Claim settings.
type OIDCClaims struct {
	Subject   string
	Username  string
	Email     string
	Name      string
	FirstName string
	LastName  string
	Groups    []string
	Raw       map[string]any
}

// OIDCAuthenticator is the interface for OIDC operations (enables mocking).
type OIDCAuthenticator interface {
	AuthCodeURL(state, nonce, codeChallenge string) string
	// ExchangeAndVerify now also returns the raw id_token string (needed for SLO).
	ExchangeAndVerify(ctx context.Context, code, codeVerifier, expectedNonce string) (*OIDCClaims, string, error)
	TestConnection(ctx context.Context) error
	// EndSessionEndpoint returns the IdP's end_session_endpoint URL from OIDC
	// discovery metadata, or "" if the IdP does not publish it.
	EndSessionEndpoint() string
}

// OIDCService implements OIDCAuthenticator against a real IdP using go-oidc.
type OIDCService struct {
	cfg      config.OIDCConfig
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
}

// NewOIDCService performs OIDC discovery against cfg.Issuer and prepares
// the oauth2.Config + id_token verifier. Returns an error if discovery fails
// — callers (main.go) should fail startup so misconfig is loud, not lazy.
//
// When cfg.PublicIssuerURL is set (split-horizon Docker setup), discovery uses
// the internal Issuer URL but the resulting provider is constructed via
// ProviderConfig to bypass go-oidc's issuer-equality check. The verifier uses
// SkipIssuerCheck because the token's iss claim will contain the public URL
// while we discovered via the internal URL.
func NewOIDCService(ctx context.Context, cfg config.OIDCConfig) (*OIDCService, error) {
	var (
		provider        *oidc.Provider
		skipIssuerCheck bool
	)
	if cfg.PublicIssuerURL != "" {
		var err error
		provider, err = newProviderViaConfig(ctx, cfg.Issuer)
		if err != nil {
			return nil, fmt.Errorf("oidc discovery: %w", err)
		}
		skipIssuerCheck = true
	} else {
		var err error
		provider, err = oidc.NewProvider(ctx, cfg.Issuer)
		if err != nil {
			return nil, fmt.Errorf("oidc discovery: %w", err)
		}
	}
	verifier := provider.Verifier(&oidc.Config{
		ClientID:        cfg.ClientID,
		SkipIssuerCheck: skipIssuerCheck,
	})
	return &OIDCService{
		cfg:      cfg,
		provider: provider,
		verifier: verifier,
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       cfg.Scopes,
		},
	}, nil
}

// newProviderViaConfig fetches OIDC discovery manually and constructs a Provider
// via ProviderConfig, bypassing go-oidc's strict issuer-URL equality check.
// This is necessary for split-horizon setups where the internal discovery URL
// (e.g. keycloak:8080) differs from the issuer in the discovery doc
// (e.g. localhost:8180). Endpoint URLs are rewritten from the public issuer
// to the internal issuer so token exchange stays on the internal network.
func newProviderViaConfig(ctx context.Context, internalIssuer string) (*oidc.Provider, error) {
	wellKnown := strings.TrimSuffix(internalIssuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch discovery doc: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var doc struct {
		Issuer   string `json:"issuer"`
		AuthURL  string `json:"authorization_endpoint"`
		TokenURL string `json:"token_endpoint"`
		JWKSURL  string `json:"jwks_uri"`
		UserInfo string `json:"userinfo_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode discovery doc: %w", err)
	}

	// Rewrite the public hostname back to the internal hostname in all endpoints
	// so nexspence calls token/JWKS endpoints via the internal Docker network.
	rewrite := func(u string) string {
		return strings.Replace(u, doc.Issuer, internalIssuer, 1)
	}

	p := oidc.ProviderConfig{
		IssuerURL:   internalIssuer,
		AuthURL:     rewrite(doc.AuthURL),
		TokenURL:    rewrite(doc.TokenURL),
		UserInfoURL: rewrite(doc.UserInfo),
		JWKSURL:     rewrite(doc.JWKSURL),
	}
	return p.NewProvider(ctx), nil
}

// publicURL rewrites an internal IdP URL to the browser-accessible public URL
// when cfg.PublicIssuerURL is set. This solves the Docker split-horizon problem:
// discovery uses the internal hostname, but the browser must reach the public one.
func (s *OIDCService) publicURL(u string) string {
	if s.cfg.PublicIssuerURL == "" || s.cfg.Issuer == "" {
		return u
	}
	return strings.Replace(u, s.cfg.Issuer, s.cfg.PublicIssuerURL, 1)
}

// AuthCodeURL returns the URL to redirect the browser to for the authorization
// code flow. state/nonce/codeChallenge are generated by the caller.
func (s *OIDCService) AuthCodeURL(state, nonce, codeChallenge string) string {
	raw := s.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	return s.publicURL(raw)
}

// ExchangeAndVerify exchanges the authorization code for tokens, validates
// the id_token (sig / iss / aud / exp / nonce), and returns normalized claims
// plus the raw id_token string (needed for SLO).
func (s *OIDCService) ExchangeAndVerify(ctx context.Context, code, codeVerifier, expectedNonce string) (*OIDCClaims, string, error) {
	tok, err := s.oauth.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return nil, "", fmt.Errorf("%w: token exchange: %v", ErrOIDCVerification, err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return nil, "", fmt.Errorf("%w: missing id_token", ErrOIDCVerification)
	}
	idTok, err := s.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, "", fmt.Errorf("%w: id_token verify: %v", ErrOIDCVerification, err)
	}
	if idTok.Nonce != expectedNonce {
		return nil, "", fmt.Errorf("%w: nonce mismatch", ErrOIDCVerification)
	}

	var raw map[string]any
	if err := idTok.Claims(&raw); err != nil {
		return nil, "", fmt.Errorf("%w: claims decode: %v", ErrOIDCVerification, err)
	}
	return s.extractClaims(idTok.Subject, raw), rawID, nil
}

func (s *OIDCService) extractClaims(subject string, raw map[string]any) *OIDCClaims {
	getStr := func(key string) string {
		if v, ok := raw[key].(string); ok {
			return v
		}
		return ""
	}
	getStrSlice := func(key string) []string {
		v, ok := raw[key]
		if !ok {
			return nil
		}
		arr, ok := v.([]any)
		if !ok {
			return nil
		}
		out := make([]string, 0, len(arr))
		for _, x := range arr {
			if str, ok := x.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return &OIDCClaims{
		Subject:   subject,
		Username:  getStr(s.cfg.UsernameClaim),
		Email:     getStr(s.cfg.EmailClaim),
		Name:      getStr(s.cfg.NameClaim),
		FirstName: getStr("given_name"),
		LastName:  getStr("family_name"),
		Groups:    getStrSlice(s.cfg.GroupsClaim),
		Raw:       raw,
	}
}

// TestConnection confirms the IdP is reachable by fetching the discovery
// document and checking for HTTP 200. Uses a plain GET instead of
// oidc.NewProvider to avoid the strict issuer-equality check — which always
// fails in split-horizon setups (internal URL keycloak:8080 vs public URL
// localhost:8180 in the iss field of the discovery doc).
func (s *OIDCService) TestConnection(ctx context.Context) error {
	wellKnown := strings.TrimSuffix(s.cfg.Issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("oidc discovery unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc discovery returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// endSessionMeta is used to extract end_session_endpoint from discovery JSON.
type endSessionMeta struct {
	EndSessionEndpoint string `json:"end_session_endpoint"`
}

// EndSessionEndpoint returns the IdP's end_session_endpoint, or "" if absent.
// The returned URL is rewritten to the public issuer URL if PublicIssuerURL is configured.
func (s *OIDCService) EndSessionEndpoint() string {
	var meta endSessionMeta
	if err := s.provider.Claims(&meta); err != nil {
		return ""
	}
	return s.publicURL(meta.EndSessionEndpoint)
}
