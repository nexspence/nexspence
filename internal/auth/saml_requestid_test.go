package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/auth"
)

// crewjam/saml refuses an assertion whose InResponseTo is not among the request
// IDs handed to ParseResponse, and the SP was passing nil — so no SP-initiated
// login could ever succeed, and there was nothing binding an assertion to the
// request that asked for it.

func TestSAML_AuthnRequest_ReturnsTheRequestID(t *testing.T) {
	svc := newTestSAMLService(t)

	url, id, err := svc.AuthnRequest("relay")
	require.NoError(t, err)
	assert.Contains(t, url, "SAMLRequest=")
	require.NotEmpty(t, id, "the caller needs the request ID to bind the response to it")
	assert.True(t, strings.HasPrefix(id, "id-"), "crewjam generates id-prefixed request IDs, got %q", id)
}

func TestSAML_AuthnRequest_IDsAreUnique(t *testing.T) {
	svc := newTestSAMLService(t)

	_, first, err := svc.AuthnRequest("relay")
	require.NoError(t, err)
	_, second, err := svc.AuthnRequest("relay")
	require.NoError(t, err)

	assert.NotEqual(t, first, second, "a reused ID would let one assertion be replayed against another login")
}

// The ID travels in a signed cookie between the redirect and the ACS post, the
// same shape as the relay state.

func TestSAML_RequestIDCookie_RoundTrip(t *testing.T) {
	svc := newTestSAMLService(t)

	sealed := svc.SignRequestID("id-abc123")
	got, err := svc.VerifyRequestID(sealed)
	require.NoError(t, err)
	assert.Equal(t, "id-abc123", got)
}

func TestSAML_RequestIDCookie_TamperedSignatureRejected(t *testing.T) {
	svc := newTestSAMLService(t)

	sealed := svc.SignRequestID("id-abc123")
	parts := strings.SplitN(sealed, ".", 2)
	require.Len(t, parts, 2)

	_, err := svc.VerifyRequestID(parts[0] + ".AAAA")
	assert.Error(t, err, "an attacker must not be able to name the request ID themselves")
}

func TestSAML_RequestIDCookie_MalformedRejected(t *testing.T) {
	svc := newTestSAMLService(t)

	for _, bad := range []string{"", "nodot", "!!!.!!!"} {
		_, err := svc.VerifyRequestID(bad)
		assert.Error(t, err, "input %q", bad)
	}
}

func TestSAML_RequestIDCookie_Expires(t *testing.T) {
	svc := newTestSAMLService(t)

	stale := svc.SignRequestIDAt("id-abc123", time.Now().Add(-2*auth.SAMLRequestIDTTL))
	_, err := svc.VerifyRequestID(stale)
	assert.Error(t, err, "a login started long ago must not still be completable")
}

// With no cookie there is no request to bind to, so the assertion is refused
// rather than accepted as IdP-initiated.
func TestSAML_ParseResponse_WithoutRequestIDs_IsRefused(t *testing.T) {
	svc := newTestSAMLService(t)

	r := httptest.NewRequest(http.MethodPost, "/acs", strings.NewReader("SAMLResponse=abc"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	_, err := svc.ParseResponse(r, nil)
	require.Error(t, err)
}
