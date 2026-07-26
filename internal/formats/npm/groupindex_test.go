package npm_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/npm"
)

func TestNPM_GroupIndexSourcePath(t *testing.T) {
	h := npm.New(formats.Deps{})

	src, ok := h.GroupIndexSourcePath("/lodash")
	require.True(t, ok)
	assert.Equal(t, "/lodash", src)

	_, ok = h.GroupIndexSourcePath("/@babel/core")
	assert.True(t, ok, "scoped packuments are indexes too")

	_, ok = h.GroupIndexSourcePath("/lodash/-/lodash-4.17.21.tgz")
	assert.False(t, ok, "tarballs keep first-non-404")
	_, ok = h.GroupIndexSourcePath("/-/package/lodash/dist-tags")
	assert.False(t, ok, "dist-tags API keeps first-non-404")
	_, ok = h.GroupIndexSourcePath("/")
	assert.False(t, ok, "repo root is not a packument")
}

func TestNPM_MergeGroupIndex_UnionsVersionsAndRewritesTarballs(t *testing.T) {
	deps := formats.Deps{BaseURL: "http://localhost:8080"}
	h := npm.New(deps)

	m1 := []byte(`{"name":"lp","dist-tags":{"latest":"1.0.0"},"versions":{
		"1.0.0":{"dist":{"tarball":"http://localhost:8080/repository/m1/lp/-/lp-1.0.0.tgz"}}}}`)
	m2 := []byte(`{"name":"lp","dist-tags":{"latest":"2.0.0","beta":"2.1.0-beta"},"versions":{
		"1.0.0":{"dist":{"tarball":"http://localhost:8080/repository/m2/lp/-/lp-1.0.0.tgz"},"m2marker":true},
		"2.0.0":{"dist":{"tarball":"http://localhost:8080/repository/m2/lp/-/lp-2.0.0.tgz"}}}}`)

	body, ct, err := h.MergeGroupIndex("npm-group", "/lp", []formats.GroupIndexPart{
		{Member: "m1", Body: m1}, {Member: "m2", Body: m2},
	})
	require.NoError(t, err)
	assert.Contains(t, ct, "json")

	var doc map[string]any
	require.NoError(t, json.Unmarshal(body, &doc))
	versions := doc["versions"].(map[string]any)
	require.Len(t, versions, 2)

	// First member wins per version key: 1.0.0 comes from m1 (no m2marker).
	v1 := versions["1.0.0"].(map[string]any)
	assert.Nil(t, v1["m2marker"], "member order = priority; m1's 1.0.0 must win")
	// Union brings in m2's 2.0.0.
	v2 := versions["2.0.0"].(map[string]any)

	// Every tarball is re-rooted at the GROUP, never a member.
	tb1 := v1["dist"].(map[string]any)["tarball"].(string)
	tb2 := v2["dist"].(map[string]any)["tarball"].(string)
	assert.Equal(t, "http://localhost:8080/repository/npm-group/lp/-/lp-1.0.0.tgz", tb1)
	assert.Equal(t, "http://localhost:8080/repository/npm-group/lp/-/lp-2.0.0.tgz", tb2)

	// dist-tags: first member wins per tag; m2 contributes its unique tag.
	tags := doc["dist-tags"].(map[string]any)
	assert.Equal(t, "1.0.0", tags["latest"], "m1 is first — its latest wins")
	assert.Equal(t, "2.1.0-beta", tags["beta"])
}

func TestNPM_MergeGroupIndex_MalformedPartSkipped(t *testing.T) {
	h := npm.New(formats.Deps{BaseURL: "http://x"})
	body, _, err := h.MergeGroupIndex("g", "/p", []formats.GroupIndexPart{
		{Member: "bad", Body: []byte("<html>")},
		{Member: "ok", Body: []byte(`{"name":"p","versions":{"1.0.0":{}}}`)},
	})
	require.NoError(t, err)
	assert.Contains(t, string(body), `"1.0.0"`)
}
