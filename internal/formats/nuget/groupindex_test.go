package nuget_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/nuget"
)

func TestNuGet_GroupIndexSourcePath(t *testing.T) {
	h := nuget.New(formats.Deps{})

	_, ok := h.GroupIndexSourcePath("/v3/flatcontainer/my.lib/index.json")
	assert.True(t, ok)
	_, ok = h.GroupIndexSourcePath("/index.json")
	assert.True(t, ok, "service index needs member→group URL rewrite")

	_, ok = h.GroupIndexSourcePath("/v3/flatcontainer/my.lib/2.1.0/my.lib.2.1.0.nupkg")
	assert.False(t, ok, "packages keep first-non-404")
	_, ok = h.GroupIndexSourcePath("/v3/registration/my.lib/index.json")
	assert.False(t, ok, "registration 404s on miss — fan-out works; not merged this phase")
}

func TestNuGet_MergeGroupIndex_VersionListUnion(t *testing.T) {
	// Kills the 200-on-empty shadowing: the empty version list is just a part.
	h := nuget.New(formats.Deps{})
	body, ct, err := h.MergeGroupIndex("g", "/v3/flatcontainer/pkg/index.json", []formats.GroupIndexPart{
		{Member: "empty", Body: []byte(`{"versions":[]}`)},
		{Member: "m1", Body: []byte(`{"versions":["1.0.0","1.1.0"]}`)},
		{Member: "m2", Body: []byte(`{"versions":["1.1.0","2.0.0"]}`)},
	})
	require.NoError(t, err)
	assert.Contains(t, ct, "json")

	var doc struct {
		Versions []string `json:"versions"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	assert.Equal(t, []string{"1.0.0", "1.1.0", "2.0.0"}, doc.Versions)
}

func TestNuGet_MergeGroupIndex_ServiceIndexRewritesMemberURLs(t *testing.T) {
	h := nuget.New(formats.Deps{BaseURL: "http://localhost:8080"})
	m1 := []byte(`{"version":"3.0.0","resources":[{"@id":"http://localhost:8080/repository/m1/v3/flatcontainer/","@type":"PackageBaseAddress/3.0.0"}]}`)

	body, _, err := h.MergeGroupIndex("ng-group", "/index.json", []formats.GroupIndexPart{
		{Member: "m1", Body: m1},
	})
	require.NoError(t, err)
	out := string(body)
	assert.Contains(t, out, "/repository/ng-group/v3/flatcontainer/")
	assert.NotContains(t, out, "/repository/m1/")
}
