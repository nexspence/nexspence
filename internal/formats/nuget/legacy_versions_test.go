package nuget_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/nuget"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Packages pushed before #590 are stored under the version as written in
// their nuspec. They stay listed once, downloadable at the spec's path and
// deletable as one version with the new ones.
func TestNuGet_LegacyVersionCasingsStayReachable(t *testing.T) {
	comps := testutil.NewComponentRepo()
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(testutil.SimpleRepo("legacy", "nuget")),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: comps,
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	r := gin.New()
	h := nuget.New(d)
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	ctx := context.Background()
	for _, v := range []string{"1.0.0-Beta", "1.0.0-BETA"} {
		_, err := base.StoreArtifact(ctx, d, "legacy", "/foo/"+v+"/foo."+v+".nupkg", "application/zip",
			base.Coords{Name: "foo", Version: v}, strings.NewReader("legacy "+v), int64(len("legacy "+v)))
		require.NoError(t, err)
	}

	get := func(p string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/legacy"+p, nil))
		return w
	}
	w := get("/v3/flatcontainer/foo/index.json")
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"versions":["1.0.0-beta"]}`, w.Body.String())

	w = get("/v3/flatcontainer/foo/1.0.0-beta/foo.1.0.0-beta.nupkg")
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, strings.HasPrefix(w.Body.String(), "legacy "))

	// A new push of the same version lands on the normalized path.
	require.Equal(t, http.StatusCreated, pushNupkg(r, "legacy", "Foo.1.0.0-beta.nupkg", "new"))
	assert.JSONEq(t, `{"versions":["1.0.0-beta"]}`, get("/v3/flatcontainer/foo/index.json").Body.String())
	assert.Equal(t, "new", get("/v3/flatcontainer/foo/1.0.0-BETA/foo.1.0.0-BETA.nupkg").Body.String())

	del := httptest.NewRecorder()
	r.ServeHTTP(del, httptest.NewRequest(http.MethodDelete, "/repository/legacy/v2/package/Foo/1.0.0-Beta", nil))
	require.Equal(t, http.StatusNoContent, del.Code)
	for _, v := range []string{"1.0.0-beta", "1.0.0-Beta", "1.0.0-BETA"} {
		_, err := d.Assets.GetByPath(ctx, "legacy", "/foo/"+v+"/foo."+v+".nupkg")
		assert.Error(t, err, "every stored casing of the version is gone: %s", v)
	}
	// The mock does not model orphan removal; the integration test checks the
	// list is empty afterwards.
	assert.Equal(t, []string{"legacy"}, comps.DeleteOrphansCalls)
}
