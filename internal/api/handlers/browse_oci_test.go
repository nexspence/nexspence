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
