package nuget_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/nuget"
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
	h := nuget.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func pushNupkg(r *gin.Engine, repoName, filename, content string) int {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("package", filename)
	_, _ = part.Write([]byte(content))
	w.Close()

	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+"/v2/package", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	wr := httptest.NewRecorder()
	r.ServeHTTP(wr, req)
	return wr.Code
}

func TestNuGet_ServiceIndex(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs", "nuget")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/pkgs/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "PackageBaseAddress")
	assert.Contains(t, w.Body.String(), "3.0.0")
}

func TestNuGet_PushAndDownload(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs2", "nuget")
	r := setup(repo)

	// filename = id.version.nupkg — handler splits at last dot, so use single-segment version
	require.Equal(t, http.StatusCreated, pushNupkg(r, "pkgs2", "mylib.1.nupkg", "nupkg-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pkgs2/v3/flatcontainer/mylib/1/mylib.1.nupkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "nupkg-bytes", w.Body.String())
}

func TestNuGet_VersionList_Empty(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs3", "nuget")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pkgs3/v3/flatcontainer/nonexistent/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"versions"`)
}

func TestNuGet_VersionList_AfterPush(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs4", "nuget")
	r := setup(repo)

	// serilog.311.nupkg → id=serilog, version=311 (single-segment to avoid last-dot splitting ambiguity)
	require.Equal(t, http.StatusCreated, pushNupkg(r, "pkgs4", "serilog.311.nupkg", "serilog-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pkgs4/v3/flatcontainer/serilog/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "311")
}

func TestNuGet_Registration(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs5", "nuget")
	r := setup(repo)

	// newtonsoft.json.1301.nupkg → id=newtonsoft.json, version=1301
	require.Equal(t, http.StatusCreated, pushNupkg(r, "pkgs5", "newtonsoft.json.1301.nupkg", "nj-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pkgs5/v3/registration/newtonsoft.json/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "newtonsoft.json")
	assert.Contains(t, w.Body.String(), "1301")
}

func TestNuGet_FindPackagesById(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs6", "nuget")
	r := setup(repo)

	// castle.core.5.nupkg → id=castle.core, version=5
	require.Equal(t, http.StatusCreated, pushNupkg(r, "pkgs6", "castle.core.5.nupkg", "castle-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pkgs6/FindPackagesById()?id='Castle.Core'", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "castle.core")
}

func TestNuGet_Delete(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs7", "nuget")
	r := setup(repo)

	require.Equal(t, http.StatusCreated, pushNupkg(r, "pkgs7", "autofac.7.nupkg", "autofac-bytes"))

	req := httptest.NewRequest(http.MethodDelete,
		"/repository/pkgs7/v2/packages/autofac/7", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestNuGet_ProxyRejectMutation(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs8", "nuget")
	repo.Type = domain.TypeProxy

	var buf bytes.Buffer
	w2 := multipart.NewWriter(&buf)
	part, _ := w2.CreateFormFile("package", "x.1.0.nupkg")
	_, _ = part.Write([]byte("x"))
	w2.Close()

	r := setup(repo)
	req := httptest.NewRequest(http.MethodPut, "/repository/pkgs8/v2/package",
		strings.NewReader(buf.String()))
	req.Header.Set("Content-Type", w2.FormDataContentType())
	wr := httptest.NewRecorder()
	r.ServeHTTP(wr, req)
	assert.Equal(t, http.StatusMethodNotAllowed, wr.Code)
}

func TestNuGet_Download_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs9", "nuget")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pkgs9/v3/flatcontainer/missing/0.0.1/missing.0.0.1.nupkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestNuGet_ProxyServiceIndex_RewritesURLs(t *testing.T) {
	// Mock upstream NuGet v3 service index
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
  "version": "3.0.0",
  "resources": [
    {"@id": "https://api.nuget.org/v3/flatcontainer/", "@type": "PackageBaseAddress/3.0.0"},
    {"@id": "https://api.nuget.org/v3/registration5-gz-semver2/", "@type": "RegistrationsBaseUrl/3.6.0"}
  ]
}`)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "rp3", Name: "nuget-proxy", Format: "nuget",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo) // BaseURL: "http://localhost:8080"

	req := httptest.NewRequest(http.MethodGet, "/repository/nuget-proxy/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "http://localhost:8080/repository/nuget-proxy/v3/flatcontainer/",
		"PackageBaseAddress @id should be rewritten")
	assert.Contains(t, body, "http://localhost:8080/repository/nuget-proxy/v3/registration5-gz-semver2/",
		"RegistrationsBaseUrl @id should be rewritten")
	assert.NotContains(t, body, "api.nuget.org",
		"upstream host must not appear in rewritten index")
}

func TestNuGet_ProxyServiceIndex_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "rp4", Name: "nuget-proxy2", Format: "nuget",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/nuget-proxy2/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
}
