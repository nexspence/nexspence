package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// TestBlobStoreMigrationExtra_Start_BadJSON_400 hits the ShouldBindJSON failure branch
// (missing required targetStoreId).
func TestBlobStoreMigrationExtra_Start_BadJSON_400(t *testing.T) {
	svc := newMigSvc(t, "my-repo", "src-store", "tgt-store")
	r := buildMigRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/my-repo/migrate-blob-store",
		strings.NewReader(`{`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestBlobStoreMigrationExtra_Start_MissingTarget_400 hits the binding:"required" branch
// when targetStoreId is empty.
func TestBlobStoreMigrationExtra_Start_MissingTarget_400(t *testing.T) {
	svc := newMigSvc(t, "my-repo", "src-store", "tgt-store")
	r := buildMigRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/my-repo/migrate-blob-store",
		strings.NewReader(`{"targetStoreId":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestBlobStoreMigrationExtra_Start_RepoNotFound_400 — the service returns a "not found"
// error for an unknown repository, which the handler maps to 400.
func TestBlobStoreMigrationExtra_Start_RepoNotFound_400(t *testing.T) {
	svc := newMigSvc(t, "my-repo", "src-store", "tgt-store")
	r := buildMigRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/ghost-repo/migrate-blob-store",
		strings.NewReader(`{"targetStoreId":"tgt-store"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "not found")
}

// TestBlobStoreMigrationExtra_Start_SameStore_400 — target equals the repo's current store,
// service returns a "same as" error → 400.
func TestBlobStoreMigrationExtra_Start_SameStore_400(t *testing.T) {
	// Repo's current store IS "src-store"; request migrating to the same store.
	svc := newMigSvc(t, "my-repo", "src-store", "tgt-store")
	r := buildMigRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/my-repo/migrate-blob-store",
		strings.NewReader(`{"targetStoreId":"src-store"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "same as")
}

// TestBlobStoreMigrationExtra_Start_TargetNotFound_400 — target store ID doesn't exist,
// service returns "target blob store not found" → handler maps "not found" to 400.
func TestBlobStoreMigrationExtra_Start_TargetNotFound_400(t *testing.T) {
	svc := newMigSvc(t, "my-repo", "src-store", "tgt-store")
	r := buildMigRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/my-repo/migrate-blob-store",
		strings.NewReader(`{"targetStoreId":"does-not-exist"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "not found")
}

// TestBlobStoreMigrationExtra_Cancel_NoMigration_404 — Cancel with no migration record.
func TestBlobStoreMigrationExtra_Cancel_NoMigration_404(t *testing.T) {
	svc := newMigSvc(t, "my-repo", "src-store", "tgt-store")
	r := buildMigRouter(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/repositories/my-repo/blob-store-migration", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// migSvcWithSeededMigration builds a migration service whose migration repo already
// contains the given migration record (bypassing Start), so we can exercise Cancel's
// status guard without racing the background goroutine.
func migSvcWithSeededMigration(t *testing.T, repoName string, m *domain.BlobStoreMigration) *service.BlobStoreMigrationService {
	t.Helper()
	src := "src-store"
	repoRepo := testutil.NewRepoRepo(&domain.Repository{ID: "r1", Name: repoName, BlobStoreID: &src})
	blobRepo := testutil.NewBlobStoreRepo(
		&domain.BlobStore{ID: "src-store", Name: "source", Type: "local", Config: map[string]any{"path": t.TempDir()}},
		&domain.BlobStore{ID: "tgt-store", Name: "target", Type: "local", Config: map[string]any{"path": t.TempDir()}},
	)
	migRepo := testutil.NewBlobStoreMigrationRepo(m)
	assetRepo := testutil.NewAssetRepo()
	reg := storage.NewRegistry(testutil.NewBlobStore())
	return service.NewBlobStoreMigrationService(migRepo, assetRepo, repoRepo, blobRepo, reg)
}

// TestBlobStoreMigrationExtra_Cancel_NotActive_400 — latest migration is already "completed",
// so Cancel returns 400 ("migration is not active").
func TestBlobStoreMigrationExtra_Cancel_NotActive_400(t *testing.T) {
	done := &domain.BlobStoreMigration{ID: "m-done", RepositoryName: "my-repo", Status: "completed"}
	svc := migSvcWithSeededMigration(t, "my-repo", done)
	r := buildMigRouter(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/repositories/my-repo/blob-store-migration", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "not active")
}

// TestBlobStoreMigrationExtra_Cancel_Pending_200 — a "pending" migration is cancellable,
// exercising the success path with a deterministic (non-running) status.
func TestBlobStoreMigrationExtra_Cancel_Pending_200(t *testing.T) {
	pending := &domain.BlobStoreMigration{ID: "m-pending", RepositoryName: "my-repo", Status: "pending"}
	svc := migSvcWithSeededMigration(t, "my-repo", pending)
	r := buildMigRouter(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/repositories/my-repo/blob-store-migration", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "cancelled") //nolint:misspell // matches handler's stable "cancelled" API response key
}
