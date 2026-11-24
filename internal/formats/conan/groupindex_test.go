package conan_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/conan"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// conanGroupEnv is a conan group repository "g" over the given members, with
// the group layer in front of them — the layer the merger is written for.
type conanGroupEnv struct {
	engine *gin.Engine
	assets *testutil.AssetRepo
}

// setupConanGroup builds the group "g" over the given conan members.
func setupConanGroup(t *testing.T, members ...*domain.Repository) *conanGroupEnv {
	t.Helper()
	names := make([]any, 0, len(members))
	for _, m := range members {
		names = append(names, m.Name)
	}
	g := &domain.Repository{
		ID: "repo-g", Name: "g", Format: "conan", Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": names},
	}
	repos := append([]*domain.Repository{g}, members...)
	repoRepo := testutil.NewRepoRepo(repos...)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	conanH := conan.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"conan": conanH})
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		conanH.ServeHTTP(c)
	})
	return &conanGroupEnv{engine: r, assets: d.Assets.(*testutil.AssetRepo)}
}

// hostedMember is a hosted conan repository.
func hostedMember(name string) *domain.Repository {
	return testutil.SimpleRepo(name, "conan")
}

// proxyMember is a proxy conan repository — its search answers from the
// recipes it has cached, the way a proxy behind center.conan.io does.
func proxyMember(name string) *domain.Repository {
	repo := testutil.SimpleRepo(name, "conan")
	repo.Type = domain.TypeProxy
	repo.ProxyConfig = map[string]any{"remote_url": "http://127.0.0.1:1"}
	return repo
}

// seedRecipe stores a recipe revision under a repository, the way a member's
// storage holds the recipes it serves.
func seedRecipe(t *testing.T, env *conanGroupEnv, repo, name, version, user, channel string) {
	t.Helper()
	require.NoError(t, env.assets.Create(context.Background(), &domain.Asset{
		Repository: repo,
		Path: fmt.Sprintf("/v2/conans/%s/%s/%s/%s/revisions/r1/files/conanfile.py",
			name, version, user, channel),
	}))
}

// seedBinary stores one binary of a recipe revision.
func seedBinary(t *testing.T, env *conanGroupEnv, repo, name, version, user, channel, pkgID string) {
	t.Helper()
	require.NoError(t, env.assets.Create(context.Background(), &domain.Asset{
		Repository: repo,
		Path: fmt.Sprintf("/v2/conans/%s/%s/%s/%s/revisions/r1/packages/%s/revisions/p1/files/conaninfo.txt",
			name, version, user, channel, pkgID),
	}))
}

// conanSearchResults reads the result list of a Conan search answer.
func conanSearchResults(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	var doc struct {
		Results []string `json:"results"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	return doc.Results
}

func TestConan_GroupIndexSourcePath(t *testing.T) {
	h := conan.New(formats.Deps{})

	src, ok := h.GroupIndexSourcePath("/v2/conans/search")
	require.True(t, ok, "the repository-wide recipe search is the aggregated index")
	assert.Equal(t, "/v2/conans/search", src)

	_, ok = h.GroupIndexSourcePath("/v2/conans/boost/1.83.0/_/_/search")
	assert.False(t, ok, "the package-level search keeps the first-non-404 fan-out")
	_, ok = h.GroupIndexSourcePath("/v2/conans/boost/1.83.0/_/_/latest")
	assert.False(t, ok, "a recipe revision is not an index")
	_, ok = h.GroupIndexSourcePath("/files/boost/1.83.0/_/_/0/export/conanfile.py")
	assert.False(t, ok, "an artifact is not an index")
}

func TestConan_MergeGroupIndex_UnionsResults(t *testing.T) {
	h := conan.New(formats.Deps{})
	body, ct, err := h.MergeGroupIndex("g", "/v2/conans/search", []formats.GroupIndexPart{
		{Member: "m1", Body: []byte(`{"results":["zlib/1.3.1","boost/1.83.0"]}`)},
		{Member: "m2", Body: []byte(`{"results":["zlib/1.3.1","fmt/10.2.1"]}`)},
	})
	require.NoError(t, err)
	assert.Contains(t, ct, "json")

	var doc struct {
		Results []string `json:"results"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	// The union, with the recipe both members hold contributed once and the
	// first member's entry first — member order is the priority order.
	assert.Equal(t, []string{"zlib/1.3.1", "boost/1.83.0", "fmt/10.2.1"}, doc.Results)
}

func TestConan_MergeGroupIndex_MalformedPartSkipped(t *testing.T) {
	h := conan.New(formats.Deps{})
	body, _, err := h.MergeGroupIndex("g", "/v2/conans/search", []formats.GroupIndexPart{
		{Member: "bad", Body: []byte("nope")},
		{Member: "ok", Body: []byte(`{"results":["zlib/1.3.1"]}`)},
	})
	require.NoError(t, err, "a malformed member body must not break the merge")
	assert.Contains(t, string(body), "zlib/1.3.1")
}

func TestConan_MergeGroupIndex_NoParsableMemberFails(t *testing.T) {
	h := conan.New(formats.Deps{})
	_, _, err := h.MergeGroupIndex("g", "/v2/conans/search", []formats.GroupIndexPart{
		{Member: "bad", Body: []byte("nope")},
		{Member: "worse", Body: []byte(`{"results":"not a list"}`)},
	})
	require.Error(t, err, "no member body could be parsed")
}

func TestConan_MergeGroupIndex_EmptyResultsStayAList(t *testing.T) {
	// The merged document must have the shape a Conan client reads: a list
	// under "results", never null.
	h := conan.New(formats.Deps{})
	body, _, err := h.MergeGroupIndex("g", "/v2/conans/search", []formats.GroupIndexPart{
		{Member: "m1", Body: []byte(`{"results":[]}`)},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"results":[]}`, string(body))
}

// The bug the merger exists for: the hosted member answers the search with an
// empty list and shadowed the proxy member behind it, so `conan search` showed
// nothing at all.
func TestConan_GroupSearch_EmptyHostedMemberDoesNotShadowProxy(t *testing.T) {
	env := setupConanGroup(t, hostedMember("local"), proxyMember("remote"))
	seedRecipe(t, env, "remote", "zlib", "1.3.1", "_", "_")

	w := v2Get(env.engine, "/repository/g/v2/conans/search?q=zlib*")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, []string{"zlib/1.3.1"}, conanSearchResults(t, w))
}

func TestConan_GroupSearch_ServesTheUnionOfItsMembers(t *testing.T) {
	env := setupConanGroup(t, hostedMember("local"), proxyMember("remote"))
	seedRecipe(t, env, "local", "zlib", "1.3.1", "_", "_")
	seedRecipe(t, env, "local", "boost", "1.83.0", "_", "_")
	seedRecipe(t, env, "remote", "zlib", "1.3.1", "_", "_")
	seedRecipe(t, env, "remote", "fmt", "10.2.1", "_", "_")

	w := v2Get(env.engine, "/repository/g/v2/conans/search?q=*")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	results := conanSearchResults(t, w)
	assert.ElementsMatch(t, []string{"zlib/1.3.1", "boost/1.83.0", "fmt/10.2.1"}, results)
	assert.Equal(t, 1, countOf(results, "zlib/1.3.1"), "a recipe both members hold appears once")
	assert.Equal(t, []string{"boost/1.83.0", "zlib/1.3.1"}, results[:2],
		"the first member's contribution comes first")
}

// The package-level search is not merged: it describes the binaries of one
// recipe, and a member without that recipe answers 404, so the fan-out
// already reaches the member holding it.
func TestConan_GroupSearch_PackageSearchKeepsFirstNon404(t *testing.T) {
	env := setupConanGroup(t, hostedMember("local"), proxyMember("remote"))
	seedRecipe(t, env, "remote", "zlib", "1.3.1", "_", "_")
	seedBinary(t, env, "remote", "zlib", "1.3.1", "_", "_", "pkgA")

	w := v2Get(env.engine, "/repository/g/v2/conans/zlib/1.3.1/_/_/search?list_only=True")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out map[string]map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	assert.Contains(t, out, "pkgA")
	assert.Equal(t, "remote", w.Header().Get("X-Nexspence-Source"))
}

// countOf counts the occurrences of v in list.
func countOf(list []string, v string) int {
	n := 0
	for _, s := range list {
		if s == v {
			n++
		}
	}
	return n
}
