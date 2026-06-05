package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
)

const (
	oidcStateCookieName = "oidc_state"
	oidcStateTTL        = 10 * time.Minute
	oidcReturnPathMax   = 200
)

// OIDCHandler serves the OIDC authorization-code + PKCE flow.
type OIDCHandler struct {
	oidc   auth.OIDCAuthenticator
	users  *service.UserService
	repo   repository.UserRepo
	sealer *auth.CookieSealer
	cfg    config.OIDCConfig
	log    logger.Logger
}

// NewOIDCHandler constructs an OIDCHandler from the authenticator, user service, repos, cookie sealer, and config.
func NewOIDCHandler(
	oidc auth.OIDCAuthenticator,
	users *service.UserService,
	repo repository.UserRepo,
	sealer *auth.CookieSealer,
	cfg config.OIDCConfig,
	log logger.Logger,
) *OIDCHandler {
	return &OIDCHandler{oidc: oidc, users: users, repo: repo, sealer: sealer, cfg: cfg, log: log}
}

// Login starts the OIDC authorization code + PKCE flow.
// GET /api/v1/auth/oidc/login[?return_to=/path]
func (h *OIDCHandler) Login(c *gin.Context) {
	state := randBase64URL(32)
	nonce := randBase64URL(32)
	codeVerifier := randBase64URL(64)
	sum := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(sum[:])

	returnTo := c.Query("return_to")
	if !IsSafeReturnPath(returnTo) {
		returnTo = "/"
	}

	sealed, err := h.sealer.Seal(auth.StateCookiePayload{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: codeVerifier,
		ReturnTo:     returnTo,
		IssuedAt:     time.Now().Unix(),
	})
	if err != nil {
		h.log.Errorw("oidc seal state failed", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oidcStateCookieName, sealed, int(oidcStateTTL.Seconds()),
		"/", "", h.cfg.CookieSecure, true /* httpOnly */)

	c.Redirect(http.StatusFound, h.oidc.AuthCodeURL(state, nonce, codeChallenge))
}

// Callback handles the IdP redirect.
// GET /api/v1/auth/oidc/callback?code=...&state=...
func (h *OIDCHandler) Callback(c *gin.Context) {
	if e := c.Query("error"); e != "" {
		h.log.Warnw("oidc idp error", "error", e, "description", c.Query("error_description"))
		h.fail(c, "idp error")
		return
	}

	sealed, err := c.Cookie(oidcStateCookieName)
	if err != nil {
		h.fail(c, "missing state")
		return
	}
	// Clear cookie immediately (one-shot).
	c.SetCookie(oidcStateCookieName, "", -1, "/", "", h.cfg.CookieSecure, true)

	payload, err := h.sealer.Open(sealed)
	if err != nil {
		h.fail(c, "invalid state")
		return
	}
	if time.Since(time.Unix(payload.IssuedAt, 0)) > oidcStateTTL {
		h.fail(c, "state expired")
		return
	}
	if c.Query("state") != payload.State {
		h.fail(c, "state mismatch")
		return
	}

	claims, rawIDToken, err := h.oidc.ExchangeAndVerify(c.Request.Context(),
		c.Query("code"), payload.CodeVerifier, payload.Nonce)
	if err != nil {
		h.log.Warnw("oidc verify failed", "err", err)
		h.fail(c, "verification failed")
		return
	}

	token, user, err := h.users.LoginOIDC(c.Request.Context(), claims, rawIDToken)
	if err != nil {
		h.log.Warnw("oidc login failed", "err", err, "username", claims.Username)
		switch {
		case errors.Is(err, service.ErrProvisioningRejected):
			h.fail(c, "provisioning rejected")
		case errors.Is(err, service.ErrProvisioningConflict):
			h.fail(c, "username conflict")
		default:
			h.fail(c, "login failed")
		}
		return
	}

	c.Set("username", user.Username)
	c.Set("userID", user.ID)
	c.Set("audit_source", "oidc")
	h.log.Infow("oidc login success",
		"username", user.Username, "roles", user.Roles,
		"ip", c.ClientIP(), "subject", claims.Subject)

	c.Redirect(http.StatusFound, fmt.Sprintf("%s/oidc/callback#token=%s&return_to=%s",
		strings.TrimRight(h.cfg.FrontendBaseURL, "/"),
		url.QueryEscape(token),
		url.QueryEscape(payload.ReturnTo)))
}

// Logout implements RP-initiated Single Logout.
// GET /api/v1/auth/oidc/logout  (RequireAuth)
// Returns {"logout_url":"..."} for the SPA to navigate to.
func (h *OIDCHandler) Logout(c *gin.Context) {
	userID, _ := c.Get("userID")
	uid, _ := userID.(string)
	if uid == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	idToken, err := h.repo.GetOIDCIDToken(c.Request.Context(), uid)
	if err != nil {
		h.log.Errorw("GetOIDCIDToken failed", "err", err, "userID", uid)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if idToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "not an OIDC session"})
		return
	}

	endpointURL := h.oidc.EndSessionEndpoint()
	frontendLogin := strings.TrimRight(h.cfg.FrontendBaseURL, "/") + "/login"

	var logoutURL string
	if endpointURL == "" {
		logoutURL = frontendLogin
	} else {
		params := url.Values{}
		params.Set("id_token_hint", idToken)
		params.Set("post_logout_redirect_uri", frontendLogin)
		params.Set("client_id", h.cfg.ClientID)
		logoutURL = endpointURL + "?" + params.Encode()
	}

	if err2 := h.repo.SetOIDCTokens(c.Request.Context(), uid, "", ""); err2 != nil {
		h.log.Warnw("SetOIDCTokens clear failed", "err", err2, "userID", uid)
	}

	c.JSON(http.StatusOK, gin.H{"logout_url": logoutURL})
}

func (h *OIDCHandler) fail(c *gin.Context, reason string) {
	c.Redirect(http.StatusFound,
		fmt.Sprintf("%s/login?oidc_error=%s",
			strings.TrimRight(h.cfg.FrontendBaseURL, "/"),
			url.QueryEscape(reason)))
}

// IsSafeReturnPath guards against open-redirect and scheme-abuse.
// Accepts only absolute paths within our own app. Exported for testing.
func IsSafeReturnPath(p string) bool {
	if p == "" || len(p) > oidcReturnPathMax {
		return false
	}
	if !strings.HasPrefix(p, "/") {
		return false
	}
	if strings.HasPrefix(p, "//") {
		return false // protocol-relative URL
	}
	if strings.ContainsAny(p, " \t\r\n") {
		return false
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return false
	}
	return true
}

func randBase64URL(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
