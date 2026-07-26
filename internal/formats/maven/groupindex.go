package maven

// Group index merging (#99): maven-metadata.xml is merged across group
// members — a single member's copy hides versions held by the others and
// breaks LATEST/RELEASE/version-range resolution. Checksum sidecars are
// computed over the MERGED document via GroupIndexSourcePath.

import (
	"crypto/md5"  //nolint:gosec // maven protocol checksum, not security
	"crypto/sha1" //nolint:gosec // maven protocol checksum, not security
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"path"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
)

const metadataFile = "maven-metadata.xml"

// mavenMetadata mirrors the maven-metadata.xml structure we merge.
type mavenMetadata struct {
	XMLName    xml.Name `xml:"metadata"`
	GroupID    string   `xml:"groupId"`
	ArtifactID string   `xml:"artifactId"`
	Versioning struct {
		Latest      string   `xml:"latest,omitempty"`
		Release     string   `xml:"release,omitempty"`
		Versions    []string `xml:"versions>version"`
		LastUpdated string   `xml:"lastUpdated,omitempty"`
	} `xml:"versioning"`
}

// GroupIndexSourcePath implements formats.GroupIndexMerger.
func (h *Handler) GroupIndexSourcePath(p string) (string, bool) {
	base := path.Base(p)
	if base == metadataFile {
		return p, true
	}
	for _, ext := range []string{".sha1", ".md5", ".sha256"} {
		if base == metadataFile+ext {
			return strings.TrimSuffix(p, ext), true
		}
	}
	return "", false
}

// MergeGroupIndex implements formats.GroupIndexMerger.
func (h *Handler) MergeGroupIndex(_, p string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	merged := mavenMetadata{}
	seen := map[string]bool{}
	for _, part := range parts {
		var m mavenMetadata
		if err := xml.Unmarshal(part.Body, &m); err != nil {
			continue // malformed member copy — merge the rest
		}
		if merged.GroupID == "" {
			merged.GroupID, merged.ArtifactID = m.GroupID, m.ArtifactID
		}
		for _, v := range m.Versioning.Versions {
			if !seen[v] {
				seen[v] = true
				merged.Versioning.Versions = append(merged.Versioning.Versions, v)
			}
		}
		if m.Versioning.LastUpdated > merged.Versioning.LastUpdated {
			merged.Versioning.LastUpdated = m.Versioning.LastUpdated
		}
	}
	if len(merged.Versioning.Versions) == 0 && merged.GroupID == "" {
		return nil, "", fmt.Errorf("maven group merge: no parsable maven-metadata.xml among %d members", len(parts))
	}

	// latest/release are recomputed over the union — taking any single
	// member's value would hide newer versions held by other members.
	for _, v := range merged.Versioning.Versions {
		if base.CompareLooseVersions(v, merged.Versioning.Latest) > 0 {
			merged.Versioning.Latest = v
		}
		if !strings.Contains(strings.ToUpper(v), "SNAPSHOT") &&
			base.CompareLooseVersions(v, merged.Versioning.Release) > 0 {
			merged.Versioning.Release = v
		}
	}

	out, err := xml.Marshal(merged)
	if err != nil {
		return nil, "", err
	}
	doc := append([]byte(xml.Header), out...)

	switch path.Ext(path.Base(p)) {
	case ".sha1":
		return []byte(fmt.Sprintf("%x", sha1.Sum(doc))), "text/plain", nil //nolint:gosec
	case ".md5":
		return []byte(fmt.Sprintf("%x", md5.Sum(doc))), "text/plain", nil //nolint:gosec
	case ".sha256":
		return []byte(fmt.Sprintf("%x", sha256.Sum256(doc))), "text/plain", nil
	}
	return doc, "application/xml", nil
}
