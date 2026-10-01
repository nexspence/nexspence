package handlers_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// apiTokenExchange wires the token endpoint, the /v2/ ping, a management route
// behind AuthMiddleware and a data route behind OptionalAuth around one user
// holding one API token — everything a JWT traded for that token can reach.
type apiTokenExchange struct {
	router *gin.Engine
	tokens *service.TokenService
	token  *domain.UserToken
}

func newAPITokenExchange(t *testing.T, issuer handlers.TokenIssuer, scopes []string, expiresAt *time.Time) *apiTokenExchange {
	t.Helper()
	u := activeUser("dev", "pw")
	userSvc := newUserSvc(u)
	tokenSvc := service.NewTokenService(testutil.NewUserTokenRepo(), testutil.NewUserRepo(u))
	tok, err := tokenSvc.Create(testContext(), u.ID, "ci", scopes, expiresAt)
	require.NoError(t, err)

	r := gin.New()
	r.GET("/v2/token", handlers.DockerToken(issuer, userSvc, tokenSvc, true, 24*time.Hour, nil, nil))
	r.GET("/v2/", handlers.DockerV2Auth(userSvc, tokenSvc, nil, nil))
	r.GET("/api/v1/me", handlers.AuthMiddleware(userSvc, tokenSvc, nil, nil), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	r.GET("/data", handlers.OptionalAuth(userSvc, tokenSvc, nil, nil), func(c *gin.Context) {
		uid, _ := c.Get("userID")
		tid, _ := c.Get("apiTokenID")
		exp, _ := c.Get("apiTokenExpiresAt")
		_, hasClaims := c.Get("claims")
		c.JSON(http.StatusOK, gin.H{"userID": uid, "apiTokenID": tid, "apiTokenExpiresAt": exp, "claims": hasClaims})
	})
	return &apiTokenExchange{router: r, tokens: tokenSvc, token: tok}
}

func (e *apiTokenExchange) basic() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("dev:"+e.token.Token))
}

func (e *apiTokenExchange) get(path, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

// Deleting a leaked API token must also end every JWT already traded for it at
// /v2/token — otherwise the JWT keeps full use of the account for up to
// jwt_expiry_hours, including minting a new permanent token (#566).
func TestDockerToken_DeletedAPIToken_RevokesExchangedJWT(t *testing.T) {
	e := newAPITokenExchange(t, auth.NewService(testSecret, 24, bcryptCostTest), nil, nil)

	code, body := fetchToken(t, e.router, e.basic())
	require.Equal(t, http.StatusOK, code)
	jwt, _ := body["token"].(string)
	require.NotEmpty(t, jwt)
	claims, err := auth.NewService(testSecret, 24, bcryptCostTest).ValidateToken(jwt)
	require.NoError(t, err)
	assert.Equal(t, e.token.ID, claims.TokenID, "the JWT must name the API token it came from")

	bearer := "Bearer " + jwt
	require.Equal(t, http.StatusOK, e.get("/api/v1/me", bearer).Code)
	require.Equal(t, http.StatusOK, e.get("/v2/", bearer).Code)

	require.NoError(t, e.tokens.Delete(testContext(), e.token.ID))

	w := e.get("/api/v1/me", bearer)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "token invalidated")
	assert.Equal(t, http.StatusUnauthorized, e.get("/v2/", bearer).Code, "the ping must not accept it either")

	var data map[string]any
	require.NoError(t, json.Unmarshal(e.get("/data", bearer).Body.Bytes(), &data))
	assert.Nil(t, data["userID"], "data routes must treat the request as anonymous")
}

// A token with one hour left must not yield a JWT valid for 24: the JWT's exp
// and the advertised expires_in are capped at the token's own expiry (#566).
func TestDockerToken_APITokenExpiry_CapsExchangedJWT(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).Truncate(time.Second)
	e := newAPITokenExchange(t, auth.NewService(testSecret, 24, bcryptCostTest), []string{"read"}, &expiresAt)

	code, body := fetchToken(t, e.router, e.basic())
	require.Equal(t, http.StatusOK, code)
	expiresIn, _ := body["expires_in"].(float64)
	assert.InDelta(t, 3600, expiresIn, 5, "expires_in must come from the capped expiry")

	jwt, _ := body["token"].(string)
	claims, err := auth.NewService(testSecret, 24, bcryptCostTest).ValidateToken(jwt)
	require.NoError(t, err)
	assert.WithinDuration(t, expiresAt, claims.ExpiresAt.Time, time.Second)
	assert.Equal(t, []string{"read"}, claims.Scopes, "scopes must still survive the exchange (#292)")
}

// A JWT from a token that never expires keeps the configured lifetime, and a
// password-exchanged JWT carries no tid and is not affected by token deletion.
func TestDockerToken_UnboundAndNonExpiringTokens_Unchanged(t *testing.T) {
	e := newAPITokenExchange(t, auth.NewService(testSecret, 24, bcryptCostTest), nil, nil)

	_, body := fetchToken(t, e.router, e.basic())
	assert.InDelta(t, 24*3600, body["expires_in"], 5)

	_, body = fetchToken(t, e.router, "Basic "+base64.StdEncoding.EncodeToString([]byte("dev:pw")))
	pwJWT, _ := body["token"].(string)
	claims, err := auth.NewService(testSecret, 24, bcryptCostTest).ValidateToken(pwJWT)
	require.NoError(t, err)
	assert.Empty(t, claims.TokenID)

	require.NoError(t, e.tokens.Delete(testContext(), e.token.ID))
	assert.Equal(t, http.StatusOK, e.get("/api/v1/me", "Bearer "+pwJWT).Code)
}

// A JWT that names an API token cannot be honored where the token service is
// not wired: nothing could tell whether the token still exists.
func TestAuthMiddleware_TokenBoundJWT_WithoutTokenService_Rejected(t *testing.T) {
	svc := newUserSvc(activeUser("alice", "pw"))
	jwt, _, err := auth.NewService(testSecret, 24, bcryptCostTest).
		GenerateAPITokenJWT("uid-alice", "alice", nil, nil, "tok-1", nil)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	w := httptest.NewRecorder()
	buildAuthRouter(svc).ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "token invalidated")
}

// plainIssuer cannot bind a JWT to an API token.
type plainIssuer struct{}

func (plainIssuer) GenerateToken(_, _ string, _ []string) (string, error) { return "unbound", nil }

// An issuer that cannot bind the JWT to its token must not be used to mint an
// unrevocable one: 503, never a silently wider credential.
func TestDockerToken_APIToken_IssuerCannotBind_Returns503(t *testing.T) {
	e := newAPITokenExchange(t, plainIssuer{}, nil, nil)
	code, _ := fetchToken(t, e.router, e.basic())
	assert.Equal(t, http.StatusServiceUnavailable, code)
}

// OptionalAuth leaves the API token's id and expiry on the context when the
// credential was an nxs_ token, Basic or Bearer, so a format handshake
// (Conan's authenticate) can mint a JWT bound to it (#567). A JWT credential
// leaves "claims" instead.
func TestOptionalAuth_APIToken_StashesTokenIDAndExpiry(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).Truncate(time.Second)
	e := newAPITokenExchange(t, auth.NewService(testSecret, 24, bcryptCostTest), nil, &expiresAt)

	for _, authz := range []string{e.basic(), "Bearer " + e.token.Token} {
		var data map[string]any
		require.NoError(t, json.Unmarshal(e.get("/data", authz).Body.Bytes(), &data))
		assert.Equal(t, e.token.ID, data["apiTokenID"], authz[:6])
		assert.NotNil(t, data["apiTokenExpiresAt"], authz[:6])
		assert.Equal(t, false, data["claims"], authz[:6])
	}

	_, body := fetchToken(t, e.router, e.basic())
	jwt, _ := body["token"].(string)
	var data map[string]any
	require.NoError(t, json.Unmarshal(e.get("/data", "Bearer "+jwt).Body.Bytes(), &data))
	assert.Equal(t, "uid-dev", data["userID"])
	assert.Nil(t, data["apiTokenID"])
	assert.Equal(t, true, data["claims"])
}
