package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// allowAllRBACRepo returns no privileges from the DB.
// Access in these tests is granted via AllowAnonymous=true on the test repos,
// not via privilege matching (which would fail for an authenticated user with empty privileges).
type allowAllRBACRepo struct{}

func (a *allowAllRBACRepo) GetUserPrivilegesWithSelectors(_ context.Context, _ string) ([]repository.PrivilegeWithSelector, error) {
	return nil, nil //nolint:nilnil // (nil, nil) signals not-found; callers check the returned value
}

// stubDockerHandler responds 200 to any request — represents a working Docker format handler.
type stubDockerHandler struct{}

func (s *stubDockerHandler) Name() string             { return "docker" }
func (s *stubDockerHandler) ServeHTTP(c *gin.Context) { c.Status(http.StatusOK) }

func buildDockerRouter(repos ...*domain.Repository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	repoRepo := testutil.NewRepoRepo(repos...)
	rbacRepo := &allowAllRBACRepo{}
	rbacSvc := service.NewRBACService(rbacRepo, repoRepo, zap.NewNop().Sugar())

	stub := &stubDockerHandler{}
	fmtRegistry := map[string]formats.FormatHandler{"docker": stub}

	dockerV2H := serveDockerV2(repoRepo, stub, fmtRegistry)

	// OptionalAuth is intentionally omitted from this test router.
	// These tests cover routing behavior and RBAC access control, not authentication.
	// All repos use AllowAnonymous=true so RBAC passes for unauthenticated GET requests —
	// exactly as production does for public repos. Tests of the authenticated path
	// belong in an integration test that starts the full server.

	// v2 discovery — public, no auth needed
	r.GET("/v2/", func(c *gin.Context) {
		c.Header("Docker-Distribution-API-Version", "registry/2.0")
		c.Status(http.StatusOK)
	})

	// Long path: /v2/repository/:repoName/*dockerpath
	v2docker := r.Group("/v2/repository", handlers.RBACMiddleware(rbacSvc, repoRepo))
	v2docker.Any("/:repoName/*dockerpath", dockerV2H)

	// Short path: /v2/:repoName/*dockerpath
	v2short := r.Group("/v2", handlers.RBACMiddleware(rbacSvc, repoRepo))
	v2short.Any("/:repoName/*dockerpath", dockerV2H)

	return r
}

func TestDockerRouting(t *testing.T) {
	dockerRepo := &domain.Repository{
		ID:             "r1",
		Name:           "myrepo",
		Format:         domain.FormatDocker,
		Type:           domain.TypeHosted,
		Online:         true,
		AllowAnonymous: true,
	}
	nonDockerRepo := &domain.Repository{
		ID:             "r2",
		Name:           "mavenrepo",
		Format:         domain.FormatMaven2,
		Type:           domain.TypeHosted,
		Online:         true,
		AllowAnonymous: true,
	}
	offlineRepo := &domain.Repository{
		ID:             "r3",
		Name:           "offlinerepo",
		Format:         domain.FormatDocker,
		Type:           domain.TypeHosted,
		Online:         false,
		AllowAnonymous: true,
	}

	tests := []struct {
		name       string
		path       string
		repos      []*domain.Repository
		wantStatus int
	}{
		// Short-path tests (new in Phase 16)
		{
			name:       "short path: docker repo returns 200",
			path:       "/v2/myrepo/tags/list",
			repos:      []*domain.Repository{dockerRepo},
			wantStatus: http.StatusOK,
		},
		{
			// RBACMiddleware runs before serveDockerV2; unknown repo → deny unauthenticated → 401.
			name:       "short path: unknown repo returns 401 (unauthenticated deny)",
			path:       "/v2/no-such-repo/tags/list",
			repos:      []*domain.Repository{dockerRepo},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "short path: non-docker repo returns 400",
			path:       "/v2/mavenrepo/tags/list",
			repos:      []*domain.Repository{nonDockerRepo},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "short path: offline docker repo returns 503",
			path:       "/v2/offlinerepo/tags/list",
			repos:      []*domain.Repository{offlineRepo},
			wantStatus: http.StatusServiceUnavailable,
		},
		// Long-path tests (backward compat)
		{
			name:       "long path: docker repo returns 200",
			path:       "/v2/repository/myrepo/tags/list",
			repos:      []*domain.Repository{dockerRepo},
			wantStatus: http.StatusOK,
		},
		{
			// RBACMiddleware runs before serveDockerV2; unknown repo → deny unauthenticated → 401.
			name:       "long path: unknown repo returns 401 (unauthenticated deny)",
			path:       "/v2/repository/no-such-repo/tags/list",
			repos:      []*domain.Repository{dockerRepo},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "long path: offline docker repo returns 503",
			path:       "/v2/repository/offlinerepo/tags/list",
			repos:      []*domain.Repository{offlineRepo},
			wantStatus: http.StatusServiceUnavailable,
		},
		// Discovery endpoint
		{
			name:       "GET /v2/ returns 200",
			path:       "/v2/",
			repos:      nil,
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := buildDockerRouter(tt.repos...)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			r.ServeHTTP(w, req)
			assert.Equal(t, tt.wantStatus, w.Code, "path: %s", tt.path)
		})
	}
}
