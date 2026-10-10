package group_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// countingUpstream answers every path with body and counts requests.
func countingUpstream(t *testing.T, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// mavenGroupWithBlockedProxy builds the group [central, internal]: the proxy
// comes FIRST, so only its routing rule — not member order — can keep a
// com.acme request away from its upstream (#642).
func mavenGroupWithBlockedProxy(t *testing.T, remote string) *gin.Engine {
	t.Helper()
	ruleID := "acme-private"
	rules := testutil.NewRoutingRuleRepo()
	require.NoError(t, rules.Create(context.Background(),
		&domain.RoutingRule{ID: ruleID, Name: ruleID, Mode: "BLOCK", Matchers: []string{`^/com/acme/`}}))
	proxy := &domain.Repository{
		ID: "central", Name: "central", Format: "maven2", Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remote}, RoutingRuleID: &ruleID,
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
		BaseURL: "http://localhost:8080", RoutingRules: rules,
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

func doReq(r *gin.Engine, method, url, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestGroup_ProxyRuleBlock_HostedServesFile(t *testing.T) {
	srv, hits := countingUpstream(t, "PUBLIC")
	r := mavenGroupWithBlockedProxy(t, srv.URL)
	const p = "/com/acme/lib/1.0/lib-1.0.jar"
	require.Contains(t, []int{200, 201}, doReq(r, http.MethodPut, "/repository/internal"+p, "PRIVATE").Code)

	w := doReq(r, http.MethodGet, "/repository/mg"+p, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "PRIVATE", w.Body.String())
	assert.Equal(t, "internal", w.Header().Get("X-Nexspence-Source"))
	assert.Zero(t, hits.Load(), "blocked path must not reach the proxy upstream")
}

func TestGroup_ProxyRuleBlock_MissNeverReachesUpstream(t *testing.T) {
	srv, hits := countingUpstream(t, "PUBLIC")
	r := mavenGroupWithBlockedProxy(t, srv.URL)

	w := doReq(r, http.MethodGet, "/repository/mg/com/acme/other/2.0/other-2.0.jar", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Zero(t, hits.Load())
}

func TestGroup_ProxyRuleBlock_IndexNotAskedOfProxy(t *testing.T) {
	srv, hits := countingUpstream(t, "<metadata><versioning><versions><version>9.9</version></versions></versioning></metadata>")
	r := mavenGroupWithBlockedProxy(t, srv.URL)
	meta := "<metadata><groupId>com.acme</groupId><artifactId>lib</artifactId><versioning><versions><version>1.0</version></versions></versioning></metadata>"
	require.Contains(t, []int{200, 201}, doReq(r, http.MethodPut, "/repository/internal/com/acme/lib/maven-metadata.xml", meta).Code)

	w := doReq(r, http.MethodGet, "/repository/mg/com/acme/lib/maven-metadata.xml", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "<version>1.0</version>")
	assert.NotContains(t, w.Body.String(), "9.9")
	assert.Equal(t, "internal", w.Header().Get("X-Nexspence-Source"))
	assert.Zero(t, hits.Load())
}

func TestGroup_ProxyRuleAllowsOtherPaths(t *testing.T) {
	srv, hits := countingUpstream(t, "PUBLIC")
	r := mavenGroupWithBlockedProxy(t, srv.URL)

	w := doReq(r, http.MethodGet, "/repository/mg/org/x/x/1.0/x-1.0.jar", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "PUBLIC", w.Body.String())
	assert.Equal(t, int32(1), hits.Load())
}
