package oci_test

import (
	"context"
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

const helmChartManifest = `{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "config": {"mediaType": "application/vnd.cncf.helm.config.v1+json", "digest": "sha256:aa", "size": 12},
  "layers": [{"mediaType": "application/vnd.cncf.helm.chart.content.v1.tar+gzip", "digest": "sha256:bb", "size": 34}],
  "annotations": {"org.opencontainers.image.version": "1.2.3"}
}`

// setupWithDeps is setup() from handler_test.go, also handing back the deps so a
// test can inspect what the handler stored.
func setupWithDeps(repo *domain.Repository) (*gin.Engine, formats.Deps) {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
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
	return r, d
}

// componentOf finds the stored component for an image/tag pair.
func componentOf(t *testing.T, d formats.Deps, repoName, name, version string) domain.Component {
	t.Helper()
	page, err := d.Components.Search(context.Background(), domain.SearchParams{Repository: repoName, Limit: 100})
	require.NoError(t, err)
	for _, comp := range page.Items {
		if comp.Name == name && comp.Version == version {
			return comp
		}
	}
	t.Fatalf("no component %s:%s in %s", name, version, repoName)
	return domain.Component{}
}

func TestPushManifest_RecordsArtifactMetadata(t *testing.T) {
	repo := &domain.Repository{
		ID: "r1", Name: "oci-hosted", Format: domain.FormatOCI, Type: domain.TypeHosted, Online: true,
	}
	r, d := setupWithDeps(repo)

	req := httptest.NewRequest(http.MethodPut,
		"/repository/oci-hosted/v2/charts/nginx/manifests/1.2.3", strings.NewReader(helmChartManifest))
	req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	req.ContentLength = int64(len(helmChartManifest))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "manifest push should succeed")

	comp := componentOf(t, d, "oci-hosted", "charts/nginx", "1.2.3")
	assert.Equal(t, "application/vnd.cncf.helm.config.v1+json", comp.Extra["oci_artifact_type"])
	assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", comp.Extra["oci_media_type"])
}

// The pushed bytes must survive parsing untouched — reading the body for
// metadata must not truncate what gets stored.
func TestPushManifest_StoresBodyVerbatim(t *testing.T) {
	repo := &domain.Repository{
		ID: "r1", Name: "oci-hosted", Format: domain.FormatOCI, Type: domain.TypeHosted, Online: true,
	}
	r, _ := setupWithDeps(repo)

	req := httptest.NewRequest(http.MethodPut,
		"/repository/oci-hosted/v2/charts/nginx/manifests/1.2.3", strings.NewReader(helmChartManifest))
	req.ContentLength = int64(len(helmChartManifest))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	get := httptest.NewRequest(http.MethodGet, "/repository/oci-hosted/v2/charts/nginx/manifests/1.2.3", nil)
	gw := httptest.NewRecorder()
	r.ServeHTTP(gw, get)
	require.Equal(t, http.StatusOK, gw.Code)
	assert.Equal(t, helmChartManifest, gw.Body.String())
}
