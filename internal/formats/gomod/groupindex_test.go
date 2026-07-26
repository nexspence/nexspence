package gomod_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/gomod"
)

func TestGomod_GroupIndexSourcePath(t *testing.T) {
	h := gomod.New(formats.Deps{})

	src, ok := h.GroupIndexSourcePath("/github.com/foo/bar/@v/list")
	require.True(t, ok)
	assert.Equal(t, "/github.com/foo/bar/@v/list", src)

	_, ok = h.GroupIndexSourcePath("/github.com/foo/bar/@latest")
	assert.True(t, ok)

	_, ok = h.GroupIndexSourcePath("/github.com/foo/bar/@v/v1.0.0.zip")
	assert.False(t, ok, "module files keep first-non-404")
	_, ok = h.GroupIndexSourcePath("/github.com/foo/bar/@v/v1.0.0.info")
	assert.False(t, ok)
}

func TestGomod_MergeGroupIndex_ListUnion(t *testing.T) {
	h := gomod.New(formats.Deps{})
	body, ct, err := h.MergeGroupIndex("g", "/m/@v/list", []formats.GroupIndexPart{
		{Member: "m1", Body: []byte("v1.0.0\nv1.1.0\n")},
		{Member: "m2", Body: []byte("v1.1.0\nv2.0.0\n")},
	})
	require.NoError(t, err)
	assert.Contains(t, ct, "text/plain")
	assert.Equal(t, "v1.0.0\nv1.1.0\nv2.0.0\n", string(body), "union, dedup, member order")
}

func TestGomod_MergeGroupIndex_LatestPicksMax(t *testing.T) {
	h := gomod.New(formats.Deps{})
	body, ct, err := h.MergeGroupIndex("g", "/m/@latest", []formats.GroupIndexPart{
		{Member: "m1", Body: []byte(`{"Version":"v1.0.0","Time":"2024-01-01T00:00:00Z"}`)},
		{Member: "m2", Body: []byte(`{"Version":"v2.0.0","Time":"2025-01-01T00:00:00Z"}`)},
	})
	require.NoError(t, err)
	assert.Contains(t, ct, "json")
	assert.Contains(t, string(body), `"v2.0.0"`, "the max version across members wins")
}

func TestGomod_MergeGroupIndex_MalformedLatestSkipped(t *testing.T) {
	h := gomod.New(formats.Deps{})
	body, _, err := h.MergeGroupIndex("g", "/m/@latest", []formats.GroupIndexPart{
		{Member: "bad", Body: []byte("not json")},
		{Member: "ok", Body: []byte(`{"Version":"v1.0.0"}`)},
	})
	require.NoError(t, err)
	assert.Contains(t, string(body), "v1.0.0")
}
