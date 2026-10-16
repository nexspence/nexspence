package repoproxy_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A client that double-encodes ".." ("%252e%252e") hands the handler "%2e%2e":
// not a ".." segment to path.Clean, but JoinURL keeps it as an escape in the
// raw path and the upstream (nginx, Apache, Go) decodes it back to "..".
func TestJoinURL_RefusesEncodedDotSegments(t *testing.T) {
	for _, p := range []string{
		"/%2e%2e/%2e%2e/other/file",
		"/a/%2E%2E/b",
		"/a/.%2e/b",
		"/a/%2e/b",
		"@scope%2F..",
	} {
		_, err := repoproxy.JoinURL("https://repo.example.com/maven2", p)
		assert.Error(t, err, p)
	}

	// The escape NPMMetadataPath produces on purpose still goes through.
	got, err := repoproxy.JoinURL("https://registry.npmjs.org/", repoproxy.NPMMetadataPath("/@types/node"))
	require.NoError(t, err)
	assert.Equal(t, "https://registry.npmjs.org/@types%2Fnode", got)
}

// End to end through ServeGET: the upstream, which would receive the proxy's
// remote credentials, never sees the encoded traversal.
func TestServeGET_EncodedTraversal_NeverReachesUpstream(t *testing.T) {
	useUnguardedUpstream(t)
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("other repository's file"))
	}))
	defer upstream.Close()

	repo := proxyRepo("travproxy", upstream.URL+"/maven2")
	d := formats.Deps{
		Repos: testutil.NewRepoRepo(repo), Blobs: testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(), Assets: testutil.NewAssetRepo(),
		BlobStore: testutil.NewBlobStore(),
	}
	const assetPath = "/%2e%2e/%2e%2e/other/file"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)

	require.NoError(t, repoproxy.ServeGET(c, d, repo, assetPath, "", base.Coords{Name: "file"}, "application/octet-stream", 0))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Zero(t, hits.Load(), "the upstream must not be asked for an encoded traversal")
}
