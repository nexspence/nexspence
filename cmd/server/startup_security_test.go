package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/netguard"
)

func secureCfg() *config.Config {
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "a-unique-production-secret-at-least-32b"
	cfg.Bootstrap.AdminPassword = "a-unique-admin-password"
	return cfg
}

func TestCheckStartupSecurity_SecureConfig_Passes(t *testing.T) {
	require.NoError(t, checkStartupSecurity(secureCfg(), zap.NewNop().Sugar()))
}

func TestCheckStartupSecurity_InvalidTrustedProxies_Refuses(t *testing.T) {
	cfg := secureCfg()
	cfg.HTTP.TrustedProxies = []string{"10.0.0.0/8", "not-an-ip"}

	err := checkStartupSecurity(cfg, zap.NewNop().Sugar())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trusted_proxies")
}

func TestCheckStartupSecurity_ValidTrustedProxies_Passes(t *testing.T) {
	cfg := secureCfg()
	cfg.HTTP.TrustedProxies = []string{"10.0.0.0/8", "*"}

	require.NoError(t, checkStartupSecurity(cfg, zap.NewNop().Sugar()))
}

func TestCheckStartupSecurity_ShippedDefaults_Refuse(t *testing.T) {
	cfg := secureCfg()
	cfg.Auth.JWTSecret = config.DevDefaultJWTSecret

	err := checkStartupSecurity(cfg, zap.NewNop().Sugar())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default secrets")
}

func TestCheckStartupSecurity_InvalidOutboundAllowlist_Refuses(t *testing.T) {
	cfg := secureCfg()
	cfg.Outbound.AllowedInternalCIDRs = []string{"10.0.0.0/8", "nonsense"}

	err := checkStartupSecurity(cfg, zap.NewNop().Sugar())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allowed_internal_cidrs")
}

func TestCheckStartupSecurity_ValidOutboundAllowlist_IsApplied(t *testing.T) {
	cfg := secureCfg()
	cfg.Outbound.AllowedInternalCIDRs = []string{"10.10.0.0/16"}
	t.Cleanup(func() { _ = netguard.SetAllowedInternalCIDRs(nil) })

	require.NoError(t, checkStartupSecurity(cfg, zap.NewNop().Sugar()))

	// Applied, not merely validated: the guard must actually let the range
	// through afterwards.
	c := netguard.Client(time.Second)
	resp, err := c.Get("http://10.10.0.1:9/") //nolint:bodyclose // the request always fails; there is no body
	require.Error(t, err, "the address is unroutable in tests")
	require.Nil(t, resp)
	assert.NotContains(t, err.Error(), "blocked",
		"an allowlisted range must fail to connect, not be refused by the guard")
}

// The shipped OIDC cookie key seals the state cookie that protects the login
// flow from CSRF. Knowing it lets an attacker forge a state cookie matching
// their own state parameter, so it belongs in the same fail-closed check as the
// JWT secret and admin password.
func TestCheckStartupSecurity_ShippedOIDCCookieKey_Refuses(t *testing.T) {
	cfg := secureCfg()
	cfg.OIDC.Enabled = true
	cfg.OIDC.CookieKey = config.DevDefaultOIDCCookieKey

	err := checkStartupSecurity(cfg, zap.NewNop().Sugar())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default secrets")
}

func TestCheckStartupSecurity_ShippedOIDCCookieKey_AllowedWhenOptedIn(t *testing.T) {
	cfg := secureCfg()
	cfg.OIDC.Enabled = true
	cfg.OIDC.CookieKey = config.DevDefaultOIDCCookieKey
	cfg.Auth.AllowInsecureDefaults = true

	require.NoError(t, checkStartupSecurity(cfg, zap.NewNop().Sugar()))
}

// A disabled provider cannot be attacked through its cookie, so the shipped key
// sitting unused in a config file must not stop the server booting.
func TestCheckStartupSecurity_ShippedOIDCCookieKey_IgnoredWhenOIDCDisabled(t *testing.T) {
	cfg := secureCfg()
	cfg.OIDC.Enabled = false
	cfg.OIDC.CookieKey = config.DevDefaultOIDCCookieKey

	require.NoError(t, checkStartupSecurity(cfg, zap.NewNop().Sugar()))
}

func TestCheckStartupSecurity_ShippedDefaults_AllowedWhenOptedIn(t *testing.T) {
	cfg := secureCfg()
	cfg.Auth.JWTSecret = config.DevDefaultJWTSecret
	cfg.Bootstrap.AdminPassword = "admin123"
	cfg.Auth.AllowInsecureDefaults = true

	require.NoError(t, checkStartupSecurity(cfg, zap.NewNop().Sugar()))
}

// The shipped admin123 default is only a risk while bootstrap will actually
// apply it. With bootstrap.enabled=false the value is inert, so it must not
// keep the server from starting (#243).
func TestCheckStartupSecurity_DisabledBootstrap_DefaultPasswordIgnored(t *testing.T) {
	cfg := secureCfg()
	cfg.Bootstrap.Enabled = false
	cfg.Bootstrap.AdminPassword = "admin123"

	require.NoError(t, checkStartupSecurity(cfg, zap.NewNop().Sugar()))
}

// With bootstrap on, admin123 still fails closed.
func TestCheckStartupSecurity_EnabledBootstrap_DefaultPasswordRefused(t *testing.T) {
	cfg := secureCfg()
	cfg.Bootstrap.Enabled = true
	cfg.Bootstrap.AdminPassword = "admin123"

	err := checkStartupSecurity(cfg, zap.NewNop().Sugar())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin123=true")
}
