package oci_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// The API publishes every asset's downloadUrl as baseURL + /repository/<repo> +
// asset.Path, and an OCI asset's stored path is /blobs/<image>/<digest> — no
// /v2/ prefix. Serving that shape makes the advertised download URL work
// (issue #205).

func TestDocker_AssetPathBlobDownload_ServesTheBlob(t *testing.T) {
	repo := testutil.SimpleRepo("apreg1", "docker")
	r := setup(repo)

	content := "layer data"
	dgst := pushBlob(t, r, "apreg1", "library/alpine", content)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/repository/apreg1/blobs/library/alpine/%s", dgst), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, content, w.Body.String())
	assert.Equal(t, dgst, w.Header().Get("Docker-Content-Digest"))
}

func TestDocker_AssetPathManifestDownload_ServesTheManifest(t *testing.T) {
	repo := testutil.SimpleRepo("apreg2", "docker")
	r := setup(repo)

	manifest := `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}`
	req := httptest.NewRequest(http.MethodPut,
		"/repository/apreg2/v2/library/ubuntu/manifests/latest",
		strings.NewReader(manifest))
	req.Header.Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
	req.ContentLength = int64(len(manifest))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	req2 := httptest.NewRequest(http.MethodGet,
		"/repository/apreg2/manifests/library/ubuntu/latest", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, manifest, w2.Body.String())
}

// setupWithAssets mirrors setup() but exposes the asset table, so a test can
// drive the exact URL the API advertises for a stored asset instead of a
// hand-written copy of it.
func setupWithAssets(repo *domain.Repository) (*gin.Engine, *testutil.AssetRepo) {
	assets := testutil.NewAssetRepo()
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     assets,
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := oci.New(d)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := requestctx.WithUser(c.Request.Context(), "test-user-id", "testuser")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, assets
}

func TestDocker_AdvertisedDownloadURL_ServesEveryStoredAsset(t *testing.T) {
	repo := testutil.SimpleRepo("apreg3", "docker")
	r, assets := setupWithAssets(repo)

	pushBlob(t, r, "apreg3", "library/alpine", "layer data")
	manifest := `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}`
	req := httptest.NewRequest(http.MethodPut,
		"/repository/apreg3/v2/library/alpine/manifests/latest", strings.NewReader(manifest))
	req.Header.Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
	req.ContentLength = int64(len(manifest))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	stored := assets.Snapshot()
	require.NotEmpty(t, stored, "the push stored assets to serve")

	for _, a := range stored {
		// The path half of downloadUrl, built exactly as the components API
		// builds it: baseURL + "/repository/" + repository + asset.Path.
		url := "/repository/apreg3" + a.Path
		req := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "advertised download URL %s must serve the asset", url)
		assert.NotEmpty(t, w.Body.String(), "%s served an empty body", url)
	}
}

// The asset-path shape must not open a second door to the upload endpoints:
// only /v2/<image>/blobs/uploads/ starts an upload.
func TestDocker_AssetPathUploadShape_IsNotAnUploadEndpoint(t *testing.T) {
	repo := testutil.SimpleRepo("apreg4", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPost, "/repository/apreg4/blobs/uploads/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.NotEqual(t, http.StatusAccepted, w.Code)
	assert.Empty(t, w.Header().Get("Location"))
}

// A path that is neither a /v2/ request nor a stored asset path is still a 404.
func TestDocker_NonV2UnknownPath_StillNotFound(t *testing.T) {
	repo := testutil.SimpleRepo("apreg5", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/apreg5/some/other/path", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
