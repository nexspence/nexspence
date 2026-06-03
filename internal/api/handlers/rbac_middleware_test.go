package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// noPrivilegesRBACRepo returns empty privileges so only AllowAnonymous + nx-admin
// bypass can grant access. Used to exercise the denyAccess path.
type noPrivilegesRBACRepo struct{}

func (n *noPrivilegesRBACRepo) GetUserPrivilegesWithSelectors(_ context.Context, _ string) ([]repository.PrivilegeWithSelector, error) {
	return nil, nil //nolint:nilnil // (nil, nil) signals not-found; callers check the returned value
}

func TestRBACMiddleware_UnauthenticatedPrivateDocker_OCIErrorBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	privateRepo := &domain.Repository{
		ID:             "r1",
		Name:           "private-docker",
		Format:         domain.FormatDocker,
		Type:           domain.TypeHosted,
		Online:         true,
		AllowAnonymous: false,
	}
	repoRepo := testutil.NewRepoRepo(privateRepo)
	rbacSvc := service.NewRBACService(&noPrivilegesRBACRepo{}, repoRepo, zap.NewNop().Sugar())

	r := gin.New()
	v2 := r.Group("/v2", handlers.RBACMiddleware(rbacSvc, repoRepo))
	v2.Any("/:repoName/*dockerpath", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/v2/private-docker/manifests/latest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Header().Get("WWW-Authenticate"), "Basic")
	assert.Equal(t, "registry/2.0", w.Header().Get("Docker-Distribution-API-Version"))

	var body struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Detail  string `json:"detail"`
		} `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Errors, 1)
	assert.Equal(t, "UNAUTHORIZED", body.Errors[0].Code)
	assert.Contains(t, body.Errors[0].Message, "private-docker",
		"error message must include the repository name so Docker CLI surfaces it")
}

func TestRBACMiddleware_UnauthenticatedNonDocker_UsesGenericBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	privateRepo := &domain.Repository{
		ID:             "r1",
		Name:           "private-maven",
		Format:         domain.FormatMaven2,
		Type:           domain.TypeHosted,
		Online:         true,
		AllowAnonymous: false,
	}
	repoRepo := testutil.NewRepoRepo(privateRepo)
	rbacSvc := service.NewRBACService(&noPrivilegesRBACRepo{}, repoRepo, zap.NewNop().Sugar())

	r := gin.New()
	rg := r.Group("/repository", handlers.RBACMiddleware(rbacSvc, repoRepo))
	rg.Any("/:repoName/*path", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/repository/private-maven/foo/bar.jar", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	// Non-Docker path keeps the legacy {"error":"..."} shape — no OCI errors[] array.
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "authentication required", body["error"])
	_, hasErrors := body["errors"]
	assert.False(t, hasErrors, "non-/v2/ path must not emit OCI errors[] body")
}
