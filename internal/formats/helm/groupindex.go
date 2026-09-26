package helm

// Group index merging (#99): Helm's index.yaml is the catalog the client
// searches. Hosted members always answer 200 (even with empty entries), so
// first-non-404 fan-out hid every proxy behind the first hosted repo. Merging
// unions chart entries across members; first member wins per name+version.
// Download URLs minted at /repository/<member>/ are re-rooted at the group
// so helm pull stays on the group URL. Off-host GitHub release URLs are
// rewritten onto the member by the proxy (then re-rooted here) so the tarball
// is fetched through Nexspence and cached.

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

var _ formats.GroupIndexMerger = (*Handler)(nil)

// GroupIndexSourcePath implements formats.GroupIndexMerger.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if normPath(p) == "/index.yaml" {
		return "/index.yaml", true
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(groupName, _ string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	merged := map[string][]any{}
	seen := map[string]bool{}
	apiVersion := ""
	parsed := false

	for _, part := range parts {
		var doc map[string]any
		if err := yaml.Unmarshal(part.Body, &doc); err != nil {
			continue
		}
		entries, _ := doc["entries"].(map[string]any)
		if entries == nil && doc["entries"] != nil {
			continue
		}
		parsed = true
		if apiVersion == "" {
			if v, ok := doc["apiVersion"].(string); ok {
				apiVersion = v
			}
		}
		for chartName, raw := range entries {
			charts, ok := raw.([]any)
			if !ok {
				continue
			}
			for _, cv := range charts {
				chart, ok := cv.(map[string]any)
				if !ok {
					continue
				}
				name := chartName
				if n := asString(chart["name"]); n != "" {
					name = n
				}
				ver := asString(chart["version"])
				key := name + "@" + ver
				if seen[key] {
					continue
				}
				seen[key] = true
				rewriteChartURLsToGroup(chart, part.Member, groupName)
				merged[name] = append(merged[name], chart)
			}
		}
	}

	if !parsed {
		return nil, "", fmt.Errorf("helm group merge: no parsable index.yaml among %d members", len(parts))
	}
	if apiVersion == "" {
		apiVersion = "v1"
	}

	out, err := yaml.Marshal(map[string]any{
		"apiVersion": apiVersion,
		"entries":    merged,
		"generated":  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, "", err
	}
	return out, "application/yaml", nil
}

// rewriteChartURLsToGroup re-roots urls that the member handler minted under
// /repository/<member>/ so the client keeps talking to the group.
func rewriteChartURLsToGroup(chart map[string]any, member, groupName string) {
	urls, ok := chart["urls"].([]any)
	if !ok {
		return
	}
	from := "/repository/" + member + "/"
	to := "/repository/" + groupName + "/"
	for i, u := range urls {
		s, ok := u.(string)
		if !ok || s == "" {
			continue
		}
		if strings.Contains(s, from) {
			urls[i] = strings.Replace(s, from, to, 1)
		}
	}
	chart["urls"] = urls
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
