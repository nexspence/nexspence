package yum

// Group index merging (#99 phase 2): repodata is merged across group members
// — a single member's primary.xml hid the others' rpms from dnf, and
// repomd.xml embedded a member-scoped href that steered clients off the
// group.

import (
	"bytes"
	"compress/gzip"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// GroupIndexSourcePath implements formats.GroupIndexMerger. The .gz flavor
// of primary fans out on the PLAIN document — the merger gzips the result.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	switch {
	case p == "/repodata/repomd.xml":
		return p, true
	case p == "/repodata/primary.xml" || p == "/repodata/primary.xml.gz":
		return "/repodata/primary.xml", true
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(groupName, p string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	if p == "/repodata/repomd.xml" {
		// repomd shapes are identical across members; take the first and
		// re-root its member-scoped hrefs at the group.
		body := strings.ReplaceAll(string(parts[0].Body),
			"/repository/"+parts[0].Member+"/", "/repository/"+groupName+"/")
		return []byte(body), "application/xml; charset=utf-8", nil
	}

	// primary.xml: union <package> entries across members, dedup by location
	// href (first member wins), recount the packages attribute.
	merged := primaryXML{XMLNS: "http://linux.duke.edu/metadata/common"}
	seen := map[string]bool{}
	parsed := false
	for _, part := range parts {
		var doc primaryXML
		if err := xml.Unmarshal(part.Body, &doc); err != nil {
			continue // malformed member copy — merge the rest
		}
		parsed = true
		for _, pkg := range doc.Packages {
			key := pkg.Location.Href
			if key != "" && seen[key] {
				continue
			}
			if key != "" {
				seen[key] = true
			}
			merged.Packages = append(merged.Packages, pkg)
		}
	}
	if !parsed {
		return nil, "", fmt.Errorf("yum group merge: no parsable primary.xml among %d members", len(parts))
	}
	merged.Count = len(merged.Packages)

	out, err := xml.Marshal(merged)
	if err != nil {
		return nil, "", err
	}
	doc := append([]byte(xml.Header), out...)

	if strings.HasSuffix(p, ".gz") {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		_, _ = gw.Write(doc)
		_ = gw.Close()
		return buf.Bytes(), "application/x-gzip", nil
	}
	return doc, "application/xml; charset=utf-8", nil
}
