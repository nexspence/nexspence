package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// An unknown severity is a malformed rule: 400 on create and on update, with
// the offending value named.
func TestPromotionRule_ScanFailSeverities_Unknown_400(t *testing.T) {
	r, repo, _ := mountPromotion2(t)
	rec := do(t, r, http.MethodPost, "/api/v1/promotion/rules", map[string]any{
		"name": "r", "from_repo": "a", "to_repo": "b", "require_scan_pass": true,
		"scan_fail_severities": []string{"high", "catastrophic"},
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "catastrophic")

	rule := &domain.PromotionRule{Name: "r", FromRepo: "a", ToRepo: "b", RequireScanPass: true}
	require.NoError(t, repo.CreateRule(testContext(), rule))
	rec = do(t, r, http.MethodPut, "/api/v1/promotion/rules/"+rule.ID, map[string]any{
		"name": "r", "from_repo": "a", "to_repo": "b", "require_scan_pass": true,
		"scan_fail_severities": []string{"moderate"},
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "moderate")
}

// The list round-trips normalized: lower-cased, deduplicated, canonical order.
func TestPromotionRule_ScanFailSeverities_RoundTrip(t *testing.T) {
	r, repo, _ := mountPromotion2(t)
	rec := do(t, r, http.MethodPost, "/api/v1/promotion/rules", map[string]any{
		"name": "r", "from_repo": "a", "to_repo": "b", "require_scan_pass": true,
		"scan_fail_severities": []string{"Medium", "CRITICAL", "medium"},
	})
	require.Equal(t, http.StatusCreated, rec.Code, "body=%s", rec.Body.String())
	var created domain.PromotionRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Equal(t, []string{"critical", "medium"}, created.ScanFailSeverities)

	stored, err := repo.GetRule(testContext(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"critical", "medium"}, stored.ScanFailSeverities)
}
