package conda_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/conda"
)

func TestConda_GroupIndexSourcePath(t *testing.T) {
	h := conda.New(formats.Deps{})

	src, ok := h.GroupIndexSourcePath("/linux-64/repodata.json")
	require.True(t, ok)
	assert.Equal(t, "/linux-64/repodata.json", src)
	_, ok = h.GroupIndexSourcePath("/noarch/repodata.json")
	assert.True(t, ok)

	_, ok = h.GroupIndexSourcePath("/linux-64/numpy-1.0-py310.tar.bz2")
	assert.False(t, ok, "package files keep first-non-404")
}

func TestConda_MergeGroupIndex_UnionsPackages(t *testing.T) {
	h := conda.New(formats.Deps{})
	m1 := []byte(`{"info":{"subdir":"linux-64"},"packages":{"numpy-1.0.tar.bz2":{"name":"numpy","version":"1.0","m1":true}},"packages.conda":{}}`)
	m2 := []byte(`{"info":{"subdir":"linux-64"},"packages":{"numpy-1.0.tar.bz2":{"name":"numpy","version":"1.0"},"scipy-2.0.tar.bz2":{"name":"scipy","version":"2.0"}},"packages.conda":{"pandas-3.0.conda":{"name":"pandas"}}}`)

	body, ct, err := h.MergeGroupIndex("g", "/linux-64/repodata.json", []formats.GroupIndexPart{
		{Member: "m1", Body: m1}, {Member: "m2", Body: m2},
	})
	require.NoError(t, err)
	assert.Contains(t, ct, "json")

	var doc struct {
		Info          map[string]any            `json:"info"`
		Packages      map[string]map[string]any `json:"packages"`
		PackagesConda map[string]map[string]any `json:"packages.conda"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	assert.Equal(t, "linux-64", doc.Info["subdir"])
	require.Len(t, doc.Packages, 2, "numpy deduped, scipy unioned in")
	assert.Equal(t, true, doc.Packages["numpy-1.0.tar.bz2"]["m1"], "first member wins per filename")
	assert.NotNil(t, doc.Packages["scipy-2.0.tar.bz2"])
	assert.NotNil(t, doc.PackagesConda["pandas-3.0.conda"])
}

func TestConda_MergeGroupIndex_MalformedPartSkipped(t *testing.T) {
	h := conda.New(formats.Deps{})
	body, _, err := h.MergeGroupIndex("g", "/noarch/repodata.json", []formats.GroupIndexPart{
		{Member: "bad", Body: []byte("nope")},
		{Member: "ok", Body: []byte(`{"info":{"subdir":"noarch"},"packages":{"a-1.tar.bz2":{"name":"a"}}}`)},
	})
	require.NoError(t, err)
	assert.Contains(t, string(body), "a-1.tar.bz2")
}
