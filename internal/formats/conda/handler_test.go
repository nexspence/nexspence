package conda_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/conda"
	"github.com/nexspence-oss/nexspence/internal/testutil"
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

func TestConda_UploadAndDownload(t *testing.T) {
	r := setup(hostedRepo("conda-hosted"))

	body := []byte("fake-tar-bz2-content") // not a real package, but tests the HTTP flow
	req := httptest.NewRequest(http.MethodPut,
		"/repository/conda-hosted/linux-64/numpy-1.24.0-py311h_0.tar.bz2",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-tar")
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Download it back
	req2 := httptest.NewRequest(http.MethodGet,
		"/repository/conda-hosted/linux-64/numpy-1.24.0-py311h_0.tar.bz2", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	got, err := io.ReadAll(w2.Body)
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

func TestConda_IndexEmpty(t *testing.T) {
	r := setup(hostedRepo("conda-hosted"))
	req := httptest.NewRequest(http.MethodGet, "/repository/conda-hosted/linux-64/repodata.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	info, ok := body["info"].(map[string]any)
	require.True(t, ok, "info must be a map")
	assert.Equal(t, "linux-64", info["subdir"])
	assert.NotNil(t, body["packages"])
	assert.NotNil(t, body["packages.conda"])
}

func TestConda_Proxy_RepoData(t *testing.T) {
	var upstreamURL string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/linux-64/repodata.json" {
			w.Header().Set("Content-Type", "application/json")
			body := map[string]any{
				"info": map[string]any{"subdir": "linux-64"},
				"packages": map[string]any{
					"numpy-1.24.0-py311_0.tar.bz2": map[string]any{
						"name":    "numpy",
						"version": "1.24.0",
						"url":     upstreamURL + "/linux-64/numpy-1.24.0-py311_0.tar.bz2",
					},
				},
				"packages.conda": map[string]any{},
			}
			json.NewEncoder(w).Encode(body) //nolint:errcheck
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	upstreamURL = upstream.URL // set after server starts (closure captures the var)

	proxyRepo := &domain.Repository{
		ID:     "proxy-1",
		Name:   "conda-proxy",
		Format: "conda",
		Type:   domain.TypeProxy,
		Online: true,
		ProxyConfig: map[string]any{
			"remote_url": upstream.URL,
		},
	}

	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(proxyRepo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := conda.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	req := httptest.NewRequest(http.MethodGet, "/repository/conda-proxy/linux-64/repodata.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))

	pkgs, ok := body["packages"].(map[string]any)
	require.True(t, ok, "packages must be a map")
	entry, ok := pkgs["numpy-1.24.0-py311_0.tar.bz2"].(map[string]any)
	require.True(t, ok, "numpy entry must be present")
	url, ok := entry["url"].(string)
	require.True(t, ok, "url field must be a string")
	// URL must be rewritten to point through our proxy
	assert.True(t, strings.HasPrefix(url, "http://localhost:8080/repository/conda-proxy/linux-64/"),
		"expected local proxy URL, got: %s", url)
}
