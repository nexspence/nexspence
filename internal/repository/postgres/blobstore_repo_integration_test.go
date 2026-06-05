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

// ── helpers ───────────────────────────────────────────────────────────────────

func int64Ptr(v int64) *int64 { return &v }

func makeLocalBS(name string) *domain.BlobStore {
	return &domain.BlobStore{
		Name: name,
		Type: "local",
		Config: map[string]any{
			"path": "/data/" + name,
		},
	}
}

func makeS3BS(name string) *domain.BlobStore {
	return &domain.BlobStore{
		Name: name,
		Type: "s3",
		Config: map[string]any{
			"bucket":   "my-bucket",
			"region":   "us-east-1",
			"endpoint": "https://s3.example.com",
		},
	}
}

func insertBS(t *testing.T, ctx context.Context, repo *blobStoreRepo, bs *domain.BlobStore) {
	t.Helper()
	if err := repo.Create(ctx, bs); err != nil {
		t.Fatalf("insertBS %q: %v", bs.Name, err)
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestBlobStoreRepo_Create_PopulatesIDAndTimestamps(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	bs := makeLocalBS("create_ts_bs")
	if err := repo.Create(ctx, bs); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if bs.ID == "" {
		t.Fatal("Create did not populate ID")
	}
	if bs.CreatedAt.IsZero() {
		t.Fatal("Create did not populate CreatedAt")
	}
	if bs.UpdatedAt.IsZero() {
		t.Fatal("Create did not populate UpdatedAt")
	}
}

func TestBlobStoreRepo_Create_LocalType_FieldsRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	bs := makeLocalBS("roundtrip_local_bs")
	bs.QuotaBytes = int64Ptr(1073741824) // 1 GiB
	insertBS(t, ctx, repo, bs)

	got, err := repo.Get(ctx, bs.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.ID != bs.ID {
		t.Errorf("ID: got %q, want %q", got.ID, bs.ID)
	}
	if got.Name != bs.Name {
		t.Errorf("Name: got %q, want %q", got.Name, bs.Name)
	}
	if got.Type != "local" {
		t.Errorf("Type: got %q, want local", got.Type)
	}
	if got.QuotaBytes == nil || *got.QuotaBytes != 1073741824 {
		t.Errorf("QuotaBytes: got %v, want 1073741824", got.QuotaBytes)
	}
	if got.UsedBytes != 0 {
		t.Errorf("UsedBytes: got %d, want 0 (initial)", got.UsedBytes)
	}
	if got.Config["path"] != "/data/roundtrip_local_bs" {
		t.Errorf("Config.path: got %v, want /data/roundtrip_local_bs", got.Config["path"])
	}
}

func TestBlobStoreRepo_Create_S3Type_ConfigRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	bs := makeS3BS("roundtrip_s3_bs")
	insertBS(t, ctx, repo, bs)

	got, err := repo.Get(ctx, bs.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Type != "s3" {
		t.Errorf("Type: got %q, want s3", got.Type)
	}
	if got.Config["bucket"] != "my-bucket" {
		t.Errorf("Config.bucket: got %v, want my-bucket", got.Config["bucket"])
	}
	if got.Config["region"] != "us-east-1" {
		t.Errorf("Config.region: got %v, want us-east-1", got.Config["region"])
	}
	if got.Config["endpoint"] != "https://s3.example.com" {
		t.Errorf("Config.endpoint: got %v, want https://s3.example.com", got.Config["endpoint"])
	}
}

func TestBlobStoreRepo_Create_NilQuota_Stored(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	bs := makeLocalBS("nil_quota_bs")
	bs.QuotaBytes = nil
	insertBS(t, ctx, repo, bs)

	got, err := repo.Get(ctx, bs.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.QuotaBytes != nil {
		t.Errorf("QuotaBytes: got %v, want nil (unlimited)", got.QuotaBytes)
	}
}

func TestBlobStoreRepo_Create_InvalidType_Errors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	bs := &domain.BlobStore{
		Name:   "bad_type_bs",
		Type:   "ftp", // not allowed by CHECK constraint
		Config: map[string]any{},
	}
	if err := repo.Create(ctx, bs); err == nil {
		t.Fatal("Create with invalid type: expected error, got nil")
	}
}

func TestBlobStoreRepo_Create_DuplicateName_Errors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	bs1 := makeLocalBS("dup_bs_name")
	insertBS(t, ctx, repo, bs1)

	bs2 := makeLocalBS("dup_bs_name")
	if err := repo.Create(ctx, bs2); err == nil {
		t.Fatal("Create with duplicate name: expected error, got nil")
	}
}

// ── Get (by name) ─────────────────────────────────────────────────────────────

func TestBlobStoreRepo_Get_NotFound_ReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	got, err := repo.Get(ctx, "nonexistent_bs_xyz")
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get(missing): want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Fatalf("Get(missing): expected nil, got %+v", got)
	}
}

// ── GetByID ───────────────────────────────────────────────────────────────────

func TestBlobStoreRepo_GetByID_HappyPath(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	bs := makeLocalBS("getbyid_bs")
	insertBS(t, ctx, repo, bs)

	got, err := repo.GetByID(ctx, bs.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("GetByID returned nil")
	}
	if got.ID != bs.ID {
		t.Errorf("ID: got %q, want %q", got.ID, bs.ID)
	}
	if got.Name != bs.Name {
		t.Errorf("Name: got %q, want %q", got.Name, bs.Name)
	}
}

func TestBlobStoreRepo_GetByID_NotFound_ReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	got, err := repo.GetByID(ctx, missing)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetByID(missing): want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Fatalf("GetByID(missing): expected nil, got %+v", got)
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestBlobStoreRepo_List_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}
}

func TestBlobStoreRepo_List_OrderedByName(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	for _, name := range []string{"zzz_list_bs", "aaa_list_bs", "mmm_list_bs"} {
		bs := makeLocalBS(name)
		insertBS(t, ctx, repo, bs)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("List: got %d, want 3", len(list))
	}
	if list[0].Name != "aaa_list_bs" || list[1].Name != "mmm_list_bs" || list[2].Name != "zzz_list_bs" {
		t.Errorf("List not ordered: got %q %q %q", list[0].Name, list[1].Name, list[2].Name)
	}
}

func TestBlobStoreRepo_List_MixedTypes(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	local := makeLocalBS("mixed_local_bs")
	s3 := makeS3BS("mixed_s3_bs")
	insertBS(t, ctx, repo, local)
	insertBS(t, ctx, repo, s3)

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List: got %d, want 2", len(list))
	}
	// Alphabetically: mixed_local_bs < mixed_s3_bs
	if list[0].Type != "local" {
		t.Errorf("list[0].Type: got %q, want local", list[0].Type)
	}
	if list[1].Type != "s3" {
		t.Errorf("list[1].Type: got %q, want s3", list[1].Type)
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestBlobStoreRepo_Update_ConfigAndQuota(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	bs := makeLocalBS("update_bs")
	bs.QuotaBytes = int64Ptr(500 * 1024 * 1024) // 500 MiB
	insertBS(t, ctx, repo, bs)

	// Change config and quota.
	bs.Config = map[string]any{"path": "/new/path"}
	bs.QuotaBytes = int64Ptr(2 * 1024 * 1024 * 1024) // 2 GiB
	if err := repo.Update(ctx, bs); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.Get(ctx, bs.Name)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got == nil {
		t.Fatal("Get after Update returned nil")
	}
	if got.Config["path"] != "/new/path" {
		t.Errorf("Config.path after Update: got %v, want /new/path", got.Config["path"])
	}
	if got.QuotaBytes == nil || *got.QuotaBytes != 2*1024*1024*1024 {
		t.Errorf("QuotaBytes after Update: got %v, want 2GiB", got.QuotaBytes)
	}
}

func TestBlobStoreRepo_Update_ClearQuota(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	bs := makeLocalBS("clear_quota_bs")
	bs.QuotaBytes = int64Ptr(1024)
	insertBS(t, ctx, repo, bs)

	// Clear quota (set to nil = unlimited).
	bs.QuotaBytes = nil
	if err := repo.Update(ctx, bs); err != nil {
		t.Fatalf("Update (clear quota): %v", err)
	}

	got, err := repo.Get(ctx, bs.Name)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got == nil {
		t.Fatal("Get after Update returned nil")
	}
	if got.QuotaBytes != nil {
		t.Errorf("QuotaBytes after clear: got %v, want nil", got.QuotaBytes)
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestBlobStoreRepo_Delete_RemovesStore(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	bs := makeLocalBS("delete_bs")
	insertBS(t, ctx, repo, bs)

	if err := repo.Delete(ctx, bs.Name); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.Get(ctx, bs.Name)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get after Delete: want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Fatal("Get after Delete: expected nil, store still exists")
	}
}

func TestBlobStoreRepo_Delete_UnknownName_NoError(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	if err := repo.Delete(ctx, "never_existed_bs_xyz"); err != nil {
		t.Fatalf("Delete(missing): unexpected error: %v", err)
	}
}

// ── UpdateUsedBytes ───────────────────────────────────────────────────────────

func TestBlobStoreRepo_UpdateUsedBytes_Increment(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	bs := makeLocalBS("used_bytes_inc_bs")
	insertBS(t, ctx, repo, bs)

	// Increment by 1024.
	if err := repo.UpdateUsedBytes(ctx, bs.Name, 1024); err != nil {
		t.Fatalf("UpdateUsedBytes(+1024): %v", err)
	}

	got, err := repo.Get(ctx, bs.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.UsedBytes != 1024 {
		t.Errorf("UsedBytes after +1024: got %d, want 1024", got.UsedBytes)
	}

	// Increment again.
	if err := repo.UpdateUsedBytes(ctx, bs.Name, 512); err != nil {
		t.Fatalf("UpdateUsedBytes(+512): %v", err)
	}

	got, err = repo.Get(ctx, bs.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UsedBytes != 1536 {
		t.Errorf("UsedBytes after second increment: got %d, want 1536", got.UsedBytes)
	}
}

func TestBlobStoreRepo_UpdateUsedBytes_Decrement(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	bs := makeLocalBS("used_bytes_dec_bs")
	insertBS(t, ctx, repo, bs)

	// Set initial value.
	if err := repo.UpdateUsedBytes(ctx, bs.Name, 2048); err != nil {
		t.Fatalf("UpdateUsedBytes(+2048): %v", err)
	}

	// Decrement by 512.
	if err := repo.UpdateUsedBytes(ctx, bs.Name, -512); err != nil {
		t.Fatalf("UpdateUsedBytes(-512): %v", err)
	}

	got, err := repo.Get(ctx, bs.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.UsedBytes != 1536 {
		t.Errorf("UsedBytes after decrement: got %d, want 1536", got.UsedBytes)
	}
}

func TestBlobStoreRepo_UpdateUsedBytes_NoGoBelow0(t *testing.T) {
	// GREATEST(0, used_bytes + delta) means used_bytes never goes negative.
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	bs := makeLocalBS("used_bytes_floor_bs")
	insertBS(t, ctx, repo, bs)

	// Decrement more than current balance (0) — should clamp at 0.
	if err := repo.UpdateUsedBytes(ctx, bs.Name, -9999); err != nil {
		t.Fatalf("UpdateUsedBytes(-9999 from 0): %v", err)
	}

	got, err := repo.Get(ctx, bs.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.UsedBytes != 0 {
		t.Errorf("UsedBytes floor: got %d, want 0 (GREATEST clamp)", got.UsedBytes)
	}
}

func TestBlobStoreRepo_UpdateUsedBytes_UnknownName_NoError(t *testing.T) {
	// UpdateUsedBytes on a non-existent store: Exec succeeds (0 rows affected).
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores")
	ctx := context.Background()
	repo := NewBlobStoreRepo(pool)

	if err := repo.UpdateUsedBytes(ctx, "ghost_bs_xyz", 100); err != nil {
		t.Fatalf("UpdateUsedBytes(missing): unexpected error: %v", err)
	}
}
