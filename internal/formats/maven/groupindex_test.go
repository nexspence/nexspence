package maven_test

import (
	"crypto/sha1" //nolint:gosec // maven protocol checksum
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/maven"
)

func metadataXML(latest, release string, versions ...string) []byte {
	s := "<metadata><groupId>com.foo</groupId><artifactId>bar</artifactId><versioning>"
	if latest != "" {
		s += "<latest>" + latest + "</latest>"
	}
	if release != "" {
		s += "<release>" + release + "</release>"
	}
	s += "<versions>"
	for _, v := range versions {
		s += "<version>" + v + "</version>"
	}
	s += "</versions><lastUpdated>20240101000000</lastUpdated></versioning></metadata>"
	return []byte(s)
}

func TestMaven_GroupIndexSourcePath(t *testing.T) {
	h := maven.New(formats.Deps{})

	src, ok := h.GroupIndexSourcePath("/com/foo/bar/maven-metadata.xml")
	require.True(t, ok)
	assert.Equal(t, "/com/foo/bar/maven-metadata.xml", src)

	// Checksum paths feed off the base metadata document (#99: the checksum
	// must be computed over the MERGED doc, not any single member's copy).
	src, ok = h.GroupIndexSourcePath("/com/foo/bar/maven-metadata.xml.sha1")
	require.True(t, ok)
	assert.Equal(t, "/com/foo/bar/maven-metadata.xml", src)

	_, ok = h.GroupIndexSourcePath("/com/foo/bar/1.0/bar-1.0.jar")
	assert.False(t, ok)
	_, ok = h.GroupIndexSourcePath("/com/foo/bar/1.0/bar-1.0.jar.sha1")
	assert.False(t, ok)
}

func TestMaven_MergeGroupIndex_UnionsVersions(t *testing.T) {
	h := maven.New(formats.Deps{})
	parts := []formats.GroupIndexPart{
		{Member: "m1", Body: metadataXML("1.0", "1.0", "0.9", "1.0")},
		{Member: "m2", Body: metadataXML("2.0", "2.0", "1.0", "2.0")},
	}

	body, ct, err := h.MergeGroupIndex("g", "/com/foo/bar/maven-metadata.xml", parts)
	require.NoError(t, err)
	assert.Contains(t, ct, "xml")
	out := string(body)
	assert.Contains(t, out, "<version>0.9</version>")
	assert.Contains(t, out, "<version>1.0</version>")
	assert.Contains(t, out, "<version>2.0</version>")
	// latest/release recomputed over the union — m2's 2.0 must win even
	// though m1 is first.
	assert.Contains(t, out, "<latest>2.0</latest>")
	assert.Contains(t, out, "<release>2.0</release>")
	assert.Contains(t, out, "<groupId>com.foo</groupId>")
}

func TestMaven_MergeGroupIndex_ReleaseSkipsSnapshots(t *testing.T) {
	h := maven.New(formats.Deps{})
	parts := []formats.GroupIndexPart{
		{Member: "m1", Body: metadataXML("1.0", "1.0", "1.0")},
		{Member: "m2", Body: metadataXML("2.0-SNAPSHOT", "", "2.0-SNAPSHOT")},
	}

	body, _, err := h.MergeGroupIndex("g", "/x/maven-metadata.xml", parts)
	require.NoError(t, err)
	out := string(body)
	assert.Contains(t, out, "<latest>2.0-SNAPSHOT</latest>")
	assert.Contains(t, out, "<release>1.0</release>", "release must skip SNAPSHOT versions")
}

func TestMaven_MergeGroupIndex_ChecksumOfMergedDoc(t *testing.T) {
	h := maven.New(formats.Deps{})
	parts := []formats.GroupIndexPart{
		{Member: "m1", Body: metadataXML("1.0", "1.0", "1.0")},
		{Member: "m2", Body: metadataXML("2.0", "2.0", "2.0")},
	}

	merged, _, err := h.MergeGroupIndex("g", "/x/maven-metadata.xml", parts)
	require.NoError(t, err)
	sum, ct, err := h.MergeGroupIndex("g", "/x/maven-metadata.xml.sha1", parts)
	require.NoError(t, err)
	assert.Contains(t, ct, "text/plain")
	assert.Equal(t, fmt.Sprintf("%x", sha1.Sum(merged)), string(sum)) //nolint:gosec
}

func TestMaven_MergeGroupIndex_MalformedPartSkipped(t *testing.T) {
	h := maven.New(formats.Deps{})
	parts := []formats.GroupIndexPart{
		{Member: "bad", Body: []byte("not xml")},
		{Member: "ok", Body: metadataXML("1.0", "1.0", "1.0")},
	}

	body, _, err := h.MergeGroupIndex("g", "/x/maven-metadata.xml", parts)
	require.NoError(t, err)
	assert.Contains(t, string(body), "<version>1.0</version>")
}

func perVersionXML(timestamp string, buildNumber int) []byte {
	return []byte(fmt.Sprintf(`<metadata modelVersion="1.1.0"><groupId>com.foo</groupId><artifactId>bar</artifactId><version>2.0-SNAPSHOT</version><versioning><snapshot><timestamp>%s</timestamp><buildNumber>%d</buildNumber></snapshot><lastUpdated>%s</lastUpdated><snapshotVersions><snapshotVersion><extension>jar</extension><value>2.0-%s-%d</value></snapshotVersion></snapshotVersions></versioning></metadata>`,
		timestamp, buildNumber, timestamp, timestamp, buildNumber))
}

// The per-SNAPSHOT-version metadata shape (#350): merging it as if it were the
// aggregate shape silently produced a content-free document — the XML decoder
// leaves an unrecognized shape as zero values instead of failing. The merge
// must dispatch on the requested path and, for a SNAPSHOT directory, pick the
// member reporting the single latest (timestamp, buildNumber) build.
func TestMaven_MergeGroupIndex_PerVersionShape_PicksLatestBuild(t *testing.T) {
	h := maven.New(formats.Deps{})
	parts := []formats.GroupIndexPart{
		{Member: "m1", Body: perVersionXML("20260829.101010", 5)},
		{Member: "m2", Body: perVersionXML("20260830.123456", 3)},
	}

	body, ct, err := h.MergeGroupIndex("g", "/com/foo/bar/2.0-SNAPSHOT/maven-metadata.xml", parts)
	require.NoError(t, err)
	assert.Contains(t, ct, "xml")
	out := string(body)
	assert.Contains(t, out, "<timestamp>20260830.123456</timestamp>", "later timestamp wins regardless of member order")
	assert.Contains(t, out, "<buildNumber>3</buildNumber>")
	assert.Contains(t, out, "<value>2.0-20260830.123456-3</value>")
	assert.NotContains(t, out, "20260829.101010")
	assert.NotContains(t, out, "<versions>", "must not degrade into an empty aggregate document")
}

func TestMaven_MergeGroupIndex_PerVersionShape_ChecksumOfWinner(t *testing.T) {
	h := maven.New(formats.Deps{})
	parts := []formats.GroupIndexPart{
		{Member: "m1", Body: perVersionXML("20260830.123456", 3)},
	}

	doc, _, err := h.MergeGroupIndex("g", "/com/foo/bar/2.0-SNAPSHOT/maven-metadata.xml", parts)
	require.NoError(t, err)
	sum, ct, err := h.MergeGroupIndex("g", "/com/foo/bar/2.0-SNAPSHOT/maven-metadata.xml.sha1", parts)
	require.NoError(t, err)
	assert.Contains(t, ct, "text/plain")
	assert.Equal(t, fmt.Sprintf("%x", sha1.Sum(doc)), string(sum)) //nolint:gosec
}
