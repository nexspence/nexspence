# Phase 28 — OIDC / OAuth2 SSO Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** First-class OIDC/OAuth2 Single Sign-On in Nexspence, coexisting with local + LDAP auth. One IdP per deployment (Keycloak / Google / Entra / Okta). Authorization code + PKCE, fragment-based JWT delivery, JIT / allowlist / manual provisioning, role resolution via `admin_group` + `role_mappings`.

**Architecture:** Mirror existing LDAP path. `internal/auth/oidc.go` (OIDCAuthenticator interface + OIDCService using `coreos/go-oidc/v3`), `UserService.WithOIDC` + `LoginOIDC`, new `OIDCHandler`, `config.OIDCConfig`. Frontend: `LoginPage` gets a "Sign in with X" button + new `OIDCCallbackPage`. Spec: `docs/superpowers/specs/2026-04-24-phase28-oidc-sso-design.md`.

**Tech Stack:** Go 1.23, Gin, `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`, `crypto/aes` + `crypto/cipher` (GCM for state cookie). Frontend: React 18 + TypeScript + Vite + Zustand. PostgreSQL (no schema changes).

**Files (at a glance):**

| File | Responsibility | Action |
|---|---|---|
| `go.mod`, `go.sum` | Deps | edit |
| `internal/config/config.go` | `OIDCConfig` struct + validation | edit |
| `internal/config/config_test.go` | Config validation tests | edit |
| `internal/domain/types.go` | `UserSourceOIDC` constant | edit |
| `internal/service/repository_service.go` | `ErrProvisioningRejected`, `ErrProvisioningConflict` sentinels (same var-block as `ErrNotFound`) | edit |
| `internal/auth/oidc.go` | `OIDCAuthenticator` interface, `OIDCClaims`, `OIDCService` impl | **new** |
| `internal/auth/oidc_test.go` | Fake-IdP tests for OIDCService | **new** |
| `internal/auth/oidc_cookie.go` | AEAD state-cookie seal/open helpers | **new** |
| `internal/auth/oidc_cookie_test.go` | Seal/open round-trip + tamper detection | **new** |
| `internal/service/user_service.go` | `WithOIDC()`, `LoginOIDC()`, `syncOIDCRoles()`, `checkProvisioning()` | edit |
| `internal/service/user_service_oidc_test.go` | Provisioning + role-sync tests | **new** |
| `internal/api/handlers/oidc.go` | `OIDCHandler.Login`, `Callback`, `isSafeReturnPath` | **new** |
| `internal/api/handlers/oidc_test.go` | Handler tests | **new** |
| `internal/api/handlers/auth.go` | `AuthHandler.Config` endpoint | edit |
| `internal/api/handlers/auth_test.go` | Test for new `Config` endpoint | edit |
| `internal/api/audit_middleware.go` | Allow OIDC callback (GET) + `audit_source` context key | edit |
| `internal/api/router.go` | Wire routes + construct `OIDCHandler` | edit |
| `cmd/server/main.go` | Bootstrap `OIDCService` if enabled; `TestConnection` log | edit |
| `config.yaml` | Default `oidc:` block (disabled) | edit |
| `frontend/src/api/client.ts` | `AuthConfig` type + `getAuthConfig()` | edit |
| `frontend/src/pages/LoginPage.tsx` | Button + error banner | edit |
| `frontend/src/pages/OIDCCallbackPage.tsx` | Fragment → localStorage → navigate | **new** |
| `frontend/src/App.tsx` | `/oidc/callback` route | edit |
| `Makefile` | `oidc-secret` target (32-byte base64 key) | edit |
| `docs/oidc-setup.md` | Keycloak / Google / Entra / Okta presets | **new** |
| `task_plan.md`, `progress.md`, `findings.md` | Phase 26 → Phase 28 status/session entry | edit |

---

## Task 1: Add go-oidc + oauth2 dependencies

**Files:**
- Modify: `go.mod`
- Modify: `go.sum` (auto)

- [ ] **Step 1: Fetch modules**

```bash
go get github.com/coreos/go-oidc/v3@latest golang.org/x/oauth2@latest
go mod tidy
```

- [ ] **Step 2: Confirm build still clean**

```bash
go build ./...
```
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps(phase28): add github.com/coreos/go-oidc/v3 + golang.org/x/oauth2"
```

---

## Task 2: Add `OIDCConfig` struct + validation

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go` (test file — check with `ls`; create if absent)

- [ ] **Step 1: Write the failing test** (append to `internal/config/config_test.go`)

```go
func TestConfig_OIDC_MissingIssuer_FailsWhenEnabled(t *testing.T) {
	c := baseValidConfig()
	c.OIDC = OIDCConfig{Enabled: true, ClientID: "x", ClientSecret: "y", RedirectURL: "https://a/cb", FrontendBaseURL: "https://a", CookieKey: validBase64Key32()}
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oidc.issuer")
}

func TestConfig_OIDC_AllowlistEmpty_FailsWhenMode(t *testing.T) {
	c := baseValidConfig()
	c.OIDC = validOIDCConfig()
	c.OIDC.Provisioning = "allowlist"
	c.OIDC.EmailAllowlist = nil
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "email_allowlist")
}

func TestConfig_OIDC_CookieKey_InvalidBase64_Fails(t *testing.T) {
	c := baseValidConfig()
	c.OIDC = validOIDCConfig()
	c.OIDC.CookieKey = "not@base64!!"
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cookie_key")
}

// Helpers (add near top of test file if not present):
func validBase64Key32() string {
	b := make([]byte, 32)
	for i := range b { b[i] = byte(i) }
	return base64.StdEncoding.EncodeToString(b)
}

func validOIDCConfig() OIDCConfig {
	return OIDCConfig{
		Enabled: true, Issuer: "https://idp.example.com",
		ClientID: "nexspence", ClientSecret: "s3cret",
		RedirectURL: "https://a/cb", FrontendBaseURL: "https://a",
		Provisioning: "jit", CookieKey: validBase64Key32(),
		Scopes: []string{"openid", "profile", "email"},
	}
}
```

- [ ] **Step 2: Run — verify failure**

```bash
go test ./internal/config/... -run TestConfig_OIDC -count=1 -v
```
Expected: compile error (`OIDCConfig` undefined) — treat as "fail."

- [ ] **Step 3: Add `OIDCConfig` struct in `internal/config/config.go`**

Locate `type LDAPConfig struct { ... }` (around line 87) and add below it:

```go
// OIDCConfig configures OIDC / OAuth2 SSO authentication.
type OIDCConfig struct {
	Enabled         bool     `mapstructure:"enabled"`
	DisplayName     string   `mapstructure:"display_name"`      // "Keycloak"
	Issuer          string   `mapstructure:"issuer"`
	ClientID        string   `mapstructure:"client_id"`
	ClientSecret    string   `mapstructure:"client_secret"`
	RedirectURL     string   `mapstructure:"redirect_url"`
	FrontendBaseURL string   `mapstructure:"frontend_base_url"`
	Scopes          []string `mapstructure:"scopes"`

	Provisioning   string   `mapstructure:"provisioning"`    // jit | allowlist | manual
	EmailAllowlist []string `mapstructure:"email_allowlist"`

	GroupsClaim  string            `mapstructure:"groups_claim"`   // default "groups"
	AdminGroup   string            `mapstructure:"admin_group"`
	RoleMappings map[string]string `mapstructure:"role_mappings"`

	UsernameClaim string `mapstructure:"username_claim"` // default "preferred_username"
	EmailClaim    string `mapstructure:"email_claim"`    // default "email"
	NameClaim     string `mapstructure:"name_claim"`     // default "name"

	ShowLoginButton    bool   `mapstructure:"show_login_button"`
	CookieSecure       bool   `mapstructure:"cookie_secure"`
	CookieKey          string `mapstructure:"cookie_key"`           // base64 32 bytes
	AllowedSkewSeconds int    `mapstructure:"allowed_skew_seconds"` // default 60
}
```

Add field to `Config` struct:

```go
OIDC OIDCConfig `mapstructure:"oidc"`
```

- [ ] **Step 4: Add validation inside existing `func (c *Config) Validate() error`**

Find `if cfg.Auth.JWTSecret == "" { ... }` (line ~196). Append at end of Validate():

```go
if c.OIDC.Enabled {
	if c.OIDC.Issuer == "" {
		return fmt.Errorf("oidc.issuer is required when oidc.enabled=true")
	}
	if c.OIDC.ClientID == "" || c.OIDC.ClientSecret == "" {
		return fmt.Errorf("oidc.client_id and oidc.client_secret are required when oidc.enabled=true")
	}
	if c.OIDC.RedirectURL == "" || c.OIDC.FrontendBaseURL == "" {
		return fmt.Errorf("oidc.redirect_url and oidc.frontend_base_url are required when oidc.enabled=true")
	}
	if c.OIDC.Provisioning == "allowlist" && len(c.OIDC.EmailAllowlist) == 0 {
		return fmt.Errorf("oidc.email_allowlist must be non-empty when oidc.provisioning=allowlist")
	}
	keyBytes, err := base64.StdEncoding.DecodeString(c.OIDC.CookieKey)
	if err != nil || len(keyBytes) != 32 {
		return fmt.Errorf("oidc.cookie_key must be base64-encoded 32 bytes")
	}
}
return nil
```

Add `"encoding/base64"` import.

- [ ] **Step 5: Add Viper defaults**

Locate where LDAP defaults are set (`v.SetDefault("ldap...")`). Add OIDC defaults:

```go
v.SetDefault("oidc.enabled", false)
v.SetDefault("oidc.display_name", "SSO")
v.SetDefault("oidc.scopes", []string{"openid", "profile", "email", "groups"})
v.SetDefault("oidc.provisioning", "jit")
v.SetDefault("oidc.groups_claim", "groups")
v.SetDefault("oidc.username_claim", "preferred_username")
v.SetDefault("oidc.email_claim", "email")
v.SetDefault("oidc.name_claim", "name")
v.SetDefault("oidc.show_login_button", true)
v.SetDefault("oidc.cookie_secure", true)
v.SetDefault("oidc.allowed_skew_seconds", 60)
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/config/... -run TestConfig_OIDC -count=1 -v
```
Expected: PASS (3 tests).

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(phase28): add OIDCConfig schema + validation"
```

---

## Task 3: Add domain constant + error sentinels

**Files:**
- Modify: `internal/domain/types.go`
- Modify: `internal/service/repository_service.go` (where `ErrNotFound` is defined — line ~17)

- [ ] **Step 1: Add `UserSourceOIDC` constant**

In `internal/domain/types.go`, locate `UserSourceLDAP UserSource = "ldap"` and add:

```go
UserSourceOIDC UserSource = "oidc"
```

- [ ] **Step 2: Add sentinel errors**

In `internal/service/repository_service.go` var-block at line ~15 (where `ErrNotFound`, `ErrInvalidInput` are declared), add:

```go
ErrProvisioningRejected = errors.New("provisioning rejected")
ErrProvisioningConflict = errors.New("user source conflict")
```

- [ ] **Step 3: Build clean**

```bash
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add internal/domain/types.go internal/service/repository_service.go
git commit -m "feat(phase28): add UserSourceOIDC + provisioning error sentinels"
```

---

## Task 4: AEAD state-cookie helpers

**Files:**
- Create: `internal/auth/oidc_cookie.go`
- Create: `internal/auth/oidc_cookie_test.go`

- [ ] **Step 1: Write the failing test** (`internal/auth/oidc_cookie_test.go`)

```go
package auth

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey() []byte {
	b := make([]byte, 32)
	for i := range b { b[i] = byte(i + 1) }
	return b
}

func TestOIDCCookie_SealOpen_RoundTrip(t *testing.T) {
	s, err := NewCookieSealer(testKey())
	require.NoError(t, err)

	in := StateCookiePayload{
		State: "abc", Nonce: "xyz", CodeVerifier: "v123",
		ReturnTo: "/foo", IssuedAt: time.Now().Unix(),
	}
	sealed, err := s.Seal(in)
	require.NoError(t, err)

	out, err := s.Open(sealed)
	require.NoError(t, err)
	assert.Equal(t, in, *out)
}

func TestOIDCCookie_TamperedCiphertext_Fails(t *testing.T) {
	s, _ := NewCookieSealer(testKey())
	sealed, _ := s.Seal(StateCookiePayload{State: "abc"})
	tampered := sealed[:len(sealed)-4] + "XXXX"
	_, err := s.Open(tampered)
	require.Error(t, err)
}

func TestOIDCCookie_WrongKey_Fails(t *testing.T) {
	s1, _ := NewCookieSealer(testKey())
	sealed, _ := s1.Seal(StateCookiePayload{State: "abc"})

	otherKey := make([]byte, 32)
	for i := range otherKey { otherKey[i] = byte(255 - i) }
	s2, _ := NewCookieSealer(otherKey)
	_, err := s2.Open(sealed)
	require.Error(t, err)
}

func TestNewCookieSealer_WrongKeySize_Fails(t *testing.T) {
	_, err := NewCookieSealer([]byte{1, 2, 3})
	require.Error(t, err)
}

func TestCookieSealer_OpenGarbage_Fails(t *testing.T) {
	s, _ := NewCookieSealer(testKey())
	_, err := s.Open("not-valid-base64-@@@")
	require.Error(t, err)

	_, err = s.Open(base64.RawURLEncoding.EncodeToString([]byte{1, 2, 3}))
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test — expect compile failure**

```bash
go test ./internal/auth/ -run TestOIDCCookie -count=1 -v
```
Expected: compile error — types undefined.

- [ ] **Step 3: Implement** (`internal/auth/oidc_cookie.go`)

```go
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// StateCookiePayload carries the short-lived state between /oidc/login
// and /oidc/callback. It is AEAD-sealed (AES-256-GCM) into an httpOnly
// cookie — not a secret per se, but tamper-proofing is required so a
// client cannot extend TTL or swap state.
type StateCookiePayload struct {
	State        string `json:"s"`
	Nonce        string `json:"n"`
	CodeVerifier string `json:"v"`
	ReturnTo     string `json:"r"`
	IssuedAt     int64  `json:"t"`
}

// CookieSealer seals and opens StateCookiePayload with AES-256-GCM.
type CookieSealer struct {
	aead cipher.AEAD
}

// NewCookieSealer requires a 32-byte key (AES-256).
func NewCookieSealer(key []byte) (*CookieSealer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("cookie key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &CookieSealer{aead: aead}, nil
}

// Seal returns a base64url(nonce||ciphertext) string safe for cookie use.
func (s *CookieSealer) Seal(p StateCookiePayload) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := s.aead.Seal(nonce, nonce, raw, nil) // nonce prepended
	return base64.RawURLEncoding.EncodeToString(ct), nil
}

func (s *CookieSealer) Open(sealed string) (*StateCookiePayload, error) {
	buf, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return nil, err
	}
	ns := s.aead.NonceSize()
	if len(buf) < ns+s.aead.Overhead() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := buf[:ns], buf[ns:]
	raw, err := s.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, err
	}
	var p StateCookiePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
```

- [ ] **Step 4: Run tests — expect PASS**

```bash
go test ./internal/auth/ -run TestOIDCCookie -run TestNewCookieSealer -run TestCookieSealer -count=1 -v
```
Expected: 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/oidc_cookie.go internal/auth/oidc_cookie_test.go
git commit -m "feat(phase28): AES-256-GCM state-cookie sealer for OIDC flow"
```

---

## Task 5: `OIDCAuthenticator` interface + `OIDCService` discovery

**Files:**
- Create: `internal/auth/oidc.go`
- Create: `internal/auth/oidc_test.go`

- [ ] **Step 1: Write types + interface** (`internal/auth/oidc.go`)

```go
package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/nexspence-oss/nexspence/internal/config"
	"golang.org/x/oauth2"
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
	ExchangeAndVerify(ctx context.Context, code, codeVerifier, expectedNonce string) (*OIDCClaims, error)
	TestConnection(ctx context.Context) error
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
// — caller (main.go) should fail startup so misconfig is loud, not lazy.
func NewOIDCService(ctx context.Context, cfg config.OIDCConfig) (*OIDCService, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	verifier := provider.Verifier(&oidc.Config{
		ClientID:             cfg.ClientID,
		SkewAllowedInSeconds: uint64(cfg.AllowedSkewSeconds),
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

func (s *OIDCService) AuthCodeURL(state, nonce, codeChallenge string) string {
	return s.oauth.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

func (s *OIDCService) ExchangeAndVerify(ctx context.Context, code, codeVerifier, expectedNonce string) (*OIDCClaims, error) {
	tok, err := s.oauth.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: token exchange: %v", ErrOIDCVerification, err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return nil, fmt.Errorf("%w: missing id_token", ErrOIDCVerification)
	}
	idTok, err := s.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, fmt.Errorf("%w: id_token verify: %v", ErrOIDCVerification, err)
	}
	if idTok.Nonce != expectedNonce {
		return nil, fmt.Errorf("%w: nonce mismatch", ErrOIDCVerification)
	}

	var raw map[string]any
	if err := idTok.Claims(&raw); err != nil {
		return nil, fmt.Errorf("%w: claims decode: %v", ErrOIDCVerification, err)
	}

	return s.extractClaims(idTok.Subject, raw), nil
}

func (s *OIDCService) extractClaims(subject string, raw map[string]any) *OIDCClaims {
	getStr := func(key string) string {
		if v, ok := raw[key].(string); ok { return v }
		return ""
	}
	getStrSlice := func(key string) []string {
		v, ok := raw[key]
		if !ok { return nil }
		// JSON arrays arrive as []any.
		arr, ok := v.([]any)
		if !ok { return nil }
		out := make([]string, 0, len(arr))
		for _, x := range arr {
			if s, ok := x.(string); ok { out = append(out, s) }
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

// TestConnection re-runs discovery to confirm the IdP is reachable.
// Called by main.go at startup for a clear "oidc discovery ok/err" log line.
func (s *OIDCService) TestConnection(ctx context.Context) error {
	_, err := oidc.NewProvider(ctx, s.cfg.Issuer)
	return err
}
```

- [ ] **Step 2: Write fake-IdP test harness** (`internal/auth/oidc_test.go`)

```go
package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeIdP is a minimal OIDC provider stub.
type fakeIdP struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	claims map[string]any
	// knobs for error paths
	issuedNonce string
	issuedAud   string
	issuedExp   int64
	signingKey  *rsa.PrivateKey // override for wrong-sig test
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	f := &fakeIdP{key: k, kid: "test-kid"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := f.server.URL
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 base,
			"authorization_endpoint": base + "/authorize",
			"token_endpoint":         base + "/token",
			"jwks_uri":               base + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(k.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA", "use": "sig", "alg": "RS256",
				"kid": f.kid, "n": n, "e": e,
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		signer := f.signingKey
		if signer == nil { signer = k }
		claims := jwt.MapClaims{
			"iss":   f.server.URL,
			"aud":   f.issuedAud,
			"exp":   f.issuedExp,
			"iat":   time.Now().Unix(),
			"sub":   "u-123",
			"nonce": f.issuedNonce,
		}
		for k, v := range f.claims { claims[k] = v }
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = f.kid
		signed, _ := tok.SignedString(signer)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-placeholder",
			"token_type":   "Bearer",
			"id_token":     signed,
		})
	})
	f.server = httptest.NewServer(mux)
	return f
}

func (f *fakeIdP) close() { f.server.Close() }

func baseOIDCCfg(idp *fakeIdP) config.OIDCConfig {
	return config.OIDCConfig{
		Enabled: true, Issuer: idp.server.URL,
		ClientID: "client-xyz", ClientSecret: "secret",
		RedirectURL: "https://app/cb",
		Scopes: []string{"openid", "profile", "email", "groups"},
		UsernameClaim: "preferred_username",
		EmailClaim: "email", NameClaim: "name",
		GroupsClaim: "groups",
		AllowedSkewSeconds: 60,
	}
}

func TestOIDCService_HappyPath(t *testing.T) {
	idp := newFakeIdP(t); defer idp.close()
	nonce := "n-ok"
	idp.issuedNonce = nonce
	idp.issuedAud = "client-xyz"
	idp.issuedExp = time.Now().Add(5 * time.Minute).Unix()
	idp.claims = map[string]any{
		"preferred_username": "alice",
		"email": "alice@example.com",
		"name": "Alice Example",
		"given_name": "Alice", "family_name": "Example",
		"groups": []any{"developers", "nexspence-admins"},
	}

	svc, err := NewOIDCService(context.Background(), baseOIDCCfg(idp))
	require.NoError(t, err)
	claims, err := svc.ExchangeAndVerify(context.Background(), "code-abc", "verifier-xyz", nonce)
	require.NoError(t, err)
	assert.Equal(t, "alice", claims.Username)
	assert.Equal(t, "alice@example.com", claims.Email)
	assert.ElementsMatch(t, []string{"developers", "nexspence-admins"}, claims.Groups)
}

func TestOIDCService_NonceMismatch(t *testing.T) {
	idp := newFakeIdP(t); defer idp.close()
	idp.issuedNonce = "n-real"
	idp.issuedAud = "client-xyz"
	idp.issuedExp = time.Now().Add(5 * time.Minute).Unix()

	svc, _ := NewOIDCService(context.Background(), baseOIDCCfg(idp))
	_, err := svc.ExchangeAndVerify(context.Background(), "code", "verifier", "n-expected")
	require.ErrorIs(t, err, ErrOIDCVerification)
}

func TestOIDCService_ExpiredToken(t *testing.T) {
	idp := newFakeIdP(t); defer idp.close()
	idp.issuedNonce = "n"
	idp.issuedAud = "client-xyz"
	idp.issuedExp = time.Now().Add(-5 * time.Minute).Unix()

	svc, _ := NewOIDCService(context.Background(), baseOIDCCfg(idp))
	_, err := svc.ExchangeAndVerify(context.Background(), "code", "verifier", "n")
	require.ErrorIs(t, err, ErrOIDCVerification)
}

func TestOIDCService_WrongAudience(t *testing.T) {
	idp := newFakeIdP(t); defer idp.close()
	idp.issuedNonce = "n"
	idp.issuedAud = "some-other-client"
	idp.issuedExp = time.Now().Add(5 * time.Minute).Unix()

	svc, _ := NewOIDCService(context.Background(), baseOIDCCfg(idp))
	_, err := svc.ExchangeAndVerify(context.Background(), "code", "verifier", "n")
	require.ErrorIs(t, err, ErrOIDCVerification)
}

func TestOIDCService_WrongSignature(t *testing.T) {
	idp := newFakeIdP(t); defer idp.close()
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	idp.signingKey = other
	idp.issuedNonce = "n"
	idp.issuedAud = "client-xyz"
	idp.issuedExp = time.Now().Add(5 * time.Minute).Unix()

	svc, _ := NewOIDCService(context.Background(), baseOIDCCfg(idp))
	_, err := svc.ExchangeAndVerify(context.Background(), "code", "verifier", "n")
	require.ErrorIs(t, err, ErrOIDCVerification)
}

func TestOIDCService_ClaimCustomization(t *testing.T) {
	idp := newFakeIdP(t); defer idp.close()
	idp.issuedNonce = "n"
	idp.issuedAud = "client-xyz"
	idp.issuedExp = time.Now().Add(5 * time.Minute).Unix()
	idp.claims = map[string]any{"email": "bob@example.com"}

	cfg := baseOIDCCfg(idp)
	cfg.UsernameClaim = "email" // Google pattern
	svc, _ := NewOIDCService(context.Background(), cfg)
	claims, err := svc.ExchangeAndVerify(context.Background(), "code", "verifier", "n")
	require.NoError(t, err)
	assert.Equal(t, "bob@example.com", claims.Username)
}

func TestOIDCService_TestConnection_OK(t *testing.T) {
	idp := newFakeIdP(t); defer idp.close()
	svc, _ := NewOIDCService(context.Background(), baseOIDCCfg(idp))
	require.NoError(t, svc.TestConnection(context.Background()))
}

func TestOIDCService_TestConnection_Unreachable(t *testing.T) {
	cfg := config.OIDCConfig{Issuer: "http://127.0.0.1:1/nowhere", AllowedSkewSeconds: 60}
	_, err := NewOIDCService(context.Background(), cfg)
	require.Error(t, err)
	_ = fmt.Sprintf // silence linter
}
```

- [ ] **Step 3: Install jwt dep used by tests**

```bash
go get github.com/golang-jwt/jwt/v5
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/auth/ -run TestOIDCService -count=1 -v
```
Expected: all 8 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/auth/oidc.go internal/auth/oidc_test.go
git commit -m "feat(phase28): OIDCAuthenticator interface + OIDCService impl (discovery, PKCE, id_token verify)"
```

---

## Task 6: `UserService.WithOIDC` + `LoginOIDC` (provisioning)

**Files:**
- Modify: `internal/service/user_service.go`
- Create: `internal/service/user_service_oidc_test.go`

- [ ] **Step 1: Extend `UserService` struct**

In `internal/service/user_service.go` below the LDAP-related fields (around line 20), add:

```go
oidc    auth.OIDCAuthenticator // nil when OIDC is disabled
oidcCfg config.OIDCConfig
```

Add import for `"github.com/nexspence-oss/nexspence/internal/config"` if not present.

- [ ] **Step 2: Add `WithOIDC` builder**

Directly after `WithLDAP` (line ~36):

```go
// WithOIDC attaches an OIDC authenticator and its config.
// Returns the same service for chaining.
func (s *UserService) WithOIDC(a auth.OIDCAuthenticator, cfg config.OIDCConfig) *UserService {
	s.oidc = a
	s.oidcCfg = cfg
	return s
}
```

- [ ] **Step 3: Write failing tests** (`internal/service/user_service_oidc_test.go`)

```go
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockOIDC satisfies auth.OIDCAuthenticator.
type mockOIDC struct{}

func (m *mockOIDC) AuthCodeURL(state, nonce, cc string) string { return "" }
func (m *mockOIDC) ExchangeAndVerify(ctx context.Context, code, v, n string) (*auth.OIDCClaims, error) {
	return nil, nil
}
func (m *mockOIDC) TestConnection(ctx context.Context) error { return nil }

func newUserSvcOIDC(t *testing.T, cfg config.OIDCConfig, seed ...*domain.User) *UserService {
	t.Helper()
	users := testutil.NewUserRepo(seed...)
	roles := testutil.NewRoleRepo(
		&domain.Role{ID: "role-admin", Name: "nx-admin"},
		&domain.Role{ID: "role-release", Name: "release-manager"},
		&domain.Role{ID: "role-read", Name: "read-only"},
	)
	authSvc := auth.NewService("test-secret-abcdef0123", 24, 4)
	s := NewUserService(users, roles, authSvc, zap.NewNop().Sugar())
	return s.WithOIDC(&mockOIDC{}, cfg)
}

func baseOIDCCfg() config.OIDCConfig {
	return config.OIDCConfig{
		Enabled: true, Provisioning: "jit",
		AdminGroup: "nexspence-admins",
		RoleMappings: map[string]string{"developers": "release-manager"},
	}
}

func TestLoginOIDC_NewUser_JIT_AutoCreatesWithRoles(t *testing.T) {
	s := newUserSvcOIDC(t, baseOIDCCfg())
	claims := &auth.OIDCClaims{
		Username: "alice", Email: "alice@ex.com",
		FirstName: "Alice", LastName: "Example",
		Groups: []string{"developers", "nexspence-admins"},
	}
	tok, u, err := s.LoginOIDC(context.Background(), claims)
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.Equal(t, "alice", u.Username)
	assert.Equal(t, domain.UserSourceOIDC, u.Source)
	assert.ElementsMatch(t, []string{"nx-admin", "release-manager"}, u.Roles)
}

func TestLoginOIDC_NewUser_Allowlist_EmailMatch_Created(t *testing.T) {
	cfg := baseOIDCCfg()
	cfg.Provisioning = "allowlist"
	cfg.EmailAllowlist = []string{"*@company.com"}
	s := newUserSvcOIDC(t, cfg)
	_, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "bob", Email: "bob@company.com", Groups: []string{"developers"},
	})
	require.NoError(t, err)
	assert.Equal(t, "bob", u.Username)
	assert.Contains(t, u.Roles, "release-manager")
}

func TestLoginOIDC_NewUser_Allowlist_EmailMiss_Rejected(t *testing.T) {
	cfg := baseOIDCCfg()
	cfg.Provisioning = "allowlist"
	cfg.EmailAllowlist = []string{"*@company.com"}
	s := newUserSvcOIDC(t, cfg)
	_, _, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "mallory", Email: "mallory@evil.io",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrProvisioningRejected))
}

func TestLoginOIDC_NewUser_Manual_Rejected(t *testing.T) {
	cfg := baseOIDCCfg()
	cfg.Provisioning = "manual"
	s := newUserSvcOIDC(t, cfg)
	_, _, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@ex.com",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrProvisioningRejected))
}

func TestLoginOIDC_ExistingUser_SourceMismatch_Rejected(t *testing.T) {
	existing := &domain.User{
		ID: "u1", Username: "alice", Email: "alice@ex.com",
		Source: domain.UserSourceLocal, Status: domain.UserStatusActive,
	}
	s := newUserSvcOIDC(t, baseOIDCCfg(), existing)
	_, _, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@ex.com",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrProvisioningConflict))
}

func TestLoginOIDC_ExistingUser_SyncRoles_Replaces(t *testing.T) {
	existing := &domain.User{
		ID: "u1", Username: "alice", Email: "alice@ex.com",
		Source: domain.UserSourceOIDC, Status: domain.UserStatusActive,
	}
	s := newUserSvcOIDC(t, baseOIDCCfg(), existing)
	// Pre-seed user with nx-admin (as if granted manually once).
	_ = s.roles.SetUserRoles(context.Background(), "u1", []string{"role-admin"})

	_, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@ex.com",
		Groups: []string{"developers"}, // no nexspence-admins → nx-admin must drop
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"release-manager"}, u.Roles)
}

func TestLoginOIDC_MissingRoleInDB_Warns_NoFail(t *testing.T) {
	cfg := baseOIDCCfg()
	cfg.RoleMappings = map[string]string{"developers": "role-that-does-not-exist"}
	s := newUserSvcOIDC(t, cfg)
	_, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@ex.com",
		Groups: []string{"developers"},
	})
	require.NoError(t, err)
	assert.Empty(t, u.Roles) // unknown mapping silently skipped
}

func TestLoginOIDC_InactiveUser_Rejected(t *testing.T) {
	existing := &domain.User{
		ID: "u1", Username: "alice", Email: "alice@ex.com",
		Source: domain.UserSourceOIDC, Status: domain.UserStatusDisabled,
	}
	s := newUserSvcOIDC(t, baseOIDCCfg(), existing)
	_, _, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@ex.com",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInput))
}

func TestLoginOIDC_EmptyUsername_Rejected(t *testing.T) {
	s := newUserSvcOIDC(t, baseOIDCCfg())
	_, _, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "", Email: "alice@ex.com",
	})
	require.Error(t, err)
}

func TestLoginOIDC_DNFormatGroup_MatchesAdminGroup(t *testing.T) {
	s := newUserSvcOIDC(t, baseOIDCCfg())
	_, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@ex.com",
		Groups: []string{"CN=nexspence-admins,OU=Groups,DC=ex,DC=com"},
	})
	require.NoError(t, err)
	assert.Contains(t, u.Roles, "nx-admin")
}
```

- [ ] **Step 4: Run — verify failures**

```bash
go test ./internal/service/ -run TestLoginOIDC -count=1 -v
```
Expected: compile errors (no `LoginOIDC`).

- [ ] **Step 5: Implement `LoginOIDC`, `checkProvisioning`, `syncOIDCRoles`** in `internal/service/user_service.go`

Append at end of file:

```go
// LoginOIDC upserts the user and assigns roles based on OIDC claims.
// See phase28 spec for provisioning modes + role-replace semantics.
func (s *UserService) LoginOIDC(ctx context.Context, claims *auth.OIDCClaims) (string, *domain.User, error) {
	if s.oidc == nil {
		return "", nil, fmt.Errorf("%w: oidc not configured", ErrInvalidInput)
	}
	username := strings.ToLower(strings.TrimSpace(claims.Username))
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if username == "" || email == "" {
		return "", nil, fmt.Errorf("%w: claims missing username or email", ErrInvalidInput)
	}

	existing, err := s.users.Get(ctx, username)
	if err != nil {
		return "", nil, err
	}
	if existing != nil && existing.Source != domain.UserSourceOIDC {
		return "", nil, fmt.Errorf("%w: username %q is claimed by %s",
			ErrProvisioningConflict, username, existing.Source)
	}

	if existing == nil {
		if err := s.checkProvisioning(email); err != nil {
			return "", nil, err
		}
		existing = &domain.User{
			Username: username, Email: email,
			FirstName: claims.FirstName, LastName: claims.LastName,
			Status: domain.UserStatusActive, Source: domain.UserSourceOIDC,
		}
		if err := s.users.Create(ctx, existing); err != nil {
			return "", nil, fmt.Errorf("create oidc user: %w", err)
		}
	} else {
		existing.Email = email
		existing.FirstName = claims.FirstName
		existing.LastName = claims.LastName
		_ = s.users.Update(ctx, existing)
	}

	if existing.Status != domain.UserStatusActive {
		return "", nil, fmt.Errorf("%w: user account is not active", ErrInvalidInput)
	}

	if err := s.syncOIDCRoles(ctx, existing.ID, claims.Groups); err != nil {
		s.log.Warnw("syncOIDCRoles failed", "username", username, "err", err)
	}

	if fresh, err2 := s.roles.GetUserRoles(ctx, existing.ID); err2 == nil {
		names := make([]string, 0, len(fresh))
		for _, r := range fresh {
			names = append(names, r.Name)
		}
		existing.Roles = names
	}

	s.log.Infow("oidc login complete", "username", username, "roles", existing.Roles)

	token, err := s.auth.GenerateToken(existing.ID, existing.Username, existing.Roles)
	if err != nil {
		return "", nil, err
	}
	_ = s.users.UpdateLastLogin(ctx, username)
	return token, existing, nil
}

func (s *UserService) checkProvisioning(email string) error {
	mode := s.oidcCfg.Provisioning
	if mode == "" {
		mode = "jit"
	}
	switch mode {
	case "jit":
		return nil
	case "allowlist":
		for _, pat := range s.oidcCfg.EmailAllowlist {
			ok, _ := path.Match(strings.ToLower(pat), email)
			if ok {
				return nil
			}
		}
		return fmt.Errorf("%w: email %q not in allowlist", ErrProvisioningRejected, email)
	case "manual":
		return fmt.Errorf("%w: user must be pre-created by an admin", ErrProvisioningRejected)
	default:
		return fmt.Errorf("%w: unknown provisioning mode %q", ErrInvalidInput, mode)
	}
}

func (s *UserService) syncOIDCRoles(ctx context.Context, userID string, groups []string) error {
	want := make(map[string]struct{})
	for _, g := range groups {
		if s.oidcCfg.AdminGroup != "" && ldapGroupMatch(g, s.oidcCfg.AdminGroup) {
			want["nx-admin"] = struct{}{}
		}
		if roleName, ok := s.oidcCfg.RoleMappings[g]; ok && roleName != "" {
			want[roleName] = struct{}{}
		}
		// Also allow role_mappings key to be a CN — normalize group once for lookup.
		for mapKey, roleName := range s.oidcCfg.RoleMappings {
			if ldapGroupMatch(g, mapKey) && roleName != "" {
				want[roleName] = struct{}{}
			}
		}
	}

	allRoles, err := s.roles.List(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(want))
	matched := make(map[string]bool)
	for _, r := range allRoles {
		if _, ok := want[r.Name]; ok {
			ids = append(ids, r.ID)
			matched[r.Name] = true
		}
	}
	for name := range want {
		if !matched[name] {
			s.log.Warnw("oidc role mapping references unknown role", "role", name)
		}
	}
	return s.roles.SetUserRoles(ctx, userID, ids)
}
```

Add imports at top: `"path"`.

- [ ] **Step 6: Run tests**

```bash
go test ./internal/service/ -run TestLoginOIDC -count=1 -v
```
Expected: 10 tests PASS.

- [ ] **Step 7: Full service test run (ensure no regression)**

```bash
go test ./internal/service/... -count=1
```
Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add internal/service/user_service.go internal/service/user_service_oidc_test.go
git commit -m "feat(phase28): UserService.WithOIDC + LoginOIDC with provisioning + role sync"
```

---

## Task 7: `OIDCHandler.Login` + `Callback` + `isSafeReturnPath`

**Files:**
- Create: `internal/api/handlers/oidc.go`
- Create: `internal/api/handlers/oidc_test.go`

- [ ] **Step 1: Write table-driven `isSafeReturnPath` test first** (`internal/api/handlers/oidc_test.go`)

```go
package handlers_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func init() { gin.SetMode(gin.TestMode) }

func TestIsSafeReturnPath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"/", true},
		{"/repos", true},
		{"/repos/a/b?q=1", true},
		{"//evil.com/x", false},
		{"http://evil.com", false},
		{"https://good.com/x", false},
		{"javascript:alert(1)", false},
		{"data:text/html,x", false},
		{"/" + strings.Repeat("a", 300), false},
	}
	for _, tc := range cases {
		got := handlers.IsSafeReturnPath(tc.in)
		assert.Equal(t, tc.want, got, "input=%q", tc.in)
	}
}
```

- [ ] **Step 2: Write handler tests**

Append to the same file:

```go
func validBase64Key32Handler() string {
	b := make([]byte, 32)
	for i := range b { b[i] = byte(i + 2) }
	return base64.StdEncoding.EncodeToString(b)
}

func newOIDCTestCfg() config.OIDCConfig {
	return config.OIDCConfig{
		Enabled: true, DisplayName: "TestIdP",
		Issuer: "https://idp", ClientID: "client", ClientSecret: "s",
		RedirectURL: "https://app/cb", FrontendBaseURL: "https://app",
		CookieKey: validBase64Key32Handler(),
		Provisioning: "jit",
		UsernameClaim: "preferred_username", EmailClaim: "email",
		NameClaim: "name", GroupsClaim: "groups",
		AdminGroup: "admins", CookieSecure: false,
	}
}

// mockOIDCAuthenticator — returns canned claims or an error.
type mockOIDCAuthenticator struct {
	claims *auth.OIDCClaims
	err    error
}

func (m *mockOIDCAuthenticator) AuthCodeURL(state, nonce, cc string) string {
	return "https://idp/authorize?state=" + state
}
func (m *mockOIDCAuthenticator) ExchangeAndVerify(ctx context.Context, code, v, n string) (*auth.OIDCClaims, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.claims, nil
}
func (m *mockOIDCAuthenticator) TestConnection(ctx context.Context) error { return nil }

func newOIDCHandlerRig(t *testing.T, mock *mockOIDCAuthenticator) (*gin.Engine, *service.UserService) {
	t.Helper()
	cfg := newOIDCTestCfg()
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo(
		&domain.Role{ID: "ra", Name: "nx-admin"},
	)
	authSvc := auth.NewService("s", 24, 4)
	userSvc := service.NewUserService(users, roles, authSvc, zap.NewNop().Sugar()).
		WithOIDC(mock, cfg)

	sealer, err := auth.NewCookieSealer(mustDecodeB64(cfg.CookieKey))
	require.NoError(t, err)
	h := handlers.NewOIDCHandler(mock, userSvc, sealer, cfg, zap.NewNop().Sugar())

	r := gin.New()
	r.GET("/api/v1/auth/oidc/login", h.Login)
	r.GET("/api/v1/auth/oidc/callback", h.Callback)
	return r, userSvc
}

func mustDecodeB64(s string) []byte {
	b, _ := base64.StdEncoding.DecodeString(s)
	return b
}

func TestOIDCHandler_Login_SetsStateCookie_And_Redirects(t *testing.T) {
	mock := &mockOIDCAuthenticator{}
	r, _ := newOIDCHandlerRig(t, mock)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login?return_to=/repos", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "https://idp/authorize")

	cookie := w.Header().Get("Set-Cookie")
	require.Contains(t, cookie, "oidc_state=")
	assert.Contains(t, cookie, "HttpOnly")
	assert.Contains(t, cookie, "SameSite=Lax")
}

func TestOIDCHandler_Callback_StateMismatch_RedirectsWithError(t *testing.T) {
	mock := &mockOIDCAuthenticator{}
	r, _ := newOIDCHandlerRig(t, mock)

	// First: /login to get a state cookie.
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	sealedCookie := extractCookieValue(w1.Header().Get("Set-Cookie"), "oidc_state")

	// Callback with mismatched state query.
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?code=x&state=WRONG", nil)
	req2.AddCookie(&http.Cookie{Name: "oidc_state", Value: sealedCookie})
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	require.Equal(t, http.StatusFound, w2.Code)
	loc, _ := url.Parse(w2.Header().Get("Location"))
	assert.Equal(t, "/login", loc.Path)
	assert.Contains(t, loc.Query().Get("oidc_error"), "state")
}

func TestOIDCHandler_Callback_IdPError_Redirects(t *testing.T) {
	mock := &mockOIDCAuthenticator{}
	r, _ := newOIDCHandlerRig(t, mock)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?error=access_denied", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "oidc_error=")
}

func TestOIDCHandler_Callback_HappyPath_RedirectsWithToken(t *testing.T) {
	mock := &mockOIDCAuthenticator{
		claims: &auth.OIDCClaims{
			Username: "alice", Email: "alice@ex.com",
			Groups: []string{"admins"},
		},
	}
	r, _ := newOIDCHandlerRig(t, mock)

	// /login to get state cookie + state query.
	reqLogin := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login?return_to=/repos", nil)
	wLogin := httptest.NewRecorder()
	r.ServeHTTP(wLogin, reqLogin)
	sealedCookie := extractCookieValue(wLogin.Header().Get("Set-Cookie"), "oidc_state")

	// Parse state from Location.
	loc, _ := url.Parse(wLogin.Header().Get("Location"))
	state := loc.Query().Get("state")
	require.NotEmpty(t, state)

	// Callback.
	reqCb := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?code=abc&state="+state, nil)
	reqCb.AddCookie(&http.Cookie{Name: "oidc_state", Value: sealedCookie})
	wCb := httptest.NewRecorder()
	r.ServeHTTP(wCb, reqCb)

	require.Equal(t, http.StatusFound, wCb.Code)
	redirected, _ := url.Parse(wCb.Header().Get("Location"))
	assert.Equal(t, "/oidc/callback", redirected.Path)
	assert.Contains(t, redirected.Fragment, "token=")
	assert.Contains(t, redirected.Fragment, "return_to=%2Frepos")
}

// extractCookieValue pulls a named cookie value out of a Set-Cookie header.
func extractCookieValue(setCookie, name string) string {
	for _, part := range strings.Split(setCookie, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, name+"=") {
			return strings.TrimPrefix(part, name+"=")
		}
	}
	return ""
}
```

- [ ] **Step 3: Run — verify failure**

```bash
go test ./internal/api/handlers/ -run TestOIDCHandler -run TestIsSafeReturnPath -count=1 -v
```
Expected: compile errors (handlers don't exist).

- [ ] **Step 4: Implement `OIDCHandler`** (`internal/api/handlers/oidc.go`)

```go
package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/service"
)

const (
	oidcStateCookieName = "oidc_state"
	oidcStateTTL        = 10 * time.Minute
	oidcReturnPathMax   = 200
)

type OIDCHandler struct {
	oidc    auth.OIDCAuthenticator
	users   *service.UserService
	sealer  *auth.CookieSealer
	cfg     config.OIDCConfig
	log     logger.Logger
}

func NewOIDCHandler(
	oidc auth.OIDCAuthenticator,
	users *service.UserService,
	sealer *auth.CookieSealer,
	cfg config.OIDCConfig,
	log logger.Logger,
) *OIDCHandler {
	return &OIDCHandler{oidc: oidc, users: users, sealer: sealer, cfg: cfg, log: log}
}

// Login starts the OIDC authorization code + PKCE flow.
// GET /api/v1/auth/oidc/login[?return_to=/path]
func (h *OIDCHandler) Login(c *gin.Context) {
	state := randBase64URL(32)
	nonce := randBase64URL(32)
	codeVerifier := randBase64URL(64)
	hash := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(hash[:])

	returnTo := c.Query("return_to")
	if !IsSafeReturnPath(returnTo) {
		returnTo = "/"
	}

	sealed, err := h.sealer.Seal(auth.StateCookiePayload{
		State: state, Nonce: nonce, CodeVerifier: codeVerifier,
		ReturnTo: returnTo, IssuedAt: time.Now().Unix(),
	})
	if err != nil {
		h.log.Errorw("oidc seal state failed", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oidcStateCookieName, sealed, int(oidcStateTTL.Seconds()),
		"/", "", h.cfg.CookieSecure, true /* httpOnly */)

	c.Redirect(http.StatusFound, h.oidc.AuthCodeURL(state, nonce, codeChallenge))
}

// Callback handles the IdP redirect.
// GET /api/v1/auth/oidc/callback?code=...&state=...
func (h *OIDCHandler) Callback(c *gin.Context) {
	// IdP reported error?
	if e := c.Query("error"); e != "" {
		h.log.Warnw("oidc idp error", "error", e, "description", c.Query("error_description"))
		h.fail(c, "idp error")
		return
	}

	sealed, err := c.Cookie(oidcStateCookieName)
	if err != nil {
		h.fail(c, "missing state")
		return
	}
	// Clear cookie immediately (one-shot).
	c.SetCookie(oidcStateCookieName, "", -1, "/", "", h.cfg.CookieSecure, true)

	payload, err := h.sealer.Open(sealed)
	if err != nil {
		h.fail(c, "invalid state")
		return
	}
	if time.Since(time.Unix(payload.IssuedAt, 0)) > oidcStateTTL {
		h.fail(c, "state expired")
		return
	}
	if c.Query("state") != payload.State {
		h.fail(c, "state mismatch")
		return
	}

	claims, err := h.oidc.ExchangeAndVerify(c.Request.Context(),
		c.Query("code"), payload.CodeVerifier, payload.Nonce)
	if err != nil {
		h.log.Warnw("oidc verify failed", "err", err)
		h.fail(c, "verification failed")
		return
	}

	token, user, err := h.users.LoginOIDC(c.Request.Context(), claims)
	if err != nil {
		h.log.Warnw("oidc login failed", "err", err, "username", claims.Username)
		switch {
		case errors.Is(err, service.ErrProvisioningRejected):
			h.fail(c, "provisioning rejected")
		case errors.Is(err, service.ErrProvisioningConflict):
			h.fail(c, "username conflict")
		default:
			h.fail(c, "login failed")
		}
		return
	}

	// Audit hooks for AuditMiddleware.
	c.Set("username", user.Username)
	c.Set("userID", user.ID)
	c.Set("audit_source", "oidc")
	h.log.Infow("oidc login success", "username", user.Username, "roles", user.Roles,
		"ip", c.ClientIP(), "subject", claims.Subject)

	c.Redirect(http.StatusFound, fmt.Sprintf("%s/oidc/callback#token=%s&return_to=%s",
		strings.TrimRight(h.cfg.FrontendBaseURL, "/"),
		url.QueryEscape(token), url.QueryEscape(payload.ReturnTo)))
}

func (h *OIDCHandler) fail(c *gin.Context, reason string) {
	c.Redirect(http.StatusFound,
		fmt.Sprintf("%s/login?oidc_error=%s",
			strings.TrimRight(h.cfg.FrontendBaseURL, "/"),
			url.QueryEscape(reason)))
}

// IsSafeReturnPath guards against open-redirect and scheme-abuse.
// Accepts only absolute paths within our own app.
func IsSafeReturnPath(p string) bool {
	if p == "" || len(p) > oidcReturnPathMax {
		return false
	}
	if !strings.HasPrefix(p, "/") {
		return false
	}
	if strings.HasPrefix(p, "//") {
		return false // protocol-relative URL
	}
	if strings.ContainsAny(p, " \t\r\n") {
		return false
	}
	// Reject control-character schemes by trying to parse as URL.
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return false
	}
	return true
}

func randBase64URL(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/api/handlers/ -run TestOIDCHandler -run TestIsSafeReturnPath -count=1 -v
```
Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/handlers/oidc.go internal/api/handlers/oidc_test.go
git commit -m "feat(phase28): OIDCHandler Login + Callback with PKCE + AEAD state cookie"
```

---

## Task 8: `AuthHandler.Config` public endpoint

**Files:**
- Modify: `internal/api/handlers/auth.go`
- Modify: `internal/api/handlers/auth_test.go`

- [ ] **Step 1: Write failing test** (append to `internal/api/handlers/auth_test.go`)

```go
func TestAuthConfig_ReturnsOIDCEnabled(t *testing.T) {
	cfg := config.Config{
		OIDC: config.OIDCConfig{Enabled: true, DisplayName: "Keycloak", ShowLoginButton: true},
		LDAP: config.LDAPConfig{Enabled: false},
	}
	h := handlers.NewAuthHandler(nil, zap.NewNop().Sugar()).WithConfig(cfg)

	r := gin.New()
	r.GET("/api/v1/auth/config", h.Config)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"oidcEnabled":true`)
	assert.Contains(t, w.Body.String(), `"oidcDisplayName":"Keycloak"`)
	assert.Contains(t, w.Body.String(), `"oidcLoginUrl":"/api/v1/auth/oidc/login"`)
	assert.Contains(t, w.Body.String(), `"ldapEnabled":false`)
}
```

Add `"github.com/nexspence-oss/nexspence/internal/config"` import to test file if missing.

- [ ] **Step 2: Extend `AuthHandler`** in `internal/api/handlers/auth.go`

After `NewAuthHandler` (line ~19):

```go
// WithConfig enables the /api/v1/auth/config feature-detection endpoint.
// Returns same handler for chaining.
func (h *AuthHandler) WithConfig(cfg config.Config) *AuthHandler {
	h.cfg = cfg
	return h
}

// Config exposes the auth-related UI feature flags (safe to call unauthenticated).
func (h *AuthHandler) Config(c *gin.Context) {
	oidcOn := h.cfg.OIDC.Enabled && h.cfg.OIDC.ShowLoginButton
	c.JSON(http.StatusOK, gin.H{
		"oidcEnabled":     oidcOn,
		"oidcDisplayName": h.cfg.OIDC.DisplayName,
		"oidcLoginUrl":    "/api/v1/auth/oidc/login",
		"ldapEnabled":     h.cfg.LDAP.Enabled,
	})
}
```

Add `cfg config.Config` field to the struct and `"github.com/nexspence-oss/nexspence/internal/config"` import.

- [ ] **Step 3: Run test**

```bash
go test ./internal/api/handlers/ -run TestAuthConfig -count=1 -v
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/api/handlers/auth.go internal/api/handlers/auth_test.go
git commit -m "feat(phase28): AuthHandler.Config — GET /api/v1/auth/config feature-detection"
```

---

## Task 9: Audit middleware — allow OIDC callback GET + source tag

**Files:**
- Modify: `internal/api/audit_middleware.go`
- Modify: `internal/api/audit_middleware_test.go`

- [ ] **Step 1: Write failing test**

Append to `internal/api/audit_middleware_test.go`:

```go
func TestAuditMiddleware_OIDCCallback_WritesLoginEvent(t *testing.T) {
	repo := testutil.NewAuditRepo()
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("username", "alice")
		c.Set("userID", "u1")
		c.Set("audit_source", "oidc")
		c.Next()
	})
	r.Use(AuditMiddleware(repo))
	r.GET("/api/v1/auth/oidc/callback", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?code=x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Give the goroutine a tick to write.
	time.Sleep(20 * time.Millisecond)

	require.Len(t, repo.Events, 1)
	ev := repo.Events[0]
	assert.Equal(t, "LOGIN", ev.Action)
	assert.Equal(t, "alice", ev.Username)
	assert.Equal(t, "oidc", ev.Context["source"])
}
```

- [ ] **Step 2: Edit `internal/api/audit_middleware.go`**

Replace method guard:

```go
method := c.Request.Method
if method != "PUT" && method != "POST" && method != "DELETE" && method != "PATCH" {
    return
}
```

with:

```go
method := c.Request.Method
if method != "PUT" && method != "POST" && method != "DELETE" && method != "PATCH" {
    // Also audit OIDC callback GET as a LOGIN event.
    if !(method == "GET" && strings.HasPrefix(c.Request.URL.Path, "/api/v1/auth/oidc/callback")) {
        return
    }
}
```

After `ctxData := classifyPath(...)` line add:

```go
if src, ok := c.Get("audit_source"); ok {
    if ctxData == nil { ctxData = map[string]any{} }
    ctxData["source"] = src
}
```

In `isAuditablePath`, add `"/api/v1/auth/oidc/callback"` to the prefix list.

Also extend `classifyPath` to recognize the OIDC callback path as a LOGIN action. Find the existing `/api/v1/login` branch and add (same block):

```go
case strings.HasPrefix(path, "/api/v1/auth/oidc/callback"):
    return "security", "LOGIN", "user", usernameFromCtx(c), nil
```

(If `classifyPath` already has an `/api/v1/login` case, follow the same pattern; the goal is to produce `action="LOGIN"`.)

- [ ] **Step 3: Run tests**

```bash
go test ./internal/api/ -run TestAudit -count=1 -v
```
Expected: new test PASSES, no regressions.

- [ ] **Step 4: Commit**

```bash
git add internal/api/audit_middleware.go internal/api/audit_middleware_test.go
git commit -m "feat(phase28): audit middleware — record OIDC callback as LOGIN + source tag"
```

---

## Task 10: Router wiring + `main.go` bootstrap + `config.yaml`

**Files:**
- Modify: `internal/api/router.go`
- Modify: `cmd/server/main.go`
- Modify: `config.yaml`

- [ ] **Step 1: Router wiring** — find where `authH := handlers.NewAuthHandler(...)` is constructed. Edit to:

```go
authH := handlers.NewAuthHandler(userSvc, log).WithConfig(*cfg)
// ...
r.GET("/api/v1/auth/config", authH.Config) // public

// OIDC routes — only register if enabled
if cfg.OIDC.Enabled {
    keyBytes, _ := base64.StdEncoding.DecodeString(cfg.OIDC.CookieKey)
    sealer, err := auth.NewCookieSealer(keyBytes)
    if err != nil {
        return nil, fmt.Errorf("oidc sealer: %w", err)
    }
    oidcH := handlers.NewOIDCHandler(oidcSvc, userSvc, sealer, cfg.OIDC, log)
    // Wrap with AuditMiddleware so callback emits LOGIN events.
    auditRepo := postgres.NewAuditRepo(pool) // reuse existing repo (find the line it's constructed)
    oidcGrp := r.Group("/api/v1/auth/oidc", AuditMiddleware(auditRepo))
    oidcGrp.GET("/login", oidcH.Login)
    oidcGrp.GET("/callback", oidcH.Callback)
}
```

(Adjust variable names `oidcSvc`, `pool`, `auditRepo` to whatever the router currently uses.)

Add required imports: `"encoding/base64"`, `"github.com/nexspence-oss/nexspence/internal/auth"`.

- [ ] **Step 2: `main.go` bootstrap** — find where `ldapSvc := auth.NewLDAPService(cfg.LDAP)` lives. Alongside it:

```go
var oidcSvc auth.OIDCAuthenticator
if cfg.OIDC.Enabled {
    svc, err := auth.NewOIDCService(ctx, cfg.OIDC)
    if err != nil {
        return fmt.Errorf("oidc init: %w", err)
    }
    oidcSvc = svc
    if err := svc.TestConnection(ctx); err != nil {
        log.Warnw("oidc discovery test failed", "err", err)
    } else {
        log.Infow("oidc discovery ok", "issuer", cfg.OIDC.Issuer, "display", cfg.OIDC.DisplayName)
    }
}
```

Pass `oidcSvc` into `NewRouter(..., oidcSvc, ...)` — update the router signature to accept it.

In `NewUserService(...)` chain, add `.WithOIDC(oidcSvc, cfg.OIDC)` if enabled:

```go
userSvc := service.NewUserService(userRepo, roleRepo, authSvc, log)
if ldapSvc != nil { userSvc = userSvc.WithLDAP(ldapSvc, cfg.LDAP.AdminGroup) }
if oidcSvc != nil { userSvc = userSvc.WithOIDC(oidcSvc, cfg.OIDC) }
```

- [ ] **Step 3: `config.yaml` default block** — append after the existing `ldap:` section:

```yaml
oidc:
  enabled: false
  display_name: "SSO"
  issuer: ""
  client_id: ""
  client_secret: "${OIDC_CLIENT_SECRET}"
  redirect_url: ""
  frontend_base_url: ""
  scopes: ["openid", "profile", "email", "groups"]

  provisioning: "jit"
  email_allowlist: []

  groups_claim: "groups"
  admin_group: ""
  role_mappings: {}

  username_claim: "preferred_username"
  email_claim: "email"
  name_claim: "name"

  show_login_button: true
  cookie_secure: true
  cookie_key: "${OIDC_COOKIE_KEY}"
  allowed_skew_seconds: 60
```

- [ ] **Step 4: Build + full test run**

```bash
go build ./... && go test ./internal/... -count=1
```
Expected: clean build, all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/api/router.go cmd/server/main.go config.yaml
git commit -m "feat(phase28): wire OIDC routes, bootstrap OIDCService in main, default config"
```

---

## Task 11: Makefile `oidc-secret` target

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Append target**

```makefile
.PHONY: oidc-secret
oidc-secret:
	@openssl rand -base64 32
```

- [ ] **Step 2: Verify**

```bash
make oidc-secret | wc -c
```
Expected: 45 (32 bytes base64 + newline).

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "feat(phase28): make oidc-secret for generating 32-byte cookie key"
```

---

## Task 12: Frontend — `client.ts` + `LoginPage.tsx` button + `OIDCCallbackPage.tsx` + `App.tsx` route

**Files:**
- Modify: `frontend/src/api/client.ts`
- Modify: `frontend/src/pages/LoginPage.tsx`
- Create: `frontend/src/pages/OIDCCallbackPage.tsx`
- Modify: `frontend/src/App.tsx`

- [ ] **Step 1: Extend `client.ts`**

```ts
export interface AuthConfig {
  oidcEnabled: boolean
  oidcDisplayName: string
  oidcLoginUrl: string
  ldapEnabled: boolean
}

// In nexspenceApi object:
async getAuthConfig(): Promise<AuthConfig> {
  const { data } = await apiClient.get<AuthConfig>('/api/v1/auth/config')
  return data
},
```

- [ ] **Step 2: Edit `LoginPage.tsx`**

Near top, imports:

```tsx
import { KeyRound } from 'lucide-react'
import { nexspenceApi, type AuthConfig } from '@/api/client'
```

State + effect additions:

```tsx
const [authConfig, setAuthConfig] = useState<AuthConfig | null>(null)
const [oidcError, setOidcError] = useState<string | null>(null)

useEffect(() => {
  const params = new URLSearchParams(window.location.search)
  const err = params.get('oidc_error')
  if (err) setOidcError(err)
  nexspenceApi.getAuthConfig().then(setAuthConfig).catch(() => setAuthConfig(null))
}, [])
```

After the existing submit button (inside the `<form>`), add:

```tsx
{authConfig?.oidcEnabled && (
  <>
    <div className={S.divider}>or</div>
    <button
      type="button"
      className={S.oidcButton}
      onClick={() => {
        const r = encodeURIComponent(location.pathname + location.search)
        window.location.href = authConfig.oidcLoginUrl + '?return_to=' + r
      }}
    >
      <KeyRound size={16} /> Sign in with {authConfig.oidcDisplayName}
    </button>
  </>
)}
{oidcError && (
  <div className={S.errorBanner} role="alert">
    OIDC login failed: {oidcError}
  </div>
)}
```

CSS module additions (`LoginPage.module.css` or inline tokens matching existing glassmorphism tokens):

```css
.divider { margin: 12px 0; text-align: center; color: #6b7380; font-size: 12px; }
.oidcButton {
  width: 100%; display: flex; align-items: center; justify-content: center; gap: 8px;
  padding: 10px 14px; border-radius: 12px; border: 1px solid rgba(59,130,246,0.4);
  background: rgba(59,130,246,0.1); color: #dbe4f2; cursor: pointer;
}
.oidcButton:hover { background: rgba(59,130,246,0.2); }
.errorBanner {
  margin-top: 12px; padding: 10px 12px; border-radius: 10px;
  background: rgba(239,68,68,0.15); border: 1px solid rgba(239,68,68,0.4);
  color: #fecaca; font-size: 13px;
}
```

- [ ] **Step 3: Create `OIDCCallbackPage.tsx`**

```tsx
import { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuthStore } from '@/store/authStore'

export default function OIDCCallbackPage() {
  const navigate = useNavigate()
  const init = useAuthStore(s => s.init)

  useEffect(() => {
    const hash = new URLSearchParams(window.location.hash.slice(1))
    const token = hash.get('token')
    const returnTo = hash.get('return_to') || '/'

    if (!token) {
      navigate('/login?oidc_error=missing+token', { replace: true })
      return
    }

    localStorage.setItem('nexspence_token', token)
    window.history.replaceState(null, '', returnTo)
    init()
      .then(() => navigate(returnTo, { replace: true }))
      .catch(() => navigate('/login?oidc_error=session+init+failed', { replace: true }))
  }, [init, navigate])

  return (
    <div style={{
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      minHeight: '100vh', color: '#dbe4f2',
    }}>
      Finishing sign-in…
    </div>
  )
}
```

- [ ] **Step 4: Edit `App.tsx`** — add the public route

```tsx
import OIDCCallbackPage from './pages/OIDCCallbackPage'

// Inside <Routes> — outside any PrivateRoute guard (sibling to /login):
<Route path="/oidc/callback" element={<OIDCCallbackPage />} />
```

- [ ] **Step 5: TypeScript check**

```bash
cd frontend && npx tsc --noEmit
```
Expected: 0 errors.

- [ ] **Step 6: Build frontend**

```bash
cd frontend && npm run build
```
Expected: successful build.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/api/client.ts frontend/src/pages/LoginPage.tsx \
        frontend/src/pages/OIDCCallbackPage.tsx frontend/src/App.tsx
git commit -m "feat(phase28): frontend OIDC login button + /oidc/callback page"
```

Note: `authStore.init` must already exist (confirmed via memory/phases). If not, the engineer adds a thin `init()` that calls `nexspenceApi.getMe()` and hydrates `user` + `token` fields; the method is documented as already-existing from Phase 6 BUG-18 fix.

---

## Task 13: `docs/oidc-setup.md` — provider presets

**Files:**
- Create: `docs/oidc-setup.md`

- [ ] **Step 1: Write the doc**

```markdown
# OIDC / OAuth2 SSO setup

Nexspence supports any OIDC-compliant Identity Provider. This guide covers
the four most common: Keycloak, Google Workspace, Microsoft Entra ID, Okta.

## Common steps

1. Register Nexspence as an application / client on your IdP.
2. Set the **redirect URI** to `https://<nexspence-host>/api/v1/auth/oidc/callback`.
3. Generate an application secret.
4. Generate a 32-byte cookie key: `make oidc-secret`.
5. Set env vars: `OIDC_CLIENT_SECRET`, `OIDC_COOKIE_KEY`.
6. Edit `config.yaml` `oidc:` block and set `enabled: true`.
7. Restart Nexspence. You should see `oidc discovery ok` in the startup log.

## Keycloak

```yaml
oidc:
  enabled: true
  display_name: "Keycloak"
  issuer: "https://kc.example.com/realms/nexspence"
  client_id: "nexspence"
  client_secret: "${OIDC_CLIENT_SECRET}"
  redirect_url: "https://nexspence.example.com/api/v1/auth/oidc/callback"
  frontend_base_url: "https://nexspence.example.com"
  scopes: ["openid", "profile", "email", "groups"]
  groups_claim: "groups"
  admin_group: "nexspence-admins"
```

In Keycloak: Client Scopes → `groups` → create a Group Membership mapper named
`groups`, full path off. Assign to your client.

## Google Workspace

Google **does not emit group membership in id_token** by default. Two options:

- **Allowlist mode** (simplest): rely on email domain.
- **Admin SDK integration**: out of Phase 28 scope.

```yaml
oidc:
  enabled: true
  display_name: "Google"
  issuer: "https://accounts.google.com"
  client_id: "<your-google-oauth-client>.apps.googleusercontent.com"
  client_secret: "${OIDC_CLIENT_SECRET}"
  redirect_url: "https://nexspence.example.com/api/v1/auth/oidc/callback"
  frontend_base_url: "https://nexspence.example.com"
  scopes: ["openid", "profile", "email"]
  provisioning: "allowlist"
  email_allowlist: ["*@company.com"]
  username_claim: "email"    # Google has no preferred_username
  groups_claim: ""           # no group claim
```

Roles for Google users: assign manually in Security → Roles, or use Entra / Keycloak
with Google as a federation source.

## Microsoft Entra ID (Azure AD)

Entra emits app-role claims under `roles`, not `groups`. Configure App Registration →
Expose an API → App roles, then assign users to roles.

```yaml
oidc:
  enabled: true
  display_name: "Microsoft"
  issuer: "https://login.microsoftonline.com/<tenant-id>/v2.0"
  client_id: "<app-registration-client-id>"
  client_secret: "${OIDC_CLIENT_SECRET}"
  redirect_url: "https://nexspence.example.com/api/v1/auth/oidc/callback"
  frontend_base_url: "https://nexspence.example.com"
  scopes: ["openid", "profile", "email"]
  groups_claim: "roles"       # Entra app-roles → claim
  role_mappings:
    "NexspenceAdmin": "nx-admin"
    "NexspenceDev": "release-manager"
```

If you prefer group-based mapping, add the `groups` optional claim to the app
registration and keep `groups_claim: "groups"`.

## Okta

```yaml
oidc:
  enabled: true
  display_name: "Okta"
  issuer: "https://<org>.okta.com/oauth2/default"
  client_id: "<okta-app-client-id>"
  client_secret: "${OIDC_CLIENT_SECRET}"
  redirect_url: "https://nexspence.example.com/api/v1/auth/oidc/callback"
  frontend_base_url: "https://nexspence.example.com"
  scopes: ["openid", "profile", "email", "groups"]
  groups_claim: "groups"
  admin_group: "nexspence-admins"
```

In Okta: Authorization Servers → default → Claims → Add Claim `groups`, type
Groups, filter `Matches regex: .*`.

## Local testing with Keycloak + docker-compose

```yaml
# docker-compose.oidc.yml (dev only)
services:
  keycloak:
    image: quay.io/keycloak/keycloak:24.0
    command: start-dev
    environment:
      KEYCLOAK_ADMIN: admin
      KEYCLOAK_ADMIN_PASSWORD: admin
    ports: ["8180:8080"]
```

Run: `docker compose -f docker-compose.yml -f docker-compose.oidc.yml up`.

Import a realm JSON or configure manually: create realm `nexspence`, client
`nexspence` (confidential, redirect `http://localhost:8081/api/v1/auth/oidc/callback`),
user with group `nexspence-admins`.

Set in `config.yaml`:

```yaml
oidc:
  issuer: "http://keycloak:8080/realms/nexspence"  # inside compose network
  cookie_secure: false  # dev only — HTTP
```

## Troubleshooting

- **"oidc discovery failed" on startup** — check `issuer` URL; must be reachable
  from Nexspence container and must serve `.well-known/openid-configuration`.
- **Redirect loop / state mismatch** — verify `cookie_secure` matches your scheme
  (false for HTTP, true for HTTPS).
- **"provisioning rejected"** (UI banner) — check `provisioning`. For allowlist
  mode, the user's email must match a pattern in `email_allowlist`.
- **No roles assigned** — check claim name (`groups_claim`), confirm the IdP emits
  it (decode id_token at jwt.io), confirm `role_mappings` values exist as role
  names in Nexspence.
```

- [ ] **Step 2: Commit**

```bash
git add docs/oidc-setup.md
git commit -m "docs(phase28): OIDC setup guide — Keycloak / Google / Entra / Okta"
```

---

## Task 14: Finalize — manual verification, update plan/progress/findings/memory

**Files:**
- Modify: `task_plan.md`, `progress.md`, `findings.md`
- Modify: `/home/skensel/.claude/projects/-home-skensel-AI-self-nexus/memory/project_phase_status.md`
- Modify: `/home/skensel/.claude/projects/-home-skensel-AI-self-nexus/memory/MEMORY.md`

- [ ] **Step 1: Full test sweep**

```bash
go build ./... && go vet ./... && go test ./internal/... -count=1 | tail -5
```
Expected: clean build, clean vet, all tests pass (should be 271 baseline + N new — report exact count).

- [ ] **Step 2: Frontend final check**

```bash
cd frontend && npx tsc --noEmit && npm run build
```
Expected: 0 TS errors, successful build.

- [ ] **Step 3: Manual verification (Keycloak)**

Run through the checklist in spec section 8.6:
- [ ] JIT creation + admin_group → nx-admin
- [ ] User without admin_group → no roles
- [ ] role_mappings for developers → release-manager granted
- [ ] Remove claim → role removed on next login
- [ ] provisioning: manual → rejected with error banner
- [ ] Username conflict (existing source=local) → rejected
- [ ] Audit log has LOGIN entry with `source: oidc` in Context

If compose-stack or Keycloak instance is unavailable, mark as deferred in the phase-complete summary — the test suite covers code-level behavior.

- [ ] **Step 4: Update `task_plan.md`** — append new phase block:

```markdown
## Phase 28: OIDC / OAuth2 SSO
**Status:** complete (2026-04-XX — fill in actual completion date)
**Goal:** First-class OIDC SSO with Keycloak / Google / Entra / Okta support.

### Done
- [x] `internal/auth/oidc.go` — OIDCAuthenticator interface + OIDCService using go-oidc/v3
- [x] `internal/auth/oidc_cookie.go` — AES-256-GCM state cookie sealer
- [x] `internal/config/config.go` — OIDCConfig + validation
- [x] `internal/service/user_service.go` — WithOIDC + LoginOIDC + syncOIDCRoles (REPLACE semantics)
- [x] `internal/api/handlers/oidc.go` — Login / Callback with PKCE + state CSRF
- [x] `internal/api/handlers/auth.go` — public /api/v1/auth/config
- [x] `internal/api/audit_middleware.go` — records OIDC callback GET as LOGIN + source tag
- [x] `internal/api/router.go` — conditional route registration when cfg.OIDC.Enabled
- [x] `cmd/server/main.go` — bootstrap + TestConnection startup log
- [x] `frontend/src/pages/LoginPage.tsx` — "Sign in with X" button + error banner
- [x] `frontend/src/pages/OIDCCallbackPage.tsx` — fragment → localStorage → authStore.init
- [x] `docs/oidc-setup.md` — 4 provider presets + docker-compose dev setup
- [x] `config.yaml` — default `oidc:` block (disabled)
- [x] `Makefile oidc-secret` target
- [x] NNN tests passing (was 271; +NN new)

### Out of scope (follow-ups)
- Phase 28.1 — Single Logout (SLO) via end_session_endpoint
- Phase 28.2 — Refresh-token storage + silent session renewal
- Phase 28.3 — Multi-provider support
```

- [ ] **Step 5: Update `progress.md`** — prepend session entry (use pattern from Phase 26 session):

```markdown
## Session: 2026-04-XX — Phase 28: OIDC / OAuth2 SSO (complete)

### Scope
Authorization code + PKCE flow, single-provider, JIT/allowlist/manual
provisioning, admin_group + role_mappings role resolution, fragment-based
JWT delivery, parallel with local + LDAP.

### Key decisions (see spec)
- Mirror LDAP pattern: parallel `internal/auth/oidc.go` + `WithOIDC` builder.
- IdP is source of truth for roles — syncOIDCRoles REPLACES user's roles,
  doesn't merge. This guarantees IdP-removed permissions propagate on next login.
- Fragment-based token (`#token=…`) so JWT never hits Referer / access logs.
- AEAD-sealed state cookie (AES-256-GCM); 10min TTL, one-shot on callback.
- `coreos/go-oidc/v3` library handles JWKS rotation, id_token verification,
  discovery. No hand-rolled crypto.

### Files
[list as in task_plan.md]

### Verification
- NNN tests pass (+NN new from 271).
- go vet clean, tsc clean, frontend build clean.
- Manual Keycloak verification: [status]
```

- [ ] **Step 6: Update `findings.md`** — prepend gotchas learned:

```markdown
## Gotcha — OIDC role sync must REPLACE, not merge (Phase 28)

syncOIDCRoles uses SetUserRoles (REPLACE), not append. Rationale: if an admin
is removed from `nexspence-admins` in Entra and the code merged, the local
Nexspence grant would never drop. Merging breaks the "IdP source of truth"
contract and creates GDPR / offboarding risk. Trade-off: admins cannot
manually grant extra roles to OIDC users that aren't in the claim mapping —
any such grant is erased on next login. Document this in the Security page
hint text when an OIDC user is viewed.

## Gotcha — Google id_token has no `preferred_username` (Phase 28)

Google Workspace OIDC tokens lack `preferred_username`. For Google IdPs set
`username_claim: "email"`. This affects username uniqueness: two users with
the same email prefix on different domains would have same `email`, but email
is guaranteed globally unique so this is fine.

## Gotcha — Entra emits app-roles as `roles` claim, not `groups` (Phase 28)

Microsoft Entra's app-role assignments land in the `roles` claim, not `groups`.
For Entra set `groups_claim: "roles"`, or add an optional `groups` claim to
the app registration. Group-based mapping requires manifest editing
(`groupMembershipClaims: "SecurityGroup"`). Documented in oidc-setup.md.
```

- [ ] **Step 7: Update `CLAUDE.md`** — replace "Current Phase" paragraph (Phase 26 → Phase 28 summary, a few lines).

- [ ] **Step 8: Update memory files**

In `project_phase_status.md`, add:

```markdown
- **Phase 28**: complete (2026-04-XX) — OIDC/OAuth2 SSO. Single provider (Keycloak/Google/Entra/Okta) via github.com/coreos/go-oidc/v3. Authorization code + PKCE; AEAD-sealed state cookie (AES-256-GCM, 10m TTL, one-shot); fragment-based JWT delivery so token never hits Referer/logs. UserService.WithOIDC + LoginOIDC parallel to LDAP; UserSourceOIDC enum. Provisioning modes: jit (default) / allowlist (email glob) / manual. Role resolution: admin_group (→ nx-admin) + role_mappings (claim value → role name); groups_claim configurable (Google: empty, Entra: "roles"). syncOIDCRoles REPLACES — IdP is source of truth. Frontend: "Sign in with X" button + /oidc/callback page. NNN tests pass (+NN new).
```

Update MEMORY.md:
```markdown
- [Phase completion status](project_phase_status.md) — which phases are done (1–28 complete as of 2026-04-XX)
```

- [ ] **Step 9: Commit everything**

```bash
git add task_plan.md progress.md findings.md CLAUDE.md
git commit -m "docs(phase28): mark Phase 28 complete — OIDC SSO"
```

```bash
# memory (separate commit / outside working tree)
git -C ~/.claude/projects/-home-skensel-AI-self-nexus/memory add MEMORY.md project_phase_status.md 2>/dev/null || true
```

(Memory files live outside the repo; update in place.)

---

## Self-review

**Spec coverage check:**

| Spec section | Covered by task |
|---|---|
| §1 Decisions (6 answered) | All — design reflected across Tasks 2, 6, 7, 12 |
| §2.1 File layout | Task headers enumerate every file |
| §2.2 Data flow (authz code + PKCE) | Tasks 5, 7 |
| §2.3 Security properties | Task 4 (AEAD), Task 7 (Secure cookie, one-shot, return-path guard, fragment) |
| §3 Interfaces + types | Task 5 (`OIDCAuthenticator`, `OIDCClaims`, `OIDCService`), Task 3 (sentinels) |
| §4 Config schema | Task 2 (struct + validation), Task 10 Step 3 (default yaml block) |
| §4.1 Validation rules | Task 2 Step 4 |
| §5 Login/Callback handlers | Task 7 |
| §6 UserService.LoginOIDC | Task 6 |
| §6.1 Provisioning switch | Task 6 Step 5 |
| §6.2 Group matching | Task 6 Step 5 (`ldapGroupMatch` reuse) |
| §7 Frontend | Task 12 |
| §8 Testing (unit + handler + config) | Tasks 4, 5, 6, 7, 8 |
| §8.6 Manual verification | Task 14 Step 3 |
| §9 Deliverables | All 15 rows present across Tasks 1-13 |
| §10 Out-of-scope | Task 14 Step 4 (listed in task_plan.md phase block) |
| §11 No open questions | — |
| §12 Risks | Task 14 Step 6 (findings.md gotchas) |

**Placeholder scan:** No `TBD` / `TODO` / vague instructions. Each step has concrete code or exact commands.

**Type consistency:** `OIDCAuthenticator`, `OIDCClaims`, `OIDCService`, `OIDCConfig`, `OIDCHandler`, `StateCookiePayload`, `CookieSealer`, `UserSourceOIDC`, `ErrProvisioningRejected`, `ErrProvisioningConflict` — used consistently across tasks. Method signatures match between definition and callers (`ExchangeAndVerify(ctx, code, codeVerifier, expectedNonce)`, `Seal/Open`, `LoginOIDC(ctx, *OIDCClaims)`).

**Scope check:** Single phase, single IdP, single implementation session. Deferred items (SLO, refresh, multi-provider) explicit. 14 tasks with bite-sized TDD steps — executable in 1-2 focused days.

No issues found inline.
