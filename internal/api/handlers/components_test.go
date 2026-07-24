package handlers_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// mountComponents wires a ComponentHandler over mock repos to a test Gin engine.
func mountComponents(t *testing.T) (*gin.Engine, *testutil.ComponentRepo, *testutil.AssetRepo, *testutil.RepoRepo) {
	t.Helper()
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	repos := testutil.NewRepoRepo()
	h := handlers.NewComponentHandler(comps, assets, repos, "http://localhost")

	r := gin.New()
	r.GET("/service/rest/v1/components", h.List)
	r.GET("/service/rest/v1/components/:id", h.Get)
	r.DELETE("/service/rest/v1/components/:id", h.Delete)
	r.PUT("/service/rest/v1/components/:id/tags", h.SetTags)
	r.GET("/service/rest/v1/search", h.Search)
	r.GET("/service/rest/v1/search/assets", h.SearchAssets)
	r.GET("/api/v1/repositories/:name/quota", h.GetQuota)
	return r, comps, assets, repos
}

// mountComponentsRBAC wires a ComponentHandler with an RBAC service attached and an
// admin-injecting middleware. Admin roles make RBAC filtering a passthrough, so the
// rbacSvc != nil branches (incl. allowAnonMap + FilterComponents/FilterAssets) run
// without blocking results.
func mountComponentsRBAC(t *testing.T) (*gin.Engine, *testutil.ComponentRepo, *testutil.AssetRepo, *testutil.RepoRepo) {
	t.Helper()
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	repos := testutil.NewRepoRepo()
	rbacSvc := service.NewRBACService(emptyRBACRepo{}, repos, zap.NewNop().Sugar())
	h := handlers.NewComponentHandler(comps, assets, repos, "http://localhost").WithRBAC(rbacSvc)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "admin")
		c.Set("roles", []string{"nx-admin"})
		c.Next()
	})
	r.GET("/service/rest/v1/components", h.List)
	r.GET("/service/rest/v1/search", h.Search)
	r.GET("/service/rest/v1/search/assets", h.SearchAssets)
	return r, comps, assets, repos
}

// componentsResp is the {items, continuationToken} envelope used by List/Search.
type componentsResp struct {
	Items             []domain.Component `json:"items"`
	ContinuationToken *string            `json:"continuationToken"`
}

type assetsResp struct {
	Items             []domain.Asset `json:"items"`
	ContinuationToken *string        `json:"continuationToken"`
}

// ── List ──────────────────────────────────────────────────────

func TestComponentHandler_List_MissingRepoParam_400(t *testing.T) {
	r, _, _, _ := mountComponents(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/components", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestComponentHandler_List_HostedRepo_OK(t *testing.T) {
	r, comps, _, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "a", Repository: "raw-host"}))
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "b", Repository: "other"}))

	rec := do(t, r, http.MethodGet, "/service/rest/v1/components?repository=raw-host", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got componentsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Items, 1)
	assert.Equal(t, "raw-host", got.Items[0].Repository)
}

// TestComponentHandler_List_GroupExpansion drives expandGroupMemberRepoNames:
// a group repo whose member_names point to two hosted members must return the
// UNION of components owned by those members, with the member name as the source
// of truth in each component's repository field.
func TestComponentHandler_List_GroupExpansion(t *testing.T) {
	r, comps, _, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "m1", Name: "mem-a", Format: domain.FormatRaw, Type: domain.TypeHosted})
	seedRepo(t, repos, &domain.Repository{ID: "m2", Name: "mem-b", Format: domain.FormatRaw, Type: domain.TypeHosted})
	seedRepo(t, repos, &domain.Repository{
		ID: "g1", Name: "raw-group", Format: domain.FormatRaw, Type: domain.TypeGroup,
		FormatConfig: map[string]any{"member_names": []string{"mem-a", "mem-b"}},
	})
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "from-a", Repository: "mem-a"}))
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "from-b", Repository: "mem-b"}))
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "outsider", Repository: "elsewhere"}))

	rec := do(t, r, http.MethodGet, "/service/rest/v1/components?repository=raw-group", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got componentsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Items, 2, "should be the union of mem-a + mem-b, excluding elsewhere")
	repoSet := map[string]bool{}
	for _, c := range got.Items {
		repoSet[c.Repository] = true
	}
	assert.True(t, repoSet["mem-a"])
	assert.True(t, repoSet["mem-b"])
	assert.False(t, repoSet["elsewhere"])
}

// TestComponentHandler_List_GroupNoMembers returns an empty result when the group
// has no member_names (expansion yields zero names).
func TestComponentHandler_List_GroupNoMembers_EmptyOK(t *testing.T) {
	r, _, _, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "g1", Name: "empty-group", Format: domain.FormatRaw, Type: domain.TypeGroup})
	rec := do(t, r, http.MethodGet, "/service/rest/v1/components?repository=empty-group", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got componentsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got.Items)
}

func TestComponentHandler_List_Pagination_Params(t *testing.T) {
	// limit + continuationToken are parsed without error; non-group repo path.
	r, comps, _, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "a", Repository: "raw-host"}))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/components?repository=raw-host&limit=10&continuationToken=0", nil)
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestComponentHandler_List_IncludesAssets: the browse UI deletes a row by the
// path of its first asset, so the list envelope must carry assets — not just
// component metadata. Without them the UI falls back to the component *name*
// and the delete silently matches nothing (issues #75/#76).
func TestComponentHandler_List_IncludesAssets(t *testing.T) {
	r, comps, assets, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "npm-proxy", Format: domain.FormatNPM, Type: domain.TypeProxy})
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "lodash", Version: "4.17.21", Repository: "npm-proxy"}))
	require.NoError(t, assets.Create(testContext(), &domain.Asset{
		ComponentID: "comp-1", Repository: "npm-proxy", Path: "/lodash/-/lodash-4.17.21.tgz", SizeBytes: 10,
	}))

	rec := do(t, r, http.MethodGet, "/service/rest/v1/components?repository=npm-proxy", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got componentsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Items, 1)
	require.Len(t, got.Items[0].Assets, 1, "list must include the component's assets")
	assert.Equal(t, "/lodash/-/lodash-4.17.21.tgz", got.Items[0].Assets[0].Path)
	assert.Equal(t, "http://localhost/repository/npm-proxy/lodash/-/lodash-4.17.21.tgz",
		got.Items[0].Assets[0].DownloadURL)
}

// Assets are fetched for every listed component in a single batched query.
func TestComponentHandler_List_BatchesAssetLookup(t *testing.T) {
	r, comps, assets, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	for i := 0; i < 3; i++ {
		require.NoError(t, comps.Create(testContext(), &domain.Component{
			Name: fmt.Sprintf("pkg-%d", i), Repository: "raw-host",
		}))
	}
	rec := do(t, r, http.MethodGet, "/service/rest/v1/components?repository=raw-host", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, assets.ListByComponentIDsCalls, "one batched query, not one per component")
	assert.Zero(t, assets.ListByComponentIDCalls)
}

// An asset-repo failure while enriching the list must surface as a 500 rather
// than silently returning components without assets.
func TestComponentHandler_List_AssetRepoError_500(t *testing.T) {
	r, comps, assets, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "a", Repository: "raw-host"}))
	assets.Err = errors.New("asset query failed")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/components?repository=raw-host", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestComponentHandler_List_OffsetParam: the browse UI pages with ?offset=,
// which the handler ignored — every "Next" page re-rendered page 1 (issue #80).
func TestComponentHandler_List_OffsetParam(t *testing.T) {
	r, comps, _, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	for _, n := range []string{"a", "b", "c", "d"} {
		require.NoError(t, comps.Create(testContext(), &domain.Component{Name: n, Repository: "raw-host"}))
	}

	rec := do(t, r, http.MethodGet, "/service/rest/v1/components?repository=raw-host&limit=2&offset=2", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 2, comps.LastListOffset, "offset query param must reach the repo layer")
	var got componentsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Items, 2)
	assert.Equal(t, "c", got.Items[0].Name)
	assert.Equal(t, "d", got.Items[1].Name)
}

// continuationToken keeps working and wins over offset when both are supplied.
func TestComponentHandler_List_ContinuationTokenWinsOverOffset(t *testing.T) {
	r, comps, _, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	for _, n := range []string{"a", "b", "c", "d"} {
		require.NoError(t, comps.Create(testContext(), &domain.Component{Name: n, Repository: "raw-host"}))
	}
	rec := do(t, r, http.MethodGet,
		"/service/rest/v1/components?repository=raw-host&limit=1&offset=2&continuationToken=3", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 3, comps.LastListOffset)
}

// A first page that has more rows behind it advertises the next token.
func TestComponentHandler_List_ContinuationTokenEmitted(t *testing.T) {
	r, comps, _, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	for _, n := range []string{"a", "b", "c"} {
		require.NoError(t, comps.Create(testContext(), &domain.Component{Name: n, Repository: "raw-host"}))
	}
	rec := do(t, r, http.MethodGet, "/service/rest/v1/components?repository=raw-host&limit=2", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got componentsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.NotNil(t, got.ContinuationToken)
	assert.Equal(t, "2", *got.ContinuationToken)
}

func TestComponentHandler_List_ExpandRepoError_500(t *testing.T) {
	r, _, _, repos := mountComponents(t)
	repos.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/components?repository=any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestComponentHandler_List_ComponentRepoError_500(t *testing.T) {
	r, comps, _, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	comps.Err = errors.New("query failed")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/components?repository=raw-host", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Get ───────────────────────────────────────────────────────

func TestComponentHandler_Get_OK(t *testing.T) {
	r, comps, assets, _ := mountComponents(t)
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "spring", Repository: "raw-host"}))
	require.NoError(t, assets.Create(testContext(), &domain.Asset{
		ComponentID: "comp-1", Repository: "raw-host", Path: "/spring.jar", SizeBytes: 42,
	}))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/components/comp-1", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got domain.Component
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "comp-1", got.ID)
	require.Len(t, got.Assets, 1)
	assert.Equal(t, "http://localhost/repository/raw-host/spring.jar", got.Assets[0].DownloadURL)
}

func TestComponentHandler_Get_NotFound_404(t *testing.T) {
	r, _, _, _ := mountComponents(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/components/ghost", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestComponentHandler_Get_ComponentRepoError_500(t *testing.T) {
	r, comps, _, _ := mountComponents(t)
	comps.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/components/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestComponentHandler_Get_AssetRepoError_500(t *testing.T) {
	r, comps, assets, _ := mountComponents(t)
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "x", Repository: "raw-host"}))
	assets.Err = errors.New("asset query failed")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/components/comp-1", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Delete ────────────────────────────────────────────────────

func TestComponentHandler_Delete_OK(t *testing.T) {
	r, comps, _, _ := mountComponents(t)
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "delme", Repository: "raw-host"}))
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/components/comp-1", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestComponentHandler_Delete_RepoError_500(t *testing.T) {
	r, comps, _, _ := mountComponents(t)
	comps.Err = errors.New("db down")
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/components/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── SetTags (500 branch; success/400 already covered in components_tags_test.go) ──

func TestComponentHandler_SetTags_RepoError_500(t *testing.T) {
	r, comps, _, _ := mountComponents(t)
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "x", Repository: "raw-host"}))
	comps.Err = errors.New("db down")
	rec := do(t, r, http.MethodPut, "/service/rest/v1/components/comp-1/tags",
		map[string]any{"tags": []string{"a"}})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestComponentHandler_SetTags_NilTags_OK(t *testing.T) {
	// Body without "tags" → body.Tags == nil → normalized to [].
	r, comps, _, _ := mountComponents(t)
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "x", Repository: "raw-host"}))
	rec := do(t, r, http.MethodPut, "/service/rest/v1/components/comp-1/tags", map[string]any{})
	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string][]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got["tags"])
}

// ── Search ────────────────────────────────────────────────────

func TestComponentHandler_Search_NoRepository_OK(t *testing.T) {
	r, comps, assets, _ := mountComponents(t)
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "a", Repository: "raw-host"}))
	require.NoError(t, assets.Create(testContext(), &domain.Asset{
		ComponentID: "comp-1", Repository: "raw-host", Path: "/a.txt",
	}))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search?name=a", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got componentsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Items, 1)
	require.Len(t, got.Items[0].Assets, 1)
	assert.Equal(t, "http://localhost/repository/raw-host/a.txt", got.Items[0].Assets[0].DownloadURL)
}

func TestComponentHandler_Search_GroupExpansion(t *testing.T) {
	r, comps, _, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "m1", Name: "mem-a", Format: domain.FormatRaw, Type: domain.TypeHosted})
	seedRepo(t, repos, &domain.Repository{ID: "m2", Name: "mem-b", Format: domain.FormatRaw, Type: domain.TypeHosted})
	seedRepo(t, repos, &domain.Repository{
		ID: "g1", Name: "raw-group", Format: domain.FormatRaw, Type: domain.TypeGroup,
		FormatConfig: map[string]any{"member_names": []string{"mem-a", "mem-b"}},
	})
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "from-a", Repository: "mem-a"}))
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "from-b", Repository: "mem-b"}))
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "outsider", Repository: "elsewhere"}))

	rec := do(t, r, http.MethodGet, "/service/rest/v1/search?repository=raw-group", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got componentsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Items, 2)
}

func TestComponentHandler_Search_GroupNoMembers_EmptyOK(t *testing.T) {
	r, _, _, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "g1", Name: "empty-group", Format: domain.FormatRaw, Type: domain.TypeGroup})
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search?repository=empty-group", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got componentsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got.Items)
}

func TestComponentHandler_Search_ContinuationToken(t *testing.T) {
	r, comps, _, _ := mountComponents(t)
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "a", Repository: "raw-host"}))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search?continuationToken=5", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestComponentHandler_Search_ExpandRepoError_500(t *testing.T) {
	r, _, _, repos := mountComponents(t)
	repos.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search?repository=any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestComponentHandler_Search_ComponentRepoError_500(t *testing.T) {
	r, comps, _, _ := mountComponents(t)
	comps.Err = errors.New("query failed")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search?name=a", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestComponentHandler_Search_AssetRepoError_500(t *testing.T) {
	// Component with no preloaded assets triggers ListByComponentID, which errors.
	r, comps, assets, _ := mountComponents(t)
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "a", Repository: "raw-host"}))
	assets.Err = errors.New("asset query failed")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search?name=a", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestComponentHandler_Search_BatchAssetPreload verifies that the Search handler
// loads assets for multiple components using a single ListByComponentIDs call
// (the batch path), and that every returned component has its assets populated.
func TestComponentHandler_Search_BatchAssetPreload(t *testing.T) {
	r, comps, assets, _ := mountComponents(t)

	// Seed 3 components, each with 2 assets.
	// Capture the ID the mock assigns to each component (via comp.ID mutation)
	// rather than reconstructing it, so the test is not coupled to ID internals.
	for i := 1; i <= 3; i++ {
		comp := &domain.Component{
			Name: fmt.Sprintf("comp-%d", i), Repository: "raw-host",
		}
		require.NoError(t, comps.Create(testContext(), comp))
		compID := comp.ID // use the ID the mock assigned
		require.NoError(t, assets.Create(testContext(), &domain.Asset{
			ComponentID: compID, Repository: "raw-host",
			Path: fmt.Sprintf("/a%d/file-a.txt", i),
		}))
		require.NoError(t, assets.Create(testContext(), &domain.Asset{
			ComponentID: compID, Repository: "raw-host",
			Path: fmt.Sprintf("/a%d/file-b.txt", i),
		}))
	}

	rec := do(t, r, http.MethodGet, "/service/rest/v1/search?name=comp", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var got componentsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Items, 3)

	// Every component must have its 2 assets populated.
	for _, comp := range got.Items {
		assert.Len(t, comp.Assets, 2, "component %q should have 2 assets", comp.Name)
	}

	// The batch method must have been called exactly once (not once per component).
	assert.Equal(t, 1, assets.ListByComponentIDsCalls,
		"ListByComponentIDs should be called once for the whole search page, not once per component")
	// The singular per-component method must never be called (would indicate N+1).
	assert.Equal(t, 0, assets.ListByComponentIDCalls,
		"ListByComponentID (singular) must not be called when the batch path is taken")
}

// ── SearchAssets ──────────────────────────────────────────────

func TestComponentHandler_SearchAssets_OK(t *testing.T) {
	r, _, assets, _ := mountComponents(t)
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "raw-host", Path: "/x.bin"}))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search/assets?repository=raw-host", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got assetsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Items, 1)
	assert.Equal(t, "http://localhost/repository/raw-host/x.bin", got.Items[0].DownloadURL)
}

func TestComponentHandler_SearchAssets_GroupExpansion(t *testing.T) {
	r, _, assets, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "m1", Name: "mem-a", Format: domain.FormatRaw, Type: domain.TypeHosted})
	seedRepo(t, repos, &domain.Repository{ID: "m2", Name: "mem-b", Format: domain.FormatRaw, Type: domain.TypeHosted})
	seedRepo(t, repos, &domain.Repository{
		ID: "g1", Name: "raw-group", Format: domain.FormatRaw, Type: domain.TypeGroup,
		FormatConfig: map[string]any{"member_names": []string{"mem-a", "mem-b"}},
	})
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "mem-a", Path: "/a.bin"}))
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "mem-b", Path: "/b.bin"}))
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "elsewhere", Path: "/c.bin"}))

	rec := do(t, r, http.MethodGet, "/service/rest/v1/search/assets?repository=raw-group", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got assetsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Items, 2)
}

func TestComponentHandler_SearchAssets_GroupNoMembers_EmptyOK(t *testing.T) {
	r, _, _, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "g1", Name: "empty-group", Format: domain.FormatRaw, Type: domain.TypeGroup})
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search/assets?repository=empty-group", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got assetsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got.Items)
}

func TestComponentHandler_SearchAssets_ContinuationToken(t *testing.T) {
	r, _, _, _ := mountComponents(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search/assets?continuationToken=3", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestComponentHandler_SearchAssets_ExpandRepoError_500(t *testing.T) {
	r, _, _, repos := mountComponents(t)
	repos.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search/assets?repository=any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestComponentHandler_SearchAssets_AssetRepoError_500(t *testing.T) {
	r, _, assets, _ := mountComponents(t)
	assets.Err = errors.New("asset query failed")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search/assets?name=x", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── SearchAssetsDownload ──────────────────────────────────────

// mountComponentsDownload wires a ComponentHandler (no RBAC) plus the
// search/assets/download route over fresh mocks.
func mountComponentsDownload(t *testing.T) (*gin.Engine, *testutil.AssetRepo, *testutil.RepoRepo) {
	t.Helper()
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	repos := testutil.NewRepoRepo()
	h := handlers.NewComponentHandler(comps, assets, repos, "http://localhost")
	r := gin.New()
	r.GET("/service/rest/v1/search/assets/download", h.SearchAssetsDownload)
	return r, assets, repos
}

func TestComponentHandler_SearchAssetsDownload_SingleMatch_302(t *testing.T) {
	r, assets, _ := mountComponentsDownload(t)
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "raw-host", Path: "/x.bin"}))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search/assets/download?repository=raw-host", nil)
	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "http://localhost/repository/raw-host/x.bin", rec.Header().Get("Location"))
}

func TestComponentHandler_SearchAssetsDownload_ZeroMatch_404(t *testing.T) {
	r, _, _ := mountComponentsDownload(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search/assets/download?repository=raw-host", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestComponentHandler_SearchAssetsDownload_MultipleMatch_400(t *testing.T) {
	r, assets, _ := mountComponentsDownload(t)
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "raw-host", Path: "/a.bin"}))
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "raw-host", Path: "/b.bin"}))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search/assets/download?repository=raw-host", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestComponentHandler_SearchAssetsDownload_GroupNoMembers_404(t *testing.T) {
	r, _, repos := mountComponentsDownload(t)
	seedRepo(t, repos, &domain.Repository{ID: "g1", Name: "empty-group", Format: domain.FormatRaw, Type: domain.TypeGroup})
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search/assets/download?repository=empty-group", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestComponentHandler_SearchAssetsDownload_ExpandRepoError_500(t *testing.T) {
	r, _, repos := mountComponentsDownload(t)
	repos.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search/assets/download?repository=any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestComponentHandler_SearchAssetsDownload_AssetRepoError_500(t *testing.T) {
	r, assets, _ := mountComponentsDownload(t)
	assets.Err = errors.New("asset query failed")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search/assets/download?name=x", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// RBAC admin passthrough: with the RBAC service attached and admin roles
// injected, a single visible asset still redirects (302).
func TestComponentHandler_SearchAssetsDownload_RBAC_AdminPassthrough_302(t *testing.T) {
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	repos := testutil.NewRepoRepo()
	rbacSvc := service.NewRBACService(emptyRBACRepo{}, repos, zap.NewNop().Sugar())
	h := handlers.NewComponentHandler(comps, assets, repos, "http://localhost").WithRBAC(rbacSvc)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "admin")
		c.Set("roles", []string{"nx-admin"})
		c.Next()
	})
	r.GET("/service/rest/v1/search/assets/download", h.SearchAssetsDownload)
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "raw-host", Path: "/x.bin"}))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search/assets/download?repository=raw-host", nil)
	require.Equal(t, http.StatusFound, rec.Code)
	assert.Equal(t, "http://localhost/repository/raw-host/x.bin", rec.Header().Get("Location"))
}

// ── GetQuota ──────────────────────────────────────────────────

func TestComponentHandler_GetQuota_Unlimited_OK(t *testing.T) {
	r, _, assets, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "raw-host", Path: "/a", SizeBytes: 100}))
	rec := do(t, r, http.MethodGet, "/api/v1/repositories/raw-host/quota", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, float64(100), got["usedBytes"])
	assert.Nil(t, got["percentUsed"])
}

func TestComponentHandler_GetQuota_WithLimit_OK(t *testing.T) {
	r, _, assets, repos := mountComponents(t)
	quota := int64(200)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted, QuotaBytes: &quota})
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "raw-host", Path: "/a", SizeBytes: 100}))
	rec := do(t, r, http.MethodGet, "/api/v1/repositories/raw-host/quota", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, float64(50), got["percentUsed"])
}

func TestComponentHandler_GetQuota_RepoNotFound_404(t *testing.T) {
	r, _, _, _ := mountComponents(t)
	rec := do(t, r, http.MethodGet, "/api/v1/repositories/ghost/quota", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestComponentHandler_GetQuota_RepoError_500(t *testing.T) {
	r, _, _, repos := mountComponents(t)
	repos.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/api/v1/repositories/any/quota", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestComponentHandler_GetQuota_AssetError_500(t *testing.T) {
	r, _, assets, repos := mountComponents(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	assets.Err = errors.New("sum failed")
	rec := do(t, r, http.MethodGet, "/api/v1/repositories/raw-host/quota", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── RBAC passthrough paths (exercise rbacSvc != nil branches) ──

func TestComponentHandler_List_RBAC_AdminPassthrough(t *testing.T) {
	r, comps, _, repos := mountComponentsRBAC(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "a", Repository: "raw-host"}))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/components?repository=raw-host", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got componentsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Items, 1)
}

func TestComponentHandler_Search_RBAC_AdminPassthrough(t *testing.T) {
	r, comps, assets, _ := mountComponentsRBAC(t)
	require.NoError(t, comps.Create(testContext(), &domain.Component{Name: "a", Repository: "raw-host"}))
	require.NoError(t, assets.Create(testContext(), &domain.Asset{
		ComponentID: "comp-1", Repository: "raw-host", Path: "/a.txt",
	}))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search?name=a", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got componentsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Items, 1)
}

func TestComponentHandler_SearchAssets_RBAC_AdminPassthrough(t *testing.T) {
	r, _, assets, _ := mountComponentsRBAC(t)
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "raw-host", Path: "/x.bin"}))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/search/assets?repository=raw-host", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got assetsResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Items, 1)
}
