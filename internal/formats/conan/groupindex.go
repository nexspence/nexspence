package conan

// Group index merging (#99): the repository-wide recipe search
// (/v2/conans/search) is merged across group members.
//
// A hosted Conan member answers that search with 200 and an empty result list
// — it has no recipe of its own to report — and under the group's
// first-non-404 fan-out that empty answer shadowed every member behind it:
// `conan search` against the group showed the hosted member's packages only,
// while the group's resolution of a named recipe did reach the proxy member.
// Merging turns the empty list into what it is — one member's empty
// contribution — and serves the union instead.
//
// The package-level search (/v2/conans/<ref>/search) is deliberately NOT
// merged: it describes the binaries of ONE recipe, and each member answers for
// its own revision of it, so a union would mix package IDs of revisions the
// group does not serve. It keeps the first-non-404 fan-out, which already
// reaches the member holding the recipe — a member without it answers 404.

import (
	"encoding/json"
	"fmt"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// searchPath is the repository-wide recipe search — the one aggregated index
// of the Conan protocol.
const searchPath = "/v2/conans/search"

// GroupIndexSourcePath implements formats.GroupIndexMerger. Only the
// repository-wide search is merged; the package-level search and every other
// path keep the first-non-404 fan-out.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if normPath(p) == searchPath {
		return searchPath, true
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger: the union of the
// members' result lists, first member wins on a recipe several members hold.
//
// A member whose body cannot be parsed is skipped — the rest still merge —
// and only a body no member could be parsed into is a failure.
func (h *Handler) MergeGroupIndex(_, _ string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	type searchDoc struct {
		Results []string `json:"results"`
	}
	merged := searchDoc{Results: []string{}}
	seen := map[string]struct{}{}
	parsed := false
	for _, part := range parts {
		var doc searchDoc
		if err := json.Unmarshal(part.Body, &doc); err != nil {
			continue // malformed member copy — merge the rest
		}
		parsed = true
		for _, ref := range doc.Results {
			if _, dup := seen[ref]; dup {
				continue // first member wins per recipe
			}
			seen[ref] = struct{}{}
			merged.Results = append(merged.Results, ref)
		}
	}
	if !parsed {
		return nil, "", fmt.Errorf("conan group merge: no parsable search result among %d members", len(parts))
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return nil, "", err
	}
	return out, "application/json", nil
}
