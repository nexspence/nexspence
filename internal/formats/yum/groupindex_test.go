package yum_test

import (
	"bytes"
	"compress/gzip"
	"io"
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

func TestYum_MergeGroupIndex_RepomdRewritesMemberHref(t *testing.T) {
	h := yum.New(formats.Deps{})
	repomd := []byte(`<repomd><data type="primary"><location href="/repository/m1/repodata/primary.xml.gz"/></data></repomd>`)
	body, _, err := h.MergeGroupIndex("yum-group", "/repodata/repomd.xml", []formats.GroupIndexPart{
		{Member: "m1", Body: repomd},
	})
	require.NoError(t, err)
	out := string(body)
	assert.Contains(t, out, "/repository/yum-group/repodata/primary.xml.gz")
	assert.NotContains(t, out, "/repository/m1/")
}
