package npm_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/npm"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	h := npm.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// publishBody builds the npm publish JSON payload with a single attachment.
func publishBody(pkgName, version, tgzContent string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(tgzContent))
	filename := pkgName + "-" + version + ".tgz"
	body := map[string]any{
		"name":      pkgName,
		"dist-tags": map[string]string{"latest": version},
		"versions": map[string]any{
			version: map[string]any{"name": pkgName, "version": version},
		},
		"_attachments": map[string]any{
			filename: map[string]any{
				"data":         encoded,
				"content_type": "application/octet-stream",
				"length":       len(tgzContent),
			},
		},
	}
	b, _ := json.Marshal(body)
	return string(b)
}

func TestNPM_Ping(t *testing.T) {
	repo := testutil.SimpleRepo("npm-repo", "npm")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/npm-repo/-/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok"`)
}

func TestNPM_PublishAndDownload(t *testing.T) {
	repo := testutil.SimpleRepo("npm-hosted", "npm")
	r := setup(repo)

	body := publishBody("mylib", "1.2.3", "fake-tgz-content")
	req := httptest.NewRequest(http.MethodPut, "/repository/npm-hosted/mylib",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Download tarball
	req2 := httptest.NewRequest(http.MethodGet, "/repository/npm-hosted/mylib/-/mylib-1.2.3.tgz", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "fake-tgz-content", w2.Body.String())
}

func TestNPM_Metadata_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("npm-empty", "npm")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/npm-empty/nonexistent-pkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestNPM_Metadata_ListsVersions(t *testing.T) {
	repo := testutil.SimpleRepo("npm-meta", "npm")
	r := setup(repo)

	// Publish two versions
	for _, ver := range []string{"1.0.0", "2.0.0"} {
		body := publishBody("coolpkg", ver, "bytes-"+ver)
		req := httptest.NewRequest(http.MethodPut, "/repository/npm-meta/coolpkg",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(httptest.NewRecorder(), req)
	}

	req := httptest.NewRequest(http.MethodGet, "/repository/npm-meta/coolpkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "coolpkg")
}

func TestNPM_Publish_MissingAttachments_Returns400(t *testing.T) {
	repo := testutil.SimpleRepo("npm-bad", "npm")
	r := setup(repo)

	body := `{"name":"pkg","dist-tags":{"latest":"1.0.0"}}`
	req := httptest.NewRequest(http.MethodPut, "/repository/npm-bad/pkg",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestNPM_ProxyPUT_Rejected(t *testing.T) {
	repo := &domain.Repository{
		ID: "npm-p", Name: "npm-proxy", Format: "npm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": "https://registry.npmjs.org"},
	}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPut, "/repository/npm-proxy/pkg",
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
