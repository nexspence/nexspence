package pypi_test

import (
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
