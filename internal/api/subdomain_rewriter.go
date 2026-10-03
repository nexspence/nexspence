package api

import (
	"net/http"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/domain"
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
// An explicit hostname alias wins over the subdomain pattern, so a legacy DNS
// name can keep serving a repository whose name does not match it (#282).
//
// This makes the existing /v2/:repoName/*dockerpath Gin routes work transparently.
type SubdomainRewriter struct {
	next       http.Handler
	baseDomain string            // lower-cased, e.g. "nexspence.example.com"
	aliases    map[string]string // lower-cased full hostname → repository name
}

// NewSubdomainRewriter wraps next with subdomain path rewriting.
// baseDomain must NOT have a leading dot (e.g. "nexspence.example.com").
// aliases maps full client hostnames to repository names; alias hostnames do
// not have to sit under baseDomain, and an alias for "<sub>.<baseDomain>"
// overrides the implicit "<sub>" repository. Alias targets that no docker
// repository could be named are ignored — the value is spliced into a URL path,
// and a config typo must not become a path injection.
func NewSubdomainRewriter(next http.Handler, baseDomain string, aliases map[string]string) http.Handler {
	m := make(map[string]string, len(aliases))
	for host, repo := range aliases {
		host = strings.ToLower(strings.TrimSpace(host))
		repo = strings.TrimSpace(repo)
		if host == "" || !domain.IsDockerPathComponent(repo) {
			continue
		}
		m[host] = repo
	}
	return &SubdomainRewriter{next: next, baseDomain: strings.ToLower(baseDomain), aliases: m}
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
		// The handlers build Location and Link from the path they received.
		// The client is still on the subdomain host and sends them back
		// through here, so the repository would be injected again on every
		// hop — a push stored its blobs under <repo>/<image> (#607).
		lw := &locationUnrewriter{ResponseWriter: w, prefix: "/v2/" + repoName + "/"}
		defer lw.fix()
		s.next.ServeHTTP(lw, r)
		return
	}
	s.next.ServeHTTP(w, r)
}

// locationUnrewriter maps the response URLs of a rewritten request back to
// the client's view: a Location or Link target that starts with the injected
// "/v2/<repo>/" gets exactly that prefix replaced by "/v2/", so a client that
// deliberately wrote /v2/<repo>/<image> keeps that image name. Absolute URLs
// are left alone. The headers are fixed before the first byte goes out.
type locationUnrewriter struct {
	http.ResponseWriter
	prefix string
	done   bool
}

func (w *locationUnrewriter) fix() {
	if w.done {
		return
	}
	w.done = true
	h := w.Header()
	if loc := h.Get("Location"); strings.HasPrefix(loc, w.prefix) {
		h.Set("Location", "/v2/"+strings.TrimPrefix(loc, w.prefix))
	}
	if links := h.Values("Link"); len(links) > 0 {
		out := make([]string, len(links))
		for i, l := range links {
			if strings.HasPrefix(l, "<"+w.prefix) {
				l = "</v2/" + strings.TrimPrefix(l, "<"+w.prefix)
			}
			out[i] = l
		}
		h["Link"] = out
	}
}

func (w *locationUnrewriter) WriteHeader(code int) {
	w.fix()
	w.ResponseWriter.WriteHeader(code)
}

func (w *locationUnrewriter) Write(b []byte) (int, error) {
	w.fix()
	return w.ResponseWriter.Write(b)
}

// Flush keeps streaming responses working through the wrapper.
func (w *locationUnrewriter) Flush() {
	w.fix()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (w *locationUnrewriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// extractRepo returns the repository name for the request's Host: an explicit
// alias when one is configured for the full hostname, else the subdomain when
// Host matches "*.<baseDomain>". Returns "" when neither applies (passthrough).
func (s *SubdomainRewriter) extractRepo(host string) string {
	// Strip port if present.
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.ToLower(host)
	if repo, ok := s.aliases[host]; ok {
		return repo
	}
	if s.baseDomain == "" {
		return ""
	}
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
