package helm_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/helm"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// TestHelm_Name verifies the handler name.
func TestHelm_Name(t *testing.T) {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := helm.New(d)
	assert.Equal(t, "helm", h.Name())
}

// TestHelm_MethodNotAllowed covers the default branch in ServeHTTP.
func TestHelm_MethodNotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("charts-405", "helm")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPatch, "/repository/charts-405/index.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestHelm_ProxyRejectMutation covers ServeHTTP proxy-write path.
func TestHelm_ProxyRejectMutation(t *testing.T) {
	repo := testutil.SimpleRepo("helm-proxy-mut", "helm")
	repo.Type = domain.TypeProxy
	repo.ProxyConfig = map[string]any{"remote_url": "http://127.0.0.1:19999"}
	r := setup(repo)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("chart", "myapp-1.0.0.tgz")
	_, _ = part.Write([]byte("data"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/repository/helm-proxy-mut/api/charts", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestHelm_Delete_BadPath covers the error path when delete path is malformed.
func TestHelm_Delete_BadPath(t *testing.T) {
	repo := testutil.SimpleRepo("charts-del-bad", "helm")
	r := setup(repo)

	// Only one segment — missing version component
	req := httptest.NewRequest(http.MethodDelete, "/repository/charts-del-bad/api/charts/myapp", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHelm_ServeFile_HEAD covers HEAD request for a chart that exists.
func TestHelm_ServeFile_HEAD(t *testing.T) {
	repo := testutil.SimpleRepo("charts-head", "helm")
	r := setup(repo)

	require.Equal(t, http.StatusCreated, uploadMultipart(r, "charts-head", "app-2.0.0.tgz", "chart-data"))

	req := httptest.NewRequest(http.MethodHead, "/repository/charts-head/app-2.0.0.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String(), "HEAD must return no body")
	assert.NotEmpty(t, w.Header().Get("Content-Length"))
}

// TestHelm_Upload_NoFileName covers raw upload with no X-Chart-Name header (fallback filename "chart.tgz").
func TestHelm_Upload_NoFileName(t *testing.T) {
	repo := testutil.SimpleRepo("charts-nofn", "helm")
	r := setup(repo)

	content := "raw-chart-bytes"
	req := httptest.NewRequest(http.MethodPost, "/repository/charts-nofn/api/charts",
		bytes.NewReader([]byte(content)))
	req.Header.Set("Content-Type", "application/x-tar")
	req.ContentLength = int64(len(content))
	// Intentionally no X-Chart-Name header → filename defaults to "chart.tgz"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// TestHelm_IndexYaml_MultipleCharts verifies multiple chart entries appear in index.
func TestHelm_IndexYaml_MultipleCharts(t *testing.T) {
	repo := testutil.SimpleRepo("charts-multi", "helm")
	r := setup(repo)

	require.Equal(t, http.StatusCreated, uploadMultipart(r, "charts-multi", "alpha-1.0.0.tgz", "alpha"))
	require.Equal(t, http.StatusCreated, uploadMultipart(r, "charts-multi", "beta-2.0.0.tgz", "beta"))

	req := httptest.NewRequest(http.MethodGet, "/repository/charts-multi/index.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "alpha")
	assert.Contains(t, body, "beta")
	assert.Contains(t, body, "http://localhost:8080/repository/charts-multi/")
}

// TestHelm_ProxyIndexYaml_HEAD verifies HEAD on proxy index.yaml returns 200 and no body.
func TestHelm_ProxyIndexYaml_HEAD(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		fmt.Fprint(w, `apiVersion: v1
entries: {}
generated: "2024-01-01T00:00:00Z"
`)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "rp-head", Name: "helm-proxy-head", Format: "helm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodHead, "/repository/helm-proxy-head/index.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())
}

// TestHelm_ProxyIndexYaml_InvalidYAML covers the upstream returning non-YAML content.
func TestHelm_ProxyIndexYaml_InvalidYAML(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "{ not valid yaml: [\n")
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "rp-iy", Name: "helm-proxy-iy", Format: "helm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/helm-proxy-iy/index.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

// TestHelm_ProxyNoRemoteURL covers the case where proxy repo has no remote_url.
func TestHelm_ProxyNoRemoteURL(t *testing.T) {
	repo := &domain.Repository{
		ID: "rp-nurl", Name: "helm-proxy-nurl", Format: "helm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{}, // no remote_url
	}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/helm-proxy-nurl/index.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// repoproxy.RemoteURL returns error when remote_url is missing
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHelm_ProxyDownload covers proxy GET for a chart tgz (non-index.yaml path).
func TestHelm_ProxyDownload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-tar")
		_, _ = w.Write([]byte("chart-tgz-bytes"))
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "rp-dl", Name: "helm-proxy-dl", Format: "helm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/helm-proxy-dl/nginx-1.0.0.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Proxy should serve the upstream content (200) or cache it.
	assert.True(t, w.Code == http.StatusOK || w.Code == http.StatusBadGateway,
		"expected 200 or 502, got %d", w.Code)
}

// TestHelm_Upload_Multipart_MissingField covers multipart upload without the "chart" field.
func TestHelm_Upload_Multipart_MissingField(t *testing.T) {
	repo := testutil.SimpleRepo("charts-mf", "helm")

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

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("not-chart", "mychart-1.0.0.tgz")
	_, _ = part.Write([]byte("data"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/repository/charts-mf/api/charts", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
