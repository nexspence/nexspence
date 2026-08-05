package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// The global auth.anonymous_enabled switch gates every anonymous path in the
// service. A repository that opted into AllowAnonymous must still be refused
// once the operator turns the switch off — otherwise the setting is a control
// that does nothing.

func TestRBAC_CanAccessRepo_AnonymousReadDeniedWhenGloballyDisabled(t *testing.T) {
	svc := newRBACTestSvcAnon(false, nil)
	repo := &domain.Repository{Name: "public-repo", Format: domain.FormatRaw, AllowAnonymous: true}

	ok, err := svc.CanAccessRepo(context.Background(), "", nil, repo, "/file.txt", "read")
	require.NoError(t, err)
	assert.False(t, ok, "global switch off must override the per-repository opt-in")
}

func TestRBAC_CanAccessRepo_PrivilegesStillWorkWhenAnonymousDisabled(t *testing.T) {
	// The switch only governs anonymous access; an authenticated user with a
	// matching privilege is unaffected.
	privs := []repository.PrivilegeWithSelector{
		{Actions: []string{"read"}, Expression: `repository == "public-repo"`},
	}
	svc := newRBACTestSvcAnon(false, privs)
	repo := &domain.Repository{Name: "public-repo", Format: domain.FormatRaw, AllowAnonymous: true}

	ok, err := svc.CanAccessRepo(context.Background(), "user1", nil, repo, "/file.txt", "read")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRBAC_FilterRepos_AnonymousReposHiddenWhenGloballyDisabled(t *testing.T) {
	repos := []domain.Repository{
		{Name: "public", AllowAnonymous: true},
		{Name: "private"},
	}
	svc := newRBACTestSvcAnon(false, nil)

	got := svc.FilterRepos(context.Background(), "", nil, repos)
	assert.Empty(t, got)
}

func TestRBAC_FilterPaths_AnonymousDeniedWhenGloballyDisabled(t *testing.T) {
	svc := newRBACTestSvcAnon(false, nil)
	paths := []string{"/a", "/b"}

	got := svc.FilterPaths(context.Background(), "", nil, "repo", true, paths)
	assert.Empty(t, got)
}

func TestRBAC_FilterDockerRows_AnonymousDeniedWhenGloballyDisabled(t *testing.T) {
	rows := []domain.DockerBrowseRow{
		{ImageName: "img1", SamplePath: "/v2/img1/manifests/latest"},
	}
	svc := newRBACTestSvcAnon(false, nil)

	got := svc.FilterDockerRows(context.Background(), "", nil, "repo", true, rows)
	assert.Empty(t, got)
}

func TestRBAC_FilterComponents_AnonymousDeniedWhenGloballyDisabled(t *testing.T) {
	items := []domain.Component{
		{Repository: "public", Name: "pkg"},
	}
	svc := newRBACTestSvcAnon(false, nil)

	got := svc.FilterComponents(context.Background(), "", nil, items, map[string]bool{"public": true})
	assert.Empty(t, got)
}

func TestRBAC_FilterAssets_AnonymousDeniedWhenGloballyDisabled(t *testing.T) {
	items := []domain.Asset{
		{Repository: "public", Path: "/pkg/file.txt"},
	}
	svc := newRBACTestSvcAnon(false, nil)

	got := svc.FilterAssets(context.Background(), "", nil, items, map[string]bool{"public": true})
	assert.Empty(t, got)
}

// Admins are an orthogonal concern: the switch governs anonymous access only
// and must not change what an administrator sees.
func TestRBAC_AdminUnaffectedWhenAnonymousDisabled(t *testing.T) {
	svc := newRBACTestSvcAnon(false, nil)
	repo := &domain.Repository{Name: "public-repo", Format: domain.FormatRaw, AllowAnonymous: true}

	ok, err := svc.CanAccessRepo(context.Background(), "u1", []string{"nx-admin"}, repo, "/file.txt", "read")
	require.NoError(t, err)
	assert.True(t, ok)

	repos := []domain.Repository{{Name: "public", AllowAnonymous: true}}
	assert.Len(t, svc.FilterRepos(context.Background(), "u1", []string{"nx-admin"}, repos), 1)
}
