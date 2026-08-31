package maven

// Dynamic maven-metadata.xml generation (#350). Nexus never attaches either
// metadata shape to a component, so migrations cannot carry them over — and
// nexspence's own schema requires every asset to belong to a component, so a
// literal copy could not even be stored. Instead both shapes are generated on
// demand from the repository's real stored assets, the same way the npm
// handler computes packuments from stored components. A generated document can
// never go stale: it always reflects exactly what the repository holds.

import (
	"crypto/md5"  //nolint:gosec // maven protocol checksum, not security
	"crypto/sha1" //nolint:gosec // maven protocol checksum, not security
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
)

// perVersionMetadata is the per-SNAPSHOT-version maven-metadata.xml shape
// (modelVersion 1.1.0): the single latest timestamped build plus one
// snapshotVersion entry per extension/classifier seen on that build.
type perVersionMetadata struct {
	XMLName      xml.Name `xml:"metadata"`
	ModelVersion string   `xml:"modelVersion,attr"`
	GroupID      string   `xml:"groupId"`
	ArtifactID   string   `xml:"artifactId"`
	Version      string   `xml:"version"`
	Versioning   struct {
		Snapshot struct {
			Timestamp   string `xml:"timestamp"`
			BuildNumber int    `xml:"buildNumber"`
		} `xml:"snapshot"`
		LastUpdated      string            `xml:"lastUpdated,omitempty"`
		SnapshotVersions []snapshotVersion `xml:"snapshotVersions>snapshotVersion"`
	} `xml:"versioning"`
}

type snapshotVersion struct {
	Classifier string `xml:"classifier,omitempty"`
	Extension  string `xml:"extension"`
	Value      string `xml:"value"`
	Updated    string `xml:"updated,omitempty"`
}

// snapshotFileRe matches the unique-snapshot remainder of a file name after
// "<artifact>-<baseVersion>-" is cut: "<timestamp>-<buildNumber>[-<classifier>].<ext>".
var snapshotFileRe = regexp.MustCompile(`^(\d{8}\.\d{6})-(\d+)(?:-([^.]+))?\.(.+)$`)

// isSnapshotDir reports whether the last path segment names a SNAPSHOT version
// directory — the discriminator between the two metadata shapes.
func isSnapshotDir(dir string) bool {
	return strings.HasSuffix(path.Base(dir), "-SNAPSHOT")
}

// generateMetadata builds the maven-metadata.xml for the given metadata path
// from the repository's stored assets. Returns nil when the directory holds
// nothing to describe (the caller then 404s).
func (h *Handler) generateMetadata(c *gin.Context, repoName, metadataPath string) []byte {
	dir := path.Dir(metadataPath)
	if dir == "/" || dir == "." {
		return nil
	}
	assets, err := h.deps.Assets.ListByRepoAndPath(c.Request.Context(), repoName, dir+"/")
	if err != nil || len(assets) == 0 {
		return nil
	}
	if isSnapshotDir(dir) {
		return generatePerVersionMetadata(dir, assets)
	}
	return generateAggregateMetadata(dir, assets)
}

func splitGroupArtifact(segments []string) (groupID, artifactID string) {
	if len(segments) < 2 {
		return "", strings.Join(segments, ".")
	}
	return strings.Join(segments[:len(segments)-1], "."), segments[len(segments)-1]
}

// generateAggregateMetadata lists the artifact's real versions: the immediate
// child directories of the artifact directory that actually contain files.
func generateAggregateMetadata(dir string, assets []domain.Asset) []byte {
	segments := strings.Split(strings.TrimPrefix(dir, "/"), "/")
	groupID, artifactID := splitGroupArtifact(segments)

	versions := map[string]bool{}
	var lastCreated time.Time
	for _, a := range assets {
		rest := strings.TrimPrefix(a.Path, dir+"/")
		child, remainder, found := strings.Cut(rest, "/")
		if !found || remainder == "" {
			continue // a file directly in the artifact dir, not a version
		}
		versions[child] = true
		if a.CreatedAt.After(lastCreated) {
			lastCreated = a.CreatedAt
		}
	}
	if len(versions) == 0 {
		return nil
	}

	m := mavenMetadata{GroupID: groupID, ArtifactID: artifactID}
	for v := range versions {
		m.Versioning.Versions = append(m.Versioning.Versions, v)
	}
	sort.Slice(m.Versioning.Versions, func(i, j int) bool {
		return base.CompareLooseVersions(m.Versioning.Versions[i], m.Versioning.Versions[j]) < 0
	})
	for _, v := range m.Versioning.Versions {
		if base.CompareLooseVersions(v, m.Versioning.Latest) > 0 {
			m.Versioning.Latest = v
		}
		if !strings.Contains(strings.ToUpper(v), "SNAPSHOT") &&
			base.CompareLooseVersions(v, m.Versioning.Release) > 0 {
			m.Versioning.Release = v
		}
	}
	if !lastCreated.IsZero() {
		m.Versioning.LastUpdated = lastCreated.UTC().Format("20060102150405")
	}

	out, err := xml.Marshal(m)
	if err != nil {
		return nil
	}
	return append([]byte(xml.Header), out...)
}

// generatePerVersionMetadata reports the single latest (timestamp,
// buildNumber) build under one X.Y.Z-SNAPSHOT directory, with one
// snapshotVersion entry per extension/classifier present on that build.
func generatePerVersionMetadata(dir string, assets []domain.Asset) []byte {
	segments := strings.Split(strings.TrimPrefix(dir, "/"), "/")
	if len(segments) < 2 {
		return nil
	}
	version := segments[len(segments)-1]
	groupID, artifactID := splitGroupArtifact(segments[:len(segments)-1])
	baseVersion := strings.TrimSuffix(version, "-SNAPSHOT")
	prefix := artifactID + "-" + baseVersion + "-"

	type build struct {
		timestamp string
		number    int
	}
	var latest build
	type entry struct {
		classifier, extension string
	}
	byBuild := map[build][]entry{}

	for _, a := range assets {
		name := strings.TrimPrefix(a.Path, dir+"/")
		if strings.Contains(name, "/") || isChecksum(name) || name == metadataFile {
			continue
		}
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}
		m := snapshotFileRe.FindStringSubmatch(rest)
		if m == nil {
			continue
		}
		num, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		b := build{timestamp: m[1], number: num}
		byBuild[b] = append(byBuild[b], entry{classifier: m[3], extension: m[4]})
		if b.timestamp > latest.timestamp ||
			(b.timestamp == latest.timestamp && b.number > latest.number) {
			latest = b
		}
	}
	if latest.timestamp == "" {
		return nil
	}

	doc := perVersionMetadata{ModelVersion: "1.1.0", GroupID: groupID, ArtifactID: artifactID, Version: version}
	doc.Versioning.Snapshot.Timestamp = latest.timestamp
	doc.Versioning.Snapshot.BuildNumber = latest.number
	doc.Versioning.LastUpdated = strings.ReplaceAll(latest.timestamp, ".", "")
	value := baseVersion + "-" + latest.timestamp + "-" + strconv.Itoa(latest.number)
	entries := byBuild[latest]
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].extension != entries[j].extension {
			return entries[i].extension < entries[j].extension
		}
		return entries[i].classifier < entries[j].classifier
	})
	for _, e := range entries {
		doc.Versioning.SnapshotVersions = append(doc.Versioning.SnapshotVersions, snapshotVersion{
			Classifier: e.classifier,
			Extension:  e.extension,
			Value:      value,
			Updated:    doc.Versioning.LastUpdated,
		})
	}

	out, err := xml.Marshal(doc)
	if err != nil {
		return nil
	}
	return append([]byte(xml.Header), out...)
}

// serveGeneratedMetadata serves a generated maven-metadata.xml, or a checksum
// over it, and reports whether it handled the request.
func (h *Handler) serveGeneratedMetadata(c *gin.Context, repoName, filePath string) bool {
	metadataPath := filePath
	hashExt := ""
	if isChecksum(filePath) {
		hashExt = path.Ext(filePath)
		metadataPath = strings.TrimSuffix(filePath, hashExt)
	}
	if path.Base(metadataPath) != metadataFile {
		return false
	}
	doc := h.generateMetadata(c, repoName, metadataPath)
	if doc == nil {
		return false
	}
	var body []byte
	contentType := "application/xml"
	switch hashExt {
	case ".sha1":
		body = []byte(fmt.Sprintf("%x", sha1.Sum(doc))) //nolint:gosec // maven checksum
		contentType = "text/plain"
	case ".md5":
		body = []byte(fmt.Sprintf("%x", md5.Sum(doc))) //nolint:gosec // maven checksum
		contentType = "text/plain"
	case ".sha256":
		body = []byte(fmt.Sprintf("%x", sha256.Sum256(doc)))
		contentType = "text/plain"
	default:
		body = doc
	}
	c.Header("Content-Length", strconv.Itoa(len(body)))
	c.Header("Content-Type", contentType)
	if c.Request.Method == http.MethodHead {
		c.Status(http.StatusOK)
		return true
	}
	c.Data(http.StatusOK, contentType, body)
	return true
}
