package repoproxy_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// ServeGETRewritten must apply the rewrite on every serve path (cache miss,
// cache hit) while the blob cache keeps the upstream ORIGINAL — BaseURL
// changes must never invalidate cached metadata (#98).
func TestServeGETRewritten_MissAndHit_RewriteOnServe(t *testing.T) {
	useUnguardedUpstream(t)
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte("hello world"))
	}))
	defer upstream.Close()

	repo := proxyRepo("rw1", upstream.URL)
	d := makeDeps(repo)
	rewrite := bytes.ToUpper

	// Cache miss → fetch upstream, serve rewritten.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/meta.json", nil)
	err := repoproxy.ServeGETRewritten(c, d, repo, "/meta.json", "", base.Coords{}, "text/plain", 0, rewrite)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "HELLO WORLD", w.Body.String())

	// Cache hit (immutable maxAge=0 → no upstream) → still rewritten.
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/meta.json", nil)
	err = repoproxy.ServeGETRewritten(c2, d, repo, "/meta.json", "", base.Coords{}, "text/plain", 0, rewrite)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "HELLO WORLD", w2.Body.String())
	assert.Equal(t, 1, hits, "second request must be served from cache")

	// The stored blob holds the upstream original, not the rewritten bytes.
	asset, err := d.Assets.GetByPath(context.Background(), "rw1", "/meta.json")
	require.NoError(t, err)
	rc, _, err := d.BlobStore.Get(context.Background(), asset.BlobKey)
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	var stored bytes.Buffer
	_, _ = stored.ReadFrom(rc)
	assert.Equal(t, "hello world", stored.String())
}

// A nil rewrite must behave exactly like ServeGET (streaming path untouched).
func TestServeGETRewritten_NilRewrite_Streams(t *testing.T) {
	useUnguardedUpstream(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("raw-bytes"))
	}))
	defer upstream.Close()

	repo := proxyRepo("rw2", upstream.URL)
	d := makeDeps(repo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/f.bin", nil)
	err := repoproxy.ServeGETRewritten(c, d, repo, "/f.bin", "", base.Coords{}, "", 0, nil)
	require.NoError(t, err)
	assert.Equal(t, "raw-bytes", w.Body.String())
}
