package api

import (
	"fmt"

	"github.com/gin-gonic/gin"
)

// trustEveryoneCIDRs is what "*" expands to: both address families, wide open.
var trustEveryoneCIDRs = []string{"0.0.0.0/0", "::/0"}

// ValidateTrustedProxies reports whether http.trusted_proxies is well-formed.
// The server calls it at startup so a mistyped entry stops the boot instead of
// quietly leaving a real proxy untrusted.
func ValidateTrustedProxies(proxies []string) error {
	return applyTrustedProxies(gin.New(), proxies)
}

// applyTrustedProxies tells gin which peers may set X-Forwarded-For.
//
// gin trusts every proxy unless told otherwise, which means c.ClientIP() would
// return whatever the caller writes in the header — and that value is what the
// audit log records and what the rate limiter buckets on. An empty list is the
// shipped default and trusts nobody, so ClientIP falls back to the real peer
// address. "*" restores the trust-everything behavior for deployments that
// cannot enumerate their proxies.
func applyTrustedProxies(r *gin.Engine, proxies []string) error {
	for _, p := range proxies {
		if p == "*" {
			if err := r.SetTrustedProxies(trustEveryoneCIDRs); err != nil {
				return fmt.Errorf("trusted proxies: %w", err)
			}
			return nil
		}
	}
	if err := r.SetTrustedProxies(proxies); err != nil {
		return fmt.Errorf("trusted proxies: %w", err)
	}
	return nil
}
