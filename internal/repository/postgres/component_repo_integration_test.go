//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── parent-chain helper (blob_store + repository only, no component) ──────────

type compParent struct {
	BlobStoreID  string
	RepositoryID string
	RepoName     string
}

// makeCompParent creates blob_store → repository rows.
// suffix must be unique within the test run.
func makeCompParent(t *testing.T, ctx context.Context, suffix string) compParent {
	t.Helper()
	pool := pgtest.Pool(t)

	bsRepo := NewBlobStoreRepo(pool)
	bs := &domain.BlobStore{
		Name:   "comp_bs_" + suffix,
		Type:   "local",
		Config: map[string]any{"path": "/data/comp_" + suffix},
	}
	if err := bsRepo.Create(ctx, bs); err != nil {
		t.Fatalf("makeCompParent: blob store: %v", err)
	}

	rRepo := NewRepositoryRepo(pool)
	repoName := "comp_repo_" + suffix
	r := &domain.Repository{
		Name:        repoName,
		Format:      domain.FormatRaw,
		Type:        domain.TypeHosted,
		BlobStoreID: &bs.ID,
		Online:      true,
	}
	if err := rRepo.Create(ctx, r); err != nil {
		t.Fatalf("makeCompParent: repository: %v", err)
	}

	return compParent{
		BlobStoreID:  bs.ID,
		RepositoryID: r.ID,
		RepoName:     repoName,
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestComponentRepo_Create_PopulatesIDAndTimestamp(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "cr_ts")
	repo := NewComponentRepo(pool)

	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Group:        "com.example",
		Name:         "mylib",
		Version:      "1.0.0",
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.ID == "" {
		t.Fatal("Create did not populate ID")
	}
	if c.CreatedAt.IsZero() {
		t.Fatal("Create did not populate CreatedAt")
	}
}

func TestComponentRepo_Create_FieldsRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "cr_rt")
	repo := NewComponentRepo(pool)

	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "maven2",
		Group:        "org.apache.commons",
		Name:         "commons-lang3",
		Version:      "3.12.0",
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}

	if got.ID != c.ID {
		t.Errorf("ID: got %q want %q", got.ID, c.ID)
	}
	if got.RepositoryID != c.RepositoryID {
		t.Errorf("RepositoryID: got %q want %q", got.RepositoryID, c.RepositoryID)
	}
	if got.Repository != p.RepoName {
		t.Errorf("Repository name: got %q want %q", got.Repository, p.RepoName)
	}
	if got.Format != c.Format {
		t.Errorf("Format: got %q want %q", got.Format, c.Format)
	}
	if got.Group != c.Group {
		t.Errorf("Group: got %q want %q", got.Group, c.Group)
	}
	if got.Name != c.Name {
		t.Errorf("Name: got %q want %q", got.Name, c.Name)
	}
	if got.Version != c.Version {
		t.Errorf("Version: got %q want %q", got.Version, c.Version)
	}
	if got.DownloadCount != 0 {
		t.Errorf("DownloadCount: got %d want 0", got.DownloadCount)
	}
	if got.LastDownloaded != nil {
		t.Errorf("LastDownloaded: expected nil, got %v", got.LastDownloaded)
	}
	if got.Tags == nil {
		t.Error("Tags should not be nil (should default to empty slice)")
	}
	if len(got.Tags) != 0 {
		t.Errorf("Tags: expected empty, got %v", got.Tags)
	}
}

func TestComponentRepo_Create_Upsert_UpdatesExtraOnConflict(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "cr_upsert")
	repo := NewComponentRepo(pool)

	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Group:        "",
		Name:         "upsert-lib",
		Version:      "2.0.0",
		Extra:        map[string]any{"key1": "value1"},
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	firstID := c.ID

	// Re-create same (repository_id, format, group_id, name, version) with different extra.
	c2 := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Group:        "",
		Name:         "upsert-lib",
		Version:      "2.0.0",
		Extra:        map[string]any{"key1": "updated"},
	}
	if err := repo.Create(ctx, c2); err != nil {
		t.Fatalf("Create (upsert): %v", err)
	}

	// ON CONFLICT returns same row ID
	if c2.ID != firstID {
		t.Errorf("upsert: ID changed: %q → %q (expected same row)", firstID, c2.ID)
	}

	got, err := repo.Get(ctx, firstID)
	if err != nil || got == nil {
		t.Fatalf("Get after upsert: err=%v got=%v", err, got)
	}
	if v, ok := got.Extra["key1"]; !ok || v != "updated" {
		t.Errorf("upsert did not update extra: got %v", got.Extra)
	}
}

// ── Get (by ID) ───────────────────────────────────────────────────────────────

func TestComponentRepo_Get_NotFoundReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	repo := NewComponentRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	got, err := repo.Get(ctx, missing)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get(missing): want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Fatalf("Get(missing): expected nil, got %+v", got)
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestComponentRepo_Delete_RemovesComponent(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "del")
	repo := NewComponentRepo(pool)

	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Group:        "",
		Name:         "to-delete",
		Version:      "1.0",
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, c.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.Get(ctx, c.ID)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get after Delete: want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Fatal("Get after Delete should return nil")
	}
}

func TestComponentRepo_Delete_NonExistentIsNoOp(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	repo := NewComponentRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	if err := repo.Delete(ctx, missing); err != nil {
		t.Fatalf("Delete(non-existent): unexpected error: %v", err)
	}
}

// ── List (single repo) ────────────────────────────────────────────────────────

func TestComponentRepo_List_ReturnsAllComponents(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "list_all")
	repo := NewComponentRepo(pool)

	for i := 0; i < 3; i++ {
		c := &domain.Component{
			RepositoryID: p.RepositoryID,
			Format:       "raw",
			Group:        "",
			Name:         fmt.Sprintf("lib-%02d", i),
			Version:      "1.0",
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("Create lib-%02d: %v", i, err)
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

func TestComponentRepo_List_PaginationToken(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "list_page")
	repo := NewComponentRepo(pool)

	for i := 0; i < 5; i++ {
		c := &domain.Component{
			RepositoryID: p.RepositoryID,
			Format:       "raw",
			Group:        "",
			Name:         fmt.Sprintf("plib-%02d", i),
			Version:      "1.0",
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("Create plib-%02d: %v", i, err)
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
		t.Fatal("expected continuation token when more rows exist")
	}
	if *page.ContinuationToken != "2" {
		t.Errorf("continuation token: got %q want %q", *page.ContinuationToken, "2")
	}
}

func TestComponentRepo_List_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "list_empty")
	repo := NewComponentRepo(pool)

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

// ── ListByRepoNames (group expansion) ────────────────────────────────────────

func TestComponentRepo_ListByRepoNames_UnionAcrossRepos(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p1 := makeCompParent(t, ctx, "lbrn_r1")
	p2 := makeCompParent(t, ctx, "lbrn_r2")
	repo := NewComponentRepo(pool)

	// seed 2 components in repo1
	for i := 0; i < 2; i++ {
		c := &domain.Component{
			RepositoryID: p1.RepositoryID,
			Format:       "raw",
			Name:         fmt.Sprintf("r1-lib-%d", i),
			Version:      "1.0",
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("Create r1-lib-%d: %v", i, err)
		}
	}
	// seed 3 components in repo2
	for i := 0; i < 3; i++ {
		c := &domain.Component{
			RepositoryID: p2.RepositoryID,
			Format:       "raw",
			Name:         fmt.Sprintf("r2-lib-%d", i),
			Version:      "1.0",
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("Create r2-lib-%d: %v", i, err)
		}
	}

	page, err := repo.ListByRepoNames(ctx, []string{p1.RepoName, p2.RepoName}, 100, 0)
	if err != nil {
		t.Fatalf("ListByRepoNames: %v", err)
	}
	if len(page.Items) != 5 {
		t.Errorf("expected 5 items (2+3), got %d", len(page.Items))
	}
	// verify names from both repos appear
	names := make(map[string]bool)
	for _, item := range page.Items {
		names[item.Name] = true
	}
	for i := 0; i < 2; i++ {
		if !names[fmt.Sprintf("r1-lib-%d", i)] {
			t.Errorf("r1-lib-%d missing from union result", i)
		}
	}
	for i := 0; i < 3; i++ {
		if !names[fmt.Sprintf("r2-lib-%d", i)] {
			t.Errorf("r2-lib-%d missing from union result", i)
		}
	}
}

func TestComponentRepo_ListByRepoNames_EmptyListReturnsEmpty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewComponentRepo(pool)

	page, err := repo.ListByRepoNames(ctx, []string{}, 100, 0)
	if err != nil {
		t.Fatalf("ListByRepoNames(empty): %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(page.Items))
	}
}

func TestComponentRepo_ListByRepoNames_SingleRepo(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "lbrn_single")
	repo := NewComponentRepo(pool)

	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "single-lib",
		Version:      "1.0",
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	page, err := repo.ListByRepoNames(ctx, []string{p.RepoName}, 10, 0)
	if err != nil {
		t.Fatalf("ListByRepoNames: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(page.Items))
	}
	if page.Items[0].Name != "single-lib" {
		t.Errorf("Name: got %q want %q", page.Items[0].Name, "single-lib")
	}
}

// ── SetTags ───────────────────────────────────────────────────────────────────

func TestComponentRepo_SetTags_SetAndReadBack(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "tags_set")
	repo := NewComponentRepo(pool)

	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "tagged-lib",
		Version:      "1.0",
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	tags := []string{"release", "stable", "approved"}
	if err := repo.SetTags(ctx, c.ID, tags); err != nil {
		t.Fatalf("SetTags: %v", err)
	}

	got, err := repo.Get(ctx, c.ID)
	if err != nil || got == nil {
		t.Fatalf("Get: err=%v got=%v", err, got)
	}
	if len(got.Tags) != 3 {
		t.Fatalf("expected 3 tags, got %d: %v", len(got.Tags), got.Tags)
	}
	tagSet := make(map[string]bool)
	for _, tag := range got.Tags {
		tagSet[tag] = true
	}
	for _, want := range tags {
		if !tagSet[want] {
			t.Errorf("tag %q missing from result: %v", want, got.Tags)
		}
	}
}

func TestComponentRepo_SetTags_ReplacesExistingTags(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "tags_replace")
	repo := NewComponentRepo(pool)

	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "replace-tagged",
		Version:      "1.0",
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.SetTags(ctx, c.ID, []string{"old-tag-1", "old-tag-2"}); err != nil {
		t.Fatalf("SetTags (first): %v", err)
	}
	// Replace with entirely different tags
	if err := repo.SetTags(ctx, c.ID, []string{"new-tag"}); err != nil {
		t.Fatalf("SetTags (replace): %v", err)
	}

	got, err := repo.Get(ctx, c.ID)
	if err != nil || got == nil {
		t.Fatalf("Get: err=%v got=%v", err, got)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "new-tag" {
		t.Errorf("expected [new-tag], got %v", got.Tags)
	}
}

func TestComponentRepo_SetTags_ClearToEmpty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "tags_clear")
	repo := NewComponentRepo(pool)

	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "clearable-tags",
		Version:      "1.0",
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.SetTags(ctx, c.ID, []string{"a", "b"}); err != nil {
		t.Fatalf("SetTags: %v", err)
	}
	// Clear to empty
	if err := repo.SetTags(ctx, c.ID, []string{}); err != nil {
		t.Fatalf("SetTags (clear): %v", err)
	}

	got, err := repo.Get(ctx, c.ID)
	if err != nil || got == nil {
		t.Fatalf("Get: err=%v got=%v", err, got)
	}
	if len(got.Tags) != 0 {
		t.Errorf("expected empty tags after clear, got %v", got.Tags)
	}
}

func TestComponentRepo_SetTags_NilTreatedAsEmpty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "tags_nil")
	repo := NewComponentRepo(pool)

	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "nil-tags",
		Version:      "1.0",
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// nil tags — implementation normalizes to []
	if err := repo.SetTags(ctx, c.ID, nil); err != nil {
		t.Fatalf("SetTags(nil): %v", err)
	}

	got, err := repo.Get(ctx, c.ID)
	if err != nil || got == nil {
		t.Fatalf("Get: err=%v got=%v", err, got)
	}
	if got.Tags == nil {
		t.Error("Tags should not be nil (scanComponent normalizes nil to [])")
	}
	if len(got.Tags) != 0 {
		t.Errorf("expected empty tags, got %v", got.Tags)
	}
}

// ── UpdateExtra (JSONB merge) ─────────────────────────────────────────────────

// TestComponentRepo_UpdateExtra_NilExtra verifies the fix for a production bug:
// Create with nil Extra previously marshalled to JSON null; Postgres `null || jsonb` = null,
// so UpdateExtra silently discarded the update. Now Create stores '{}' for a nil Extra,
// and UpdateExtra uses COALESCE(extra,'{}') to handle any legacy null rows.
func TestComponentRepo_UpdateExtra_NilExtra(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "extra_nilbug")
	repo := NewComponentRepo(pool)

	// Create with nil Extra — intentionally not initialised
	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "nil-extra-bug",
		Version:      "1.0",
		// Extra intentionally nil
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.UpdateExtra(ctx, c.ID, map[string]any{"scan": "clean"}); err != nil {
		t.Fatalf("UpdateExtra: %v", err)
	}

	got, err := repo.Get(ctx, c.ID)
	if err != nil || got == nil {
		t.Fatalf("Get: err=%v got=%v", err, got)
	}
	if v, ok := got.Extra["scan"]; !ok || v != "clean" {
		t.Errorf("UpdateExtra on nil-Extra component: expected Extra[\"scan\"]==\"clean\", got %v", got.Extra)
	}
}

func TestComponentRepo_UpdateExtra_SetsKey(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "extra_set")
	repo := NewComponentRepo(pool)

	// Extra must be initialized to {} (not nil) for UpdateExtra merges to work.
	// See TestComponentRepo_UpdateExtra_NilExtraBug for the nil-extra production bug.
	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "extra-lib",
		Version:      "1.0",
		Extra:        map[string]any{}, // initialize to {} so || merge works
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.UpdateExtra(ctx, c.ID, map[string]any{"scan_result": "clean"}); err != nil {
		t.Fatalf("UpdateExtra: %v", err)
	}

	got, err := repo.Get(ctx, c.ID)
	if err != nil || got == nil {
		t.Fatalf("Get: err=%v got=%v", err, got)
	}
	if v, ok := got.Extra["scan_result"]; !ok || v != "clean" {
		t.Errorf("scan_result: got %v want %q", got.Extra, "clean")
	}
}

func TestComponentRepo_UpdateExtra_MergesKeys(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "extra_merge")
	repo := NewComponentRepo(pool)

	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "merge-lib",
		Version:      "1.0",
		Extra:        map[string]any{"key1": "v1"},
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Merge a second key — should not overwrite key1
	if err := repo.UpdateExtra(ctx, c.ID, map[string]any{"key2": "v2"}); err != nil {
		t.Fatalf("UpdateExtra (key2): %v", err)
	}

	got, err := repo.Get(ctx, c.ID)
	if err != nil || got == nil {
		t.Fatalf("Get: err=%v got=%v", err, got)
	}
	if v, ok := got.Extra["key1"]; !ok || v != "v1" {
		t.Errorf("key1 missing or changed after merge: extra=%v", got.Extra)
	}
	if v, ok := got.Extra["key2"]; !ok || v != "v2" {
		t.Errorf("key2 not present after merge: extra=%v", got.Extra)
	}
}

func TestComponentRepo_UpdateExtra_OverwritesExistingKey(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "extra_overwrite")
	repo := NewComponentRepo(pool)

	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "overwrite-lib",
		Version:      "1.0",
		Extra:        map[string]any{"status": "old"},
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// || operator in Postgres: right side wins on key collision
	if err := repo.UpdateExtra(ctx, c.ID, map[string]any{"status": "new"}); err != nil {
		t.Fatalf("UpdateExtra: %v", err)
	}

	got, err := repo.Get(ctx, c.ID)
	if err != nil || got == nil {
		t.Fatalf("Get: err=%v got=%v", err, got)
	}
	if v, ok := got.Extra["status"]; !ok || v != "new" {
		t.Errorf("status: expected %q, got %v", "new", got.Extra["status"])
	}
}

func TestComponentRepo_UpdateExtra_MultipleKeys_AllPresent(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "extra_multi")
	repo := NewComponentRepo(pool)

	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "multi-extra-lib",
		Version:      "1.0",
		Extra:        map[string]any{}, // must initialize to {} for merges to work
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Three separate merges
	if err := repo.UpdateExtra(ctx, c.ID, map[string]any{"a": "1"}); err != nil {
		t.Fatalf("UpdateExtra a: %v", err)
	}
	if err := repo.UpdateExtra(ctx, c.ID, map[string]any{"b": "2"}); err != nil {
		t.Fatalf("UpdateExtra b: %v", err)
	}
	if err := repo.UpdateExtra(ctx, c.ID, map[string]any{"c": "3"}); err != nil {
		t.Fatalf("UpdateExtra c: %v", err)
	}

	got, err := repo.Get(ctx, c.ID)
	if err != nil || got == nil {
		t.Fatalf("Get: err=%v got=%v", err, got)
	}
	for _, k := range []string{"a", "b", "c"} {
		if _, ok := got.Extra[k]; !ok {
			t.Errorf("key %q missing after sequential merges: extra=%v", k, got.Extra)
		}
	}
}

// ── Search ────────────────────────────────────────────────────────────────────

func TestComponentRepo_Search_ByName(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "srch_name")
	repo := NewComponentRepo(pool)

	names := []string{"alpha-client", "alpha-server", "beta-worker"}
	for _, name := range names {
		c := &domain.Component{
			RepositoryID: p.RepositoryID,
			Format:       "raw",
			Name:         name,
			Version:      "1.0",
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("Create %q: %v", name, err)
		}
	}

	page, err := repo.Search(ctx, domain.SearchParams{
		Repository: p.RepoName,
		Name:       "alpha",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Items) != 2 {
		t.Errorf("expected 2 alpha items, got %d: %v", len(page.Items), page.Items)
	}
	for _, item := range page.Items {
		if item.Name != "alpha-client" && item.Name != "alpha-server" {
			t.Errorf("unexpected item %q in results", item.Name)
		}
	}
}

func TestComponentRepo_Search_ByVersion(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "srch_ver")
	repo := NewComponentRepo(pool)

	versions := []string{"1.0.0", "1.1.0", "2.0.0"}
	for i, v := range versions {
		c := &domain.Component{
			RepositoryID: p.RepositoryID,
			Format:       "raw",
			Name:         fmt.Sprintf("versioned-%d", i),
			Version:      v,
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("Create %q: %v", v, err)
		}
	}

	page, err := repo.Search(ctx, domain.SearchParams{
		Repository: p.RepoName,
		Version:    "1.0.0",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("expected 1 item with version 1.0.0, got %d", len(page.Items))
	}
	if page.Items[0].Version != "1.0.0" {
		t.Errorf("Version: got %q want %q", page.Items[0].Version, "1.0.0")
	}
}

func TestComponentRepo_Search_ByTag(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "srch_tag")
	repo := NewComponentRepo(pool)

	c1 := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "tagged-artifact",
		Version:      "1.0",
	}
	c2 := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "untagged-artifact",
		Version:      "1.0",
	}
	if err := repo.Create(ctx, c1); err != nil {
		t.Fatalf("Create c1: %v", err)
	}
	if err := repo.Create(ctx, c2); err != nil {
		t.Fatalf("Create c2: %v", err)
	}

	if err := repo.SetTags(ctx, c1.ID, []string{"prod", "release"}); err != nil {
		t.Fatalf("SetTags: %v", err)
	}

	page, err := repo.Search(ctx, domain.SearchParams{
		Repository: p.RepoName,
		Tag:        "prod",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("Search by tag: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("expected 1 item with tag 'prod', got %d", len(page.Items))
	}
	if page.Items[0].Name != "tagged-artifact" {
		t.Errorf("Name: got %q want %q", page.Items[0].Name, "tagged-artifact")
	}
	// Verify tags round-tripped via Search
	if len(page.Items[0].Tags) == 0 {
		t.Error("tags should be populated in Search result")
	}
}

func TestComponentRepo_Search_ByFormat(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "srch_fmt")
	repo := NewComponentRepo(pool)

	formats := []string{"raw", "maven2", "npm"}
	for i, f := range formats {
		c := &domain.Component{
			RepositoryID: p.RepositoryID,
			Format:       f,
			Name:         fmt.Sprintf("fmt-lib-%d", i),
			Version:      "1.0",
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("Create format=%q: %v", f, err)
		}
	}

	page, err := repo.Search(ctx, domain.SearchParams{
		Repository: p.RepoName,
		Format:     "maven2",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("expected 1 maven2 item, got %d", len(page.Items))
	}
	if page.Items[0].Format != "maven2" {
		t.Errorf("Format: got %q want %q", page.Items[0].Format, "maven2")
	}
}

func TestComponentRepo_Search_ByGroup(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "srch_grp")
	repo := NewComponentRepo(pool)

	groups := []string{"com.example", "org.test", "io.other"}
	for i, g := range groups {
		c := &domain.Component{
			RepositoryID: p.RepositoryID,
			Format:       "raw",
			Group:        g,
			Name:         fmt.Sprintf("grp-lib-%d", i),
			Version:      "1.0",
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("Create group=%q: %v", g, err)
		}
	}

	page, err := repo.Search(ctx, domain.SearchParams{
		Repository: p.RepoName,
		Group:      "example",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("Search by group: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("expected 1 item matching group 'example', got %d", len(page.Items))
	}
	if page.Items[0].Group != "com.example" {
		t.Errorf("Group: got %q want %q", page.Items[0].Group, "com.example")
	}
}

func TestComponentRepo_Search_ByMavenGroupID(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "srch_mvn")
	repo := NewComponentRepo(pool)

	comps := []struct {
		group string
		name  string
	}{
		{"com.acme", "acme-core"},
		{"org.other", "other-lib"},
	}
	for _, tc := range comps {
		c := &domain.Component{
			RepositoryID: p.RepositoryID,
			Format:       "maven2",
			Group:        tc.group,
			Name:         tc.name,
			Version:      "1.0",
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("Create %q: %v", tc.name, err)
		}
	}

	page, err := repo.Search(ctx, domain.SearchParams{
		Repository:   p.RepoName,
		MavenGroupID: "com.acme",
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("Search MavenGroupID: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(page.Items))
	}
	if page.Items[0].Group != "com.acme" {
		t.Errorf("Group: got %q want %q", page.Items[0].Group, "com.acme")
	}
}

func TestComponentRepo_Search_ByRepositoryNames(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p1 := makeCompParent(t, ctx, "srch_rn1")
	p2 := makeCompParent(t, ctx, "srch_rn2")
	p3 := makeCompParent(t, ctx, "srch_rn3")
	repo := NewComponentRepo(pool)

	for i, parent := range []compParent{p1, p2, p3} {
		c := &domain.Component{
			RepositoryID: parent.RepositoryID,
			Format:       "raw",
			Name:         fmt.Sprintf("rn-lib-%d", i),
			Version:      "1.0",
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("Create rn-lib-%d: %v", i, err)
		}
	}

	// Search across repo1 + repo2 only (not repo3)
	page, err := repo.Search(ctx, domain.SearchParams{
		RepositoryNames: []string{p1.RepoName, p2.RepoName},
		Limit:           10,
	})
	if err != nil {
		t.Fatalf("Search RepositoryNames: %v", err)
	}
	if len(page.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(page.Items))
	}
	for _, item := range page.Items {
		if item.Repository != p1.RepoName && item.Repository != p2.RepoName {
			t.Errorf("unexpected repo %q in results", item.Repository)
		}
	}
}

func TestComponentRepo_Search_Pagination(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "srch_pg")
	repo := NewComponentRepo(pool)

	for i := 0; i < 7; i++ {
		c := &domain.Component{
			RepositoryID: p.RepositoryID,
			Format:       "raw",
			Name:         fmt.Sprintf("pg-lib-%02d", i),
			Version:      "1.0",
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("Create pg-lib-%02d: %v", i, err)
		}
	}

	page1, err := repo.Search(ctx, domain.SearchParams{
		Repository: p.RepoName,
		Limit:      3,
		Offset:     0,
	})
	if err != nil {
		t.Fatalf("Search page1: %v", err)
	}
	if len(page1.Items) != 3 {
		t.Errorf("page1: expected 3 items, got %d", len(page1.Items))
	}
	if page1.ContinuationToken == nil {
		t.Error("page1: expected continuation token")
	}

	page2, err := repo.Search(ctx, domain.SearchParams{
		Repository: p.RepoName,
		Limit:      3,
		Offset:     3,
	})
	if err != nil {
		t.Fatalf("Search page2: %v", err)
	}
	if len(page2.Items) != 3 {
		t.Errorf("page2: expected 3 items, got %d", len(page2.Items))
	}
}

func TestComponentRepo_Search_DefaultLimit(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "srch_deflim")
	repo := NewComponentRepo(pool)

	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "dl-lib",
		Version:      "1.0",
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Limit=0 should default to 50 (not error)
	page, err := repo.Search(ctx, domain.SearchParams{
		Repository: p.RepoName,
		Limit:      0,
	})
	if err != nil {
		t.Fatalf("Search(limit=0): %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("expected 1 item with default limit, got %d", len(page.Items))
	}
}

// ── ListDockerBrowseRows ──────────────────────────────────────────────────────

func TestComponentRepo_ListDockerBrowseRows_ReturnsDockerComponents(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	// Create a Docker-format repository
	bsRepo := NewBlobStoreRepo(pool)
	bs := &domain.BlobStore{
		Name:   "docker_bs_browse",
		Type:   "local",
		Config: map[string]any{"path": "/data/docker_browse"},
	}
	if err := bsRepo.Create(ctx, bs); err != nil {
		t.Fatalf("Create blob store: %v", err)
	}
	rRepo := NewRepositoryRepo(pool)
	r := &domain.Repository{
		Name:        "docker-browse-repo",
		Format:      domain.FormatDocker,
		Type:        domain.TypeHosted,
		BlobStoreID: &bs.ID,
		Online:      true,
	}
	if err := rRepo.Create(ctx, r); err != nil {
		t.Fatalf("Create docker repo: %v", err)
	}

	repo := NewComponentRepo(pool)

	images := []struct{ name, version string }{
		{"nginx", "latest"},
		{"nginx", "1.25"},
		{"redis", "7.0"},
	}
	for _, img := range images {
		c := &domain.Component{
			RepositoryID: r.ID,
			Format:       "docker",
			Name:         img.name,
			Version:      img.version,
		}
		if err := repo.Create(ctx, c); err != nil {
			t.Fatalf("Create %s:%s: %v", img.name, img.version, err)
		}
	}

	rows, err := repo.ListDockerBrowseRows(ctx, []string{"docker-browse-repo"}, 100)
	if err != nil {
		t.Fatalf("ListDockerBrowseRows: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(rows))
	}
	// Verify fields
	for _, row := range rows {
		if row.ComponentID == "" {
			t.Error("ComponentID must not be empty")
		}
		if row.ImageName == "" {
			t.Error("ImageName must not be empty")
		}
		if row.Version == "" {
			t.Error("Version must not be empty")
		}
	}
}

// An oci repository speaks the same OCI Distribution protocol as a docker one,
// so its components must appear in the registry browse rows too.
func TestComponentRepo_ListDockerBrowseRows_ReturnsOCIComponents(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	bsRepo := NewBlobStoreRepo(pool)
	bs := &domain.BlobStore{
		Name:   "oci_bs_browse",
		Type:   "local",
		Config: map[string]any{"path": "/data/oci_browse"},
	}
	if err := bsRepo.Create(ctx, bs); err != nil {
		t.Fatalf("Create blob store: %v", err)
	}
	rRepo := NewRepositoryRepo(pool)
	r := &domain.Repository{
		Name:        "oci-browse-repo",
		Format:      domain.FormatOCI,
		Type:        domain.TypeHosted,
		BlobStoreID: &bs.ID,
		Online:      true,
	}
	if err := rRepo.Create(ctx, r); err != nil {
		t.Fatalf("Create oci repo: %v", err)
	}

	repo := NewComponentRepo(pool)
	c := &domain.Component{
		RepositoryID: r.ID,
		Format:       "oci",
		Name:         "charts/nginx",
		Version:      "1.2.3",
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create component: %v", err)
	}

	rows, err := repo.ListDockerBrowseRows(ctx, []string{"oci-browse-repo"}, 100)
	if err != nil {
		t.Fatalf("ListDockerBrowseRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row for an oci repository, got %d", len(rows))
	}
	if rows[0].ImageName != "charts/nginx" || rows[0].Version != "1.2.3" {
		t.Errorf("unexpected row: %+v", rows[0])
	}
}

func TestComponentRepo_ListDockerBrowseRows_EmptyRepoNamesReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewComponentRepo(pool)

	rows, err := repo.ListDockerBrowseRows(ctx, []string{}, 100)
	if err != nil {
		t.Fatalf("ListDockerBrowseRows(empty): %v", err)
	}
	if rows != nil {
		t.Errorf("expected nil for empty repo names, got %v", rows)
	}
}

func TestComponentRepo_ListDockerBrowseRows_NonDockerRepoExcluded(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	// raw-format repository — should NOT appear in docker browse rows
	p := makeCompParent(t, ctx, "docker_excl")
	repo := NewComponentRepo(pool)

	c := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "not-docker",
		Version:      "1.0",
	}
	if err := repo.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rows, err := repo.ListDockerBrowseRows(ctx, []string{p.RepoName}, 100)
	if err != nil {
		t.Fatalf("ListDockerBrowseRows: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows for non-docker repo, got %d", len(rows))
	}
}

// ── DeleteOrphans ─────────────────────────────────────────────────────────────

func TestComponentRepo_DeleteOrphans_RemovesComponentsWithNoAssets(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "orphan_del")
	repo := NewComponentRepo(pool)

	// Create component without any assets (orphan)
	orphan := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "orphan-lib",
		Version:      "1.0",
	}
	if err := repo.Create(ctx, orphan); err != nil {
		t.Fatalf("Create orphan: %v", err)
	}

	// Create component with an asset (not orphan)
	kept := &domain.Component{
		RepositoryID: p.RepositoryID,
		Format:       "raw",
		Name:         "kept-lib",
		Version:      "1.0",
	}
	if err := repo.Create(ctx, kept); err != nil {
		t.Fatalf("Create kept: %v", err)
	}

	// Add an asset to "kept"
	aRepo := NewAssetRepo(pool)
	a := &domain.Asset{
		ComponentID:  kept.ID,
		RepositoryID: p.RepositoryID,
		Path:         "/kept/file.bin",
		BlobStoreID:  p.BlobStoreID,
		BlobKey:      "bk_kept",
		SizeBytes:    100,
		ContentType:  "application/octet-stream",
		SHA1:         "da39a3ee5e6b4b0d3255bfef95601890afd80709",
		SHA256:       "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		MD5:          "d41d8cd98f00b204e9800998ecf8427e",
	}
	if err := aRepo.Create(ctx, a); err != nil {
		t.Fatalf("Create asset: %v", err)
	}

	// Run DeleteOrphans
	if err := repo.DeleteOrphans(ctx, p.RepoName); err != nil {
		t.Fatalf("DeleteOrphans: %v", err)
	}

	// Orphan should be gone
	gotOrphan, err := repo.Get(ctx, orphan.ID)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get orphan: want ErrNotFound, got %v", err)
	}
	if gotOrphan != nil {
		t.Error("orphan component should have been deleted")
	}

	// Non-orphan should still exist
	gotKept, err := repo.Get(ctx, kept.ID)
	if err != nil {
		t.Fatalf("Get kept: %v", err)
	}
	if gotKept == nil {
		t.Error("kept component (has assets) should NOT have been deleted")
	}
}

func TestComponentRepo_DeleteOrphans_EmptyRepoIsNoOp(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p := makeCompParent(t, ctx, "orphan_noop")
	repo := NewComponentRepo(pool)

	// No components at all — DeleteOrphans should not error
	if err := repo.DeleteOrphans(ctx, p.RepoName); err != nil {
		t.Fatalf("DeleteOrphans on empty repo: %v", err)
	}
}

// ── ListOCIReferrers ─────────────────────────────────────────────────────────

func TestComponentRepo_ListOCIReferrers_FiltersBySubjectDigest(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p1 := makeCompParent(t, ctx, "referrers_r1")
	p2 := makeCompParent(t, ctx, "referrers_r2")
	repo := NewComponentRepo(pool)

	subjectDigest := "sha256:1111111111111111111111111111111111111111111111111111111111111111"

	// repo1: one referrer of subjectDigest ...
	referrer1 := &domain.Component{
		RepositoryID: p1.RepositoryID,
		Format:       "oci",
		Name:         "signed-image",
		Version:      "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		Extra:        map[string]any{"oci_subject": subjectDigest},
	}
	if err := repo.Create(ctx, referrer1); err != nil {
		t.Fatalf("Create referrer1: %v", err)
	}

	// ... and one unrelated component with no oci_subject at all.
	unrelated := &domain.Component{
		RepositoryID: p1.RepositoryID,
		Format:       "oci",
		Name:         "plain-image",
		Version:      "sha256:3333333333333333333333333333333333333333333333333333333333333333",
	}
	if err := repo.Create(ctx, unrelated); err != nil {
		t.Fatalf("Create unrelated: %v", err)
	}

	// repo2: another referrer of the same subjectDigest.
	referrer2 := &domain.Component{
		RepositoryID: p2.RepositoryID,
		Format:       "oci",
		Name:         "sbom",
		Version:      "sha256:4444444444444444444444444444444444444444444444444444444444444444",
		Extra:        map[string]any{"oci_subject": subjectDigest},
	}
	if err := repo.Create(ctx, referrer2); err != nil {
		t.Fatalf("Create referrer2: %v", err)
	}

	// Querying repo1 alone returns only referrer1.
	got, err := repo.ListOCIReferrers(ctx, []string{p1.RepoName}, "", subjectDigest)
	if err != nil {
		t.Fatalf("ListOCIReferrers(repo1): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 referrer in repo1, got %d: %+v", len(got), got)
	}
	if got[0].ID != referrer1.ID {
		t.Errorf("expected referrer1 (%s), got %s (%s)", referrer1.ID, got[0].ID, got[0].Name)
	}

	// Querying both repo names returns both referrers.
	gotBoth, err := repo.ListOCIReferrers(ctx, []string{p1.RepoName, p2.RepoName}, "", subjectDigest)
	if err != nil {
		t.Fatalf("ListOCIReferrers(both repos): %v", err)
	}
	if len(gotBoth) != 2 {
		t.Fatalf("expected 2 referrers across both repos, got %d: %+v", len(gotBoth), gotBoth)
	}
	ids := map[string]bool{gotBoth[0].ID: true, gotBoth[1].ID: true}
	if !ids[referrer1.ID] || !ids[referrer2.ID] {
		t.Errorf("expected both referrer1 and referrer2, got %+v", gotBoth)
	}

	// A digest nothing points at returns no rows.
	none, err := repo.ListOCIReferrers(ctx, []string{p1.RepoName, p2.RepoName}, "", "sha256:deadbeef")
	if err != nil {
		t.Fatalf("ListOCIReferrers(no match): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 referrers for unmatched digest, got %d: %+v", len(none), none)
	}
}

// The referrers handler always passes a non-empty imageName — /v2/{name}/... —
// so the "AND c.name = $N" clause and the placeholder arithmetic that positions
// it after the repository names and the digest are what production actually
// runs. Covered here against real SQL, not only against the in-memory mock.
func TestComponentRepo_ListOCIReferrers_FiltersByImageName(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	p1 := makeCompParent(t, ctx, "referrers_name_r1")
	p2 := makeCompParent(t, ctx, "referrers_name_r2")
	repo := NewComponentRepo(pool)

	subjectDigest := "sha256:1111111111111111111111111111111111111111111111111111111111111111"

	// The referrer we want: right image name, right subject.
	wanted := &domain.Component{
		RepositoryID: p1.RepositoryID,
		Format:       "oci",
		Name:         "charts/nginx",
		Version:      "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		Extra:        map[string]any{"oci_subject": subjectDigest},
	}
	if err := repo.Create(ctx, wanted); err != nil {
		t.Fatalf("Create wanted: %v", err)
	}

	// The same subject digest under a different image name. Digests are global,
	// so this row really can exist; naming it would leak another image's
	// signature into this image's index.
	otherName := &domain.Component{
		RepositoryID: p1.RepositoryID,
		Format:       "oci",
		Name:         "charts/redis",
		Version:      "sha256:3333333333333333333333333333333333333333333333333333333333333333",
		Extra:        map[string]any{"oci_subject": subjectDigest},
	}
	if err := repo.Create(ctx, otherName); err != nil {
		t.Fatalf("Create otherName: %v", err)
	}

	// The right image name in a repository that was not asked for, so the name
	// filter cannot be mistaken for the repository filter.
	otherRepo := &domain.Component{
		RepositoryID: p2.RepositoryID,
		Format:       "oci",
		Name:         "charts/nginx",
		Version:      "sha256:4444444444444444444444444444444444444444444444444444444444444444",
		Extra:        map[string]any{"oci_subject": subjectDigest},
	}
	if err := repo.Create(ctx, otherRepo); err != nil {
		t.Fatalf("Create otherRepo: %v", err)
	}

	// A prefix of the wanted name must not match: the clause is equality.
	prefixName := &domain.Component{
		RepositoryID: p1.RepositoryID,
		Format:       "oci",
		Name:         "charts/nginx-extra",
		Version:      "sha256:5555555555555555555555555555555555555555555555555555555555555555",
		Extra:        map[string]any{"oci_subject": subjectDigest},
	}
	if err := repo.Create(ctx, prefixName); err != nil {
		t.Fatalf("Create prefixName: %v", err)
	}

	got, err := repo.ListOCIReferrers(ctx, []string{p1.RepoName}, "charts/nginx", subjectDigest)
	if err != nil {
		t.Fatalf("ListOCIReferrers(imageName): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly the charts/nginx referrer in repo1, got %d: %+v", len(got), got)
	}
	if got[0].ID != wanted.ID {
		t.Errorf("expected %s (charts/nginx), got %s (%s)", wanted.ID, got[0].ID, got[0].Name)
	}

	// Two repository names push the digest and image-name placeholders one
	// position further along; the arithmetic must still line up.
	gotBoth, err := repo.ListOCIReferrers(ctx, []string{p1.RepoName, p2.RepoName}, "charts/nginx", subjectDigest)
	if err != nil {
		t.Fatalf("ListOCIReferrers(both repos, imageName): %v", err)
	}
	if len(gotBoth) != 2 {
		t.Fatalf("expected both charts/nginx referrers across the two repos, got %d: %+v", len(gotBoth), gotBoth)
	}
	for _, c := range gotBoth {
		if c.Name != "charts/nginx" {
			t.Errorf("image-name filter leaked %q into the result", c.Name)
		}
	}

	// An image name nothing was pushed under returns no rows, even though the
	// subject digest itself matches four components.
	none, err := repo.ListOCIReferrers(ctx, []string{p1.RepoName, p2.RepoName}, "charts/absent", subjectDigest)
	if err != nil {
		t.Fatalf("ListOCIReferrers(absent imageName): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 referrers for an unused image name, got %d: %+v", len(none), none)
	}
}
