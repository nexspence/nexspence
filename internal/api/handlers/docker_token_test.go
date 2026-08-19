package handlers_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/service"
)

func buildDockerTokenRouter(svc *service.UserService, anonymousEnabled bool) *gin.Engine {
	issuer := auth.NewService(testSecret, 24, bcryptCostTest)
	r := gin.New()
	h := handlers.DockerToken(issuer, svc, nil, anonymousEnabled, 24*time.Hour, nil, nil)
	r.GET("/v2/token", h)
	r.POST("/v2/token", h)
	r.GET("/v2/", handlers.DockerV2Auth(svc, nil, nil, nil))
	return r
}

func fetchToken(t *testing.T, r *gin.Engine, authorization string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v2/token", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func TestDockerToken_NoCredentials_IssuesAnonymousToken(t *testing.T) {
	svc := newUserSvc()
	r := buildDockerTokenRouter(svc, true)

	code, body := fetchToken(t, r, "")
	require.Equal(t, http.StatusOK, code)

	token, _ := body["token"].(string)
	require.NotEmpty(t, token)
	assert.Equal(t, token, body["access_token"], "OAuth2-style clients read access_token")
	assert.Greater(t, body["expires_in"], float64(0))

	// The token is signature-valid but names no user: the ping must accept it,
	// and nothing may mistake it for an authenticated identity.
	claims, err := auth.NewService(testSecret, 24, bcryptCostTest).ValidateToken(token)
	require.NoError(t, err)
	assert.Empty(t, claims.UserID)
	assert.Empty(t, claims.Username)
	assert.Empty(t, claims.Roles)
}

// The full anonymous round-trip: challenge on the bare ping, token from the
// realm, ping accepted with the token — the sequence every docker-family
// client performs before its first data request.
func TestDockerToken_AnonymousToken_PassesTheV2Ping(t *testing.T) {
	svc := newUserSvc()
	r := buildDockerTokenRouter(svc, true)

	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Header().Values("WWW-Authenticate")[0], "Bearer realm=")

	_, body := fetchToken(t, r, "")
	token, _ := body["token"].(string)
	require.NotEmpty(t, token)

	req2 := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
}

// The anonymous token must not authenticate anybody on data routes:
// OptionalAuth leaves the request anonymous, so per-repository RBAC treats it
// exactly like a request with no Authorization header at all.
func TestDockerToken_AnonymousToken_StaysAnonymousThroughOptionalAuth(t *testing.T) {
	svc := newUserSvc()
	r := buildDockerTokenRouter(svc, true)
	_, body := fetchToken(t, r, "")
	token, _ := body["token"].(string)
	require.NotEmpty(t, token)

	probe := gin.New()
	probe.Use(handlers.OptionalAuth(svc, nil, nil, nil))
	probe.GET("/probe", func(c *gin.Context) {
		userID, _ := c.Get("userID")
		username, _ := c.Get("username")
		c.JSON(http.StatusOK, gin.H{"userID": userID, "username": username})
	})

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	probe.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Nil(t, got["userID"])
	assert.Nil(t, got["username"])
}

func TestDockerToken_NoCredentials_AnonymousDisabled_Returns401(t *testing.T) {
	svc := newUserSvc()
	r := buildDockerTokenRouter(svc, false)

	code, _ := fetchToken(t, r, "")
	assert.Equal(t, http.StatusUnauthorized, code)
}

func TestDockerToken_ValidBasic_IssuesUserToken(t *testing.T) {
	svc := newUserSvc(activeUser("guy", "s3cret"))
	r := buildDockerTokenRouter(svc, true)

	creds := base64.StdEncoding.EncodeToString([]byte("guy:s3cret"))
	code, body := fetchToken(t, r, "Basic "+creds)
	require.Equal(t, http.StatusOK, code)

	token, _ := body["token"].(string)
	require.NotEmpty(t, token)
	claims, err := auth.NewService(testSecret, 24, bcryptCostTest).ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "uid-guy", claims.UserID)
	assert.Equal(t, "guy", claims.Username)
}

// This is the request `docker login` verifies, so a wrong password has to
// fail here — with the 200-ping design it "succeeded" unchecked and the
// failure surfaced one push later.
func TestDockerToken_WrongPassword_Returns401(t *testing.T) {
	svc := newUserSvc(activeUser("guy", "right"))
	r := buildDockerTokenRouter(svc, true)

	creds := base64.StdEncoding.EncodeToString([]byte("guy:wrong"))
	code, _ := fetchToken(t, r, "Basic "+creds)
	assert.Equal(t, http.StatusUnauthorized, code)
}

// containerd and BuildKit try the OAuth2 form POST before the GET; serving it
// directly keeps credentialed Kubernetes pulls off the fallback path.
func TestDockerToken_OAuth2PostWithFormCredentials(t *testing.T) {
	svc := newUserSvc(activeUser("guy", "s3cret"))
	r := buildDockerTokenRouter(svc, true)

	form := url.Values{
		"grant_type": {"password"},
		"username":   {"guy"},
		"password":   {"s3cret"},
		"service":    {"nexspence-registry"},
		"client_id":  {"containerd-client"},
	}
	req := httptest.NewRequest(http.MethodPost, "/v2/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	token, _ := body["access_token"].(string)
	require.NotEmpty(t, token)
	claims, err := auth.NewService(testSecret, 24, bcryptCostTest).ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "guy", claims.Username)
}

// An unsupported grant_type answers 404 so the client falls back to GET
// instead of mis-reading the refusal as bad credentials.
func TestDockerToken_UnsupportedGrantType_Returns404(t *testing.T) {
	svc := newUserSvc()
	r := buildDockerTokenRouter(svc, true)

	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"x"}}
	req := httptest.NewRequest(http.MethodPost, "/v2/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// The token endpoint is where `docker login` retry storms land now, so it
// throttles failed attempts the same way the ping's Basic path does.
func TestDockerToken_RepeatedFailures_Get429(t *testing.T) {
	guard := testGuard()
	svc := newUserSvc(activeUser("guy", "right"))
	issuer := auth.NewService(testSecret, 24, bcryptCostTest)
	r := gin.New()
	r.GET("/v2/token", handlers.DockerToken(issuer, svc, nil, true, 24*time.Hour, guard, nil))

	creds := "Basic " + base64.StdEncoding.EncodeToString([]byte("guy:wrong"))
	var code int
	for range 4 {
		code, _ = fetchToken(t, r, creds)
	}
	assert.Equal(t, http.StatusTooManyRequests, code)
}
