//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func makeRepo(name string, format domain.RepoFormat, repoType domain.RepoType, bsID *string) *domain.Repository {
	return &domain.Repository{
		Name:        name,
		Format:      format,
		Type:        repoType,
		BlobStoreID: bsID,
		Online:      true,
		Description: "test repo " + name,
	}
}

// newRepoBlobStore creates a local blob store with a unique name and returns its ID.
func newRepoBlobStore(t *testing.T, ctx context.Context, name string) (bsID string) {
	t.Helper()
	pool := pgtest.Pool(t)
	bsRepo := NewBlobStoreRepo(pool)
	bs := &domain.BlobStore{
		Name:   name,
		Type:   "local",
		Config: map[string]any{"path": "/data/" + name},
	}
	if err := bsRepo.Create(ctx, bs); err != nil {
		t.Fatalf("newRepoBlobStore %q: %v", name, err)
	}
	return bs.ID
}

func strPtr(s string) *string { return &s }

// ── Create ────────────────────────────────────────────────────────────────────

func TestRepositoryRepo_Create_PopulatesIDAndTimestamps(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	bsID := newRepoBlobStore(t, ctx, "rr_create_ts_bs")

	repo := NewRepositoryRepo(pool)
	r := makeRepo("rr_create_ts", domain.FormatRaw, domain.TypeHosted, strPtr(bsID))
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.ID == "" {
		t.Fatal("Create did not populate ID")
	}
	if r.CreatedAt.IsZero() {
		t.Fatal("Create did not populate CreatedAt")
	}
	if r.UpdatedAt.IsZero() {
		t.Fatal("Create did not populate UpdatedAt")
	}
}

func TestRepositoryRepo_Create_FieldsRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	bsID := newRepoBlobStore(t, ctx, "rr_roundtrip_bs")

	repo := NewRepositoryRepo(pool)
	r := &domain.Repository{
		Name:        "rr_roundtrip",
		Format:      domain.FormatMaven2,
		Type:        domain.TypeHosted,
		BlobStoreID: strPtr(bsID),
		Online:      true,
		Description: "round trip test",
		FormatConfig: map[string]any{
			"version_policy": "release",
		},
		AllowAnonymous: true,
	}
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, r.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.ID != r.ID {
		t.Errorf("ID: got %q, want %q", got.ID, r.ID)
	}
	if got.Name != r.Name {
		t.Errorf("Name: got %q, want %q", got.Name, r.Name)
	}
	if got.Format != domain.FormatMaven2 {
		t.Errorf("Format: got %q, want maven2", got.Format)
	}
	if got.Type != domain.TypeHosted {
		t.Errorf("Type: got %q, want hosted", got.Type)
	}
	if got.BlobStoreID == nil || *got.BlobStoreID != bsID {
		t.Errorf("BlobStoreID: got %v, want %q", got.BlobStoreID, bsID)
	}
	if !got.Online {
		t.Error("Online: got false, want true")
	}
	if !got.AllowAnonymous {
		t.Error("AllowAnonymous: got false, want true")
	}
	if got.Description != "round trip test" {
		t.Errorf("Description: got %q, want %q", got.Description, "round trip test")
	}
	if got.FormatConfig["version_policy"] != "release" {
		t.Errorf("FormatConfig.version_policy: got %v, want release", got.FormatConfig["version_policy"])
	}
}

func TestRepositoryRepo_Create_NilBlobStoreID(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	repo := NewRepositoryRepo(pool)
	r := makeRepo("rr_nil_bs", domain.FormatRaw, domain.TypeHosted, nil)
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create with nil BlobStoreID: %v", err)
	}

	got, err := repo.Get(ctx, r.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.BlobStoreID != nil {
		t.Errorf("BlobStoreID: got %v, want nil", got.BlobStoreID)
	}
}

func TestRepositoryRepo_Create_DuplicateName_Errors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	repo := NewRepositoryRepo(pool)
	r1 := makeRepo("rr_dup", domain.FormatRaw, domain.TypeHosted, nil)
	if err := repo.Create(ctx, r1); err != nil {
		t.Fatalf("Create first: %v", err)
	}

	r2 := makeRepo("rr_dup", domain.FormatNPM, domain.TypeProxy, nil)
	if err := repo.Create(ctx, r2); err == nil {
		t.Fatal("Create with duplicate name: expected error, got nil")
	}
}

// ── CleanupPolicyIDs array round-trip ─────────────────────────────────────────

func TestRepositoryRepo_Create_CleanupPolicyIDs_RoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "cleanup_policies")
	ctx := context.Background()

	// Insert two cleanup policies so we have valid UUIDs to reference.
	cpRepo := NewCleanupPolicyRepo(pool)
	cp1 := &domain.CleanupPolicy{Name: "rr_cp1", Format: "*", Criteria: map[string]any{"lastDownloadedDays": 30}}
	cp2 := &domain.CleanupPolicy{Name: "rr_cp2", Format: "*", Criteria: map[string]any{"lastDownloadedDays": 60}}
	if err := cpRepo.Create(ctx, cp1); err != nil {
		t.Fatalf("Create cp1: %v", err)
	}
	if err := cpRepo.Create(ctx, cp2); err != nil {
		t.Fatalf("Create cp2: %v", err)
	}

	repo := NewRepositoryRepo(pool)
	r := makeRepo("rr_cleanup_ids", domain.FormatMaven2, domain.TypeHosted, nil)
	r.CleanupPolicyIDs = []string{cp1.ID, cp2.ID}
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, r.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if len(got.CleanupPolicyIDs) != 2 {
		t.Fatalf("CleanupPolicyIDs len: got %d, want 2", len(got.CleanupPolicyIDs))
	}
	// UUIDs may be returned in any order by Postgres; build a set.
	idSet := map[string]bool{got.CleanupPolicyIDs[0]: true, got.CleanupPolicyIDs[1]: true}
	if !idSet[cp1.ID] || !idSet[cp2.ID] {
		t.Errorf("CleanupPolicyIDs mismatch: got %v, want [%s %s]", got.CleanupPolicyIDs, cp1.ID, cp2.ID)
	}
}

func TestRepositoryRepo_Create_EmptyCleanupPolicyIDs(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	repo := NewRepositoryRepo(pool)
	r := makeRepo("rr_empty_cpids", domain.FormatRaw, domain.TypeHosted, nil)
	r.CleanupPolicyIDs = nil
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, r.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// policyIDsToStrings converts nil → [] so DB stores empty array.
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if len(got.CleanupPolicyIDs) != 0 {
		t.Errorf("CleanupPolicyIDs: got %v, want empty", got.CleanupPolicyIDs)
	}
}

// ── Get (by name) ─────────────────────────────────────────────────────────────

func TestRepositoryRepo_Get_NotFound_ReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	got, err := repo.Get(ctx, "does_not_exist_rr_xyz")
	if err != nil {
		t.Fatalf("Get(missing): unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("Get(missing): expected nil, got %+v", got)
	}
}

// ── GetByID ───────────────────────────────────────────────────────────────────

func TestRepositoryRepo_GetByID_HappyPath(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	bsID := newRepoBlobStore(t, ctx, "rr_getbyid_bs")

	repo := NewRepositoryRepo(pool)
	r := makeRepo("rr_getbyid", domain.FormatNPM, domain.TypeHosted, strPtr(bsID))
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("GetByID returned nil")
	}
	if got.ID != r.ID {
		t.Errorf("ID: got %q, want %q", got.ID, r.ID)
	}
	if got.Name != r.Name {
		t.Errorf("Name: got %q, want %q", got.Name, r.Name)
	}
}

func TestRepositoryRepo_GetByID_NotFound_ReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	got, err := repo.GetByID(ctx, missing)
	if err != nil {
		t.Fatalf("GetByID(missing): unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("GetByID(missing): expected nil, got %+v", got)
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestRepositoryRepo_List_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	list, err := repo.List(ctx, "", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("List: expected empty, got %d", len(list))
	}
}

func TestRepositoryRepo_List_OrderedByName(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	for _, name := range []string{"zzz_rr_list", "aaa_rr_list", "mmm_rr_list"} {
		r := makeRepo(name, domain.FormatRaw, domain.TypeHosted, nil)
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("Create %q: %v", name, err)
		}
	}

	list, err := repo.List(ctx, "", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("List: got %d, want 3", len(list))
	}
	if list[0].Name != "aaa_rr_list" || list[1].Name != "mmm_rr_list" || list[2].Name != "zzz_rr_list" {
		t.Errorf("List not ordered: got %q %q %q", list[0].Name, list[1].Name, list[2].Name)
	}
}

func TestRepositoryRepo_List_FilterByFormat(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	// Insert repos of two different formats.
	for _, name := range []string{"rr_fmt_maven_1", "rr_fmt_maven_2"} {
		r := makeRepo(name, domain.FormatMaven2, domain.TypeHosted, nil)
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("Create %q: %v", name, err)
		}
	}
	rNPM := makeRepo("rr_fmt_npm_1", domain.FormatNPM, domain.TypeHosted, nil)
	if err := repo.Create(ctx, rNPM); err != nil {
		t.Fatalf("Create npm: %v", err)
	}

	list, err := repo.List(ctx, "maven2", "")
	if err != nil {
		t.Fatalf("List(maven2): %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List(maven2): got %d, want 2", len(list))
	}
	for _, r := range list {
		if r.Format != domain.FormatMaven2 {
			t.Errorf("unexpected format %q in maven2 filter", r.Format)
		}
	}
}

func TestRepositoryRepo_List_FilterByType(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	r1 := makeRepo("rr_type_hosted_1", domain.FormatRaw, domain.TypeHosted, nil)
	r2 := makeRepo("rr_type_proxy_1", domain.FormatRaw, domain.TypeProxy, nil)
	r3 := makeRepo("rr_type_group_1", domain.FormatRaw, domain.TypeGroup, nil)
	for _, r := range []*domain.Repository{r1, r2, r3} {
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("Create %q: %v", r.Name, err)
		}
	}

	list, err := repo.List(ctx, "", "proxy")
	if err != nil {
		t.Fatalf("List(proxy): %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List(proxy): got %d, want 1", len(list))
	}
	if list[0].Type != domain.TypeProxy {
		t.Errorf("Type: got %q, want proxy", list[0].Type)
	}
}

func TestRepositoryRepo_List_FilterByFormatAndType(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	// docker hosted, docker proxy, npm hosted — filter to docker+hosted
	repos := []*domain.Repository{
		makeRepo("rr_combo_docker_hosted", domain.FormatDocker, domain.TypeHosted, nil),
		makeRepo("rr_combo_docker_proxy", domain.FormatDocker, domain.TypeProxy, nil),
		makeRepo("rr_combo_npm_hosted", domain.FormatNPM, domain.TypeHosted, nil),
	}
	for _, r := range repos {
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("Create %q: %v", r.Name, err)
		}
	}

	list, err := repo.List(ctx, "docker", "hosted")
	if err != nil {
		t.Fatalf("List(docker,hosted): %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List(docker,hosted): got %d, want 1", len(list))
	}
	if list[0].Name != "rr_combo_docker_hosted" {
		t.Errorf("Name: got %q, want rr_combo_docker_hosted", list[0].Name)
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestRepositoryRepo_Update_Fields(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	bsID1 := newRepoBlobStore(t, ctx, "rr_upd_bs1")
	bsID2 := newRepoBlobStore(t, ctx, "rr_upd_bs2")

	repo := NewRepositoryRepo(pool)
	r := makeRepo("rr_update", domain.FormatNPM, domain.TypeHosted, strPtr(bsID1))
	r.Online = true
	r.AllowAnonymous = false
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Mutate several fields.
	r.Online = false
	r.AllowAnonymous = true
	r.Description = "updated description"
	r.BlobStoreID = strPtr(bsID2)
	r.FormatConfig = map[string]any{"strict": true}
	if err := repo.Update(ctx, r); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.Get(ctx, r.Name)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got == nil {
		t.Fatal("Get after Update returned nil")
	}
	if got.Online {
		t.Error("Online: expected false after update")
	}
	if !got.AllowAnonymous {
		t.Error("AllowAnonymous: expected true after update")
	}
	if got.Description != "updated description" {
		t.Errorf("Description: got %q, want %q", got.Description, "updated description")
	}
	if got.BlobStoreID == nil || *got.BlobStoreID != bsID2 {
		t.Errorf("BlobStoreID: got %v, want %q", got.BlobStoreID, bsID2)
	}
	if got.FormatConfig["strict"] != true {
		t.Errorf("FormatConfig.strict: got %v, want true", got.FormatConfig["strict"])
	}
}

func TestRepositoryRepo_Update_CleanupPolicyIDs(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "cleanup_policies")
	ctx := context.Background()

	cpRepo := NewCleanupPolicyRepo(pool)
	cp := &domain.CleanupPolicy{Name: "rr_upd_cp", Format: "*", Criteria: map[string]any{"lastDownloadedDays": 10}}
	if err := cpRepo.Create(ctx, cp); err != nil {
		t.Fatalf("Create cp: %v", err)
	}

	repo := NewRepositoryRepo(pool)
	r := makeRepo("rr_upd_cpids", domain.FormatRaw, domain.TypeHosted, nil)
	r.CleanupPolicyIDs = nil
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Add a cleanup policy via Update.
	r.CleanupPolicyIDs = []string{cp.ID}
	if err := repo.Update(ctx, r); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.Get(ctx, r.Name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if len(got.CleanupPolicyIDs) != 1 || got.CleanupPolicyIDs[0] != cp.ID {
		t.Errorf("CleanupPolicyIDs after update: got %v, want [%s]", got.CleanupPolicyIDs, cp.ID)
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestRepositoryRepo_Delete_RemovesRepo(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	repo := NewRepositoryRepo(pool)
	r := makeRepo("rr_delete", domain.FormatRaw, domain.TypeHosted, nil)
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, r.Name); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.Get(ctx, r.Name)
	if err != nil {
		t.Fatalf("Get after Delete: %v", err)
	}
	if got != nil {
		t.Fatal("Get after Delete: expected nil, repo still exists")
	}
}

func TestRepositoryRepo_Delete_UnknownName_NoError(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	if err := repo.Delete(ctx, "never_existed_rr_xyz"); err != nil {
		t.Fatalf("Delete(missing): unexpected error: %v", err)
	}
}

// ── HasAnyAnonymousDocker ─────────────────────────────────────────────────────

func TestRepositoryRepo_HasAnyAnonymousDocker_True(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	// A docker repo with allow_anonymous=true.
	r := makeRepo("rr_anon_docker", domain.FormatDocker, domain.TypeHosted, nil)
	r.AllowAnonymous = true
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	ok, err := repo.HasAnyAnonymousDocker(ctx)
	if err != nil {
		t.Fatalf("HasAnyAnonymousDocker: %v", err)
	}
	if !ok {
		t.Error("HasAnyAnonymousDocker: got false, want true")
	}
}

func TestRepositoryRepo_HasAnyAnonymousDocker_FalseWhenNoDockerRepos(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	ok, err := repo.HasAnyAnonymousDocker(ctx)
	if err != nil {
		t.Fatalf("HasAnyAnonymousDocker (empty): %v", err)
	}
	if ok {
		t.Error("HasAnyAnonymousDocker (empty): got true, want false")
	}
}

func TestRepositoryRepo_HasAnyAnonymousDocker_FalseWhenDockerNotAnonymous(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	// Docker repo with allow_anonymous=false.
	r := makeRepo("rr_nonanon_docker", domain.FormatDocker, domain.TypeHosted, nil)
	r.AllowAnonymous = false
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	ok, err := repo.HasAnyAnonymousDocker(ctx)
	if err != nil {
		t.Fatalf("HasAnyAnonymousDocker: %v", err)
	}
	if ok {
		t.Error("HasAnyAnonymousDocker: got true for non-anonymous docker repo, want false")
	}
}

func TestRepositoryRepo_HasAnyAnonymousDocker_FalseWhenOnlyNonDockerAnonymous(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	// A non-docker repo with allow_anonymous=true — should not count.
	r := makeRepo("rr_anon_npm", domain.FormatNPM, domain.TypeHosted, nil)
	r.AllowAnonymous = true
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	ok, err := repo.HasAnyAnonymousDocker(ctx)
	if err != nil {
		t.Fatalf("HasAnyAnonymousDocker: %v", err)
	}
	if ok {
		t.Error("HasAnyAnonymousDocker: got true for non-docker anonymous repo, want false")
	}
}

// ── ListNamesByCleanupPolicyID ────────────────────────────────────────────────

func TestRepositoryRepo_ListNamesByCleanupPolicyID_HappyPath(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "cleanup_policies")
	ctx := context.Background()

	cpRepo := NewCleanupPolicyRepo(pool)
	cp := &domain.CleanupPolicy{Name: "rr_lnbcp_cp", Format: "*", Criteria: map[string]any{"lastDownloadedDays": 7}}
	if err := cpRepo.Create(ctx, cp); err != nil {
		t.Fatalf("Create cp: %v", err)
	}

	repo := NewRepositoryRepo(pool)

	// Two repos use the policy, one does not.
	r1 := makeRepo("rr_lnbcp_1", domain.FormatRaw, domain.TypeHosted, nil)
	r1.CleanupPolicyIDs = []string{cp.ID}
	r2 := makeRepo("rr_lnbcp_2", domain.FormatMaven2, domain.TypeHosted, nil)
	r2.CleanupPolicyIDs = []string{cp.ID}
	r3 := makeRepo("rr_lnbcp_3", domain.FormatNPM, domain.TypeHosted, nil)
	r3.CleanupPolicyIDs = nil

	for _, r := range []*domain.Repository{r1, r2, r3} {
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("Create %q: %v", r.Name, err)
		}
	}

	names, err := repo.ListNamesByCleanupPolicyID(ctx, cp.ID)
	if err != nil {
		t.Fatalf("ListNamesByCleanupPolicyID: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("got %d names, want 2: %v", len(names), names)
	}
	nameSet := map[string]bool{names[0]: true, names[1]: true}
	if !nameSet[r1.Name] || !nameSet[r2.Name] {
		t.Errorf("names mismatch: got %v, want [%s %s]", names, r1.Name, r2.Name)
	}
}

func TestRepositoryRepo_ListNamesByCleanupPolicyID_NoneMatch(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	const nonexistentPolicyID = "00000000-0000-0000-0000-000000000001"
	names, err := repo.ListNamesByCleanupPolicyID(ctx, nonexistentPolicyID)
	if err != nil {
		t.Fatalf("ListNamesByCleanupPolicyID(missing): %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("expected empty, got %v", names)
	}
}

// ── DetachCleanupPolicyID ─────────────────────────────────────────────────────

func TestRepositoryRepo_DetachCleanupPolicyID_RemovesFromArray(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "cleanup_policies")
	ctx := context.Background()

	cpRepo := NewCleanupPolicyRepo(pool)
	cp1 := &domain.CleanupPolicy{Name: "rr_det_cp1", Format: "*", Criteria: map[string]any{"lastDownloadedDays": 5}}
	cp2 := &domain.CleanupPolicy{Name: "rr_det_cp2", Format: "*", Criteria: map[string]any{"lastDownloadedDays": 10}}
	if err := cpRepo.Create(ctx, cp1); err != nil {
		t.Fatalf("Create cp1: %v", err)
	}
	if err := cpRepo.Create(ctx, cp2); err != nil {
		t.Fatalf("Create cp2: %v", err)
	}

	repo := NewRepositoryRepo(pool)
	r := makeRepo("rr_detach", domain.FormatRaw, domain.TypeHosted, nil)
	r.CleanupPolicyIDs = []string{cp1.ID, cp2.ID}
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Detach cp1; cp2 should remain.
	if err := repo.DetachCleanupPolicyID(ctx, cp1.ID); err != nil {
		t.Fatalf("DetachCleanupPolicyID: %v", err)
	}

	got, err := repo.Get(ctx, r.Name)
	if err != nil {
		t.Fatalf("Get after Detach: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if len(got.CleanupPolicyIDs) != 1 {
		t.Fatalf("CleanupPolicyIDs after detach: got %v, want 1 entry", got.CleanupPolicyIDs)
	}
	if got.CleanupPolicyIDs[0] != cp2.ID {
		t.Errorf("remaining ID: got %q, want %q", got.CleanupPolicyIDs[0], cp2.ID)
	}
}

func TestRepositoryRepo_DetachCleanupPolicyID_NoneMatch_NoError(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	const nonexistentPolicyID = "00000000-0000-0000-0000-000000000002"
	if err := repo.DetachCleanupPolicyID(ctx, nonexistentPolicyID); err != nil {
		t.Fatalf("DetachCleanupPolicyID(no match): unexpected error: %v", err)
	}
}

// ── ListByBlobStoreID ─────────────────────────────────────────────────────────

func TestRepositoryRepo_ListByBlobStoreID_HappyPath(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	bsID1 := newRepoBlobStore(t, ctx, "rr_lbbs_bs1")
	bsID2 := newRepoBlobStore(t, ctx, "rr_lbbs_bs2")

	repo := NewRepositoryRepo(pool)

	// Two repos in bs1, one in bs2.
	r1 := makeRepo("rr_lbbs_1", domain.FormatRaw, domain.TypeHosted, strPtr(bsID1))
	r2 := makeRepo("rr_lbbs_2", domain.FormatNPM, domain.TypeHosted, strPtr(bsID1))
	r3 := makeRepo("rr_lbbs_3", domain.FormatDocker, domain.TypeHosted, strPtr(bsID2))
	for _, r := range []*domain.Repository{r1, r2, r3} {
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("Create %q: %v", r.Name, err)
		}
	}

	list, err := repo.ListByBlobStoreID(ctx, bsID1)
	if err != nil {
		t.Fatalf("ListByBlobStoreID: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListByBlobStoreID: got %d, want 2", len(list))
	}
	for _, r := range list {
		if r.BlobStoreID == nil || *r.BlobStoreID != bsID1 {
			t.Errorf("unexpected BlobStoreID %v in result", r.BlobStoreID)
		}
	}
}

func TestRepositoryRepo_ListByBlobStoreID_NoneMatch(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewRepositoryRepo(pool)

	const missingBlobStoreID = "00000000-0000-0000-0000-000000000003"
	list, err := repo.ListByBlobStoreID(ctx, missingBlobStoreID)
	if err != nil {
		t.Fatalf("ListByBlobStoreID(missing): %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty, got %d", len(list))
	}
}

// ── Group repo member_names (FormatConfig) ────────────────────────────────────

func TestRepositoryRepo_Create_GroupMemberNames_RoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()

	repo := NewRepositoryRepo(pool)

	// Create two member repos first.
	m1 := makeRepo("rr_grp_member1", domain.FormatRaw, domain.TypeHosted, nil)
	m2 := makeRepo("rr_grp_member2", domain.FormatRaw, domain.TypeHosted, nil)
	for _, r := range []*domain.Repository{m1, m2} {
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("Create member %q: %v", r.Name, err)
		}
	}

	// Group repo carries member_names in FormatConfig (JSON).
	grp := makeRepo("rr_grp_group", domain.FormatRaw, domain.TypeGroup, nil)
	grp.FormatConfig = map[string]any{
		"member_names": []any{m1.Name, m2.Name},
	}
	if err := repo.Create(ctx, grp); err != nil {
		t.Fatalf("Create group: %v", err)
	}

	got, err := repo.Get(ctx, grp.Name)
	if err != nil {
		t.Fatalf("Get group: %v", err)
	}
	if got == nil {
		t.Fatal("Get group returned nil")
	}
	members := domain.GroupMemberNames(got)
	if len(members) != 2 {
		t.Fatalf("GroupMemberNames: got %d, want 2: %v", len(members), members)
	}
	memberSet := map[string]bool{members[0]: true, members[1]: true}
	if !memberSet[m1.Name] || !memberSet[m2.Name] {
		t.Errorf("member names mismatch: got %v, want [%s %s]", members, m1.Name, m2.Name)
	}
}

// ── BlobStoreID FK association ────────────────────────────────────────────────

func TestRepositoryRepo_BlobStoreID_FK_Association(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	bsID := newRepoBlobStore(t, ctx, "rr_fk_bs")

	repo := NewRepositoryRepo(pool)
	r := makeRepo("rr_fk_repo", domain.FormatHelm, domain.TypeHosted, strPtr(bsID))
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Query by blob store ID — confirms the FK is properly stored.
	list, err := repo.ListByBlobStoreID(ctx, bsID)
	if err != nil {
		t.Fatalf("ListByBlobStoreID: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListByBlobStoreID: got %d, want 1", len(list))
	}
	if list[0].Name != r.Name {
		t.Errorf("Name: got %q, want %q", list[0].Name, r.Name)
	}
}
