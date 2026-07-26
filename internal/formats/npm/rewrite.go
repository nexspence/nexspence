package npm

import (
	"encoding/json"
	"net/url"
)

// RewritePackument rewrites every versions.*.dist.tarball URL in an npm
// packument to point at this proxy (#98). Upstream registries embed absolute
// URLs to themselves; served verbatim they make npm download tarballs directly
// from upstream, bypassing the proxy cache. localBase is the proxy repo base
// (e.g. "http://host/repository/npm-proxy"); the tarball's upstream path
// ("/<pkg>/-/<file>.tgz", scoped packages included) is preserved.
//
// Malformed bodies are returned unchanged — serving upstream bytes verbatim
// is strictly better than serving nothing.
func RewritePackument(body []byte, localBase string) []byte {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body
	}

	versions, ok := doc["versions"].(map[string]any)
	if !ok {
		return body
	}
	changed := false
	for _, v := range versions {
		ver, ok := v.(map[string]any)
		if !ok {
			continue
		}
		dist, ok := ver["dist"].(map[string]any)
		if !ok {
			continue
		}
		tb, ok := dist["tarball"].(string)
		if !ok || tb == "" {
			continue
		}
		u, err := url.Parse(tb)
		if err != nil || u.Path == "" {
			continue
		}
		dist["tarball"] = localBase + u.Path
		changed = true
	}
	if !changed {
		return body
	}

	out, err := json.Marshal(doc)
	if err != nil {
		return body
	}
	return out
}
