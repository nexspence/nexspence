package main

import (
	"fmt"

	"github.com/nexspence-oss/nexspence/internal/api"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/logger"
)

// checkStartupSecurity refuses to boot on a configuration that would leave a
// security control silently ineffective, and warns about the ones that are
// merely permissive. Returning an error here is deliberate: a server that
// starts with a broken control is worse than one that does not start.
func checkStartupSecurity(cfg *config.Config, log logger.Logger) error {
	if cfg.Auth.AnonymousEnabled {
		log.Warn("auth.anonymous_enabled is true — repositories with allow_anonymous serve unauthenticated reads; set false to require authentication everywhere")
	}

	// A mistyped entry would leave the real proxy untrusted and every ClientIP
	// wrong — in the audit log and in the rate limiter — so fail loudly.
	if err := api.ValidateTrustedProxies(cfg.HTTP.TrustedProxies); err != nil {
		return fmt.Errorf("invalid http.trusted_proxies: %w", err)
	}
	if len(cfg.HTTP.TrustedProxies) == 0 {
		log.Info("http.trusted_proxies is empty — X-Forwarded-For is ignored; set it when running behind a reverse proxy")
	}

	// Fail closed on shipped insecure defaults unless explicitly allowed
	// (local dev / quick-start sets auth.allow_insecure_defaults=true).
	insecureJWT := config.IsDevDefaultJWTSecret(cfg.Auth.JWTSecret)
	insecureAdmin := cfg.Bootstrap.AdminPassword == "admin123"
	// Only meaningful while OIDC is on: the key seals the state cookie of a
	// login flow that is not running otherwise.
	insecureOIDCKey := cfg.OIDC.Enabled && config.IsDevDefaultOIDCCookieKey(cfg.OIDC.CookieKey)
	if insecureJWT || insecureAdmin || insecureOIDCKey {
		if !cfg.Auth.AllowInsecureDefaults {
			return fmt.Errorf("refusing to start with shipped default secrets (jwt_default=%v, admin123=%v, oidc_cookie_key_default=%v); set unique secrets or auth.allow_insecure_defaults=true for local dev", insecureJWT, insecureAdmin, insecureOIDCKey)
		}
		if insecureOIDCKey {
			log.Warn("oidc.cookie_key is the shipped development default — an attacker who knows it can forge the OIDC state cookie; set a unique key (NEXSPENCE_OIDC_COOKIE_KEY, openssl rand -base64 32) before production use")
		}
		if insecureJWT {
			log.Warn("auth.jwt_secret is the shipped development default — set a unique secret (NEXSPENCE_AUTH_JWT_SECRET) before production use")
		}
		if insecureAdmin {
			log.Warn("bootstrap.admin_password is the shipped development default (admin123) — set a unique password (NEXSPENCE_BOOTSTRAP_ADMIN_PASSWORD) before production use")
		}
	}
	return nil
}
