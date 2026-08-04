package oci_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

func TestProxyManifest_RecordsArtifactMetadata(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/charts/nginx/manifests/1.2.3" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write([]byte(helmChartManifest))
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "r2", Name: "oci-proxy", Format: domain.FormatOCI, Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r, d := setupWithDeps(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/oci-proxy/v2/charts/nginx/manifests/1.2.3", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "proxy should serve the upstream manifest")
	assert.Equal(t, helmChartManifest, w.Body.String())

	comp := componentOf(t, d, "oci-proxy", "charts/nginx", "1.2.3")
	assert.Equal(t, "application/vnd.cncf.helm.config.v1+json", comp.Extra["oci_artifact_type"],
		"a cached chart must be typed like a pushed one")
}

// cosignManifest carries no top-level mediaType — the artifact type lives only in
// config.mediaType. Real cosign signatures and older OCI 1.0 producers look like
// this, so the "have we typed this yet?" guard cannot key on the media type.
const cosignManifest = `{
  "schemaVersion": 2,
  "config": {"mediaType": "application/vnd.oci.image.config.v1+json", "digest": "sha256:cc", "size": 12},
  "layers": [{"mediaType": "application/vnd.dev.cosign.simplesigning.v1+json", "digest": "sha256:dd", "size": 34}]
}`

// countingStore wraps the blob store double to count Get calls, so a test can
// observe whether the metadata helper re-reads a manifest it has already typed.
// Embedding supplies every other BlobStore method unchanged.
type countingStore struct {
	*testutil.BlobStore
	mu   sync.Mutex
	gets int
}

func (s *countingStore) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	s.mu.Lock()
	s.gets++
	s.mu.Unlock()
	return s.BlobStore.Get(ctx, key)
}

func (s *countingStore) getCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets
}

// setupCounting is setupWithDeps with a Get-counting blob store.
func setupCounting(repo *domain.Repository) (*gin.Engine, formats.Deps, *countingStore) {
	store := &countingStore{BlobStore: testutil.NewBlobStore()}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  store,
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
	return r, d, store
}

// pullTag issues one proxy pull and asserts it succeeded.
func pullTag(t *testing.T, r *gin.Engine, repoName, tag string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/repository/"+repoName+"/v2/charts/nginx/manifests/"+tag, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "proxy pull should succeed")
	return w.Body.String()
}

// oversizedManifest is valid JSON larger than the 4 MiB spec cap, padded with an
// annotation. Nothing rejects it on the proxy read-back path — it is already in
// the cache — so the metadata helper must still arm its guard on it.
func oversizedManifest() string {
	return `{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "config": {"mediaType": "application/vnd.cncf.helm.config.v1+json", "digest": "sha256:aa", "size": 12},
  "annotations": {"pad": "` + strings.Repeat("x", 5<<20) + `"}
}`
}

// A cached manifest over the 4 MiB cap must not be re-read from the blob store on
// every pull: parsing it is optional, arming the guard is not.
func TestProxyManifest_DoesNotRereadOversizedManifest(t *testing.T) {
	body := oversizedManifest()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "r6", Name: "oci-proxy", Format: domain.FormatOCI, Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r, _, store := setupCounting(repo)

	require.Equal(t, body, pullTag(t, r, "oci-proxy", "1.2.3"))
	afterFirst := store.getCount()
	require.Positive(t, afterFirst, "the counting store must actually be the one in use")

	require.Equal(t, body, pullTag(t, r, "oci-proxy", "1.2.3"))
	assert.Equal(t, afterFirst+1, store.getCount(),
		"an oversized cached manifest must not be re-read for metadata on every pull")
}

// A mutable tag re-pointed upstream must re-type the component: the cached blob
// now holds a different manifest, so metadata describing the old one is wrong.
func TestProxyManifest_RetypesRepointedTag(t *testing.T) {
	var serveSecond atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		if serveSecond.Load() {
			_, _ = w.Write([]byte(cosignManifest))
			return
		}
		_, _ = w.Write([]byte(helmChartManifest))
	}))
	defer upstream.Close()

	// A 1ns metadata TTL forces revalidation on the second pull without sleeping:
	// any elapsed time between the two requests already exceeds it.
	repo := &domain.Repository{
		ID: "r3", Name: "oci-proxy", Format: domain.FormatOCI, Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL, "metadata_max_age": 0.000000001},
	}
	r, d := setupWithDeps(repo)

	require.Equal(t, helmChartManifest, pullTag(t, r, "oci-proxy", "1.2.3"))
	require.Equal(t, "application/vnd.cncf.helm.config.v1+json",
		componentOf(t, d, "oci-proxy", "charts/nginx", "1.2.3").Extra["oci_artifact_type"])

	serveSecond.Store(true)
	require.Equal(t, cosignManifest, pullTag(t, r, "oci-proxy", "1.2.3"),
		"revalidation should have replaced the cached body")

	comp := componentOf(t, d, "oci-proxy", "charts/nginx", "1.2.3")
	assert.Equal(t, "application/vnd.oci.image.config.v1+json", comp.Extra["oci_artifact_type"],
		"a re-pointed tag must carry the NEW manifest's artifact type, not the old one")
}

// Steady state: a second pull of an unchanged manifest serves from cache and must
// not read the blob a second time for metadata it already recorded.
func TestProxyManifest_DoesNotRereadUnchangedManifest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write([]byte(helmChartManifest))
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "r4", Name: "oci-proxy", Format: domain.FormatOCI, Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r, _, store := setupCounting(repo)

	require.Equal(t, helmChartManifest, pullTag(t, r, "oci-proxy", "1.2.3"))
	afterFirst := store.getCount()
	require.Positive(t, afterFirst, "the counting store must actually be the one in use")

	require.Equal(t, helmChartManifest, pullTag(t, r, "oci-proxy", "1.2.3"))
	assert.Equal(t, afterFirst+1, store.getCount(),
		"the second pull may read the blob once to serve it, never twice")
}

// The same steady-state guarantee for a manifest with no top-level mediaType:
// the guard must still arm, or every pull re-reads the blob forever.
func TestProxyManifest_DoesNotRereadManifestWithoutMediaType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write([]byte(cosignManifest))
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "r5", Name: "oci-proxy", Format: domain.FormatOCI, Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r, d, store := setupCounting(repo)

	require.Equal(t, cosignManifest, pullTag(t, r, "oci-proxy", "1.2.3"))
	require.Equal(t, "application/vnd.oci.image.config.v1+json",
		componentOf(t, d, "oci-proxy", "charts/nginx", "1.2.3").Extra["oci_artifact_type"],
		"a manifest with no top-level mediaType is still typed from config.mediaType")
	afterFirst := store.getCount()
	require.Positive(t, afterFirst, "the counting store must actually be the one in use")

	require.Equal(t, cosignManifest, pullTag(t, r, "oci-proxy", "1.2.3"))
	assert.Equal(t, afterFirst+1, store.getCount(),
		"a manifest without mediaType must not be re-read on every pull")
}
