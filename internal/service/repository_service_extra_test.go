package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// newRepoSvcFull builds a RepositoryService with pre-seeded repos, blob stores, and cleanup policies.
func newRepoSvcFull(
	repos *testutil.RepoRepo,
	blobs *testutil.BlobStoreRepo,
	policies *testutil.CleanupPolicyRepo,
) *service.RepositoryService {
	return service.NewRepositoryService(repos, blobs, testutil.NewBlobStore(), policies)
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestRepositoryService_List_Empty(t *testing.T) {
	svc := newRepoSvc()
	repos, err := svc.List(context.Background(), "", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(repos) != 0 {
		t.Errorf("expected 0 repos, got %d", len(repos))
	}
}

func TestRepositoryService_List_Populated(t *testing.T) {
	r1 := testutil.SimpleRepo("repo-a", "raw")
	r2 := testutil.SimpleRepo("repo-b", "maven2")
	svc := newRepoSvcFull(testutil.NewRepoRepo(r1, r2), testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	repos, err := svc.List(context.Background(), "", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(repos) != 2 {
		t.Errorf("expected 2 repos, got %d", len(repos))
	}
}

func TestRepositoryService_List_PropagatesRepoError(t *testing.T) {
	repoRepo := testutil.NewRepoRepo()
	repoRepo.Err = errors.New("db down")
	svc := newRepoSvcFull(repoRepo, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	_, err := svc.List(context.Background(), "", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestRepositoryService_Delete_Success(t *testing.T) {
	r := testutil.SimpleRepo("to-delete", "raw")
	repoRepo := testutil.NewRepoRepo(r)
	svc := newRepoSvcFull(repoRepo, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	if err := svc.Delete(context.Background(), "to-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Verify the repo is gone (repo layer now reports not-found via ErrNotFound)
	got, err := repoRepo.Get(context.Background(), "to-delete")
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("Get after delete: expected ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Error("expected repo to be deleted, still exists")
	}
}

func TestRepositoryService_Delete_NotFound(t *testing.T) {
	svc := newRepoSvc()
	err := svc.Delete(context.Background(), "ghost")
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// ── validateGroupMembers via Create ──────────────────────────────────────────

func TestRepositoryService_Create_Group_MemberNotFound(t *testing.T) {
	svc := newRepoSvc()
	err := svc.Create(context.Background(), &domain.Repository{
		Name:   "my-group",
		Format: domain.FormatRaw,
		Type:   domain.TypeGroup,
		FormatConfig: map[string]any{
			"member_names": []string{"nonexistent"},
		},
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput (member not found), got %v", err)
	}
}

func TestRepositoryService_Create_Group_MemberFormatMismatch(t *testing.T) {
	member := testutil.SimpleRepo("maven-repo", "maven2")
	svc := newRepoSvcFull(
		testutil.NewRepoRepo(member),
		testutil.NewBlobStoreRepo(),
		testutil.NewCleanupPolicyRepo(),
	)
	err := svc.Create(context.Background(), &domain.Repository{
		Name:   "raw-group",
		Format: domain.FormatRaw,
		Type:   domain.TypeGroup,
		FormatConfig: map[string]any{
			"member_names": []string{"maven-repo"},
		},
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput (format mismatch), got %v", err)
	}
}

func TestRepositoryService_Create_Group_MemberIsGroup(t *testing.T) {
	member := &domain.Repository{
		ID:     "repo-inner-group",
		Name:   "inner-group",
		Format: domain.FormatRaw,
		Type:   domain.TypeGroup,
		Online: true,
	}
	svc := newRepoSvcFull(
		testutil.NewRepoRepo(member),
		testutil.NewBlobStoreRepo(),
		testutil.NewCleanupPolicyRepo(),
	)
	err := svc.Create(context.Background(), &domain.Repository{
		Name:   "outer-group",
		Format: domain.FormatRaw,
		Type:   domain.TypeGroup,
		FormatConfig: map[string]any{
			"member_names": []string{"inner-group"},
		},
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput (member is group), got %v", err)
	}
}

func TestRepositoryService_Create_Group_NoMembers(t *testing.T) {
	svc := newRepoSvc()
	err := svc.Create(context.Background(), &domain.Repository{
		Name:         "empty-group",
		Format:       domain.FormatRaw,
		Type:         domain.TypeGroup,
		FormatConfig: map[string]any{},
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput (no members), got %v", err)
	}
}

func TestRepositoryService_Create_Group_Valid(t *testing.T) {
	member := testutil.SimpleRepo("raw-hosted", "raw")
	svc := newRepoSvcFull(
		testutil.NewRepoRepo(member),
		testutil.NewBlobStoreRepo(),
		testutil.NewCleanupPolicyRepo(),
	)
	err := svc.Create(context.Background(), &domain.Repository{
		Name:   "raw-group",
		Format: domain.FormatRaw,
		Type:   domain.TypeGroup,
		FormatConfig: map[string]any{
			"member_names": []string{"raw-hosted"},
		},
	})
	if err != nil {
		t.Fatalf("Create valid group: %v", err)
	}
}

// ── validateCleanupPolicies via Create ────────────────────────────────────────

func TestRepositoryService_Create_CleanupPolicy_NotFound(t *testing.T) {
	svc := newRepoSvc()
	err := svc.Create(context.Background(), &domain.Repository{
		Name:             "r1",
		Format:           domain.FormatRaw,
		Type:             domain.TypeHosted,
		CleanupPolicyIDs: []string{"ghost-policy-id"},
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput (policy not found), got %v", err)
	}
}

func TestRepositoryService_Create_CleanupPolicy_FormatMismatch(t *testing.T) {
	policy := &domain.CleanupPolicy{
		ID:     "policy-maven",
		Name:   "maven-cleanup",
		Format: "maven2",
	}
	svc := newRepoSvcFull(
		testutil.NewRepoRepo(),
		testutil.NewBlobStoreRepo(),
		testutil.NewCleanupPolicyRepo(policy),
	)
	err := svc.Create(context.Background(), &domain.Repository{
		Name:             "raw-repo",
		Format:           domain.FormatRaw,
		Type:             domain.TypeHosted,
		CleanupPolicyIDs: []string{"policy-maven"},
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput (format mismatch), got %v", err)
	}
}

func TestRepositoryService_Create_CleanupPolicy_WildcardFormat_Allowed(t *testing.T) {
	policy := &domain.CleanupPolicy{
		ID:     "policy-all",
		Name:   "all-cleanup",
		Format: "*",
	}
	svc := newRepoSvcFull(
		testutil.NewRepoRepo(),
		testutil.NewBlobStoreRepo(),
		testutil.NewCleanupPolicyRepo(policy),
	)
	err := svc.Create(context.Background(), &domain.Repository{
		Name:             "raw-repo2",
		Format:           domain.FormatRaw,
		Type:             domain.TypeHosted,
		CleanupPolicyIDs: []string{"policy-all"},
	})
	if err != nil {
		t.Fatalf("Create with wildcard policy: %v", err)
	}
}

// ── Update error branches ────────────────────────────────────────────────────

func TestRepositoryService_Update_NotFound(t *testing.T) {
	svc := newRepoSvc()
	_, err := svc.Update(context.Background(), "ghost", &domain.Repository{})
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRepositoryService_Update_BlobStoreNotFound(t *testing.T) {
	r := testutil.SimpleRepo("my-repo", "raw")
	svc := newRepoSvcFull(
		testutil.NewRepoRepo(r),
		testutil.NewBlobStoreRepo(), // no store with ID "bad-id"
		testutil.NewCleanupPolicyRepo(),
	)
	_, err := svc.Update(context.Background(), "my-repo", &domain.Repository{
		BlobStoreID: strPtr("bad-blob-id"),
	})
	if !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("want ErrNotFound (blob store), got %v", err)
	}
}

func TestRepositoryService_Update_CleanupPolicyInvalidFormat(t *testing.T) {
	r := testutil.SimpleRepo("my-repo2", "raw")
	policy := &domain.CleanupPolicy{
		ID:     "policy-npm",
		Name:   "npm-policy",
		Format: "npm",
	}
	svc := newRepoSvcFull(
		testutil.NewRepoRepo(r),
		testutil.NewBlobStoreRepo(),
		testutil.NewCleanupPolicyRepo(policy),
	)
	_, err := svc.Update(context.Background(), "my-repo2", &domain.Repository{
		CleanupPolicyIDs: []string{"policy-npm"},
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput (policy format), got %v", err)
	}
}

func TestRepositoryService_Update_GroupMemberValidationFails(t *testing.T) {
	group := &domain.Repository{
		ID:     "repo-group",
		Name:   "my-group",
		Format: domain.FormatRaw,
		Type:   domain.TypeGroup,
		Online: true,
		FormatConfig: map[string]any{
			"member_names": []string{"raw-hosted"},
		},
	}
	member := testutil.SimpleRepo("raw-hosted", "raw")
	svc := newRepoSvcFull(
		testutil.NewRepoRepo(group, member),
		testutil.NewBlobStoreRepo(),
		testutil.NewCleanupPolicyRepo(),
	)

	// Update group to point to a non-existent member
	_, err := svc.Update(context.Background(), "my-group", &domain.Repository{
		FormatConfig: map[string]any{
			"member_names": []string{"does-not-exist"},
		},
	})
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput (group member validation), got %v", err)
	}
}
