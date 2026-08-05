package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/config"
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
