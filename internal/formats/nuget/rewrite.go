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
//   - "@id" values whose URL path contains a segment starting with
//     "registration" are re-rooted at localBase+"/v3/registration/"+tail.
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

// rewriteRegistrationNode walks the registration JSON tree in place.
func rewriteRegistrationNode(node any, localBase string) {
	switch n := node.(type) {
	case map[string]any:
		if id, ok := n["@id"].(string); ok {
			if rw, ok2 := rerootRegistrationID(id, localBase); ok2 {
				n["@id"] = rw
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
// registration* segment flavor, e.g. /v3/registration5-gz-semver2/<id>/...)
// onto this proxy's /v3/registration/<tail>.
func rerootRegistrationID(rawURL, localBase string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || !u.IsAbs() {
		return "", false
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, s := range segs {
		if strings.HasPrefix(s, "registration") && i+1 < len(segs) {
			return localBase + "/v3/registration/" + strings.Join(segs[i+1:], "/"), true
		}
	}
	return "", false
}
