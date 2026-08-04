package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// The registry browse tree is the same for both labels of the OCI protocol.
func TestDockerTree_OCIFormatRepo_IsAccepted(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	require.NoError(t, repos.Create(context.Background(), &domain.Repository{
		ID: "r1", Name: "oci-hosted", Format: domain.FormatOCI, Type: domain.TypeHosted,
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/browse/repositories/oci-hosted/docker-tree", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "an oci repository must have a browse tree")
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "oci", body["format"])
}

// A component stored in an oci repository must reach the tree, not just the
// format gate: the browse rows are the tree's only data source.
func TestDockerTree_OCIFormatRepo_ComponentAppearsInTree(t *testing.T) {
	r, repos, comps, _, _, _ := mountBrowse(t)
	require.NoError(t, repos.Create(context.Background(), &domain.Repository{
		ID: "r1", Name: "oci-hosted", Format: domain.FormatOCI, Type: domain.TypeHosted,
	}))
	comps.DockerRowsByRepo = map[string][]domain.DockerBrowseRow{
		"oci-hosted": {
			{ComponentID: "c1", ImageName: "charts/nginx", Version: "1.2.3", SamplePath: "/manifests/charts/nginx/1.2.3"},
		},
	}

	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/oci-hosted/docker-tree", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

	charts, ok := findChild(got.Root, "charts")
	require.True(t, ok, "the oci component must appear in the browse tree")
	nginx, ok := findChild(charts, "nginx")
	require.True(t, ok)
	tags, ok := findChild(nginx, "Tags")
	require.True(t, ok)
	leaf, ok := findChild(tags, "1.2.3")
	require.True(t, ok)
	assert.Equal(t, "c1", leaf.ComponentID)
}
