package api

import (
	"net/http"
	"strings"
)

// SubdomainRewriter is an http.Handler wrapper that rewrites Docker /v2/* paths
// for subdomain-based repository access.
//
// When a request arrives with Host matching "*.<baseDomain>", the subdomain is
// extracted as the repository name and injected into the URL path:
//
//	/v2/alpine/manifests/latest  →  /v2/<repoName>/alpine/manifests/latest
//	/v2/                         →  /v2/  (unchanged — OCI version check)
//
// This makes the existing /v2/:repoName/*dockerpath Gin routes work transparently.
type SubdomainRewriter struct {
	next       http.Handler
	baseDomain string // lower-cased, e.g. "nexspence.example.com"
}

// NewSubdomainRewriter wraps next with subdomain path rewriting.
// baseDomain must NOT have a leading dot (e.g. "nexspence.example.com").
func NewSubdomainRewriter(next http.Handler, baseDomain string) http.Handler {
	return &SubdomainRewriter{next: next, baseDomain: strings.ToLower(baseDomain)}
}

func (s *SubdomainRewriter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	repoName := s.extractRepo(r.Host)
	// "/v2/" (the ping) and "/v2/token" (the auth realm the ping's challenge
	// points at) are registry-level endpoints: rewriting them into a repo
	// dispatch would 404 the token fetch and break docker login/pull on
	// subdomain hosts.
	if repoName != "" && strings.HasPrefix(r.URL.Path, "/v2/") && r.URL.Path != "/v2/" && r.URL.Path != "/v2/token" {
		// Rewrite /v2/<imagepath> → /v2/<repoName>/<imagepath>
		suffix := strings.TrimPrefix(r.URL.Path, "/v2/")
		r.URL.Path = "/v2/" + repoName + "/" + suffix
		if r.URL.RawPath != "" {
			rawSuffix := strings.TrimPrefix(r.URL.RawPath, "/v2/")
			r.URL.RawPath = "/v2/" + repoName + "/" + rawSuffix
		}
	}
	s.next.ServeHTTP(w, r)
}

// extractRepo returns the subdomain when Host matches "*.<baseDomain>".
// Returns "" when the pattern doesn't match (passthrough).
func (s *SubdomainRewriter) extractRepo(host string) string {
	// Strip port if present.
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.ToLower(host)
	suffix := "." + s.baseDomain
	if !strings.HasSuffix(host, suffix) {
		return ""
	}
	sub := strings.TrimSuffix(host, suffix)
	// Only single-level subdomains are supported.
	if sub == "" || strings.Contains(sub, ".") {
		return ""
	}
	// The value is spliced into the URL path, so accept only what a repository
	// name can look like. RBAC still runs on the result, so this is not the
	// boundary that stops a bypass — it stops a host header from injecting
	// separators or encoded segments into a path we build.
	if !isRepoNameLabel(sub) {
		return ""
	}
	return sub
}

// isRepoNameLabel reports whether s is a lower-case DNS-style label:
// [a-z0-9] separated by single hyphens, no leading or trailing hyphen.
func isRepoNameLabel(s string) bool {
	if s == "" || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}
