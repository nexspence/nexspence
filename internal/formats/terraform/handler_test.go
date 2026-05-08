package terraform_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/terraform"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
)

func init() { gin.SetMode(gin.TestMode) }

func hostedRepo(name string) *domain.Repository {
	return &domain.Repository{
		ID:     "tf-1",
		Name:   name,
		Format: "terraform",
		Type:   domain.TypeHosted,
		Online: true,
	}
}

func proxyRepo(name, upstream string) *domain.Repository {
	return &domain.Repository{
		ID:     "tf-2",
		Name:   name,
		Format: "terraform",
		Type:   domain.TypeProxy,
		Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream},
	}
}

func setup(repo *domain.Repository) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := terraform.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func TestTerraform_UnknownEndpoint(t *testing.T) {
	r := setup(hostedRepo("tf-hosted"))
	req := httptest.NewRequest(http.MethodGet, "/repository/tf-hosted/v2/something", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
