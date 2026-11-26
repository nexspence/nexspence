package publicurl_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nexspence-oss/nexspence/internal/publicurl"
)

// request builds a request from a peer address, with forwarded headers.
func request(remoteAddr string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "http://internal:8081/repository/npm/lodash", nil)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// A proxy this instance trusts speaks for the client: its X-Forwarded-Host is
// the host the client reached the instance on, and that is the host an
// absolute link has to name.
func TestPublicURL_TrustedProxyHostIsUsed(t *testing.T) {
	r := publicurl.NewResolver("http://localhost:8081", []string{"10.0.0.0/8"})

	got := r.PublicBaseURL(request("10.1.2.3:5000", map[string]string{
		"X-Forwarded-Host": "nexus.example.com",
	}))
	assert.Equal(t, "http://nexus.example.com", got)
}

// A request that did not come through a trusted proxy never has its host
// believed, whoever sent the header: the Host header and
// X-Forwarded-Host are the client's word, and a document that names it sends
// every later client wherever the client chose.
func TestPublicURL_UntrustedPeerHeaderIsIgnored(t *testing.T) {
	r := publicurl.NewResolver("http://localhost:8081", []string{"10.0.0.0/8"})

	got := r.PublicBaseURL(request("203.0.113.9:5000", map[string]string{
		"X-Forwarded-Host": "evil.example.com",
	}))
	assert.Equal(t, "http://localhost:8081", got)
}

// With no trusted proxy configured, nothing is believed either.
func TestPublicURL_NoTrustedProxiesTrustsNobody(t *testing.T) {
	r := publicurl.NewResolver("http://localhost:8081", nil)

	got := r.PublicBaseURL(request("10.1.2.3:5000", map[string]string{
		"X-Forwarded-Host": "nexus.example.com",
	}))
	assert.Equal(t, "http://localhost:8081", got)
}

// "*" trusts every peer — the escape hatch for deployments that cannot
// enumerate their proxies.
func TestPublicURL_TrustEveryone(t *testing.T) {
	r := publicurl.NewResolver("http://localhost:8081", []string{"*"})

	got := r.PublicBaseURL(request("203.0.113.9:5000", map[string]string{
		"X-Forwarded-Host": "nexus.example.com",
	}))
	assert.Equal(t, "http://nexus.example.com", got)
}

// A proxy that appends to the header leaves the client's own claim to the
// left of it; the rightmost value is the proxy's.
func TestPublicURL_TheRightmostForwardedHostWins(t *testing.T) {
	r := publicurl.NewResolver("http://localhost:8081", []string{"10.0.0.0/8"})

	got := r.PublicBaseURL(request("10.1.2.3:5000", map[string]string{
		"X-Forwarded-Host": "evil.example.com, nexus.example.com",
	}))
	assert.Equal(t, "http://nexus.example.com", got)
}

// The scheme comes from the proxy too — an instance behind a TLS-terminating
// proxy serves plain HTTP to the proxy, and the client saw HTTPS.
func TestPublicURL_ForwardedProtoNamesTheScheme(t *testing.T) {
	r := publicurl.NewResolver("http://localhost:8081", []string{"10.0.0.0/8"})

	got := r.PublicBaseURL(request("10.1.2.3:5000", map[string]string{
		"X-Forwarded-Host":  "nexus.example.com",
		"X-Forwarded-Proto": "https",
	}))
	assert.Equal(t, "https://nexus.example.com", got)
}

// A scheme that is not http or https is not believed; the configured one
// stands.
func TestPublicURL_OnlyHTTPAndHTTPSAreBelieved(t *testing.T) {
	r := publicurl.NewResolver("https://nexus.example.com", []string{"10.0.0.0/8"})

	got := r.PublicBaseURL(request("10.1.2.3:5000", map[string]string{
		"X-Forwarded-Host":  "other.example.com",
		"X-Forwarded-Proto": "gopher",
	}))
	assert.Equal(t, "https://other.example.com", got)
}

// A value that is not a host is refused: the header names a host, and
// anything else is a path, a userinfo or a scheme in disguise.
func TestPublicURL_AMalformedForwardedHostIsRefused(t *testing.T) {
	for _, value := range []string{
		"nexus.example.com/evil",
		"user@nexus.example.com",
		"http://nexus.example.com",
		"nexus.example.com?x=1",
	} {
		t.Run(value, func(t *testing.T) {
			r := publicurl.NewResolver("http://localhost:8081", []string{"10.0.0.0/8"})

			got := r.PublicBaseURL(request("10.1.2.3:5000", map[string]string{
				"X-Forwarded-Host": value,
			}))
			assert.Equal(t, "http://localhost:8081", got)
		})
	}
}

// A host with a port is a host: the public port is part of what the client
// reached.
func TestPublicURL_AForwardedHostMayCarryAPort(t *testing.T) {
	r := publicurl.NewResolver("http://localhost:8081", []string{"10.0.0.0/8"})

	got := r.PublicBaseURL(request("10.1.2.3:5000", map[string]string{
		"X-Forwarded-Host": "nexus.example.com:8443",
	}))
	assert.Equal(t, "http://nexus.example.com:8443", got)
}

// An instance served under a sub-path keeps it: only the host is taken from
// the proxy.
func TestPublicURL_TheConfiguredPathIsKept(t *testing.T) {
	r := publicurl.NewResolver("https://nexus.example.com/registry", []string{"10.0.0.0/8"})

	got := r.PublicBaseURL(request("10.1.2.3:5000", map[string]string{
		"X-Forwarded-Host": "other.example.com",
	}))
	assert.Equal(t, "https://other.example.com/registry", got)
}

// Without a configured base URL, a trusted proxy is the only thing that can
// say what the public URL is.
func TestPublicURL_AnUnconfiguredBaseURLTakesTheProxyHost(t *testing.T) {
	r := publicurl.NewResolver("", []string{"10.0.0.0/8"})

	got := r.PublicBaseURL(request("10.1.2.3:5000", map[string]string{
		"X-Forwarded-Host":  "nexus.example.com",
		"X-Forwarded-Proto": "https",
	}))
	assert.Equal(t, "https://nexus.example.com", got)
}

// A single IP is a trusted peer too, not only a CIDR.
func TestPublicURL_ASingleIPIsATrustedPeer(t *testing.T) {
	r := publicurl.NewResolver("http://localhost:8081", []string{"10.1.2.3"})

	got := r.PublicBaseURL(request("10.1.2.3:5000", map[string]string{
		"X-Forwarded-Host": "nexus.example.com",
	}))
	assert.Equal(t, "http://nexus.example.com", got)

	other := r.PublicBaseURL(request("10.1.2.4:5000", map[string]string{
		"X-Forwarded-Host": "nexus.example.com",
	}))
	assert.Equal(t, "http://localhost:8081", other)
}

// An IPv6 peer is matched like any other.
func TestPublicURL_AnIPv6PeerIsMatched(t *testing.T) {
	r := publicurl.NewResolver("http://localhost:8081", []string{"2001:db8::/32"})

	got := r.PublicBaseURL(request("[2001:db8::1]:5000", map[string]string{
		"X-Forwarded-Host": "nexus.example.com",
	}))
	assert.Equal(t, "http://nexus.example.com", got)
}

// A peer address the server cannot read is not a trusted peer.
func TestPublicURL_AnUnreadablePeerAddressIsNotTrusted(t *testing.T) {
	r := publicurl.NewResolver("http://localhost:8081", []string{"*"})

	got := r.PublicBaseURL(request("", map[string]string{
		"X-Forwarded-Host": "nexus.example.com",
	}))
	assert.Equal(t, "http://nexus.example.com", got,
		"\"*\" trusts every peer, including one whose address cannot be read")
}

// A list the server itself refuses to boot on is refused here too: a
// resolver that quietly trusted the readable half of it would believe a
// header the server had stopped trusting.
func TestPublicURL_AMistypedProxyIsNotTrusted(t *testing.T) {
	r := publicurl.NewResolver("http://localhost:8081", []string{"10.0.0.0/8", "not-an-ip"})

	got := r.PublicBaseURL(request("10.1.2.3:5000", map[string]string{
		"X-Forwarded-Host": "nexus.example.com",
	}))
	assert.Equal(t, "http://localhost:8081", got)
}
