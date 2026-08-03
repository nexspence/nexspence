package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// rawGroup builds a stored raw group repository over the given members.
func rawGroup(members ...string) *domain.Repository {
	return &domain.Repository{
		ID: "g1", Name: "raw-group", Format: domain.FormatRaw, Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": members},
	}
}

func TestRepositoryService_Update_ChangesGroupMembers(t *testing.T) {
	repos := testutil.NewRepoRepo(
		rawGroup("raw-a"),
		testutil.SimpleRepo("raw-a", "raw"),
		testutil.SimpleRepo("raw-b", "raw"),
	)
	svc := newRepoSvcFull(repos, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	got, err := svc.Update(context.Background(), "raw-group", &domain.Repository{
		FormatConfig: map[string]any{"member_names": []string{"raw-b", "raw-a"}},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"raw-b", "raw-a"}, domain.GroupMemberNames(got), "member order is the resolution order")

	stored, err := repos.Get(context.Background(), "raw-group")
	require.NoError(t, err)
	assert.Equal(t, []string{"raw-b", "raw-a"}, domain.GroupMemberNames(stored))
}

func TestRepositoryService_Update_RejectsEmptyGroupMembers(t *testing.T) {
	repos := testutil.NewRepoRepo(rawGroup("raw-a"), testutil.SimpleRepo("raw-a", "raw"))
	svc := newRepoSvcFull(repos, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	_, err := svc.Update(context.Background(), "raw-group", &domain.Repository{
		FormatConfig: map[string]any{"member_names": []string{}},
	})

	require.ErrorIs(t, err, service.ErrInvalidInput)
}

func TestRepositoryService_Update_RejectsGroupMemberOfOtherFormat(t *testing.T) {
	repos := testutil.NewRepoRepo(
		rawGroup("raw-a"),
		testutil.SimpleRepo("raw-a", "raw"),
		testutil.SimpleRepo("mvn-a", "maven2"),
	)
	svc := newRepoSvcFull(repos, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	_, err := svc.Update(context.Background(), "raw-group", &domain.Repository{
		FormatConfig: map[string]any{"member_names": []string{"mvn-a"}},
	})

	require.ErrorIs(t, err, service.ErrInvalidInput)
}

func TestRepositoryService_Update_RejectsGroupMemberSelfReference(t *testing.T) {
	repos := testutil.NewRepoRepo(rawGroup("raw-a"), testutil.SimpleRepo("raw-a", "raw"))
	svc := newRepoSvcFull(repos, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	_, err := svc.Update(context.Background(), "raw-group", &domain.Repository{
		FormatConfig: map[string]any{"member_names": []string{"raw-group"}},
	})

	require.ErrorIs(t, err, service.ErrInvalidInput)
}

func TestRepositoryService_Update_RejectsDuplicateGroupMembers(t *testing.T) {
	repos := testutil.NewRepoRepo(rawGroup("raw-a"), testutil.SimpleRepo("raw-a", "raw"))
	svc := newRepoSvcFull(repos, testutil.NewBlobStoreRepo(), testutil.NewCleanupPolicyRepo())

	_, err := svc.Update(context.Background(), "raw-group", &domain.Repository{
		FormatConfig: map[string]any{"member_names": []string{"raw-a", "raw-a"}},
	})

	require.ErrorIs(t, err, service.ErrInvalidInput)
}
