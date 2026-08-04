package oci_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ── Helpers ───────────────────────────────────────────────────

const pagingManifest = `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}`

// pushTags pushes the same manifest body under each tag, which is what a client
// re-tagging one image does.
func pushTags(t *testing.T, r *gin.Engine, repoName, imageName string, tags ...string) {
	t.Helper()
	for _, tag := range tags {
		req := httptest.NewRequest(http.MethodPut,
			fmt.Sprintf("/repository/%s/v2/%s/manifests/%s", repoName, imageName, url.PathEscape(tag)),
			strings.NewReader(pagingManifest))
		req.ContentLength = int64(len(pagingManifest))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusCreated, w.Code, "push tag %q: %s", tag, w.Body.String())
	}
}

// getTags issues a tags/list request and decodes the tag names plus the raw Link header.
func getTags(t *testing.T, r *gin.Engine, target string) (tags []string, link string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "GET %s: %s", target, w.Body.String())
	var body struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Tags, w.Header().Get("Link")
}

// parseNextLink splits a `</path?query>; rel="next"` header into the URL it names.
func parseNextLink(t *testing.T, link string) *url.URL {
	t.Helper()
	require.NotEmpty(t, link, "Link header expected")
	require.True(t, strings.HasSuffix(link, `>; rel="next"`),
		`Link must be formatted <url>; rel="next", got %q`, link)
	raw := strings.TrimSuffix(strings.TrimPrefix(link, "<"), `>; rel="next"`)
	require.True(t, strings.HasPrefix(link, "<"), "Link must start with '<', got %q", link)
	u, err := url.Parse(raw)
	require.NoError(t, err, "Link URL must parse: %q", raw)
	return u
}

// ── The spec's paging shape ───────────────────────────────────

func TestTagsList_FirstPageIsSortedAndLinksToNext(t *testing.T) {
	repo := testutil.SimpleRepo("pag1", "docker")
	r, _ := setupWithDeps(repo)
	pushTags(t, r, "pag1", "myapp", "v1.2", "v1.0", "v2.0", "v1.10", "v1.1")

	tags, link := getTags(t, r, "/repository/pag1/v2/myapp/tags/list?n=2")
	assert.Equal(t, []string{"v1.0", "v1.1"}, tags, "first page must be the lexically first two tags")

	u := parseNextLink(t, link)
	assert.Equal(t, "2", u.Query().Get("n"))
	assert.Equal(t, "v1.1", u.Query().Get("last"), "cursor must name the last tag of this page")
}

func TestTagsList_FollowingLinksReachesTheEnd(t *testing.T) {
	repo := testutil.SimpleRepo("pag2", "docker")
	r, _ := setupWithDeps(repo)
	pushTags(t, r, "pag2", "myapp", "v1.0", "v1.1", "v1.2", "v1.3", "v1.4")

	tags, link := getTags(t, r, "/repository/pag2/v2/myapp/tags/list?n=2")
	require.Equal(t, []string{"v1.0", "v1.1"}, tags)

	tags2, link2 := getTags(t, r, parseNextLink(t, link).String())
	assert.Equal(t, []string{"v1.2", "v1.3"}, tags2)

	tags3, link3 := getTags(t, r, parseNextLink(t, link2).String())
	assert.Equal(t, []string{"v1.4"}, tags3)
	assert.Empty(t, link3, "the final page must carry no Link header")
}

func TestTagsList_NoLimitReturnsEverything(t *testing.T) {
	repo := testutil.SimpleRepo("pag3", "docker")
	r, _ := setupWithDeps(repo)
	all := []string{"v1.0", "v1.1", "v1.2", "v1.3", "v1.4"}
	pushTags(t, r, "pag3", "myapp", all...)

	for _, target := range []string{
		"/repository/pag3/v2/myapp/tags/list",
		"/repository/pag3/v2/myapp/tags/list?n=0",
	} {
		tags, link := getTags(t, r, target)
		assert.Equal(t, all, tags, "GET %s must return every tag", target)
		assert.Empty(t, link, "GET %s must carry no Link header", target)
	}
}

func TestTagsList_LastWithoutNReturnsTheRemainder(t *testing.T) {
	repo := testutil.SimpleRepo("pag4", "docker")
	r, _ := setupWithDeps(repo)
	pushTags(t, r, "pag4", "myapp", "v1.0", "v1.1", "v1.2", "v1.3", "v1.4")

	tags, link := getTags(t, r, "/repository/pag4/v2/myapp/tags/list?last=v1.2")
	assert.Equal(t, []string{"v1.3", "v1.4"}, tags)
	assert.Empty(t, link)
}

func TestTagsList_NBiggerThanTagCount(t *testing.T) {
	repo := testutil.SimpleRepo("pag5", "docker")
	r, _ := setupWithDeps(repo)
	all := []string{"v1.0", "v1.1", "v1.2"}
	pushTags(t, r, "pag5", "myapp", all...)

	tags, link := getTags(t, r, "/repository/pag5/v2/myapp/tags/list?n=50")
	assert.Equal(t, all, tags)
	assert.Empty(t, link, "nothing was truncated, so there is no next page")
}

// TestTagsList_LinkKeepsTheClientsPathForm covers both mounts a Docker client can
// reach: the long /repository/<repo>/v2/... form and the short /v2/<repo>/... one
// that setupV2Scoped mimics. A client on the short path must not be handed a
// long-path link — it only sends its credentials to /v2/ (issue #47).
func TestTagsList_LinkKeepsTheClientsPathForm(t *testing.T) {
	t.Run("long form", func(t *testing.T) {
		repo := testutil.SimpleRepo("pag6", "docker")
		r, _ := setupWithDeps(repo)
		pushTags(t, r, "pag6", "myapp", "v1.0", "v1.1", "v1.2")

		_, link := getTags(t, r, "/repository/pag6/v2/myapp/tags/list?n=1")
		assert.Equal(t, "/repository/pag6/v2/myapp/tags/list", parseNextLink(t, link).Path)
	})

	t.Run("short form", func(t *testing.T) {
		repo := testutil.SimpleRepo("pag6s", "docker")
		r := setupV2Scoped(repo)
		for _, tag := range []string{"v1.0", "v1.1", "v1.2"} {
			req := httptest.NewRequest(http.MethodPut, "/v2/pag6s/myapp/manifests/"+tag,
				strings.NewReader(pagingManifest))
			req.ContentLength = int64(len(pagingManifest))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		}

		_, link := getTags(t, r, "/v2/pag6s/myapp/tags/list?n=1")
		u := parseNextLink(t, link)
		assert.Equal(t, "/v2/pag6s/myapp/tags/list", u.Path)

		// The link must be servable as-is.
		tags2, _ := getTags(t, r, u.String())
		assert.Equal(t, []string{"v1.1"}, tags2)
	})
}

// A tag containing a character that has to be escaped in a query string must
// survive the round trip through the Link header.
func TestTagsList_CursorSurvivesEscaping(t *testing.T) {
	repo := testutil.SimpleRepo("pag7", "docker")
	r, d := setupWithDeps(repo)
	// Written straight to the store: the odd characters are about the query
	// string, not about what a push URL accepts.
	seedTags(t, d, "pag7", "myapp", "v1+a", "v1 b", "v1&c", "v2")

	tags, link := getTags(t, r, "/repository/pag7/v2/myapp/tags/list?n=3")
	require.Equal(t, []string{"v1 b", "v1&c", "v1+a"}, tags)

	u := parseNextLink(t, link)
	assert.Equal(t, "v1+a", u.Query().Get("last"), "cursor must decode back to the exact tag")

	rest, link2 := getTags(t, r, u.String())
	assert.Equal(t, []string{"v2"}, rest)
	assert.Empty(t, link2)
}

// ── The 500-row search cap ────────────────────────────────────

// TestTagsList_PagesPastTheSearchCap proves a repository with more tags than the
// component search will return in one call is still fully enumerable by following
// links. The search is wrapped in cappedSearch, which reproduces the SQL clamp.
func TestTagsList_PagesPastTheSearchCap(t *testing.T) {
	repo := testutil.SimpleRepo("pag8", "docker")
	comps := &cappedSearch{ComponentRepo: testutil.NewComponentRepo()}
	r := setupWithComponents(repo, comps)

	const total = 600
	want := make([]string, 0, total)
	for i := 0; i < total; i++ {
		tag := fmt.Sprintf("v%04d", i)
		want = append(want, tag)
		require.NoError(t, comps.Create(context.Background(), &domain.Component{
			RepositoryID: repo.ID, Repository: repo.Name, Format: "docker",
			Name: "myapp", Version: tag,
		}))
	}
	sort.Strings(want)

	// One shot, no n: every tag, past the cap.
	all, link := getTags(t, r, "/repository/pag8/v2/myapp/tags/list")
	require.Len(t, all, total, "an uncapped listing must enumerate all %d tags", total)
	assert.Equal(t, want, all, "an uncapped listing must enumerate all %d tags", total)
	assert.Empty(t, link)

	// And page by page: 7 pages of 100 (the last one short) must cover the same set.
	var got []string
	target := "/repository/pag8/v2/myapp/tags/list?n=100"
	for pages := 0; ; pages++ {
		require.Less(t, pages, 20, "following links must terminate")
		page, l := getTags(t, r, target)
		got = append(got, page...)
		if l == "" {
			break
		}
		target = parseNextLink(t, l).String()
	}
	assert.Equal(t, want, got, "paging must enumerate every tag exactly once")
}

// cappedSearch reproduces the component search's storage semantics on top of the
// in-memory mock, whose own Search ignores Limit/Offset entirely: the SQL
// implementation (internal/repository/postgres/component_repo.go) clamps a Limit
// above 500 back down to 50, filters the name with a substring ILIKE, orders by
// (name, version) and hands back a continuation token when more rows remain.
type cappedSearch struct {
	*testutil.ComponentRepo
}

func (c *cappedSearch) Search(ctx context.Context, p domain.SearchParams) (*domain.Page[domain.Component], error) {
	page, err := c.ListByRepoNames(ctx, []string{p.Repository}, 0, 0)
	if err != nil {
		return nil, err
	}
	items := make([]domain.Component, 0, len(page.Items))
	for _, comp := range page.Items {
		if p.Name != "" && !strings.Contains(strings.ToLower(comp.Name), strings.ToLower(p.Name)) {
			continue
		}
		items = append(items, comp)
	}
	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if p.Offset >= len(items) {
		return &domain.Page[domain.Component]{Items: nil}, nil
	}
	items = items[p.Offset:]
	var token *string
	if len(items) > limit {
		items = items[:limit]
		next := fmt.Sprintf("%d", p.Offset+limit)
		token = &next
	}
	return &domain.Page[domain.Component]{Items: items, ContinuationToken: token}, nil
}

// setupWithComponents is setupWithDeps with a caller-supplied component repo.
func setupWithComponents(repo *domain.Repository, comps repository.ComponentRepo) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: comps,
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := oci.New(d)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := requestctx.WithUser(c.Request.Context(), "test-user-id", "testuser")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// seedTags writes components straight to the store, for tag names a push URL
// cannot carry.
func seedTags(t *testing.T, d formats.Deps, repoName, imageName string, tags ...string) {
	t.Helper()
	for _, tag := range tags {
		require.NoError(t, d.Components.Create(context.Background(), &domain.Component{
			RepositoryID: "repo-" + repoName, Repository: repoName, Format: "docker",
			Name: imageName, Version: tag,
		}))
	}
}
