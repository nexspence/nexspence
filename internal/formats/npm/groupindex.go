package npm

// Group index merging (#99): the packument is merged across group members —
// a single member's copy hides versions held by the others. Member order is
// the priority contract: first member wins per version key and per dist-tag.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// GroupIndexSourcePath implements formats.GroupIndexMerger. Any GET path that
// is not the repo root, a tarball, or a /-/ API path is a packument request.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if p == "/" || p == "" || strings.Contains(p, "/-/") || strings.HasPrefix(p, "/-") {
		return "", false
	}
	return p, true
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(groupName, _ string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	groupBase := strings.TrimRight(h.deps.BaseURL, "/") + "/repository/" + groupName

	merged := map[string]any{}
	versions := map[string]any{}
	distTags := map[string]any{}
	times := map[string]any{}

	for _, part := range parts {
		var doc map[string]any
		if err := json.Unmarshal(part.Body, &doc); err != nil {
			continue // malformed member copy — merge the rest
		}
		// Scalar fields (name, description, …): first member wins.
		for k, v := range doc {
			switch k {
			case "versions", "dist-tags", "time":
			default:
				if _, seen := merged[k]; !seen {
					merged[k] = v
				}
			}
		}
		if vs, ok := doc["versions"].(map[string]any); ok {
			for ver, vdoc := range vs {
				if _, seen := versions[ver]; seen {
					continue // first member wins per version
				}
				rewriteVersionTarball(vdoc, part.Member, groupBase)
				versions[ver] = vdoc
			}
		}
		if tags, ok := doc["dist-tags"].(map[string]any); ok {
			for tag, v := range tags {
				if _, seen := distTags[tag]; !seen {
					distTags[tag] = v // first member wins per tag
				}
			}
		}
		if tm, ok := doc["time"].(map[string]any); ok {
			for k, v := range tm {
				if _, seen := times[k]; !seen {
					times[k] = v
				}
			}
		}
	}

	if len(versions) == 0 && len(merged) == 0 {
		return nil, "", fmt.Errorf("npm group merge: no parsable packument among %d members", len(parts))
	}

	merged["versions"] = versions
	if len(distTags) > 0 {
		merged["dist-tags"] = distTags
	}
	if len(times) > 0 {
		merged["time"] = times
	}

	out, err := json.Marshal(merged)
	if err != nil {
		return nil, "", err
	}
	return out, "application/json", nil
}

// rewriteVersionTarball re-roots a version's dist.tarball from the member
// repo at the group: the member handler minted a /repository/<member>/ URL,
// which would steer the client off the group (and its RBAC).
func rewriteVersionTarball(vdoc any, member, groupBase string) {
	ver, ok := vdoc.(map[string]any)
	if !ok {
		return
	}
	dist, ok := ver["dist"].(map[string]any)
	if !ok {
		return
	}
	tb, ok := dist["tarball"].(string)
	if !ok || tb == "" {
		return
	}
	u, err := url.Parse(tb)
	if err != nil {
		return
	}
	memberPrefix := "/repository/" + member
	if tail, found := strings.CutPrefix(u.Path, memberPrefix); found {
		dist["tarball"] = groupBase + tail
	}
}
