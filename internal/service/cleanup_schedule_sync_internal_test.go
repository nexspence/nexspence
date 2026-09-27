package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// newSyncTestCleanupService returns a service with a cron scheduler that is
// set up but not started, so tests can inspect its entries without any job
// firing.
func newSyncTestCleanupService(policies *testutil.CleanupPolicyRepo) *CleanupService {
	s := NewCleanupService(policies, testutil.NewRepoRepo(), testutil.NewAssetRepo(),
		testutil.NewBlobStoreRepo(), testutil.NewBlobStore(), zap.NewNop().Sugar())
	s.defaultSchedule = "0 3 * * *"
	s.cronScheduler = cron.New()
	return s
}

func registeredSchedules(s *CleanupService) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.entries))
	for id, e := range s.entries {
		out[id] = e.schedule
	}
	return out
}

// Another node saved the policy: only the database knows about it here, and
// ReloadPolicy never ran on this node (#552).
func TestCleanupSync_RegistersPolicyCreatedOnAnotherNode(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo()
	s := newSyncTestCleanupService(policies)

	require.NoError(t, policies.Create(context.Background(), &domain.CleanupPolicy{
		Name: "made-elsewhere", Enabled: true, ScheduleCron: "*/5 * * * *",
	}))
	s.syncPolicies(context.Background())

	assert.Equal(t, map[string]string{"policy-1": "*/5 * * * *"}, registeredSchedules(s))
	assert.Len(t, s.cronScheduler.Entries(), 1)
}

func TestCleanupSync_ReplacesEntryWhenScheduleChangedOnAnotherNode(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{
		ID: "p1", Name: "p1", Enabled: true, ScheduleCron: "*/5 * * * *",
	})
	s := newSyncTestCleanupService(policies)
	s.syncPolicies(context.Background())
	require.Equal(t, map[string]string{"p1": "*/5 * * * *"}, registeredSchedules(s))

	require.NoError(t, policies.Update(context.Background(), &domain.CleanupPolicy{
		ID: "p1", Name: "p1", Enabled: true, ScheduleCron: "0 * * * *",
	}))
	s.syncPolicies(context.Background())

	assert.Equal(t, map[string]string{"p1": "0 * * * *"}, registeredSchedules(s))
	assert.Len(t, s.cronScheduler.Entries(), 1, "the old entry must be removed, not left beside the new one")
}

func TestCleanupSync_UsesDefaultScheduleForPolicyWithoutOne(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{ID: "p1", Name: "p1", Enabled: true})
	s := newSyncTestCleanupService(policies)
	s.syncPolicies(context.Background())
	s.syncPolicies(context.Background())

	assert.Equal(t, map[string]string{"p1": "0 3 * * *"}, registeredSchedules(s))
	assert.Len(t, s.cronScheduler.Entries(), 1)
}

func TestCleanupSync_RemovesEntryWhenPolicyDisabledOrDeletedOnAnotherNode(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(
		&domain.CleanupPolicy{ID: "p-off", Name: "p-off", Enabled: true, ScheduleCron: "*/5 * * * *"},
		&domain.CleanupPolicy{ID: "p-gone", Name: "p-gone", Enabled: true, ScheduleCron: "*/5 * * * *"},
	)
	s := newSyncTestCleanupService(policies)
	s.syncPolicies(context.Background())
	require.Len(t, registeredSchedules(s), 2)

	require.NoError(t, policies.Update(context.Background(), &domain.CleanupPolicy{
		ID: "p-off", Name: "p-off", Enabled: false, ScheduleCron: "*/5 * * * *",
	}))
	require.NoError(t, policies.Delete(context.Background(), "p-gone"))
	s.syncPolicies(context.Background())

	assert.Empty(t, registeredSchedules(s))
	assert.Empty(t, s.cronScheduler.Entries())
}

// A failed read says nothing about the policies, so the node keeps running
// what it has instead of dropping every schedule.
func TestCleanupSync_ListErrorKeepsExistingEntries(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{
		ID: "p1", Name: "p1", Enabled: true, ScheduleCron: "*/5 * * * *",
	})
	s := newSyncTestCleanupService(policies)
	s.syncPolicies(context.Background())

	policies.Err = errors.New("db down")
	s.syncPolicies(context.Background())

	assert.Equal(t, map[string]string{"p1": "*/5 * * * *"}, registeredSchedules(s))
}

// Until the next sync, this node's entry may still describe the policy as it
// was when registered. The job reads the policy when it fires, so a policy
// disabled or deleted elsewhere does not run on a stale snapshot.
func TestCleanupScheduledRun_ReadsThePolicyWhenItFires(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{
		ID: "p1", Name: "p1", Enabled: true, DryRun: true,
		Criteria: map[string]any{"artifactAgeDays": float64(30)},
		Scope:    domain.CleanupScope{RepositoryName: "raw-c"},
	})
	s := newSyncTestCleanupService(policies)
	ctx := context.Background()

	s.runScheduled(ctx, "p1", "0 3 * * *")
	require.Len(t, policies.RunRecords, 1, "an enabled policy runs")

	require.NoError(t, policies.Update(ctx, &domain.CleanupPolicy{ID: "p1", Name: "p1", Enabled: false}))
	s.runScheduled(ctx, "p1", "0 3 * * *")
	require.NoError(t, policies.Delete(ctx, "p1"))
	s.runScheduled(ctx, "p1", "0 3 * * *")

	assert.Len(t, policies.RunRecords, 1, "a disabled or deleted policy must not run")
}

// End to end: the scheduler's own sync entry picks the change up without any
// ReloadPolicy call on this node.
func TestCleanupScheduler_SyncsPoliciesPeriodically(t *testing.T) {
	prev := cleanupPolicySyncSpec
	cleanupPolicySyncSpec = "@every 1s"
	t.Cleanup(func() { cleanupPolicySyncSpec = prev })

	policies := testutil.NewCleanupPolicyRepo()
	s := NewCleanupService(policies, testutil.NewRepoRepo(), testutil.NewAssetRepo(),
		testutil.NewBlobStoreRepo(), testutil.NewBlobStore(), zap.NewNop().Sugar())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.StartCronScheduler(ctx, "0 3 * * *"); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	require.NoError(t, policies.Create(context.Background(), &domain.CleanupPolicy{
		Name: "made-elsewhere", Enabled: true, ScheduleCron: "*/5 * * * *",
	}))
	require.Eventually(t, func() bool {
		return registeredSchedules(s)["policy-1"] == "*/5 * * * *"
	}, 5*time.Second, 50*time.Millisecond)
}

// A node still holding the entry for the old schedule must not run the policy
// at the old time after another node rescheduled it.
func TestCleanupScheduledRun_SkipsAStaleSchedule(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{
		ID: "p1", Name: "p1", Enabled: true, DryRun: true, ScheduleCron: "0 4 * * *",
		Criteria: map[string]any{"artifactAgeDays": float64(30)},
		Scope:    domain.CleanupScope{RepositoryName: "raw-c"},
	})
	s := newSyncTestCleanupService(policies)
	ctx := context.Background()

	s.runScheduled(ctx, "p1", "*/5 * * * *")
	assert.Empty(t, policies.RunRecords, "the entry's schedule is no longer the policy's")

	s.runScheduled(ctx, "p1", "0 4 * * *")
	assert.Len(t, policies.RunRecords, 1)
}
