package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/auth/loginguard"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// TokenIssuer mints a bearer token for an already-authenticated caller.
// *auth.Service satisfies it (mirrors formats.TokenIssuer, which the Conan
// credential handshake uses for exactly the same job).
type TokenIssuer interface {
	GenerateToken(userID, username string, roles []string) (string, error)
}

// DockerToken serves GET /v2/token — the docker token protocol endpoint the
// /v2/ ping's Bearer challenge points at (#260).
//
// The token protocol is what lets anonymous pull and authenticated push
// coexist: the ping always challenges, clients with credentials trade them
// here for a user token, and clients without get an anonymous token that
// carries no identity. Per-repository RBAC then decides each data request,
// exactly as it always has — nothing about access is decided registry-wide.
//
//   - Valid Basic credentials (password may be an nxs_ API token) → a user JWT,
//     the same token the rest of the API accepts as Bearer.
//   - Invalid credentials → 401. This is the request `docker login` actually
//     verifies, so a wrong password fails at login time instead of surfacing
//     as a mysterious push failure later.
//   - No credentials → an anonymous JWT when auth.anonymous_enabled, else 401.
//     The anonymous token is signature-valid (so the ping accepts it) but
//     names no user, so OptionalAuth leaves the request anonymous and only
//     allow_anonymous repositories answer it.
//
// The requested scope/service query parameters are deliberately ignored: the
// token conveys identity, not capability — authorization happens per request
// in RBACMiddleware, so tokens never need to encode what they may touch.
//
// Registered for GET and POST. containerd and BuildKit try the OAuth2 form
// POST (grant_type=password) first and fall back to GET only on 404/405 —
// serving the POST directly keeps their credentialed flows (Kubernetes
// imagePullSecrets included) off the fallback path. Credentials arrive as
// Basic on GET and as form fields on POST; both are accepted from either.
// An unsupported grant_type (e.g. refresh_token) answers 404 so the client
// falls back to GET rather than mis-reading a refusal as bad credentials.
func DockerToken(
	issuer TokenIssuer,
	users *service.UserService,
	tokens *service.TokenService,
	anonymousEnabled bool,
	tokenTTL time.Duration,
	guard *loginguard.Guard,
	log logger.Logger,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		issue := func(userID, username string, roles []string) {
			token, err := issuer.GenerateToken(userID, username, roles)
			if err != nil {
				// Not 401: the caller's credentials were fine, signing failed.
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not issue token"})
				return
			}
			c.JSON(http.StatusOK, gin.H{
				"token":        token,
				"access_token": token, // OAuth2-style clients read this name
				"expires_in":   int(tokenTTL / time.Second),
				"issued_at":    time.Now().UTC().Format(time.RFC3339),
			})
		}

		username, password, _ := c.Request.BasicAuth()
		if c.Request.Method == http.MethodPost {
			if gt := c.PostForm("grant_type"); gt != "" && gt != "password" {
				c.JSON(http.StatusNotFound, gin.H{"error": "unsupported grant_type"})
				return
			}
			if username == "" {
				username, password = c.PostForm("username"), c.PostForm("password")
			}
		}

		if username != "" {
			ctx := c.Request.Context()
			if tokens != nil && strings.HasPrefix(password, service.TokenPrefix) {
				if u, err := tokens.Authenticate(ctx, password); err == nil && u != nil {
					issue(u.ID, u.Username, u.Roles)
					return
				}
			}
			if wait := guard.RetryAfter(ctx, username); wait > 0 {
				logAuthEvent(log, "authentication throttled", c, username, "retryAfter", wait)
				c.Header("Retry-After", strconv.Itoa(retryAfterSeconds(wait)))
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many failed attempts"})
				return
			}
			_, user, err := users.Login(ctx, username, password)
			if err == nil {
				guard.RecordSuccess(ctx, username)
				issue(user.ID, user.Username, user.Roles)
				return
			}
			guard.RecordFailure(ctx, username)
			logAuthEvent(log, "authentication failed", c, username, "err", err)
			c.Header("WWW-Authenticate", `Basic realm="Nexspence"`)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			return
		}

		if !anonymousEnabled {
			c.Header("WWW-Authenticate", `Basic realm="Nexspence"`)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		issue("", "", nil)
	}
}
