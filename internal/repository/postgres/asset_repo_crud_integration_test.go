//go:build integration

package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── parent-chain helper ───────────────────────────────────────────────────────

// assetParent holds the IDs of the prerequisite rows every asset test needs.
type assetParent struct {
	BlobStoreID  string
	RepositoryID string
	ComponentID  string
	RepoName     string
}

// makeAssetParent creates one blob_store → repository → component chain.
// suffix must be unique across concurrent tests (use t.Name() fragment or a short tag).
func makeAssetParent(t *testing.T, ctx context.Context, suffix string) assetParent {
	t.Helper()
	pool := pgtest.Pool(t)

	// blob store
	bsRepo := NewBlobStoreRepo(pool)
	bs := &domain.BlobStore{
		Name:   "asset_bs_" + suffix,
		Type:   "local",
		Config: map[string]any{"path": "/data/asset_" + suffix},
	}
	if err := bsRepo.Create(ctx, bs); err != nil {
		t.Fatalf("makeAssetParent: blob store: %v", err)
	}

	// repository
	rRepo := NewRepositoryRepo(pool)
	repoName := "asset_repo_" + suffix
	r := &domain.Repository{
		Name:        repoName,
		Format:      domain.FormatRaw,
		Type:        domain.TypeHosted,
		BlobStoreID: &bs.ID,
		Online:      true,
	}
	if err := rRepo.Create(ctx, r); err != nil {
		t.Fatalf("makeAssetParent: repository: %v", err)
	}

	// component
	cRepo := NewComponentRepo(pool)
	comp := &domain.Component{
		RepositoryID: r.ID,
		Format:       "raw",
		Group:        "grp_" + suffix,
		Name:         "comp_" + suffix,
		Version:      "1.0",
	}
	if err := cRepo.Create(ctx, comp); err != nil {
		t.Fatalf("makeAssetParent: component: %v", err)
	}

	return assetParent{
		BlobStoreID:  bs.ID,
		RepositoryID: r.ID,
		ComponentID:  comp.ID,
		RepoName:     repoName,
	}
}

// makeAsset builds a minimal domain.Asset using the given parent chain.
// path must be unique within the repository.
func makeAsset(p assetParent, path string) *domain.Asset {
	return &domain.Asset{
		ComponentID:  p.ComponentID,
		RepositoryID: p.RepositoryID,
		Path:         path,
		BlobStoreID:  p.BlobStoreID,
		BlobKey:      "blobkey_" + path,
		SizeBytes:    1024,
		ContentType:  "application/octet-stream",
		SHA1:         "da39a3ee5e6b4b0d3255bfef95601890afd80709",
		SHA256:       "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		MD5:          "d41d8cd98f00b204e9800998ecf8427e",
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestAssetRepo_Create_PopulatesIDAndTimestamps(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "cr_ts")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/create/file.bin")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.ID == "" {
		t.Fatal("Create did not populate ID")
	}
	if a.CreatedAt.IsZero() {
		t.Fatal("Create did not populate CreatedAt")
	}
}

func TestAssetRepo_Create_FieldsRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "cr_rt")
	repo := NewAssetRepo(pool)

	a := &domain.Asset{
		ComponentID:  p.ComponentID,
		RepositoryID: p.RepositoryID,
		Path:         "/mygroup/myartifact/1.0/file.jar",
		BlobStoreID:  p.BlobStoreID,
		BlobKey:      "sha256:abcdef123456",
		SizeBytes:    65536,
		ContentType:  "application/java-archive",
		SHA1:         "aabbcc001122334455667788990011223344556677",
		SHA256:       "deadbeef" + fmt.Sprintf("%056d", 0),
		MD5:          "feedface" + fmt.Sprintf("%024d", 0),
	}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}

	if got.ID != a.ID {
		t.Errorf("ID: got %q want %q", got.ID, a.ID)
	}
	if got.ComponentID != a.ComponentID {
		t.Errorf("ComponentID: got %q want %q", got.ComponentID, a.ComponentID)
	}
	if got.RepositoryID != a.RepositoryID {
		t.Errorf("RepositoryID: got %q want %q", got.RepositoryID, a.RepositoryID)
	}
	if got.Repository != p.RepoName {
		t.Errorf("Repository name: got %q want %q", got.Repository, p.RepoName)
	}
	if got.Path != a.Path {
		t.Errorf("Path: got %q want %q", got.Path, a.Path)
	}
	if got.BlobStoreID != a.BlobStoreID {
		t.Errorf("BlobStoreID: got %q want %q", got.BlobStoreID, a.BlobStoreID)
	}
	if got.BlobKey != a.BlobKey {
		t.Errorf("BlobKey: got %q want %q", got.BlobKey, a.BlobKey)
	}
	if got.SizeBytes != a.SizeBytes {
		t.Errorf("SizeBytes: got %d want %d", got.SizeBytes, a.SizeBytes)
	}
	if got.ContentType != a.ContentType {
		t.Errorf("ContentType: got %q want %q", got.ContentType, a.ContentType)
	}
	if got.SHA1 != a.SHA1 {
		t.Errorf("SHA1: got %q want %q", got.SHA1, a.SHA1)
	}
	if got.SHA256 != a.SHA256 {
		t.Errorf("SHA256: got %q want %q", got.SHA256, a.SHA256)
	}
	if got.MD5 != a.MD5 {
		t.Errorf("MD5: got %q want %q", got.MD5, a.MD5)
	}
	if got.DownloadCount != 0 {
		t.Errorf("DownloadCount: got %d want 0", got.DownloadCount)
	}
	if got.LastDownloaded != nil {
		t.Errorf("LastDownloaded: got %v want nil", got.LastDownloaded)
	}
	if got.LastModified.IsZero() {
		t.Error("LastModified must not be zero")
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt must not be zero")
	}
}

// TestAssetRepo_Create_NullableChecksums verifies that scanAsset correctly handles
// NULL sha1/sha256/md5 columns. PostgreSQL stores empty-string checksums as NULL
// via nullStr(); scanAsset must use sql.NullString intermediaries so that pgx v5
// does not error when scanning NULL into a Go string field.
func TestAssetRepo_Create_NullableChecksums(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "cr_null")
	repo := NewAssetRepo(pool)

	// Create asset with no checksums (all empty strings → stored as NULL via nullStr())
	a := &domain.Asset{
		ComponentID:  p.ComponentID,
		RepositoryID: p.RepositoryID,
		Path:         "/null-checksums/file.bin",
		BlobStoreID:  p.BlobStoreID,
		BlobKey:      "bk_null",
		SizeBytes:    0,
		ContentType:  "application/octet-stream",
		// SHA1, SHA256, MD5 intentionally empty — will be NULL in DB
	}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.SHA1 != "" || got.SHA256 != "" || got.MD5 != "" {
		t.Errorf("expected empty checksums, got SHA1=%q SHA256=%q MD5=%q", got.SHA1, got.SHA256, got.MD5)
	}
}

// Create with ON CONFLICT updates the existing row (upsert behaviour)
func TestAssetRepo_Create_UpsertUpdatesExistingPath(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "cr_ups")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/upsert/file.bin")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create (first): %v", err)
	}
	firstID := a.ID

	// Re-create same (repo,path) with different size/blob_key.
	// All checksum fields must be non-empty to avoid the scanAsset NULL bug.
	a2 := &domain.Asset{
		ComponentID:  p.ComponentID,
		RepositoryID: p.RepositoryID,
		Path:         "/upsert/file.bin", // same path = conflict
		BlobStoreID:  p.BlobStoreID,
		BlobKey:      "updated_blob_key",
		SizeBytes:    9999,
		ContentType:  "application/octet-stream",
		SHA1:         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SHA256:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		MD5:          "cccccccccccccccccccccccccccccccc",
	}
	if err := repo.Create(ctx, a2); err != nil {
		t.Fatalf("Create (upsert): %v", err)
	}

	// The upsert should return the same existing row ID
	if a2.ID != firstID {
		t.Errorf("upsert: ID changed: %q → %q (expected same row)", firstID, a2.ID)
	}

	got, err := repo.Get(ctx, firstID)
	if err != nil {
		t.Fatalf("Get after upsert: %v", err)
	}
	if got.BlobKey != "updated_blob_key" {
		t.Errorf("BlobKey not updated: got %q", got.BlobKey)
	}
	if got.SizeBytes != 9999 {
		t.Errorf("SizeBytes not updated: got %d", got.SizeBytes)
	}
}

// ── Get (by ID) ───────────────────────────────────────────────────────────────

func TestAssetRepo_Get_NotFoundReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()
	repo := NewAssetRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	got, err := repo.Get(ctx, missing)
	if err != nil {
		t.Fatalf("Get(missing): unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("Get(missing): expected nil, got %+v", got)
	}
}

// ── GetByPath ─────────────────────────────────────────────────────────────────

func TestAssetRepo_GetByPath_HappyPath(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "gbp_ok")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/path/to/artifact.tgz")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByPath(ctx, p.RepoName, "/path/to/artifact.tgz")
	if err != nil {
		t.Fatalf("GetByPath: %v", err)
	}
	if got == nil {
		t.Fatal("GetByPath returned nil")
	}
	if got.ID != a.ID {
		t.Errorf("ID: got %q want %q", got.ID, a.ID)
	}
	if got.Path != a.Path {
		t.Errorf("Path: got %q want %q", got.Path, a.Path)
	}
}

func TestAssetRepo_GetByPath_NotFoundReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "gbp_nf")
	repo := NewAssetRepo(pool)

	got, err := repo.GetByPath(ctx, p.RepoName, "/nonexistent/path.bin")
	if err != nil {
		t.Fatalf("GetByPath(missing): unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("GetByPath(missing): expected nil, got %+v", got)
	}
}

func TestAssetRepo_GetByPath_WrongRepoReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "gbp_wr")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/file.bin")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Query with a different (non-existent) repo name
	got, err := repo.GetByPath(ctx, "wrong_repo_name", "/file.bin")
	if err != nil {
		t.Fatalf("GetByPath(wrong repo): unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("GetByPath(wrong repo): expected nil, got %+v", got)
	}
}

// ── ListByComponentID ─────────────────────────────────────────────────────────

func TestAssetRepo_ListByComponentID_ReturnsAllAssets(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "lbci")
	repo := NewAssetRepo(pool)

	paths := []string{"/comp/a.jar", "/comp/a.pom", "/comp/a-sources.jar"}
	for _, path := range paths {
		a := makeAsset(p, path)
		a.BlobKey = "bk_" + path
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create %q: %v", path, err)
		}
	}

	assets, err := repo.ListByComponentID(ctx, p.ComponentID)
	if err != nil {
		t.Fatalf("ListByComponentID: %v", err)
	}
	if len(assets) != 3 {
		t.Errorf("expected 3 assets, got %d", len(assets))
	}
	for _, got := range assets {
		if got.ComponentID != p.ComponentID {
			t.Errorf("asset %q has wrong ComponentID: %q", got.Path, got.ComponentID)
		}
	}
}

func TestAssetRepo_ListByComponentID_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "lbci_empty")
	repo := NewAssetRepo(pool)

	assets, err := repo.ListByComponentID(ctx, p.ComponentID)
	if err != nil {
		t.Fatalf("ListByComponentID(empty): %v", err)
	}
	if len(assets) != 0 {
		t.Errorf("expected 0 assets, got %d", len(assets))
	}
}

// ── List (basic) ──────────────────────────────────────────────────────────────

func TestAssetRepo_List_BasicSingleRepo(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "list_basic")
	repo := NewAssetRepo(pool)

	for i := 0; i < 3; i++ {
		a := makeAsset(p, fmt.Sprintf("/list/file%02d.bin", i))
		a.BlobKey = fmt.Sprintf("bk_list_%d", i)
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	page, err := repo.List(ctx, p.RepoName, 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 3 {
		t.Errorf("expected 3 items, got %d", len(page.Items))
	}
	if page.ContinuationToken != nil {
		t.Error("expected no continuation token for page < limit")
	}
}

func TestAssetRepo_List_PaginationToken(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "list_page")
	repo := NewAssetRepo(pool)

	for i := 0; i < 5; i++ {
		a := makeAsset(p, fmt.Sprintf("/page/file%02d.bin", i))
		a.BlobKey = fmt.Sprintf("bk_page_%d", i)
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	page, err := repo.List(ctx, p.RepoName, 2, 0)
	if err != nil {
		t.Fatalf("List(limit=2): %v", err)
	}
	if len(page.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(page.Items))
	}
	if page.ContinuationToken == nil {
		t.Error("expected continuation token when more rows exist")
	}
	if *page.ContinuationToken != "2" {
		t.Errorf("continuation token: got %q want %q", *page.ContinuationToken, "2")
	}
}

func TestAssetRepo_List_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "list_empty")
	repo := NewAssetRepo(pool)

	page, err := repo.List(ctx, p.RepoName, 10, 0)
	if err != nil {
		t.Fatalf("List(empty): %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(page.Items))
	}
	if page.ContinuationToken != nil {
		t.Error("expected no token for empty result")
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestAssetRepo_Delete_RemovesAsset(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "del")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/delete/file.bin")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get after Delete: %v", err)
	}
	if got != nil {
		t.Fatal("Get after Delete should return nil")
	}
}

func TestAssetRepo_Delete_NonExistentIsNoOp(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()
	repo := NewAssetRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	// Delete of a non-existent row should not error (exec with 0 rows affected)
	if err := repo.Delete(ctx, missing); err != nil {
		t.Fatalf("Delete(non-existent): unexpected error: %v", err)
	}
}

// ── IncrementDownload ─────────────────────────────────────────────────────────

func TestAssetRepo_IncrementDownload_IncrementsCounter(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "inc_dl")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/download/file.bin")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.IncrementDownload(ctx, a.ID); err != nil {
		t.Fatalf("IncrementDownload: %v", err)
	}

	got, err := repo.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get after IncrementDownload: %v", err)
	}
	if got.DownloadCount != 1 {
		t.Errorf("DownloadCount: got %d want 1", got.DownloadCount)
	}
	if got.LastDownloaded == nil {
		t.Fatal("LastDownloaded should be set after IncrementDownload")
	}
}

func TestAssetRepo_IncrementDownload_MultipleIncrements(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "inc_multi")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/download/multi.bin")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	const n = 5
	for i := 0; i < n; i++ {
		if err := repo.IncrementDownload(ctx, a.ID); err != nil {
			t.Fatalf("IncrementDownload [%d]: %v", i, err)
		}
	}

	got, err := repo.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DownloadCount != n {
		t.Errorf("DownloadCount after %d increments: got %d", n, got.DownloadCount)
	}
}

func TestAssetRepo_IncrementDownload_SetsLastDownloaded(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "inc_ts")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/download/ts.bin")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	before := time.Now().Add(-time.Second)
	if err := repo.IncrementDownload(ctx, a.ID); err != nil {
		t.Fatalf("IncrementDownload: %v", err)
	}
	after := time.Now().Add(time.Second)

	got, err := repo.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastDownloaded == nil {
		t.Fatal("LastDownloaded is nil")
	}
	if got.LastDownloaded.Before(before) || got.LastDownloaded.After(after) {
		t.Errorf("LastDownloaded %v not within expected range [%v, %v]",
			got.LastDownloaded, before, after)
	}
}

func TestAssetRepo_IncrementDownload_AlsoUpdatesComponent(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "inc_comp")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/download/comp.bin")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.IncrementDownload(ctx, a.ID); err != nil {
		t.Fatalf("IncrementDownload: %v", err)
	}

	// Verify the component's download_count was also incremented
	cRepo := NewComponentRepo(pool)
	comp, err := cRepo.Get(ctx, p.ComponentID)
	if err != nil {
		t.Fatalf("component Get: %v", err)
	}
	if comp == nil {
		t.Fatal("component Get returned nil")
	}
	if comp.DownloadCount != 1 {
		t.Errorf("component DownloadCount: got %d want 1", comp.DownloadCount)
	}
	if comp.LastDownloaded == nil {
		t.Fatal("component LastDownloaded should be set")
	}
}

// ── SumSizeByRepo ─────────────────────────────────────────────────────────────

func TestAssetRepo_SumSizeByRepo_ReturnsCorrectSum(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "sum")
	repo := NewAssetRepo(pool)

	sizes := []int64{100, 200, 300}
	for i, sz := range sizes {
		a := makeAsset(p, fmt.Sprintf("/sum/file%d.bin", i))
		a.BlobKey = fmt.Sprintf("bk_sum_%d", i)
		a.SizeBytes = sz
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	total, err := repo.SumSizeByRepo(ctx, p.RepoName)
	if err != nil {
		t.Fatalf("SumSizeByRepo: %v", err)
	}
	var want int64
	for _, s := range sizes {
		want += s
	}
	if total != want {
		t.Errorf("SumSizeByRepo: got %d want %d", total, want)
	}
}

func TestAssetRepo_SumSizeByRepo_EmptyReturnsZero(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "sum_empty")
	repo := NewAssetRepo(pool)

	total, err := repo.SumSizeByRepo(ctx, p.RepoName)
	if err != nil {
		t.Fatalf("SumSizeByRepo(empty): %v", err)
	}
	if total != 0 {
		t.Errorf("SumSizeByRepo(empty): got %d want 0", total)
	}
}

// ── CountByBlobKey ────────────────────────────────────────────────────────────

func TestAssetRepo_CountByBlobKey_BlobKeyDedup(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "bkdedup")
	repo := NewAssetRepo(pool)

	const sharedBlobKey = "sha256:shared_blob_key_for_dedup_test"

	// Create two assets sharing the same blob_key (different paths)
	a1 := makeAsset(p, "/dedup/file1.bin")
	a1.BlobKey = sharedBlobKey
	if err := repo.Create(ctx, a1); err != nil {
		t.Fatalf("Create a1: %v", err)
	}

	a2 := makeAsset(p, "/dedup/file2.bin")
	a2.BlobKey = sharedBlobKey
	if err := repo.Create(ctx, a2); err != nil {
		t.Fatalf("Create a2: %v", err)
	}

	// Verify both rows exist
	got1, err := repo.Get(ctx, a1.ID)
	if err != nil || got1 == nil {
		t.Fatalf("Get a1: err=%v got=%v", err, got1)
	}
	got2, err := repo.Get(ctx, a2.ID)
	if err != nil || got2 == nil {
		t.Fatalf("Get a2: err=%v got=%v", err, got2)
	}
	if got1.BlobKey != sharedBlobKey || got2.BlobKey != sharedBlobKey {
		t.Errorf("BlobKey not shared: a1=%q a2=%q", got1.BlobKey, got2.BlobKey)
	}

	// CountByBlobKey(a1.ID): excludes a1, so count=1 (a2 still has it)
	count, err := repo.CountByBlobKey(ctx, sharedBlobKey, a1.ID)
	if err != nil {
		t.Fatalf("CountByBlobKey: %v", err)
	}
	if count != 1 {
		t.Errorf("CountByBlobKey(exclude a1): got %d want 1", count)
	}

	// CountByBlobKey(a2.ID): excludes a2, so count=1 (a1 still has it)
	count, err = repo.CountByBlobKey(ctx, sharedBlobKey, a2.ID)
	if err != nil {
		t.Fatalf("CountByBlobKey: %v", err)
	}
	if count != 1 {
		t.Errorf("CountByBlobKey(exclude a2): got %d want 1", count)
	}
}

func TestAssetRepo_CountByBlobKey_UniqueKeyIsZeroAfterExcludeSelf(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "bkcount_zero")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/unique/file.bin")
	a.BlobKey = "unique_blob_key_xyz"
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Excluding the only owner → count should be 0
	count, err := repo.CountByBlobKey(ctx, "unique_blob_key_xyz", a.ID)
	if err != nil {
		t.Fatalf("CountByBlobKey: %v", err)
	}
	if count != 0 {
		t.Errorf("CountByBlobKey: got %d want 0", count)
	}
}
