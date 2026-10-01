package conan

// Conan credential handshake (Conan 2.x clients):
//
//	GET /v2/users/authenticate        → bearer token for the Basic-authenticated caller
//	GET /v2/users/check_credentials   → username the caller is authenticated as
//
// The client calls check_credentials immediately before every upload and
// refuses to write without a 200, so without these two routes a Conan 2 client
// cannot publish at all even though the rest of the v2 revisions API works
// (#244).
//
// Both responses are plain text carrying a bare value — the client reads the
// body directly as the token / username. JSON here breaks it, quoted strings
// included. The reference contract is conan_server's
// conans/server/rest/controller/v2/.

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// usersPath reports whether p is one of the credential routes, and which.
const (
	pathAuthenticate     = "/v2/users/authenticate"
	pathCheckCredentials = "/v2/users/check_credentials" //nolint:gosec // G101: a request path the Conan client calls, not a credential
)

// isUsersPath reports whether p is a Conan credential route. These are served
// locally for every repository type, proxies included: they authenticate the
// caller against Nexspence, and an upstream that has never heard of this user
// cannot answer them.
func isUsersPath(p string) bool {
	return p == pathAuthenticate || p == pathCheckCredentials
}

// serveUsers handles the credential routes. The request has already passed
// through OptionalAuth, so an authenticated caller arrives with a username set
// in the gin context and everyone else arrives anonymous.
func (h *Handler) serveUsers(c *gin.Context, p string) {
	if c.Request.Method != http.MethodGet {
		c.Status(http.StatusMethodNotAllowed)
		return
	}

	username, userID, roles, ok := caller(c)
	if !ok {
		// Not "forbidden": the client is asking to log in, and a challenge is
		// what makes it prompt for credentials instead of giving up. A
		// repository with allow_anonymous lets the anonymous request reach us,
		// so the check has to happen here — answering 200 would log the client
		// in as nobody and attribute its uploads to nobody.
		c.Header("WWW-Authenticate", `Basic realm="Nexspence"`)
		c.Status(http.StatusUnauthorized)
		return
	}

	if p == pathCheckCredentials {
		c.String(http.StatusOK, username)
		return
	}

	// A JWT is not a login credential. Trading one for a fresh JWT would let a
	// session renew itself forever, outliving the API token it came from
	// (#567). The Conan client always logs in with Basic and only sends the
	// Bearer on later requests (check_credentials included, served above).
	if _, isJWT := c.Get("claims"); isJWT {
		c.Header("WWW-Authenticate", `Basic realm="Nexspence"`)
		c.Status(http.StatusUnauthorized)
		return
	}

	if h.deps.Tokens == nil {
		c.String(http.StatusServiceUnavailable, "token issuer not configured")
		return
	}
	token, err := h.issue(c, userID, username, roles)
	if err != nil {
		// Deliberately not 401: the credentials were fine, we failed to sign.
		// Reporting it as an auth failure would send the user off to check a
		// password that is not the problem.
		c.String(http.StatusServiceUnavailable, "could not issue token")
		return
	}
	c.String(http.StatusOK, token)
}

// errCannotRestrict is returned when the configured issuer cannot carry the
// API token's restriction into the minted token.
var errCannotRestrict = errors.New("token issuer cannot carry the API token's restrictions")

// issue mints the login token under the same rules as /v2/token. A caller
// that logged in with an nxs_ API token gets a JWT bound to it — its scopes,
// its id (so deleting the token revokes the JWT) and an expiry no later than
// the token's (#566, #567). An issuer that cannot carry that restriction
// fails the login rather than widening it into a full-power session.
func (h *Handler) issue(c *gin.Context, userID, username string, roles []string) (string, error) {
	var scopes []string
	if v, ok := c.Get("tokenScopes"); ok {
		scopes, _ = v.([]string)
	}
	if v, ok := c.Get("apiTokenID"); ok {
		tokenID, _ := v.(string)
		ai, ok := h.deps.Tokens.(formats.APITokenIssuer)
		if !ok {
			return "", errCannotRestrict
		}
		var expiresAt *time.Time
		if e, ok := c.Get("apiTokenExpiresAt"); ok {
			expiresAt, _ = e.(*time.Time)
		}
		token, _, err := ai.GenerateAPITokenJWT(userID, username, roles, scopes, tokenID, expiresAt)
		return token, err
	}
	if len(scopes) > 0 {
		si, ok := h.deps.Tokens.(formats.ScopedTokenIssuer)
		if !ok {
			return "", errCannotRestrict
		}
		return si.GenerateScopedToken(userID, username, roles, scopes)
	}
	return h.deps.Tokens.GenerateToken(userID, username, roles)
}

// caller reads the authenticated identity OptionalAuth left on the context.
func caller(c *gin.Context) (username, userID string, roles []string, ok bool) {
	u, _ := c.Get("username")
	username, _ = u.(string)
	if username == "" {
		return "", "", nil, false
	}
	id, _ := c.Get("userID")
	userID, _ = id.(string)
	r, _ := c.Get("roles")
	roles, _ = r.([]string)
	return username, userID, roles, true
}
