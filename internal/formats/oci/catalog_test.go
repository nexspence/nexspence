package oci_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ── Helpers ───────────────────────────────────────────────────

// getCatalog issues a _catalog request and decodes the repository names plus the
// raw Link header, the same shape getTags uses for the tag list.
func getCatalog(t *testing.T, r *gin.Engine, target string) (repos []string, link string) {
	t.Helper()
	w := doCatalog(r, target)
	require.Equal(t, http.StatusOK, w.Code, "GET %s: %s", target, w.Body.String())
	var body struct {
		Repositories []string `json:"repositories"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Repositories, w.Header().Get("Link")
}

func doCatalog(r *gin.Engine, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ── What the catalog lists ────────────────────────────────────

// Each Nexspence repository is its own registry namespace, so the catalog of
// <repoName> is the set of image names inside it — the same names tags/list is
// addressed by.
func TestCatalog_ListsEveryImageNameOnceSorted(t *testing.T) {
	repo := testutil.SimpleRepo("cat1", "docker")
	r, _ := setupWithDeps(repo)

	// library/ubuntu carries two tags, which is what would duplicate a name
	// derived naively from components: every tag is its own component row, and a
	// manifest push registers a second digest-alias component on top of that.
	pushTags(t, r, "cat1", "library/ubuntu", "latest", "22.04")
	pushTags(t, r, "cat1", "charts/nginx", "1.2.3")
	// A sibling whose name contains another image's name in full: the component
	// search matches names with a substring ILIKE, so anything built on it would
	// fuse the two.
	pushTags(t, r, "cat1", "charts/nginx-extra", "1.0.0")

	repos, link := getCatalog(t, r, "/repository/cat1/v2/_catalog")
	assert.Equal(t, []string{"charts/nginx", "charts/nginx-extra", "library/ubuntu"}, repos)
	assert.Empty(t, link, "nothing was truncated, so there is no next page")
}

// Every blob upload registers a component of its own, so a catalog built from
// components lists junk: layer names, and images whose upload was abandoned
// before the manifest. Only an image with a manifest can be pulled, and only
// that is a repository name in the OCI sense.
func TestCatalog_OmitsImagesWithBlobsButNoManifest(t *testing.T) {
	repo := testutil.SimpleRepo("cat2", "docker")
	r, _ := setupWithDeps(repo)

	pushTags(t, r, "cat2", "library/ubuntu", "latest")
	// A blob pushed under an image whose manifest never followed.
	pushBlob(t, r, "cat2", "aborted/upload", "layer bytes that never got a manifest")
	// And a layer pushed under an image that does have a manifest — the same
	// image name, so it must not appear twice either.
	pushBlob(t, r, "cat2", "library/ubuntu", "a layer of the real image")

	repos, _ := getCatalog(t, r, "/repository/cat2/v2/_catalog")
	assert.Equal(t, []string{"library/ubuntu"}, repos,
		"only images that have a manifest are pullable, so only those are repository names")
	assert.NotContains(t, repos, "aborted/upload")
}

// An image whose manifests have all been deleted leaves its blobs behind until
// the next cleanup run, but nothing that can be pulled: tags/list is empty and
// every manifest reference 404s. Listing it would be a promise the registry
// cannot keep, so it drops out of the catalog with its last manifest.
func TestCatalog_DropsImagesWhoseManifestsWereAllDeleted(t *testing.T) {
	repo := testutil.SimpleRepo("cat8", "docker")
	r, _ := setupWithDeps(repo)

	pushTags(t, r, "cat8", "library/ubuntu", "latest")
	pushTags(t, r, "cat8", "charts/nginx", "1.2.3")
	// The layer stays behind after the manifests go.
	pushBlob(t, r, "cat8", "library/ubuntu", "a layer that outlives its manifest")

	// Both references the tagged push registered: the tag and the digest alias.
	for _, ref := range []string{"latest", digest(pagingManifest)} {
		req := httptest.NewRequest(http.MethodDelete,
			"/repository/cat8/v2/library/ubuntu/manifests/"+ref, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusAccepted, w.Code, "delete %s: %s", ref, w.Body.String())
	}

	repos, _ := getCatalog(t, r, "/repository/cat8/v2/_catalog")
	assert.Equal(t, []string{"charts/nginx"}, repos,
		"an image with no manifest left cannot be pulled, so it is no longer a repository name")
}

// ── The spec's paging shape ───────────────────────────────────

func TestCatalog_PaginatesWithLinkHeader(t *testing.T) {
	repo := testutil.SimpleRepo("cat3", "docker")
	r, _ := setupWithDeps(repo)
	for _, img := range []string{"img/e", "img/a", "img/d", "img/b", "img/c"} {
		pushTags(t, r, "cat3", img, "1.0")
	}

	repos, link := getCatalog(t, r, "/repository/cat3/v2/_catalog?n=2")
	assert.Equal(t, []string{"img/a", "img/b"}, repos, "first page must be the lexically first two names")

	u := parseNextLink(t, link)
	assert.Equal(t, "/repository/cat3/v2/_catalog", u.Path, "the link must keep the client's path form")
	assert.Equal(t, "2", u.Query().Get("n"))
	assert.Equal(t, "img/b", u.Query().Get("last"), "cursor must name the last entry of this page")

	repos2, link2 := getCatalog(t, r, u.String())
	assert.Equal(t, []string{"img/c", "img/d"}, repos2)

	repos3, link3 := getCatalog(t, r, parseNextLink(t, link2).String())
	assert.Equal(t, []string{"img/e"}, repos3)
	assert.Empty(t, link3, "the final page must carry no Link header")
}

func TestCatalog_LastWithoutNReturnsTheRemainder(t *testing.T) {
	repo := testutil.SimpleRepo("cat4", "docker")
	r, _ := setupWithDeps(repo)
	for _, img := range []string{"img/a", "img/b", "img/c"} {
		pushTags(t, r, "cat4", img, "1.0")
	}

	repos, link := getCatalog(t, r, "/repository/cat4/v2/_catalog?last=img/a")
	assert.Equal(t, []string{"img/b", "img/c"}, repos)
	assert.Empty(t, link)
}

// ── The empty repository ──────────────────────────────────────

// An empty catalog must serialize as [] and not null: a null breaks clients that
// range over the list.
func TestCatalog_EmptyRepositoryIsAnEmptyArray(t *testing.T) {
	repo := testutil.SimpleRepo("cat5", "docker")
	r, _ := setupWithDeps(repo)

	w := doCatalog(r, "/repository/cat5/v2/_catalog")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"repositories":[]}`, w.Body.String())
	assert.NotContains(t, w.Body.String(), "null")
}

// ── Proxy repositories ────────────────────────────────────────

// A proxy answers the catalog from what it has cached and never forwards. Docker
// Hub, GHCR and almost every other upstream refuse _catalog outright, so
// forwarding would turn the endpoint into a 502 for the common case; and an
// upstream that did answer would have the proxy claim a catalog of images it
// cannot serve without first fetching them.
func TestCatalog_ProxyAnswersFromCacheWithoutAskingUpstream(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		paths = append(paths, req.URL.Path)
		mu.Unlock()
		if req.URL.Path != "/v2/library/ubuntu/manifests/latest" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write([]byte(helmChartManifest))
	}))
	defer upstream.Close()

	r, _ := setupWithDeps(proxyOCIRepo("r2", "oci-proxy", upstream.URL))

	// Pull one manifest through the proxy so the cache holds exactly one image.
	req := httptest.NewRequest(http.MethodGet,
		"/repository/oci-proxy/v2/library/ubuntu/manifests/latest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "the proxy pull must succeed: %s", w.Body.String())

	mu.Lock()
	paths = nil
	mu.Unlock()

	repos, _ := getCatalog(t, r, "/repository/oci-proxy/v2/_catalog")
	assert.Equal(t, []string{"library/ubuntu"}, repos,
		"a proxy lists what it has cached")

	mu.Lock()
	defer mu.Unlock()
	assert.Empty(t, paths, "the catalog request must never reach the upstream, got %v", paths)
}

// ── Method and shape ──────────────────────────────────────────

func TestCatalog_RejectsNonGET(t *testing.T) {
	repo := testutil.SimpleRepo("cat6", "docker")
	r, _ := setupWithDeps(repo)

	for _, method := range []string{http.MethodPut, http.MethodDelete, http.MethodPost} {
		req := httptest.NewRequest(method, "/repository/cat6/v2/_catalog", strings.NewReader(""))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "%s _catalog", method)
	}
}

// The short path form a Docker client uses must reach the same endpoint, and its
// Link header must stay on /v2/ (issue #47).
func TestCatalog_ShortPathForm(t *testing.T) {
	repo := testutil.SimpleRepo("cat7", "docker")
	r := setupV2Scoped(repo)
	for _, img := range []string{"img/a", "img/b"} {
		req := httptest.NewRequest(http.MethodPut,
			fmt.Sprintf("/v2/cat7/%s/manifests/1.0", img), strings.NewReader(pagingManifest))
		req.ContentLength = int64(len(pagingManifest))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	}

	repos, link := getCatalog(t, r, "/v2/cat7/_catalog?n=1")
	assert.Equal(t, []string{"img/a"}, repos)
	u := parseNextLink(t, link)
	assert.Equal(t, "/v2/cat7/_catalog", u.Path)

	rest, _ := getCatalog(t, r, u.String())
	assert.Equal(t, []string{"img/b"}, rest)
}
