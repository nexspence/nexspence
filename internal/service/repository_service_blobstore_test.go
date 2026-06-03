package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func int64Ptr(v int64) *int64 { return &v }
func strPtr(s string) *string { return &s }

// svcWithStore builds a RepositoryService wired to a single custom blob store.
func svcWithStore(bs *domain.BlobStore) *service.RepositoryService {
	return service.NewRepositoryService(
		testutil.NewRepoRepo(),
		testutil.NewBlobStoreRepo(bs),
		testutil.NewBlobStore(),
		testutil.NewCleanupPolicyRepo(),
	)
}

// Reproduces the bug: Create used Get(name) with a UUID → nil → ErrNotFound.
// After fix, GetByID must resolve and Create must succeed.
func TestRepositoryService_Create_ResolvesBlobStoreByUUID(t *testing.T) {
	const bsID = "adffa5bc-f166-4829-9db1-2e62fa983ce8"
	svc := svcWithStore(&domain.BlobStore{ID: bsID, Name: "store-a", Type: "local"})

	repo := &domain.Repository{
		Name:        "r1",
		Format:      domain.FormatRaw,
		Type:        domain.TypeHosted,
		BlobStoreID: strPtr(bsID),
	}
	if err := svc.Create(context.Background(), repo); err != nil {
		t.Fatalf("Create with UUID blob store: %v", err)
	}
	if repo.BlobStoreID == nil || *repo.BlobStoreID != bsID {
		t.Errorf("BlobStoreID not normalized: got %v", repo.BlobStoreID)
	}
}

func TestRepositoryService_Create_UnknownBlobStore_ReturnsNotFound(t *testing.T) {
	svc := svcWithStore(&domain.BlobStore{ID: "real-id", Name: "store-a", Type: "local"})

	err := svc.Create(context.Background(), &domain.Repository{
		Name:        "r1",
		Format:      domain.FormatRaw,
		Type:        domain.TypeHosted,
		BlobStoreID: strPtr("ghost-uuid"),
	})
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRepositoryService_Create_QuotaExceedsStore_Rejected(t *testing.T) {
	const bsID = "store-uuid-1"
	svc := svcWithStore(&domain.BlobStore{
		ID: bsID, Name: "small", Type: "local",
		QuotaBytes: int64Ptr(100),
	})

	err := svc.Create(context.Background(), &domain.Repository{
		Name:        "too-big",
		Format:      domain.FormatRaw,
		Type:        domain.TypeHosted,
		BlobStoreID: strPtr(bsID),
		QuotaBytes:  int64Ptr(200),
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}

func TestRepositoryService_Create_QuotaWithinStore_Allowed(t *testing.T) {
	const bsID = "store-uuid-2"
	svc := svcWithStore(&domain.BlobStore{
		ID: bsID, Name: "big", Type: "local",
		QuotaBytes: int64Ptr(1000),
	})

	err := svc.Create(context.Background(), &domain.Repository{
		Name:        "within",
		Format:      domain.FormatRaw,
		Type:        domain.TypeHosted,
		BlobStoreID: strPtr(bsID),
		QuotaBytes:  int64Ptr(500),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestRepositoryService_Update_QuotaExceedsStore_Rejected(t *testing.T) {
	const bsID = "store-uuid-3"
	svc := svcWithStore(&domain.BlobStore{
		ID: bsID, Name: "tight", Type: "local",
		QuotaBytes: int64Ptr(500),
	})

	ctx := context.Background()
	if err := svc.Create(ctx, &domain.Repository{
		Name:        "r1",
		Format:      domain.FormatRaw,
		Type:        domain.TypeHosted,
		BlobStoreID: strPtr(bsID),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := svc.Update(ctx, "r1", &domain.Repository{
		QuotaBytes: int64Ptr(1000),
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}
