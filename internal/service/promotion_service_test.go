package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func newTestPromotionSvc(t *testing.T) (
	*service.PromotionService,
	*testutil.PromotionRepo,
	*testutil.ComponentRepo,
	*testutil.AssetRepo,
	*testutil.BlobStore,
	*testutil.RepoRepo,
	*testutil.BlobStoreRepo,
	*testutil.ScanResultRepo,
) {
	t.Helper()
	promoRepo := testutil.NewPromotionRepo()
	compRepo := testutil.NewComponentRepo()
	assetRepo := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()
	blobRepo := testutil.NewBlobStoreRepo()
	scanRepo := testutil.NewScanResultRepo()
	repoRepo := testutil.NewRepoRepo()
	registry := storage.NewRegistry(blobStore)

	svc, err := service.NewPromotionService(
		promoRepo, compRepo, assetRepo, repoRepo, blobRepo, scanRepo, blobStore, registry,
	)
	if err != nil {
		t.Fatalf("NewPromotionService: %v", err)
	}
	return svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, blobRepo, scanRepo
}

// TestPromotionService_CreateRule_Validation checks that invalid rule inputs are rejected.
func TestPromotionService_CreateRule_Validation(t *testing.T) {
	svc, _, _, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	t.Run("empty name", func(t *testing.T) {
		err := svc.CreateRule(ctx, &domain.PromotionRule{
			FromRepo: "staging",
			ToRepo:   "production",
		})
		if err == nil || !strings.Contains(err.Error(), "name is required") {
			t.Fatalf("expected name required error, got: %v", err)
		}
	})

	t.Run("same from and to", func(t *testing.T) {
		err := svc.CreateRule(ctx, &domain.PromotionRule{
			Name:     "self-loop",
			FromRepo: "staging",
			ToRepo:   "staging",
		})
		if err == nil || !strings.Contains(err.Error(), "must be different") {
			t.Fatalf("expected same-repo error, got: %v", err)
		}
	})

	t.Run("invalid CEL expression", func(t *testing.T) {
		err := svc.CreateRule(ctx, &domain.PromotionRule{
			Name:       "bad-cel",
			FromRepo:   "staging",
			ToRepo:     "production",
			PathFilter: "this is not valid CEL !!!",
		})
		if err == nil || !strings.Contains(err.Error(), "path_filter") {
			t.Fatalf("expected CEL validation error, got: %v", err)
		}
	})
}

// TestPromotionService_AutoApprove_CopiesBlob verifies that a rule with RequireManualApproval=false
// immediately copies the blob and sets status=completed.
func TestPromotionService_AutoApprove_CopiesBlob(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	// Seed source and target repos.
	fromRepo := testutil.SimpleRepo("staging", "raw")
	toRepo := testutil.SimpleRepo("production", "raw")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	// Seed a component in staging.
	comp := &domain.Component{
		ID:           "comp-src-1",
		RepositoryID: fromRepo.ID,
		Repository:   fromRepo.Name,
		Format:       "raw",
		Group:        "com/example",
		Name:         "mylib",
		Version:      "1.0.0",
	}
	compRepo.AddComponent(comp)

	// Seed an asset with a blob.
	blobKey := "staging:mylib-1.0.0.jar"
	if err := blobStore.PutBytes(ctx, blobKey, []byte("binary content")); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	assetRepo.Create(ctx, &domain.Asset{
		ComponentID:  comp.ID,
		RepositoryID: fromRepo.ID,
		Repository:   fromRepo.Name,
		Path:         "mylib-1.0.0.jar",
		BlobKey:      blobKey,
		SizeBytes:    14,
		ContentType:  "application/java-archive",
	})

	// Create an auto-approve rule.
	rule := &domain.PromotionRule{
		Name:                  "auto-to-prod",
		FromRepo:              "staging",
		ToRepo:                "production",
		RequireManualApproval: false,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	// Execute promotion.
	results, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-1")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != domain.PromotionCompleted {
		t.Errorf("expected status completed, got %s", results[0].Status)
	}

	// Verify blob was written to the target store.
	newKey := results[0].ComponentID // use the blob key we can predict indirectly
	_ = newKey
	// The new blob key is base.BlobKey("production", "mylib-1.0.0.jar") — just check at least one new key exists.
	keys, err := blobStore.ListKeys(ctx)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(keys) < 2 {
		t.Errorf("expected at least 2 blobs (source + copied), got %d", len(keys))
	}

	// Verify a new component was created in the target repo.
	page, err := compRepo.ListByRepoNames(ctx, []string{"production"}, 100, 0)
	if err != nil {
		t.Fatalf("ListByRepoNames: %v", err)
	}
	if len(page.Items) == 0 {
		t.Error("expected a component to be created in target repo")
	}
}

// TestPromotionService_ManualApproval_StaysPending verifies that a manual-approval rule
// creates a pending request and Approve transitions it to completed.
func TestPromotionService_ManualApproval_StaysPending(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := testutil.SimpleRepo("dev", "raw")
	toRepo := testutil.SimpleRepo("qa", "raw")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	comp := &domain.Component{
		ID:           "comp-manual-1",
		RepositoryID: fromRepo.ID,
		Repository:   fromRepo.Name,
		Format:       "raw",
		Group:        "com/example",
		Name:         "app",
		Version:      "2.0.0",
	}
	compRepo.AddComponent(comp)

	blobKey := "dev:app-2.0.0.tar.gz"
	if err := blobStore.PutBytes(ctx, blobKey, []byte("app content")); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	assetRepo.Create(ctx, &domain.Asset{
		ComponentID:  comp.ID,
		RepositoryID: fromRepo.ID,
		Repository:   fromRepo.Name,
		Path:         "app-2.0.0.tar.gz",
		BlobKey:      blobKey,
		SizeBytes:    11,
		ContentType:  "application/gzip",
	})

	rule := &domain.PromotionRule{
		Name:                  "manual-dev-to-qa",
		FromRepo:              "dev",
		ToRepo:                "qa",
		RequireManualApproval: true,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	results, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-2")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != domain.PromotionPending {
		t.Errorf("expected status pending before approval, got %s", results[0].Status)
	}

	reqID := results[0].ID

	// Approve the request.
	if err := svc.Approve(ctx, reqID, "reviewer-1"); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// Check updated status in the repo.
	updated := promoRepo.Requests[reqID]
	if updated == nil {
		t.Fatal("request not found in repo after approve")
	}
	if updated.Status != domain.PromotionCompleted {
		t.Errorf("expected completed after approve, got %s", updated.Status)
	}
	if updated.ReviewedBy == nil || *updated.ReviewedBy != "reviewer-1" {
		t.Error("expected ReviewedBy to be set to reviewer-1")
	}
}

// TestPromotionService_Reject verifies that a pending request can be rejected with a reason.
func TestPromotionService_Reject(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := testutil.SimpleRepo("dev", "raw")
	toRepo := testutil.SimpleRepo("prod", "raw")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	comp := &domain.Component{
		ID:           "comp-reject-1",
		RepositoryID: fromRepo.ID,
		Repository:   fromRepo.Name,
		Format:       "raw",
		Group:        "com/example",
		Name:         "svc",
		Version:      "3.0.0",
	}
	compRepo.AddComponent(comp)

	rule := &domain.PromotionRule{
		Name:                  "reject-test-rule",
		FromRepo:              "dev",
		ToRepo:                "prod",
		RequireManualApproval: true,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	results, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-3")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	reqID := results[0].ID

	reason := "security review failed"
	if err := svc.Reject(ctx, reqID, "reviewer-2", reason); err != nil {
		t.Fatalf("Reject: %v", err)
	}

	updated := promoRepo.Requests[reqID]
	if updated == nil {
		t.Fatal("request not found after reject")
	}
	if updated.Status != domain.PromotionRejected {
		t.Errorf("expected rejected status, got %s", updated.Status)
	}
	if updated.Error != reason {
		t.Errorf("expected error=%q, got %q", reason, updated.Error)
	}
	if updated.ReviewedBy == nil || *updated.ReviewedBy != "reviewer-2" {
		t.Error("expected ReviewedBy to be set")
	}
}

// TestPromotionService_PathFilter verifies that ListRulesForComponent only returns rules
// whose path filter matches the component.
func TestPromotionService_PathFilter(t *testing.T) {
	svc, promoRepo, compRepo, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	// Create a rule that matches only paths starting with /com/myco/
	rule := &domain.PromotionRule{
		Name:       "myco-only",
		FromRepo:   "staging",
		ToRepo:     "production",
		PathFilter: `path.startsWith("/com/myco/")`,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	// Component whose path matches: /com/myco/ + name
	matchingComp := &domain.Component{
		ID:         "comp-matching",
		Repository: "staging",
		Format:     "raw",
		Group:      "com/myco",
		Name:       "artifact",
		Version:    "1.0",
	}
	compRepo.AddComponent(matchingComp)

	// Component whose path does not match: /org/other/ + name
	nonMatchingComp := &domain.Component{
		ID:         "comp-nonmatching",
		Repository: "staging",
		Format:     "raw",
		Group:      "org/other",
		Name:       "artifact",
		Version:    "1.0",
	}
	compRepo.AddComponent(nonMatchingComp)

	t.Run("matching component gets rule", func(t *testing.T) {
		rules, err := svc.ListRulesForComponent(ctx, matchingComp.ID)
		if err != nil {
			t.Fatalf("ListRulesForComponent: %v", err)
		}
		if len(rules) != 1 {
			t.Errorf("expected 1 matching rule, got %d", len(rules))
		}
	})

	t.Run("non-matching component gets no rule", func(t *testing.T) {
		rules, err := svc.ListRulesForComponent(ctx, nonMatchingComp.ID)
		if err != nil {
			t.Fatalf("ListRulesForComponent: %v", err)
		}
		if len(rules) != 0 {
			t.Errorf("expected 0 rules for non-matching component, got %d", len(rules))
		}
	})
}

// Ensure time package is used (referenced in domain types).
var _ = time.Now
