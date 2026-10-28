package rubygems_test

import (
	"crypto/md5" //nolint:gosec // compact-index ETag, not security
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A proxied compact-index file served from cache must carry the ETag
// rubygems.org sends: the quoted MD5 of the body. Bundler before 2.5 checks
// the body it received against that ETag and raises MisMatchedChecksumError
// on anything else, so a sha256 ETag broke every resolution served from
// cache (#679). Repr-Digest/Digest carry the sha-256 that Bundler 2.5+
// verifies instead.

func md5ETag(body []byte) string {
	sum := md5.Sum(body) //nolint:gosec
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func sha256Digest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha-256=:" + base64.StdEncoding.EncodeToString(sum[:]) + ":"
}

func getWith(r *gin.Engine, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRubyGems_Proxy_CachedCompactIndexHasMD5ETag(t *testing.T) {
	for _, tc := range []struct {
		name       string
		minAgeSecs int
	}{
		{"no age policy", 0},
		// Every version of the gem is old enough: /info passes the policy
		// unchanged, so the cached original's headers describe the body.
		{"age policy, nothing filtered", 24 * 3600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream, _ := gemsUpstream(t, map[string]fakeGem{"rack": {versions: []gemVersion{
				{number: "3.1.0", created: ago(90 * day)},
			}, infoDates: true, api: true}})
			r, _ := setup(gemsProxyRepo("gems", upstream.URL, tc.minAgeSecs))

			for _, p := range []string{"/versions", "/info/rack"} {
				miss := get(r, "/repository/gems"+p)
				require.Equal(t, http.StatusOK, miss.Code, p)
				body := miss.Body.Bytes()
				assert.Equal(t, md5ETag(body), miss.Header().Get("ETag"), p+": miss")

				hit := get(r, "/repository/gems"+p)
				require.Equal(t, http.StatusOK, hit.Code, p)
				assert.Equal(t, body, hit.Body.Bytes(), p+": hit body")
				assert.Equal(t, md5ETag(body), hit.Header().Get("ETag"), p+": hit ETag")
				assert.Equal(t, sha256Digest(body), hit.Header().Get("Repr-Digest"), p+": hit Repr-Digest")
				assert.Equal(t, sha256Digest(body), hit.Header().Get("Digest"), p+": hit Digest")
			}
		})
	}
}

// Bundler revalidates its local copy with If-None-Match (the MD5 it holds,
// or the ETag it saved) and asks for the tail with Range. A matching ETag is
// answered 304; anything else gets the whole file, which Bundler accepts in
// place of a 206 as long as the ETag matches the body.
func TestRubyGems_Proxy_CachedCompactIndexConditional(t *testing.T) {
	upstream, _ := gemsUpstream(t, map[string]fakeGem{"rack": rackGem()})
	r, _ := setup(gemsProxyRepo("gems", upstream.URL, 0))

	for _, p := range []string{"/versions", "/info/rack"} {
		miss := get(r, "/repository/gems"+p)
		require.Equal(t, http.StatusOK, miss.Code, p)
		body := miss.Body.Bytes()
		etag := md5ETag(body)

		notModified := getWith(r, "/repository/gems"+p, map[string]string{
			"If-None-Match": etag, "Range": "bytes=10-",
		})
		assert.Equal(t, http.StatusNotModified, notModified.Code, p)
		assert.Empty(t, notModified.Body.Bytes(), p)
		assert.Equal(t, etag, notModified.Header().Get("ETag"), p)

		weak := getWith(r, "/repository/gems"+p, map[string]string{"If-None-Match": `"x", W/` + etag})
		assert.Equal(t, http.StatusNotModified, weak.Code, p+": weak validator in a list")

		stale := getWith(r, "/repository/gems"+p, map[string]string{
			"If-None-Match": `"0123456789abcdef0123456789abcdef"`, "Range": "bytes=10-",
		})
		require.Equal(t, http.StatusOK, stale.Code, p)
		assert.Equal(t, body, stale.Body.Bytes(), p+": full body in place of a range")
		assert.Equal(t, etag, stale.Header().Get("ETag"), p)
	}
}

// Gem files keep the generic proxy headers.
func TestRubyGems_Proxy_CachedGemKeepsSHA256ETag(t *testing.T) {
	upstream, _ := gemsUpstream(t, map[string]fakeGem{"rack": rackGem()})
	r, _ := setup(gemsProxyRepo("gems", upstream.URL, 0))

	require.Equal(t, http.StatusOK, get(r, "/repository/gems/gems/rack-3.1.0.gem").Code)
	hit := get(r, "/repository/gems/gems/rack-3.1.0.gem")
	require.Equal(t, http.StatusOK, hit.Code)
	sum := sha256.Sum256(hit.Body.Bytes())
	assert.Equal(t, `"`+hex.EncodeToString(sum[:])+`"`, hit.Header().Get("ETag"))
	assert.Empty(t, hit.Header().Get("Repr-Digest"))
}
