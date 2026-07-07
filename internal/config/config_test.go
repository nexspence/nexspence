package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validBase64Key32 returns a 32-byte base64 key suitable for OIDC cookie_key.
func validBase64Key32() string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func validOIDC() OIDCConfig {
	return OIDCConfig{
		Enabled:         true,
		Issuer:          "https://idp.example.com",
		ClientID:        "nexspence",
		ClientSecret:    "s3cret",
		RedirectURL:     "https://app/cb",
		FrontendBaseURL: "https://app",
		Provisioning:    "jit",
		CookieKey:       validBase64Key32(),
		Scopes:          []string{"openid", "profile", "email"},
	}
}

func TestValidateOIDC_Disabled_AlwaysPasses(t *testing.T) {
	c := OIDCConfig{Enabled: false} // all other fields empty
	require.NoError(t, ValidateOIDC(c))
}

func TestValidateOIDC_MissingIssuer_FailsWhenEnabled(t *testing.T) {
	c := validOIDC()
	c.Issuer = ""
	err := ValidateOIDC(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "oidc.issuer")
}

func TestValidateOIDC_MissingClientID_FailsWhenEnabled(t *testing.T) {
	c := validOIDC()
	c.ClientID = ""
	err := ValidateOIDC(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client_id")
}

func TestValidateOIDC_MissingRedirectURL_FailsWhenEnabled(t *testing.T) {
	c := validOIDC()
	c.RedirectURL = ""
	err := ValidateOIDC(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redirect_url")
}

func TestValidateOIDC_AllowlistEmpty_FailsWhenMode(t *testing.T) {
	c := validOIDC()
	c.Provisioning = "allowlist"
	c.EmailAllowlist = nil
	err := ValidateOIDC(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "email_allowlist")
}

func TestValidateOIDC_Allowlist_PassesWithEntries(t *testing.T) {
	c := validOIDC()
	c.Provisioning = "allowlist"
	c.EmailAllowlist = []string{"*@company.com"}
	require.NoError(t, ValidateOIDC(c))
}

func TestValidateOIDC_CookieKey_InvalidBase64_Fails(t *testing.T) {
	c := validOIDC()
	c.CookieKey = "not@base64!!"
	err := ValidateOIDC(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cookie_key")
}

func TestValidateOIDC_CookieKey_WrongLength_Fails(t *testing.T) {
	c := validOIDC()
	// 16-byte key — valid base64 but wrong length for AES-256.
	c.CookieKey = base64.StdEncoding.EncodeToString(make([]byte, 16))
	err := ValidateOIDC(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cookie_key")
}

func TestValidateOIDC_FullValidConfig_Passes(t *testing.T) {
	c := validOIDC()
	c.Provisioning = "jit"
	c.GroupsClaim = "groups"
	c.AdminGroup = "admins"
	c.RoleMappings = map[string]string{"developers": "release-manager"}
	c.ShowLoginButton = true
	c.CookieSecure = true
	c.AllowedSkewSeconds = 60
	require.NoError(t, ValidateOIDC(c))
}

func validAuth() AuthConfig {
	return AuthConfig{JWTSecret: "a-sufficiently-long-unique-secret-value-123"}
}

func TestValidateAuth_Empty_Fails(t *testing.T) {
	err := ValidateAuth(AuthConfig{JWTSecret: ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jwt_secret")
}

func TestValidateAuth_Placeholder_Fails(t *testing.T) {
	err := ValidateAuth(AuthConfig{JWTSecret: "CHANGE_ME_AT_LEAST_32_CHARACTERS_LONG"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "placeholder")
}

func TestValidateAuth_TooShort_Fails(t *testing.T) {
	err := ValidateAuth(AuthConfig{JWTSecret: "short"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "32")
}

func TestValidateAuth_Valid_Passes(t *testing.T) {
	require.NoError(t, ValidateAuth(validAuth()))
}

func TestValidateAuth_EncryptionKey_Valid_Passes(t *testing.T) {
	c := validAuth()
	c.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 32))
	require.NoError(t, ValidateAuth(c))
}

func TestValidateAuth_EncryptionKey_WrongLength_Fails(t *testing.T) {
	c := validAuth()
	c.EncryptionKey = base64.StdEncoding.EncodeToString(make([]byte, 16))
	err := ValidateAuth(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth.encryption_key must be base64-encoded 32 bytes")
}

func TestValidateAuth_EncryptionKey_NotBase64_Fails(t *testing.T) {
	c := validAuth()
	c.EncryptionKey = "%%%not-base64%%%"
	err := ValidateAuth(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth.encryption_key must be base64-encoded 32 bytes")
}

func TestDevDefaultJWTSecret_PassesValidation_ButRecognized(t *testing.T) {
	require.NoError(t, ValidateAuth(AuthConfig{JWTSecret: DevDefaultJWTSecret}),
		"dev default must pass ValidateAuth so quick-start boots")
	require.True(t, IsDevDefaultJWTSecret(DevDefaultJWTSecret))
	require.False(t, IsDevDefaultJWTSecret("some-other-unique-production-secret-value"))
	require.NotEqual(t, exampleJWTSecret, DevDefaultJWTSecret)
}

func TestLoad_AllowInsecureDefaults_DefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "" +
		"database:\n  dsn: \"postgres://u:p@localhost:5432/db?sslmode=disable\"\n" +
		"auth:\n  jwt_secret: \"a-unique-production-secret-at-least-32b\"\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.False(t, cfg.Auth.AllowInsecureDefaults,
		"auth.allow_insecure_defaults must default to false (fail-closed)")
}

func TestLoad_GCDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "" +
		"database:\n  dsn: \"postgres://u:p@localhost:5432/db?sslmode=disable\"\n" +
		"auth:\n  jwt_secret: \"a-unique-production-secret-at-least-32b\"\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.True(t, cfg.GC.Enabled, "gc.enabled default should be true")
	assert.Equal(t, "0 3 * * 0", cfg.GC.Schedule)
	assert.Equal(t, 24*time.Hour, cfg.GC.MinAge)
}

func TestLoad_AllowInsecureDefaults_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "" +
		"database:\n  dsn: \"postgres://u:p@localhost:5432/db?sslmode=disable\"\n" +
		"auth:\n  jwt_secret: \"a-unique-production-secret-at-least-32b\"\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	t.Setenv("NEXSPENCE_AUTH_ALLOW_INSECURE_DEFAULTS", "true")
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.True(t, cfg.Auth.AllowInsecureDefaults)
}
