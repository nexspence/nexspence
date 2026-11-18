package nuget_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/nuget"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// setupWithAssets is setup() with the asset repo exposed, so a test can read
// back the path a push stored the package under.
func setupWithAssets(repo *domain.Repository, assets repository.AssetRepo) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     assets,
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := nuget.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// A NuGet package is stored under the flat-container path without its
// prefix ("<id>/<version>/<id>.<version>.nupkg"), but the product hands that
// stored path out as the download link — the WebUI's per-asset Download
// button (repositoryPath(repo, asset.path)) and the Nexus-compatible asset
// downloadUrl (BaseURL + "/repository/" + repo + asset.path). Those links
// have to be served, or every download from the UI fails with 405.
func TestNuGet_HostedDownloadByStoredPath(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs-dl", "nuget")
	assets := testutil.NewAssetRepo()
	r := setupWithAssets(repo, assets)

	require.Equal(t, http.StatusCreated, pushNupkg(r, "pkgs-dl", "mylib.1.0.0.nupkg", "nupkg-bytes"))

	// The stored path is what the product links to.
	rows, err := assets.SearchAssets(context.Background(), domain.SearchParams{Repository: "pkgs-dl"})
	require.NoError(t, err)
	require.Len(t, rows.Items, 1)
	stored := rows.Items[0].Path
	require.Equal(t, "/mylib/1.0.0/mylib.1.0.0.nupkg", stored)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/pkgs-dl"+stored, nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "nupkg-bytes", w.Body.String())
}

// The same package, addressed by its coordinates rather than by its stored
// spelling — the id as the user typed it, in any case.
func TestNuGet_HostedDownloadByCoordinates(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs-coord", "nuget")
	r := setup(repo)

	require.Equal(t, http.StatusCreated, pushNupkg(r, "pkgs-coord", "Edt.Core.Dal.1.0.0.nupkg", "nupkg-bytes"))

	for _, p := range []string{
		"/Edt.Core.Dal/1.0.0",
		"/edt.core.dal/1.0.0",
		"/Edt.Core.Dal/1.0.0/Edt.Core.Dal.1.0.0.nupkg",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/pkgs-coord"+p, nil))
		assert.Equal(t, http.StatusOK, w.Code, "path %s", p)
		assert.Equal(t, "nupkg-bytes", w.Body.String(), "path %s", p)
	}
}

// A HEAD on the stored path answers with the size and no body.
func TestNuGet_HostedDownloadHead(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs-head", "nuget")
	r := setup(repo)

	require.Equal(t, http.StatusCreated, pushNupkg(r, "pkgs-head", "mylib.1.0.0.nupkg", "nupkg-bytes"))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/repository/pkgs-head/mylib/1.0.0/mylib.1.0.0.nupkg", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "11", w.Header().Get("Content-Length"))
	assert.Empty(t, w.Body.String())
}

// A package that is not there is a 404, not a 405: the route exists, the
// package does not.
func TestNuGet_HostedDownloadUnknownPackage(t *testing.T) {
	repo := testutil.SimpleRepo("pkgs-404", "nuget")
	r := setup(repo)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/pkgs-404/nosuch/1.0.0/nosuch.1.0.0.nupkg", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}
