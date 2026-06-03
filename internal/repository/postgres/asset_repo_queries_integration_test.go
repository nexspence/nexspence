//go:build integration

package postgres

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── globToLike (unit-style, no DB) ────────────────────────────────────────────

func TestAssetRepoQueries_GlobToLike(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"*", "%"},
		{"?", "_"},
		{"foo*", "foo%"},
		{"foo?bar", "foo_bar"},
		{"*.jar", "%.jar"},
		{"foo%bar", `foo\%bar`},  // % escaped
		{"foo_bar", `foo\_bar`},  // _ escaped
		{"foo%_*?", `foo\%\_%_`}, // combined escaping + glob
		{"literal", "literal"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := globToLike(tc.input)
			if got != tc.want {
				t.Errorf("globToLike(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ── SearchAssets ──────────────────────────────────────────────────────────────

func TestAssetRepoQueries_SearchAssets_ByRepositoryName(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "sa_repo")
	repo := NewAssetRepo(pool)

	// Insert 3 assets in p's repo
	for i := 0; i < 3; i++ {
		a := makeAsset(p, fmt.Sprintf("/sa/file%02d.bin", i))
		a.BlobKey = fmt.Sprintf("bk_sa_%d", i)
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	// Create a second parent/repo — assets here should NOT appear
	p2 := makeAssetParent(t, ctx, "sa_repo2")
	a2 := makeAsset(p2, "/other/file.bin")
	if err := repo.Create(ctx, a2); err != nil {
		t.Fatalf("Create p2 asset: %v", err)
	}

	page, err := repo.SearchAssets(ctx, domain.SearchParams{
		Repository: p.RepoName,
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("SearchAssets: %v", err)
	}
	if len(page.Items) != 3 {
		t.Errorf("expected 3 assets, got %d", len(page.Items))
	}
	for _, item := range page.Items {
		if item.Repository != p.RepoName {
			t.Errorf("unexpected repo %q in results", item.Repository)
		}
	}
}

func TestAssetRepoQueries_SearchAssets_ByRepositoryNames(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p1 := makeAssetParent(t, ctx, "sa_names1")
	p2 := makeAssetParent(t, ctx, "sa_names2")
	p3 := makeAssetParent(t, ctx, "sa_names3")
	repo := NewAssetRepo(pool)

	for _, p := range []assetParent{p1, p2, p3} {
		a := makeAsset(p, "/file.bin")
		a.BlobKey = "bk_" + p.RepoName
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	// Search p1 and p2 but not p3
	page, err := repo.SearchAssets(ctx, domain.SearchParams{
		RepositoryNames: []string{p1.RepoName, p2.RepoName},
		Limit:           50,
	})
	if err != nil {
		t.Fatalf("SearchAssets: %v", err)
	}
	if len(page.Items) != 2 {
		t.Errorf("expected 2 assets, got %d", len(page.Items))
	}
	for _, item := range page.Items {
		if item.Repository == p3.RepoName {
			t.Error("p3 repo should not appear in results")
		}
	}
}

func TestAssetRepoQueries_SearchAssets_ByNameFilter(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "sa_name")
	repo := NewAssetRepo(pool)

	paths := []string{"/mylib/foo-1.0.jar", "/mylib/foo-2.0.jar", "/mylib/bar-1.0.jar"}
	for _, path := range paths {
		a := makeAsset(p, path)
		a.BlobKey = "bk" + path
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	page, err := repo.SearchAssets(ctx, domain.SearchParams{
		Repository: p.RepoName,
		Name:       "foo",
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("SearchAssets: %v", err)
	}
	if len(page.Items) != 2 {
		t.Errorf("expected 2 assets matching 'foo', got %d", len(page.Items))
	}
}

func TestAssetRepoQueries_SearchAssets_Pagination(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "sa_page")
	repo := NewAssetRepo(pool)

	for i := 0; i < 5; i++ {
		a := makeAsset(p, fmt.Sprintf("/page/file%02d.bin", i))
		a.BlobKey = fmt.Sprintf("bk_page_%d", i)
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	// Page 1
	page1, err := repo.SearchAssets(ctx, domain.SearchParams{Repository: p.RepoName, Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("SearchAssets page1: %v", err)
	}
	if len(page1.Items) != 2 {
		t.Errorf("page1: expected 2 items, got %d", len(page1.Items))
	}
	if page1.ContinuationToken == nil {
		t.Error("page1: expected continuation token")
	}
	if *page1.ContinuationToken != "2" {
		t.Errorf("page1: continuation token = %q, want %q", *page1.ContinuationToken, "2")
	}

	// Page 2 (offset=2)
	page2, err := repo.SearchAssets(ctx, domain.SearchParams{Repository: p.RepoName, Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("SearchAssets page2: %v", err)
	}
	if len(page2.Items) != 2 {
		t.Errorf("page2: expected 2 items, got %d", len(page2.Items))
	}

	// Last page (offset=4)
	page3, err := repo.SearchAssets(ctx, domain.SearchParams{Repository: p.RepoName, Limit: 2, Offset: 4})
	if err != nil {
		t.Fatalf("SearchAssets page3: %v", err)
	}
	if len(page3.Items) != 1 {
		t.Errorf("page3: expected 1 item, got %d", len(page3.Items))
	}
	if page3.ContinuationToken != nil {
		t.Error("page3: expected no continuation token on last page")
	}
}

func TestAssetRepoQueries_SearchAssets_BySHA256(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "sa_sha")
	repo := NewAssetRepo(pool)

	target := "aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff6666aaaa1111bbbb2222"
	a := makeAsset(p, "/sha/target.bin")
	a.BlobKey = "bk_sha_target"
	a.SHA256 = target
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	a2 := makeAsset(p, "/sha/other.bin")
	a2.BlobKey = "bk_sha_other"
	a2.SHA256 = "bbbb2222cccc3333dddd4444eeee5555ffff6666aaaa1111bbbb2222cccc3333"
	if err := repo.Create(ctx, a2); err != nil {
		t.Fatalf("Create other: %v", err)
	}

	page, err := repo.SearchAssets(ctx, domain.SearchParams{
		Repository: p.RepoName,
		SHA256:     target,
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("SearchAssets: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected 1 asset by SHA256, got %d", len(page.Items))
	}
	if page.Items[0].SHA256 != target {
		t.Errorf("wrong asset returned: SHA256=%q", page.Items[0].SHA256)
	}
}

func TestAssetRepoQueries_SearchAssets_EmptyResult(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "sa_empty")
	repo := NewAssetRepo(pool)

	page, err := repo.SearchAssets(ctx, domain.SearchParams{
		Repository: p.RepoName,
		Limit:      50,
	})
	if err != nil {
		t.Fatalf("SearchAssets: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(page.Items))
	}
	if page.ContinuationToken != nil {
		t.Error("expected nil continuation token for empty result")
	}
}

// ── ListAllBlobKeys (GC) ──────────────────────────────────────────────────────

func TestAssetRepoQueries_ListAllBlobKeys_ReturnsDistinctKeys(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "bk_gc")
	repo := NewAssetRepo(pool)

	const sharedKey = "sha256:shared_gc_key"
	// Two assets share one blob key; a third has a unique key.
	for i, key := range []string{sharedKey, sharedKey, "sha256:unique_gc_key"} {
		a := makeAsset(p, fmt.Sprintf("/gc/file%d.bin", i))
		a.BlobKey = key
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	keys, err := repo.ListAllBlobKeys(ctx)
	if err != nil {
		t.Fatalf("ListAllBlobKeys: %v", err)
	}
	// DISTINCT should give us exactly 2 keys
	if len(keys) != 2 {
		t.Errorf("expected 2 distinct keys, got %d: %v", len(keys), keys)
	}
	keySet := make(map[string]bool)
	for _, k := range keys {
		keySet[k] = true
	}
	if !keySet[sharedKey] {
		t.Errorf("shared key %q not in results", sharedKey)
	}
	if !keySet["sha256:unique_gc_key"] {
		t.Error("unique_gc_key not in results")
	}
}

func TestAssetRepoQueries_ListAllBlobKeys_SkipsEmptyAndNullKeys(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "bk_empty")
	repo := NewAssetRepo(pool)

	// Asset with a real key
	a := makeAsset(p, "/gc/realkey.bin")
	a.BlobKey = "sha256:realkey"
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Asset with empty blob key — stored as empty string (not NULL via nullStr, but blobKey is not nullable in the domain)
	a2 := makeAsset(p, "/gc/nokey.bin")
	a2.BlobKey = "" // empty string
	if err := repo.Create(ctx, a2); err != nil {
		t.Fatalf("Create no-key: %v", err)
	}

	keys, err := repo.ListAllBlobKeys(ctx)
	if err != nil {
		t.Fatalf("ListAllBlobKeys: %v", err)
	}
	// Only the real key should appear; empty string excluded by WHERE TRIM <> ''
	for _, k := range keys {
		if k == "" {
			t.Error("empty blob key should not be returned")
		}
	}
	if len(keys) != 1 || keys[0] != "sha256:realkey" {
		t.Errorf("expected [sha256:realkey], got %v", keys)
	}
}

// ── ListPathsByRepo ───────────────────────────────────────────────────────────

func TestAssetRepoQueries_ListPathsByRepo_ReturnsDirPrefixes(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "lp_repo")
	repo := NewAssetRepo(pool)

	// /da/devops/foo.jar → should yield /da/ and /da/devops/
	paths := []string{
		"/da/devops/foo.jar",
		"/da/devops/bar.jar",
		"/releases/v1/artifact.zip",
	}
	for _, path := range paths {
		a := makeAsset(p, path)
		a.BlobKey = "bk" + path
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create %q: %v", path, err)
		}
	}

	dirs, err := repo.ListPathsByRepo(ctx, p.RepoName, "")
	if err != nil {
		t.Fatalf("ListPathsByRepo: %v", err)
	}

	expected := []string{"/da/", "/da/devops/", "/releases/", "/releases/v1/"}
	sort.Strings(dirs)
	sort.Strings(expected)

	if len(dirs) != len(expected) {
		t.Errorf("expected dirs %v, got %v", expected, dirs)
	} else {
		for i := range expected {
			if dirs[i] != expected[i] {
				t.Errorf("dirs[%d]: got %q want %q", i, dirs[i], expected[i])
			}
		}
	}
}

func TestAssetRepoQueries_ListPathsByRepo_QueryFilter(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "lp_filter")
	repo := NewAssetRepo(pool)

	paths := []string{
		"/alpha/x.bin",
		"/beta/y.bin",
	}
	for _, path := range paths {
		a := makeAsset(p, path)
		a.BlobKey = "bk" + path
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create %q: %v", path, err)
		}
	}

	dirs, err := repo.ListPathsByRepo(ctx, p.RepoName, "alpha")
	if err != nil {
		t.Fatalf("ListPathsByRepo: %v", err)
	}
	if len(dirs) != 1 {
		t.Errorf("expected 1 dir for filter 'alpha', got %v", dirs)
	} else if dirs[0] != "/alpha/" {
		t.Errorf("expected /alpha/, got %q", dirs[0])
	}
}

func TestAssetRepoQueries_ListPathsByRepo_EmptyRepoReturnsEmpty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "lp_empty")
	repo := NewAssetRepo(pool)

	dirs, err := repo.ListPathsByRepo(ctx, p.RepoName, "")
	if err != nil {
		t.Fatalf("ListPathsByRepo: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("expected 0 dirs, got %v", dirs)
	}
}

// ── ListRawAssetPaths ─────────────────────────────────────────────────────────

func TestAssetRepoQueries_ListRawAssetPaths_ReturnsAllPaths(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "rap")
	repo := NewAssetRepo(pool)

	want := []string{"/a/file1.bin", "/a/file2.bin", "/b/file3.bin"}
	for _, path := range want {
		a := makeAsset(p, path)
		a.BlobKey = "bk" + path
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create %q: %v", path, err)
		}
	}

	got, err := repo.ListRawAssetPaths(ctx, p.RepoName)
	if err != nil {
		t.Fatalf("ListRawAssetPaths: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d paths, got %d: %v", len(want), len(got), got)
	}
	// Results are ordered by path
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("path[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestAssetRepoQueries_ListRawAssetPaths_EmptyRepo(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "rap_empty")
	repo := NewAssetRepo(pool)

	got, err := repo.ListRawAssetPaths(ctx, p.RepoName)
	if err != nil {
		t.Fatalf("ListRawAssetPaths: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 paths, got %v", got)
	}
}

// ── ListRawBrowseAssets ───────────────────────────────────────────────────────

func TestAssetRepoQueries_ListRawBrowseAssets_ReturnsRawFormatOnly(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	// makeAssetParent creates a "raw" format repo — this is the correct format
	p := makeAssetParent(t, ctx, "rba_raw")
	repo := NewAssetRepo(pool)

	a := makeAsset(p, "/raw/file.bin")
	a.BlobKey = "bk_rba_raw"
	a.SizeBytes = 512
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rows, err := repo.ListRawBrowseAssets(ctx, []string{p.RepoName})
	if err != nil {
		t.Fatalf("ListRawBrowseAssets: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 raw browse asset, got %d", len(rows))
	}
	if rows[0].Path != "/raw/file.bin" {
		t.Errorf("path: got %q want %q", rows[0].Path, "/raw/file.bin")
	}
	if rows[0].SizeBytes != 512 {
		t.Errorf("size: got %d want 512", rows[0].SizeBytes)
	}
	if rows[0].RepoName != p.RepoName {
		t.Errorf("repoName: got %q want %q", rows[0].RepoName, p.RepoName)
	}
	if rows[0].ComponentID == "" {
		t.Error("ComponentID should not be empty")
	}
}

func TestAssetRepoQueries_ListRawBrowseAssets_MultipleRepos(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p1 := makeAssetParent(t, ctx, "rba_multi1")
	p2 := makeAssetParent(t, ctx, "rba_multi2")
	repo := NewAssetRepo(pool)

	a1 := makeAsset(p1, "/file1.bin")
	a1.BlobKey = "bk_rba_1"
	if err := repo.Create(ctx, a1); err != nil {
		t.Fatalf("Create p1: %v", err)
	}
	a2 := makeAsset(p2, "/file2.bin")
	a2.BlobKey = "bk_rba_2"
	if err := repo.Create(ctx, a2); err != nil {
		t.Fatalf("Create p2: %v", err)
	}

	rows, err := repo.ListRawBrowseAssets(ctx, []string{p1.RepoName, p2.RepoName})
	if err != nil {
		t.Fatalf("ListRawBrowseAssets: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows))
	}
}

func TestAssetRepoQueries_ListRawBrowseAssets_EmptySliceReturnsEmpty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()
	repo := NewAssetRepo(pool)

	rows, err := repo.ListRawBrowseAssets(ctx, []string{})
	if err != nil {
		t.Fatalf("ListRawBrowseAssets(empty): %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

// ── ListByRepoAndPath ─────────────────────────────────────────────────────────

func TestAssetRepoQueries_ListByRepoAndPath_NoPrefix(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "lbrap_all")
	repo := NewAssetRepo(pool)

	paths := []string{"/a/file1.bin", "/b/file2.bin", "/c/file3.bin"}
	for _, path := range paths {
		a := makeAsset(p, path)
		a.BlobKey = "bk" + path
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create %q: %v", path, err)
		}
	}

	// Empty prefix = return all assets for repo
	assets, err := repo.ListByRepoAndPath(ctx, p.RepoName, "")
	if err != nil {
		t.Fatalf("ListByRepoAndPath: %v", err)
	}
	if len(assets) != 3 {
		t.Errorf("expected 3 assets, got %d", len(assets))
	}
}

func TestAssetRepoQueries_ListByRepoAndPath_WithPrefix(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "lbrap_prefix")
	repo := NewAssetRepo(pool)

	paths := []string{"/prefix/file1.bin", "/prefix/file2.bin", "/other/file3.bin"}
	for _, path := range paths {
		a := makeAsset(p, path)
		a.BlobKey = "bk" + path
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create %q: %v", path, err)
		}
	}

	assets, err := repo.ListByRepoAndPath(ctx, p.RepoName, "/prefix")
	if err != nil {
		t.Fatalf("ListByRepoAndPath: %v", err)
	}
	if len(assets) != 2 {
		t.Errorf("expected 2 assets under /prefix, got %d", len(assets))
	}
	for _, a := range assets {
		if len(a.Path) < 7 || a.Path[:7] != "/prefix" {
			t.Errorf("unexpected path %q returned for /prefix filter", a.Path)
		}
	}
}

func TestAssetRepoQueries_ListByRepoAndPath_SortedByPath(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "lbrap_sort")
	repo := NewAssetRepo(pool)

	// Insert out of order
	for _, path := range []string{"/z/file.bin", "/a/file.bin", "/m/file.bin"} {
		a := makeAsset(p, path)
		a.BlobKey = "bk" + path
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create %q: %v", path, err)
		}
	}

	assets, err := repo.ListByRepoAndPath(ctx, p.RepoName, "")
	if err != nil {
		t.Fatalf("ListByRepoAndPath: %v", err)
	}
	if len(assets) != 3 {
		t.Fatalf("expected 3 assets, got %d", len(assets))
	}
	if assets[0].Path != "/a/file.bin" || assets[1].Path != "/m/file.bin" || assets[2].Path != "/z/file.bin" {
		t.Errorf("wrong order: %v", []string{assets[0].Path, assets[1].Path, assets[2].Path})
	}
}

// ── ListForBlobStoreMigration + UpdateBlobStoreForBlobKey ──────────────────────

func TestAssetRepoQueries_ListForBlobStoreMigration_ReturnsMismatchedRows(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	// p has a source blob store
	p := makeAssetParent(t, ctx, "bsmig_src")
	repo := NewAssetRepo(pool)

	// Create a second blob store (target)
	bsRepo := NewBlobStoreRepo(pool)
	targetBS := &domain.BlobStore{
		Name:   "bsmig_target_bs",
		Type:   "local",
		Config: map[string]any{"path": "/data/target"},
	}
	if err := bsRepo.Create(ctx, targetBS); err != nil {
		t.Fatalf("Create target blob store: %v", err)
	}

	// Two assets on source store
	a1 := makeAsset(p, "/mig/file1.bin")
	a1.BlobKey = "bk_mig_1"
	a1.SizeBytes = 100
	if err := repo.Create(ctx, a1); err != nil {
		t.Fatalf("Create a1: %v", err)
	}
	a2 := makeAsset(p, "/mig/file2.bin")
	a2.BlobKey = "bk_mig_2"
	a2.SizeBytes = 200
	if err := repo.Create(ctx, a2); err != nil {
		t.Fatalf("Create a2: %v", err)
	}

	// List assets that need migration to targetBS
	rows, err := repo.ListForBlobStoreMigration(ctx, p.RepoName, targetBS.ID)
	if err != nil {
		t.Fatalf("ListForBlobStoreMigration: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 migration rows, got %d", len(rows))
	}
	keySet := make(map[string]bool)
	for _, r := range rows {
		keySet[r.BlobKey] = true
		if r.SourceBlobStoreID == "" {
			t.Error("SourceBlobStoreID should not be empty")
		}
	}
	if !keySet["bk_mig_1"] || !keySet["bk_mig_2"] {
		t.Errorf("expected bk_mig_1 and bk_mig_2, got %v", rows)
	}
}

func TestAssetRepoQueries_ListForBlobStoreMigration_AlreadyOnTargetExcluded(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "bsmig_same")
	repo := NewAssetRepo(pool)

	// The asset's blob_store_id == p.BlobStoreID; query with targetStoreID = p.BlobStoreID
	a := makeAsset(p, "/mig/same.bin")
	a.BlobKey = "bk_same"
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rows, err := repo.ListForBlobStoreMigration(ctx, p.RepoName, p.BlobStoreID)
	if err != nil {
		t.Fatalf("ListForBlobStoreMigration: %v", err)
	}
	// asset already on target store, should not appear
	if len(rows) != 0 {
		t.Errorf("expected 0 rows when asset is already on target store, got %d", len(rows))
	}
}

func TestAssetRepoQueries_UpdateBlobStoreForBlobKey_UpdatesRows(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "ubsfbk")
	repo := NewAssetRepo(pool)

	// Create target blob store
	bsRepo := NewBlobStoreRepo(pool)
	newBS := &domain.BlobStore{
		Name:   "ubsfbk_new_bs",
		Type:   "local",
		Config: map[string]any{"path": "/data/new"},
	}
	if err := bsRepo.Create(ctx, newBS); err != nil {
		t.Fatalf("Create new blob store: %v", err)
	}

	// Create asset on original blob store
	a := makeAsset(p, "/migrate/file.bin")
	a.BlobKey = "bk_migrate_me"
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify original blob store
	got, err := repo.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BlobStoreID != p.BlobStoreID {
		t.Errorf("before migration: BlobStoreID = %q, want %q", got.BlobStoreID, p.BlobStoreID)
	}

	// Update the blob_store_id
	if err := repo.UpdateBlobStoreForBlobKey(ctx, "bk_migrate_me", p.RepoName, newBS.ID); err != nil {
		t.Fatalf("UpdateBlobStoreForBlobKey: %v", err)
	}

	// Verify it moved
	got2, err := repo.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get after migration: %v", err)
	}
	if got2.BlobStoreID != newBS.ID {
		t.Errorf("after migration: BlobStoreID = %q, want %q", got2.BlobStoreID, newBS.ID)
	}
}

// ── ListStale ──────────────────────────────────────────────────────────────────

// makeStaleParent creates a blob_store + repository + multiple components with
// distinct versions, all under the same name, to test the retain-N CTE logic.
// Returns the assetParent for the first component and the repoName.
func makeStaleParentMultiVersion(t *testing.T, ctx context.Context, suffix string, versions []string) (p assetParent, extraCompIDs []string) {
	t.Helper()
	pool := pgtest.Pool(t)

	bsRepo := NewBlobStoreRepo(pool)
	bs := &domain.BlobStore{
		Name:   "stale_bs_" + suffix,
		Type:   "local",
		Config: map[string]any{"path": "/data/stale_" + suffix},
	}
	if err := bsRepo.Create(ctx, bs); err != nil {
		t.Fatalf("makeStaleParent: blob store: %v", err)
	}

	rRepo := NewRepositoryRepo(pool)
	repoName := "stale_repo_" + suffix
	r := &domain.Repository{
		Name:        repoName,
		Format:      domain.FormatMaven2,
		Type:        domain.TypeHosted,
		BlobStoreID: &bs.ID,
		Online:      true,
	}
	if err := rRepo.Create(ctx, r); err != nil {
		t.Fatalf("makeStaleParent: repository: %v", err)
	}

	cRepo := NewComponentRepo(pool)
	var firstCompID string
	for i, ver := range versions {
		comp := &domain.Component{
			RepositoryID: r.ID,
			Format:       "maven2",
			Group:        "com.example",
			Name:         "mylib_" + suffix,
			Version:      ver,
		}
		if err := cRepo.Create(ctx, comp); err != nil {
			t.Fatalf("makeStaleParent: component %q: %v", ver, err)
		}
		if i == 0 {
			firstCompID = comp.ID
		} else {
			extraCompIDs = append(extraCompIDs, comp.ID)
		}
	}

	p = assetParent{
		BlobStoreID:  bs.ID,
		RepositoryID: r.ID,
		ComponentID:  firstCompID,
		RepoName:     repoName,
	}
	return p, extraCompIDs
}

func TestAssetRepoQueries_ListStale_RetainNVersions_Zero_AllCandidates(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	versions := []string{"1.0", "2.0", "3.0", "4.0"}
	p, extraCompIDs := makeStaleParentMultiVersion(t, ctx, "retain0", versions)
	repo := NewAssetRepo(pool)

	allCompIDs := append([]string{p.ComponentID}, extraCompIDs...)
	for i, compID := range allCompIDs {
		a := &domain.Asset{
			ComponentID:  compID,
			RepositoryID: p.RepositoryID,
			Path:         fmt.Sprintf("/com/example/mylib/retain0-%d/mylib.jar", i),
			BlobStoreID:  p.BlobStoreID,
			BlobKey:      fmt.Sprintf("bk_retain0_%d", i),
			SizeBytes:    1024,
			ContentType:  "application/java-archive",
		}
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create asset for comp %d: %v", i, err)
		}
	}

	// retainNVersions=0 → no retention exclusion → all 4 are candidates
	assets, err := repo.ListStale(ctx, "maven2", []string{p.RepoName}, 0, 0, "", "", 0, 100)
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	if len(assets) != 4 {
		t.Errorf("retainNVersions=0: expected 4 stale candidates, got %d", len(assets))
	}
}

func TestAssetRepoQueries_ListStale_RetainNVersions_Two_ExcludesNewest(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	// 4 versions of the same artifact — retain the 2 newest → 2 stale remain
	versions := []string{"1.0", "2.0", "3.0", "4.0"}
	p, extraCompIDs := makeStaleParentMultiVersion(t, ctx, "retain2", versions)
	repo := NewAssetRepo(pool)

	allCompIDs := append([]string{p.ComponentID}, extraCompIDs...)
	for i, compID := range allCompIDs {
		a := &domain.Asset{
			ComponentID:  compID,
			RepositoryID: p.RepositoryID,
			Path:         fmt.Sprintf("/com/example/mylib/retain2-%d/mylib.jar", i),
			BlobStoreID:  p.BlobStoreID,
			BlobKey:      fmt.Sprintf("bk_retain2_%d", i),
			SizeBytes:    1024,
			ContentType:  "application/java-archive",
		}
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create asset for comp %d: %v", i, err)
		}
	}

	// retainNVersions=2 → 2 newest excluded by CTE → 2 older ones are stale
	assets, err := repo.ListStale(ctx, "maven2", []string{p.RepoName}, 0, 0, "", "", 2, 100)
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	if len(assets) != 2 {
		t.Errorf("retainNVersions=2: expected 2 stale candidates, got %d", len(assets))
	}
}

func TestAssetRepoQueries_ListStale_RetainNVersions_ExceedsCount_AllProtected(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	// Only 2 versions exist; retain 10 → nothing is stale
	versions := []string{"1.0", "2.0"}
	p, extraCompIDs := makeStaleParentMultiVersion(t, ctx, "retain10", versions)
	repo := NewAssetRepo(pool)

	allCompIDs := append([]string{p.ComponentID}, extraCompIDs...)
	for i, compID := range allCompIDs {
		a := &domain.Asset{
			ComponentID:  compID,
			RepositoryID: p.RepositoryID,
			Path:         fmt.Sprintf("/com/example/mylib/retain10-%d/mylib.jar", i),
			BlobStoreID:  p.BlobStoreID,
			BlobKey:      fmt.Sprintf("bk_retain10_%d", i),
			SizeBytes:    1024,
			ContentType:  "application/java-archive",
		}
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create asset for comp %d: %v", i, err)
		}
	}

	assets, err := repo.ListStale(ctx, "maven2", []string{p.RepoName}, 0, 0, "", "", 10, 100)
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	if len(assets) != 0 {
		t.Errorf("retainNVersions=10 with only 2 versions: expected 0 stale, got %d", len(assets))
	}
}

func TestAssetRepoQueries_ListStale_FormatFilter(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p, _ := makeStaleParentMultiVersion(t, ctx, "stale_fmt", []string{"1.0"})
	repo := NewAssetRepo(pool)

	a := &domain.Asset{
		ComponentID:  p.ComponentID,
		RepositoryID: p.RepositoryID,
		Path:         "/com/example/mylib/1.0/mylib.jar",
		BlobStoreID:  p.BlobStoreID,
		BlobKey:      "bk_stale_fmt",
		SizeBytes:    1024,
		ContentType:  "application/java-archive",
	}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// format="npm" should not match maven2 component
	assets, err := repo.ListStale(ctx, "npm", []string{p.RepoName}, 0, 0, "", "", 0, 100)
	if err != nil {
		t.Fatalf("ListStale npm: %v", err)
	}
	if len(assets) != 0 {
		t.Errorf("format filter 'npm': expected 0, got %d", len(assets))
	}

	// format="maven2" should match
	assets, err = repo.ListStale(ctx, "maven2", []string{p.RepoName}, 0, 0, "", "", 0, 100)
	if err != nil {
		t.Fatalf("ListStale maven2: %v", err)
	}
	if len(assets) != 1 {
		t.Errorf("format filter 'maven2': expected 1, got %d", len(assets))
	}
}

func TestAssetRepoQueries_ListStale_PathPrefixFilter(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p, extraCompIDs := makeStaleParentMultiVersion(t, ctx, "stale_pfx", []string{"1.0", "2.0"})
	repo := NewAssetRepo(pool)

	allCompIDs := []string{p.ComponentID, extraCompIDs[0]}
	paths := []string{"/com/example/mylib/1.0/mylib.jar", "/other/mylib/2.0/mylib.jar"}
	for i, compID := range allCompIDs {
		a := &domain.Asset{
			ComponentID:  compID,
			RepositoryID: p.RepositoryID,
			Path:         paths[i],
			BlobStoreID:  p.BlobStoreID,
			BlobKey:      fmt.Sprintf("bk_pfx_%d", i),
			SizeBytes:    1024,
			ContentType:  "application/java-archive",
		}
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	assets, err := repo.ListStale(ctx, "maven2", []string{p.RepoName}, 0, 0, "/com/", "", 0, 100)
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	if len(assets) != 1 {
		t.Errorf("pathPrefix=/com/: expected 1 stale asset, got %d", len(assets))
	}
	if assets[0].Path != "/com/example/mylib/1.0/mylib.jar" {
		t.Errorf("wrong asset: %q", assets[0].Path)
	}
}

func TestAssetRepoQueries_ListStale_NameGlobFilter(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p, extraCompIDs := makeStaleParentMultiVersion(t, ctx, "stale_glob", []string{"1.0", "2.0"})
	repo := NewAssetRepo(pool)

	allCompIDs := []string{p.ComponentID, extraCompIDs[0]}
	// nameGlob is applied as a LIKE against the full asset path.
	// Use paths that start with a distinguishing prefix so the glob anchors correctly.
	paths := []string{"mylib-stale_glob-1.0.jar", "otherlib-stale_glob-2.0.jar"}
	for i, compID := range allCompIDs {
		a := &domain.Asset{
			ComponentID:  compID,
			RepositoryID: p.RepositoryID,
			Path:         paths[i],
			BlobStoreID:  p.BlobStoreID,
			BlobKey:      fmt.Sprintf("bk_glob_%d", i),
			SizeBytes:    1024,
			ContentType:  "application/java-archive",
		}
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	// glob "mylib*" → LIKE "mylib%" matches "mylib-stale_glob-1.0.jar" only
	assets, err := repo.ListStale(ctx, "maven2", []string{p.RepoName}, 0, 0, "", "mylib*", 0, 100)
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	if len(assets) != 1 {
		t.Errorf("nameGlob='mylib*': expected 1, got %d", len(assets))
	}

	// glob "*.jar" → LIKE "%.jar" matches both
	assets, err = repo.ListStale(ctx, "maven2", []string{p.RepoName}, 0, 0, "", "*.jar", 0, 100)
	if err != nil {
		t.Fatalf("ListStale *.jar: %v", err)
	}
	if len(assets) != 2 {
		t.Errorf("nameGlob='*.jar': expected 2, got %d", len(assets))
	}
}

func TestAssetRepoQueries_ListStale_LimitRespected(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	versions := []string{"1.0", "2.0", "3.0", "4.0", "5.0"}
	p, extraCompIDs := makeStaleParentMultiVersion(t, ctx, "stale_lim", versions)
	repo := NewAssetRepo(pool)

	allCompIDs := append([]string{p.ComponentID}, extraCompIDs...)
	for i, compID := range allCompIDs {
		a := &domain.Asset{
			ComponentID:  compID,
			RepositoryID: p.RepositoryID,
			Path:         fmt.Sprintf("/lib/stale_lim-%d.jar", i),
			BlobStoreID:  p.BlobStoreID,
			BlobKey:      fmt.Sprintf("bk_lim_%d", i),
			SizeBytes:    1024,
			ContentType:  "application/java-archive",
		}
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	assets, err := repo.ListStale(ctx, "maven2", []string{p.RepoName}, 0, 0, "", "", 0, 3)
	if err != nil {
		t.Fatalf("ListStale: %v", err)
	}
	if len(assets) != 3 {
		t.Errorf("limit=3: expected 3 assets, got %d", len(assets))
	}
}

func TestAssetRepoQueries_ListStale_DefaultLimitOnZero(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p, _ := makeStaleParentMultiVersion(t, ctx, "stale_deflim", []string{"1.0"})
	repo := NewAssetRepo(pool)

	a := &domain.Asset{
		ComponentID:  p.ComponentID,
		RepositoryID: p.RepositoryID,
		Path:         "/lib/deflim.jar",
		BlobStoreID:  p.BlobStoreID,
		BlobKey:      "bk_deflim",
		SizeBytes:    1024,
		ContentType:  "application/java-archive",
	}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// limit=0 → code defaults to 500
	assets, err := repo.ListStale(ctx, "maven2", []string{p.RepoName}, 0, 0, "", "", 0, 0)
	if err != nil {
		t.Fatalf("ListStale limit=0: %v", err)
	}
	if len(assets) != 1 {
		t.Errorf("expected 1 asset (default limit=500), got %d", len(assets))
	}
}

func TestAssetRepoQueries_ListStale_WildcardFormat(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p, _ := makeStaleParentMultiVersion(t, ctx, "stale_wild", []string{"1.0"})
	repo := NewAssetRepo(pool)

	a := &domain.Asset{
		ComponentID:  p.ComponentID,
		RepositoryID: p.RepositoryID,
		Path:         "/lib/wild.jar",
		BlobStoreID:  p.BlobStoreID,
		BlobKey:      "bk_wild",
		SizeBytes:    1024,
		ContentType:  "application/java-archive",
	}
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// format="*" = wildcard (all formats)
	assets, err := repo.ListStale(ctx, "*", []string{p.RepoName}, 0, 0, "", "", 0, 100)
	if err != nil {
		t.Fatalf("ListStale *: %v", err)
	}
	if len(assets) != 1 {
		t.Errorf("wildcard format: expected 1 asset, got %d", len(assets))
	}
}

func TestAssetRepoQueries_ListStale_EmptyRepoNames_ReturnsEmpty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()
	repo := NewAssetRepo(pool)

	// No repo names provided → WHERE clause has no repo filter → empty result
	assets, err := repo.ListStale(ctx, "*", []string{}, 0, 0, "", "", 0, 100)
	if err != nil {
		t.Fatalf("ListStale empty repos: %v", err)
	}
	if len(assets) != 0 {
		t.Errorf("empty repoNames: expected 0 assets, got %d", len(assets))
	}
}
