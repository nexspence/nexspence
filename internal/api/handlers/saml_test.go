package handlers_test

import (
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

	requestIDErr  error
	gotRequestIDs []string
}

func (m *mockSAMLAuthenticator) MetadataXML() ([]byte, error) {
	return m.metaXML, m.metaErr
}
func (m *mockSAMLAuthenticator) AuthnRequest(rs string) (string, string, error) {
	return m.authnURL, "id-mock-request", m.authnErr
}
func (m *mockSAMLAuthenticator) ParseResponse(r *http.Request, ids []string) (*auth.SAMLClaims, error) {
	m.gotRequestIDs = ids
	return m.claims, m.parseErr
}
func (m *mockSAMLAuthenticator) SignRequestID(id string) string { return "signed:" + id }
func (m *mockSAMLAuthenticator) VerifyRequestID(v string) (string, error) {
	if m.requestIDErr != nil {
		return "", m.requestIDErr
	}
	return strings.TrimPrefix(v, "signed:"), nil
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
	req.AddCookie(&http.Cookie{Name: "saml_request_id", Value: "signed:id-mock-request"})
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
	req.AddCookie(&http.Cookie{Name: "saml_request_id", Value: "signed:id-mock-request"})
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

// ── request-ID binding (M-5) ──────────────────────────────────

// The login redirect has to leave something behind that ties the coming
// assertion to this browser's request.
func TestSAMLHandler_Login_SetsRequestIDCookie(t *testing.T) {
	mock := &mockSAMLAuthenticator{authnURL: "https://idp.example.com/sso?SAMLRequest=x"}
	r := newSAMLRig(t, mock)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/saml/login", nil))

	var found *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "saml_request_id" {
			found = ck
		}
	}
	require.NotNil(t, found, "no binding cookie was set")
	assert.Equal(t, "signed:id-mock-request", found.Value)
	assert.True(t, found.HttpOnly, "the browser's scripts have no business reading it")
}

// The classic SAML login-CSRF: a validly-signed assertion the attacker obtained
// elsewhere, POSTed into a victim's browser. With no pending request there is
// nothing for it to answer.
func TestSAMLHandler_ACS_WithoutPendingRequest_IsRefused(t *testing.T) {
	mock := &mockSAMLAuthenticator{
		claims:   &auth.SAMLClaims{Subject: "attacker@idp", Username: "attacker"},
		returnTo: "/",
	}
	r := newSAMLRig(t, mock)

	form := url.Values{}
	form.Set("SAMLResponse", "a-perfectly-valid-assertion")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/saml/acs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusFound, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "/login?saml_error=")
	assert.NotContains(t, w.Header().Get("Location"), "token=")
	assert.Nil(t, mock.gotRequestIDs, "the assertion must not even be parsed")
}

func TestSAMLHandler_ACS_ForgedCookie_IsRefused(t *testing.T) {
	mock := &mockSAMLAuthenticator{
		claims:       &auth.SAMLClaims{Subject: "attacker@idp", Username: "attacker"},
		requestIDErr: assert.AnError,
	}
	r := newSAMLRig(t, mock)

	form := url.Values{}
	form.Set("SAMLResponse", "assertion")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/saml/acs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "saml_request_id", Value: "i-made-this-up"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Contains(t, w.Header().Get("Location"), "/login?saml_error=")
	assert.Nil(t, mock.gotRequestIDs)
}

// The verified ID must actually reach the library — that is the check doing the
// work; everything above it is just plumbing.
func TestSAMLHandler_ACS_PassesRequestIDToParser(t *testing.T) {
	mock := &mockSAMLAuthenticator{
		claims:   &auth.SAMLClaims{Subject: "alice@idp", Username: "alice", Groups: []string{}},
		returnTo: "/",
	}
	r := newSAMLRig(t, mock)

	form := url.Values{}
	form.Set("SAMLResponse", "assertion")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/saml/acs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "saml_request_id", Value: "signed:id-mock-request"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, []string{"id-mock-request"}, mock.gotRequestIDs)
}

// One assertion per login: the cookie is spent whether or not it worked.
func TestSAMLHandler_ACS_ClearsTheCookie(t *testing.T) {
	mock := &mockSAMLAuthenticator{
		claims:   &auth.SAMLClaims{Subject: "alice@idp", Username: "alice", Groups: []string{}},
		returnTo: "/",
	}
	r := newSAMLRig(t, mock)

	form := url.Values{}
	form.Set("SAMLResponse", "assertion")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/saml/acs", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "saml_request_id", Value: "signed:id-mock-request"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	for _, ck := range w.Result().Cookies() {
		if ck.Name == "saml_request_id" {
			assert.Less(t, ck.MaxAge, 0, "cookie should be expired")
			return
		}
	}
	t.Fatal("expected the binding cookie to be cleared")
}
