//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// makeBSMParents creates source+target blob stores and returns their IDs.
// Each store needs a unique name because makeLocalBS derives its config path
// from the name; both satisfy the source/target FKs on blob_store_migrations.
func makeBSMParents(t *testing.T, ctx context.Context, bsRepo *blobStoreRepo, prefix string) (sourceID, targetID string) {
	t.Helper()
	src := makeLocalBS(prefix + "_src")
	if err := bsRepo.Create(ctx, src); err != nil {
		t.Fatalf("create source blob store: %v", err)
	}
	dst := makeLocalBS(prefix + "_dst")
	if err := bsRepo.Create(ctx, dst); err != nil {
		t.Fatalf("create target blob store: %v", err)
	}
	return src.ID, dst.ID
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestBlobStoreMigrationRepo_Create_PopulatesIDAndTimestamps(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	bsRepo := NewBlobStoreRepo(pool)
	repo := NewBlobStoreMigrationRepo(pool)

	srcID, dstID := makeBSMParents(t, ctx, bsRepo, "create_ts")

	m := &domain.BlobStoreMigration{
		RepositoryName: "create_ts_repo",
		SourceStoreID:  srcID,
		TargetStoreID:  dstID,
		Status:         "pending",
	}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if m.ID == "" {
		t.Error("Create did not populate ID")
	}
	if m.CreatedAt.IsZero() {
		t.Error("Create did not populate CreatedAt")
	}
	if m.UpdatedAt.IsZero() {
		t.Error("Create did not populate UpdatedAt")
	}
}

func TestBlobStoreMigrationRepo_Create_EmptySourceStoresNull(t *testing.T) {
	// SourceStoreID is optional (nullable FK). An empty string must round-trip
	// to NULL and back to "" via the COALESCE in the SELECT.
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	bsRepo := NewBlobStoreRepo(pool)
	repo := NewBlobStoreMigrationRepo(pool)

	dst := makeLocalBS("nullsrc_dst")
	if err := bsRepo.Create(ctx, dst); err != nil {
		t.Fatalf("create target blob store: %v", err)
	}

	m := &domain.BlobStoreMigration{
		RepositoryName: "nullsrc_repo",
		SourceStoreID:  "", // → NULL
		TargetStoreID:  dst.ID,
		Status:         "pending",
	}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.SourceStoreID != "" {
		t.Errorf("SourceStoreID: got %q, want empty string (NULL)", got.SourceStoreID)
	}
}

// ── Get ────────────────────────────────────────────────────────────────────────

func TestBlobStoreMigrationRepo_Get_FieldsRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	bsRepo := NewBlobStoreRepo(pool)
	repo := NewBlobStoreMigrationRepo(pool)

	srcID, dstID := makeBSMParents(t, ctx, bsRepo, "roundtrip")

	m := &domain.BlobStoreMigration{
		RepositoryName: "roundtrip_repo",
		SourceStoreID:  srcID,
		TargetStoreID:  dstID,
		Status:         "pending",
	}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil for existing migration")
	}
	if got.ID != m.ID {
		t.Errorf("ID: got %q, want %q", got.ID, m.ID)
	}
	if got.RepositoryName != "roundtrip_repo" {
		t.Errorf("RepositoryName: got %q, want roundtrip_repo", got.RepositoryName)
	}
	if got.SourceStoreID != srcID {
		t.Errorf("SourceStoreID: got %q, want %q", got.SourceStoreID, srcID)
	}
	if got.TargetStoreID != dstID {
		t.Errorf("TargetStoreID: got %q, want %q", got.TargetStoreID, dstID)
	}
	if got.Status != "pending" {
		t.Errorf("Status: got %q, want pending", got.Status)
	}
	// Defaults from the migration DDL.
	if got.TotalAssets != 0 || got.DoneAssets != 0 {
		t.Errorf("asset counters: got total=%d done=%d, want 0/0", got.TotalAssets, got.DoneAssets)
	}
	if got.TotalBytes != 0 || got.DoneBytes != 0 {
		t.Errorf("byte counters: got total=%d done=%d, want 0/0", got.TotalBytes, got.DoneBytes)
	}
	if got.ErrorMessage != nil {
		t.Errorf("ErrorMessage: got %v, want nil", *got.ErrorMessage)
	}
	if got.StartedAt != nil {
		t.Errorf("StartedAt: got %v, want nil", got.StartedAt)
	}
	if got.FinishedAt != nil {
		t.Errorf("FinishedAt: got %v, want nil", got.FinishedAt)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt: got zero, want non-zero")
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt: got zero, want non-zero")
	}
}

func TestBlobStoreMigrationRepo_Get_NotFound_ReturnsNilNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreMigrationRepo(pool)

	got, err := repo.Get(ctx, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get(missing): want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Errorf("Get(missing): got %+v, want nil", got)
	}
}

// ── SetTotals ───────────────────────────────────────────────────────────────────

func TestBlobStoreMigrationRepo_SetTotals(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	bsRepo := NewBlobStoreRepo(pool)
	repo := NewBlobStoreMigrationRepo(pool)

	srcID, dstID := makeBSMParents(t, ctx, bsRepo, "settotals")
	m := &domain.BlobStoreMigration{
		RepositoryName: "settotals_repo",
		SourceStoreID:  srcID,
		TargetStoreID:  dstID,
		Status:         "pending",
	}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.SetTotals(ctx, m.ID, 42, 987654321); err != nil {
		t.Fatalf("SetTotals: %v", err)
	}

	got, err := repo.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TotalAssets != 42 {
		t.Errorf("TotalAssets: got %d, want 42", got.TotalAssets)
	}
	if got.TotalBytes != 987654321 {
		t.Errorf("TotalBytes: got %d, want 987654321", got.TotalBytes)
	}
}

// ── UpdateProgress ──────────────────────────────────────────────────────────────

func TestBlobStoreMigrationRepo_UpdateProgress(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	bsRepo := NewBlobStoreRepo(pool)
	repo := NewBlobStoreMigrationRepo(pool)

	srcID, dstID := makeBSMParents(t, ctx, bsRepo, "progress")
	m := &domain.BlobStoreMigration{
		RepositoryName: "progress_repo",
		SourceStoreID:  srcID,
		TargetStoreID:  dstID,
		Status:         "running",
	}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.UpdateProgress(ctx, m.ID, 7, 12345); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}

	got, err := repo.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DoneAssets != 7 {
		t.Errorf("DoneAssets: got %d, want 7", got.DoneAssets)
	}
	if got.DoneBytes != 12345 {
		t.Errorf("DoneBytes: got %d, want 12345", got.DoneBytes)
	}

	// A second progress update overwrites (not accumulates).
	if err := repo.UpdateProgress(ctx, m.ID, 15, 99999); err != nil {
		t.Fatalf("UpdateProgress #2: %v", err)
	}
	got2, err := repo.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get #2: %v", err)
	}
	if got2.DoneAssets != 15 || got2.DoneBytes != 99999 {
		t.Errorf("progress not overwritten: got assets=%d bytes=%d, want 15/99999", got2.DoneAssets, got2.DoneBytes)
	}
}

// ── UpdateStatus ────────────────────────────────────────────────────────────────

func TestBlobStoreMigrationRepo_UpdateStatus_RunningSetsStartedAt(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	bsRepo := NewBlobStoreRepo(pool)
	repo := NewBlobStoreMigrationRepo(pool)

	srcID, dstID := makeBSMParents(t, ctx, bsRepo, "status_run")
	m := &domain.BlobStoreMigration{
		RepositoryName: "status_run_repo",
		SourceStoreID:  srcID,
		TargetStoreID:  dstID,
		Status:         "pending",
	}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.UpdateStatus(ctx, m.ID, "running", nil); err != nil {
		t.Fatalf("UpdateStatus(running): %v", err)
	}

	got, err := repo.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "running" {
		t.Errorf("Status: got %q, want running", got.Status)
	}
	if got.StartedAt == nil {
		t.Error("StartedAt: got nil, want set when transitioning to running")
	}
	firstStart := got.StartedAt

	// Transitioning to another status must NOT overwrite started_at (COALESCE).
	if err := repo.UpdateStatus(ctx, m.ID, "cancelled", nil); err != nil {
		t.Fatalf("UpdateStatus(cancelled): %v", err)
	}
	got2, err := repo.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get #2: %v", err)
	}
	if got2.Status != "cancelled" {
		t.Errorf("Status: got %q, want cancelled", got2.Status)
	}
	if got2.StartedAt == nil {
		t.Fatal("StartedAt cleared on later status change, want preserved")
	}
	if !got2.StartedAt.Equal(*firstStart) {
		t.Errorf("StartedAt overwritten: got %v, want preserved %v", got2.StartedAt, firstStart)
	}
}

func TestBlobStoreMigrationRepo_UpdateStatus_NonRunningLeavesStartedAtNull(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	bsRepo := NewBlobStoreRepo(pool)
	repo := NewBlobStoreMigrationRepo(pool)

	srcID, dstID := makeBSMParents(t, ctx, bsRepo, "status_nostart")
	m := &domain.BlobStoreMigration{
		RepositoryName: "status_nostart_repo",
		SourceStoreID:  srcID,
		TargetStoreID:  dstID,
		Status:         "pending",
	}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Move straight to failed without ever being running.
	if err := repo.UpdateStatus(ctx, m.ID, "failed", strPtr("boom")); err != nil {
		t.Fatalf("UpdateStatus(failed): %v", err)
	}

	got, err := repo.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("Status: got %q, want failed", got.Status)
	}
	if got.StartedAt != nil {
		t.Errorf("StartedAt: got %v, want nil (never ran)", got.StartedAt)
	}
	if got.ErrorMessage == nil || *got.ErrorMessage != "boom" {
		t.Errorf("ErrorMessage: got %v, want boom", got.ErrorMessage)
	}
}

// ── FinishMigration ─────────────────────────────────────────────────────────────

func TestBlobStoreMigrationRepo_FinishMigration_Done(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	bsRepo := NewBlobStoreRepo(pool)
	repo := NewBlobStoreMigrationRepo(pool)

	srcID, dstID := makeBSMParents(t, ctx, bsRepo, "finish_done")
	m := &domain.BlobStoreMigration{
		RepositoryName: "finish_done_repo",
		SourceStoreID:  srcID,
		TargetStoreID:  dstID,
		Status:         "running",
	}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.FinishMigration(ctx, m.ID, "done", nil); err != nil {
		t.Fatalf("FinishMigration: %v", err)
	}

	got, err := repo.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "done" {
		t.Errorf("Status: got %q, want done", got.Status)
	}
	if got.FinishedAt == nil {
		t.Error("FinishedAt: got nil, want set after FinishMigration")
	}
	if got.ErrorMessage != nil {
		t.Errorf("ErrorMessage: got %v, want nil for a clean finish", *got.ErrorMessage)
	}
}

func TestBlobStoreMigrationRepo_FinishMigration_FailedWithError(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	bsRepo := NewBlobStoreRepo(pool)
	repo := NewBlobStoreMigrationRepo(pool)

	srcID, dstID := makeBSMParents(t, ctx, bsRepo, "finish_fail")
	m := &domain.BlobStoreMigration{
		RepositoryName: "finish_fail_repo",
		SourceStoreID:  srcID,
		TargetStoreID:  dstID,
		Status:         "running",
	}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.FinishMigration(ctx, m.ID, "failed", strPtr("disk full")); err != nil {
		t.Fatalf("FinishMigration: %v", err)
	}

	got, err := repo.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("Status: got %q, want failed", got.Status)
	}
	if got.FinishedAt == nil {
		t.Error("FinishedAt: got nil, want set")
	}
	if got.ErrorMessage == nil || *got.ErrorMessage != "disk full" {
		t.Errorf("ErrorMessage: got %v, want 'disk full'", got.ErrorMessage)
	}
}

// ── GetActiveByRepo ─────────────────────────────────────────────────────────────

func TestBlobStoreMigrationRepo_GetActiveByRepo_ReturnsPendingOrRunning(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	bsRepo := NewBlobStoreRepo(pool)
	repo := NewBlobStoreMigrationRepo(pool)

	srcID, dstID := makeBSMParents(t, ctx, bsRepo, "active")
	repoName := "active_repo"

	// An old finished migration must be ignored.
	old := &domain.BlobStoreMigration{
		RepositoryName: repoName, SourceStoreID: srcID, TargetStoreID: dstID, Status: "running",
	}
	if err := repo.Create(ctx, old); err != nil {
		t.Fatalf("Create old: %v", err)
	}
	if err := repo.FinishMigration(ctx, old.ID, "done", nil); err != nil {
		t.Fatalf("Finish old: %v", err)
	}

	// A current running migration.
	cur := &domain.BlobStoreMigration{
		RepositoryName: repoName, SourceStoreID: srcID, TargetStoreID: dstID, Status: "running",
	}
	if err := repo.Create(ctx, cur); err != nil {
		t.Fatalf("Create current: %v", err)
	}

	got, err := repo.GetActiveByRepo(ctx, repoName)
	if err != nil {
		t.Fatalf("GetActiveByRepo: %v", err)
	}
	if got == nil {
		t.Fatal("GetActiveByRepo returned nil, want active migration")
	}
	if got.ID != cur.ID {
		t.Errorf("GetActiveByRepo returned %q, want active %q", got.ID, cur.ID)
	}
}

func TestBlobStoreMigrationRepo_GetActiveByRepo_NoneActiveReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	bsRepo := NewBlobStoreRepo(pool)
	repo := NewBlobStoreMigrationRepo(pool)

	srcID, dstID := makeBSMParents(t, ctx, bsRepo, "noactive")
	repoName := "noactive_repo"

	m := &domain.BlobStoreMigration{
		RepositoryName: repoName, SourceStoreID: srcID, TargetStoreID: dstID, Status: "running",
	}
	if err := repo.Create(ctx, m); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.FinishMigration(ctx, m.ID, "done", nil); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	got, err := repo.GetActiveByRepo(ctx, repoName)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetActiveByRepo: want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Errorf("GetActiveByRepo: got %+v, want nil (all finished)", got)
	}
}

func TestBlobStoreMigrationRepo_GetActiveByRepo_UnknownRepoReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreMigrationRepo(pool)

	got, err := repo.GetActiveByRepo(ctx, "does-not-exist")
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetActiveByRepo: want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Errorf("GetActiveByRepo(unknown): got %+v, want nil", got)
	}
}

// ── GetLatestByRepo ─────────────────────────────────────────────────────────────

func TestBlobStoreMigrationRepo_GetLatestByRepo_ReturnsMostRecentRegardlessOfStatus(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	bsRepo := NewBlobStoreRepo(pool)
	repo := NewBlobStoreMigrationRepo(pool)

	srcID, dstID := makeBSMParents(t, ctx, bsRepo, "latest")
	repoName := "latest_repo"

	first := &domain.BlobStoreMigration{
		RepositoryName: repoName, SourceStoreID: srcID, TargetStoreID: dstID, Status: "running",
	}
	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if err := repo.FinishMigration(ctx, first.ID, "done", nil); err != nil {
		t.Fatalf("Finish first: %v", err)
	}

	// Force the second row to sort strictly after the first via created_at.
	second := &domain.BlobStoreMigration{
		RepositoryName: repoName, SourceStoreID: srcID, TargetStoreID: dstID, Status: "failed",
	}
	if err := repo.Create(ctx, second); err != nil {
		t.Fatalf("Create second: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE blob_store_migrations SET created_at = created_at + interval '1 hour' WHERE id = $1`,
		second.ID); err != nil {
		t.Fatalf("bump created_at: %v", err)
	}

	got, err := repo.GetLatestByRepo(ctx, repoName)
	if err != nil {
		t.Fatalf("GetLatestByRepo: %v", err)
	}
	if got == nil {
		t.Fatal("GetLatestByRepo returned nil")
	}
	if got.ID != second.ID {
		t.Errorf("GetLatestByRepo returned %q, want most-recent %q", got.ID, second.ID)
	}
	if got.Status != "failed" {
		t.Errorf("Status: got %q, want failed (latest regardless of status)", got.Status)
	}
}

func TestBlobStoreMigrationRepo_GetLatestByRepo_UnknownRepoReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreMigrationRepo(pool)

	got, err := repo.GetLatestByRepo(ctx, "no-such-repo")
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetLatestByRepo: want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Errorf("GetLatestByRepo(unknown): got %+v, want nil", got)
	}
}

// ── ListActive ──────────────────────────────────────────────────────────────────

func TestBlobStoreMigrationRepo_ListActive_OnlyPendingAndRunning(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	bsRepo := NewBlobStoreRepo(pool)
	repo := NewBlobStoreMigrationRepo(pool)

	srcID, dstID := makeBSMParents(t, ctx, bsRepo, "listactive")

	mk := func(repoName, status string) *domain.BlobStoreMigration {
		m := &domain.BlobStoreMigration{
			RepositoryName: repoName, SourceStoreID: srcID, TargetStoreID: dstID, Status: "pending",
		}
		if err := repo.Create(ctx, m); err != nil {
			t.Fatalf("Create %s: %v", repoName, err)
		}
		if status != "pending" {
			if err := repo.UpdateStatus(ctx, m.ID, status, nil); err != nil {
				t.Fatalf("UpdateStatus %s: %v", repoName, err)
			}
		}
		return m
	}

	pending := mk("la_pending", "pending")
	running := mk("la_running", "running")
	mk("la_done", "done")
	mk("la_failed", "failed")
	mk("la_cancelled", "cancelled")

	active, err := repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("ListActive: got %d rows, want 2 (pending+running)", len(active))
	}
	ids := map[string]bool{active[0].ID: true, active[1].ID: true}
	if !ids[pending.ID] {
		t.Errorf("ListActive missing pending migration %q", pending.ID)
	}
	if !ids[running.ID] {
		t.Errorf("ListActive missing running migration %q", running.ID)
	}
	for _, m := range active {
		if m.Status != "pending" && m.Status != "running" {
			t.Errorf("ListActive returned non-active status %q", m.Status)
		}
	}
}

func TestBlobStoreMigrationRepo_ListActive_EmptyReturnsNoRows(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_store_migrations", "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreMigrationRepo(pool)

	active, err := repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive on empty: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("ListActive on empty: got %d rows, want 0", len(active))
	}
}
