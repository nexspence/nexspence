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
	redirectURL, err := h.saml.AuthnRequestURL(relayState)
	if err != nil {
		h.log.Errorw("saml authn request error", "err", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Redirect(http.StatusFound, redirectURL)
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

	claims, err := h.saml.ParseResponse(c.Request)
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
