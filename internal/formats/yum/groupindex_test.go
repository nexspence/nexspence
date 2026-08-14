package yum_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/yum"
)

const primaryM1 = `<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" packages="1"><package type="rpm"><name>curl</name><arch>x86_64</arch><version epoch="0" ver="8.0.0" rel="1"></version><size package="100"></size><location href="/pool/curl-8.0.0-1.x86_64.rpm"></location></package></metadata>`

const primaryM2 = `<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" packages="2"><package type="rpm"><name>curl</name><arch>x86_64</arch><version epoch="0" ver="8.0.0" rel="1"></version><size package="100"></size><location href="/pool/curl-8.0.0-1.x86_64.rpm"></location></package><package type="rpm"><name>vim</name><arch>x86_64</arch><version epoch="0" ver="9.0" rel="2"></version><size package="200"></size><location href="/pool/vim-9.0-2.x86_64.rpm"></location></package></metadata>`

func TestYum_GroupIndexSourcePath(t *testing.T) {
	h := yum.New(formats.Deps{})

	_, ok := h.GroupIndexSourcePath("/repodata/repomd.xml")
	assert.True(t, ok)
	src, ok := h.GroupIndexSourcePath("/repodata/primary.xml.gz")
	require.True(t, ok)
	assert.Equal(t, "/repodata/primary.xml", src, ".gz fans out on the plain doc")

	_, ok = h.GroupIndexSourcePath("/pool/curl-8.0.0-1.x86_64.rpm")
	assert.False(t, ok, "rpm downloads keep first-non-404")
}

func TestYum_MergeGroupIndex_PrimaryUnion(t *testing.T) {
	h := yum.New(formats.Deps{})
	body, ct, err := h.MergeGroupIndex("g", "/repodata/primary.xml", []formats.GroupIndexPart{
		{Member: "m1", Body: []byte(primaryM1)},
		{Member: "m2", Body: []byte(primaryM2)},
	})
	require.NoError(t, err)
	assert.Contains(t, ct, "xml")
	out := string(body)
	assert.Contains(t, out, "<name>curl</name>")
	assert.Contains(t, out, "<name>vim</name>")
	assert.Equal(t, 1, bytes.Count(body, []byte("<name>curl</name>")), "dedup by location href")
	assert.Contains(t, out, `packages="2"`, "package count recomputed")
}

func TestYum_MergeGroupIndex_PrimaryGzip(t *testing.T) {
	h := yum.New(formats.Deps{})
	body, ct, err := h.MergeGroupIndex("g", "/repodata/primary.xml.gz", []formats.GroupIndexPart{
		{Member: "m1", Body: []byte(primaryM1)},
	})
	require.NoError(t, err)
	assert.Contains(t, ct, "gzip")
	zr, err := gzip.NewReader(bytes.NewReader(body))
	require.NoError(t, err)
	plain, _ := io.ReadAll(zr)
	assert.Contains(t, string(plain), "<name>curl</name>")
}

// repomd.xml is built over the documents the group serves, not relayed: a
// member's copy carries checksums of that member's own single-member metadata,
// which dnf rejects against the union it downloads (#222).
func TestYum_MergeGroupIndex_RepomdChecksumsTheFetchedDocuments(t *testing.T) {
	h := yum.New(formats.Deps{})
	served := map[string][]byte{
		"/repodata/primary.xml":      []byte("merged-primary"),
		"/repodata/primary.xml.gz":   []byte("merged-primary-gz"),
		"/repodata/filelists.xml":    []byte("merged-filelists"),
		"/repodata/filelists.xml.gz": []byte("merged-filelists-gz"),
		"/repodata/other.xml":        []byte("merged-other"),
		"/repodata/other.xml.gz":     []byte("merged-other-gz"),
	}
	fetch := func(p string) ([]byte, error) {
		body, ok := served[p]
		if !ok {
			return nil, errors.New("no such document")
		}
		return body, nil
	}

	memberRepomd := []byte(`<repomd><data type="primary"><checksum type="sha256">deadbeef</checksum>` +
		`<location href="repodata/primary.xml.gz"/></data></repomd>`)
	body, ct, err := h.MergeGroupIndexWithFetch("yum-group", "/repodata/repomd.xml",
		[]formats.GroupIndexPart{{Member: "m1", Body: memberRepomd}}, fetch)
	require.NoError(t, err)
	assert.Contains(t, ct, "xml")

	var doc struct {
		Data []struct {
			Type     string `xml:"type,attr"`
			Location struct {
				Href string `xml:"href,attr"`
			} `xml:"location"`
			Checksum struct {
				Value string `xml:",chardata"`
			} `xml:"checksum"`
			OpenChecksum struct {
				Value string `xml:",chardata"`
			} `xml:"open-checksum"`
			Size     int64 `xml:"size"`
			OpenSize int64 `xml:"open-size"`
		} `xml:"data"`
	}
	require.NoError(t, xml.Unmarshal(body, &doc))
	require.Len(t, doc.Data, 3, "primary, filelists and other")

	for _, d := range doc.Data {
		gz := served["/repodata/"+d.Type+".xml.gz"]
		plain := served["/repodata/"+d.Type+".xml"]
		assert.Equal(t, "repodata/"+d.Type+".xml.gz", d.Location.Href)
		assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(gz)), d.Checksum.Value)
		assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(plain)), d.OpenChecksum.Value)
		assert.Equal(t, int64(len(gz)), d.Size)
		assert.Equal(t, int64(len(plain)), d.OpenSize)
	}
	assert.NotContains(t, string(body), "deadbeef", "a member's own checksum never survives into the group's repomd")
}

// A document no member could serve is one the group cannot vouch for, so it is
// left out rather than advertised with a checksum of nothing.
func TestYum_MergeGroupIndex_RepomdSkipsUnavailableDocuments(t *testing.T) {
	h := yum.New(formats.Deps{})
	fetch := func(p string) ([]byte, error) {
		if strings.HasPrefix(p, "/repodata/primary.xml") {
			return []byte("merged-primary" + p), nil
		}
		return nil, errors.New("no such document")
	}

	body, _, err := h.MergeGroupIndexWithFetch("g", "/repodata/repomd.xml",
		[]formats.GroupIndexPart{{Member: "m1", Body: []byte("<repomd/>")}}, fetch)
	require.NoError(t, err)
	assert.Contains(t, string(body), `type="primary"`)
	assert.NotContains(t, string(body), `type="filelists"`)
}

// Without the group's own documents there is nothing to checksum, and a repomd
// that guesses is worse than none — the group falls back to a member's copy.
func TestYum_MergeGroupIndex_RepomdWithoutFetcherFails(t *testing.T) {
	h := yum.New(formats.Deps{})
	_, _, err := h.MergeGroupIndex("g", "/repodata/repomd.xml",
		[]formats.GroupIndexPart{{Member: "m1", Body: []byte("<repomd/>")}})
	require.Error(t, err)
}

func TestYum_MergeGroupIndex_FilelistsAndOtherUnion(t *testing.T) {
	h := yum.New(formats.Deps{})
	m1 := `<?xml version="1.0" encoding="UTF-8"?><filelists xmlns="http://linux.duke.edu/metadata/filelists" packages="1">` +
		`<package pkgid="aaa" name="curl" arch="x86_64"><version epoch="0" ver="8.0.0" rel="1"></version></package></filelists>`
	m2 := `<?xml version="1.0" encoding="UTF-8"?><filelists xmlns="http://linux.duke.edu/metadata/filelists" packages="2">` +
		`<package pkgid="aaa" name="curl" arch="x86_64"><version epoch="0" ver="8.0.0" rel="1"></version></package>` +
		`<package pkgid="bbb" name="vim" arch="x86_64"><version epoch="0" ver="9.0" rel="2"></version></package></filelists>`

	body, ct, err := h.MergeGroupIndex("g", "/repodata/filelists.xml", []formats.GroupIndexPart{
		{Member: "m1", Body: []byte(m1)},
		{Member: "m2", Body: []byte(m2)},
	})
	require.NoError(t, err)
	assert.Contains(t, ct, "xml")
	out := string(body)
	assert.Contains(t, out, `name="curl"`)
	assert.Contains(t, out, `name="vim"`)
	assert.Equal(t, 1, strings.Count(out, `name="curl"`), "dedup by pkgid")
	assert.Contains(t, out, `packages="2"`, "package count recomputed")

	other := `<?xml version="1.0" encoding="UTF-8"?><otherdata xmlns="http://linux.duke.edu/metadata/other" packages="1">` +
		`<package pkgid="ccc" name="wget" arch="x86_64"><version epoch="0" ver="1.21" rel="1"></version></package></otherdata>`
	body, _, err = h.MergeGroupIndex("g", "/repodata/other.xml", []formats.GroupIndexPart{{Member: "m1", Body: []byte(other)}})
	require.NoError(t, err)
	assert.Contains(t, string(body), `name="wget"`)
	assert.Contains(t, string(body), "otherdata")
}

// A member that answered with something unparsable is merged around, but a
// document nobody could parse is an error rather than a silently empty index.
func TestYum_MergeGroupIndex_UnparsableMembers(t *testing.T) {
	h := yum.New(formats.Deps{})

	body, _, err := h.MergeGroupIndex("g", "/repodata/filelists.xml", []formats.GroupIndexPart{
		{Member: "broken", Body: []byte("not xml at all")},
		{Member: "m1", Body: []byte(`<filelists packages="1"><package pkgid="aaa" name="curl" arch="x86_64"></package></filelists>`)},
	})
	require.NoError(t, err)
	assert.Contains(t, string(body), `name="curl"`)

	_, _, err = h.MergeGroupIndex("g", "/repodata/filelists.xml", []formats.GroupIndexPart{
		{Member: "broken", Body: []byte("not xml at all")},
	})
	require.Error(t, err)
}

func TestYum_GroupIndexSourcePath_FilelistsAndOther(t *testing.T) {
	h := yum.New(formats.Deps{})
	for _, typ := range []string{"filelists", "other"} {
		src, ok := h.GroupIndexSourcePath("/repodata/" + typ + ".xml")
		require.True(t, ok, typ)
		assert.Equal(t, "/repodata/"+typ+".xml", src)

		src, ok = h.GroupIndexSourcePath("/repodata/" + typ + ".xml.gz")
		require.True(t, ok, typ+".gz")
		assert.Equal(t, "/repodata/"+typ+".xml", src, ".gz fans out on the plain doc")
	}
}
