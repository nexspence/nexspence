package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// An oci-format repository speaks the same /v2/ protocol as a docker one and
// must be dispatched to the registry handler, not rejected as "not a registry".
func TestServeDockerV2_OCIFormatRepo_IsDispatched(t *testing.T) {
	repo := &domain.Repository{
		Name: "oci-hosted", Format: domain.FormatOCI, Type: domain.TypeHosted,
		Online: true, AllowAnonymous: true,
	}
	r := buildDockerRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/v2/oci-hosted/charts/nginx/tags/list", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "an oci repository must reach the registry handler")
}

func TestServeDockerV2_NonRegistryFormatRepo_IsRejected(t *testing.T) {
	repo := &domain.Repository{
		Name: "maven-hosted", Format: domain.FormatMaven2, Type: domain.TypeHosted,
		Online: true, AllowAnonymous: true,
	}
	r := buildDockerRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/v2/maven-hosted/some/tags/list", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
