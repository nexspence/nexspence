package helm_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/helm"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func init() { gin.SetMode(gin.TestMode) }

func setup(repo *domain.Repository) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := helm.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// uploadMultipart uploads a chart via multipart/form-data (helm push-compatible).
func uploadMultipart(r *gin.Engine, repoName, filename, content string) int {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("chart", filename)
	_, _ = part.Write([]byte(content))
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/repository/"+repoName+"/api/charts", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	wr := httptest.NewRecorder()
	r.ServeHTTP(wr, req)
	return wr.Code
}

// uploadRaw uploads a chart via raw body with X-Chart-Name header.
func uploadRaw(r *gin.Engine, repoName, filename, content string) int {
	req := httptest.NewRequest(http.MethodPost, "/repository/"+repoName+"/api/charts",
		strings.NewReader(content))
	req.Header.Set("X-Chart-Name", filename)
	req.Header.Set("Content-Type", "application/x-tar")
	req.ContentLength = int64(len(content))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestHelm_UploadAndDownload_Multipart(t *testing.T) {
	repo := testutil.SimpleRepo("charts", "helm")
	r := setup(repo)

	require.Equal(t, http.StatusCreated, uploadMultipart(r, "charts", "nginx-1.0.0.tgz", "chart-bytes"))

	req := httptest.NewRequest(http.MethodGet, "/repository/charts/nginx-1.0.0.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "chart-bytes", w.Body.String())
}

func TestHelm_UploadAndDownload_RawBody(t *testing.T) {
	repo := testutil.SimpleRepo("charts2", "helm")
	r := setup(repo)

	require.Equal(t, http.StatusCreated, uploadRaw(r, "charts2", "redis-2.1.0.tgz", "redis-chart"))

	req := httptest.NewRequest(http.MethodGet, "/repository/charts2/redis-2.1.0.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "redis-chart", w.Body.String())
}

func TestHelm_IndexYaml_Empty(t *testing.T) {
	repo := testutil.SimpleRepo("charts3", "helm")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/charts3/index.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "apiVersion: v1")
}

func TestHelm_IndexYaml_ListsChart(t *testing.T) {
	repo := testutil.SimpleRepo("charts4", "helm")
	r := setup(repo)

	require.Equal(t, http.StatusCreated, uploadMultipart(r, "charts4", "wordpress-3.0.0.tgz", "wp-chart"))

	req := httptest.NewRequest(http.MethodGet, "/repository/charts4/index.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "wordpress")
}

func TestHelm_Delete(t *testing.T) {
	repo := testutil.SimpleRepo("charts5", "helm")
	r := setup(repo)

	require.Equal(t, http.StatusCreated, uploadMultipart(r, "charts5", "app-1.0.0.tgz", "app"))

	req := httptest.NewRequest(http.MethodDelete, "/repository/charts5/api/charts/app/1.0.0", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHelm_GetNotFound(t *testing.T) {
	repo := testutil.SimpleRepo("charts6", "helm")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/charts6/missing-1.0.0.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHelm_ProxyIndexYaml_RewritesURLs(t *testing.T) {
	// Mock upstream helm repository
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		fmt.Fprint(w, `apiVersion: v1
entries:
  nginx:
  - name: nginx
    version: "15.0.0"
    urls:
    - https://charts.bitnami.com/bitnami/nginx-15.0.0.tgz
generated: "2024-01-01T00:00:00Z"
`)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "rp", Name: "helm-proxy", Format: "helm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo) // uses BaseURL: "http://localhost:8080"

	req := httptest.NewRequest(http.MethodGet, "/repository/helm-proxy/index.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "http://localhost:8080/repository/helm-proxy/nginx-15.0.0.tgz",
		"chart URL should be rewritten to local proxy")
	assert.NotContains(t, body, "charts.bitnami.com",
		"upstream URL must not appear in rewritten index")
}

func TestHelm_ProxyIndexYaml_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusGone)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "rp2", Name: "helm-proxy2", Format: "helm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/helm-proxy2/index.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
}
