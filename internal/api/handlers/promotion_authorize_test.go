package handlers_test

import (
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

// promotionAuthz mounts the promotion handler with RBAC wired, the way the
// router wires it, and returns the repos a test seeds its state into.
func promotionAuthz(t *testing.T, privs []repository.PrivilegeWithSelector) (*gin.Engine, *testutil.PromotionRepo, *testutil.ComponentRepo, *testutil.RepoRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	promoRepo := testutil.NewPromotionRepo()
	compRepo := testutil.NewComponentRepo()
	assetRepo := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()
	blobRepo := testutil.NewBlobStoreRepo()
	scanRepo := testutil.NewScanResultRepo()
	repoRepo := testutil.NewRepoRepo()
	svc, err := service.NewPromotionService(promoRepo, compRepo, assetRepo, repoRepo, blobRepo, scanRepo, testutil.NewFakeResolver(blobStore))
	require.NoError(t, err)
	rbac := service.NewRBACService(fixedPrivilegesRBACRepo{privs: privs}, repoRepo, zap.NewNop().Sugar(), true)
	h := handlers.NewPromotionHandler(svc).WithRBAC(repoRepo, rbac)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "bob")
		c.Set("roles", []string{"dev"})
		c.Next()
	})
	r.POST("/api/v1/promotion/promote", h.Promote)
	r.GET("/api/v1/promotion/requests", h.ListRequests)
	return r, promoRepo, compRepo, repoRepo
}

// seedStagingPromotion creates the staging → production rule and a component
// living in staging, the shape both tests below promote.
func seedStagingPromotion(t *testing.T, promoRepo *testutil.PromotionRepo, compRepo *testutil.ComponentRepo, repoRepo *testutil.RepoRepo) *domain.PromotionRule {
	t.Helper()
	ctx := testContext()
	require.NoError(t, repoRepo.Create(ctx, testutil.SimpleRepo("staging", "raw")))
	require.NoError(t, repoRepo.Create(ctx, testutil.SimpleRepo("production", "raw")))
	compRepo.AddComponent(&domain.Component{
		ID: "comp-1", Repository: "staging", Format: "raw",
		Group: "com/example", Name: "mylib", Version: "1.0.0",
	})
	rule := &domain.PromotionRule{Name: "stage-to-prod", FromRepo: "staging", ToRepo: "production"}
	require.NoError(t, promoRepo.CreateRule(ctx, rule))
	return rule
}

// The promote endpoint sits in the non-admin group, so the only thing standing
// between an ordinary account and the production repository is the privilege
// check inside the handler. There was none: the rule's own gates decide what
// is promoted, never who is asking, so a caller who could publish to staging
// could push artifacts into production — a repository they may not even be
// allowed to read.
func TestPromotionHandler_Promote_DeniesCallerWithoutWriteOnTarget(t *testing.T) {
	r, promoRepo, compRepo, repoRepo := promotionAuthz(t, []repository.PrivilegeWithSelector{
		{Actions: []string{"read", "browse"}, Expression: `repository == "staging"`},
	})
	rule := seedStagingPromotion(t, promoRepo, compRepo, repoRepo)

	rec := do(t, r, http.MethodPost, "/api/v1/promotion/promote", map[string]any{
		"rule_id": rule.ID, "component_ids": []string{"comp-1"},
	})
	assert.Equal(t, http.StatusForbidden, rec.Code, "body=%s", rec.Body.String())

	reqs, err := promoRepo.ListRequests(testContext(), "")
	require.NoError(t, err)
	assert.Empty(t, reqs, "a refused promotion must not leave a request behind")
}

// The same call from a caller that does hold write on the target repository
// goes through — the check is a privilege check, not a blanket refusal.
func TestPromotionHandler_Promote_AllowsCallerWithWriteOnTarget(t *testing.T) {
	r, promoRepo, compRepo, repoRepo := promotionAuthz(t, []repository.PrivilegeWithSelector{
		{Actions: []string{"read", "browse"}, Expression: `repository == "staging"`},
		{Actions: []string{"write"}, Expression: `repository == "production"`},
	})
	rule := seedStagingPromotion(t, promoRepo, compRepo, repoRepo)

	rec := do(t, r, http.MethodPost, "/api/v1/promotion/promote", map[string]any{
		"rule_id": rule.ID, "component_ids": []string{"comp-1"},
	})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	reqs, err := promoRepo.ListRequests(testContext(), "")
	require.NoError(t, err)
	assert.Len(t, reqs, 1)
}

// The request list is the harvest: it names the rule and the component, which
// is everything the promote endpoint needs. A caller who cannot browse both
// sides of a rule must not see its requests either.
func TestPromotionHandler_ListRequests_HidesRulesTheCallerCannotSee(t *testing.T) {
	r, promoRepo, compRepo, repoRepo := promotionAuthz(t, []repository.PrivilegeWithSelector{
		{Actions: []string{"read", "browse"}, Expression: `repository == "staging"`},
	})
	rule := seedStagingPromotion(t, promoRepo, compRepo, repoRepo)
	ctx := testContext()
	require.NoError(t, promoRepo.CreateRequest(ctx, &domain.PromotionRequest{
		RuleID: rule.ID, ComponentID: "comp-1", Status: domain.PromotionPending, RequestedBy: "someone",
	}))

	rec := do(t, r, http.MethodGet, "/api/v1/promotion/requests", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.NotContains(t, rec.Body.String(), rule.ID, "a request naming a rule the caller cannot see must not be listed")
}
