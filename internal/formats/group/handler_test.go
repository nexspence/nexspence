package group_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/raw"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { gin.SetMode(gin.TestMode) }

func makeGroupRepo(name string, members ...string) *domain.Repository {
	memberSlice := make([]interface{}, len(members))
	for i, m := range members {
		memberSlice[i] = m
	}
	return &domain.Repository{
		ID: "repo-" + name, Name: name, Format: "raw",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": memberSlice},
	}
}

func buildEngine(repos ...*domain.Repository) *gin.Engine {
	repoRepo := testutil.NewRepoRepo(repos...)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}

	rawH := raw.New(d)
	registry := map[string]formats.FormatHandler{"raw": rawH}
	groupH := group.New(d, registry)

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repoName := c.Param("repoName")
		repo, _ := repoRepo.Get(c.Request.Context(), repoName)
		if repo == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
		} else {
			rawH.ServeHTTP(c)
		}
	})
	return r
}

func put(r *gin.Engine, repoName, path, body string) int {
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+path, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestGroup_FirstMemberServes(t *testing.T) {
	m1 := testutil.SimpleRepo("m1", "raw")
	m2 := testutil.SimpleRepo("m2", "raw")
	grp := makeGroupRepo("grp", "m1", "m2")
	r := buildEngine(m1, m2, grp)

	require.Equal(t, http.StatusCreated, put(r, "m1", "/file.txt", "hello from m1"))

	req := httptest.NewRequest(http.MethodGet, "/repository/grp/file.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "hello from m1", w.Body.String())
	assert.Equal(t, "m1", w.Header().Get("X-Nexspence-Source"))
}

func TestGroup_SkipsFirstMember_UsesSecond(t *testing.T) {
	m1 := testutil.SimpleRepo("skip1", "raw") // empty
	m2 := testutil.SimpleRepo("skip2", "raw") // has file
	grp := makeGroupRepo("grp2", "skip1", "skip2")
	r := buildEngine(m1, m2, grp)

	require.Equal(t, http.StatusCreated, put(r, "skip2", "/artifact.bin", "from second"))

	req := httptest.NewRequest(http.MethodGet, "/repository/grp2/artifact.bin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "from second", w.Body.String())
	assert.Equal(t, "skip2", w.Header().Get("X-Nexspence-Source"))
}

func TestGroup_AllMissing_Returns404(t *testing.T) {
	m1 := testutil.SimpleRepo("nn1", "raw")
	m2 := testutil.SimpleRepo("nn2", "raw")
	grp := makeGroupRepo("grp3", "nn1", "nn2")
	r := buildEngine(m1, m2, grp)

	req := httptest.NewRequest(http.MethodGet, "/repository/grp3/missing.bin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGroup_PUT_Returns405_ProxyOnly: group with no hosted members → 405
func TestGroup_PUT_Returns405_ProxyOnly(t *testing.T) {
	proxy := &domain.Repository{
		ID: "repo-px", Name: "px-only", Format: "raw",
		Type: domain.TypeProxy, Online: true,
	}
	grp := makeGroupRepo("grp-proxy-only", "px-only")
	r := buildEngine(proxy, grp)

	req := httptest.NewRequest(http.MethodPut, "/repository/grp-proxy-only/file.txt", strings.NewReader("x"))
	req.ContentLength = 1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestGroup_PUT_ForwardsToFirstHosted: PUT on group → stores artifact in first hosted member
func TestGroup_PUT_ForwardsToFirstHosted(t *testing.T) {
	m1 := testutil.SimpleRepo("hw1", "raw")
	grp := makeGroupRepo("grp-write", "hw1")
	r := buildEngine(m1, grp)

	code := put(r, "grp-write", "/uploaded.txt", "via group")
	assert.Equal(t, http.StatusCreated, code)

	req := httptest.NewRequest(http.MethodGet, "/repository/hw1/uploaded.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "via group", w.Body.String())
}

// TestGroup_PUT_SkipsProxyUsesHosted: first member is proxy, second is hosted
func TestGroup_PUT_SkipsProxyUsesHosted(t *testing.T) {
	proxy := &domain.Repository{
		ID: "repo-px2", Name: "px2", Format: "raw",
		Type: domain.TypeProxy, Online: true,
	}
	hosted := testutil.SimpleRepo("hx2", "raw")
	grp := makeGroupRepo("grp-mixed", "px2", "hx2")
	r := buildEngine(proxy, hosted, grp)

	code := put(r, "grp-mixed", "/art.bin", "from mixed")
	assert.Equal(t, http.StatusCreated, code)

	req := httptest.NewRequest(http.MethodGet, "/repository/hx2/art.bin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "from mixed", w.Body.String())
}

// TestGroup_PUT_UsesWritableMemberConfig: explicit writable_member overrides auto-detect
func TestGroup_PUT_UsesWritableMemberConfig(t *testing.T) {
	m1 := testutil.SimpleRepo("wm1", "raw")
	m2 := testutil.SimpleRepo("wm2", "raw")
	grp := &domain.Repository{
		ID: "repo-grp-wm", Name: "grp-wm", Format: "raw",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{
			"member_names":    []interface{}{"wm1", "wm2"},
			"writable_member": "wm2",
		},
	}
	r := buildEngine(m1, m2, grp)

	code := put(r, "grp-wm", "/targeted.txt", "to m2")
	assert.Equal(t, http.StatusCreated, code)

	req1 := httptest.NewRequest(http.MethodGet, "/repository/wm1/targeted.txt", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusNotFound, w1.Code)

	req2 := httptest.NewRequest(http.MethodGet, "/repository/wm2/targeted.txt", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "to m2", w2.Body.String())
}

func TestGroup_EmptyMembers_Returns404(t *testing.T) {
	grp := &domain.Repository{
		ID: "grp-empty", Name: "grp-empty", Format: "raw",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []interface{}{}},
	}
	r := buildEngine(grp)

	req := httptest.NewRequest(http.MethodGet, "/repository/grp-empty/x.bin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGroup_HEAD_NoBody(t *testing.T) {
	m := testutil.SimpleRepo("hm", "raw")
	grp := makeGroupRepo("grp-head", "hm")
	r := buildEngine(m, grp)

	require.Equal(t, http.StatusCreated, put(r, "hm", "/check.txt", "content"))

	req := httptest.NewRequest(http.MethodHead, "/repository/grp-head/check.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())
}

func makeBlockRule(id string, matchers ...string) *domain.RoutingRule {
	return &domain.RoutingRule{ID: id, Mode: "BLOCK", Matchers: matchers}
}

func makeAllowRule(id string, matchers ...string) *domain.RoutingRule {
	return &domain.RoutingRule{ID: id, Mode: "ALLOW", Matchers: matchers}
}

func buildEngineWithRule(rule *domain.RoutingRule, repos ...*domain.Repository) *gin.Engine {
	repoRepo := testutil.NewRepoRepo(repos...)
	rrRepo := testutil.NewRoutingRuleRepo()
	if rule != nil {
		_ = rrRepo.Create(context.Background(), rule)
	}
	d := formats.Deps{
		Repos:        repoRepo,
		Blobs:        testutil.NewBlobStoreRepo(),
		Components:   testutil.NewComponentRepo(),
		Assets:       testutil.NewAssetRepo(),
		BlobStore:    testutil.NewBlobStore(),
		BaseURL:      "http://localhost:8080",
		RoutingRules: rrRepo,
	}

	rawH := raw.New(d)
	registry := map[string]formats.FormatHandler{"raw": rawH}
	groupH := group.New(d, registry)

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repoName := c.Param("repoName")
		repo, _ := repoRepo.Get(c.Request.Context(), repoName)
		if repo == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
		} else {
			rawH.ServeHTTP(c)
		}
	})
	return r
}

func TestGroupHandler_RoutingRule_BlocksPath(t *testing.T) {
	rule := makeBlockRule("rule-1", `.*-SNAPSHOT.*`)

	member := testutil.SimpleRepo("snapshots", "raw")
	ruleID := "rule-1"
	grp := &domain.Repository{
		ID: "repo-grp", Name: "mygroup", Format: "raw",
		Type: domain.TypeGroup, Online: true,
		RoutingRuleID: &ruleID,
		FormatConfig:  map[string]any{"member_names": []any{"snapshots"}},
	}

	r := buildEngineWithRule(rule, member, grp)
	require.Equal(t, http.StatusCreated, put(r, "snapshots", "/foo-1.0-SNAPSHOT.jar", "data"))

	req := httptest.NewRequest(http.MethodGet, "/repository/mygroup/foo-1.0-SNAPSHOT.jar", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGroupHandler_RoutingRule_AllowsMatchingPath(t *testing.T) {
	rule := makeAllowRule("rule-2", `^/releases/`)

	member := testutil.SimpleRepo("releases", "raw")
	ruleID := "rule-2"
	grp := &domain.Repository{
		ID: "repo-grp2", Name: "mygroup2", Format: "raw",
		Type: domain.TypeGroup, Online: true,
		RoutingRuleID: &ruleID,
		FormatConfig:  map[string]any{"member_names": []any{"releases"}},
	}

	r := buildEngineWithRule(rule, member, grp)
	require.Equal(t, http.StatusCreated, put(r, "releases", "/releases/foo-1.0.jar", "data"))

	req := httptest.NewRequest(http.MethodGet, "/repository/mygroup2/releases/foo-1.0.jar", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	req2 := httptest.NewRequest(http.MethodGet, "/repository/mygroup2/snapshots/bar.jar", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusNotFound, w2.Code)
}
