package handlers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
)

// NOTE: the auth.LDAPAuthenticator fake (`fakeLDAP{err error}`) is declared in
// system_test.go in this same package — reused here. There is no live LDAP
// server in unit tests, so the real bind/dial path in auth.LDAPService is not
// reachable; we cover the handler branches by injecting fakeLDAP whose
// TestConnection returns either nil (success) or an error (the 502 branch).

func mountLDAP(t *testing.T, cfg config.LDAPConfig, ldap auth.LDAPAuthenticator) *gin.Engine {
	t.Helper()
	h := handlers.NewLDAPHandler(cfg, ldap)
	r := gin.New()
	r.GET("/api/v1/ldap/config", h.GetConfig)
	r.POST("/api/v1/ldap/test", h.TestConnection)
	r.GET("/service/rest/v1/security/ldap", h.NexusList)
	return r
}

func sampleLDAPConfig() config.LDAPConfig {
	return config.LDAPConfig{
		Enabled:      true,
		Host:         "ldap.example.com",
		Port:         389,
		BindDN:       "cn=svc,dc=example,dc=com",
		BindPassword: "super-secret",
		SearchBase:   "ou=people,dc=example,dc=com",
		TimeoutSec:   5,
	}
}

// ── GetConfig ─────────────────────────────────────────────────────────────────

func TestLDAP_GetConfig_RedactsPassword(t *testing.T) {
	cfg := sampleLDAPConfig()
	r := mountLDAP(t, cfg, fakeLDAP{})
	rec := do(t, r, http.MethodGet, "/api/v1/ldap/config", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, true, got["enabled"])
	assert.Equal(t, "ldap.example.com", got["host"])
	assert.Equal(t, "cn=svc,dc=example,dc=com", got["bindDn"])
	// Password must never be exposed.
	assert.Equal(t, "***", got["bindPassword"])
	assert.NotContains(t, rec.Body.String(), "super-secret")
}

// ── TestConnection ────────────────────────────────────────────────────────────

func TestLDAP_TestConnection_Disabled_503(t *testing.T) {
	// ldap == nil → LDAP disabled branch.
	cfg := config.LDAPConfig{Enabled: false}
	r := mountLDAP(t, cfg, nil)
	rec := do(t, r, http.MethodPost, "/api/v1/ldap/test", nil)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Contains(t, got["error"], "disabled")
}

func TestLDAP_TestConnection_Success_200(t *testing.T) {
	r := mountLDAP(t, sampleLDAPConfig(), fakeLDAP{err: nil})
	rec := do(t, r, http.MethodPost, "/api/v1/ldap/test", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, true, got["success"])
	assert.Equal(t, "LDAP connection successful", got["message"])
}

func TestLDAP_TestConnection_ConnError_502(t *testing.T) {
	// A real unreachable host would surface here as a dial error; the fake
	// reproduces that error branch deterministically without a live server.
	r := mountLDAP(t, sampleLDAPConfig(), fakeLDAP{err: errors.New("dial tcp: connection refused")})
	rec := do(t, r, http.MethodPost, "/api/v1/ldap/test", nil)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, false, got["success"])
	assert.Contains(t, got["error"], "connection refused")
}

// ── NexusList (Nexus-compat GET /service/rest/v1/security/ldap) ────────────────

func TestLDAP_NexusList_Disabled_EmptyArray(t *testing.T) {
	cfg := config.LDAPConfig{Enabled: false}
	r := mountLDAP(t, cfg, nil)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/ldap", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Len(t, got, 0)
	// Must be an empty array, not null.
	assert.Equal(t, "[]", rec.Body.String())
}

func TestLDAP_NexusList_Enabled_LDAPS(t *testing.T) {
	cfg := sampleLDAPConfig()
	cfg.UseTLS = true
	cfg.SearchFilter = "(uid={0})"
	cfg.GroupBase = "ou=groups,dc=example,dc=com"
	r := mountLDAP(t, cfg, fakeLDAP{})
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/ldap", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 1)
	m := got[0]
	assert.Equal(t, "ldap", m["id"])
	assert.Equal(t, "ldaps", m["protocol"])
	assert.Equal(t, cfg.Host, m["host"])
	assert.Equal(t, float64(cfg.Port), m["port"])
	assert.Equal(t, cfg.SearchBase, m["searchBase"])
	assert.Equal(t, cfg.SearchFilter, m["userLdapFilter"])
	assert.Equal(t, cfg.GroupBase, m["groupBaseDn"])
	// Password must never be exposed.
	_, ok := m["bindPassword"]
	assert.False(t, ok)
	assert.NotContains(t, rec.Body.String(), "super-secret")
}

func TestLDAP_NexusList_Enabled_LDAP(t *testing.T) {
	cfg := sampleLDAPConfig()
	cfg.UseTLS = false
	r := mountLDAP(t, cfg, fakeLDAP{})
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/ldap", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "ldap", got[0]["protocol"])
}
