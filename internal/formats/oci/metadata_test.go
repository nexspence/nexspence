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

// Docker clients re-fetch a manifest by digest after pulling it by tag, so the
// digest reference gets a component of its own. Phase 2's referrers API resolves
// a subject by digest — that component needs the same metadata as the tag.
func TestPushManifest_TypesDigestAlias(t *testing.T) {
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
	require.Equal(t, http.StatusCreated, w.Code)

	dgst := digest(helmChartManifest)
	require.Equal(t, dgst, w.Header().Get("Docker-Content-Digest"))

	comp := componentOf(t, d, "oci-hosted", "charts/nginx", dgst)
	assert.Equal(t, "application/vnd.cncf.helm.config.v1+json", comp.Extra["oci_artifact_type"])
	assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", comp.Extra["oci_media_type"])
}

// maxManifest mirrors the unexported maxManifestBytes: the 4 MiB manifest limit
// from the OCI Distribution Spec.
const maxManifest = 4 << 20

// A body past the limit must be rejected outright. Truncating it to the limit
// would store a corrupt manifest and answer 201 with a digest over bytes the
// client never pushed.
func TestPushManifest_RejectsOversizedManifest(t *testing.T) {
	repo := &domain.Repository{
		ID: "r1", Name: "oci-hosted", Format: domain.FormatOCI, Type: domain.TypeHosted, Online: true,
	}
	r, d := setupWithDeps(repo)

	// Exactly one byte over the limit, and valid JSON apart from its size.
	head := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","pad":"`
	tail := `"}`
	oversized := head + strings.Repeat("a", maxManifest+1-len(head)-len(tail)) + tail
	require.Len(t, oversized, maxManifest+1)

	req := httptest.NewRequest(http.MethodPut,
		"/repository/oci-hosted/v2/charts/big/manifests/1.0.0", strings.NewReader(oversized))
	req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	req.ContentLength = int64(len(oversized))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)

	// Rejection must be total — no half-stored manifest behind the error.
	page, err := d.Components.Search(context.Background(), domain.SearchParams{Repository: "oci-hosted", Limit: 100})
	require.NoError(t, err)
	assert.Empty(t, page.Items, "a rejected push must leave no component")

	_, err = d.Assets.GetByPath(context.Background(), "oci-hosted", "/manifests/charts/big/1.0.0")
	assert.Error(t, err, "a rejected push must leave no asset")
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
