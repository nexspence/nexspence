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
