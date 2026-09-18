//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

func TestBackupSettingsRepo_Get_ReturnsDefaultsWhenNeverWritten(t *testing.T) {
	pool := pgtest.Pool(t)
	repo := NewBackupSettingsRepo(pool)
	ctx := context.Background()

	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Enabled {
		t.Errorf("Enabled = true, want false (never configured)")
	}
	if got.ScheduleCron != "0 3 * * *" {
		t.Errorf("ScheduleCron = %q, want the column default", got.ScheduleCron)
	}
	if got.RetentionCount != 7 {
		t.Errorf("RetentionCount = %d, want the column default 7", got.RetentionCount)
	}
	if got.BlobStoreID != "" {
		t.Errorf("BlobStoreID = %q, want empty", got.BlobStoreID)
	}
}

func TestBackupSettingsRepo_Upsert_RoundTrips(t *testing.T) {
	pool := pgtest.Pool(t)
	repo := NewBackupSettingsRepo(pool)
	bsRepo := NewBlobStoreRepo(pool)
	ctx := context.Background()

	bs := &domain.BlobStore{Name: "backup-dest", Type: "s3", Config: map[string]any{"bucket": "backups"}}
	if err := bsRepo.Create(ctx, bs); err != nil {
		t.Fatalf("create blob store: %v", err)
	}

	if err := repo.Upsert(ctx, &domain.BackupSettings{
		Enabled: true, ScheduleCron: "0 4 * * *", BlobStoreID: bs.ID, RetentionCount: 3,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Enabled || got.ScheduleCron != "0 4 * * *" || got.BlobStoreID != bs.ID || got.RetentionCount != 3 {
		t.Errorf("Get after Upsert = %+v, fields did not round-trip", got)
	}

	// A second Upsert (the settings-form "save" path) must replace, not add a row.
	if err := repo.Upsert(ctx, &domain.BackupSettings{
		Enabled: false, ScheduleCron: "0 5 * * *", RetentionCount: 10,
	}); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	got2, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get after second Upsert: %v", err)
	}
	if got2.Enabled || got2.ScheduleCron != "0 5 * * *" || got2.BlobStoreID != "" || got2.RetentionCount != 10 {
		t.Errorf("Get after second Upsert = %+v, want the replaced values (BlobStoreID cleared)", got2)
	}
}

func TestBackupSettingsRepo_Upsert_NeverTouchesLastRunFields(t *testing.T) {
	pool := pgtest.Pool(t)
	repo := NewBackupSettingsRepo(pool)
	ctx := context.Background()

	at := time.Now().Truncate(time.Second)
	if err := repo.RecordRun(ctx, at, "backups/x.tar.gz", ""); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	if err := repo.Upsert(ctx, &domain.BackupSettings{Enabled: true, ScheduleCron: "0 3 * * *", RetentionCount: 5}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastRunAt == nil || !got.LastRunAt.Equal(at) {
		t.Errorf("LastRunAt = %v, want %v preserved across Upsert", got.LastRunAt, at)
	}
	if got.LastRunKey != "backups/x.tar.gz" {
		t.Errorf("LastRunKey = %q, want it preserved across Upsert", got.LastRunKey)
	}
}

func TestBackupSettingsRepo_RecordRun_ReportsErrorAndSucceedsWithoutPriorUpsert(t *testing.T) {
	pool := pgtest.Pool(t)
	repo := NewBackupSettingsRepo(pool)
	ctx := context.Background()

	at := time.Now().Truncate(time.Second)
	if err := repo.RecordRun(ctx, at, "", "export: list repos: connection refused"); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastRunKey != "" {
		t.Errorf("LastRunKey = %q, want empty for a failed run", got.LastRunKey)
	}
	if got.LastRunError == "" {
		t.Errorf("LastRunError = empty, want the failure message")
	}
}
