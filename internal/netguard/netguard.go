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
	"sync/atomic"
	"syscall"
	"time"
)

// cgnat is RFC 6598 shared address space. It is not "private" as far as Go's
// net package is concerned, but nothing outbound should ever reach it — and
// some providers put their metadata service there.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// allowedInternal holds the operator's opt-out: ranges that are internal but
// deliberately reachable, e.g. an on-prem registry being proxied. Stored as an
// atomic value so the dial hook stays lock-free on the hot path.
var allowedInternal atomic.Pointer[[]*net.IPNet]

// SetAllowedInternalCIDRs records the ranges that may be reached despite being
// internal. Entries are CIDRs or bare addresses (treated as a single host).
// The list is validated in full before being applied, so a typo leaves the
// previous policy in place rather than half-applying a new one. A nil or empty
// list restores the default of blocking every internal range.
func SetAllowedInternalCIDRs(entries []string) error {
	nets := make([]*net.IPNet, 0, len(entries))
	for _, e := range entries {
		n, err := parseCIDROrIP(e)
		if err != nil {
			return fmt.Errorf("netguard: invalid allowed internal target %q: %w", e, err)
		}
		nets = append(nets, n)
	}
	allowedInternal.Store(&nets)
	return nil
}

func parseCIDROrIP(s string) (*net.IPNet, error) {
	if _, n, err := net.ParseCIDR(s); err == nil {
		return n, nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, fmt.Errorf("not a CIDR or IP address")
	}
	bits := 32
	if ip.To4() == nil {
		bits = 128
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
}

// isAllowlisted reports whether ip was explicitly permitted by the operator.
func isAllowlisted(ip net.IP) bool {
	nets := allowedInternal.Load()
	if nets == nil {
		return false
	}
	for _, n := range *nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// isBlockedIP reports whether s is an IP that requests must not reach.
// Unparseable input fails closed (blocked).
func isBlockedIP(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return true
	}
	// The operator's allowlist wins: naming a range is them saying they know
	// what is there, including link-local if they spell it out.
	if isAllowlisted(ip) {
		return false
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip.Equal(net.IPv4bcast) ||
		cgnat.Contains(ip)
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
