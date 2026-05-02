package storage_test

import (
	"testing"

	"github.com/nexspence-oss/nexspence/internal/storage"
)

func int64p(v int64) *int64 { return &v }

func members(ids ...string) []storage.MemberInfo {
	out := make([]storage.MemberInfo, len(ids))
	for i, id := range ids {
		out[i] = storage.MemberInfo{ID: id}
	}
	return out
}

func TestPickMember_RoundRobin_Cycles(t *testing.T) {
	r := storage.NewRegistry(nil)
	ms := members("a", "b", "c")
	got := []string{
		r.PickMember("g1", "round_robin", ms),
		r.PickMember("g1", "round_robin", ms),
		r.PickMember("g1", "round_robin", ms),
		r.PickMember("g1", "round_robin", ms),
	}
	if got[0] != "a" || got[1] != "b" || got[2] != "c" || got[3] != "a" {
		t.Errorf("expected a,b,c,a cycle, got %v", got)
	}
}

func TestPickMember_RoundRobin_IndependentCountersPerGroup(t *testing.T) {
	r := storage.NewRegistry(nil)
	ms := members("x", "y")
	r.PickMember("g1", "round_robin", ms)
	got := r.PickMember("g2", "round_robin", ms)
	if got != "x" {
		t.Errorf("want x (fresh counter), got %s", got)
	}
}

func TestPickMember_RoundRobin_Empty_ReturnsEmpty(t *testing.T) {
	r := storage.NewRegistry(nil)
	got := r.PickMember("g1", "round_robin", nil)
	if got != "" {
		t.Errorf("want empty string for empty members, got %q", got)
	}
}

func TestPickMember_WriteToFirstFill_NoQuota_AlwaysFirst(t *testing.T) {
	r := storage.NewRegistry(nil)
	ms := []storage.MemberInfo{
		{ID: "a", QuotaBytes: nil, UsedBytes: 999},
		{ID: "b"},
	}
	got := r.PickMember("g1", "write_to_first_fill", ms)
	if got != "a" {
		t.Errorf("nil quota = unlimited, want a, got %s", got)
	}
}

func TestPickMember_WriteToFirstFill_FirstFull_PicksSecond(t *testing.T) {
	r := storage.NewRegistry(nil)
	ms := []storage.MemberInfo{
		{ID: "a", QuotaBytes: int64p(100), UsedBytes: 100},
		{ID: "b", QuotaBytes: int64p(100), UsedBytes: 50},
	}
	got := r.PickMember("g1", "write_to_first_fill", ms)
	if got != "b" {
		t.Errorf("want b (first not full), got %s", got)
	}
}

func TestPickMember_WriteToFirstFill_AllFull_ReturnsEmpty(t *testing.T) {
	r := storage.NewRegistry(nil)
	ms := []storage.MemberInfo{
		{ID: "a", QuotaBytes: int64p(100), UsedBytes: 100},
		{ID: "b", QuotaBytes: int64p(200), UsedBytes: 200},
	}
	got := r.PickMember("g1", "write_to_first_fill", ms)
	if got != "" {
		t.Errorf("want empty (all full), got %s", got)
	}
}

func TestPickMember_UnknownPolicy_FallsBackToFirst(t *testing.T) {
	r := storage.NewRegistry(nil)
	ms := members("x", "y")
	got := r.PickMember("g1", "unknown_policy", ms)
	if got != "x" {
		t.Errorf("want x (fallback to first), got %s", got)
	}
}
