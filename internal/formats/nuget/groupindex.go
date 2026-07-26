package nuget

// Group index merging (#99 phase 2): the flatcontainer version list answers
// 200 with {"versions":[]} for unknown packages, which under first-non-404
// fan-out shadowed every member behind the first; the service index embeds
// member-scoped URLs. Registration is NOT merged this phase — it 404s on
// miss, so plain fan-out reaches the right member.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// GroupIndexSourcePath implements formats.GroupIndexMerger.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if p == "/index.json" {
		return p, true
	}
	if strings.HasPrefix(p, "/v3/flatcontainer/") && strings.HasSuffix(p, "/index.json") {
		return p, true
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(groupName, p string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	if p == "/index.json" {
		// Service index: first member's document re-rooted at the group —
		// the resource shapes are identical across members.
		body := strings.ReplaceAll(string(parts[0].Body),
			"/repository/"+parts[0].Member+"/", "/repository/"+groupName+"/")
		return []byte(body), "application/json", nil
	}

	// Flatcontainer version list: union across members, member order.
	var out []string
	seen := map[string]bool{}
	parsed := false
	for _, part := range parts {
		var doc struct {
			Versions []string `json:"versions"`
		}
		if err := json.Unmarshal(part.Body, &doc); err != nil {
			continue // malformed member copy — merge the rest
		}
		parsed = true
		for _, v := range doc.Versions {
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	if !parsed {
		return nil, "", fmt.Errorf("nuget group merge: no parsable version list among %d members", len(parts))
	}
	if out == nil {
		out = []string{}
	}
	body, err := json.Marshal(map[string]any{"versions": out})
	return body, "application/json", err
}
