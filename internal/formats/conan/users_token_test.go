package conan_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// bindingIssuer stands in for *auth.Service with every capability it has:
// plain, scoped, and API-token-bound JWTs. It records which one was used.
type bindingIssuer struct {
	stubIssuer
	scoped    bool
	scopes    []string
	tokenID   string
	tokenExp  *time.Time
	boundUsed bool
}

func (b *bindingIssuer) GenerateScopedToken(userID, username string, roles, scopes []string) (string, error) {
	b.scoped, b.scopes = true, scopes
	return b.GenerateToken(userID, username, roles)
}

func (b *bindingIssuer) GenerateAPITokenJWT(userID, username string, roles, scopes []string, tokenID string, tokenExpiresAt *time.Time) (string, time.Time, error) {
	b.boundUsed, b.scopes, b.tokenID, b.tokenExp = true, scopes, tokenID, tokenExpiresAt
	tok, err := b.GenerateToken(userID, username, roles)
	return tok, time.Now().Add(time.Hour), err
}

// A read-only API token must not log in to a JWT that writes: the scopes
// OptionalAuth left on the context are minted into the token (#567), and the
// JWT is bound to the API token so deleting it revokes the session (#566).
func TestConan_V2_Authenticate_ScopedAPIToken_KeepsScopes(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-auth-scoped", "conan")
	issuer := &bindingIssuer{}
	exp := time.Now().Add(time.Hour)
	r := setupUsersWith(repo, "carl", issuer, func(c *gin.Context) {
		c.Set("tokenScopes", []string{"read"})
		c.Set("apiTokenID", "tok-ro")
		c.Set("apiTokenExpiresAt", &exp)
	})

	w := v2Get(r, "/repository/cv2-auth-scoped/v2/users/authenticate")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "jwt-for-carl", w.Body.String())
	assert.True(t, issuer.boundUsed, "an API-token login must mint a token-bound JWT")
	assert.Equal(t, []string{"read"}, issuer.scopes)
	assert.Equal(t, "tok-ro", issuer.tokenID)
	require.NotNil(t, issuer.tokenExp)
	assert.True(t, exp.Equal(*issuer.tokenExp))
}

// The same binding for an unscoped API token: no scopes, but still a tid.
func TestConan_V2_Authenticate_UnscopedAPIToken_BindsToken(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-auth-unscoped", "conan")
	issuer := &bindingIssuer{}
	r := setupUsersWith(repo, "carl", issuer, func(c *gin.Context) {
		c.Set("apiTokenID", "tok-rw")
	})

	w := v2Get(r, "/repository/cv2-auth-unscoped/v2/users/authenticate")

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, issuer.boundUsed)
	assert.Equal(t, "tok-rw", issuer.tokenID)
	assert.Nil(t, issuer.tokenExp)
	assert.Empty(t, issuer.scopes)
}

// Scopes without a token id still survive, through the scoped issuer.
func TestConan_V2_Authenticate_ScopesWithoutTokenID_UsesScopedIssuer(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-auth-scoped-only", "conan")
	issuer := &bindingIssuer{}
	r := setupUsersWith(repo, "carl", issuer, func(c *gin.Context) {
		c.Set("tokenScopes", []string{"write"})
	})

	w := v2Get(r, "/repository/cv2-auth-scoped-only/v2/users/authenticate")

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, issuer.scoped)
	assert.False(t, issuer.boundUsed)
	assert.Equal(t, []string{"write"}, issuer.scopes)
}

// An issuer that cannot carry the restriction must refuse rather than widen
// the token into a full-power session.
func TestConan_V2_Authenticate_IssuerCannotCarryRestriction_503(t *testing.T) {
	for name, extra := range map[string]func(*gin.Context){
		"scopes":   func(c *gin.Context) { c.Set("tokenScopes", []string{"read"}) },
		"token id": func(c *gin.Context) { c.Set("apiTokenID", "tok-1") },
	} {
		repo := testutil.SimpleRepo("cv2-auth-plain", "conan")
		issuer := &stubIssuer{}
		r := setupUsersWith(repo, "carl", issuer, extra)

		w := v2Get(r, "/repository/cv2-auth-plain/v2/users/authenticate")

		assert.Equal(t, http.StatusServiceUnavailable, w.Code, name)
		assert.Empty(t, issuer.userID, "%s: nothing may be minted", name)
	}
}

// A JWT is not a login credential: trading one for a fresh JWT would let a
// session renew itself forever, past the deletion of the API token it came
// from (#567). The Conan client always logs in with Basic.
func TestConan_V2_Authenticate_BearerJWT_NotRenewed(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-auth-jwt", "conan")
	issuer := &bindingIssuer{}
	r := setupUsersWith(repo, "carl", issuer, func(c *gin.Context) {
		c.Set("claims", struct{}{})
	})

	w := v2Get(r, "/repository/cv2-auth-jwt/v2/users/authenticate")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Header().Get("WWW-Authenticate"), "Basic")
	assert.Empty(t, issuer.userID, "no token may be minted from a JWT")

	// check_credentials keeps accepting the Bearer — that is what the client
	// sends it before every upload.
	w = v2Get(r, "/repository/cv2-auth-jwt/v2/users/check_credentials")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "carl", w.Body.String())
}
