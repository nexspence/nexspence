package terraform_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/terraform"
)

func TestTerraform_GroupIndexSourcePath(t *testing.T) {
	h := terraform.New(formats.Deps{})

	_, ok := h.GroupIndexSourcePath("/v1/providers/hashicorp/aws/versions")
	assert.True(t, ok)
	_, ok = h.GroupIndexSourcePath("/v1/modules/ns/vpc/aws/versions")
	assert.True(t, ok)
	_, ok = h.GroupIndexSourcePath("/.well-known/terraform.json")
	assert.True(t, ok)

	_, ok = h.GroupIndexSourcePath("/v1/providers/hashicorp/aws/1.0.0/download/linux/amd64")
	assert.False(t, ok, "downloads keep first-non-404")
}

func TestTerraform_MergeGroupIndex_ProviderVersionsUnion(t *testing.T) {
	h := terraform.New(formats.Deps{})
	m1 := []byte(`{"versions":[{"version":"1.0.0","protocols":["5.0"],"platforms":[{"os":"linux","arch":"amd64"}]}]}`)
	m2 := []byte(`{"versions":[{"version":"1.0.0","protocols":["5.0"],"platforms":[{"os":"darwin","arch":"arm64"}]},{"version":"2.0.0","protocols":["5.0"],"platforms":[]}]}`)

	body, ct, err := h.MergeGroupIndex("g", "/v1/providers/ns/aws/versions", []formats.GroupIndexPart{
		{Member: "m1", Body: m1}, {Member: "m2", Body: m2},
	})
	require.NoError(t, err)
	assert.Contains(t, ct, "json")

	var doc struct {
		Versions []map[string]any `json:"versions"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	require.Len(t, doc.Versions, 2, "1.0.0 deduped (first wins), 2.0.0 unioned in")

	vers := map[string]map[string]any{}
	for _, v := range doc.Versions {
		vers[v["version"].(string)] = v
	}
	// first member wins for 1.0.0 → its platforms are linux/amd64
	p := vers["1.0.0"]["platforms"].([]any)[0].(map[string]any)
	assert.Equal(t, "linux", p["os"])
	assert.NotNil(t, vers["2.0.0"])
}

func TestTerraform_MergeGroupIndex_ModuleVersionsUnion(t *testing.T) {
	h := terraform.New(formats.Deps{})
	m1 := []byte(`{"modules":[{"versions":[{"version":"0.1.0"}]}]}`)
	m2 := []byte(`{"modules":[{"versions":[{"version":"0.1.0"},{"version":"0.2.0"}]}]}`)

	body, _, err := h.MergeGroupIndex("g", "/v1/modules/ns/vpc/aws/versions", []formats.GroupIndexPart{
		{Member: "m1", Body: m1}, {Member: "m2", Body: m2},
	})
	require.NoError(t, err)
	var doc struct {
		Modules []struct {
			Versions []map[string]string `json:"versions"`
		} `json:"modules"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	require.Len(t, doc.Modules, 1)
	assert.Len(t, doc.Modules[0].Versions, 2)
}

func TestTerraform_MergeGroupIndex_DiscoveryRewritesMemberURLs(t *testing.T) {
	h := terraform.New(formats.Deps{BaseURL: "http://localhost:8080"})
	m1 := []byte(`{"providers.v1":"http://localhost:8080/repository/m1/v1/providers/","modules.v1":"http://localhost:8080/repository/m1/v1/modules/"}`)

	body, _, err := h.MergeGroupIndex("tf-group", "/.well-known/terraform.json", []formats.GroupIndexPart{
		{Member: "m1", Body: m1},
	})
	require.NoError(t, err)
	out := string(body)
	assert.Contains(t, out, "/repository/tf-group/v1/providers/")
	assert.NotContains(t, out, "/repository/m1/")
}
