//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

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
		UserRealms:   []string{"default", "LDAP"},
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
	if len(got.UserRealms) != 2 || got.UserRealms[0] != "default" || got.UserRealms[1] != "LDAP" {
		t.Fatalf("UserRealms did not round-trip: %+v", got.UserRealms)
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

func TestMigrationRepo_RunnerLifecycle(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "migration_jobs")
	ctx := context.Background()
	repo := NewMigrationRepo(pool)

	job := &domain.MigrationJob{
		SourceURL:           "https://nexus.example.com",
		SourceUser:          "admin",
		SourcePassword:      "sealed-blob",
		MigrateRepos:        true,
		MigrateBlobs:        true,
		MigratePrivileges:   true,
		MigrateRoles:        true,
		MigrateRoutingRules: false,
	}
	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SourcePassword != "sealed-blob" {
		t.Fatalf("source password not round-tripped: %q", got.SourcePassword)
	}
	if !got.MigratePrivileges || !got.MigrateRoles || got.MigrateRoutingRules {
		t.Fatalf("scope flags mismatch: %+v", got)
	}
	if got.Status != domain.MigrationPending {
		t.Fatalf("new job should be pending, got %s", got.Status)
	}

	// A pending job is one the runner must re-attach to after a restart.
	active, err := repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 1 || active[0].ID != job.ID {
		t.Fatalf("ListActive mismatch: %+v", active)
	}

	started := time.Now().UTC().Truncate(time.Millisecond)
	if err := repo.SetStarted(ctx, job.ID, started); err != nil {
		t.Fatalf("SetStarted: %v", err)
	}
	got, _ = repo.Get(ctx, job.ID)
	if got.Status != domain.MigrationRunning || got.StartedAt == nil {
		t.Fatalf("SetStarted did not take: %+v", got)
	}
	first := *got.StartedAt

	// Resuming must not rewrite when the transfer originally began.
	if err := repo.SetStarted(ctx, job.ID, started.Add(time.Hour)); err != nil {
		t.Fatalf("SetStarted (resume): %v", err)
	}
	got, _ = repo.Get(ctx, job.ID)
	if !got.StartedAt.Equal(first) {
		t.Fatalf("resume moved started_at from %v to %v", first, *got.StartedAt)
	}

	if err := repo.SetTotals(ctx, job.ID, 4, 120); err != nil {
		t.Fatalf("SetTotals: %v", err)
	}
	msg := "asset raw-hosted/a.txt: 404"
	if err := repo.UpdateProgress(ctx, job.ID, 2, 55, 1, &msg); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	got, _ = repo.Get(ctx, job.ID)
	if got.TotalRepos != 4 || got.TotalAssets != 120 || got.DoneRepos != 2 || got.DoneAssets != 55 {
		t.Fatalf("progress mismatch: %+v", got)
	}
	if got.ErrorCount != 1 || got.LastError == nil || *got.LastError != msg {
		t.Fatalf("error tally mismatch: %+v", got)
	}

	// A later progress write with no new message keeps the one already recorded.
	if err := repo.UpdateProgress(ctx, job.ID, 3, 90, 1, nil); err != nil {
		t.Fatalf("UpdateProgress (no message): %v", err)
	}
	got, _ = repo.Get(ctx, job.ID)
	if got.LastError == nil || *got.LastError != msg {
		t.Fatalf("last_error was cleared by a progress write: %+v", got.LastError)
	}

	if err := repo.FinishJob(ctx, job.ID, domain.MigrationDone, nil); err != nil {
		t.Fatalf("FinishJob: %v", err)
	}
	got, _ = repo.Get(ctx, job.ID)
	if got.Status != domain.MigrationDone || got.FinishedAt == nil {
		t.Fatalf("FinishJob did not take: %+v", got)
	}

	// A finished job is no longer the runner's to pick up.
	active, _ = repo.ListActive(ctx)
	if len(active) != 0 {
		t.Fatalf("finished job still listed as active: %+v", active)
	}
}

func TestMigrationRepo_SetSourcePassword(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "migration_jobs")
	ctx := context.Background()
	repo := NewMigrationRepo(pool)

	job := &domain.MigrationJob{SourceURL: "https://nexus.example.com"}
	if err := repo.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.SetSourcePassword(ctx, job.ID, "sealed-v2"); err != nil {
		t.Fatalf("SetSourcePassword: %v", err)
	}
	got, _ := repo.Get(ctx, job.ID)
	if got.SourcePassword != "sealed-v2" {
		t.Fatalf("password not updated: %q", got.SourcePassword)
	}
}

func TestMigrationRepo_RunnerUpdates_NotFound(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "migration_jobs")
	ctx := context.Background()
	repo := NewMigrationRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	if err := repo.SetStarted(ctx, missing, time.Now()); err == nil {
		t.Fatal("SetStarted(missing) should error")
	}
	if err := repo.SetTotals(ctx, missing, 1, 1); err == nil {
		t.Fatal("SetTotals(missing) should error")
	}
	if err := repo.UpdateProgress(ctx, missing, 1, 1, 0, nil); err == nil {
		t.Fatal("UpdateProgress(missing) should error")
	}
	if err := repo.FinishJob(ctx, missing, domain.MigrationDone, nil); err == nil {
		t.Fatal("FinishJob(missing) should error")
	}
	if err := repo.SetSourcePassword(ctx, missing, "x"); err == nil {
		t.Fatal("SetSourcePassword(missing) should error")
	}
}
