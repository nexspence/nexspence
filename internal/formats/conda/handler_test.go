package conda_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/conda"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
)

func init() { gin.SetMode(gin.TestMode) }

func hostedRepo(name string) *domain.Repository {
	return &domain.Repository{
		ID:     "repo-id-1",
		Name:   name,
		Format: "conda",
		Type:   domain.TypeHosted,
		Online: true,
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
	h := conda.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func TestConda_BadPath(t *testing.T) {
	r := setup(hostedRepo("conda-hosted"))
	req := httptest.NewRequest(http.MethodGet, "/repository/conda-hosted/no-slash", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestConda_Bz2Returns404(t *testing.T) {
	r := setup(hostedRepo("conda-hosted"))
	req := httptest.NewRequest(http.MethodGet, "/repository/conda-hosted/linux-64/repodata.json.bz2", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
