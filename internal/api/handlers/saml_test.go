package handlers_test

import (
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

// mockSAMLAuthenticator satisfies auth.SAMLAuthenticator.
type mockSAMLAuthenticator struct {
	metaXML   []byte
	metaErr   error
	authnURL  string
	authnErr  error
	claims    *auth.SAMLClaims
	parseErr  error
	returnTo  string
	verifyErr error
}

func (m *mockSAMLAuthenticator) MetadataXML() ([]byte, error) {
	return m.metaXML, m.metaErr
}
func (m *mockSAMLAuthenticator) AuthnRequestURL(rs string) (string, error) {
	return m.authnURL, m.authnErr
}
func (m *mockSAMLAuthenticator) ParseResponse(r *http.Request) (*auth.SAMLClaims, error) {
	return m.claims, m.parseErr
}
func (m *mockSAMLAuthenticator) SignRelayState(returnTo string) string {
	return returnTo
}
func (m *mockSAMLAuthenticator) VerifyRelayState(rs string) (string, error) {
	if m.verifyErr != nil {
		return "", m.verifyErr
	}
	if m.returnTo != "" {
		return m.returnTo, nil
	}
	return rs, nil
}

func samlTestCfg() config.SAMLConfig {
	return config.SAMLConfig{
		Enabled:         true,
		DisplayName:     "Test IdP",
		FrontendBaseURL: "https://app",
		Provisioning:    "jit",
		AdminGroup:      "admins",
	}
}

func newSAMLRig(t *testing.T, mock *mockSAMLAuthenticator) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := samlTestCfg()
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo(
		&domain.Role{ID: "ra", Name: "nx-admin"},
	)
	authSvc := auth.NewService("test-secret-samlrig", 24, 4)
	userSvc := service.NewUserService(users, roles, authSvc, zap.NewNop().Sugar()).
		WithSAML(mock, cfg)
	h := handlers.NewSAMLHandler(mock, userSvc, cfg, zap.NewNop().Sugar())
	r := gin.New()
	r.GET("/api/v1/auth/saml/metadata", h.Metadata)
	r.GET("/api/v1/auth/saml/login", h.Login)
	r.POST("/api/v1/auth/saml/acs", h.ACS)
	return r
}

// ── Metadata ───────────────────────────────────────────────────

func TestSAMLHandler_Metadata_ReturnsXML(t *testing.T) {
	mock := &mockSAMLAuthenticator{metaXML: []byte("<EntityDescriptor/>")}
	r := newSAMLRig(t, mock)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/saml/metadata", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/xml")
	assert.Equal(t, "<EntityDescriptor/>", w.Body.String())
}

func TestSAMLHandler_Metadata_Error_Returns500(t *testing.T) {
	mock := &mockSAMLAuthenticator{metaErr: assert.AnError}
	r := newSAMLRig(t, mock)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/saml/metadata", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ── Login ──────────────────────────────────────────────────────

func TestSAMLHandler_Login_RedirectsToIdP(t *testing.T) {
	mock := &mockSAMLAuthenticator{authnURL: "https://idp.example.com/sso?SAMLRequest=x"}
	r := newSAMLRig(t, mock)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/saml/login?return_to=/repos", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "idp.example.com")
	assert.Contains(t, w.Header().Get("Location"), "SAMLRequest")
}

// ── ACS ───────────────────────────────────────────────────────

func TestSAMLHandler_ACS_ValidAssertion_RedirectsWithToken(t *testing.T) {
	mock := &mockSAMLAuthenticator{
		claims: &auth.SAMLClaims{
			Subject:  "alice@idp",
			Username: "alice",
			Email:    "alice@ex.com",
			Name:     "Alice",
			Groups:   []string{},
		},
		returnTo: "/repos",
	}
	r := newSAMLRig(t, mock)

	form := url.Values{}
	form.Set("SAMLResponse", "base64encodedresponse")
	form.Set("RelayState", "/repos")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/saml/acs",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusFound, w.Code)
	loc := w.Header().Get("Location")
	assert.Contains(t, loc, "/saml/callback#token=")
	assert.Contains(t, loc, "return_to=")
}

func TestSAMLHandler_ACS_InvalidAssertion_RedirectsToLoginWithError(t *testing.T) {
	mock := &mockSAMLAuthenticator{parseErr: assert.AnError}
	r := newSAMLRig(t, mock)

	form := url.Values{}
	form.Set("SAMLResponse", "bad")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/saml/acs",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "/login?saml_error=")
}

func TestSAMLHandler_ACS_ProvisioningRejected_RedirectsWithError(t *testing.T) {
	mock := &mockSAMLAuthenticator{
		claims: &auth.SAMLClaims{
			Username: "blocked",
			Email:    "blocked@evil.io",
		},
	}
	gin.SetMode(gin.TestMode)
	cfg := samlTestCfg()
	cfg.Provisioning = "allowlist"
	cfg.EmailAllowlist = []string{"*@allowed.com"}
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo()
	authSvc := auth.NewService("test-secret-prov", 24, 4)
	userSvc := service.NewUserService(users, roles, authSvc, zap.NewNop().Sugar()).
		WithSAML(mock, cfg)
	h := handlers.NewSAMLHandler(mock, userSvc, cfg, zap.NewNop().Sugar())
	r2 := gin.New()
	r2.POST("/api/v1/auth/saml/acs", h.ACS)

	form := url.Values{}
	form.Set("SAMLResponse", "x")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/saml/acs",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r2.ServeHTTP(w, req)

	require.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "saml_error=")
}
