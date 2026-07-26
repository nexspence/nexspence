package conda

// Group index merging (#99 phase 2): repodata.json is merged across group
// members — a single member's index hides packages held by the others, so
// `conda install` could not resolve them even though the package files
// themselves are reachable through fan-out.

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// GroupIndexSourcePath implements formats.GroupIndexMerger.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if path.Base(p) == "repodata.json" && strings.Count(strings.Trim(p, "/"), "/") == 1 {
		return p, true
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(_, _ string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	type repodata struct {
		Info          map[string]any             `json:"info"`
		Packages      map[string]json.RawMessage `json:"packages"`
		PackagesConda map[string]json.RawMessage `json:"packages.conda"`
	}
	merged := repodata{
		Packages:      map[string]json.RawMessage{},
		PackagesConda: map[string]json.RawMessage{},
	}
	parsed := false
	for _, part := range parts {
		var doc repodata
		if err := json.Unmarshal(part.Body, &doc); err != nil {
			continue // malformed member copy — merge the rest
		}
		parsed = true
		if merged.Info == nil {
			merged.Info = doc.Info
		}
		for k, v := range doc.Packages {
			if _, seen := merged.Packages[k]; !seen {
				merged.Packages[k] = v // first member wins per filename
			}
		}
		for k, v := range doc.PackagesConda {
			if _, seen := merged.PackagesConda[k]; !seen {
				merged.PackagesConda[k] = v
			}
		}
	}
	if !parsed {
		return nil, "", fmt.Errorf("conda group merge: no parsable repodata.json among %d members", len(parts))
	}
	out, err := json.Marshal(merged)
	return out, "application/json", err
}
