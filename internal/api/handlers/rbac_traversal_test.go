package handlers_test

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
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

type fixedPrivilegesRBACRepo struct {
	privs []repository.PrivilegeWithSelector
}

func (f fixedPrivilegesRBACRepo) GetUserPrivilegesWithSelectors(context.Context, string) ([]repository.PrivilegeWithSelector, error) {
	return f.privs, nil
}

// teamARouter serves raw-priv to a user whose only privilege is the
// /team-a/ prefix, on both the repository and the /v2 docker-style routes.
func teamARouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	repo := &domain.Repository{ID: "r1", Name: "raw-priv", Format: domain.FormatRaw, Type: domain.TypeHosted, Online: true}
	repoRepo := testutil.NewRepoRepo(repo)
	rbac := service.NewRBACService(fixedPrivilegesRBACRepo{privs: []repository.PrivilegeWithSelector{{
		Actions:    []string{"read", "browse", "write", "delete"},
		Expression: `repository == "raw-priv" && path.startsWith("/team-a/")`,
	}}}, repoRepo, zap.NewNop().Sugar(), true)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "bob")
		c.Set("roles", []string{"dev"})
		c.Next()
	})
	r.Group("/repository/:repoName", handlers.RBACMiddleware(rbac, repoRepo)).
		Any("/*path", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.Group("/v2/:repoName", handlers.RBACMiddleware(rbac, repoRepo)).
		Any("/*dockerpath", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

// GHSA-jcgv-hchv-g397: the middleware judged the raw path, the handler served
// the cleaned one. A ".." segment, in any spelling, is refused before the
// selector is evaluated.
func TestRBACMiddleware_RefusesTraversalSegment(t *testing.T) {
	r := teamARouter()
	for _, prefix := range []string{"/repository/raw-priv", "/v2/raw-priv"} {
		for _, dots := range []string{"..", "%2e%2e", "%2E%2E", ".%2e"} {
			for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete} {
				target := prefix + "/team-a/" + dots + "/team-b/secret.txt"
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(method, target, nil))
				assert.Equal(t, http.StatusBadRequest, w.Code, "%s %s", method, target)
			}
		}
	}
}

// The selector itself still works, and dots inside a name are not a segment.
func TestRBACMiddleware_PathScopeStillApplies(t *testing.T) {
	r := teamARouter()
	for target, want := range map[string]int{
		"/repository/raw-priv/team-a/ok.txt":      http.StatusOK,
		"/repository/raw-priv/team-a/we..ird.txt": http.StatusOK,
		"/repository/raw-priv/team-b/secret.txt":  http.StatusForbidden,
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		assert.Equal(t, want, w.Code, target)
	}
}
