package handlers_test

import (
	"encoding/json"
	"errors"
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
