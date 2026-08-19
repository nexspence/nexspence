package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
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

// The "default blob store not found" diagnostic must actually be reachable:
// the postgres repo reports a missing row as ErrNotFound, not (nil, nil), and
// an err-first check would bury the actionable message under a bare
// "not found" (the default row is deletable via the blobstore API when
// nothing references it).
func TestPromotionService_MissingDefaultStoreRow_NamesTheFix(t *testing.T) {
	promoRepo := testutil.NewPromotionRepo()
	compRepo := testutil.NewComponentRepo()
	assetRepo := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()
	// Seeding a non-default row keeps the constructor from creating "default".
	blobRepo := testutil.NewBlobStoreRepo(&domain.BlobStore{ID: "bs-other", Name: "other", Type: "local"})
	repoRepo := testutil.NewRepoRepo()
	svc, err := service.NewPromotionService(
		promoRepo, compRepo, assetRepo, repoRepo, blobRepo, testutil.NewScanResultRepo(), testutil.NewFakeResolver(blobStore),
	)
	if err != nil {
		t.Fatalf("NewPromotionService: %v", err)
	}
	ctx := context.Background()

	repoRepo.Create(ctx, testutil.SimpleRepo("staging", "raw"))
	repoRepo.Create(ctx, testutil.SimpleRepo("production", "raw"))
	comp := &domain.Component{ID: "c1", Repository: "staging", Format: "raw", Group: "g", Name: "n", Version: "1"}
	compRepo.AddComponent(comp)
	rule := &domain.PromotionRule{Name: "auto", FromRepo: "staging", ToRepo: "production"}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	reqs, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-1")
	if err != nil || len(reqs) != 1 {
		t.Fatalf("Promote: %v (%d requests)", err, len(reqs))
	}
	if reqs[0].Status != domain.PromotionFailed {
		t.Fatalf("status: got %s, want failed", reqs[0].Status)
	}
	if !strings.Contains(reqs[0].Error, "default blob store not found") {
		t.Fatalf("error %q does not carry the actionable diagnostic", reqs[0].Error)
	}
}
