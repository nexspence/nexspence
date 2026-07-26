package pypi

import (
	"regexp"
	"strings"
)

// hrefRe matches href attribute values on PyPI simple pages (PEP 503 pages
// are machine-generated anchor lists, so attribute-level matching is safe).
var hrefRe = regexp.MustCompile(`href="([^"]+)"`)

// RewriteSimplePage rewrites absolute package-file links on a proxied PyPI
// simple page to point at this proxy (#98). Upstream pages embed absolute
// URLs (e.g. https://files.pythonhosted.org/packages/...), which make pip
// download release files directly from upstream, bypassing the proxy cache.
// Every absolute href containing "/packages/" is re-rooted at
// localBase+"/packages/<tail>", preserving the path tail and any #sha256=
// fragment. Relative hrefs and URLs without a /packages/ segment are left
// untouched. Malformed input is returned unchanged by construction (regex
// replacement only touches matching attributes).
func RewriteSimplePage(body []byte, localBase string) []byte {
	return hrefRe.ReplaceAllFunc(body, func(m []byte) []byte {
		href := string(m[len(`href="`) : len(m)-1])
		if !strings.HasPrefix(href, "http://") && !strings.HasPrefix(href, "https://") {
			return m
		}
		idx := strings.Index(href, "/packages/")
		if idx < 0 {
			return m
		}
		return []byte(`href="` + localBase + href[idx:] + `"`)
	})
}
