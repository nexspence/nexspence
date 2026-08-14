package yum

// Group index merging (#99 phase 2): repodata is merged across group members
// — a single member's primary.xml hid the others' rpms from dnf.
//
// #222 extends that to the rest of the repodata set. filelists.xml and
// other.xml are aggregated indexes just like primary, so a group serving one
// member's copy hides every other member's packages from `dnf provides` and
// changelog queries. And repomd.xml cannot be relayed at all: it carries the
// checksum of each of those documents, computed by that member over its own
// single-member copy, while the group serves the union — a mismatch dnf treats
// as a hard sync failure. The group builds its own repomd over the merged
// documents it serves, which it asks the group layer for (see
// formats.GroupIndexDependentMerger) so proxy members, whose metadata comes
// from upstream, are covered the same way.

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/nexspence-oss/nexspence/internal/formats"
)

// repodataDocTypes are the documents repomd.xml vouches for, in the order dnf
// reads them.
var repodataDocTypes = []string{"primary", "filelists", "other"}

// GroupIndexSourcePath implements formats.GroupIndexMerger. The .gz flavor
// of each document fans out on the PLAIN one — the merger gzips the result.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	if p == "/repodata/repomd.xml" {
		return p, true
	}
	for _, typ := range repodataDocTypes {
		plain := "/repodata/" + typ + ".xml"
		if p == plain || p == plain+".gz" {
			return plain, true
		}
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(groupName, p string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	return h.MergeGroupIndexWithFetch(groupName, p, parts, nil)
}

// MergeGroupIndexWithFetch implements formats.GroupIndexDependentMerger.
func (h *Handler) MergeGroupIndexWithFetch(_, p string, parts []formats.GroupIndexPart,
	fetch formats.GroupIndexFetcher,
) ([]byte, string, error) {
	if p == "/repodata/repomd.xml" {
		return mergeRepomd(fetch)
	}

	doc, err := mergeRepodataDoc(p, parts)
	if err != nil {
		return nil, "", err
	}
	if strings.HasSuffix(p, ".gz") {
		return gzipBytes(doc), "application/x-gzip", nil
	}
	return doc, "application/xml; charset=utf-8", nil
}

// mergeRepomd builds the group's own repomd.xml over the merged documents the
// group serves, so every checksum describes bytes a client can actually
// download from it.
func mergeRepomd(fetch formats.GroupIndexFetcher) ([]byte, string, error) {
	if fetch == nil {
		return nil, "", fmt.Errorf("yum group merge: repomd.xml describes the group's own repodata, which was not available")
	}

	now := time.Now().Unix()
	var entries []repomdEntry
	for _, typ := range repodataDocTypes {
		plain, err := fetch("/repodata/" + typ + ".xml")
		if err != nil {
			continue // a document no member serves is one the group cannot vouch for
		}
		gz, err := fetch("/repodata/" + typ + ".xml.gz")
		if err != nil {
			continue
		}
		entries = append(entries, repomdEntryFor(typ, gz, plain, now))
	}
	if len(entries) == 0 {
		return nil, "", fmt.Errorf("yum group merge: no repodata document could be merged")
	}
	return renderRepomd(now, entries), "application/xml; charset=utf-8", nil
}

// mergeRepodataDoc unions one document type across members. Packages are
// deduped by identity — the location href for primary, the pkgid for the file
// and changelog lists — with the first member winning, the same priority the
// rest of the group fan-out uses.
func mergeRepodataDoc(p string, parts []formats.GroupIndexPart) ([]byte, error) {
	switch {
	case strings.HasPrefix(p, "/repodata/primary.xml"):
		merged := primaryXML{XMLNS: "http://linux.duke.edu/metadata/common"}
		parsed := false
		seen := map[string]bool{}
		for _, part := range parts {
			var doc primaryXML
			if err := xml.Unmarshal(part.Body, &doc); err != nil {
				continue // malformed member copy — merge the rest
			}
			parsed = true
			for _, pkg := range doc.Packages {
				if firstWins(seen, pkg.Location.Href) {
					merged.Packages = append(merged.Packages, pkg)
				}
			}
		}
		if !parsed {
			return nil, fmt.Errorf("yum group merge: no parsable primary.xml among %d members", len(parts))
		}
		merged.Count = len(merged.Packages)
		return marshalXML(merged), nil

	case strings.HasPrefix(p, "/repodata/filelists.xml"):
		merged := filelistsXML{XMLNS: "http://linux.duke.edu/metadata/filelists"}
		entries, err := mergeFileListEntries(p, parts, func(part []byte) ([]fileListEntry, error) {
			var doc filelistsXML
			err := xml.Unmarshal(part, &doc)
			return doc.Packages, err
		})
		if err != nil {
			return nil, err
		}
		merged.Packages = entries
		merged.Count = len(entries)
		return marshalXML(merged), nil

	case strings.HasPrefix(p, "/repodata/other.xml"):
		merged := otherXML{XMLNS: "http://linux.duke.edu/metadata/other"}
		entries, err := mergeFileListEntries(p, parts, func(part []byte) ([]fileListEntry, error) {
			var doc otherXML
			err := xml.Unmarshal(part, &doc)
			return doc.Packages, err
		})
		if err != nil {
			return nil, err
		}
		merged.Packages = entries
		merged.Count = len(entries)
		return marshalXML(merged), nil
	}
	return nil, fmt.Errorf("yum group merge: %q is not a mergeable repodata document", p)
}

func mergeFileListEntries(p string, parts []formats.GroupIndexPart,
	unmarshal func([]byte) ([]fileListEntry, error),
) ([]fileListEntry, error) {
	var out []fileListEntry
	seen := map[string]bool{}
	parsed := false
	for _, part := range parts {
		packages, err := unmarshal(part.Body)
		if err != nil {
			continue // malformed member copy — merge the rest
		}
		parsed = true
		for _, entry := range packages {
			key := entry.PkgID
			if key == "" {
				key = entry.Name + "-" + entry.Version.Ver + "-" + entry.Version.Rel + "." + entry.Arch
			}
			if firstWins(seen, key) {
				out = append(out, entry)
			}
		}
	}
	if !parsed {
		return nil, fmt.Errorf("yum group merge: no parsable %s among %d members", strings.TrimPrefix(p, "/repodata/"), len(parts))
	}
	return out, nil
}

// firstWins reports whether key is new, recording it when it is. An entry
// without an identity is always kept: dropping every one of them would lose
// real packages to a metadata gap.
func firstWins(seen map[string]bool, key string) bool {
	if key == "" {
		return true
	}
	if seen[key] {
		return false
	}
	seen[key] = true
	return true
}
