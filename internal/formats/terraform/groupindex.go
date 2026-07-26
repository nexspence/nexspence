package terraform

// Group index merging (#99 phase 2): provider/module version lists answer
// 200-empty for members without the artifact, shadowing later members under
// first-non-404 fan-out; the discovery document embeds member-scoped URLs.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// GroupIndexSourcePath implements formats.GroupIndexMerger.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	switch {
	case strings.HasSuffix(p, "/versions") &&
		(strings.HasPrefix(p, "/v1/providers/") || strings.HasPrefix(p, "/v1/modules/")):
		return p, true
	case p == "/.well-known/terraform.json":
		return p, true
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(groupName, p string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	switch {
	case p == "/.well-known/terraform.json":
		// Discovery: first member's document with its URLs re-rooted at the
		// group — the shape is identical across members.
		body := string(parts[0].Body)
		body = strings.ReplaceAll(body, "/repository/"+parts[0].Member+"/", "/repository/"+groupName+"/")
		return []byte(body), "application/json", nil

	case strings.HasPrefix(p, "/v1/modules/"):
		return mergeModuleVersions(parts)

	default: // /v1/providers/.../versions
		return mergeProviderVersions(parts)
	}
}

func mergeProviderVersions(parts []formats.GroupIndexPart) ([]byte, string, error) {
	var out []any
	seen := map[string]bool{}
	parsed := false
	for _, part := range parts {
		var doc struct {
			Versions []map[string]any `json:"versions"`
		}
		if err := json.Unmarshal(part.Body, &doc); err != nil {
			continue // malformed member copy — merge the rest
		}
		parsed = true
		for _, v := range doc.Versions {
			ver, _ := v["version"].(string)
			if ver == "" || seen[ver] {
				continue // first member wins per version
			}
			seen[ver] = true
			out = append(out, v)
		}
	}
	if !parsed {
		return nil, "", fmt.Errorf("terraform group merge: no parsable provider versions among %d members", len(parts))
	}
	if out == nil {
		out = []any{}
	}
	body, err := json.Marshal(map[string]any{"versions": out})
	return body, "application/json", err
}

func mergeModuleVersions(parts []formats.GroupIndexPart) ([]byte, string, error) {
	var out []any
	seen := map[string]bool{}
	parsed := false
	for _, part := range parts {
		var doc struct {
			Modules []struct {
				Versions []map[string]any `json:"versions"`
			} `json:"modules"`
		}
		if err := json.Unmarshal(part.Body, &doc); err != nil {
			continue
		}
		parsed = true
		for _, m := range doc.Modules {
			for _, v := range m.Versions {
				ver, _ := v["version"].(string)
				if ver == "" || seen[ver] {
					continue
				}
				seen[ver] = true
				out = append(out, v)
			}
		}
	}
	if !parsed {
		return nil, "", fmt.Errorf("terraform group merge: no parsable module versions among %d members", len(parts))
	}
	if out == nil {
		out = []any{}
	}
	body, err := json.Marshal(map[string]any{"modules": []map[string]any{{"versions": out}}})
	return body, "application/json", err
}
