package nuget_test

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
	"github.com/nexspence-oss/nexspence/internal/formats/nuget"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// TestNuGet_Name verifies the handler name.
func TestNuGet_Name(t *testing.T) {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := nuget.New(d)
	assert.Equal(t, "nuget", h.Name())
}

// TestNuGet_MethodNotAllowed covers the default branch in ServeHTTP.
func TestNuGet_MethodNotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs-405", "nuget")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPatch, "/repository/pkgs-405/v2/package", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestNuGet_Delete_BadPath covers DELETE with only one path segment.
func TestNuGet_Delete_BadPath(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs-del-bad", "nuget")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodDelete, "/repository/pkgs-del-bad/v2/packages/autofac", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestNuGet_Registration_NotFound covers the case where no versions exist.
func TestNuGet_Registration_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs-reg-nf", "nuget")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pkgs-reg-nf/v3/registration/nopackage/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestNuGet_VersionList_Populated verifies version list after push.
func TestNuGet_VersionList_Populated(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs-vl", "nuget")
	r := setup(repo)

	require.Equal(t, http.StatusCreated, pushNupkg(r, "pkgs-vl", "mypackage.200.nupkg", "data"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pkgs-vl/v3/flatcontainer/mypackage/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"versions"`)
}

// TestNuGet_Push_FileFieldFallback uses "file" as the multipart field name.
func TestNuGet_Push_FileFieldFallback(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs-ff", "nuget")
	r := setup(repo)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "moq.5.nupkg")
	_, _ = part.Write([]byte("moq-bytes"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPut, "/repository/pkgs-ff/v2/package", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// TestNuGet_Push_MissingFile covers the case where no recognizable file field is present.
func TestNuGet_Push_MissingFile(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs-nofile", "nuget")
	r := setup(repo)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("other", "x.nupkg")
	_, _ = part.Write([]byte("x"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPut, "/repository/pkgs-nofile/v2/package", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestNuGet_FlatContainer_InvalidPath covers too-few segments in flat container path.
func TestNuGet_FlatContainer_InvalidPath(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs-fc-inv", "nuget")
	r := setup(repo)

	// Only 2 segments after flatcontainer prefix: id/id.ver.nupkg (no version dir)
	req := httptest.NewRequest(http.MethodGet,
		"/repository/pkgs-fc-inv/v3/flatcontainer/pkg/pkg.1.nupkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestNuGet_ProxyServiceIndex_HEAD covers HEAD branch of fetchAndRewriteNuGetIndex.
func TestNuGet_ProxyServiceIndex_HEAD(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"version":"3.0.0","resources":[]}`)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "rp-head", Name: "nuget-proxy-head", Format: "nuget",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodHead, "/repository/nuget-proxy-head/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())
}

// TestNuGet_ProxyServiceIndex_InvalidJSON covers upstream returning non-JSON body.
func TestNuGet_ProxyServiceIndex_InvalidJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "not-json{{{{")
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "rp-ij", Name: "nuget-proxy-ij", Format: "nuget",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/nuget-proxy-ij/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

// TestNuGet_ProxyNoRemoteURL covers proxy with no remote_url configured.
func TestNuGet_ProxyNoRemoteURL(t *testing.T) {
	repo := &domain.Repository{
		ID: "rp-nurl", Name: "nuget-proxy-nurl", Format: "nuget",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{}, // no remote_url
	}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/nuget-proxy-nurl/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestNuGet_FindPackagesById_Empty covers OData query returning no results.
func TestNuGet_FindPackagesById_Empty(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs-fpb-empty", "nuget")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pkgs-fpb-empty/FindPackagesById()?id='NoSuchPackage'", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "feed")
}

// TestNuGet_Push_BadMultipart covers a push where ParseMultipartForm fails.
func TestNuGet_Push_BadMultipart(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs-bad-mp", "nuget")

	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := nuget.New(d)
	eng := gin.New()
	eng.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	// Claim multipart but send garbage body — ParseMultipartForm should fail
	req := httptest.NewRequest(http.MethodPut, "/repository/pkgs-bad-mp/v2/package",
		bytes.NewReader([]byte("not-multipart-body")))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=--boundary")
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, req)
	// Expect 400 (ParseMultipartForm fails) or 400 (missing package file)
	assert.True(t, w.Code == http.StatusBadRequest || w.Code == http.StatusInternalServerError,
		"expected 400 or 500, got %d", w.Code)
}
