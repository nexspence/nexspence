package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
)

// LDAPHandler handles LDAP configuration and test endpoints.
type LDAPHandler struct {
	cfg  config.LDAPConfig
	ldap auth.LDAPAuthenticator // nil when LDAP disabled
}

func NewLDAPHandler(cfg config.LDAPConfig, ldap auth.LDAPAuthenticator) *LDAPHandler {
	return &LDAPHandler{cfg: cfg, ldap: ldap}
}

// GetConfig handles GET /api/v1/ldap/config — returns current LDAP config (password redacted).
func (h *LDAPHandler) GetConfig(c *gin.Context) {
	safe := map[string]any{
		"enabled":          h.cfg.Enabled,
		"host":             h.cfg.Host,
		"port":             h.cfg.Port,
		"useTls":           h.cfg.UseTLS,
		"startTls":         h.cfg.StartTLS,
		"bindDn":           h.cfg.BindDN,
		"bindPassword":     "***",
		"searchBase":       h.cfg.SearchBase,
		"searchFilter":     h.cfg.SearchFilter,
		"userAttributes":   h.cfg.UserAttributes,
		"groupBase":        h.cfg.GroupBase,
		"groupFilter":      h.cfg.GroupFilter,
		"groupAttribute":   h.cfg.GroupAttribute,
		"autoCreateUsers":  h.cfg.AutoCreateUsers,
		"timeoutSec":       h.cfg.TimeoutSec,
	}
	c.JSON(http.StatusOK, safe)
}

// TestConnection handles POST /api/v1/ldap/test — verifies LDAP connectivity.
func (h *LDAPHandler) TestConnection(c *gin.Context) {
	if h.ldap == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "LDAP is disabled in configuration"})
		return
	}
	if err := h.ldap.TestConnection(c.Request.Context()); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "LDAP connection successful"})
}
