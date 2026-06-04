package handlers_test

import (
	"context"
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
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// emptyRBACRepo grants no privileges — RBACService.FilterRepos returns repos as-is
// for admin roles, so the List handler's RBAC pass is exercised but never blocks.
type emptyRBACRepo struct{}

func (emptyRBACRepo) GetUserPrivilegesWithSelectors(_ context.Context, _ string) ([]repository.PrivilegeWithSelector, error) {
	return nil, nil
}

// mountRepos wires RepositoryHandler over mock repos to a test Gin engine.
// The "userID"/"roles" middleware injects admin so RBAC filtering is a passthrough.
func mountRepos(t *testing.T) (*gin.Engine, *testutil.RepoRepo, *testutil.BlobStoreRepo, *testutil.CleanupPolicyRepo) {
	t.Helper()
	repos := testutil.NewRepoRepo()
	blobs := testutil.NewBlobStoreRepo()
	policies := testutil.NewCleanupPolicyRepo()
	store := testutil.NewBlobStore()

	repoSvc := service.NewRepositoryService(repos, blobs, store, policies)
	rbacSvc := service.NewRBACService(emptyRBACRepo{}, repos, zap.NewNop().Sugar())
	h := handlers.NewRepositoryHandler(repoSvc, rbacSvc)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "admin")
		c.Set("roles", []string{"nx-admin"})
		c.Next()
	})
	r.GET("/service/rest/v1/repositories", h.List)
	r.GET("/service/rest/v1/repositories/:name", h.Get)
	r.POST("/service/rest/v1/repositories/:format/:type", h.Create)
	r.PUT("/service/rest/v1/repositories/:format/:type/:name", h.Update)
	r.PATCH("/service/rest/v1/repositories/:name", h.Patch)
	r.DELETE("/service/rest/v1/repositories/:name", h.Delete)
	return r, repos, blobs, policies
}

// seedRepo inserts a repository directly into the mock store.
func seedRepo(t *testing.T, repos *testutil.RepoRepo, r *domain.Repository) {
	t.Helper()
	require.NoError(t, repos.Create(testContext(), r))
}

// ── List ──────────────────────────────────────────────────────

func TestRepositoryHandler_List_Empty(t *testing.T) {
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/repositories", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []domain.Repository
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got)
}

func TestRepositoryHandler_List_WithRepos(t *testing.T) {
	r, repos, _, _ := mountRepos(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "maven-hosted", Format: domain.FormatMaven2, Type: domain.TypeHosted})
	rec := do(t, r, http.MethodGet, "/service/rest/v1/repositories?format=maven2&type=hosted", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []domain.Repository
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "maven-hosted", got[0].Name)
}

func TestRepositoryHandler_List_RepoError_500(t *testing.T) {
	r, repos, _, _ := mountRepos(t)
	repos.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/repositories", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Get ───────────────────────────────────────────────────────

func TestRepositoryHandler_Get_OK(t *testing.T) {
	r, repos, _, _ := mountRepos(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-1", Format: domain.FormatRaw, Type: domain.TypeHosted})
	rec := do(t, r, http.MethodGet, "/service/rest/v1/repositories/raw-1", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got domain.Repository
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "raw-1", got.Name)
}

func TestRepositoryHandler_Get_NotFound_404(t *testing.T) {
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/repositories/ghost", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRepositoryHandler_Get_RepoError_500(t *testing.T) {
	r, repos, _, _ := mountRepos(t)
	repos.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/repositories/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Create ────────────────────────────────────────────────────

func TestRepositoryHandler_Create_OK(t *testing.T) {
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/raw/hosted",
		map[string]any{"name": "new-raw"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var got domain.Repository
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "new-raw", got.Name)
	assert.Equal(t, domain.FormatRaw, got.Format)
	assert.Equal(t, domain.TypeHosted, got.Type)
	assert.True(t, got.Online)
}

func TestRepositoryHandler_Create_BadJSON_400(t *testing.T) {
	r, _, _, _ := mountRepos(t)
	rec := doRaw(t, r, http.MethodPost, "/service/rest/v1/repositories/raw/hosted", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRepositoryHandler_Create_EmptyName_400(t *testing.T) {
	// Missing name → ErrInvalidInput → 400.
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/raw/hosted",
		map[string]any{"description": "no name"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRepositoryHandler_Create_Conflict_409(t *testing.T) {
	r, repos, _, _ := mountRepos(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "dup", Format: domain.FormatRaw, Type: domain.TypeHosted})
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/raw/hosted",
		map[string]any{"name": "dup"})
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestRepositoryHandler_Create_ProxyMissingConfig_400(t *testing.T) {
	// Proxy without proxy_config.remote_url → ErrInvalidInput → 400.
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/maven2/proxy",
		map[string]any{"name": "mvn-proxy"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRepositoryHandler_Create_ProxyOK(t *testing.T) {
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/maven2/proxy",
		map[string]any{
			"name":        "mvn-proxy",
			"proxyConfig": map[string]any{"remote_url": "https://repo1.maven.org/maven2/"},
		})
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestRepositoryHandler_Create_GroupNoMembers_400(t *testing.T) {
	// Group with no member_names → ErrInvalidInput → 400.
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/maven2/group",
		map[string]any{"name": "mvn-group"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRepositoryHandler_Create_GroupMemberMissing_400(t *testing.T) {
	// Group member that does not exist → ErrInvalidInput → 400.
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/maven2/group",
		map[string]any{
			"name":         "mvn-group",
			"formatConfig": map[string]any{"member_names": []string{"nope"}},
		})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRepositoryHandler_Create_GroupOK(t *testing.T) {
	// A valid hosted member of matching format makes the group create succeed.
	r, repos, _, _ := mountRepos(t)
	seedRepo(t, repos, &domain.Repository{ID: "m1", Name: "mvn-host", Format: domain.FormatMaven2, Type: domain.TypeHosted})
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/maven2/group",
		map[string]any{
			"name":         "mvn-group",
			"formatConfig": map[string]any{"member_names": []string{"mvn-host"}},
		})
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestRepositoryHandler_Create_BlobStoreNotFound_400(t *testing.T) {
	// References a blob store ID that does not exist → ErrNotFound → 400.
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/raw/hosted",
		map[string]any{"name": "blobless", "blobStoreId": "no-such-store"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRepositoryHandler_Create_QuotaExceedsStore_400(t *testing.T) {
	// Repo quota larger than the owning blob store quota → ErrInvalidInput → 400.
	r, _, blobs, _ := mountRepos(t)
	storeQuota := int64(100)
	require.NoError(t, blobs.Create(testContext(), &domain.BlobStore{
		ID: "bs-1", Name: "small", Type: "local", QuotaBytes: &storeQuota,
	}))
	repoQuota := int64(500)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/raw/hosted",
		map[string]any{"name": "over-quota", "blobStoreId": "bs-1", "quotaBytes": repoQuota})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRepositoryHandler_Create_WithBlobStore_OK(t *testing.T) {
	r, _, blobs, _ := mountRepos(t)
	require.NoError(t, blobs.Create(testContext(), &domain.BlobStore{
		ID: "bs-1", Name: "big", Type: "local",
	}))
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/raw/hosted",
		map[string]any{"name": "withstore", "blobStoreId": "bs-1"})
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestRepositoryHandler_Create_RepoError_500(t *testing.T) {
	// repos.Err makes the duplicate-check Get fail with a generic error → 500.
	r, repos, _, _ := mountRepos(t)
	repos.Err = errors.New("db down")
	rec := do(t, r, http.MethodPost, "/service/rest/v1/repositories/raw/hosted",
		map[string]any{"name": "boom"})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Update ────────────────────────────────────────────────────

func TestRepositoryHandler_Update_OK(t *testing.T) {
	r, repos, _, _ := mountRepos(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "upd", Format: domain.FormatRaw, Type: domain.TypeHosted, Online: true})
	rec := do(t, r, http.MethodPut, "/service/rest/v1/repositories/raw/hosted/upd",
		map[string]any{"description": "updated", "online": true})
	require.Equal(t, http.StatusOK, rec.Code)
	var got domain.Repository
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "updated", got.Description)
}

func TestRepositoryHandler_Update_BadJSON_400(t *testing.T) {
	r, _, _, _ := mountRepos(t)
	rec := doRaw(t, r, http.MethodPut, "/service/rest/v1/repositories/raw/hosted/any", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRepositoryHandler_Update_NotFound_404(t *testing.T) {
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodPut, "/service/rest/v1/repositories/raw/hosted/ghost",
		map[string]any{"description": "x"})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRepositoryHandler_Update_BlobStoreNotFound_404(t *testing.T) {
	// Update referencing a non-existent blob store → ErrNotFound → 404.
	r, repos, _, _ := mountRepos(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "upd2", Format: domain.FormatRaw, Type: domain.TypeHosted})
	rec := do(t, r, http.MethodPut, "/service/rest/v1/repositories/raw/hosted/upd2",
		map[string]any{"blobStoreId": "no-such"})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRepositoryHandler_Update_QuotaExceedsStore_400(t *testing.T) {
	r, repos, blobs, _ := mountRepos(t)
	storeQuota := int64(100)
	require.NoError(t, blobs.Create(testContext(), &domain.BlobStore{
		ID: "bs-1", Name: "small", Type: "local", QuotaBytes: &storeQuota,
	}))
	bsID := "bs-1"
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "qrepo", Format: domain.FormatRaw, Type: domain.TypeHosted, BlobStoreID: &bsID})
	rec := do(t, r, http.MethodPut, "/service/rest/v1/repositories/raw/hosted/qrepo",
		map[string]any{"quotaBytes": int64(9999)})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRepositoryHandler_Update_RepoError_500(t *testing.T) {
	// repos.Err makes the initial Get fail with a generic error → 500.
	r, repos, _, _ := mountRepos(t)
	repos.Err = errors.New("db down")
	rec := do(t, r, http.MethodPut, "/service/rest/v1/repositories/raw/hosted/any",
		map[string]any{"description": "x"})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Patch ─────────────────────────────────────────────────────

func TestRepositoryHandler_Patch_OK(t *testing.T) {
	r, repos, _, _ := mountRepos(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "patchme", Format: domain.FormatRaw, Type: domain.TypeHosted, Online: true})
	rec := do(t, r, http.MethodPatch, "/service/rest/v1/repositories/patchme",
		map[string]any{"online": false})
	require.Equal(t, http.StatusOK, rec.Code)
	var got domain.Repository
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.False(t, got.Online)
}

func TestRepositoryHandler_Patch_BadJSON_400(t *testing.T) {
	r, _, _, _ := mountRepos(t)
	rec := doRaw(t, r, http.MethodPatch, "/service/rest/v1/repositories/any", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRepositoryHandler_Patch_NotFound_404(t *testing.T) {
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodPatch, "/service/rest/v1/repositories/ghost",
		map[string]any{"online": false})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRepositoryHandler_Patch_GetRepoError_500(t *testing.T) {
	r, repos, _, _ := mountRepos(t)
	repos.Err = errors.New("db down")
	rec := do(t, r, http.MethodPatch, "/service/rest/v1/repositories/any",
		map[string]any{"online": false})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Delete ────────────────────────────────────────────────────

func TestRepositoryHandler_Delete_OK(t *testing.T) {
	r, repos, _, _ := mountRepos(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "delme", Format: domain.FormatRaw, Type: domain.TypeHosted})
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/repositories/delme", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestRepositoryHandler_Delete_NotFound_404(t *testing.T) {
	r, _, _, _ := mountRepos(t)
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/repositories/ghost", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRepositoryHandler_Delete_RepoError_500(t *testing.T) {
	// repos.Err makes the existence-check Get fail with a generic error → 500.
	r, repos, _, _ := mountRepos(t)
	repos.Err = errors.New("db down")
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/repositories/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
