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
	if repoName != "" && strings.HasPrefix(r.URL.Path, "/v2/") && r.URL.Path != "/v2/" {
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
	return sub
}
