package gomod

// Group index merging (#99 phase 2): @v/list and @latest are merged across
// group members — @v/list answers 200 with an empty body when a member has
// no versions, which under first-non-404 fan-out shadowed later members.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
)

// GroupIndexSourcePath implements formats.GroupIndexMerger.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if strings.HasSuffix(p, "/@v/list") || strings.HasSuffix(p, "/@latest") {
		return p, true
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(_, p string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	if strings.HasSuffix(p, "/@latest") {
		return mergeLatest(parts)
	}
	// @v/list: newline-separated versions — union, dedup, member order.
	var out []string
	seen := map[string]bool{}
	for _, part := range parts {
		for _, v := range strings.Split(strings.TrimSpace(string(part.Body)), "\n") {
			v = strings.TrimSpace(v)
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}
	body := strings.Join(out, "\n")
	if body != "" {
		body += "\n"
	}
	return []byte(body), "text/plain; charset=utf-8", nil
}

// mergeLatest picks the highest Version across members' @latest documents.
func mergeLatest(parts []formats.GroupIndexPart) ([]byte, string, error) {
	var best []byte
	bestVer := ""
	for _, part := range parts {
		var doc struct {
			Version string `json:"Version"`
		}
		if err := json.Unmarshal(part.Body, &doc); err != nil || doc.Version == "" {
			continue // malformed member copy — merge the rest
		}
		if best == nil || base.CompareLooseVersions(doc.Version, bestVer) > 0 {
			best, bestVer = part.Body, doc.Version
		}
	}
	if best == nil {
		return nil, "", fmt.Errorf("gomod group merge: no parsable @latest among %d members", len(parts))
	}
	return best, "application/json", nil
}
