package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// auto_promote round-trips on create and update (#542), and defaults to off.
func TestPromotionRule_AutoPromote_RoundTrip(t *testing.T) {
	r, repo, _ := mountPromotion2(t)
	rec := do(t, r, http.MethodPost, "/api/v1/promotion/rules", map[string]any{
		"name": "r", "from_repo": "a", "to_repo": "b", "auto_promote": true,
	})
	require.Equal(t, http.StatusCreated, rec.Code, "body=%s", rec.Body.String())
	var created domain.PromotionRule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.True(t, created.AutoPromote)
	stored, err := repo.GetRule(testContext(), created.ID)
	require.NoError(t, err)
	assert.True(t, stored.AutoPromote)

	rec = do(t, r, http.MethodPut, "/api/v1/promotion/rules/"+created.ID, map[string]any{
		"name": "r", "from_repo": "a", "to_repo": "b",
	})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	stored, err = repo.GetRule(testContext(), created.ID)
	require.NoError(t, err)
	assert.False(t, stored.AutoPromote, "a client that omits auto_promote turns it off")
}
