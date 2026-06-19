package handlers

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/redisclient"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	users *service.UserService
	cfg   config.Config
	log   logger.Logger
}

// NewAuthHandler constructs an AuthHandler backed by the given user service and logger.
func NewAuthHandler(users *service.UserService, log logger.Logger) *AuthHandler {
	return &AuthHandler{users: users, log: log}
}

// WithConfig enables the /api/v1/auth/config feature-detection endpoint.
// Returns the same handler for chaining.
func (h *AuthHandler) WithConfig(cfg config.Config) *AuthHandler {
	h.cfg = cfg
	return h
}

// Config exposes the auth-related UI feature flags.
// Unauthenticated, safe to call; used by LoginPage to decide whether to
// show the "Sign in with {provider}" button.
func (h *AuthHandler) Config(c *gin.Context) {
	oidcOn := h.cfg.OIDC.Enabled && h.cfg.OIDC.ShowLoginButton
	samlOn := h.cfg.SAML.Enabled && h.cfg.SAML.ShowLoginButton
	resp := gin.H{
		"oidcEnabled":     oidcOn,
		"oidcDisplayName": h.cfg.OIDC.DisplayName,
		"oidcLoginUrl":    "/api/v1/auth/oidc/login",
		"ldapEnabled":     h.cfg.LDAP.Enabled,
		"samlEnabled":     samlOn,
		"samlDisplayName": h.cfg.SAML.DisplayName,
		"samlLoginUrl":    "/api/v1/auth/saml/login",
	}
	if h.cfg.SAML.Enabled {
		resp["samlEntityId"] = h.cfg.SAML.SPEntityID
		resp["samlAcsUrl"] = h.cfg.SAML.ACSURL
		resp["samlIdpMetadataUrl"] = h.cfg.SAML.IDPMetadataURL
		resp["samlProvisioning"] = h.cfg.SAML.Provisioning
		resp["samlMetadataUrl"] = "/api/v1/auth/saml/metadata"
	}
	c.JSON(http.StatusOK, resp)
}

// Login handles POST /api/v1/login  and  POST /service/rest/v1/security/users/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Set username in context early so the audit middleware records who attempted the login.
	c.Set("username", req.Username)

	token, user, err := h.users.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		h.log.Infow("login failed", "username", req.Username, "ip", c.ClientIP(), "err", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	c.Set("userID", user.ID)
	h.log.Infow("login success", "username", user.Username, "ip", c.ClientIP(), "roles", user.Roles)

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id":        user.ID,
			"username":  user.Username,
			"email":     user.Email,
			"firstName": user.FirstName,
			"lastName":  user.LastName,
			"roles":     user.Roles,
		},
	})
}

// Me handles GET /api/v1/me — returns info about the currently authenticated user.
func (h *AuthHandler) Me(c *gin.Context) {
	var userID string
	if claims, ok := c.Get("claims"); ok {
		userID = claims.(*auth.Claims).UserID
	} else if uid, ok := c.Get("userID"); ok {
		userID, _ = uid.(string)
	}
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	user, err := h.users.GetByID(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":        user.ID,
		"username":  user.Username,
		"email":     user.Email,
		"firstName": user.FirstName,
		"lastName":  user.LastName,
		"roles":     user.Roles,
		"status":    user.Status,
		"source":    user.Source,
	})
}

// AdminRequired aborts with 403 if the authenticated user does not have the nx-admin role.
// Must be placed after AuthMiddleware which populates the "roles" context key.
func AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		roles, _ := c.Get("roles")
		list, _ := roles.([]string)
		for _, r := range list {
			if r == "nx-admin" {
				c.Next()
				return
			}
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin role required"})
	}
}

// authenticateBearer tries the JWT service first, then falls back to an API
// token if tokens is non-nil. On success it populates gin context keys
// (username, userID, roles) and returns ok=true. When the JWT validated but
// was rejected by the per-user revocation cutoff, it returns ok=false,
// revoked=true so callers can emit a precise "token invalidated" response;
// revoked is false for every other failure (bad/expired JWT, bad API token).
func authenticateBearer(c *gin.Context, users *service.UserService, tokens *service.TokenService, raw string) (ok, revoked bool) {
	if claims, err := users.ValidateToken(raw); err == nil {
		// Enforce per-user token revocation: a JWT issued before the user's
		// tokens_valid_after cutoff (set on disable, password, or role change)
		// is rejected. Look up the user; on lookup failure, fail closed.
		if !jwtNotRevoked(c, users, claims) {
			return false, true
		}
		c.Set("claims", claims)
		c.Set("username", claims.Username)
		c.Set("userID", claims.UserID)
		c.Set("roles", claims.Roles)
		return true, false
	}
	if tokens == nil {
		return false, false
	}
	u, err := tokens.Authenticate(c.Request.Context(), raw)
	if err != nil || u == nil {
		return false, false
	}
	c.Set("username", u.Username)
	c.Set("userID", u.ID)
	c.Set("roles", u.Roles)
	return true, false
}

// jwtNotRevoked reports whether the JWT's claims pass the per-user
// tokens_valid_after revocation cutoff. It loads the user; on lookup failure it
// fails closed (returns false). A nil IssuedAt or zero-value cutoff passes.
func jwtNotRevoked(c *gin.Context, users *service.UserService, claims *auth.Claims) bool {
	user, err := users.GetByID(c.Request.Context(), claims.UserID)
	if err != nil {
		return false
	}
	if claims.IssuedAt != nil && claims.IssuedAt.Before(user.TokensValidAfter) {
		return false
	}
	return true
}

// AuthMiddleware validates Bearer JWT, Bearer API-token, or Basic auth.
// Tokens may be nil when API tokens are disabled; Bearer then only accepts JWT.
func AuthMiddleware(users *service.UserService, tokens *service.TokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var raw string

		// Bearer token
		if a := c.GetHeader("Authorization"); strings.HasPrefix(a, "Bearer ") {
			raw = strings.TrimPrefix(a, "Bearer ")
		}

		// Basic auth fallback
		if raw == "" {
			if username, password, ok := c.Request.BasicAuth(); ok {
				// An API token can also be supplied via Basic auth with any
				// username (convention: username=<token-name-or-user>, password=token).
				if tokens != nil && strings.HasPrefix(password, service.TokenPrefix) {
					if u, err := tokens.Authenticate(c.Request.Context(), password); err == nil && u != nil {
						c.Set("username", u.Username)
						c.Set("userID", u.ID)
						c.Set("roles", u.Roles)
						c.Next()
						return
					}
				}
				_, user, err := users.Login(c.Request.Context(), username, password)
				if err != nil {
					c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
					return
				}
				c.Set("username", user.Username)
				c.Set("userID", user.ID)
				c.Set("roles", user.Roles)
				c.Next()
				return
			}
		}

		if raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}

		if ok, revoked := authenticateBearer(c, users, tokens, raw); !ok {
			msg := "invalid or expired token"
			if revoked {
				msg = "token invalidated"
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": msg})
			return
		}
		c.Next()
	}
}

// dockerV2AnonTTL bounds the freshness of the "any Docker repo is anonymous?"
// decision made by DockerV2Auth. A miss-hit delay of up to this duration is
// acceptable — admins flipping allow_anonymous will see the effect within the
// window, and a cheap `EXISTS(...)` query per /v2/ ping would otherwise hit
// the DB on every Docker client poll.
const dockerV2AnonTTL = 30 * time.Second

// DockerV2Auth handles GET/HEAD /v2/ — the OCI Distribution Spec version check.
//
// Decision tree:
//   - No Authorization header + at least one Docker repo has allow_anonymous=true
//     → 200 (per-repo RBAC still enforced on subsequent /v2/:repo/... calls).
//   - No Authorization header + all Docker repos are private
//     → 401 + WWW-Authenticate: Basic challenge so `docker login` gets invoked
//     and credentials are sent on subsequent requests.
//   - Authorization header present → validated as before; 200 on success,
//     401 + challenge on invalid credentials.
//
// The allow_anonymous lookup is cached for dockerV2AnonTTL to keep Docker
// clients that poll /v2/ off the hot DB path.
func DockerV2Auth(
	users *service.UserService,
	tokens *service.TokenService,
	repos repository.RepositoryRepo,
	rdb *redisclient.Client, // nil = use in-process cache
) gin.HandlerFunc {
	const redisKey = "nexspence:docker:anon_allowed"
	var (
		cachedValue   atomic.Bool
		cachedExpires atomic.Int64 // UnixNano
	)

	anyAnonDocker := func(ctx context.Context) bool {
		if repos == nil {
			return false
		}

		if rdb != nil {
			val, err := rdb.Get(ctx, redisKey)
			if err == nil {
				return val == "1"
			}
			// Cache miss or Redis error — fall through to DB.
		} else {
			now := time.Now().UnixNano()
			if now < cachedExpires.Load() {
				return cachedValue.Load()
			}
		}

		v, err := repos.HasAnyAnonymousDocker(ctx)
		if err != nil {
			// Fail closed: when the DB is unreachable, require auth rather than
			// silently opening the registry.
			return false
		}

		if rdb != nil {
			val := "0"
			if v {
				val = "1"
			}
			_ = rdb.Set(ctx, redisKey, val, dockerV2AnonTTL)
		} else {
			cachedValue.Store(v)
			cachedExpires.Store(time.Now().UnixNano() + dockerV2AnonTTL.Nanoseconds())
		}

		return v
	}

	return func(c *gin.Context) {
		c.Header("Docker-Distribution-API-Version", "registry/2.0")

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			if anyAnonDocker(c.Request.Context()) {
				c.Status(http.StatusOK)
				return
			}
			c.Header("WWW-Authenticate", `Basic realm="Nexspence"`)
			c.Status(http.StatusUnauthorized)
			return
		}

		if strings.HasPrefix(authHeader, "Bearer ") {
			raw := strings.TrimPrefix(authHeader, "Bearer ")
			if _, err := users.ValidateToken(raw); err == nil {
				c.Status(http.StatusOK)
				return
			}
			if tokens != nil {
				if _, err := tokens.Authenticate(c.Request.Context(), raw); err == nil {
					c.Status(http.StatusOK)
					return
				}
			}
		} else if strings.HasPrefix(authHeader, "Basic ") {
			if username, password, ok := c.Request.BasicAuth(); ok {
				if tokens != nil && strings.HasPrefix(password, service.TokenPrefix) {
					if u, err := tokens.Authenticate(c.Request.Context(), password); err == nil && u != nil {
						c.Set("username", u.Username)
						c.Set("userID", u.ID)
						c.Status(http.StatusOK)
						return
					}
				}
				if _, user, err := users.Login(c.Request.Context(), username, password); err == nil {
					c.Set("username", user.Username)
					c.Set("userID", user.ID)
					c.Status(http.StatusOK)
					return
				}
			}
		}

		c.Header("WWW-Authenticate", `Basic realm="Nexspence"`)
		c.AbortWithStatus(http.StatusUnauthorized)
	}
}

// QueryTokenAuth authenticates SSE/EventSource style clients that cannot
// set the Authorization header. It accepts a JWT or API token in `?token=...`.
// Falls back to AuthMiddleware behavior (Bearer/Basic) when the query param
// is absent.
func QueryTokenAuth(users *service.UserService, tokens *service.TokenService) gin.HandlerFunc {
	mw := AuthMiddleware(users, tokens)
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			if t := c.Query("token"); t != "" {
				c.Request.Header.Set("Authorization", "Bearer "+t)
			}
		}
		mw(c)
	}
}

// OptionalAuth sets user context if a valid Bearer JWT, Bearer API-token, or
// Basic auth is present, but does not block unauthenticated requests. Basic
// auth is required for Docker clients (`docker login`) so that pushes/cached
// pulls are attributed to a real user instead of "anonymous".
func OptionalAuth(users *service.UserService, tokens *service.TokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")

		// Bearer JWT or API token
		if strings.HasPrefix(authHeader, "Bearer ") {
			raw := strings.TrimPrefix(authHeader, "Bearer ")
			authenticateBearer(c, users, tokens, raw)
			c.Next()
			return
		}

		// Basic auth (Docker registry clients, Maven, curl, etc.)
		if username, password, ok := c.Request.BasicAuth(); ok && username != "" {
			if tokens != nil && strings.HasPrefix(password, service.TokenPrefix) {
				if u, err := tokens.Authenticate(c.Request.Context(), password); err == nil && u != nil {
					c.Set("username", u.Username)
					c.Set("userID", u.ID)
					c.Set("roles", u.Roles)
					c.Next()
					return
				}
			}
			if _, user, err := users.Login(c.Request.Context(), username, password); err == nil {
				c.Set("username", user.Username)
				c.Set("userID", user.ID)
				c.Set("roles", user.Roles)
			}
		}
		c.Next()
	}
}
