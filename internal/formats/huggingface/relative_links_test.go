package huggingface_test

// #821: every URL the client is expected to follow must be root-relative.
//
// The huggingface format is the ONE exception among the formats audited for
// #821: its pagination Link header stays absolute. The clients follow the
// Link target verbatim and none of them resolves a relative reference
// against the request URL:
//
//   - huggingface_hub hands the target straight to its HTTP client
//     (huggingface_hub/utils/_pagination.py: _get_next_page ->
//     response.links["next"]["url"]), and httpx refuses a relative URL
//     ("Request URL is missing an 'http://' or 'https://' protocol");
//   - Node's fetch does the same ("Failed to parse URL from ...").
//
// That breaks list_repo_tree (and HfFileSystem with it), which is how a
// client browses a repository — so the Link header keeps its host, and this
// file pins that decision together with the rest of the format's rules: the
// host is the configured one, never the request's, and the metadata body
// carries no URL at all.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/huggingface"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// wrongBaseURL is deliberately not the host the test client reaches.
const wrongBaseURL = "http://wrong.example:8080"

// setupWithBaseURL wires a huggingface proxy repo pointed at upstream, with
// the configured base URL spelled out.
func setupWithBaseURL(t *testing.T, name, upstream, baseURL string) *gin.Engine {
	t.Helper()
	repo := testutil.SimpleRepo(name, "huggingface")
	repo.Type = domain.TypeProxy
	repo.ProxyConfig = map[string]any{"remote_url": upstream}

	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    baseURL,
	}
	h := huggingface.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// linkUpstream answers with a paginated tree: a Link header naming the next
// page upstream, and one page of file entries.
func linkUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link",
			`<https://huggingface.co/api/models/big/model/tree/main?cursor=abc&recursive=True>; rel="next"`)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"type":"file","oid":"deadbeef","size":1,"path":"a.bin"}]`)
	}))
}

// The pagination Link header stays absolute — the clients cannot follow a
// relative one — but it names the configured host, never the request's.
func TestProxy_LinkHeaderStaysAbsolute(t *testing.T) {
	useUnguardedUpstream(t)
	up := linkUpstream(t)
	defer up.Close()

	r := setupWithBaseURL(t, "hf-rel-link", up.URL, wrongBaseURL)

	w := do(r, http.MethodGet, "/repository/hf-rel-link/api/models/big/model/tree/main?recursive=True", "")
	require.Equal(t, http.StatusOK, w.Code)

	link := w.Header().Get("Link")
	assert.Contains(t, link, "<http://wrong.example:8080/repository/hf-rel-link/api/models/big/model/tree/main?cursor=abc&recursive=True>",
		"the Link target stays absolute and points at the configured host")
	assert.Contains(t, link, `rel="next"`)
}

// The metadata body names its files relatively, so the client builds the
// download URL itself out of its own endpoint: nothing in the body carries a
// host.
func TestProxy_MetadataBodyCarriesNoAbsoluteURL(t *testing.T) {
	useUnguardedUpstream(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"bert-base-uncased","sha":"deadbeef","siblings":[{"rfilename":"config.json"}]}`)
	}))
	defer up.Close()

	r := setupWithBaseURL(t, "hf-rel-body", up.URL, wrongBaseURL)

	w := do(r, http.MethodGet, "/repository/hf-rel-body/api/models/bert-base-uncased", "")
	require.Equal(t, http.StatusOK, w.Code)

	body := w.Body.String()
	assert.NotContains(t, body, "http://", "the metadata body must not carry an absolute URL")
	assert.NotContains(t, body, "https://", "the metadata body must not carry an absolute URL")
	assert.NotContains(t, body, wrongBaseURL, "the configured base URL must not reach the client")
}

// The Link header must not depend on the request's Host header. A link built
// from the request's host would be the cache-poisoning vector this fix exists
// to avoid — and it would be invisible: every other test sends the usual
// host, so such a change would still pass them all. The same request served
// under a foreign host must return the very same header.
func TestProxy_LinkHeaderDoesNotDependOnRequestHost(t *testing.T) {
	useUnguardedUpstream(t)
	up := linkUpstream(t)
	defer up.Close()

	r := setupWithBaseURL(t, "hf-rel-hostdep", up.URL, "http://localhost:8080")

	serve := func(host string) string {
		req := httptest.NewRequest(http.MethodGet,
			"/repository/hf-rel-hostdep/api/models/big/model/tree/main?recursive=True", nil)
		req.Host = host
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		return w.Header().Get("Link")
	}

	normal := serve("localhost")
	foreign := serve("myrepo.ru")

	assert.NotEmpty(t, normal, "the comparison must not be vacuous")
	assert.Equal(t, normal, foreign,
		"the served Link header must not depend on the request's Host header")
}
