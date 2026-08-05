package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// SAMLHandler serves the SAML SP-initiated SSO flow.
type SAMLHandler struct {
	saml  auth.SAMLAuthenticator
	users *service.UserService
	cfg   config.SAMLConfig
	log   logger.Logger
}

// NewSAMLHandler constructs a SAMLHandler from the SAML authenticator, user service, config, and logger.
func NewSAMLHandler(
	saml auth.SAMLAuthenticator,
	users *service.UserService,
	cfg config.SAMLConfig,
	log logger.Logger,
) *SAMLHandler {
	return &SAMLHandler{saml: saml, users: users, cfg: cfg, log: log}
}

// Metadata serves GET /api/v1/auth/saml/metadata — SP metadata XML (public, no auth).
func (h *SAMLHandler) Metadata(c *gin.Context) {
	xmlBytes, err := h.saml.MetadataXML()
	if err != nil {
		h.log.Errorw("saml metadata error", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "application/xml; charset=utf-8", xmlBytes)
}

// Login serves GET /api/v1/auth/saml/login — redirects browser to IdP.
func (h *SAMLHandler) Login(c *gin.Context) {
	returnTo := c.Query("return_to")
	if !IsSafeReturnPath(returnTo) {
		returnTo = "/"
	}
	relayState := h.saml.SignRelayState(returnTo)
	redirectURL, requestID, err := h.saml.AuthnRequest(relayState)
	if err != nil {
		h.log.Errorw("saml authn request error", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	// Remember which request this browser started. ACS refuses any assertion
	// that does not answer it, which is what stops an attacker replaying an
	// assertion of their own into someone else's browser.
	h.setRequestIDCookie(c, h.saml.SignRequestID(requestID))
	c.Redirect(http.StatusFound, redirectURL)
}

// samlRequestIDCookie carries the pending AuthnRequest ID between the redirect
// to the IdP and the assertion coming back.
const samlRequestIDCookie = "saml_request_id"

func (h *SAMLHandler) setRequestIDCookie(c *gin.Context, value string) {
	//nolint:gosec // G124: SameSite=None is required — the IdP delivers the
	// assertion as a cross-site POST, and a Lax cookie would not be sent with
	// it. It is paired with Secure whenever the ACS URL is HTTPS.
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     samlRequestIDCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   int(auth.SAMLRequestIDTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.secureCookies(),
		// The assertion arrives as a cross-site POST from the IdP, so the
		// cookie has to survive it; None requires Secure.
		SameSite: h.cookieSameSite(),
	})
}

func (h *SAMLHandler) clearRequestIDCookie(c *gin.Context) {
	//nolint:gosec // G124: mirrors setRequestIDCookie so the browser matches and
	// drops the same cookie; an expiring cookie carries no value anyway.
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     samlRequestIDCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookies(),
		SameSite: h.cookieSameSite(),
	})
}

// secureCookies reports whether the ACS endpoint is served over HTTPS, which
// SameSite=None requires. A plain-HTTP dev setup falls back to Lax.
func (h *SAMLHandler) secureCookies() bool {
	return strings.HasPrefix(strings.ToLower(h.cfg.ACSURL), "https://")
}

func (h *SAMLHandler) cookieSameSite() http.SameSite {
	if h.secureCookies() {
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}

// ACS serves POST /api/v1/auth/saml/acs — Assertion Consumer Service.
// IdP POSTs the SAMLResponse here after authentication.
func (h *SAMLHandler) ACS(c *gin.Context) {
	relayState := c.PostForm("RelayState")
	returnTo := "/"
	if relayState != "" {
		if rt, err := h.saml.VerifyRelayState(relayState); err == nil && IsSafeReturnPath(rt) {
			returnTo = rt
		}
	}

	// The assertion is only accepted as the answer to a request this browser
	// started. No cookie means no pending request: refuse rather than fall back
	// to treating it as IdP-initiated.
	var possibleRequestIDs []string
	if raw, err := c.Cookie(samlRequestIDCookie); err == nil && raw != "" {
		if id, verr := h.saml.VerifyRequestID(raw); verr == nil {
			possibleRequestIDs = []string{id}
		} else {
			h.log.Warnw("saml request id cookie rejected", "err", verr, "ip", c.ClientIP())
		}
	}
	// One assertion per request, whatever the outcome.
	h.clearRequestIDCookie(c)
	if len(possibleRequestIDs) == 0 {
		h.log.Warnw("saml assertion without a pending request", "ip", c.ClientIP())
		h.fail(c, "verification failed")
		return
	}

	claims, err := h.saml.ParseResponse(c.Request, possibleRequestIDs)
	if err != nil {
		h.log.Warnw("saml parse response failed", "err", err)
		h.fail(c, "verification failed")
		return
	}

	token, user, err := h.users.LoginSAML(c.Request.Context(), claims)
	if err != nil {
		h.log.Warnw("saml login failed", "err", err, "username", claims.Username)
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
	c.Set("audit_source", "saml")
	h.log.Infow("saml login success",
		"username", user.Username, "roles", user.Roles,
		"ip", c.ClientIP(), "subject", claims.Subject)

	c.Redirect(http.StatusFound, fmt.Sprintf("%s/saml/callback#token=%s&return_to=%s",
		strings.TrimRight(h.cfg.FrontendBaseURL, "/"),
		url.QueryEscape(token),
		url.QueryEscape(returnTo)))
}

func (h *SAMLHandler) fail(c *gin.Context, reason string) {
	c.Redirect(http.StatusFound,
		fmt.Sprintf("%s/login?saml_error=%s",
			strings.TrimRight(h.cfg.FrontendBaseURL, "/"),
			url.QueryEscape(reason)))
}
