package config

import (
	"encoding/base64"
	"testing"

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
