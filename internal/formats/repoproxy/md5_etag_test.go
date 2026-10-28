package repoproxy_test

import (
	"bytes"
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
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// MD5ETag (#679): the edges the RubyGems handler tests do not reach.

func serveMD5ETag(t *testing.T, repo *domain.Repository, d formats.Deps, method string, hdr map[string]string, rewrite func([]byte) []byte) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/info/rack", nil)
	for k, v := range hdr {
		c.Request.Header.Set(k, v)
	}
	err := repoproxy.ServeGETWithOptions(c, d, repo, "/info/rack", "", base.Coords{}, "text/plain", 0,
		repoproxy.ServeOptions{Rewrite: rewrite, MD5ETag: true})
	require.NoError(t, err)
	return w
}

func cachedInfo(t *testing.T, md5 string) (*domain.Repository, formats.Deps) {
	t.Helper()
	repo := proxyRepo("md5etag", "https://upstream.example.com")
	d := makeDeps(repo)
	content := "---\n1.0.0 |checksum:abc\n"
	require.NoError(t, d.BlobStore.Put(context.Background(), "k", strings.NewReader(content), int64(len(content))))
	require.NoError(t, d.Assets.Create(context.Background(), &domain.Asset{
		Repository: repo.Name, RepositoryID: repo.ID, Path: "/info/rack", BlobKey: "k",
		ContentType: "text/plain", SizeBytes: int64(len(content)),
		SHA256: "0000000000000000000000000000000000000000000000000000000000000000", MD5: md5,
	}))
	return repo, d
}

// An asset cached without an MD5 gets no ETag at all — Bundler accepts a
// missing ETag, but would check any other value as an MD5 and reject it.
func TestServeGETWithOptions_MD5ETag_NoMD5NoETag(t *testing.T) {
	repo, d := cachedInfo(t, "")
	w := serveMD5ETag(t, repo, d, http.MethodGet, map[string]string{"If-None-Match": "*"}, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("ETag"))
	assert.Equal(t, "sha-256=:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=:", w.Header().Get("Repr-Digest"))
}

// A rewrite may change the body the cached MD5 names, so it never answers 304.
func TestServeGETWithOptions_MD5ETag_RewriteNeverNotModified(t *testing.T) {
	repo, d := cachedInfo(t, "0123456789abcdef0123456789abcdef")
	w := serveMD5ETag(t, repo, d, http.MethodGet,
		map[string]string{"If-None-Match": `"0123456789abcdef0123456789abcdef"`}, bytes.ToUpper)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "---\n1.0.0 |CHECKSUM:ABC\n", w.Body.String())
}

func TestServeGETWithOptions_MD5ETag_Head(t *testing.T) {
	repo, d := cachedInfo(t, "0123456789abcdef0123456789abcdef")
	w := serveMD5ETag(t, repo, d, http.MethodHead, nil, nil)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, `"0123456789abcdef0123456789abcdef"`, w.Header().Get("ETag"))
	w = serveMD5ETag(t, repo, d, http.MethodHead,
		map[string]string{"If-None-Match": `"0123456789abcdef0123456789abcdef"`}, nil)
	assert.Equal(t, http.StatusNotModified, w.Code)
}
