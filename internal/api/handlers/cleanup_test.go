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

func cleanupNopLog() *zap.SugaredLogger { return zap.NewNop().Sugar() }

// mountCleanup wires the real CleanupService (over mocks) as the runner so Preview / Run
// exercise the actual service path, mirroring router.go.
func mountCleanup(t *testing.T) (*gin.Engine, *testutil.CleanupPolicyRepo, *testutil.RepoRepo, *testutil.AssetRepo) {
	t.Helper()
	policies := testutil.NewCleanupPolicyRepo()
	repos := testutil.NewRepoRepo()
	assets := testutil.NewAssetRepo()
	blobRepo := testutil.NewBlobStoreRepo()
	blobs := testutil.NewBlobStore()
	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, cleanupNopLog())
	h := handlers.NewCleanupHandler(policies, repos, svc)

	r := gin.New()
	r.GET("/service/rest/v1/cleanup-policies", h.List)
	r.GET("/service/rest/v1/cleanup-policies/:id", h.Get)
	r.POST("/service/rest/v1/cleanup-policies", h.Create)
	r.PUT("/service/rest/v1/cleanup-policies/:id", h.Update)
	r.DELETE("/service/rest/v1/cleanup-policies/:id", h.Delete)
	r.POST("/service/rest/v1/cleanup-policies/:id/run", h.Run)
	r.POST("/api/v1/cleanup-policies/:id/preview", h.Preview)
	return r, policies, repos, assets
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestCleanupHandler_List_Empty(t *testing.T) {
	r, _, _, _ := mountCleanup(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/cleanup-policies", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []domain.CleanupPolicy
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got)
}

func TestCleanupHandler_List_RepoError_500(t *testing.T) {
	r, policies, _, _ := mountCleanup(t)
	policies.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/cleanup-policies", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestCleanupHandler_Get_OK(t *testing.T) {
	r, policies, _, _ := mountCleanup(t)
	p := &domain.CleanupPolicy{Name: "p", Format: "*", Criteria: map[string]any{}}
	require.NoError(t, policies.Create(testContext(), p))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/cleanup-policies/"+p.ID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got domain.CleanupPolicy
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "p", got.Name)
}

func TestCleanupHandler_Get_NotFound_404(t *testing.T) {
	r, _, _, _ := mountCleanup(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/cleanup-policies/ghost", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCleanupHandler_Get_RepoError_500(t *testing.T) {
	r, policies, _, _ := mountCleanup(t)
	policies.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/cleanup-policies/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestCleanupHandler_Create_OK_DefaultsFormatAndCriteria(t *testing.T) {
	r, _, _, _ := mountCleanup(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/cleanup-policies",
		map[string]any{"name": "keep-recent", "retainNVersions": 3})
	require.Equal(t, http.StatusCreated, rec.Code)
	var got domain.CleanupPolicy
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "keep-recent", got.Name)
	assert.Equal(t, "*", got.Format)        // defaulted
	assert.NotNil(t, got.Criteria)          // defaulted to {}
	assert.Equal(t, 3, got.RetainNVersions) // persisted
	assert.NotEmpty(t, got.ID)
}

func TestCleanupHandler_Create_BadJSON_400(t *testing.T) {
	r, _, _, _ := mountCleanup(t)
	rec := doRaw(t, r, http.MethodPost, "/service/rest/v1/cleanup-policies", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCleanupHandler_Create_EmptyName_400(t *testing.T) {
	r, _, _, _ := mountCleanup(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/cleanup-policies",
		map[string]any{"format": "maven2"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCleanupHandler_Create_RepoError_500(t *testing.T) {
	r, policies, _, _ := mountCleanup(t)
	policies.Err = errors.New("db down")
	rec := do(t, r, http.MethodPost, "/service/rest/v1/cleanup-policies",
		map[string]any{"name": "fail"})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestCleanupHandler_Update_OK(t *testing.T) {
	r, policies, _, _ := mountCleanup(t)
	p := &domain.CleanupPolicy{Name: "old", Format: "*", Criteria: map[string]any{}}
	require.NoError(t, policies.Create(testContext(), p))
	rec := do(t, r, http.MethodPut, "/service/rest/v1/cleanup-policies/"+p.ID,
		map[string]any{"name": "new", "format": "npm", "retainNVersions": 5})
	require.Equal(t, http.StatusOK, rec.Code)
	var got domain.CleanupPolicy
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, p.ID, got.ID) // ID forced from path param
	assert.Equal(t, "new", got.Name)
	assert.Equal(t, 5, got.RetainNVersions)
}

func TestCleanupHandler_Update_BadJSON_400(t *testing.T) {
	r, _, _, _ := mountCleanup(t)
	rec := doRaw(t, r, http.MethodPut, "/service/rest/v1/cleanup-policies/any", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCleanupHandler_Update_RepoError_500(t *testing.T) {
	r, policies, _, _ := mountCleanup(t)
	policies.Err = errors.New("db down")
	rec := do(t, r, http.MethodPut, "/service/rest/v1/cleanup-policies/any",
		map[string]any{"name": "x"})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestCleanupHandler_Delete_OK(t *testing.T) {
	r, policies, _, _ := mountCleanup(t)
	p := &domain.CleanupPolicy{Name: "temp", Format: "*", Criteria: map[string]any{}}
	require.NoError(t, policies.Create(testContext(), p))
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/cleanup-policies/"+p.ID, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	got, _ := policies.Get(testContext(), p.ID)
	assert.Nil(t, got)
}

func TestCleanupHandler_Delete_DetachError_500(t *testing.T) {
	r, _, repos, _ := mountCleanup(t)
	repos.Err = errors.New("detach failed")
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/cleanup-policies/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestCleanupHandler_Delete_DeleteError_500(t *testing.T) {
	r, policies, _, _ := mountCleanup(t)
	// Detach succeeds (repos.Err nil), policy Delete fails.
	policies.Err = errors.New("delete failed")
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/cleanup-policies/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Run ───────────────────────────────────────────────────────────────────────

func TestCleanupHandler_Run_Single_202(t *testing.T) {
	r, policies, _, _ := mountCleanup(t)
	p := &domain.CleanupPolicy{
		Name: "run-me", Format: "*", Enabled: true,
		Criteria: map[string]any{"artifactAgeDays": 30},
	}
	require.NoError(t, policies.Create(testContext(), p))
	rec := do(t, r, http.MethodPost, "/service/rest/v1/cleanup-policies/"+p.ID+"/run", nil)
	require.Equal(t, http.StatusAccepted, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "running", body["status"])
	assert.Equal(t, p.ID, body["id"])
}

func TestCleanupHandler_Run_All_202(t *testing.T) {
	r, _, _, _ := mountCleanup(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/cleanup-policies/_all/run", nil)
	require.Equal(t, http.StatusAccepted, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "running all policies", body["status"])
}

// ── Preview ───────────────────────────────────────────────────────────────────

func TestCleanupHandler_Preview_OK(t *testing.T) {
	r, policies, repos, assets := mountCleanup(t)
	// Attach the policy to a repo so PreviewPolicy resolves repo names, then seed stale assets.
	repoName := "maven-hosted"
	p := &domain.CleanupPolicy{
		Name: "preview-me", Format: "*",
		Criteria: map[string]any{"artifactAgeDays": 90},
	}
	require.NoError(t, policies.Create(testContext(), p))
	require.NoError(t, repos.Create(testContext(), &domain.Repository{
		Name: repoName, Format: "maven2", CleanupPolicyIDs: []string{p.ID},
	}))
	assets.Stale = []domain.Asset{
		{ID: "a1", Path: "com/x/1.0/x.jar", Repository: repoName, SizeBytes: 100},
		{ID: "a2", Path: "com/x/2.0/x.jar", Repository: repoName, SizeBytes: 200},
	}

	rec := do(t, r, http.MethodPost, "/api/v1/cleanup-policies/"+p.ID+"/preview", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var res domain.CleanupPreviewResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, 2, res.TotalCount)
	assert.Equal(t, int64(300), res.TotalBytes)
	require.Len(t, res.Assets, 2)
	assert.Equal(t, "age 90d", res.Assets[0].Reason)
}

func TestCleanupHandler_Preview_NotFound_500(t *testing.T) {
	// PreviewPolicy returns an error for a missing policy → handler maps to 500.
	r, _, _, _ := mountCleanup(t)
	rec := do(t, r, http.MethodPost, "/api/v1/cleanup-policies/ghost/preview", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
