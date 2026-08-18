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
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// buildCatalogRouter serves /v2/_catalog over two docker repos (one public,
// one private), an npm repo (never listed), and an offline docker repo
// (never listed). identity, when non-nil, plays the role OptionalAuth fills
// in production.
func buildCatalogRouter(t *testing.T, identity gin.HandlerFunc) *gin.Engine {
	t.Helper()
	repoRepo := testutil.NewRepoRepo(
		&domain.Repository{ID: "r1", Name: "pub", Format: domain.FormatDocker, Type: domain.TypeHosted, Online: true, AllowAnonymous: true},
		&domain.Repository{ID: "r2", Name: "priv", Format: domain.FormatOCI, Type: domain.TypeHosted, Online: true},
		&domain.Repository{ID: "r3", Name: "npm-hosted", Format: domain.FormatNPM, Type: domain.TypeHosted, Online: true, AllowAnonymous: true},
		&domain.Repository{ID: "r4", Name: "parked", Format: domain.FormatDocker, Type: domain.TypeHosted, Online: false, AllowAnonymous: true},
	)
	assets := testutil.NewAssetRepo()
	ctx := context.Background()
	for _, a := range []*domain.Asset{
		{Repository: "pub", Path: "/manifests/web/app/latest"},
		{Repository: "pub", Path: "/manifests/web/app/v2"}, // same image, second tag — no duplicate entry
		{Repository: "pub", Path: "/blobs/sha256:aa"},      // blobs name no image
		{Repository: "priv", Path: "/manifests/secret/img/latest"},
		{Repository: "parked", Path: "/manifests/gone/latest"},
	} {
		require.NoError(t, assets.Create(ctx, a))
	}
	rbacSvc := service.NewRBACService(&noPrivilegesRBACRepo{}, repoRepo, zap.NewNop().Sugar(), true)

	r := gin.New()
	hs := []gin.HandlerFunc{}
	if identity != nil {
		hs = append(hs, identity)
	}
	hs = append(hs, handlers.DockerCatalog(repoRepo, rbacSvc, assets))
	r.GET("/v2/_catalog", hs...)
	return r
}

func catalogGet(t *testing.T, r *gin.Engine, url string) (int, []string, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	var body struct {
		Repositories []string `json:"repositories"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return w.Code, body.Repositories, w.Header().Get("Link")
}

func TestDockerCatalog_AnonymousSeesOnlyPublicRegistries(t *testing.T) {
	r := buildCatalogRouter(t, nil)

	code, got, _ := catalogGet(t, r, "/v2/_catalog")
	require.Equal(t, http.StatusOK, code)
	// pub/web/app once (two tags, one image); nothing from the private, npm,
	// or offline repos — a catalog probe must not leak their contents.
	assert.Equal(t, []string{"pub/web/app"}, got)
}

func TestDockerCatalog_AdminSeesEverythingOnline(t *testing.T) {
	admin := func(c *gin.Context) {
		c.Set("userID", "u-admin")
		c.Set("roles", []string{"nx-admin"})
	}
	r := buildCatalogRouter(t, gin.HandlerFunc(admin))

	code, got, _ := catalogGet(t, r, "/v2/_catalog")
	require.Equal(t, http.StatusOK, code)
	// Sorted, repository-prefixed — each entry is a pullable reference.
	// The offline repo stays out even for an admin.
	assert.Equal(t, []string{"priv/secret/img", "pub/web/app"}, got)
}

func TestDockerCatalog_Pagination(t *testing.T) {
	admin := func(c *gin.Context) {
		c.Set("userID", "u-admin")
		c.Set("roles", []string{"nx-admin"})
	}
	r := buildCatalogRouter(t, gin.HandlerFunc(admin))

	code, first, link := catalogGet(t, r, "/v2/_catalog?n=1")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, []string{"priv/secret/img"}, first)
	require.Contains(t, link, `rel="next"`)
	require.Contains(t, link, "last=")

	code, second, link2 := catalogGet(t, r, "/v2/_catalog?n=1&last=priv/secret/img")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, []string{"pub/web/app"}, second)
	assert.Empty(t, link2, "the final page carries no next link")
}

func TestDockerCatalog_EmptyIsAListNotNull(t *testing.T) {
	repoRepo := testutil.NewRepoRepo()
	rbacSvc := service.NewRBACService(&noPrivilegesRBACRepo{}, repoRepo, zap.NewNop().Sugar(), true)
	r := gin.New()
	r.GET("/v2/_catalog", handlers.DockerCatalog(repoRepo, rbacSvc, testutil.NewAssetRepo()))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v2/_catalog", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"repositories":[]`)
}
