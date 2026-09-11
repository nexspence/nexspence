package huggingface_test

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
	"github.com/nexspence-oss/nexspence/internal/formats/huggingface"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// setupProxy wires a huggingface proxy repo pointed at upstream, returning the
// engine plus the mocks so a test can inspect what was cached.
func setupProxy(t *testing.T, name, upstream string) (*gin.Engine, *testutil.ComponentRepo, *testutil.AssetRepo) {
	t.Helper()
	repo := testutil.SimpleRepo(name, "huggingface")
	repo.Type = domain.TypeProxy
	repo.ProxyConfig = map[string]any{"remote_url": upstream}

	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: comps,
		Assets:     assets,
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := huggingface.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, comps, assets
}

// hubUpstream is a stand-in for huggingface.co: the metadata document, and a
// resolve/ answer carrying the headers a real client reads off it.
func hubUpstream(t *testing.T, hits *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits = append(*hits, r.Method+" "+r.URL.RequestURI())
		switch r.URL.Path {
		case "/api/models/bert-base-uncased":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"id":"bert-base-uncased","sha":"`+upstreamCommit+`",`+
				`"siblings":[{"rfilename":"config.json"}]}`)
		case "/bert-base-uncased/resolve/main/config.json",
			"/bert-base-uncased/resolve/" + upstreamCommit + "/config.json":
			w.Header().Set("ETag", `"upstream-etag"`)
			w.Header().Set("X-Repo-Commit", upstreamCommit)
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodHead {
				return
			}
			fmt.Fprint(w, `{"model_type":"bert"}`)
		default:
			http.NotFound(w, r)
		}
	}))
}

const upstreamCommit = "1dbc166cf8765166998eff31ade2eb64c8a40076"

func TestProxy_CachesResolveUnderRealCoordinates(t *testing.T) {
	useUnguardedUpstream(t)
	var hits []string
	up := hubUpstream(t, &hits)
	defer up.Close()
	r, comps, assets := setupProxy(t, "hf-proxy", up.URL)

	w := do(r, http.MethodGet, "/repository/hf-proxy/bert-base-uncased/resolve/main/config.json", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, `{"model_type":"bert"}`, w.Body.String())
	// The upstream headers a real client needs reach it on a cache miss.
	assert.Equal(t, upstreamCommit, w.Header().Get("X-Repo-Commit"))

	// Cached under the canonical local layout, with coordinates that browse
	// like a hosted upload's rather than as one opaque path.
	a, err := assets.GetByPath(t.Context(), "hf-proxy", "/models/bert-base-uncased/resolve/main/config.json")
	require.NoError(t, err)
	require.NotNil(t, a)
	comp, err := comps.Get(t.Context(), a.ComponentID)
	require.NoError(t, err)
	assert.Equal(t, "models", comp.Group)
	assert.Equal(t, "bert-base-uncased", comp.Name)
	assert.Equal(t, "main", comp.Version)

	// Second GET is served from the cache: upstream is not asked again.
	before := len(hits)
	w = do(r, http.MethodGet, "/repository/hf-proxy/bert-base-uncased/resolve/main/config.json", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, before, len(hits))
}

// huggingface_hub asks for a file's metadata with HEAD and refuses to download
// without an ETag AND an X-Repo-Commit. The blob cache knows neither (it has
// our own sha256, and nothing about the upstream commit a branch pointed at), so
// the probe has to reach upstream even when the bytes are already cached.
func TestProxy_HEADIsAnsweredWithUpstreamHeaders(t *testing.T) {
	useUnguardedUpstream(t)
	var hits []string
	up := hubUpstream(t, &hits)
	defer up.Close()
	r, _, _ := setupProxy(t, "hf-proxy", up.URL)

	require.Equal(t, http.StatusOK,
		do(r, http.MethodGet, "/repository/hf-proxy/bert-base-uncased/resolve/main/config.json", "").Code)

	w := do(r, http.MethodHead, "/repository/hf-proxy/bert-base-uncased/resolve/main/config.json", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, `"upstream-etag"`, w.Header().Get("ETag"))
	assert.Equal(t, upstreamCommit, w.Header().Get("X-Repo-Commit"))
	assert.Contains(t, hits, "HEAD /bert-base-uncased/resolve/main/config.json")
}

// A commit sha names its contents permanently, so a hit is answered from the
// cache without contacting upstream — and the commit header is right by
// definition even then.
func TestProxy_CommitRevisionIsImmutable(t *testing.T) {
	useUnguardedUpstream(t)
	var hits []string
	up := hubUpstream(t, &hits)
	defer up.Close()
	r, _, _ := setupProxy(t, "hf-proxy", up.URL)

	url := "/repository/hf-proxy/bert-base-uncased/resolve/" + upstreamCommit + "/config.json"
	require.Equal(t, http.StatusOK, do(r, http.MethodGet, url, "").Code)
	before := len(hits)
	w := do(r, http.MethodGet, url, "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, before, len(hits))
	assert.Equal(t, upstreamCommit, w.Header().Get("X-Repo-Commit"))
}

func TestProxy_RelaysMetadataUnchanged(t *testing.T) {
	useUnguardedUpstream(t)
	var hits []string
	up := hubUpstream(t, &hits)
	defer up.Close()
	r, _, _ := setupProxy(t, "hf-proxy", up.URL)

	w := do(r, http.MethodGet, "/repository/hf-proxy/api/models/bert-base-uncased", "")
	require.Equal(t, http.StatusOK, w.Code)
	// A Hub metadata document names files relatively, so — unlike terraform's
	// absolute download_url — there is nothing in the body to rewrite.
	assert.Contains(t, w.Body.String(), `"rfilename":"config.json"`)
	assert.Contains(t, w.Body.String(), upstreamCommit)
}

// A tree page's Link header points at huggingface.co. Relayed verbatim it would
// send the client past this repository for page two.
func TestProxy_RewritesTreePaginationLink(t *testing.T) {
	useUnguardedUpstream(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link",
			`<https://huggingface.co/api/models/big/model/tree/main?cursor=abc&recursive=True>; rel="next"`)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"type":"file","oid":"deadbeef","size":1,"path":"a.bin"}]`)
	}))
	defer up.Close()
	r, _, _ := setupProxy(t, "hf-proxy", up.URL)

	w := do(r, http.MethodGet, "/repository/hf-proxy/api/models/big/model/tree/main?recursive=True", "")
	require.Equal(t, http.StatusOK, w.Code)
	link := w.Header().Get("Link")
	assert.True(t, strings.HasPrefix(link,
		`<http://localhost:8080/repository/hf-proxy/api/models/big/model/tree/main?cursor=abc&recursive=True>`), link)
	assert.Contains(t, link, `rel="next"`)
}

func TestProxy_RejectsPublish(t *testing.T) {
	useUnguardedUpstream(t)
	var hits []string
	up := hubUpstream(t, &hits)
	defer up.Close()
	r, _, _ := setupProxy(t, "hf-proxy", up.URL)

	w := do(r, http.MethodPut, "/repository/hf-proxy/myns/mymodel/resolve/main/config.json", "{}")
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	assert.Empty(t, hits)
}

// TestProxy_HEADFollowsSameHostButStopsAtTheCDN reproduces a real Hub LFS
// download: HEAD on the resolve/ path answers with a redirect to a separate
// host (the CDN), and the file's real metadata (X-Repo-Commit, X-Linked-ETag,
// X-Linked-Size) lives ONLY on that redirect response — confirmed live
// against bert-base-uncased's real pytorch_model.bin (420 MB) through
// huggingface_hub 1.31.0. A client that only saw the CDN's own response
// (which carries none of those headers) refuses to download at all
// (FileMetadataError) — this is exactly what a naive "let the client
// auto-follow" implementation produces, and unit tests whose mock upstream
// never actually redirects cannot catch it.
func TestProxy_HEADFollowsSameHostButStopsAtTheCDN(t *testing.T) {
	useUnguardedUpstream(t)

	cdnHits := 0
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cdnHits++
		// The CDN's own response carries none of the Hub's metadata headers —
		// exactly what a real signed-URL storage backend returns.
		w.Header().Set("ETag", `"cdn-storage-etag"`)
		w.Header().Set("Content-Length", "440473133")
		w.WriteHeader(http.StatusOK)
	}))
	defer cdn.Close()

	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/big-org/big-model/resolve/main/pytorch_model.bin" {
			http.NotFound(w, r)
			return
		}
		// The metadata-carrying redirect: only present on THIS response, not
		// on whatever the client's HTTP stack eventually lands on.
		w.Header().Set("X-Repo-Commit", "86b5e0934494bd15c9632b12f734a8a67f723594")
		w.Header().Set("X-Linked-ETag", `"097417381d6c7230bd9e3557456d726de6e83245ec8b24f529f60198a67b203a"`)
		w.Header().Set("X-Linked-Size", "440473133")
		w.Header().Set("Location", cdn.URL+"/xet-bridge-us/deadbeef")
		w.WriteHeader(http.StatusFound)
	}))
	defer hub.Close()

	r, _, _ := setupProxy(t, "hf-proxy", hub.URL)

	w := do(r, http.MethodHead, "/repository/hf-proxy/big-org/big-model/resolve/main/pytorch_model.bin", "")
	require.Equal(t, http.StatusOK, w.Code, "must answer 200, not relay the redirect, so the client's GET comes back through Nexspence")
	assert.Equal(t, "86b5e0934494bd15c9632b12f734a8a67f723594", w.Header().Get("X-Repo-Commit"))
	assert.Equal(t, `"097417381d6c7230bd9e3557456d726de6e83245ec8b24f529f60198a67b203a"`, w.Header().Get("X-Linked-ETag"))
	assert.Equal(t, "440473133", w.Header().Get("X-Linked-Size"))
	assert.Empty(t, w.Header().Get("Location"), "the CDN's signed URL must not leak to the client")
	// The CDN itself is never hit for a HEAD — only its metadata-carrying
	// redirect from the Hub is consulted.
	assert.Equal(t, 0, cdnHits)
}

// TestProxy_HEADFollowsAnInternalRedirectBeforeTheCDN mirrors
// huggingface_hub's own redirect follower: a same-host redirect (e.g. the Hub
// canonicalizing an org name) is followed transparently, and only a
// cross-host hand-off stops the chain.
func TestProxy_HEADFollowsAnInternalRedirectBeforeTheCDN(t *testing.T) {
	useUnguardedUpstream(t)

	var hub *httptest.Server
	hub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/old-org/model/resolve/main/f.bin":
			// Same-host canonicalization redirect — no metadata here, and
			// none expected: the real metadata lives on the NEXT hop.
			w.Header().Set("Location", hub.URL+"/new-org/model/resolve/main/f.bin")
			w.WriteHeader(http.StatusMovedPermanently)
		case "/new-org/model/resolve/main/f.bin":
			w.Header().Set("X-Repo-Commit", "cafef00dcafef00dcafef00dcafef00dcafef00")
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer hub.Close()

	r, _, _ := setupProxy(t, "hf-proxy", hub.URL)

	w := do(r, http.MethodHead, "/repository/hf-proxy/old-org/model/resolve/main/f.bin", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "cafef00dcafef00dcafef00dcafef00dcafef00", w.Header().Get("X-Repo-Commit"))
}

// TestProxy_HEADDropsXetHashSoTheClientFallsBackToPlainHTTP reproduces a real
// Xet-backed file (huggingface.co's newer storage backend for most of its
// actual weight files today — model.safetensors, pytorch_model.bin,
// tf_model.h5). huggingface_hub treats X-Xet-Hash as the sole trigger to fetch
// through a three-party CAS reconstruction protocol this proxy does not speak
// (a xet-read-token call back to the Hub, then chunk reconstruction against a
// THIRD host) — relaying it verbatim sends a real client into that path and
// then into a 404, since Nexspence answers none of those endpoints. Confirmed
// live against bert-base-uncased's real pytorch_model.bin before this test
// was written.
func TestProxy_HEADDropsXetHashSoTheClientFallsBackToPlainHTTP(t *testing.T) {
	useUnguardedUpstream(t)

	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Repo-Commit", "86b5e0934494bd15c9632b12f734a8a67f723594")
		w.Header().Set("X-Xet-Hash", "2d8408d3a894d02517d04956e2f7546ff08362594072f3527ce144b5212a3296")
		w.Header().Set("Link", `<https://huggingface.co/api/models/big-org/big-model/xet-read-token/86b5e0934494bd15c9632b12f734a8a67f723594>; rel="xet-auth"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer hub.Close()

	r, _, _ := setupProxy(t, "hf-proxy", hub.URL)

	w := do(r, http.MethodHead, "/repository/hf-proxy/big-org/big-model/resolve/main/pytorch_model.bin", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "86b5e0934494bd15c9632b12f734a8a67f723594", w.Header().Get("X-Repo-Commit"))
	assert.Empty(t, w.Header().Get("X-Xet-Hash"),
		"leaking this sends a real client into a CAS protocol Nexspence does not serve, instead of the plain HTTP fallback it also offers")
}
