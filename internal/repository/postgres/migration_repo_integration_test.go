//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

func TestMigrationRepo_CRUD(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "migration_jobs")
	ctx := context.Background()
	repo := NewMigrationRepo(pool)

	job := &domain.MigrationJob{
		SourceURL:    "https://nexus.example.com",
		SourceUser:   "admin",
		MigrateRepos: true,
		MigrateUsers: true,
	}
	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if job.ID == "" {
		t.Fatal("Create did not populate ID")
	}
	if job.CreatedAt.IsZero() || job.UpdatedAt.IsZero() {
		t.Fatal("Create did not populate timestamps")
	}

	got, err := repo.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SourceURL != job.SourceURL || got.SourceUser != "admin" {
		t.Fatalf("Get mismatch: %+v", got)
	}
	if !got.MigrateRepos || !got.MigrateUsers || got.MigrateBlobs || got.MigratePolicies {
		t.Fatalf("Get bool flags mismatch: %+v", got)
	}

	if err := repo.UpdateStatus(ctx, job.ID, domain.MigrationRunning); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ = repo.Get(ctx, job.ID)
	if got.Status != domain.MigrationRunning {
		t.Fatalf("status not updated: %s", got.Status)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != job.ID {
		t.Fatalf("List mismatch: %+v", list)
	}

	if err := repo.Delete(ctx, job.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, job.ID); err == nil {
		t.Fatal("Get after Delete should error (not found)")
	}
}

func TestMigrationRepo_NotFound(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "migration_jobs")
	ctx := context.Background()
	repo := NewMigrationRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	if _, err := repo.Get(ctx, missing); err == nil {
		t.Fatal("Get(missing) should error")
	}
	if err := repo.UpdateStatus(ctx, missing, domain.MigrationDone); err == nil {
		t.Fatal("UpdateStatus(missing) should error")
	}
	if err := repo.Delete(ctx, missing); err == nil {
		t.Fatal("Delete(missing) should error")
	}
}

func TestMigrationRepo_List_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "migration_jobs")
	repo := NewMigrationRepo(pool)
	list, err := repo.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty, got %d", len(list))
	}
}
