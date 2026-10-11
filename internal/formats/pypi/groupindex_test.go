package pypi_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/pypi"
)

func TestPyPI_GroupIndexSourcePath(t *testing.T) {
	h := pypi.New(formats.Deps{})

	_, ok := h.GroupIndexSourcePath("/simple")
	assert.True(t, ok)
	src, ok := h.GroupIndexSourcePath("/simple/requests")
	require.True(t, ok)
	assert.Equal(t, "/simple/requests", src)

	_, ok = h.GroupIndexSourcePath("/packages/ab/cd/requests-2.31.0.whl")
	assert.False(t, ok, "release files keep first-non-404")
}

func TestPyPI_MergeGroupIndex_UnionsAnchors(t *testing.T) {
	h := pypi.New(formats.Deps{BaseURL: "http://localhost:8080"})

	m1 := []byte(`<html><body><h1>Links for pkg</h1>
<a href="http://localhost:8080/repository/m1/packages/aa/pkg-1.0.whl#sha256=aaa">pkg-1.0.whl</a>
</body></html>`)
	m2 := []byte(`<html><body><h1>Links for pkg</h1>
<a href="http://localhost:8080/repository/m2/packages/bb/pkg-1.0.whl#sha256=OTHER">pkg-1.0.whl</a>
<a href="http://localhost:8080/repository/m2/packages/cc/pkg-2.0.whl#sha256=ccc">pkg-2.0.whl</a>
</body></html>`)

	body, ct, err := h.MergeGroupIndex("py-group", "/simple/pkg", []formats.GroupIndexPart{
		{Member: "m1", Body: m1}, {Member: "m2", Body: m2},
	})
	require.NoError(t, err)
	assert.Contains(t, ct, "text/html")
	out := string(body)

	// Union with first-member priority on duplicate filenames.
	assert.Contains(t, out, "http://localhost:8080/repository/py-group/packages/aa/pkg-1.0.whl#sha256=aaa")
	assert.NotContains(t, out, "sha256=OTHER", "m1's pkg-1.0.whl must win")
	assert.Contains(t, out, "http://localhost:8080/repository/py-group/packages/cc/pkg-2.0.whl#sha256=ccc")
	// No member URLs leak.
	assert.NotContains(t, out, "/repository/m1/")
	assert.NotContains(t, out, "/repository/m2/")
}

func TestPyPI_MergeGroupIndex_EmptyPartContributesNothing(t *testing.T) {
	// The 200-on-empty page (#99 shadowing) is just an anchor-less part.
	h := pypi.New(formats.Deps{BaseURL: "http://localhost:8080"})

	empty := []byte(`<html><body><h1>Links for pkg</h1></body></html>`)
	full := []byte(`<html><body><h1>Links for pkg</h1>
<a href="http://localhost:8080/repository/full/packages/dd/pkg-3.0.whl">pkg-3.0.whl</a>
</body></html>`)

	body, _, err := h.MergeGroupIndex("g", "/simple/pkg", []formats.GroupIndexPart{
		{Member: "empty", Body: empty}, {Member: "full", Body: full},
	})
	require.NoError(t, err)
	assert.Contains(t, string(body), "pkg-3.0.whl")
	assert.Contains(t, string(body), "/repository/g/packages/dd/pkg-3.0.whl")
}

// Every attribute a member serves on a link reaches the client through the
// group (#661): pip skips releases by data-requires-python, honors
// data-yanked, and fetches PEP 658 metadata by data-core-metadata. Only the
// href is re-rooted.
func TestPyPI_MergeGroupIndex_KeepsAnchorAttributes(t *testing.T) {
	h := pypi.New(formats.Deps{BaseURL: "http://localhost:8080"})

	proxy := []byte(`<html><body><h1>Links for numpy</h1>
<a href="http://localhost:8080/repository/proxy/packages/fd/5f/numpy-2.3.0.tar.gz#sha256=abc" data-requires-python="&gt;=3.11" data-dist-info-metadata="sha256=def" data-core-metadata="sha256=def">numpy-2.3.0.tar.gz</a><br/>
<a data-yanked="broken build" href="http://localhost:8080/repository/proxy/packages/aa/numpy-2.2.9.tar.gz#sha256=111" data-requires-python="&gt;=3.10">numpy-2.2.9.tar.gz</a><br/>
</body></html>`)
	hosted := []byte(`<html><body><h1>Links for numpy</h1>
<a href="http://localhost:8080/repository/hosted/packages/numpy/numpy-2.3.0.tar.gz#sha256=OTHER" data-requires-python="">numpy-2.3.0.tar.gz</a>
<a name="anchor-without-href">not a link</a>
</body></html>`)

	body, _, err := h.MergeGroupIndex("py-group", "/simple/numpy", []formats.GroupIndexPart{
		{Member: "proxy", Body: proxy}, {Member: "hosted", Body: hosted},
	})
	require.NoError(t, err)
	out := string(body)

	assert.Contains(t, out, `<a href="http://localhost:8080/repository/py-group/packages/fd/5f/numpy-2.3.0.tar.gz#sha256=abc" data-requires-python="&gt;=3.11" data-dist-info-metadata="sha256=def" data-core-metadata="sha256=def">numpy-2.3.0.tar.gz</a>`,
		"attributes kept verbatim, entities included; href re-rooted")
	assert.Contains(t, out, `<a data-yanked="broken build" href="http://localhost:8080/repository/py-group/packages/aa/numpy-2.2.9.tar.gz#sha256=111" data-requires-python="&gt;=3.10">numpy-2.2.9.tar.gz</a>`,
		"an href that is not the first attribute is found and re-rooted too")
	assert.NotContains(t, out, "sha256=OTHER", "the first member's file and attributes win")
	assert.NotContains(t, out, "not a link", "anchors without an href are not links")
	assert.NotContains(t, out, "/repository/proxy/")
	assert.Equal(t, 2, strings.Count(out, "data-requires-python="))
}
