package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clientIPFor builds an engine with the given trusted-proxy config and reports
// what c.ClientIP() resolves to for a request carrying a forged
// X-Forwarded-For. The audit log and the rate limiter both key on that value.
func clientIPFor(t *testing.T, trusted []string, remoteAddr, xff string) string {
	t.Helper()
	r := gin.New()
	require.NoError(t, applyTrustedProxies(r, trusted))

	var got string
	r.GET("/ip", func(c *gin.Context) {
		got = c.ClientIP()
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = remoteAddr
	req.Header.Set("X-Forwarded-For", xff)
	r.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestTrustedProxies_EmptyConfig_IgnoresForgedXForwardedFor(t *testing.T) {
	// The shipped default trusts nothing, so a client cannot choose the IP that
	// lands in the audit log or the rate-limit bucket.
	got := clientIPFor(t, nil, "203.0.113.7:5555", "1.2.3.4")
	assert.Equal(t, "203.0.113.7", got)
}

func TestTrustedProxies_ConfiguredProxy_HonorsXForwardedFor(t *testing.T) {
	// Behind a real reverse proxy the header is the only way to see the client.
	got := clientIPFor(t, []string{"203.0.113.7"}, "203.0.113.7:5555", "1.2.3.4")
	assert.Equal(t, "1.2.3.4", got)
}

func TestTrustedProxies_CIDR_HonorsXForwardedFor(t *testing.T) {
	got := clientIPFor(t, []string{"203.0.113.0/24"}, "203.0.113.7:5555", "1.2.3.4")
	assert.Equal(t, "1.2.3.4", got)
}

func TestTrustedProxies_UntrustedPeer_IgnoresXForwardedFor(t *testing.T) {
	// A peer outside the configured range does not get to rewrite its own IP.
	got := clientIPFor(t, []string{"10.0.0.0/8"}, "203.0.113.7:5555", "1.2.3.4")
	assert.Equal(t, "203.0.113.7", got)
}

func TestTrustedProxies_Wildcard_TrustsEveryone(t *testing.T) {
	// Opt-in escape hatch for deployments that cannot enumerate their proxies.
	got := clientIPFor(t, []string{"*"}, "203.0.113.7:5555", "1.2.3.4")
	assert.Equal(t, "1.2.3.4", got)
}

func TestTrustedProxies_InvalidEntry_ReturnsError(t *testing.T) {
	err := applyTrustedProxies(gin.New(), []string{"not-an-ip"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-an-ip")
}

// The server validates the list at startup so a typo fails loudly instead of
// silently leaving the proxy untrusted.
func TestValidateTrustedProxies(t *testing.T) {
	require.NoError(t, ValidateTrustedProxies(nil))
	require.NoError(t, ValidateTrustedProxies([]string{"10.0.0.0/8", "192.168.1.1"}))
	require.NoError(t, ValidateTrustedProxies([]string{"*"}))
	require.Error(t, ValidateTrustedProxies([]string{"10.0.0.0/8", "bogus"}))
}
