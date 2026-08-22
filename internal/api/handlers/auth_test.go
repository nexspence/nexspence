package handlers_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func init() { gin.SetMode(gin.TestMode) }

const testSecret = "test-jwt-secret-1234"

func newUserSvc(users ...*domain.User) *service.UserService {
	authSvc := auth.NewService(testSecret, 24, bcryptCostTest)
	userRepo := testutil.NewUserRepo(users...)
	roleRepo := testutil.NewRoleRepo()
	return service.NewUserService(userRepo, roleRepo, authSvc, zap.NewNop().Sugar())
}

// bcryptCostTest uses cost=4 (minimum) to keep tests fast.
const bcryptCostTest = 4

func activeUser(username, password string) *domain.User {
	authSvc := auth.NewService(testSecret, 24, bcryptCostTest)
	hash, _ := authSvc.HashPassword(password)
	return &domain.User{
		ID:           "uid-" + username,
		Username:     username,
		PasswordHash: hash,
		Status:       domain.UserStatusActive,
	}
}

func bearerToken(svc *service.UserService, username string) string {
	authSvc := auth.NewService(testSecret, 24, bcryptCostTest)
	tok, _ := authSvc.GenerateToken("uid-"+username, username, nil)
	return tok
}

func buildAuthRouter(svc *service.UserService) *gin.Engine {
	r := gin.New()
	r.Use(handlers.AuthMiddleware(svc, nil, nil, nil))
	r.GET("/protected", func(c *gin.Context) {
		username, _ := c.Get("username")
		c.JSON(http.StatusOK, gin.H{"user": username})
	})
	return r
}

// ── AuthMiddleware ────────────────────────────────────────────

func TestAuthMiddleware_ValidBearer(t *testing.T) {
	user := activeUser("alice", "pass123")
	svc := newUserSvc(user)
	r := buildAuthRouter(svc)

	token := bearerToken(svc, "alice")
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_InvalidBearer_Returns401(t *testing.T) {
	svc := newUserSvc()
	r := buildAuthRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	// A bad/expired token (never validated) must get the generic message, not
	// the revocation-specific "token invalidated" body.
	assert.Contains(t, w.Body.String(), "invalid or expired token")
	assert.NotContains(t, w.Body.String(), "token invalidated")
}

func TestAuthMiddleware_NoAuth_Returns401(t *testing.T) {
	svc := newUserSvc()
	r := buildAuthRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_ValidBasicAuth(t *testing.T) {
	user := activeUser("bob", "secret")
	svc := newUserSvc(user)
	r := buildAuthRouter(svc)

	creds := base64.StdEncoding.EncodeToString([]byte("bob:secret"))
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Basic "+creds)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_WrongBasicPassword_Returns401(t *testing.T) {
	user := activeUser("charlie", "rightpassword")
	svc := newUserSvc(user)
	r := buildAuthRouter(svc)

	creds := base64.StdEncoding.EncodeToString([]byte("charlie:wrongpassword"))
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Basic "+creds)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_TokensValidAfterFuture_Rejects401(t *testing.T) {
	user := activeUser("revoked", "pass")
	userRepo := testutil.NewUserRepo(user)
	roleRepo := testutil.NewRoleRepo()
	authSvc := auth.NewService(testSecret, 24, bcryptCostTest)
	svc := service.NewUserService(userRepo, roleRepo, authSvc, zap.NewNop().Sugar())

	// Issue a fresh token, then set the cutoff to the future so the token's iat
	// is before the cutoff and must be rejected.
	token := bearerToken(svc, "revoked")
	user.TokensValidAfter = time.Now().Add(time.Hour)

	r := buildAuthRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "token invalidated")
}

func TestAuthMiddleware_ZeroTokensValidAfter_Accepts(t *testing.T) {
	// Pre-existing users with an unset (zero-value, distant past) cutoff must
	// keep working — the cutoff check must not reject them.
	user := activeUser("legacy", "pass")
	require.True(t, user.TokensValidAfter.IsZero())
	svc := newUserSvc(user)

	token := bearerToken(svc, "legacy")
	r := buildAuthRouter(svc)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_AfterRoleChange_RejectsOldToken(t *testing.T) {
	user := activeUser("roleshift", "pass")
	userRepo := testutil.NewUserRepo(user)
	roleRepo := testutil.NewRoleRepo()
	authSvc := auth.NewService(testSecret, 24, bcryptCostTest)
	svc := service.NewUserService(userRepo, roleRepo, authSvc, zap.NewNop().Sugar())

	token := bearerToken(svc, "roleshift")
	r := buildAuthRouter(svc)

	// Old token works before the role change.
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// A role change bumps tokens_valid_after; the previously-issued token whose
	// iat is now before the cutoff must be rejected.
	require.NoError(t, svc.SetUserRoles(testContext(), user.ID, []string{"role-x"}))

	req2 := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusUnauthorized, w2.Code)
	assert.Contains(t, w2.Body.String(), "token invalidated")
}

// ── OptionalAuth ──────────────────────────────────────────────

func buildOptionalAuthRouter(svc *service.UserService) *gin.Engine {
	r := gin.New()
	r.Use(handlers.OptionalAuth(svc, nil, nil, nil))
	r.GET("/open", func(c *gin.Context) {
		username, _ := c.Get("username")
		c.JSON(http.StatusOK, gin.H{"user": username})
	})
	return r
}

func TestOptionalAuth_NoAuth_Passes(t *testing.T) {
	svc := newUserSvc()
	r := buildOptionalAuthRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/open", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOptionalAuth_ValidBearer_SetsUser(t *testing.T) {
	user := activeUser("dave", "pw")
	svc := newUserSvc(user)
	r := buildOptionalAuthRouter(svc)

	token := bearerToken(svc, "dave")
	req := httptest.NewRequest(http.MethodGet, "/open", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "dave")
}

// ── AuthHandler.Login ─────────────────────────────────────────

func TestLogin_ValidCredentials(t *testing.T) {
	user := activeUser("eve", "mypass")
	svc := newUserSvc(user)

	r := gin.New()
	authH := handlers.NewAuthHandler(svc, zap.NewNop().Sugar())
	r.POST("/api/v1/login", authH.Login)

	body := `{"username":"eve","password":"mypass"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login", stringReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"token"`)
}

// ── AuthHandler.Config ────────────────────────────────────────

func TestAuthConfig_ReturnsOIDCEnabled(t *testing.T) {
	cfg := config.Config{
		OIDC: config.OIDCConfig{
			Enabled: true, DisplayName: "Keycloak", ShowLoginButton: true,
		},
		LDAP: config.LDAPConfig{Enabled: false},
	}
	h := handlers.NewAuthHandler(nil, zap.NewNop().Sugar()).WithConfig(cfg)

	r := gin.New()
	r.GET("/api/v1/auth/config", h.Config)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `"oidcEnabled":true`)
	assert.Contains(t, body, `"oidcDisplayName":"Keycloak"`)
	assert.Contains(t, body, `"oidcLoginUrl":"/api/v1/auth/oidc/login"`)
	assert.Contains(t, body, `"ldapEnabled":false`)
}

func TestAuthConfig_OIDCDisabled_WhenButtonHidden(t *testing.T) {
	cfg := config.Config{
		OIDC: config.OIDCConfig{
			Enabled: true, DisplayName: "Keycloak", ShowLoginButton: false,
		},
	}
	h := handlers.NewAuthHandler(nil, zap.NewNop().Sugar()).WithConfig(cfg)

	r := gin.New()
	r.GET("/api/v1/auth/config", h.Config)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"oidcEnabled":false`)
}

func TestLogin_WrongPassword_Returns401(t *testing.T) {
	user := activeUser("frank", "correct")
	svc := newUserSvc(user)

	r := gin.New()
	authH := handlers.NewAuthHandler(svc, zap.NewNop().Sugar())
	r.POST("/api/v1/login", authH.Login)

	body := `{"username":"frank","password":"wrong"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login", stringReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func stringReader(s string) *strings.Reader { return strings.NewReader(s) }

var _ = stringReader // used in Login tests above

// ── DockerV2Auth ──────────────────────────────────────────────

func buildDockerV2AuthRouter(svc *service.UserService) *gin.Engine {
	r := gin.New()
	h := handlers.DockerV2Auth(svc, nil, nil, nil)
	r.GET("/v2/", h)
	r.HEAD("/v2/", h)
	return r
}

// The unauthenticated ping must always challenge (#260). The earlier
// fall-through to 200 whenever any repository allowed anonymous access made
// docker select the anonymous scheme for the whole session and drop its
// stored credentials, so one public repository broke authenticated push
// registry-wide.
func TestDockerV2Auth_NoAuth_Returns401WithBearerAndBasicChallenge(t *testing.T) {
	svc := newUserSvc()
	r := buildDockerV2AuthRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	req.Host = "registry.example:8000"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "registry/2.0", w.Header().Get("Docker-Distribution-API-Version"))
	challenges := w.Header().Values("WWW-Authenticate")
	// Bearer alone — a Basic challenge alongside makes the distribution
	// client fail a credential-less request even when its token handler
	// already succeeded (see dockerChallenge). The realm falls back to the
	// request host when no base_url is configured.
	require.Len(t, challenges, 1)
	assert.Contains(t, challenges[0], `Bearer realm="http://registry.example:8000/v2/token"`)
	assert.Contains(t, challenges[0], `service="nexspence-registry"`)
}

func TestDockerV2Auth_BearerRealmHonorsForwardedProto(t *testing.T) {
	svc := newUserSvc()
	r := gin.New()
	r.GET("/v2/", handlers.DockerV2Auth(svc, nil, nil, nil))

	// Behind a TLS-terminating proxy the realm must not point docker at a
	// plaintext URL — the proxy says https, the realm says https.
	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	req.Host = "nexspence.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Header().Values("WWW-Authenticate")[0],
		`Bearer realm="https://nexspence.example/v2/token"`)
	// Per-client challenges must never be replayed across clients by a cache.
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestDockerV2Auth_ValidBasicAuth_Returns200(t *testing.T) {
	user := activeUser("admin", "secret")
	svc := newUserSvc(user)
	r := buildDockerV2AuthRouter(svc)

	creds := base64.StdEncoding.EncodeToString([]byte("admin:secret"))
	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	req.Header.Set("Authorization", "Basic "+creds)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "registry/2.0", w.Header().Get("Docker-Distribution-API-Version"))
}

func TestDockerV2Auth_WrongBasicPassword_Returns401(t *testing.T) {
	user := activeUser("admin", "correct")
	svc := newUserSvc(user)
	r := buildDockerV2AuthRouter(svc)

	creds := base64.StdEncoding.EncodeToString([]byte("admin:wrong"))
	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	req.Header.Set("Authorization", "Basic "+creds)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	// The challenge again: the client should re-prompt.
	challenges := w.Header().Values("WWW-Authenticate")
	require.Len(t, challenges, 1)
	assert.Contains(t, challenges[0], "Bearer realm=")
}

// ── API token scope enforcement (#292) ────────────────────────

// scopedTokenSetup builds a user + token service pair and mints one API token
// with the given scopes, returning the plaintext token.
func scopedTokenSetup(t *testing.T, scopes []string) (*service.UserService, *service.TokenService, string) {
	t.Helper()
	u := activeUser("dev", "pw")
	userRepo := testutil.NewUserRepo(u)
	userSvc := newUserSvc(u)
	tokenSvc := service.NewTokenService(testutil.NewUserTokenRepo(), userRepo)
	tok, err := tokenSvc.Create(testContext(), u.ID, "ci", scopes, nil)
	require.NoError(t, err)
	return userSvc, tokenSvc, tok.Token
}

// scopedRouter mounts AuthMiddleware plus echo handlers for each method.
func scopedRouter(userSvc *service.UserService, tokenSvc *service.TokenService) *gin.Engine {
	r := gin.New()
	r.Use(handlers.AuthMiddleware(userSvc, tokenSvc, nil, nil))
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	r.GET("/x", ok)
	r.PUT("/x", ok)
	r.DELETE("/x", ok)
	return r
}

// A token created with scopes: ["read"] promised a restricted session; writes
// through it must be refused even though the owning user could write (#292).
func TestAuthMiddleware_TokenScopes_ReadOnlyBlocksWrite(t *testing.T) {
	userSvc, tokenSvc, raw := scopedTokenSetup(t, []string{"read"})
	r := scopedRouter(userSvc, tokenSvc)

	get := httptest.NewRequest(http.MethodGet, "/x", nil)
	get.Header.Set("Authorization", "Bearer "+raw)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, get)
	assert.Equal(t, http.StatusOK, w.Code)

	put := httptest.NewRequest(http.MethodPut, "/x", nil)
	put.Header.Set("Authorization", "Bearer "+raw)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, put)
	assert.Equal(t, http.StatusForbidden, w.Code)

	del := httptest.NewRequest(http.MethodDelete, "/x", nil)
	del.Header.Set("Authorization", "Bearer "+raw)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, del)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// Scopes are hierarchical: write covers read (a push HEADs blobs on the way),
// delete covers everything.
func TestAuthMiddleware_TokenScopes_WriteImpliesRead(t *testing.T) {
	userSvc, tokenSvc, raw := scopedTokenSetup(t, []string{"write"})
	r := scopedRouter(userSvc, tokenSvc)

	for method, want := range map[string]int{
		http.MethodGet:    http.StatusOK,
		http.MethodPut:    http.StatusOK,
		http.MethodDelete: http.StatusForbidden,
	} {
		req := httptest.NewRequest(method, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, want, w.Code, method)
	}
}

// A token created without scopes stays fully unrestricted — the behavior of
// every token issued before scopes were enforced.
func TestAuthMiddleware_TokenScopes_NoScopesUnrestricted(t *testing.T) {
	userSvc, tokenSvc, raw := scopedTokenSetup(t, nil)
	r := scopedRouter(userSvc, tokenSvc)

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+raw)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, method)
	}
}

// The same cap holds when the token arrives as a Basic password.
func TestAuthMiddleware_TokenScopes_BasicAuthPath(t *testing.T) {
	userSvc, tokenSvc, raw := scopedTokenSetup(t, []string{"read"})
	r := scopedRouter(userSvc, tokenSvc)

	req := httptest.NewRequest(http.MethodPut, "/x", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("dev:"+raw)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// Exchanging a scoped API token at /v2/token must not widen it: the issued
// JWT carries the scopes, and a Bearer request through OptionalAuth+RBAC
// obeys the same cap (#292).
func TestDockerToken_ScopedExchange_JWTKeepsScopes(t *testing.T) {
	userSvc, tokenSvc, raw := scopedTokenSetup(t, []string{"read"})
	authSvc := auth.NewService(testSecret, 24, bcryptCostTest)

	r := gin.New()
	r.GET("/v2/token", handlers.DockerToken(authSvc, userSvc, tokenSvc, false, time.Hour, nil, nil))

	req := httptest.NewRequest(http.MethodGet, "/v2/token", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("dev:"+raw)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	claims, err := authSvc.ValidateToken(body.Token)
	require.NoError(t, err)
	assert.Equal(t, []string{"read"}, claims.Scopes)

	// The scoped JWT is capped exactly like the API token it came from.
	pr := scopedRouter(userSvc, tokenSvc)
	put := httptest.NewRequest(http.MethodPut, "/x", nil)
	put.Header.Set("Authorization", "Bearer "+body.Token)
	w = httptest.NewRecorder()
	pr.ServeHTTP(w, put)
	assert.Equal(t, http.StatusForbidden, w.Code)
}
