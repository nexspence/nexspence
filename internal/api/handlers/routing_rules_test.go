package handlers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// mountRoutingRules wires RoutingRuleHandler to a test Gin engine.
func mountRoutingRules(t *testing.T) (*gin.Engine, *testutil.RoutingRuleRepo) {
	t.Helper()
	repo := testutil.NewRoutingRuleRepo()
	svc := service.NewRoutingRuleService(repo)
	h := handlers.NewRoutingRuleHandler(svc)
	r := gin.New()
	r.GET("/service/rest/v1/routing-rules", h.List)
	r.GET("/service/rest/v1/routing-rules/:id", h.Get)
	r.POST("/service/rest/v1/routing-rules", h.Create)
	r.PUT("/service/rest/v1/routing-rules/:id", h.Update)
	r.DELETE("/service/rest/v1/routing-rules/:id", h.Delete)
	return r, repo
}

// ── List ──────────────────────────────────────────────────────

func TestRoutingRuleHandler_List_Empty(t *testing.T) {
	r, _ := mountRoutingRules(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/routing-rules", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []domain.RoutingRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got)
}

func TestRoutingRuleHandler_List_RepoError_500(t *testing.T) {
	r, repo := mountRoutingRules(t)
	repo.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/routing-rules", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Get ───────────────────────────────────────────────────────

func TestRoutingRuleHandler_Get_NotFound_404(t *testing.T) {
	r, _ := mountRoutingRules(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/routing-rules/ghost", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRoutingRuleHandler_Get_OK(t *testing.T) {
	r, repo := mountRoutingRules(t)
	rr := &domain.RoutingRule{Name: "allow-maven", Mode: "ALLOW", Matchers: []string{`.*\.jar`}}
	require.NoError(t, repo.Create(testContext(), rr))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/routing-rules/"+rr.ID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got domain.RoutingRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "allow-maven", got.Name)
}

func TestRoutingRuleHandler_Get_RepoError_500(t *testing.T) {
	r, repo := mountRoutingRules(t)
	repo.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/routing-rules/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Create ────────────────────────────────────────────────────

func TestRoutingRuleHandler_Create_OK(t *testing.T) {
	r, _ := mountRoutingRules(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/routing-rules",
		map[string]any{"name": "block-npm", "mode": "BLOCK", "matchers": []string{`.*\.tgz`}})
	require.Equal(t, http.StatusCreated, rec.Code)
	var got domain.RoutingRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "block-npm", got.Name)
	assert.Equal(t, "BLOCK", got.Mode)
}

func TestRoutingRuleHandler_Create_NoMatchers_OK(t *testing.T) {
	// Handler sets Matchers = [] when nil, so this should still pass validation.
	r, _ := mountRoutingRules(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/routing-rules",
		map[string]any{"name": "allow-all", "mode": "ALLOW"})
	// Service returns 400 for ALLOW with no matchers? No — validateMatchers([]string{}) is valid.
	// name is set, mode is ALLOW, matchers [] → service.Create succeeds.
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestRoutingRuleHandler_Create_BadJSON_400(t *testing.T) {
	r, _ := mountRoutingRules(t)
	rec := doRaw(t, r, http.MethodPost, "/service/rest/v1/routing-rules", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRoutingRuleHandler_Create_EmptyName_400(t *testing.T) {
	// Service validates: name is required → error → handler returns 400.
	r, _ := mountRoutingRules(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/routing-rules",
		map[string]any{"mode": "ALLOW", "matchers": []string{}})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRoutingRuleHandler_Create_InvalidMode_400(t *testing.T) {
	r, _ := mountRoutingRules(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/routing-rules",
		map[string]any{"name": "r1", "mode": "INVALID"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRoutingRuleHandler_Create_InvalidMatcher_400(t *testing.T) {
	r, _ := mountRoutingRules(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/routing-rules",
		map[string]any{"name": "r1", "mode": "ALLOW", "matchers": []string{`[invalid`}})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── Update ────────────────────────────────────────────────────

func TestRoutingRuleHandler_Update_OK(t *testing.T) {
	r, repo := mountRoutingRules(t)
	rr := &domain.RoutingRule{Name: "orig", Mode: "ALLOW", Matchers: []string{}}
	require.NoError(t, repo.Create(testContext(), rr))
	rec := do(t, r, http.MethodPut, "/service/rest/v1/routing-rules/"+rr.ID,
		map[string]any{"name": "updated", "mode": "BLOCK", "matchers": []string{`.*`}})
	require.Equal(t, http.StatusOK, rec.Code)
	var got domain.RoutingRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "updated", got.Name)
	assert.Equal(t, rr.ID, got.ID)
}

func TestRoutingRuleHandler_Update_BadJSON_400(t *testing.T) {
	r, _ := mountRoutingRules(t)
	rec := doRaw(t, r, http.MethodPut, "/service/rest/v1/routing-rules/any", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRoutingRuleHandler_Update_InvalidMode_400(t *testing.T) {
	r, _ := mountRoutingRules(t)
	rec := do(t, r, http.MethodPut, "/service/rest/v1/routing-rules/any",
		map[string]any{"mode": "WRONG"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRoutingRuleHandler_Update_InvalidMatcher_400(t *testing.T) {
	r, _ := mountRoutingRules(t)
	rec := do(t, r, http.MethodPut, "/service/rest/v1/routing-rules/any",
		map[string]any{"mode": "ALLOW", "matchers": []string{`[bad`}})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// ── Delete ────────────────────────────────────────────────────

func TestRoutingRuleHandler_Delete_OK(t *testing.T) {
	r, repo := mountRoutingRules(t)
	rr := &domain.RoutingRule{Name: "todelete", Mode: "BLOCK", Matchers: []string{}}
	require.NoError(t, repo.Create(testContext(), rr))
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/routing-rules/"+rr.ID, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestRoutingRuleHandler_Delete_RepoError_500(t *testing.T) {
	r, repo := mountRoutingRules(t)
	repo.Err = errors.New("db down")
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/routing-rules/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
