package netguard

import (
	"testing"
)

func resetAllowlist(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { SetAllowedInternalCIDRs(nil) })
}

// Ranges an operator never means to reach outbound, and which the original
// guard let through.
func TestIsBlockedIP_ExtraRanges(t *testing.T) {
	for _, ip := range []string{
		"100.64.0.1",       // CGNAT (RFC 6598) — some cloud metadata lives here
		"100.100.100.200",  // Alibaba Cloud metadata
		"255.255.255.255",  // broadcast
		"224.0.0.1",        // multicast
		"::ffff:127.0.0.1", // IPv4-mapped loopback
		"fe80::1",          // IPv6 link-local
		"fc00::1",          // IPv6 unique-local
	} {
		if !isBlockedIP(ip) {
			t.Errorf("expected %q to be blocked", ip)
		}
	}
}

// On-prem deployments legitimately proxy an internal registry, so the guard
// needs a documented way to say "this range is fine" — without that, the only
// workaround is switching the guard off entirely.
func TestAllowlist_PermitsConfiguredRange(t *testing.T) {
	resetAllowlist(t)

	if !isBlockedIP("10.10.0.7") {
		t.Fatal("precondition: private range is blocked by default")
	}

	if err := SetAllowedInternalCIDRs([]string{"10.10.0.0/16"}); err != nil {
		t.Fatalf("SetAllowedInternalCIDRs: %v", err)
	}

	if isBlockedIP("10.10.0.7") {
		t.Error("expected an explicitly allowed range to pass")
	}
	if !isBlockedIP("10.20.0.7") {
		t.Error("expected a private address outside the allowlist to stay blocked")
	}
	if !isBlockedIP("169.254.169.254") {
		t.Error("expected link-local to stay blocked when it is not allowlisted")
	}
}

// The allowlist is the operator saying "I know what is there", so it covers
// link-local too when named explicitly — nothing is special-cased away.
func TestAllowlist_CanPermitLinkLocalExplicitly(t *testing.T) {
	resetAllowlist(t)

	if err := SetAllowedInternalCIDRs([]string{"169.254.169.254/32"}); err != nil {
		t.Fatalf("SetAllowedInternalCIDRs: %v", err)
	}
	if isBlockedIP("169.254.169.254") {
		t.Error("expected the explicitly allowed metadata address to pass")
	}
	if !isBlockedIP("169.254.1.1") {
		t.Error("expected the rest of link-local to stay blocked")
	}
}

func TestAllowlist_SingleAddressWithoutMask(t *testing.T) {
	resetAllowlist(t)

	if err := SetAllowedInternalCIDRs([]string{"192.168.5.10"}); err != nil {
		t.Fatalf("SetAllowedInternalCIDRs: %v", err)
	}
	if isBlockedIP("192.168.5.10") {
		t.Error("expected the bare address to be treated as a /32")
	}
	if !isBlockedIP("192.168.5.11") {
		t.Error("expected neighboring addresses to stay blocked")
	}
}

// A typo must be rejected loudly at startup rather than silently widening or
// narrowing what the server may reach.
func TestAllowlist_RejectsGarbage(t *testing.T) {
	resetAllowlist(t)

	if err := SetAllowedInternalCIDRs([]string{"10.0.0.0/8", "not-a-cidr"}); err == nil {
		t.Fatal("expected an error for an unparsable entry")
	}
	if !isBlockedIP("10.0.0.1") {
		t.Error("a rejected allowlist must not be applied even partially")
	}
}

func TestAllowlist_EmptyRestoresDefault(t *testing.T) {
	resetAllowlist(t)

	if err := SetAllowedInternalCIDRs([]string{"10.0.0.0/8"}); err != nil {
		t.Fatalf("SetAllowedInternalCIDRs: %v", err)
	}
	if isBlockedIP("10.0.0.1") {
		t.Fatal("precondition: allowlisted range passes")
	}

	if err := SetAllowedInternalCIDRs(nil); err != nil {
		t.Fatalf("SetAllowedInternalCIDRs(nil): %v", err)
	}
	if !isBlockedIP("10.0.0.1") {
		t.Error("clearing the allowlist must restore the default deny")
	}
}
