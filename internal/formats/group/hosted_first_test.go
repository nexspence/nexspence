package group_test

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/maven"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// mavenGroupProxyFirst builds the group [central, internal] with no routing
// rule: the proxy is listed FIRST, so only hosted-first ordering keeps a name
// the hosted member publishes away from the upstream (#642).
func mavenGroupProxyFirst(t *testing.T, remote string) *gin.Engine {
	t.Helper()
	proxy := &domain.Repository{
		ID: "central", Name: "central", Format: "maven2", Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remote},
	}
	hosted := testutil.SimpleRepo("internal", "maven2")
	g := &domain.Repository{
		ID: "mg", Name: "mg", Format: "maven2", Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []any{"central", "internal"}},
	}
	repoRepo := testutil.NewRepoRepo(proxy, hosted, g)
	d := formats.Deps{
		Repos: repoRepo, Blobs: testutil.NewBlobStoreRepo(), Components: testutil.NewComponentRepo(),
		Assets: testutil.NewAssetRepo(), BlobStore: testutil.NewBlobStore(),
		BaseURL: "http://localhost:8080", RoutingRules: testutil.NewRoutingRuleRepo(),
	}
	mavenH := maven.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"maven2": mavenH})
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		mavenH.ServeHTTP(c)
	})
	return r
}

const publicMeta = "<metadata><groupId>com.acme</groupId><artifactId>lib</artifactId>" +
	"<versioning><versions><version>9.9</version></versions></versioning></metadata>"

func TestGroup_HostedFirst_FileNeverAskedOfProxy(t *testing.T) {
	srv, hits := countingUpstream(t, "PUBLIC")
	r := mavenGroupProxyFirst(t, srv.URL)
	const p = "/com/acme/lib/1.0/lib-1.0.jar"
	require.Contains(t, []int{200, 201}, doReq(r, http.MethodPut, "/repository/internal"+p, "PRIVATE").Code)

	w := doReq(r, http.MethodGet, "/repository/mg"+p, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "PRIVATE", w.Body.String())
	assert.Equal(t, "internal", w.Header().Get("X-Nexspence-Source"))
	assert.Zero(t, hits.Load(), "a file the hosted member has must not be fetched upstream")
}

func TestGroup_HostedFirst_MissFallsThroughToProxy(t *testing.T) {
	srv, hits := countingUpstream(t, "PUBLIC")
	r := mavenGroupProxyFirst(t, srv.URL)

	w := doReq(r, http.MethodGet, "/repository/mg/org/x/x/1.0/x-1.0.jar", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "PUBLIC", w.Body.String())
	assert.Equal(t, "central", w.Header().Get("X-Nexspence-Source"))
	assert.Equal(t, int32(1), hits.Load())
}

func TestGroup_HostedFirst_ClaimedIndexNotAskedOfProxy(t *testing.T) {
	srv, hits := countingUpstream(t, publicMeta)
	r := mavenGroupProxyFirst(t, srv.URL)
	meta := "<metadata><groupId>com.acme</groupId><artifactId>lib</artifactId>" +
		"<versioning><versions><version>1.0</version></versions></versioning></metadata>"
	require.Contains(t, []int{200, 201}, doReq(r, http.MethodPut, "/repository/internal/com/acme/lib/maven-metadata.xml", meta).Code)

	w := doReq(r, http.MethodGet, "/repository/mg/com/acme/lib/maven-metadata.xml", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "<version>1.0</version>")
	assert.NotContains(t, w.Body.String(), "9.9")
	assert.Equal(t, "internal", w.Header().Get("X-Nexspence-Source"))
	assert.Zero(t, hits.Load(), "an index the hosted member claims must not be asked upstream")

	// The checksum is computed over the same hosted-only document.
	w = doReq(r, http.MethodGet, "/repository/mg/com/acme/lib/maven-metadata.xml.sha1", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Zero(t, hits.Load())
}

func TestGroup_HostedFirst_UnclaimedIndexMergesProxy(t *testing.T) {
	srv, hits := countingUpstream(t, publicMeta)
	r := mavenGroupProxyFirst(t, srv.URL)

	w := doReq(r, http.MethodGet, "/repository/mg/com/acme/lib/maven-metadata.xml", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "9.9")
	assert.Equal(t, int32(1), hits.Load())
}
