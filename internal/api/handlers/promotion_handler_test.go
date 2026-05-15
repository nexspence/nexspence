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

func newTestPromotionHandler(t *testing.T) *handlers.PromotionHandler {
	t.Helper()
	promoRepo := testutil.NewPromotionRepo()
	compRepo := testutil.NewComponentRepo()
	assetRepo := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()
	blobRepo := testutil.NewBlobStoreRepo()
	scanRepo := testutil.NewScanResultRepo()
	repoRepo := testutil.NewRepoRepo()
	registry := storage.NewRegistry(blobStore)
	svc, err := service.NewPromotionService(promoRepo, compRepo, assetRepo, repoRepo, blobRepo, scanRepo, blobStore, registry)
	if err != nil {
		t.Fatalf("NewPromotionService: %v", err)
	}
	return handlers.NewPromotionHandler(svc)
}

func buildPromotionRouter(t *testing.T) *gin.Engine {
	t.Helper()
	h := newTestPromotionHandler(t)
	r := gin.New()
	r.GET("/api/v1/promotion/rules", h.ListRules)
	r.POST("/api/v1/promotion/rules", h.CreateRule)
	r.PUT("/api/v1/promotion/rules/:id", h.UpdateRule)
	r.DELETE("/api/v1/promotion/rules/:id", h.DeleteRule)
	r.GET("/api/v1/components/:id/promotion-rules", h.GetComponentRules)
	r.POST("/api/v1/promotion/promote", h.Promote)
	r.GET("/api/v1/promotion/requests", h.ListRequests)
	r.POST("/api/v1/promotion/requests/:id/approve", h.Approve)
	r.POST("/api/v1/promotion/requests/:id/reject", h.Reject)
	return r
}

// TestPromotionHandler_ListRules_Empty verifies GET returns 200 with empty array when no rules exist.
func TestPromotionHandler_ListRules_Empty(t *testing.T) {
	r := buildPromotionRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/promotion/rules", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}
	var rules []domain.PromotionRule
	if err := json.Unmarshal(w.Body.Bytes(), &rules); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("want empty array, got %d rules", len(rules))
	}
}

// TestPromotionHandler_CreateRule verifies POST returns 201 with a rule that has an ID.
func TestPromotionHandler_CreateRule(t *testing.T) {
	r := buildPromotionRouter(t)

	body := `{"name":"staging-to-prod","from_repo":"staging","to_repo":"production"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/promotion/rules", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201 got %d body=%s", w.Code, w.Body.String())
	}
	var rule domain.PromotionRule
	if err := json.Unmarshal(w.Body.Bytes(), &rule); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if rule.ID == "" {
		t.Fatal("want rule.ID to be set, got empty string")
	}
	if rule.Name != "staging-to-prod" {
		t.Fatalf("want name=staging-to-prod got %q", rule.Name)
	}
}

// TestPromotionHandler_Promote_MissingFields verifies POST /promote returns 400 when rule_id is missing.
func TestPromotionHandler_Promote_MissingFields(t *testing.T) {
	r := buildPromotionRouter(t)

	body := `{"component_ids":["comp-1"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/promotion/promote", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp["error"] == "" {
		t.Fatal("want non-empty error field")
	}
}

// TestPromotionHandler_Approve_NotFound verifies POST /approve returns 400 for a non-existent request.
func TestPromotionHandler_Approve_NotFound(t *testing.T) {
	r := buildPromotionRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/promotion/requests/doesnotexist/approve", nil)
	// Simulate userID set by auth middleware.
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp["error"] == "" {
		t.Fatal("want non-empty error field")
	}
}
