package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func newMigSvc(t *testing.T, repoName, sourceID, targetID string) *service.BlobStoreMigrationService {
	t.Helper()
	repoRepo := testutil.NewRepoRepo(&domain.Repository{ID: "r1", Name: repoName, BlobStoreID: &sourceID})
	blobRepo := testutil.NewBlobStoreRepo(
		&domain.BlobStore{ID: sourceID, Name: "source", Type: "local", Config: map[string]any{"path": t.TempDir()}},
		&domain.BlobStore{ID: targetID, Name: "target", Type: "local", Config: map[string]any{"path": t.TempDir()}},
	)
	migRepo := testutil.NewBlobStoreMigrationRepo()
	assetRepo := testutil.NewAssetRepo()
	reg := storage.NewRegistry(testutil.NewBlobStore())
	return service.NewBlobStoreMigrationService(migRepo, assetRepo, repoRepo, blobRepo, reg)
}

func buildMigRouter(svc *service.BlobStoreMigrationService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handlers.NewBlobStoreMigrationHandler(svc)
	r.POST("/api/v1/repositories/:name/migrate-blob-store", h.Start)
	r.GET("/api/v1/repositories/:name/blob-store-migration", h.GetLatest)
	r.DELETE("/api/v1/repositories/:name/blob-store-migration", h.Cancel)
	return r
}

// TestBlobStoreMigrationHandler_Start_201 verifies 201 is returned when migration starts successfully.
func TestBlobStoreMigrationHandler_Start_201(t *testing.T) {
	svc := newMigSvc(t, "my-repo", "src-store", "tgt-store")
	r := buildMigRouter(svc)

	body := `{"targetStoreId":"tgt-store"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/my-repo/migrate-blob-store", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201 got %d body=%s", w.Code, w.Body.String())
	}
	var m domain.BlobStoreMigration
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if m.ID == "" {
		t.Error("expected non-empty migration ID")
	}
	if m.RepositoryName != "my-repo" {
		t.Errorf("RepositoryName = %q; want %q", m.RepositoryName, "my-repo")
	}
}

// TestBlobStoreMigrationHandler_Start_409 verifies 409 when an active migration already exists.
func TestBlobStoreMigrationHandler_Start_409(t *testing.T) {
	svc := newMigSvc(t, "my-repo", "src-store", "tgt-store")
	r := buildMigRouter(svc)

	// Start first migration.
	body := `{"targetStoreId":"tgt-store"}`
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/my-repo/migrate-blob-store", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first Start: want 201 got %d body=%s", w1.Code, w1.Body.String())
	}

	// Second start should conflict.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/my-repo/migrate-blob-store", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Fatalf("want 409 got %d body=%s", w2.Code, w2.Body.String())
	}
}

// TestBlobStoreMigrationHandler_GetLatest_200 verifies 200 with progress fields.
func TestBlobStoreMigrationHandler_GetLatest_200(t *testing.T) {
	svc := newMigSvc(t, "my-repo", "src-store", "tgt-store")
	r := buildMigRouter(svc)

	// Start a migration so there is one to fetch.
	startBody := `{"targetStoreId":"tgt-store"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/my-repo/migrate-blob-store", strings.NewReader(startBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("Start: want 201 got %d", w.Code)
	}

	// Now fetch it.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/repositories/my-repo/blob-store-migration", nil)
	getW := httptest.NewRecorder()
	r.ServeHTTP(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", getW.Code, getW.Body.String())
	}
	var m domain.BlobStoreMigration
	if err := json.Unmarshal(getW.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if m.ID == "" {
		t.Error("expected non-empty ID")
	}
	if m.RepositoryName != "my-repo" {
		t.Errorf("RepositoryName = %q; want %q", m.RepositoryName, "my-repo")
	}
}

// TestBlobStoreMigrationHandler_GetLatest_404 verifies 404 when no migration exists.
func TestBlobStoreMigrationHandler_GetLatest_404(t *testing.T) {
	svc := newMigSvc(t, "my-repo", "src-store", "tgt-store")
	r := buildMigRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/repositories/my-repo/blob-store-migration", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 got %d body=%s", w.Code, w.Body.String())
	}
}

// TestBlobStoreMigrationHandler_Cancel verifies cancel returns 200 for active or 400 if already done.
func TestBlobStoreMigrationHandler_Cancel(t *testing.T) {
	svc := newMigSvc(t, "my-repo", "src-store", "tgt-store")
	r := buildMigRouter(svc)

	// Start a migration first.
	startBody := `{"targetStoreId":"tgt-store"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/my-repo/migrate-blob-store", strings.NewReader(startBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("Start: want 201 got %d", w.Code)
	}

	// Cancel it.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/repositories/my-repo/blob-store-migration", nil)
	delW := httptest.NewRecorder()
	r.ServeHTTP(delW, delReq)

	// 200 if still active, 400 if the goroutine already finished — both are valid.
	if delW.Code != http.StatusOK && delW.Code != http.StatusBadRequest {
		t.Fatalf("want 200 or 400 got %d body=%s", delW.Code, delW.Body.String())
	}
}
