package nuget_test

import (
	"archive/zip"
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
	"github.com/nexspence-oss/nexspence/internal/formats/nuget"
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

// realShapeNuGetUpstream mimics the REAL api.nuget.org (#349): the service
// index lives at /v3/index.json — NOT /index.json — and its resources point at
// /v3-flatcontainer/ (hyphenated, a sibling of /v3/, not nested inside it).
// The old mock invented the nested shape the code's own wrong assumption used,
// which is exactly how the bug survived its test.
func realShapeNuGetUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/index.json":
			fmt.Fprintf(w, `{
  "version": "3.0.0",
  "resources": [
    {"@id": "%s/v3-flatcontainer/", "@type": "PackageBaseAddress/3.0.0"},
    {"@id": "%s/v3/registration5-gz-semver2/", "@type": "RegistrationsBaseUrl/3.6.0"}
  ]
}`, srv.URL, srv.URL)
		case "/v3-flatcontainer/newtonsoft.json/index.json":
			fmt.Fprint(w, `{"versions":["13.0.3"]}`)
		case "/v3-flatcontainer/newtonsoft.json/13.0.3/newtonsoft.json.13.0.3.nupkg":
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(w, "nupkg-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNuGet_ProxyServiceIndex_RewritesURLs(t *testing.T) {
	upstream := realShapeNuGetUpstream(t)

	repo := &domain.Repository{
		ID: "rp3", Name: "nuget-proxy", Format: "nuget",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo) // BaseURL: "http://localhost:8080"

	req := httptest.NewRequest(http.MethodGet, "/repository/nuget-proxy/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, "http://localhost:8080/repository/nuget-proxy/v3-flatcontainer/",
		"PackageBaseAddress @id keeps the real hyphenated upstream path, re-rooted locally")
	assert.Contains(t, body, "http://localhost:8080/repository/nuget-proxy/v3/registration5-gz-semver2/",
		"RegistrationsBaseUrl @id should be rewritten")
	assert.NotContains(t, body, upstream.URL,
		"upstream host must not appear in rewritten index")
}

// The advertised resource paths must actually WORK when the client comes back
// for them — with remote_url as the bare origin, the discovery fetch adds /v3
// itself and the resource paths forward unchanged (#349's NuGet bug: a /v3'd
// remote_url doubled itself onto every resource path and 404'd).
func TestNuGet_ProxyFlatcontainer_ResolvesAgainstRealShape(t *testing.T) {
	upstream := realShapeNuGetUpstream(t)

	repo := &domain.Repository{
		ID: "rp4", Name: "nuget-real", Format: "nuget",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo)

	list := httptest.NewRecorder()
	r.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/repository/nuget-real/v3-flatcontainer/newtonsoft.json/index.json", nil))
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.Contains(t, list.Body.String(), "13.0.3")

	pkg := httptest.NewRecorder()
	r.ServeHTTP(pkg, httptest.NewRequest(http.MethodGet, "/repository/nuget-real/v3-flatcontainer/newtonsoft.json/13.0.3/newtonsoft.json.13.0.3.nupkg", nil))
	require.Equal(t, http.StatusOK, pkg.Code, pkg.Body.String())
	assert.Equal(t, "nupkg-bytes", pkg.Body.String())
}

// Legacy configurations kept remote_url with a /v3 suffix (it was the only way
// the index fetch worked before); they must keep working unchanged.
func TestNuGet_ProxyLegacyV3RemoteURL_StillWorks(t *testing.T) {
	upstream := realShapeNuGetUpstream(t)

	repo := &domain.Repository{
		ID: "rp5", Name: "nuget-legacy", Format: "nuget",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL + "/v3"},
	}
	r := setup(repo)

	idx := httptest.NewRecorder()
	r.ServeHTTP(idx, httptest.NewRequest(http.MethodGet, "/repository/nuget-legacy/index.json", nil))
	require.Equal(t, http.StatusOK, idx.Code, idx.Body.String())

	pkg := httptest.NewRecorder()
	r.ServeHTTP(pkg, httptest.NewRequest(http.MethodGet, "/repository/nuget-legacy/v3-flatcontainer/newtonsoft.json/13.0.3/newtonsoft.json.13.0.3.nupkg", nil))
	require.Equal(t, http.StatusOK, pkg.Code,
		"a /v3-suffixed remote_url must not double itself onto resource paths: %d %s", pkg.Code, pkg.Body.String())
	assert.Equal(t, "nupkg-bytes", pkg.Body.String())
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

func TestNuGet_ServiceIndex_AdvertisesPublish(t *testing.T) {
	// Regression for #97: without a PackagePublish/2.0.0 resource in the
	// service index, `dotnet nuget push` refuses to publish client-side.
	repo := testutil.SimpleRepo("pkgs-pub", "nuget")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/pkgs-pub/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "PackagePublish/2.0.0")
	assert.Contains(t, w.Body.String(), "/repository/pkgs-pub/v2/package")
}

// buildNupkg builds a minimal real .nupkg (zip with a root .nuspec).
func buildNupkg(t *testing.T, id, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create(id + ".nuspec")
	require.NoError(t, err)
	_, err = f.Write([]byte(`<?xml version="1.0"?><package><metadata><id>` +
		id + `</id><version>` + version + `</version></metadata></package>`))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestNuGet_Push_SemverFilename_Heuristic(t *testing.T) {
	// Regression for #100: non-zip body falls back to filename parsing.
	// The version must be the trailing digit-led parts, not just the
	// last dot segment.
	repo := testutil.SimpleRepo("pkgs-semver", "nuget")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		pushNupkg(r, "pkgs-semver", "Newtonsoft.Json.13.0.1.nupkg", "fake-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pkgs-semver/v3/flatcontainer/newtonsoft.json/13.0.1/newtonsoft.json.13.0.1.nupkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code,
		"stored coordinates must be id=newtonsoft.json version=13.0.1")
	assert.Equal(t, "fake-bytes", w.Body.String())
}

func TestNuGet_Push_NuspecAuthoritative(t *testing.T) {
	// A real zip with a .nuspec: manifest id/version win over the filename.
	repo := testutil.SimpleRepo("pkgs-nuspec", "nuget")
	r := setup(repo)

	nupkg := buildNupkg(t, "My.Lib", "2.1.0")
	require.Equal(t, http.StatusCreated,
		pushNupkg(r, "pkgs-nuspec", "whatever.nupkg", string(nupkg)))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pkgs-nuspec/v3/flatcontainer/my.lib/2.1.0/my.lib.2.1.0.nupkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "coordinates must come from the .nuspec")
}
