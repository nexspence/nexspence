package terraform_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/terraform"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestTerraform_ServiceDiscovery(t *testing.T) {
	r := setup(hostedRepo("tf-hosted"))
	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-hosted/.well-known/terraform.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Contains(t, body["providers.v1"], "/repository/tf-hosted/v1/providers/")
	assert.Contains(t, body["modules.v1"], "/repository/tf-hosted/v1/modules/")
}

func TestTerraform_Proxy_ProviderVersions(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/providers/hashicorp/aws/versions" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"versions": []map[string]any{
					{
						"version":   "5.0.0",
						"protocols": []string{"5.0"},
						"platforms": []map[string]any{{"os": "linux", "arch": "amd64"}},
					},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	r := setup(proxyRepo("tf-proxy", upstream.URL))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-proxy/v1/providers/hashicorp/aws/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	versions, ok := body["versions"].([]any)
	require.True(t, ok)
	assert.Len(t, versions, 1)
}
