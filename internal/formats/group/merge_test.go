package group_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// mergeFmt is a fake format handler implementing formats.GroupIndexMerger.
// Members serve "<member-body>" at /index (from FormatConfig["body"]) and 404
// elsewhere; the merger concatenates parts as "member=body;".
type mergeFmt struct {
	deps     formats.Deps
	failWith error // when non-nil, MergeGroupIndex fails
	// fetchPath, when set, makes this a dependent merger that asks the group
	// layer for that path and serves whatever comes back.
	fetchPath string
}

func (m *mergeFmt) Name() string { return "mergefmt" }

func (m *mergeFmt) ServeHTTP(c *gin.Context) {
	repoName := c.Param("repoName")
	// Real format handlers normalize before they look anything up — that gap
	// between the path the group checked and the path a member resolves is what
	// the traversal guard exists for, so the fake models it.
	p := path.Clean("/" + strings.TrimPrefix(c.Param("path"), "/"))
	repo, _ := m.deps.Repos.Get(c.Request.Context(), repoName)
	if p == "/secret" {
		c.String(http.StatusOK, "SECRET")
		return
	}
	if p == "/index" && repo != nil {
		body, _ := repo.FormatConfig["body"].(string)
		if body == "FAIL" {
			c.String(http.StatusBadGateway, "upstream down")
			return
		}
		// An empty body still returns 200 — models the 200-on-empty
		// shadowing formats (pypi simple, nuget version list).
		c.String(http.StatusOK, body)
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
}

func (m *mergeFmt) GroupIndexSourcePath(path string) (string, bool) {
	if path == "/index" || path == "/index.sha1" {
		return "/index", true
	}
	return "", false
}

func (m *mergeFmt) MergeGroupIndex(groupName, path string, parts []formats.GroupIndexPart) ([]byte, string, error) {
	if m.failWith != nil {
		return nil, "", m.failWith
	}
	var sb strings.Builder
	for _, p := range parts {
		fmt.Fprintf(&sb, "%s=%s;", p.Member, p.Body)
	}
	if path == "/index.sha1" {
		return []byte("sha1(" + sb.String() + ")"), "text/plain", nil
	}
	return []byte(sb.String()), "text/merged", nil
}

// MergeGroupIndexWithFetch implements formats.GroupIndexDependentMerger for the
// tests that need one. Only used when fetchPath is set.
func (m *mergeFmt) MergeGroupIndexWithFetch(groupName, path string, parts []formats.GroupIndexPart,
	fetch formats.GroupIndexFetcher,
) ([]byte, string, error) {
	if m.fetchPath == "" {
		return m.MergeGroupIndex(groupName, path, parts)
	}
	body, err := fetch(m.fetchPath)
	if err != nil {
		// Surfaced as the document body so a test can read why the fetch was
		// refused; a real merger would decide what a missing sub-index means.
		return []byte("fetch refused: " + err.Error()), "text/plain", nil //nolint:nilerr // the refusal IS this fake's result
	}
	return append([]byte("fetched:"), body...), "text/plain", nil
}

func mergeMember(name, body string) *domain.Repository {
	return &domain.Repository{
		ID: "repo-" + name, Name: name, Format: "mergefmt",
		Type: domain.TypeHosted, Online: true,
		FormatConfig: map[string]any{"body": body},
	}
}

func mergeGroup(name string, members ...string) *domain.Repository {
	ms := make([]interface{}, len(members))
	for i, m := range members {
		ms[i] = m
	}
	return &domain.Repository{
		ID: "repo-" + name, Name: name, Format: "mergefmt",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": ms},
	}
}

func buildMergeEngine(failWith error, repos ...*domain.Repository) *gin.Engine {
	repoRepo := testutil.NewRepoRepo(repos...)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	mh := &mergeFmt{deps: d, failWith: failWith}
	registry := map[string]formats.FormatHandler{"mergefmt": mh}
	groupH := group.New(d, registry)

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		mh.ServeHTTP(c)
	})
	return r
}

func get(r *gin.Engine, url string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestGroupMerge_UnionAcrossMembers(t *testing.T) {
	r := buildMergeEngine(nil,
		mergeGroup("g", "m1", "m2"),
		mergeMember("m1", "aaa"), mergeMember("m2", "bbb"))

	w := get(r, "/repository/g/index")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "m1=aaa;m2=bbb;", w.Body.String())
	assert.Equal(t, "text/merged", w.Header().Get("Content-Type"))
	assert.Equal(t, "m1,m2", w.Header().Get("X-Nexspence-Source"))
}

func TestGroupMerge_EmptyMemberDoesNotShadow(t *testing.T) {
	// The 200-on-empty member must not stop the fan-out (#99 shadowing).
	r := buildMergeEngine(nil,
		mergeGroup("g2", "empty", "full"),
		mergeMember("empty", ""), mergeMember("full", "pkg"))

	w := get(r, "/repository/g2/index")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "full=pkg;")
}

func TestGroupMerge_FailingMemberSkipped(t *testing.T) {
	r := buildMergeEngine(nil,
		mergeGroup("g3", "down", "ok"),
		mergeMember("down", "FAIL"), mergeMember("ok", "xyz"))

	w := get(r, "/repository/g3/index")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ok=xyz;", w.Body.String())
	assert.Equal(t, "ok", w.Header().Get("X-Nexspence-Source"))
}

func TestGroupMerge_SourcePathMapping(t *testing.T) {
	// /index.sha1 fans out on /index and returns the merged checksum doc.
	r := buildMergeEngine(nil,
		mergeGroup("g4", "m1", "m2"),
		mergeMember("m1", "a"), mergeMember("m2", "b"))

	w := get(r, "/repository/g4/index.sha1")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "sha1(m1=a;m2=b;)", w.Body.String())
}

func TestGroupMerge_AllMiss404(t *testing.T) {
	r := buildMergeEngine(nil,
		mergeGroup("g5", "down1", "down2"),
		mergeMember("down1", "FAIL"), mergeMember("down2", "FAIL"))

	w := get(r, "/repository/g5/index")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGroupMerge_MergeErrorServesFirstPart(t *testing.T) {
	r := buildMergeEngine(fmt.Errorf("boom"),
		mergeGroup("g6", "m1", "m2"),
		mergeMember("m1", "first"), mergeMember("m2", "second"))

	w := get(r, "/repository/g6/index")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "first", w.Body.String(), "merge failure degrades to the first member body")
}

func TestGroupMerge_NonIndexPathKeepsFirstNon404(t *testing.T) {
	// Artifact paths keep the lazy first-non-404 behavior.
	r := buildMergeEngine(nil,
		mergeGroup("g7", "m1", "m2"),
		mergeMember("m1", "a"), mergeMember("m2", "b"))

	w := get(r, "/repository/g7/some/artifact.bin")
	assert.Equal(t, http.StatusNotFound, w.Code) // members 404 everything but /index
}

// A merger builds sub-index paths out of its members' documents — untrusted
// content for a proxy member — so the group refuses a traversal path there too,
// instead of trusting the merger to have validated it.
func TestGroupMerge_SubIndexFetchRefusesTraversal(t *testing.T) {
	repoRepo := testutil.NewRepoRepo(mergeGroup("gt", "m1"), mergeMember("m1", "aaa"))
	d := formats.Deps{
		Repos: repoRepo, Blobs: testutil.NewBlobStoreRepo(), Components: testutil.NewComponentRepo(),
		Assets: testutil.NewAssetRepo(), BlobStore: testutil.NewBlobStore(), BaseURL: "http://localhost:8080",
	}
	mh := &mergeFmt{deps: d, fetchPath: "/index/../secret"}
	groupH := group.New(d, map[string]formats.FormatHandler{"mergefmt": mh})

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		mh.ServeHTTP(c)
	})

	w := get(r, "/repository/gt/index")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `must not contain a ".." segment`)
	assert.NotContains(t, w.Body.String(), "SECRET", "the traversal path must not resolve to the member's artifact")
}
