package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/nexspence-oss/nexspence/internal/distlock"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// newSyncTestReplicationService returns a service with a cron scheduler that
// is set up but not started, so tests can inspect its entries without any job
// firing.
func newSyncTestReplicationService(rules *testutil.ReplicationRepo) *ReplicationService {
	s := NewReplicationService(rules, testutil.NewAssetRepo(), testutil.NewBlobStore(),
		"test-secret", nil, zap.NewNop().Sugar())
	s.cronScheduler = cron.New()
	return s
}

func registeredRuleSchedules(s *ReplicationService) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.entries))
	for id, e := range s.entries {
		out[id] = e.schedule
	}
	return out
}

func createRule(t *testing.T, rules *testutil.ReplicationRepo, r domain.ReplicationRule) string {
	t.Helper()
	require.NoError(t, rules.CreateRule(context.Background(), &r))
	return r.ID
}

func updateRule(t *testing.T, rules *testutil.ReplicationRepo, id string, mutate func(*domain.ReplicationRule)) {
	t.Helper()
	r, err := rules.GetRule(context.Background(), id)
	require.NoError(t, err)
	mutate(r)
	require.NoError(t, rules.UpdateRule(context.Background(), r))
}

func historyCount(t *testing.T, rules *testutil.ReplicationRepo, id string) int {
	t.Helper()
	h, err := rules.ListHistory(context.Background(), id, 100)
	require.NoError(t, err)
	return len(h)
}

// Another node saved the rule: only the database knows about it here, and
// ReloadRule never ran on this node (#574).
func TestReplicationSync_RegistersRuleCreatedOnAnotherNode(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	s := newSyncTestReplicationService(rules)

	id := createRule(t, rules, domain.ReplicationRule{Name: "made-elsewhere", CronExpr: "*/5 * * * *", Enabled: true})
	s.syncRules(context.Background())

	assert.Equal(t, map[string]string{id: "*/5 * * * *"}, registeredRuleSchedules(s))
	assert.Len(t, s.cronScheduler.Entries(), 1)
}

func TestReplicationSync_ReplacesEntryWhenScheduleChangedOnAnotherNode(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	id := createRule(t, rules, domain.ReplicationRule{Name: "r", CronExpr: "*/5 * * * *", Enabled: true})
	s := newSyncTestReplicationService(rules)
	s.syncRules(context.Background())
	require.Equal(t, map[string]string{id: "*/5 * * * *"}, registeredRuleSchedules(s))

	updateRule(t, rules, id, func(r *domain.ReplicationRule) { r.CronExpr = "0 * * * *" })
	s.syncRules(context.Background())

	assert.Equal(t, map[string]string{id: "0 * * * *"}, registeredRuleSchedules(s))
	assert.Len(t, s.cronScheduler.Entries(), 1, "the old entry must be removed, not left beside the new one")
}

func TestReplicationSync_RemovesEntryWhenRuleDisabledOrDeletedOnAnotherNode(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	off := createRule(t, rules, domain.ReplicationRule{Name: "off", CronExpr: "*/5 * * * *", Enabled: true})
	gone := createRule(t, rules, domain.ReplicationRule{Name: "gone", CronExpr: "*/5 * * * *", Enabled: true})
	s := newSyncTestReplicationService(rules)
	s.syncRules(context.Background())
	require.Len(t, registeredRuleSchedules(s), 2)

	updateRule(t, rules, off, func(r *domain.ReplicationRule) { r.Enabled = false })
	require.NoError(t, rules.DeleteRule(context.Background(), gone))
	s.syncRules(context.Background())

	assert.Empty(t, registeredRuleSchedules(s))
	assert.Empty(t, s.cronScheduler.Entries())
}

// A rule with an expression cron cannot parse is remembered without an entry,
// so the sync does not try (and warn) again every pass until it changes.
func TestReplicationSync_InvalidScheduleRegisteredOnce(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	id := createRule(t, rules, domain.ReplicationRule{Name: "bad", CronExpr: "not a cron", Enabled: true})
	core, logs := observer.New(zapcore.WarnLevel)
	s := newSyncTestReplicationService(rules)
	s.log = zap.New(core).Sugar()

	s.syncRules(context.Background())
	s.syncRules(context.Background())

	assert.Equal(t, map[string]string{id: "not a cron"}, registeredRuleSchedules(s))
	assert.Empty(t, s.cronScheduler.Entries())
	assert.Equal(t, 1, logs.FilterMessageSnippet("invalid cron_expr").Len())
}

// A failed read says nothing about the rules, so the node keeps running what
// it has instead of dropping every schedule.
func TestReplicationSync_ListErrorKeepsExistingEntries(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	id := createRule(t, rules, domain.ReplicationRule{Name: "r", CronExpr: "*/5 * * * *", Enabled: true})
	s := newSyncTestReplicationService(rules)
	s.syncRules(context.Background())

	rules.ListErr = errors.New("db down")
	s.syncRules(context.Background())

	assert.Equal(t, map[string]string{id: "*/5 * * * *"}, registeredRuleSchedules(s))
}

// Until the next sync, this node's entry may still describe the rule as it
// was when registered. The job reads the rule when it fires, so a rule
// disabled elsewhere must not run (the issue's phase C).
func TestReplicationScheduledRun_SkipsRuleDisabledOnAnotherNode(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	id := createRule(t, rules, domain.ReplicationRule{
		Name: "r", SourceRepo: "src", TargetURL: "http://127.0.0.1:1", TargetRepo: "dst",
		CronExpr: "* * * * *", Enabled: true,
	})
	s := newSyncTestReplicationService(rules)
	ctx := context.Background()

	s.runScheduled(ctx, id, "* * * * *")
	require.Equal(t, 1, historyCount(t, rules, id), "an enabled rule runs")

	updateRule(t, rules, id, func(r *domain.ReplicationRule) { r.Enabled = false })
	s.runScheduled(ctx, id, "* * * * *")
	require.NoError(t, rules.DeleteRule(ctx, id))
	s.runScheduled(ctx, id, "* * * * *")

	assert.Equal(t, 1, historyCount(t, rules, id), "a disabled or deleted rule must not run")
}

// A node still holding the entry for the old schedule must not run the rule at
// the old time after another node rescheduled it.
func TestReplicationScheduledRun_SkipsAStaleSchedule(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	id := createRule(t, rules, domain.ReplicationRule{
		Name: "r", SourceRepo: "src", TargetURL: "http://127.0.0.1:1", TargetRepo: "dst",
		CronExpr: "0 4 * * *", Enabled: true,
	})
	s := newSyncTestReplicationService(rules)
	ctx := context.Background()

	s.runScheduled(ctx, id, "*/5 * * * *")
	assert.Equal(t, 0, historyCount(t, rules, id), "the entry's schedule is no longer the rule's")

	s.runScheduled(ctx, id, "0 4 * * *")
	assert.Equal(t, 1, historyCount(t, rules, id))
}

// End to end: the scheduler's own sync entry picks the change up without any
// ReloadRule call on this node.
func TestReplicationScheduler_SyncsRulesPeriodically(t *testing.T) {
	prev := replicationRuleSyncSpec
	replicationRuleSyncSpec = "@every 1s"
	t.Cleanup(func() { replicationRuleSyncSpec = prev })

	rules := testutil.NewReplicationRepo()
	s := NewReplicationService(rules, testutil.NewAssetRepo(), testutil.NewBlobStore(),
		"test-secret", nil, zap.NewNop().Sugar())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.StartCronScheduler(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	id := createRule(t, rules, domain.ReplicationRule{Name: "made-elsewhere", CronExpr: "*/5 * * * *", Enabled: true})
	require.Eventually(t, func() bool {
		return registeredRuleSchedules(s)[id] == "*/5 * * * *"
	}, 5*time.Second, 50*time.Millisecond)
}

// sharedLocker is an in-memory distlock.Locker shared by several services,
// standing in for the Redis locker every HA node talks to.
type sharedLocker struct {
	mu       sync.Mutex
	held     map[string]bool
	acquired []string
	err      error // when set, Acquire fails with it (lock backend down)
}

func newSharedLocker() *sharedLocker { return &sharedLocker{held: map[string]bool{}} }

func (l *sharedLocker) Acquire(_ context.Context, key string, _ time.Duration) (distlock.Lock, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	if l.held[key] {
		return nil, distlock.ErrLockHeld
	}
	l.held[key] = true
	l.acquired = append(l.acquired, key)
	return &sharedLock{owner: l, key: key}, nil
}

func (l *sharedLocker) ForceRelease(_ context.Context, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, key)
	return nil
}

func (l *sharedLocker) isHeld(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held[key]
}

func (l *sharedLocker) keys() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.acquired...)
}

type sharedLock struct {
	owner *sharedLocker
	key   string
}

func (l *sharedLock) Release(context.Context) error {
	return l.owner.ForceRelease(context.Background(), l.key)
}

// Every node registers every enabled rule after a restart (the issue's phase
// B); the per-rule lock makes only one of them run it.
func TestReplicationRunRule_LockHeldByAnotherNode_DoesNotRun(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	id := createRule(t, rules, domain.ReplicationRule{
		Name: "r", SourceRepo: "src", TargetURL: "http://127.0.0.1:1", TargetRepo: "dst",
		CronExpr: "* * * * *", Enabled: true,
	})
	locker := newSharedLocker()
	other, err := locker.Acquire(context.Background(), replicationRuleLockPrefix+id, time.Minute)
	require.NoError(t, err)

	core, logs := observer.New(zapcore.InfoLevel)
	s := newSyncTestReplicationService(rules).WithLocker(locker)
	s.log = zap.New(core).Sugar()

	err = s.RunRule(context.Background(), id)
	require.ErrorIs(t, err, distlock.ErrLockHeld)
	require.ErrorIs(t, err, ErrReplicationRuleRunning, "callers answer 409 on this")
	assert.Equal(t, 0, historyCount(t, rules, id), "no history row for a run that never happened")

	run, err := s.StartRule(context.Background(), id)
	require.ErrorIs(t, err, ErrReplicationRuleRunning, "a manual run is refused up front, not after a 202")
	assert.Nil(t, run)
	assert.False(t, s.Running(id), "a refused start leaves no in-process guard behind")

	// The cron path reports it at info level, not as a cron error.
	s.runScheduled(context.Background(), id, "* * * * *")
	assert.Equal(t, 0, historyCount(t, rules, id))
	assert.Zero(t, logs.FilterLevelExact(zapcore.ErrorLevel).Len())
	assert.Equal(t, 1, logs.FilterMessageSnippet("already running").Len())

	require.NoError(t, other.Release(context.Background()))
	// Once the other node is done, this one runs it (the run itself fails
	// against the unreachable target, but it does run and record history).
	err = s.RunRule(context.Background(), id)
	assert.NotErrorIs(t, err, distlock.ErrLockHeld)
	assert.Equal(t, 1, historyCount(t, rules, id))
}

func TestReplicationRunRule_TakesAndReleasesPerRuleLock(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	a := createRule(t, rules, domain.ReplicationRule{Name: "a", SourceRepo: "src", TargetURL: "http://127.0.0.1:1", TargetRepo: "dst", Enabled: true})
	b := createRule(t, rules, domain.ReplicationRule{Name: "b", SourceRepo: "src", TargetURL: "http://127.0.0.1:1", TargetRepo: "dst", Enabled: true})
	locker := newSharedLocker()
	s := newSyncTestReplicationService(rules).WithLocker(locker)

	_ = s.RunRule(context.Background(), a)
	_ = s.RunRule(context.Background(), b)

	assert.Equal(t, []string{replicationRuleLockPrefix + a, replicationRuleLockPrefix + b}, locker.keys())
	assert.False(t, locker.isHeld(replicationRuleLockPrefix+a), "the lock is released when the run ends")
	assert.False(t, locker.isHeld(replicationRuleLockPrefix+b))
}

// Without the lock there is no exclusion, so the run stops instead of
// pushing unprotected.
func TestReplicationRunRule_LockBackendDown_DoesNotRun(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	id := createRule(t, rules, domain.ReplicationRule{Name: "r", SourceRepo: "src", TargetURL: "http://127.0.0.1:1", TargetRepo: "dst", Enabled: true})
	locker := newSharedLocker()
	locker.err = errors.New("redis down")
	s := newSyncTestReplicationService(rules).WithLocker(locker)

	err := s.RunRule(context.Background(), id)
	require.ErrorContains(t, err, "redis down")
	assert.Equal(t, 0, historyCount(t, rules, id))
}

// Nothing renews the lock, so past its TTL another node may start the same
// rule; a run that reaches that moment stops pushing (#371) and the next run
// diffs against the target again.
func TestReplicationRunRule_StopsPushingAtLockDeadline(t *testing.T) {
	var puts atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/service/rest/v1/assets") {
			fmt.Fprint(w, `{"items":[],"continuationToken":null}`)
			return
		}
		puts.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer target.Close()

	ctx := context.Background()
	assets := testutil.NewAssetRepo()
	blobs := testutil.NewBlobStore()
	for i := range 2 {
		key := fmt.Sprintf("k%d", i)
		require.NoError(t, blobs.Put(ctx, key, testutil.MakeReader("x"), 1))
		require.NoError(t, assets.Create(ctx, &domain.Asset{Repository: "src", Path: fmt.Sprintf("/a%d", i), BlobKey: key, SizeBytes: 1}))
	}
	s := NewReplicationService(testutil.NewReplicationRepo(), assets, blobs, "test-secret", nil, zap.NewNop().Sugar())
	s.newClient = func(timeout time.Duration) *http.Client { return &http.Client{Timeout: timeout} }
	rule := &domain.ReplicationRule{ID: "r", Name: "r", SourceRepo: "src", TargetURL: target.URL, TargetRepo: "dst"}

	hist := &domain.ReplicationHistory{}
	require.NoError(t, s.runRule(ctx, rule, hist, time.Now().Add(-time.Second)))
	assert.Zero(t, puts.Load(), "past the deadline nothing is pushed")
	assert.Zero(t, hist.PushedCount)

	hist = &domain.ReplicationHistory{}
	require.NoError(t, s.runRule(ctx, rule, hist, time.Time{}))
	assert.Equal(t, int32(2), puts.Load(), "no deadline (no locker) pushes everything")
}

// The lock only covers a run in progress: a node whose tick fires after the
// other node already finished the slot's run must not run it again (the
// issue's "two history rows per slot").
func TestReplicationScheduledRun_SkipsSlotAlreadyRunOnAnotherNode(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	id := createRule(t, rules, domain.ReplicationRule{
		Name: "r", SourceRepo: "src", TargetURL: "http://127.0.0.1:1", TargetRepo: "dst",
		CronExpr: "* * * * *", Enabled: true,
	})
	locker := newSharedLocker()
	node1 := newSyncTestReplicationService(rules).WithLocker(locker)
	node2 := newSyncTestReplicationService(rules).WithLocker(locker)

	node1.runScheduled(context.Background(), id, "* * * * *")
	require.Equal(t, 1, historyCount(t, rules, id))
	node2.runScheduled(context.Background(), id, "* * * * *")

	assert.Equal(t, 1, historyCount(t, rules, id), "one run per slot across nodes")
}

func TestReplicationScheduledRun_RunsWhenLastRunWasAnEarlierSlot(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	id := createRule(t, rules, domain.ReplicationRule{
		Name: "r", SourceRepo: "src", TargetURL: "http://127.0.0.1:1", TargetRepo: "dst",
		CronExpr: "* * * * *", Enabled: true,
	})
	require.NoError(t, rules.AddHistory(context.Background(), &domain.ReplicationHistory{
		RuleID: id, StartedAt: time.Now().Add(-2 * time.Minute),
	}))
	s := newSyncTestReplicationService(rules).WithLocker(newSharedLocker())

	s.runScheduled(context.Background(), id, "* * * * *")

	assert.Equal(t, 2, historyCount(t, rules, id))
}

// The slot guard is for the scheduler only: an admin's manual run goes ahead
// whatever ran a moment ago.
func TestReplicationRunRule_ManualRunIgnoresSlot(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	id := createRule(t, rules, domain.ReplicationRule{
		Name: "r", SourceRepo: "src", TargetURL: "http://127.0.0.1:1", TargetRepo: "dst",
		CronExpr: "* * * * *", Enabled: true,
	})
	require.NoError(t, rules.AddHistory(context.Background(), &domain.ReplicationHistory{RuleID: id, StartedAt: time.Now()}))
	s := newSyncTestReplicationService(rules).WithLocker(newSharedLocker())

	_ = s.RunRule(context.Background(), id)

	assert.Equal(t, 2, historyCount(t, rules, id))
}

func TestReplicationSlotStart(t *testing.T) {
	now := time.Date(2026, 9, 30, 11, 14, 30, 0, time.UTC)
	// Standard specs fire on whole minutes; the slot reaches back by the
	// allowed clock skew for a node whose clock runs ahead.
	assert.Equal(t, time.Date(2026, 9, 30, 11, 13, 50, 0, time.UTC), replicationSlotStart("* * * * *", now))
	assert.Equal(t, time.Date(2026, 9, 30, 11, 13, 50, 0, time.UTC), replicationSlotStart("0 2 * * *", now))
	// @every intervals are not aligned across nodes (each counts from its
	// own registration) and may be under a minute: half an interval back.
	assert.Equal(t, now.Add(-15*time.Second), replicationSlotStart("@every 30s", now))
	assert.Equal(t, now.Add(-time.Hour), replicationSlotStart("@every 2h", now))
}

// A rule without a schedule is manual-only: no cron entry and nothing to warn
// about on every sync.
func TestReplicationSync_ManualOnlyRuleIsNotScheduled(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	createRule(t, rules, domain.ReplicationRule{Name: "manual", Enabled: true})
	core, logs := observer.New(zapcore.WarnLevel)
	s := newSyncTestReplicationService(rules)
	s.log = zap.New(core).Sugar()

	s.syncRules(context.Background())
	s.syncRules(context.Background())

	assert.Empty(t, s.cronScheduler.Entries())
	assert.Zero(t, logs.Len())
}

func TestReplicationStartRule_RefusesOverlappingRunInProcess(t *testing.T) {
	rules := testutil.NewReplicationRepo()
	id := createRule(t, rules, domain.ReplicationRule{Name: "r", SourceRepo: "src", TargetURL: "http://127.0.0.1:1", TargetRepo: "dst"})
	locker := newSharedLocker()
	s := newSyncTestReplicationService(rules).WithLocker(locker)

	run, err := s.StartRule(context.Background(), id)
	require.NoError(t, err)
	assert.True(t, s.Running(id))
	assert.True(t, locker.isHeld(replicationRuleLockPrefix+id), "the lock is taken before the caller answers")

	_, err = s.StartRule(context.Background(), id)
	require.ErrorIs(t, err, ErrReplicationRuleRunning)

	_ = run(context.Background())
	assert.False(t, s.Running(id))
	assert.False(t, locker.isHeld(replicationRuleLockPrefix+id))
	assert.Equal(t, 1, historyCount(t, rules, id))
}
