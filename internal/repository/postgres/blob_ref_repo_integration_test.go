//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── Increment ─────────────────────────────────────────────────────────────────

func TestBlobRefRepo_Increment_NewKey_RefCountOne(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "global_blobs")
	ctx := context.Background()
	repo := NewBlobRefRepo(pool)

	const key = "sha256:aabbcc_new"
	if err := repo.Increment(ctx, key, 4096); err != nil {
		t.Fatalf("Increment: %v", err)
	}

	count, err := repo.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if count != 1 {
		t.Errorf("ref_count after first Increment: got %d, want 1", count)
	}
}

func TestBlobRefRepo_Increment_ExistingKey_IncrementsCount(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "global_blobs")
	ctx := context.Background()
	repo := NewBlobRefRepo(pool)

	const key = "sha256:aabbcc_existing"
	// Insert twice.
	for i := 0; i < 2; i++ {
		if err := repo.Increment(ctx, key, 8192); err != nil {
			t.Fatalf("Increment #%d: %v", i+1, err)
		}
	}

	count, err := repo.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if count != 2 {
		t.Errorf("ref_count after 2 increments: got %d, want 2", count)
	}
}

func TestBlobRefRepo_Increment_ThreeTimes_CountIsThree(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "global_blobs")
	ctx := context.Background()
	repo := NewBlobRefRepo(pool)

	const key = "sha256:aabbcc_three"
	for i := 0; i < 3; i++ {
		if err := repo.Increment(ctx, key, 1024); err != nil {
			t.Fatalf("Increment #%d: %v", i+1, err)
		}
	}

	count, err := repo.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if count != 3 {
		t.Errorf("ref_count after 3 increments: got %d, want 3", count)
	}
}

// ── Decrement ─────────────────────────────────────────────────────────────────

func TestBlobRefRepo_Decrement_CountReachesZero_DeletesRow_ReturnsTrue(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "global_blobs")
	ctx := context.Background()
	repo := NewBlobRefRepo(pool)

	const key = "sha256:dec_to_zero"
	if err := repo.Increment(ctx, key, 512); err != nil {
		t.Fatalf("Increment: %v", err)
	}

	shouldDelete, err := repo.Decrement(ctx, key)
	if err != nil {
		t.Fatalf("Decrement: %v", err)
	}
	if !shouldDelete {
		t.Errorf("Decrement to zero: got shouldDelete=false, want true")
	}

	// Row should be gone — Get returns 0.
	count, err := repo.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get after decrement-to-zero: %v", err)
	}
	if count != 0 {
		t.Errorf("ref_count after row deleted: got %d, want 0", count)
	}
}

func TestBlobRefRepo_Decrement_CountAboveOne_ReturnsFalse(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "global_blobs")
	ctx := context.Background()
	repo := NewBlobRefRepo(pool)

	const key = "sha256:dec_above_one"
	// Create with ref_count = 2.
	if err := repo.Increment(ctx, key, 1024); err != nil {
		t.Fatalf("Increment 1: %v", err)
	}
	if err := repo.Increment(ctx, key, 1024); err != nil {
		t.Fatalf("Increment 2: %v", err)
	}

	shouldDelete, err := repo.Decrement(ctx, key)
	if err != nil {
		t.Fatalf("Decrement: %v", err)
	}
	if shouldDelete {
		t.Errorf("Decrement 2→1: got shouldDelete=true, want false")
	}

	count, err := repo.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if count != 1 {
		t.Errorf("ref_count after 2→1 decrement: got %d, want 1", count)
	}
}

func TestBlobRefRepo_Decrement_SequenceFromThreeToZero(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "global_blobs")
	ctx := context.Background()
	repo := NewBlobRefRepo(pool)

	const key = "sha256:countdown"
	for i := 0; i < 3; i++ {
		if err := repo.Increment(ctx, key, 256); err != nil {
			t.Fatalf("Increment #%d: %v", i+1, err)
		}
	}

	// 3 → 2
	done, err := repo.Decrement(ctx, key)
	if err != nil {
		t.Fatalf("Decrement (3→2): %v", err)
	}
	if done {
		t.Error("Decrement 3→2: shouldDelete should be false")
	}
	if c, _ := repo.Get(ctx, key); c != 2 {
		t.Errorf("ref_count at 2: got %d", c)
	}

	// 2 → 1
	done, err = repo.Decrement(ctx, key)
	if err != nil {
		t.Fatalf("Decrement (2→1): %v", err)
	}
	if done {
		t.Error("Decrement 2→1: shouldDelete should be false")
	}
	if c, _ := repo.Get(ctx, key); c != 1 {
		t.Errorf("ref_count at 1: got %d", c)
	}

	// 1 → 0 (row deleted)
	done, err = repo.Decrement(ctx, key)
	if err != nil {
		t.Fatalf("Decrement (1→0): %v", err)
	}
	if !done {
		t.Error("Decrement 1→0: shouldDelete should be true")
	}
	if c, _ := repo.Get(ctx, key); c != 0 {
		t.Errorf("ref_count after deletion: got %d, want 0", c)
	}
}

func TestBlobRefRepo_Decrement_MissingKey_ReturnsTrueNoError(t *testing.T) {
	// A blob key that was never tracked (old path-based blob) — the code
	// treats ErrNoRows as "caller should delete it" → (true, nil).
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "global_blobs")
	ctx := context.Background()
	repo := NewBlobRefRepo(pool)

	shouldDelete, err := repo.Decrement(ctx, "sha256:never_tracked_key")
	if err != nil {
		t.Fatalf("Decrement(missing): unexpected error: %v", err)
	}
	if !shouldDelete {
		t.Errorf("Decrement(missing): got shouldDelete=false, want true (untracked → delete)")
	}
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestBlobRefRepo_Get_MissingKey_ReturnsZero(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "global_blobs")
	ctx := context.Background()
	repo := NewBlobRefRepo(pool)

	count, err := repo.Get(ctx, "sha256:does_not_exist")
	if err != nil {
		t.Fatalf("Get(missing): unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("Get(missing): got %d, want 0", count)
	}
}

func TestBlobRefRepo_Get_AfterMultipleIncrements_ReturnsCorrectCount(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "global_blobs")
	ctx := context.Background()
	repo := NewBlobRefRepo(pool)

	const key = "sha256:multi_ref"
	for i := 0; i < 5; i++ {
		if err := repo.Increment(ctx, key, 2048); err != nil {
			t.Fatalf("Increment #%d: %v", i+1, err)
		}
	}

	count, err := repo.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if count != 5 {
		t.Errorf("Get after 5 increments: got %d, want 5", count)
	}
}

func TestBlobRefRepo_Get_AfterRowDeleted_ReturnsZero(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "global_blobs")
	ctx := context.Background()
	repo := NewBlobRefRepo(pool)

	const key = "sha256:deleted_row_check"
	if err := repo.Increment(ctx, key, 100); err != nil {
		t.Fatalf("Increment: %v", err)
	}

	if _, err := repo.Decrement(ctx, key); err != nil {
		t.Fatalf("Decrement: %v", err)
	}

	// Row was deleted by Decrement-to-zero; Get should return 0.
	count, err := repo.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if count != 0 {
		t.Errorf("Get after row deleted: got %d, want 0", count)
	}
}

// ── Multiple keys isolation ───────────────────────────────────────────────────

func TestBlobRefRepo_MultipleKeys_Independent(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "global_blobs")
	ctx := context.Background()
	repo := NewBlobRefRepo(pool)

	const keyA = "sha256:keyA_iso"
	const keyB = "sha256:keyB_iso"

	// keyA gets 3 refs, keyB gets 1.
	for i := 0; i < 3; i++ {
		if err := repo.Increment(ctx, keyA, 512); err != nil {
			t.Fatalf("Increment keyA #%d: %v", i+1, err)
		}
	}
	if err := repo.Increment(ctx, keyB, 512); err != nil {
		t.Fatalf("Increment keyB: %v", err)
	}

	// Decrement keyB to zero — should not affect keyA.
	done, err := repo.Decrement(ctx, keyB)
	if err != nil {
		t.Fatalf("Decrement keyB: %v", err)
	}
	if !done {
		t.Errorf("Decrement keyB to zero: shouldDelete should be true")
	}

	countA, err := repo.Get(ctx, keyA)
	if err != nil {
		t.Fatalf("Get keyA: %v", err)
	}
	if countA != 3 {
		t.Errorf("keyA ref_count unaffected: got %d, want 3", countA)
	}

	countB, err := repo.Get(ctx, keyB)
	if err != nil {
		t.Fatalf("Get keyB: %v", err)
	}
	if countB != 0 {
		t.Errorf("keyB ref_count after deletion: got %d, want 0", countB)
	}
}
