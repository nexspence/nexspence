package nuget

import (
	"encoding/json"
	"net/url"
	"strings"
)

// RewriteRegistration rewrites absolute upstream URLs in a proxied NuGet v3
// registration document to point at this proxy (#98):
//
//   - "packageContent" is REBUILT from the sibling catalogEntry's id/version
//     (lowercased id, our flatcontainer layout) — upstream flatcontainer path
//     shapes vary (api.nuget.org uses /v3-flatcontainer/), so string-munging
//     the upstream URL would be fragile.
//   - links into a registration hive — "@id", and a page's "parent" and a
//     leaf's "registration" — whose URL path has a segment starting with
//     "registration" are re-rooted on localBase keeping the upstream path,
//     hive included (#678). The proxy forwards a request path onto the
//     upstream origin as is, the way the rewritten service index re-roots
//     every resource, so a link stays followable only in the hive it came
//     from: nuget.org serves /v3/registration5-gz-semver2/ and has no
//     /v3/registration/ to collapse it into.
//
// localBase is the proxy repo base (e.g. "http://host/repository/nuget-proxy").
// Malformed bodies are returned unchanged.
func RewriteRegistration(body []byte, localBase string) []byte {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body
	}
	rewriteRegistrationNode(doc, localBase)
	out, err := json.Marshal(doc)
	if err != nil {
		return body
	}
	return out
}

// registrationLinkKeys are the registration document fields that link into a
// registration hive.
var registrationLinkKeys = []string{"@id", "parent", "registration"}

// rewriteRegistrationNode walks the registration JSON tree in place.
func rewriteRegistrationNode(node any, localBase string) {
	switch n := node.(type) {
	case map[string]any:
		for _, key := range registrationLinkKeys {
			if link, ok := n[key].(string); ok {
				if rw, ok2 := rerootRegistrationID(link, localBase); ok2 {
					n[key] = rw
				}
			}
		}
		if _, ok := n["packageContent"].(string); ok {
			if ce, ok2 := n["catalogEntry"].(map[string]any); ok2 {
				id, _ := ce["id"].(string)
				ver, _ := ce["version"].(string)
				if id != "" && ver != "" {
					lid := strings.ToLower(id)
					n["packageContent"] = localBase + "/v3/flatcontainer/" + lid + "/" + ver + "/" + lid + "." + ver + ".nupkg"
				}
			}
		}
		for _, v := range n {
			rewriteRegistrationNode(v, localBase)
		}
	case []any:
		for _, v := range n {
			rewriteRegistrationNode(v, localBase)
		}
	}
}

// rerootRegistrationID maps an upstream registration URL (any host, any
// registration* hive, e.g. /v3/registration5-gz-semver2/<id>/...) onto this
// proxy at the same path: localBase + upstream path (+ query, fragment). A
// URL outside a registration hive (registrationHiveSegment) is left alone.
func rerootRegistrationID(rawURL, localBase string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || !u.IsAbs() {
		return "", false
	}
	if registrationHiveSegment(u.Path) < 0 {
		return "", false
	}
	local := &url.URL{Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery, Fragment: u.Fragment, RawFragment: u.RawFragment}
	return strings.TrimRight(localBase, "/") + local.String(), true
}
