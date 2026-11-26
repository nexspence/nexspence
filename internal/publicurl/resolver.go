// Package publicurl answers what the public URL of this instance is for a
// request.
//
// The host of a request is never taken from the request itself. The Host
// header is the client's word, and a document that names it — an npm
// packument's dist.tarball, a search result — is a document a client can be
// sent anywhere. That is the cache-poisoning vector: whoever can reach the
// instance once can leave a document behind that names a host of their
// choosing, and every later client follows it.
//
// So the public host comes from two places only:
//
//   - http.base_url — the deployment's own statement of its public URL, and
//     the fallback for everything;
//   - the X-Forwarded-Host of a proxy this instance trusts, and only when the
//     request actually came from that proxy. The header is then the proxy's
//     word about the host the client used, which is what a deployment serving
//     several public hostnames needs.
//
// A request that did not come through a trusted proxy never has its host
// believed, whoever sent the header.
package publicurl

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// Resolver computes the public base URL of a request.
//
// It is safe for concurrent use: it holds configuration, not request state.
type Resolver struct {
	scheme   string
	host     string
	path     string
	base     string
	prefixes []netip.Prefix
	trustAll bool
	invalid  bool
}

// NewResolver builds a resolver from the deployment's public URL and the
// peers whose forwarded headers it believes.
//
// trustedProxies holds IPs and CIDRs, and "*" for "every peer" — the same
// list and the same meaning as http.trusted_proxies, which decides whose
// X-Forwarded-For the audit log believes.
func NewResolver(baseURL string, trustedProxies []string) *Resolver {
	r := &Resolver{base: strings.TrimRight(strings.TrimSpace(baseURL), "/")}
	if u, err := url.Parse(r.base); err == nil && u.Host != "" {
		r.scheme = u.Scheme
		r.host = u.Host
		r.path = strings.TrimRight(u.Path, "/")
	}
	for _, p := range trustedProxies {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == "*" {
			r.trustAll = true
			continue
		}
		if prefix, err := netip.ParsePrefix(p); err == nil {
			r.prefixes = append(r.prefixes, prefix.Masked())
			continue
		}
		if addr, err := netip.ParseAddr(p); err == nil {
			r.prefixes = append(r.prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		// An entry that is neither an IP nor a CIDR is a mistake in the
		// configuration, and the list is refused as a whole: the server
		// refuses to boot on such a list, and a resolver that quietly trusted
		// the readable half of it would believe a header the server itself
		// stopped trusting.
		r.prefixes = nil
		r.trustAll = false
		r.invalid = true
	}
	return r
}

// PublicBaseURL returns the base URL a client used to reach this instance —
// the URL that belongs in the absolute links this instance hands out.
//
// It returns the configured base URL for a request that did not come through
// a trusted proxy, and "" when neither the configuration nor a trusted proxy
// says what the public URL is.
func (r *Resolver) PublicBaseURL(req *http.Request) string {
	if r == nil {
		return ""
	}
	if req == nil || !r.trustsPeer(req) {
		return r.base
	}
	host := forwardedHost(req)
	if host == "" {
		host = r.host
	}
	scheme := forwardedProto(req)
	if scheme == "" {
		scheme = r.scheme
	}
	if scheme == "" {
		scheme = "http"
	}
	if host == "" {
		return r.base
	}
	return scheme + "://" + host + r.path
}

// trustsPeer reports whether the request came from a peer this instance
// trusts to speak for the client.
//
// The peer is the address the request came from, not anything the request
// says about it. A client that reaches the instance directly — around the
// proxy — is not trusted, whatever headers it carries.
func (r *Resolver) trustsPeer(req *http.Request) bool {
	if r.invalid {
		return false
	}
	if r.trustAll {
		return true
	}
	if len(r.prefixes) == 0 {
		return false
	}
	addr, ok := peerAddr(req.RemoteAddr)
	if !ok {
		return false
	}
	for _, prefix := range r.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// peerAddr extracts the IP address a request came from.
func peerAddr(remoteAddr string) (netip.Addr, bool) {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		remoteAddr = host
	}
	addr, err := netip.ParseAddr(strings.Trim(remoteAddr, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// forwardedHost reads the host the client used from the proxy's
// X-Forwarded-Host.
//
// The rightmost value is the one to believe: a proxy that overwrites the
// header leaves one value, and a proxy that appends to it puts its own view
// last — the client's own claim, if it sent one, is to the left of it.
//
// The value is a host, optionally with a port. Anything else — a path, a
// userinfo, a scheme — is not a host and is refused.
func forwardedHost(req *http.Request) string {
	raw := lastHeaderValue(req.Header.Get("X-Forwarded-Host"))
	if raw == "" {
		return ""
	}
	u, err := url.Parse("//" + raw)
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return u.Host
}

// forwardedProto reads the scheme the client used from the proxy's
// X-Forwarded-Proto. Only http and https are believed.
func forwardedProto(req *http.Request) string {
	raw := strings.ToLower(lastHeaderValue(req.Header.Get("X-Forwarded-Proto")))
	switch raw {
	case "http", "https":
		return raw
	}
	return ""
}

// lastHeaderValue returns the rightmost value of a comma-separated header.
func lastHeaderValue(header string) string {
	parts := strings.Split(header, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		if v := strings.TrimSpace(parts[i]); v != "" {
			return v
		}
	}
	return ""
}
