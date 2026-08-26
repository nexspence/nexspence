package base_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Quota enforcement must be atomic with the registration, not a separate
// check-then-write: two concurrent uploads each individually within quota at
// check time could otherwise both pass and jointly exceed the limit.

// quotaAtomicDeps is deps() plus a "default" blob store row (resolveBlobStoreObj
// falls back to it when the repo pins no store).
func quotaAtomicDeps(repo *domain.Repository, defaultStore *domain.BlobStore) (formats.Deps, *testutil.BlobStore, *testutil.AssetRepo, *testutil.BlobStoreRepo) {
	d, blobStore, _, assets := deps(repo)
	blobs := testutil.NewBlobStoreRepo(defaultStore)
	d.Blobs = blobs
	return d, blobStore, assets, blobs
}

// Two concurrent uploads, 10 bytes each, into a repository with a 15-byte
// quota, both aligned on the same usage snapshot: exactly one may land.
func TestStoreArtifact_ConcurrentUploads_CannotJointlyExceedRepoQuota(t *testing.T) {
	repo := testutil.SimpleRepo("cqrepo", "raw")
	quota := int64(15)
	repo.QuotaBytes = &quota
	d, blobStore, assets, _ := quotaAtomicDeps(repo, &domain.BlobStore{
		ID: "00000000-0000-0000-0000-000000000001", Name: "default", Type: "local",
	})
	ctx := context.Background()

	// Barrier: hold each caller right after its usage read until both have read
	// (or a timeout, for the fixed path where the enforcement read is serialized
	// and both can never be inside it at once).
	var barrierMu sync.Mutex
	arrived := 0
	bothRead := make(chan struct{})
	assets.SumSizeByRepoHook = func(string) {
		barrierMu.Lock()
		arrived++
		if arrived == 2 {
			close(bothRead)
		}
		barrierMu.Unlock()
		select {
		case <-bothRead:
		case <-time.After(300 * time.Millisecond):
		}
	}

	body := "ten-bytes!" // 10 bytes
	var wg sync.WaitGroup
	errs := make([]error, 2)
	paths := []string{"/a.bin", "/b.bin"}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = base.StoreArtifact(ctx, d, "cqrepo", paths[i],
				"application/octet-stream", base.Coords{Name: "f" + paths[i]},
				strings.NewReader(body), int64(len(body)))
		}(i)
	}
	wg.Wait()

	quotaErrs := 0
	for _, err := range errs {
		if errors.Is(err, base.ErrQuotaExceeded) {
			quotaErrs++
		} else if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if quotaErrs != 1 {
		t.Errorf("%d of 2 uploads rejected, want exactly 1 — both passed an unserialized quota check and jointly exceeded the limit", quotaErrs)
	}

	rows, err := assets.ListByRepoAndPath(ctx, "cqrepo", "")
	require.NoError(t, err)
	if len(rows) != 1 {
		t.Errorf("repository holds %d assets (%d bytes each) against a %d-byte quota, want 1", len(rows), len(body), quota)
	}
	// The rejected upload's bytes must not linger in the store.
	for i, err := range errs {
		if errors.Is(err, base.ErrQuotaExceeded) {
			assert.False(t, blobStore.Has(base.BlobKey("cqrepo", paths[i])),
				"the rejected upload's blob was left behind")
		}
	}
}

// RegisterStoredBlob is the narrow waist every write path funnels through
// (StoreArtifact, the proxy cache fill, the OCI mount), so the quota has to
// hold there — a caller that skipped its own pre-check must still be stopped.
func TestRegisterStoredBlob_EnforcesBlobStoreQuota(t *testing.T) {
	repo := testutil.SimpleRepo("bsqrepo", "raw")
	quota := int64(10)
	d, _, assets, blobs := quotaAtomicDeps(repo, &domain.BlobStore{
		ID: "00000000-0000-0000-0000-000000000001", Name: "default", Type: "local",
		QuotaBytes: &quota, UsedBytes: 8,
	})
	ctx := context.Background()

	_, err := base.RegisterStoredBlob(ctx, d, repo, "/big.bin", "application/octet-stream",
		base.Coords{Name: "big.bin"}, base.BlobKey("bsqrepo", "/big.bin"),
		"aa", "bb", "cc", 5, "", "")
	if !errors.Is(err, base.ErrQuotaExceeded) {
		t.Fatalf("err = %v, want ErrQuotaExceeded — registration must not push used_bytes past the quota", err)
	}
	if _, gerr := assets.GetByPath(ctx, "bsqrepo", "/big.bin"); gerr == nil {
		t.Error("the over-quota asset row was registered anyway")
	}
	bs, _ := blobs.Get(ctx, "default")
	if bs.UsedBytes != 8 {
		t.Errorf("used_bytes = %d, want unchanged 8", bs.UsedBytes)
	}
}

func TestRegisterStoredBlob_EnforcesRepoQuota(t *testing.T) {
	repo := testutil.SimpleRepo("rqrepo", "raw")
	quota := int64(10)
	repo.QuotaBytes = &quota
	d, _, assets, _ := quotaAtomicDeps(repo, &domain.BlobStore{
		ID: "00000000-0000-0000-0000-000000000001", Name: "default", Type: "local",
	})
	ctx := context.Background()

	// 8 of the 10 bytes are already used.
	require.NoError(t, assets.Create(ctx, &domain.Asset{
		Repository: "rqrepo", RepositoryID: repo.ID, Path: "/old.bin",
		BlobKey: "k-old", SizeBytes: 8,
	}))

	_, err := base.RegisterStoredBlob(ctx, d, repo, "/new.bin", "application/octet-stream",
		base.Coords{Name: "new.bin"}, base.BlobKey("rqrepo", "/new.bin"),
		"aa", "bb", "cc", 5, "", "")
	if !errors.Is(err, base.ErrQuotaExceeded) {
		t.Fatalf("err = %v, want ErrQuotaExceeded", err)
	}
	if _, gerr := assets.GetByPath(ctx, "rqrepo", "/new.bin"); gerr == nil {
		t.Error("the over-quota asset row was registered anyway")
	}
}

// A registration that adds no bytes — an OCI digest alias, a cross-repo mount
// sharing an already-stored blob — must pass even at full quota: the store
// gains nothing, and rejecting it would break mounts exactly when the store
// is fullest.
func TestRegisterStoredBlob_SharedBlobKeyAtFullQuota_StillRegisters(t *testing.T) {
	repo := testutil.SimpleRepo("fullrepo", "raw")
	quota := int64(10)
	d, _, assets, _ := quotaAtomicDeps(repo, &domain.BlobStore{
		ID: "00000000-0000-0000-0000-000000000001", Name: "default", Type: "local",
		QuotaBytes: &quota, UsedBytes: 10,
	})
	ctx := context.Background()

	require.NoError(t, assets.Create(ctx, &domain.Asset{
		Repository: "fullrepo", RepositoryID: repo.ID, Path: "/orig.bin",
		BlobKey: "shared-key", SizeBytes: 10,
	}))

	_, err := base.RegisterStoredBlob(ctx, d, repo, "/alias.bin", "application/octet-stream",
		base.Coords{Name: "alias.bin"}, "shared-key",
		"aa", "bb", "cc", 10, "", "")
	if err != nil {
		t.Fatalf("aliasing an already-stored blob at full quota must succeed (it adds no bytes), got: %v", err)
	}
}
