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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ── IsSafeReturnPath table-driven ─────────────────────────────

func TestIsSafeReturnPath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"/", true},
		{"/repos", true},
		{"/repos/a/b", true},
		{"/repos/a/b?q=1", true},
		{"//evil.com/x", false},
		{"http://evil.com", false},
		{"https://good.com/x", false},
		{"javascript:alert(1)", false},
		{"data:text/html,x", false},
		{"/" + strings.Repeat("a", 300), false},
		{"/with space", false},
		{"/with\ttab", false},
	}
	for _, tc := range cases {
		got := handlers.IsSafeReturnPath(tc.in)
		assert.Equal(t, tc.want, got, "input=%q", tc.in)
	}
}

// ── OIDCHandler test rig ──────────────────────────────────────

func validCookieKey32() string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i + 2)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func oidcTestCfg() config.OIDCConfig {
	return config.OIDCConfig{
		Enabled:         true,
		DisplayName:     "TestIdP",
		Issuer:          "https://idp",
		ClientID:        "client",
		ClientSecret:    "s",
		RedirectURL:     "https://app/cb",
		FrontendBaseURL: "https://app",
		CookieKey:       validCookieKey32(),
		Provisioning:    "jit",
		UsernameClaim:   "preferred_username",
		EmailClaim:      "email",
		NameClaim:       "name",
		GroupsClaim:     "groups",
		AdminGroup:      "admins",
		CookieSecure:    false, // test over httptest.Server (http://)
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
func (m *mockOIDCAuthenticator) ExchangeAndVerify(ctx context.Context, code, v, n string) (*auth.OIDCClaims, string, error) {
	if m.err != nil {
		return nil, "", m.err
	}
	return m.claims, "fake-id-token", nil
}
func (m *mockOIDCAuthenticator) TestConnection(ctx context.Context) error { return nil }
func (m *mockOIDCAuthenticator) EndSessionEndpoint() string               { return "" }

func mustDecodeB64(s string) []byte {
	b, _ := base64.StdEncoding.DecodeString(s)
	return b
}

func newOIDCHandlerRig(t *testing.T, mock *mockOIDCAuthenticator) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := oidcTestCfg()
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo(
		&domain.Role{ID: "ra", Name: "nx-admin"},
	)
	authSvc := auth.NewService("test-secret-xyzzy", 24, 4)
	userSvc := service.NewUserService(users, roles, authSvc, zap.NewNop().Sugar()).
		WithOIDC(mock, cfg)

	sealer, err := auth.NewCookieSealer(mustDecodeB64(cfg.CookieKey))
	require.NoError(t, err)
	h := handlers.NewOIDCHandler(mock, userSvc, users, sealer, cfg, zap.NewNop().Sugar())

	r := gin.New()
	r.GET("/api/v1/auth/oidc/login", h.Login)
	r.GET("/api/v1/auth/oidc/callback", h.Callback)
	return r
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

// ── Tests ─────────────────────────────────────────────────────

func TestOIDCHandler_Login_SetsStateCookie_AndRedirects(t *testing.T) {
	r := newOIDCHandlerRig(t, &mockOIDCAuthenticator{})

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

func TestOIDCHandler_Callback_MissingCookie_Redirects(t *testing.T) {
	r := newOIDCHandlerRig(t, &mockOIDCAuthenticator{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?code=x&state=s", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusFound, w.Code)
	loc, _ := url.Parse(w.Header().Get("Location"))
	assert.Equal(t, "/login", loc.Path)
	assert.Equal(t, "missing state", loc.Query().Get("oidc_error"))
}

func TestOIDCHandler_Callback_StateMismatch_Redirects(t *testing.T) {
	r := newOIDCHandlerRig(t, &mockOIDCAuthenticator{})

	// Login to obtain a valid sealed cookie.
	reqLogin := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login", nil)
	wLogin := httptest.NewRecorder()
	r.ServeHTTP(wLogin, reqLogin)
	sealed := extractCookieValue(wLogin.Header().Get("Set-Cookie"), "oidc_state")
	require.NotEmpty(t, sealed)

	// Callback with mismatched state in query.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/callback?code=x&state=WRONG", nil)
	req.AddCookie(&http.Cookie{Name: "oidc_state", Value: sealed})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusFound, w.Code)
	loc, _ := url.Parse(w.Header().Get("Location"))
	assert.Equal(t, "/login", loc.Path)
	assert.Contains(t, loc.Query().Get("oidc_error"), "state")
}

func TestOIDCHandler_Callback_IdPError_Redirects(t *testing.T) {
	r := newOIDCHandlerRig(t, &mockOIDCAuthenticator{})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/oidc/callback?error=access_denied&error_description=user+canceled", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusFound, w.Code)
	loc, _ := url.Parse(w.Header().Get("Location"))
	assert.Equal(t, "/login", loc.Path)
	assert.Equal(t, "idp error", loc.Query().Get("oidc_error"))
}

func TestOIDCHandler_Callback_HappyPath_RedirectsWithToken(t *testing.T) {
	mock := &mockOIDCAuthenticator{
		claims: &auth.OIDCClaims{
			Username: "alice",
			Email:    "alice@ex.com",
			Groups:   []string{"admins"},
		},
	}
	r := newOIDCHandlerRig(t, mock)

	// Login to get state cookie + state in query.
	reqLogin := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login?return_to=/repos", nil)
	wLogin := httptest.NewRecorder()
	r.ServeHTTP(wLogin, reqLogin)
	sealed := extractCookieValue(wLogin.Header().Get("Set-Cookie"), "oidc_state")

	authURL, _ := url.Parse(wLogin.Header().Get("Location"))
	state := authURL.Query().Get("state")
	require.NotEmpty(t, state)

	// Callback.
	reqCb := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/oidc/callback?code=abc&state="+state, nil)
	reqCb.AddCookie(&http.Cookie{Name: "oidc_state", Value: sealed})
	wCb := httptest.NewRecorder()
	r.ServeHTTP(wCb, reqCb)

	require.Equal(t, http.StatusFound, wCb.Code)
	redirected, _ := url.Parse(wCb.Header().Get("Location"))
	assert.Equal(t, "app", redirected.Host)
	assert.Equal(t, "/oidc/callback", redirected.Path)
	assert.Contains(t, redirected.Fragment, "token=")
	// return_to preserved through the flow (Fragment is decoded by url.Parse).
	assert.Contains(t, redirected.Fragment, "return_to=/repos")
}

func TestOIDCHandler_Callback_ProvisioningRejected_Redirects(t *testing.T) {
	// Manual mode: any new user → rejected.
	mock := &mockOIDCAuthenticator{
		claims: &auth.OIDCClaims{Username: "bob", Email: "bob@ex.com"},
	}
	gin.SetMode(gin.TestMode)
	cfg := oidcTestCfg()
	cfg.Provisioning = "manual"

	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo()
	authSvc := auth.NewService("secret", 24, 4)
	userSvc := service.NewUserService(users, roles, authSvc, zap.NewNop().Sugar()).
		WithOIDC(mock, cfg)

	sealer, err := auth.NewCookieSealer(mustDecodeB64(cfg.CookieKey))
	require.NoError(t, err)
	h := handlers.NewOIDCHandler(mock, userSvc, users, sealer, cfg, zap.NewNop().Sugar())

	r := gin.New()
	r.GET("/api/v1/auth/oidc/login", h.Login)
	r.GET("/api/v1/auth/oidc/callback", h.Callback)

	// Login.
	reqLogin := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/login", nil)
	wLogin := httptest.NewRecorder()
	r.ServeHTTP(wLogin, reqLogin)
	sealed := extractCookieValue(wLogin.Header().Get("Set-Cookie"), "oidc_state")
	state := mustParseState(t, wLogin.Header().Get("Location"))

	// Callback.
	reqCb := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/oidc/callback?code=c&state="+state, nil)
	reqCb.AddCookie(&http.Cookie{Name: "oidc_state", Value: sealed})
	wCb := httptest.NewRecorder()
	r.ServeHTTP(wCb, reqCb)

	require.Equal(t, http.StatusFound, wCb.Code)
	redirected, _ := url.Parse(wCb.Header().Get("Location"))
	assert.Equal(t, "/login", redirected.Path)
	assert.Equal(t, "provisioning rejected", redirected.Query().Get("oidc_error"))
}

func mustParseState(t *testing.T, loc string) string {
	u, err := url.Parse(loc)
	require.NoError(t, err)
	return u.Query().Get("state")
}
