package service_test

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Promotion between repositories on the implicit default blob store — the
// common case — must record the seeded default row's UUID on the copied
// assets: assets.blob_store_id is a NOT NULL foreign key, and the old
// empty-string answer from resolveStore failed every such promotion with a
// raw constraint error (#256).
func TestPromotionService_DefaultStoreRepos_PromoteRecordsRealStoreID(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	// Both repos use the implicit default store (no BlobStoreID).
	fromRepo := testutil.SimpleRepo("staging", "raw")
	toRepo := testutil.SimpleRepo("production", "raw")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	comp := &domain.Component{
		ID: "comp-default-store", RepositoryID: fromRepo.ID, Repository: "staging",
		Format: "raw", Group: "com/example", Name: "mylib", Version: "1.0.0",
	}
	compRepo.AddComponent(comp)

	blobKey := "staging:mylib-1.0.0.jar"
	if err := blobStore.PutBytes(ctx, blobKey, []byte("bytes")); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	assetRepo.Create(ctx, &domain.Asset{
		ComponentID: comp.ID, RepositoryID: fromRepo.ID, Repository: "staging",
		Path: "mylib-1.0.0.jar", BlobKey: blobKey, SizeBytes: 5,
	})

	rule := &domain.PromotionRule{Name: "auto", FromRepo: "staging", ToRepo: "production"}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	reqs, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-1")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if len(reqs) != 1 || reqs[0].Status != domain.PromotionCompleted {
		t.Fatalf("expected one completed request, got %+v", reqs)
	}

	// The copied asset carries the seeded default row's UUID, never "".
	copied, err := assetRepo.ListByRepoAndPath(ctx, "production", "")
	if err != nil {
		t.Fatalf("ListByRepoAndPath: %v", err)
	}
	if len(copied) == 0 {
		t.Fatal("no asset was copied into production")
	}
	for _, a := range copied {
		if a.BlobStoreID != "00000000-0000-0000-0000-000000000001" {
			t.Fatalf("copied asset blob_store_id: got %q, want the seeded default store UUID", a.BlobStoreID)
		}
	}
}
