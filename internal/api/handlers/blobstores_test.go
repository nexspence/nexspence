package handlers_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// mountBlobStores builds a fully-wired BlobStoreHandler over testutil mocks and a gin engine
// covering every non-group route. The returned mocks are the same instances the handler uses,
// so tests can seed/inspect them. The mock storage.BlobStore is NOT a PresignableStore, which
// exercises the "S3 only" guard branches in the presign/lifecycle endpoints.
func mountBlobStores(t *testing.T, stores ...*domain.BlobStore) (*gin.Engine, *testutil.BlobStoreRepo, *testutil.RepoRepo, *testutil.AssetRepo, *testutil.BlobStore) {
	t.Helper()
	blobRepo := testutil.NewBlobStoreRepo(stores...)
	repoRepo := testutil.NewRepoRepo()
	assetRepo := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()
	gc := &service.BlobGCService{
		Assets:   assetRepo,
		Stores:   blobRepo,
		Resolver: testutil.NewFakeResolver(blobStore),
		Log:      slog.Default(),
	}

	h := handlers.NewBlobStoreHandler(blobRepo).
		WithBlobStore(blobStore).
		WithUsageDeps(repoRepo, assetRepo).
		WithGC(gc)

	r := gin.New()
	r.GET("/service/rest/v1/blobstores", h.List)
	r.GET("/service/rest/v1/blobstores/:name", h.Get)
	r.POST("/service/rest/v1/blobstores/:type", h.Create)
	r.PUT("/service/rest/v1/blobstores/:type/:name", h.Update)
	r.DELETE("/service/rest/v1/blobstores/:name", h.Delete)
	r.GET("/api/v1/blobstores/:name/presign", h.PresignGet)
	r.POST("/api/v1/blobstores/:name/presign", h.PresignPut)
	r.PUT("/api/v1/blobstores/:name/lifecycle", h.ConfigureLifecycle)
	r.GET("/api/v1/blob-stores/:name/usage", h.Usage)
	r.POST("/api/v1/blobstores/test", h.TestConnection)
	r.POST("/api/v1/blobstores/:name/compact", h.Compact)
	return r, blobRepo, repoRepo, assetRepo, blobStore
}

// ── List ───────────────────────────────────────────────────────────────────

func TestBlobStore_List_Empty(t *testing.T) {
	// NewBlobStoreRepo with no seeds inserts a "default" store, so list is never empty here.
	r, _, _, _, _ := mountBlobStores(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/blobstores", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []domain.BlobStore
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Len(t, got, 1)
}

func TestBlobStore_List_Populated(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t,
		&domain.BlobStore{ID: "a", Name: "store-a", Type: "local"},
		&domain.BlobStore{ID: "b", Name: "store-b", Type: "s3"},
	)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/blobstores", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []domain.BlobStore
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Len(t, got, 2)
}

// ── Get ────────────────────────────────────────────────────────────────────

func TestBlobStore_Get_Found(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t, &domain.BlobStore{ID: "a", Name: "store-a", Type: "local"})
	rec := do(t, r, http.MethodGet, "/service/rest/v1/blobstores/store-a", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got domain.BlobStore
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "store-a", got.Name)
}

func TestBlobStore_Get_NotFound_404(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/blobstores/ghost", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// ── Create (non-group) ───────────────────────────────────────────────────────

func TestBlobStore_Create_Local_Success(t *testing.T) {
	r, repo, _, _, _ := mountBlobStores(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/blobstores/local",
		map[string]any{"name": "new-local", "config": map[string]any{"path": "/tmp/x"}})
	require.Equal(t, http.StatusCreated, rec.Code)
	var got domain.BlobStore
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "new-local", got.Name)
	assert.Equal(t, "local", got.Type)
	stored, _ := repo.Get(testContext(), "new-local")
	require.NotNil(t, stored)
}

func TestBlobStore_Create_S3_Success(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/blobstores/s3",
		map[string]any{"name": "new-s3", "config": map[string]any{"bucket": "b", "region": "us-east-1"}})
	require.Equal(t, http.StatusCreated, rec.Code)
	var got domain.BlobStore
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "s3", got.Type)
}

func TestBlobStore_Create_BadJSON_400(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t)
	rec := doRaw(t, r, http.MethodPost, "/service/rest/v1/blobstores/local", []byte(`{not json`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBlobStore_Create_EmptyName_400(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/blobstores/local",
		map[string]any{"config": map[string]any{"path": "/tmp/x"}})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── Update ───────────────────────────────────────────────────────────────────

func TestBlobStore_Update_Success(t *testing.T) {
	r, repo, _, _, _ := mountBlobStores(t, &domain.BlobStore{ID: "a", Name: "store-a", Type: "local"})
	rec := do(t, r, http.MethodPut, "/service/rest/v1/blobstores/local/store-a",
		map[string]any{"config": map[string]any{"path": "/new/path"}})
	require.Equal(t, http.StatusOK, rec.Code)
	updated, _ := repo.Get(testContext(), "store-a")
	require.NotNil(t, updated)
	// Type must be preserved from existing, name forced from URL.
	assert.Equal(t, "local", updated.Type)
	assert.Equal(t, "store-a", updated.Name)
}

func TestBlobStore_Update_NotFound_404(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t)
	rec := do(t, r, http.MethodPut, "/service/rest/v1/blobstores/local/ghost",
		map[string]any{"config": map[string]any{"path": "/x"}})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestBlobStore_Update_BadJSON_400(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t, &domain.BlobStore{ID: "a", Name: "store-a", Type: "local"})
	rec := doRaw(t, r, http.MethodPut, "/service/rest/v1/blobstores/local/store-a", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── Delete ───────────────────────────────────────────────────────────────────

func TestBlobStore_Delete_Success_204(t *testing.T) {
	r, repo, _, _, _ := mountBlobStores(t, &domain.BlobStore{ID: "a", Name: "store-a", Type: "local"})
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/blobstores/store-a", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	gone, _ := repo.Get(testContext(), "store-a")
	assert.Nil(t, gone)
}

func TestBlobStore_Delete_NotPresent_204(t *testing.T) {
	// Deleting a non-existent (and non-member) store still succeeds (mock delete is a no-op).
	r, _, _, _, _ := mountBlobStores(t)
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/blobstores/ghost", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

// ── Presign / Lifecycle (mock store is not PresignableStore → 400) ───────────

func TestBlobStore_PresignGet_NotS3_400(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t)
	rec := do(t, r, http.MethodGet, "/api/v1/blobstores/store-a/presign?key=k", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBlobStore_PresignPut_NotS3_400(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t)
	rec := do(t, r, http.MethodPost, "/api/v1/blobstores/store-a/presign",
		map[string]any{"key": "k"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBlobStore_ConfigureLifecycle_NotS3_400(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t)
	rec := do(t, r, http.MethodPut, "/api/v1/blobstores/store-a/lifecycle",
		map[string]any{"expiration_days": 30})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── Usage ────────────────────────────────────────────────────────────────────

func TestBlobStore_Usage_DepsNotConfigured_503(t *testing.T) {
	// Build a handler WITHOUT WithUsageDeps to hit the 503 guard.
	h := handlers.NewBlobStoreHandler(testutil.NewBlobStoreRepo())
	r := gin.New()
	r.GET("/api/v1/blob-stores/:name/usage", h.Usage)
	rec := do(t, r, http.MethodGet, "/api/v1/blob-stores/store-a/usage", nil)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestBlobStore_Usage_NotFound_404(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t)
	rec := do(t, r, http.MethodGet, "/api/v1/blob-stores/ghost/usage", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestBlobStore_Usage_Local_WithQuota_Success(t *testing.T) {
	quota := int64(1000)
	r, _, repoRepo, assetRepo, _ := mountBlobStores(t,
		&domain.BlobStore{ID: "bs-1", Name: "store-a", Type: "local", QuotaBytes: &quota, UsedBytes: 200})

	// Seed a repository linked to this blob store, plus an asset so SumSizeByRepo > 0.
	bsID := "bs-1"
	require.NoError(t, repoRepo.Create(testContext(),
		&domain.Repository{ID: "r1", Name: "maven-hosted", Format: "maven2", Type: "hosted", BlobStoreID: &bsID}))
	require.NoError(t, assetRepo.Create(testContext(),
		&domain.Asset{Repository: "maven-hosted", Path: "a/b.jar", SizeBytes: 123}))

	rec := do(t, r, http.MethodGet, "/api/v1/blob-stores/store-a/usage", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	linked, _ := resp["linkedRepositories"].([]any)
	require.Len(t, linked, 1)
	// quotaRemaining = quota - usedBytes = 1000 - 200 = 800.
	assert.EqualValues(t, 800, resp["quotaRemaining"])
}

func TestBlobStore_Usage_Group_Success(t *testing.T) {
	q := int64(500)
	member := &domain.BlobStore{ID: "m1", Name: "member-a", Type: "local", UsedBytes: 100, QuotaBytes: &q}
	group := &domain.BlobStore{
		ID: "g1", Name: "grp", Type: "group",
		Config: map[string]any{"fill_policy": "round_robin", "member_ids": []any{"m1"}},
	}
	r, _, repoRepo, assetRepo, _ := mountBlobStores(t, member, group)

	mID := "m1"
	require.NoError(t, repoRepo.Create(testContext(),
		&domain.Repository{ID: "r1", Name: "raw-hosted", Format: "raw", Type: "hosted", BlobStoreID: &mID}))
	require.NoError(t, assetRepo.Create(testContext(),
		&domain.Asset{Repository: "raw-hosted", Path: "f.txt", SizeBytes: 50}))

	rec := do(t, r, http.MethodGet, "/api/v1/blob-stores/grp/usage", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	members, _ := resp["members"].([]any)
	require.Len(t, members, 1)
	assert.EqualValues(t, 100, resp["memberTotalUsed"])
	// hasQuota=true (single member with quota): memberTotalQuota=500, remaining=500-100=400.
	assert.EqualValues(t, 500, resp["memberTotalQuota"])
	assert.EqualValues(t, 400, resp["quotaRemaining"])
}

// ── TestConnection ───────────────────────────────────────────────────────────

func TestBlobStore_TestConnection_BadJSON_400(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t)
	rec := doRaw(t, r, http.MethodPost, "/api/v1/blobstores/test", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBlobStore_TestConnection_MissingType_400(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t)
	rec := do(t, r, http.MethodPost, "/api/v1/blobstores/test", map[string]any{"config": map[string]any{}})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBlobStore_TestConnection_Local_OK(t *testing.T) {
	r, _, _, _, _ := mountBlobStores(t)
	rec := do(t, r, http.MethodPost, "/api/v1/blobstores/test",
		map[string]any{"type": "local", "config": map[string]any{"path": t.TempDir()}})
	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["ok"])
}

func TestBlobStore_TestConnection_S3_MissingBucket_NotOK(t *testing.T) {
	// s3 with no bucket → storage.NewFromConfig errors → {"ok":false} (still HTTP 200).
	r, _, _, _, _ := mountBlobStores(t)
	rec := do(t, r, http.MethodPost, "/api/v1/blobstores/test",
		map[string]any{"type": "s3", "config": map[string]any{}})
	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["ok"])
	assert.NotEmpty(t, resp["error"])
}

// ── Compact ──────────────────────────────────────────────────────────────────

func TestBlobStore_Compact_NoGC_503(t *testing.T) {
	// Handler without WithGC → 503.
	h := handlers.NewBlobStoreHandler(testutil.NewBlobStoreRepo())
	r := gin.New()
	r.POST("/api/v1/blobstores/:name/compact", h.Compact)
	rec := do(t, r, http.MethodPost, "/api/v1/blobstores/store-a/compact", nil)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestBlobStore_Compact_DryRun_Success(t *testing.T) {
	r, _, _, _, blobStore := mountBlobStores(t)
	// Put an orphan blob (no asset references it) so Compact reports an orphan.
	require.NoError(t, blobStore.Put(testContext(), "orphan-key", strings.NewReader("data"), 4))

	rec := do(t, r, http.MethodPost, "/api/v1/blobstores/default/compact?dry_run=true", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var res service.GCResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.True(t, res.DryRun)
	assert.Equal(t, 1, res.Orphans)
	// Dry-run must NOT delete.
	assert.True(t, blobStore.Has("orphan-key"))
}

func TestBlobStore_Compact_Delete_Success(t *testing.T) {
	r, _, _, _, blobStore := mountBlobStores(t)
	require.NoError(t, blobStore.Put(testContext(), "orphan-key", strings.NewReader("data"), 4))

	rec := do(t, r, http.MethodPost, "/api/v1/blobstores/default/compact", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var res service.GCResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.False(t, res.DryRun)
	assert.Equal(t, 1, res.Orphans)
	assert.False(t, blobStore.Has("orphan-key"))
}
