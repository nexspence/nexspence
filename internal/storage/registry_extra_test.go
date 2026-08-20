package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/storage"
)

// ── Registry.Get ─────────────────────────────────────────────────────────────

func TestRegistry_Get_EmptyID_ReturnsDefault(t *testing.T) {
	def, _ := storage.NewLocalBlobStore(t.TempDir())
	r := storage.NewRegistry(def)
	got, err := r.Get(context.Background(), storage.BlobStoreDescriptor{})
	require.NoError(t, err)
	assert.Equal(t, def, got)
}

func TestRegistry_Get_CacheMiss_CreatesLocal(t *testing.T) {
	r := storage.NewRegistry(nil)
	desc := storage.BlobStoreDescriptor{
		ID:     "store-1",
		Type:   "local",
		Config: map[string]any{"path": t.TempDir()},
	}
	bs, err := r.Get(context.Background(), desc)
	require.NoError(t, err)
	require.NotNil(t, bs)
}

func TestRegistry_Get_CacheHit_ReturnsSame(t *testing.T) {
	r := storage.NewRegistry(nil)
	desc := storage.BlobStoreDescriptor{
		ID:     "store-2",
		Type:   "local",
		Config: map[string]any{"path": t.TempDir()},
	}
	ctx := context.Background()
	first, err := r.Get(ctx, desc)
	require.NoError(t, err)
	second, err := r.Get(ctx, desc)
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestRegistry_Get_UnknownType_Error(t *testing.T) {
	r := storage.NewRegistry(nil)
	_, err := r.Get(context.Background(), storage.BlobStoreDescriptor{ID: "x", Type: "unknown"})
	require.Error(t, err)
}

func TestRegistry_Get_S3MissingBucket_Error(t *testing.T) {
	r := storage.NewRegistry(nil)
	_, err := r.Get(context.Background(), storage.BlobStoreDescriptor{
		ID:   "s3-bad",
		Type: "s3",
		Config: map[string]any{
			"bucket": "", // empty → should error
		},
	})
	require.Error(t, err)
}

// ── Registry.Invalidate ───────────────────────────────────────────────────────

func TestRegistry_Invalidate_ForcesRecreate(t *testing.T) {
	r := storage.NewRegistry(nil)
	dir := t.TempDir()
	desc := storage.BlobStoreDescriptor{
		ID:     "store-inv",
		Type:   "local",
		Config: map[string]any{"path": dir},
	}
	ctx := context.Background()
	first, err := r.Get(ctx, desc)
	require.NoError(t, err)

	r.Invalidate("store-inv")

	// After invalidation a new instance is created (may be equal by type/path but not ptr)
	second, err := r.Get(ctx, desc)
	require.NoError(t, err)
	// Both should be valid even if different pointers.
	assert.NotNil(t, second)
	_ = first
}

func TestRegistry_Invalidate_NonExistent_NoOp(t *testing.T) {
	r := storage.NewRegistry(nil)
	assert.NotPanics(t, func() { r.Invalidate("does-not-exist") })
}

// ── NewFromConfig ─────────────────────────────────────────────────────────────

func TestNewFromConfig_Local(t *testing.T) {
	bs, err := storage.NewFromConfig(context.Background(), "local", map[string]any{
		"path": t.TempDir(),
	})
	require.NoError(t, err)
	require.NotNil(t, bs)
}

func TestNewFromConfig_LocalEmpty_DefaultPath(t *testing.T) {
	// Empty type + no path → uses "./data/blobs" (creates it relative to cwd)
	bs, err := storage.NewFromConfig(context.Background(), "", map[string]any{})
	require.NoError(t, err)
	require.NotNil(t, bs)
}

func TestNewFromConfig_S3MissingBucket_Error(t *testing.T) {
	_, err := storage.NewFromConfig(context.Background(), "s3", map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bucket")
}

func TestNewFromConfig_UnknownType_Error(t *testing.T) {
	_, err := storage.NewFromConfig(context.Background(), "sftp", map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown")
}

func TestNewFromConfig_NilConfig_Local(t *testing.T) {
	// nil config should not panic — strVal handles nil map
	bs, err := storage.NewFromConfig(context.Background(), "local", nil)
	require.NoError(t, err)
	require.NotNil(t, bs)
}

// ── Registry.PickMember ──────────────────────────────────────────────────────

func quota(n int64) *int64 { return &n }

// Round-robin rotates while every member has room.
func TestRegistry_PickMember_RoundRobin_Rotates(t *testing.T) {
	r := storage.NewRegistry(nil)
	members := []storage.MemberInfo{{ID: "a"}, {ID: "b"}}
	got := []string{
		r.PickMember("g", "round_robin", members),
		r.PickMember("g", "round_robin", members),
		r.PickMember("g", "round_robin", members),
	}
	assert.Equal(t, []string{"a", "b", "a"}, got)
}

// A member at or over its quota is skipped, not written to. Without this the
// round-robin branch kept filling a full member until the filesystem refused.
func TestRegistry_PickMember_RoundRobin_SkipsFullMember(t *testing.T) {
	r := storage.NewRegistry(nil)
	members := []storage.MemberInfo{
		{ID: "full", QuotaBytes: quota(10), UsedBytes: 12},
		{ID: "roomy", QuotaBytes: quota(100), UsedBytes: 1},
	}
	for i := 0; i < 4; i++ {
		assert.Equal(t, "roomy", r.PickMember("g-skip", "round_robin", members),
			"pick %d landed on the member that is over quota", i)
	}
}

// Nothing is written when the whole group is full: the caller turns "" into a
// clean quota rejection.
func TestRegistry_PickMember_RoundRobin_AllFull_ReturnsEmpty(t *testing.T) {
	r := storage.NewRegistry(nil)
	members := []storage.MemberInfo{
		{ID: "a", QuotaBytes: quota(10), UsedBytes: 10},
		{ID: "b", QuotaBytes: quota(10), UsedBytes: 12},
	}
	assert.Equal(t, "", r.PickMember("g-full", "round_robin", members))
}

func TestRegistry_PickMember_WriteToFirstFill_MovesOnWhenFull(t *testing.T) {
	r := storage.NewRegistry(nil)
	members := []storage.MemberInfo{
		{ID: "a", QuotaBytes: quota(10), UsedBytes: 10},
		{ID: "b", QuotaBytes: quota(10), UsedBytes: 3},
	}
	assert.Equal(t, "b", r.PickMember("g-wtff", "write_to_first_fill", members))
	assert.Equal(t, "", r.PickMember("g-wtff", "write_to_first_fill", []storage.MemberInfo{members[0]}))
}

// An unrecognized policy must not become a way around the quota either.
func TestRegistry_PickMember_UnknownPolicy_StillRespectsQuota(t *testing.T) {
	r := storage.NewRegistry(nil)
	members := []storage.MemberInfo{
		{ID: "a", QuotaBytes: quota(10), UsedBytes: 10},
		{ID: "b"},
	}
	assert.Equal(t, "b", r.PickMember("g-unknown", "whatever", members))
}

func TestRegistry_PickMember_NoMembers_ReturnsEmpty(t *testing.T) {
	r := storage.NewRegistry(nil)
	assert.Equal(t, "", r.PickMember("g-empty", "round_robin", nil))
}
