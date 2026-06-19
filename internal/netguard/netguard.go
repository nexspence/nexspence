// Package netguard provides SSRF protection for HTTP clients that fetch
// user-configured URLs (webhooks, proxy upstreams, replication targets).
//
// The guard runs in the dialer's Control hook, which fires AFTER DNS
// resolution on the resolved IP — so hostnames that resolve to internal
// addresses (e.g. metadata services, RFC1918 ranges, loopback) are blocked
// too, not just literal internal IPs.
package netguard

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// isBlockedIP reports whether s is an IP that requests must not reach.
// Unparseable input fails closed (blocked).
func isBlockedIP(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

// control is the net.Dialer Control hook that rejects connections to blocked IPs.
func control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("netguard: invalid dial address %q: %w", address, err)
	}
	if isBlockedIP(host) {
		return fmt.Errorf("netguard: blocked connection to internal address %q", host)
	}
	return nil
}

// Client returns an *http.Client whose dialer rejects connections to internal
// addresses. timeout is the overall request timeout (mirrors the timeout the
// caller previously used on its plain client).
func Client(timeout time.Duration) *http.Client {
	d := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: control,
	}
	tr := &http.Transport{DialContext: d.DialContext}
	return &http.Client{Timeout: timeout, Transport: tr}
}

// DialControl is the dialer Control hook exported for callers that build their
// own *http.Transport (e.g. with custom connection-pool tuning) and want to
// add the same SSRF guard via net.Dialer{Control: netguard.DialControl}.
func DialControl(network, address string, c syscall.RawConn) error {
	return control(network, address, c)
}
