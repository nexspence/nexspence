package audit_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/audit"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func nopLog() *zap.SugaredLogger { return zap.NewNop().Sugar() }

// ── Fake PartitionStore ──────────────────────────────────────────────────

type fakeStore struct {
	mu         sync.Mutex
	partitions map[string]audit.Partition
	rowCount   int64
	errOn      string // method name to fail on, "" = none
}

func newFakeStore() *fakeStore { return &fakeStore{partitions: map[string]audit.Partition{}} }

func (f *fakeStore) ListPartitions(_ context.Context) ([]audit.Partition, error) {
	if f.errOn == "list" {
		return nil, errors.New("boom")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]audit.Partition, 0, len(f.partitions))
	for _, p := range f.partitions {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeStore) CreatePartition(_ context.Context, name string, from, to time.Time) error {
	if f.errOn == "create" {
		return errors.New("create failed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.partitions[name] = audit.Partition{Name: name, From: from, To: to}
	return nil
}

func (f *fakeStore) DropPartition(_ context.Context, name string) error {
	if f.errOn == "drop" {
		return fmt.Errorf("drop %s failed", name)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.partitions, name)
	return nil
}

func (f *fakeStore) CountRows(_ context.Context) (int64, error) {
	if f.errOn == "count" {
		return 0, errors.New("count failed")
	}
	return f.rowCount, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────

func newRotator(t *testing.T, store audit.PartitionStore, cfg config.AuditConfig, now time.Time) *audit.Rotator {
	t.Helper()
	r := audit.NewRotator(store, cfg, nopLog())
	audit.SetNowFuncForTest(r, func() time.Time { return now })
	return r
}

// ── Tests ────────────────────────────────────────────────────────────────

func TestRotator_EnsureFuturePartitions_Idempotent(t *testing.T) {
	store := newFakeStore()
	cfg := config.AuditConfig{LookaheadMonths: 2, RetentionDays: 90}
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	r := newRotator(t, store, cfg, now)
	r.RunOnce(context.Background())
	r.RunOnce(context.Background())

	parts, err := store.ListPartitions(context.Background())
	require.NoError(t, err)
	require.Len(t, parts, 3, "lookahead=2 → current + next 2 = 3 partitions")
	assert.Equal(t, "audit_events_2026_05", parts[0].Name)
	assert.Equal(t, "audit_events_2026_06", parts[1].Name)
	assert.Equal(t, "audit_events_2026_07", parts[2].Name)
}

func TestRotator_DropOldPartitions(t *testing.T) {
	store := newFakeStore()
	// Manually pre-load an old partition.
	_ = store.CreatePartition(context.Background(),
		"audit_events_2024_01",
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
	)
	cfg := config.AuditConfig{RetentionDays: 90, LookaheadMonths: 0}
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	r := newRotator(t, store, cfg, now)
	r.RunOnce(context.Background())

	parts, err := store.ListPartitions(context.Background())
	require.NoError(t, err)
	for _, p := range parts {
		assert.NotEqual(t, "audit_events_2024_01", p.Name, "old partition must be dropped")
	}
}

func TestRotator_DropOldPartitions_KeepsInWindow(t *testing.T) {
	store := newFakeStore()
	cfg := config.AuditConfig{RetentionDays: 90, LookaheadMonths: 0}
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

	// A partition whose end is inside the 90-day window.
	inWindowEnd := now.AddDate(0, 0, -10)
	_ = store.CreatePartition(context.Background(),
		"audit_events_in_window",
		inWindowEnd.AddDate(0, 0, -30),
		inWindowEnd,
	)

	r := newRotator(t, store, cfg, now)
	r.RunOnce(context.Background())

	parts, err := store.ListPartitions(context.Background())
	require.NoError(t, err)
	// ensureFuturePartitions (lookahead=0) creates current month partition (2026_05),
	// and we pre-created "in_window", so expect 2.
	require.Len(t, parts, 2)
	// Verify both the created partition and the in-window partition exist.
	names := map[string]bool{}
	for _, p := range parts {
		names[p.Name] = true
	}
	assert.True(t, names["audit_events_in_window"], "in-window partition must be kept")
	assert.True(t, names["audit_events_2026_05"], "current month partition must be created")
}

func TestRotator_SoftCap_UpdatesMetricAndWarns(t *testing.T) {
	store := newFakeStore()
	store.rowCount = 1500
	cfg := config.AuditConfig{SoftCap: 1000, LookaheadMonths: 0, RetentionDays: 90}
	now := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)

	// We can't assert on the warn line without a captured logger; this test
	// just verifies the rotator does not panic and the soft-cap path runs.
	r := newRotator(t, store, cfg, now)
	r.RunOnce(context.Background())
	// Metric is updated as a side effect — see metrics.AuditEventsCount usage.
}

func TestRotator_SoftCap_Disabled_ZeroCap(t *testing.T) {
	store := newFakeStore()
	store.rowCount = 999_999
	cfg := config.AuditConfig{SoftCap: 0, LookaheadMonths: 0, RetentionDays: 90}
	now := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)

	r := newRotator(t, store, cfg, now)
	r.RunOnce(context.Background()) // no panic, no warn (cap disabled)
}

func TestRotator_TickContinuesOnPartialFailure(t *testing.T) {
	store := newFakeStore()
	store.errOn = "list" // dropOldPartitions will fail; ensureFuturePartitions still runs

	cfg := config.AuditConfig{RetentionDays: 90, LookaheadMonths: 1, SoftCap: 0}
	now := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)

	r := newRotator(t, store, cfg, now)
	r.RunOnce(context.Background()) // must not panic

	// ensureFuturePartitions ran first and created partitions.
	store.errOn = ""
	parts, err := store.ListPartitions(context.Background())
	require.NoError(t, err)
	assert.Len(t, parts, 2, "lookahead=1 → current + next = 2 partitions")
}

func TestPartitionName_FormatsAsYearMonth(t *testing.T) {
	got := audit.PartitionName(time.Date(2026, 5, 12, 13, 14, 15, 0, time.UTC))
	assert.Equal(t, "audit_events_2026_05", got)
}

func TestMonthBounds_StartAndEnd(t *testing.T) {
	from, to := audit.MonthBounds(time.Date(2026, 5, 12, 13, 14, 15, 0, time.UTC))
	assert.Equal(t, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), from)
	assert.Equal(t, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), to)
}
