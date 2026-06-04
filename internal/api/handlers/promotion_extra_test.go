package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// mountPromotion2 builds a PromotionHandler over mocks and returns the engine plus the
// underlying promotion + component repos so tests can seed state directly. Distinct from
// the existing buildPromotionRouter helper (which exposes no seams).
func mountPromotion2(t *testing.T) (*gin.Engine, *testutil.PromotionRepo, *testutil.ComponentRepo) {
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
	require.NoError(t, err)
	h := handlers.NewPromotionHandler(svc)

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
	return r, promoRepo, compRepo
}

// ── CreateRule ─────────────────────────────────────────────────────────────────

func TestPromotionExtra_CreateRule_BadJSON_400(t *testing.T) {
	r, _, _ := mountPromotion2(t)
	rec := doRaw(t, r, http.MethodPost, "/api/v1/promotion/rules", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPromotionExtra_CreateRule_SameRepo_400(t *testing.T) {
	r, _, _ := mountPromotion2(t)
	// from_repo == to_repo → service rejects → handler 400.
	rec := do(t, r, http.MethodPost, "/api/v1/promotion/rules", map[string]any{
		"name": "bad", "from_repo": "x", "to_repo": "x",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── UpdateRule ─────────────────────────────────────────────────────────────────

func TestPromotionExtra_UpdateRule_OK(t *testing.T) {
	r, repo, _ := mountPromotion2(t)
	rule := &domain.PromotionRule{Name: "orig", FromRepo: "staging", ToRepo: "prod"}
	require.NoError(t, repo.CreateRule(testContext(), rule))

	rec := do(t, r, http.MethodPut, "/api/v1/promotion/rules/"+rule.ID, map[string]any{
		"name": "renamed", "from_repo": "staging", "to_repo": "prod",
	})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var got domain.PromotionRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, rule.ID, got.ID)
	assert.Equal(t, "renamed", got.Name)
}

func TestPromotionExtra_UpdateRule_BadJSON_400(t *testing.T) {
	r, _, _ := mountPromotion2(t)
	rec := doRaw(t, r, http.MethodPut, "/api/v1/promotion/rules/any", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPromotionExtra_UpdateRule_Invalid_400(t *testing.T) {
	r, _, _ := mountPromotion2(t)
	// empty name → service returns "name is required" → handler 400.
	rec := do(t, r, http.MethodPut, "/api/v1/promotion/rules/any", map[string]any{
		"from_repo": "a", "to_repo": "b",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── DeleteRule ─────────────────────────────────────────────────────────────────

func TestPromotionExtra_DeleteRule_204(t *testing.T) {
	r, repo, _ := mountPromotion2(t)
	rule := &domain.PromotionRule{Name: "del", FromRepo: "a", ToRepo: "b"}
	require.NoError(t, repo.CreateRule(testContext(), rule))
	rec := do(t, r, http.MethodDelete, "/api/v1/promotion/rules/"+rule.ID, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

// ── GetComponentRules ──────────────────────────────────────────────────────────

func TestPromotionExtra_GetComponentRules_NotFound_404(t *testing.T) {
	r, _, _ := mountPromotion2(t)
	rec := do(t, r, http.MethodGet, "/api/v1/components/ghost/promotion-rules", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPromotionExtra_GetComponentRules_OK(t *testing.T) {
	r, repo, comps := mountPromotion2(t)
	comp := &domain.Component{Name: "lib", Repository: "staging", Format: "maven2"}
	require.NoError(t, comps.Create(testContext(), comp))
	// Rule keyed on the component's from-repo, empty path filter matches.
	require.NoError(t, repo.CreateRule(testContext(), &domain.PromotionRule{
		Name: "promote-staging", FromRepo: "staging", ToRepo: "prod",
	}))

	rec := do(t, r, http.MethodGet, "/api/v1/components/"+comp.ID+"/promotion-rules", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var rules []domain.PromotionRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rules))
	require.Len(t, rules, 1)
	assert.Equal(t, "promote-staging", rules[0].Name)
}

// ── Promote ────────────────────────────────────────────────────────────────────

func TestPromotionExtra_Promote_BadJSON_400(t *testing.T) {
	r, _, _ := mountPromotion2(t)
	rec := doRaw(t, r, http.MethodPost, "/api/v1/promotion/promote", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPromotionExtra_Promote_RuleNotFound_422(t *testing.T) {
	r, _, _ := mountPromotion2(t)
	rec := do(t, r, http.MethodPost, "/api/v1/promotion/promote", map[string]any{
		"rule_id": "ghost", "component_ids": []string{"comp-1"},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestPromotionExtra_Promote_ManualApproval_OK(t *testing.T) {
	r, repo, comps := mountPromotion2(t)
	comp := &domain.Component{Name: "lib", Repository: "staging", Format: "maven2"}
	require.NoError(t, comps.Create(testContext(), comp))
	rule := &domain.PromotionRule{
		Name: "manual", FromRepo: "staging", ToRepo: "prod", RequireManualApproval: true,
	}
	require.NoError(t, repo.CreateRule(testContext(), rule))

	rec := do(t, r, http.MethodPost, "/api/v1/promotion/promote", map[string]any{
		"rule_id": rule.ID, "component_ids": []string{comp.ID},
	})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var body struct {
		Requests []domain.PromotionRequest `json:"requests"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Requests, 1)
	assert.Equal(t, domain.PromotionPending, body.Requests[0].Status)
}

// ── ListRequests ───────────────────────────────────────────────────────────────

func TestPromotionExtra_ListRequests_Empty_OK(t *testing.T) {
	r, _, _ := mountPromotion2(t)
	rec := do(t, r, http.MethodGet, "/api/v1/promotion/requests", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var reqs []domain.PromotionRequest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &reqs))
	assert.Empty(t, reqs)
}

func TestPromotionExtra_ListRequests_StatusFilter_OK(t *testing.T) {
	r, repo, _ := mountPromotion2(t)
	require.NoError(t, repo.CreateRequest(testContext(), &domain.PromotionRequest{
		RuleID: "r1", ComponentID: "c1", Status: domain.PromotionPending,
	}))
	rec := do(t, r, http.MethodGet, "/api/v1/promotion/requests?status=pending", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var reqs []domain.PromotionRequest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &reqs))
	require.Len(t, reqs, 1)
}

// ── Reject ─────────────────────────────────────────────────────────────────────

func TestPromotionExtra_Reject_NotFound_400(t *testing.T) {
	r, _, _ := mountPromotion2(t)
	rec := do(t, r, http.MethodPost, "/api/v1/promotion/requests/ghost/reject",
		map[string]any{"reason": "no"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPromotionExtra_Reject_OK(t *testing.T) {
	r, repo, _ := mountPromotion2(t)
	req := &domain.PromotionRequest{
		RuleID: "r1", ComponentID: "c1", Status: domain.PromotionPending,
	}
	require.NoError(t, repo.CreateRequest(testContext(), req))
	rec := do(t, r, http.MethodPost, "/api/v1/promotion/requests/"+req.ID+"/reject",
		map[string]any{"reason": "not ready"})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, true, body["ok"])
}
