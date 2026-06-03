package auth_test

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
)

// minimalIdPMetaXML is a valid IdP EntityDescriptor with a Redirect SSO binding.
const minimalIdPMetaXML = `<?xml version="1.0"?>
<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://idp.example.com">
  <IDPSSODescriptor WantAuthnRequestsSigned="false"
    protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect"
      Location="https://idp.example.com/sso"/>
    <SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST"
      Location="https://idp.example.com/sso"/>
  </IDPSSODescriptor>
</EntityDescriptor>`

func testSAMLCfg() config.SAMLConfig {
	return config.SAMLConfig{
		Enabled:           true,
		DisplayName:       "Test IdP",
		SPEntityID:        "https://sp.example.com/saml",
		ACSURL:            "https://sp.example.com/api/v1/auth/saml/acs",
		IDPMetadataXML:    minimalIdPMetaXML,
		Provisioning:      "jit",
		EmailAttribute:    "email",
		UsernameAttribute: "uid",
		NameAttribute:     "displayName",
		GroupsAttribute:   "groups",
	}
}

func newTestSAMLService(t *testing.T) *auth.SAMLService {
	t.Helper()
	svc, err := auth.NewSAMLService(testSAMLCfg())
	require.NoError(t, err)
	return svc
}

// ── Relay State ────────────────────────────────────────────────

func TestSAMLService_SignVerifyRelayState_RoundTrip(t *testing.T) {
	svc := newTestSAMLService(t)
	rs := svc.SignRelayState("/repositories")
	returnTo, err := svc.VerifyRelayState(rs)
	require.NoError(t, err)
	assert.Equal(t, "/repositories", returnTo)
}

func TestSAMLService_VerifyRelayState_TamperedSig_Fails(t *testing.T) {
	svc := newTestSAMLService(t)
	rs := svc.SignRelayState("/repositories")
	parts := strings.SplitN(rs, ".", 2)
	sigBytes, _ := base64.RawURLEncoding.DecodeString(parts[1])
	sigBytes[len(sigBytes)-1] ^= 0xFF
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(sigBytes)
	_, err := svc.VerifyRelayState(tampered)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature invalid")
}

func TestSAMLService_VerifyRelayState_MissingDot_Fails(t *testing.T) {
	svc := newTestSAMLService(t)
	_, err := svc.VerifyRelayState("nodothere")
	require.Error(t, err)
}

// ── MetadataXML ────────────────────────────────────────────────

func TestSAMLService_MetadataXML_ContainsEntityID(t *testing.T) {
	svc := newTestSAMLService(t)
	xmlBytes, err := svc.MetadataXML()
	require.NoError(t, err)
	xmlStr := string(xmlBytes)
	assert.Contains(t, xmlStr, "https://sp.example.com/saml")
	assert.Contains(t, xmlStr, "https://sp.example.com/api/v1/auth/saml/acs")
}

// ── AuthnRequestURL ────────────────────────────────────────────

func TestSAMLService_AuthnRequestURL_ContainsSAMLRequest(t *testing.T) {
	svc := newTestSAMLService(t)
	rs := svc.SignRelayState("/")
	redirectURL, err := svc.AuthnRequestURL(rs)
	require.NoError(t, err)
	assert.Contains(t, redirectURL, "SAMLRequest=")
	assert.Contains(t, redirectURL, "idp.example.com/sso")
}

// ── NewSAMLService error paths ─────────────────────────────────

func TestNewSAMLService_EphemeralKeyPair_NoError(t *testing.T) {
	cfg := testSAMLCfg()
	cfg.SPKeyPEM = ""
	cfg.SPCertPEM = ""
	_, err := auth.NewSAMLService(cfg)
	require.NoError(t, err)
}

func TestNewSAMLService_InvalidMetadataXML_Fails(t *testing.T) {
	cfg := testSAMLCfg()
	cfg.IDPMetadataXML = "<not valid xml"
	cfg.IDPMetadataURL = ""
	_, err := auth.NewSAMLService(cfg)
	require.Error(t, err)
}

func TestNewSAMLService_HMACKey_32Bytes_OK(t *testing.T) {
	cfg := testSAMLCfg()
	key := make([]byte, 32)
	cfg.HMACKey = base64.StdEncoding.EncodeToString(key)
	_, err := auth.NewSAMLService(cfg)
	require.NoError(t, err)
}

func TestNewSAMLService_HMACKey_WrongLength_Fails(t *testing.T) {
	cfg := testSAMLCfg()
	cfg.HMACKey = base64.StdEncoding.EncodeToString([]byte("tooshort"))
	_, err := auth.NewSAMLService(cfg)
	require.Error(t, err)
}

// Compile-time interface check.
var _ auth.SAMLAuthenticator = (*auth.SAMLService)(nil)

func TestSAMLService_ParseResponse_MissingBody_ReturnsError(t *testing.T) {
	svc := newTestSAMLService(t)
	r, _ := http.NewRequest(http.MethodPost, "/acs", strings.NewReader("SAMLResponse=invalid"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	_, err := svc.ParseResponse(r)
	require.Error(t, err)
}
