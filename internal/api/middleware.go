package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/logger"
)

func requestLogger(log logger.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Infow("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency", time.Since(start),
			"ip", c.ClientIP(),
		)
	}
}

// corsMiddleware reflects an Origin only when it is present in allowed.
//
// An empty list sends no Access-Control-Allow-Origin at all. It used to mean
// "wildcard", which was exploitable: this API authenticates by Authorization
// header and serves anonymous repositories, so a page the user visits could
// fetch an internal artifact from inside their network and read the response.
// A wildcard now has to be written out as cors_origins: ["*"].
func corsMiddleware(allowed []string) gin.HandlerFunc {
	wildcard := false
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		if o == "*" {
			wildcard = true
		}
		set[o] = struct{}{}
	}
	return func(c *gin.Context) {
		if wildcard {
			c.Header("Access-Control-Allow-Origin", "*")
		} else if origin := c.GetHeader("Origin"); origin != "" {
			if _, ok := set[origin]; ok {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Writer.Header().Add("Vary", "Origin")
			}
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, HEAD")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Requested-With")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// DefaultCSP is the policy served with the bundled UI.
//
// The SPA stores its JWT in localStorage — normal for a bearer-token client,
// but it means any future XSS is a full token theft. This is the defense in
// depth for that. Everything the UI needs is same-origin: Vite emits a module
// script and a stylesheet, small assets arrive as data: URIs, and the API is
// on the same host.
//
// style-src keeps 'unsafe-inline' because component libraries inject <style>
// at runtime; script-src deliberately does not, which is the half that matters.
// The two Google Fonts origins are the only third parties the UI references
// (frontend/index.html); a policy without them renders the app in a fallback
// font and says so only in the console. Self-hosting the fonts would let both
// entries go.
const DefaultCSP = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"img-src 'self' data:; " +
	"font-src 'self' data: https://fonts.gstatic.com; " +
	"connect-src 'self'; " +
	"object-src 'none'; " +
	"base-uri 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'"

// cspExemptPrefixes are the paths that serve user-uploaded content rather than
// the UI. Raw repositories are used to host documentation sites, which carry
// their own scripts and styles; applying the UI policy to them would break a
// shipped feature. They are not covered by this header — the isolation there
// comes from hosting such sites on their own repository, not from CSP.
var cspExemptPrefixes = []string{"/repository/", "/v2/"}

// securityHeaders sets baseline hardening response headers, including the
// Content-Security-Policy for UI and API responses. An empty policy omits the
// CSP header entirely.
func securityHeaders(policy string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		if policy != "" && !hasAnyPrefix(c.Request.URL.Path, cspExemptPrefixes) {
			c.Header("Content-Security-Policy", policy)
		}
		c.Next()
	}
}

// cspPolicy resolves the configured policy: unset means the default, and the
// literal "off" disables the header for operators whose reverse proxy sets its
// own. Distinguishing "unset" from "off" is why this is not just an empty
// string default.
func cspPolicy(cfg *config.Config) string {
	switch cfg.HTTP.CSP {
	case "":
		return DefaultCSP
	case "off":
		return ""
	default:
		return cfg.HTTP.CSP
	}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// bodyLimit caps request body size at maxMB megabytes, except for paths that
// begin with an exempt prefix (large legitimate artifact uploads).
func bodyLimit(maxMB int, exemptPrefixes []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		for _, p := range exemptPrefixes {
			if strings.HasPrefix(c.Request.URL.Path, p) {
				c.Next()
				return
			}
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, int64(maxMB)<<20)
		c.Next()
	}
}
