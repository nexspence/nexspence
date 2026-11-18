package raw

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"
)

// hrefRe matches href attribute values in an HTML document.
var hrefRe = regexp.MustCompile(`href="([^"]*)"`)

// ListingRewrite re-roots the links of a proxied upstream directory index at
// this repository.
//
// Upstream indexes link their files with absolute paths rooted at the
// upstream's own base path — nodejs.org/dist links its tarballs as
// "/dist/node-v20.0.0.tar.gz". Served verbatim, such a link escapes
// "/repository/<repo>/" and resolves against the local origin: the
// repository segment is lost and the upstream prefix leaks, so the link
// 404s and nothing can be downloaded. Each absolute link under the
// upstream's base path is stripped of that base path and re-rooted at
// LocalBase.
//
// Links that already resolve through the proxy are left alone: a relative
// link ("latest-argon/") is resolved by the browser against the current
// URL, which is the repository's own path at the same depth as the
// upstream's. A link outside the upstream's base path is left alone too —
// it names something the mirror does not carry.
//
// Bodies that are not HTML are returned unchanged, and a malformed link is
// left as it was: serving the upstream's bytes is strictly better than
// serving nothing.
type ListingRewrite struct {
	// LocalBase is this repository's base URL, e.g.
	// "http://host:8081/repository/myraw-proxy".
	LocalBase string
	// UpstreamHost is the host of the upstream's remote URL.
	UpstreamHost string
	// UpstreamBasePath is the path of the upstream's remote URL, e.g.
	// "/dist". It is empty when the upstream is rooted at its origin.
	UpstreamBasePath string
	// RequestPath is the repository-relative path of the document being
	// served. It is what tells the repository root from a subdirectory.
	RequestPath string
}

// Apply rewrites the links of an HTML body.
func (r ListingRewrite) Apply(body []byte) []byte {
	return hrefRe.ReplaceAllFunc(body, func(m []byte) []byte {
		href := string(m[len(`href="`) : len(m)-1])
		rewritten, ok := r.rewriteHref(href)
		if !ok {
			return m
		}
		return []byte(`href="` + rewritten + `"`)
	})
}

// rewriteHref returns the local URL for one link, and whether it had one.
func (r ListingRewrite) rewriteHref(href string) (string, bool) {
	switch {
	case strings.HasPrefix(href, "http://"), strings.HasPrefix(href, "https://"):
		u, err := url.Parse(href)
		if err != nil || !strings.EqualFold(u.Host, r.UpstreamHost) {
			return "", false
		}
		tail, ok := r.stripBasePath(u.Path)
		if !ok {
			return "", false
		}
		return r.LocalBase + tail, true

	case strings.HasPrefix(href, "/"):
		tail, ok := r.stripBasePath(href)
		if !ok {
			return "", false
		}
		return r.LocalBase + tail, true

	case href == "../" || href == "..":
		// The upstream's "up" link at the root of the mirror points above
		// the upstream's base path. The repository root is its local
		// equivalent; deeper down, the link already resolves through the
		// proxy and is left alone.
		if r.RequestPath == "" || r.RequestPath == "/" {
			return r.LocalBase + "/", true
		}
		return "", false

	default:
		return "", false // relative — already resolves through the proxy
	}
}

// stripBasePath removes the upstream's base path from a path, leaving the
// repository-relative path. A path outside the base path is not the
// mirror's and is left alone.
func (r ListingRewrite) stripBasePath(p string) (string, bool) {
	base := strings.TrimSuffix(r.UpstreamBasePath, "/")
	if base == "" {
		return p, true
	}
	if p == base {
		return "/", true
	}
	if strings.HasPrefix(p, base+"/") {
		return p[len(base):], true
	}
	return "", false
}

// IsHTMLBody reports whether a body looks like an HTML document. The proxy
// rewrites links in HTML only; every other body is served as it came, so a
// binary that happens to contain something that looks like a link is not
// corrupted by the rewrite.
func IsHTMLBody(body []byte) bool {
	trimmed := bytes.TrimLeft(body, " \t\r\n\uFEFF")
	return bytes.HasPrefix(trimmed, []byte("<"))
}
