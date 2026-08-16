package conan_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/conan"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// stubIssuer stands in for *auth.Service. It records what it was asked to sign
// so the tests can prove the token is minted for the authenticated caller and
// not for whoever the request claims to be.
type stubIssuer struct {
	userID   string
	username string
	roles    []string
	err      error
}

func (s *stubIssuer) GenerateToken(userID, username string, roles []string) (string, error) {
	s.userID, s.username, s.roles = userID, username, roles
	if s.err != nil {
		return "", s.err
	}
	return "jwt-for-" + username, nil
}

// setupUsers builds a Conan router whose requests arrive already authenticated
// as user (empty user = anonymous), mirroring what handlers.OptionalAuth sets
// on the real route.
func setupUsers(repo *domain.Repository, user string, issuer formats.TokenIssuer) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
		Tokens:     issuer,
	}
	h := conan.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		if user != "" {
			c.Set("username", user)
			c.Set("userID", "u-"+user)
			c.Set("roles", []string{"nx-deployer"})
		}
		h.ServeHTTP(c)
	})
	return r
}

// The Conan 2 client reads this response body as the bearer token itself, so it
// has to be the bare token as text — not JSON, not a quoted string (#244).
func TestConan_V2_Authenticate_ReturnsBareToken(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-auth", "conan")
	issuer := &stubIssuer{}
	r := setupUsers(repo, "alice", issuer)

	w := v2Get(r, "/repository/cv2-auth/v2/users/authenticate")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "jwt-for-alice", w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")
	assert.Equal(t, "u-alice", issuer.userID, "token must be minted for the authenticated caller")
	assert.Equal(t, []string{"nx-deployer"}, issuer.roles, "token must carry the caller's roles")
}

// Without credentials this must not hand out a token for the anonymous user:
// on a repository with allow_anonymous the request reaches the handler, and
// answering 200 would let anyone log in as nobody and upload as nobody.
func TestConan_V2_Authenticate_Anonymous_401(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-auth-anon", "conan")
	repo.AllowAnonymous = true
	r := setupUsers(repo, "", &stubIssuer{})

	w := v2Get(r, "/repository/cv2-auth-anon/v2/users/authenticate")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Header().Get("WWW-Authenticate"), "Basic")
}

// check_credentials answers with the username as bare text — the client prints
// it and uses it as the account the remote is logged in as.
func TestConan_V2_CheckCredentials_ReturnsUsername(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-check", "conan")
	r := setupUsers(repo, "bob", &stubIssuer{})

	w := v2Get(r, "/repository/cv2-check/v2/users/check_credentials")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "bob", w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")
}

func TestConan_V2_CheckCredentials_Anonymous_401(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-check-anon", "conan")
	repo.AllowAnonymous = true
	r := setupUsers(repo, "", &stubIssuer{})

	w := v2Get(r, "/repository/cv2-check-anon/v2/users/check_credentials")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// A signing failure must not look like a wrong password: the client would tell
// the user to check their credentials, which would be a dead end.
func TestConan_V2_Authenticate_IssuerError_503(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-auth-err", "conan")
	r := setupUsers(repo, "carol", &stubIssuer{err: assert.AnError})

	w := v2Get(r, "/repository/cv2-auth-err/v2/users/authenticate")

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// Nothing wires a token issuer on a route that cannot mint one; say so rather
// than answering 200 with an empty body, which the client would store as a
// token and then fail on every later request.
func TestConan_V2_Authenticate_NoIssuer_503(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-auth-noissuer", "conan")
	r := setupUsers(repo, "dave", nil)

	w := v2Get(r, "/repository/cv2-auth-noissuer/v2/users/authenticate")

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// check_credentials needs no issuer — it only reports who the caller is.
func TestConan_V2_CheckCredentials_NoIssuer_StillWorks(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-check-noissuer", "conan")
	r := setupUsers(repo, "erin", nil)

	w := v2Get(r, "/repository/cv2-check-noissuer/v2/users/check_credentials")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "erin", w.Body.String())
}

// The routes are GET-only; anything else stays a 405 rather than falling into
// the authenticate branch.
func TestConan_V2_Users_NonGET_405(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-users-method", "conan")
	r := setupUsers(repo, "alice", &stubIssuer{})

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/repository/cv2-users-method/v2/users/authenticate", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code, method)
	}
}

// A proxy repository answers the credential handshake itself. Forwarding it
// upstream would ask a remote that does not know this user, and the whole
// point of the endpoint is to authenticate against Nexspence.
func TestConan_V2_Users_ProxyRepo_ServedLocally(t *testing.T) {
	repo := testutil.SimpleRepo("cv2-users-proxy", "conan")
	repo.Type = domain.TypeProxy
	repo.ProxyConfig = map[string]any{"remote_url": "http://127.0.0.1:19998"}
	r := setupUsers(repo, "frank", &stubIssuer{})

	w := v2Get(r, "/repository/cv2-users-proxy/v2/users/check_credentials")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "frank", w.Body.String())
}
