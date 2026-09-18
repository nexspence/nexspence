package service

import (
	"context"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// White-box (package service) so runOnce — the actual cron-triggered path —
// can be exercised directly instead of waiting on a real schedule.

func newInternalBackupSvc(repos ...*domain.Repository) (*BackupService, *testutil.BackupSettingsRepo, *testutil.AuditRepo) {
	settings := testutil.NewBackupSettingsRepo()
	audit := testutil.NewAuditRepo()
	svc := &BackupService{
		BlobStores: testutil.NewBlobStoreRepo(),
		Repos:      testutil.NewRepoRepo(repos...),
		Users:      testutil.NewUserRepo(),
		Roles:      testutil.NewRoleRepo(),
		Policies:   testutil.NewCleanupPolicyRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
	}
	svc.WithSettings(settings).WithAudit(audit)
	return svc, settings, audit
}

func TestBackupService_RunOnce_Success_WritesAuditEvent(t *testing.T) {
	ctx := context.Background()
	svc, settings, audit := newInternalBackupSvc(testutil.SimpleRepo("r1", "raw"))

	dest := testutil.NewBlobStore()
	svc.Resolver = testutil.NewFakeResolver(dest)
	bs := &domain.BlobStore{ID: "bs-1", Name: "dest", Type: "s3"}
	if err := svc.BlobStores.Create(ctx, bs); err != nil {
		t.Fatalf("create blob store: %v", err)
	}
	if err := settings.Upsert(ctx, &domain.BackupSettings{Enabled: true, ScheduleCron: "0 3 * * *", BlobStoreID: bs.ID, RetentionCount: 7}); err != nil {
		t.Fatalf("upsert settings: %v", err)
	}

	svc.runOnce(ctx)

	events := audit.Snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Result != "success" {
		t.Errorf("Result = %q, want success", e.Result)
	}
	if e.Domain != "SYSTEM" || e.Action != "BACKUP" {
		t.Errorf("Domain/Action = %q/%q, want SYSTEM/BACKUP", e.Domain, e.Action)
	}
	if e.EntityName == "" {
		t.Error("EntityName (the backup key) must not be empty on success")
	}
}

func TestBackupService_RunOnce_Failure_WritesAuditEventWithError(t *testing.T) {
	ctx := context.Background()
	svc, settings, audit := newInternalBackupSvc(testutil.SimpleRepo("r1", "raw"))
	svc.Resolver = failingResolverForInternalTest{}

	bs := &domain.BlobStore{ID: "bs-broken", Name: "broken", Type: "s3"}
	if err := svc.BlobStores.Create(ctx, bs); err != nil {
		t.Fatalf("create blob store: %v", err)
	}
	if err := settings.Upsert(ctx, &domain.BackupSettings{Enabled: true, ScheduleCron: "0 3 * * *", BlobStoreID: bs.ID, RetentionCount: 7}); err != nil {
		t.Fatalf("upsert settings: %v", err)
	}

	svc.runOnce(ctx)

	events := audit.Snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Result != "failure" {
		t.Errorf("Result = %q, want failure", e.Result)
	}
	if e.Context["error"] == nil || e.Context["error"] == "" {
		t.Error("Context[\"error\"] must carry the failure reason")
	}
}

func TestBackupService_RunOnce_NoopDoesNotWriteAuditEvent(t *testing.T) {
	ctx := context.Background()
	svc, _, audit := newInternalBackupSvc()

	svc.runOnce(ctx) // disabled by default (Settings.Get returns Enabled=false)

	if got := len(audit.Snapshot()); got != 0 {
		t.Errorf("audit events = %d, want 0 for a disabled/no-destination no-op", got)
	}
}

type failingResolverForInternalTest struct{}

func (failingResolverForInternalTest) Get(context.Context, storage.BlobStoreDescriptor) (storage.BlobStore, error) {
	return nil, errors.New("resolver: simulated failure")
}
