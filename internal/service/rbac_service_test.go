package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ── minimal inline RBACRepo mock ─────────────────────────────

type mockRBACRepo struct {
	privs []repository.PrivilegeWithSelector
	err   error
}

func (m *mockRBACRepo) GetUserPrivilegesWithSelectors(_ context.Context, _ string) ([]repository.PrivilegeWithSelector, error) {
	return m.privs, m.err
}

// ── constructor helper ────────────────────────────────────────

func newRBACTestSvc(privs []repository.PrivilegeWithSelector, repos ...*domain.Repository) *service.RBACService {
	rbacRepo := &mockRBACRepo{privs: privs}
	repoRepo := testutil.NewRepoRepo(repos...)
	log := zap.NewNop().Sugar()
	return service.NewRBACService(rbacRepo, repoRepo, log)
}

func newRBACTestSvcWithErr(repoErr error) *service.RBACService {
	rbacRepo := &mockRBACRepo{err: repoErr}
	repoRepo := testutil.NewRepoRepo()
	log := zap.NewNop().Sugar()
	return service.NewRBACService(rbacRepo, repoRepo, log)
}

// ── CanAccessRepo ─────────────────────────────────────────────

func TestRBAC_CanAccessRepo_AdminBypass(t *testing.T) {
	svc := newRBACTestSvc(nil)
	repo := &domain.Repository{Name: "myrepo", Format: domain.FormatRaw}

	ok, err := svc.CanAccessRepo(context.Background(), "u1", []string{"nx-admin"}, repo, "/any/path", "read")
	require.NoError(t, err)
	assert.True(t, ok, "admin must always have access")
}

func TestRBAC_CanAccessRepo_AdminBypass_Write(t *testing.T) {
	svc := newRBACTestSvc(nil)
	repo := &domain.Repository{Name: "myrepo", Format: domain.FormatMaven2}

	ok, err := svc.CanAccessRepo(context.Background(), "u1", []string{"nx-admin"}, repo, "/path", "write")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRBAC_CanAccessRepo_AnonymousReadAllowed(t *testing.T) {
	svc := newRBACTestSvc(nil)
	repo := &domain.Repository{Name: "public-repo", Format: domain.FormatRaw, AllowAnonymous: true}

	ok, err := svc.CanAccessRepo(context.Background(), "", nil, repo, "/file.txt", "read")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRBAC_CanAccessRepo_AnonymousBrowseAllowed(t *testing.T) {
	svc := newRBACTestSvc(nil)
	repo := &domain.Repository{Name: "public-repo", Format: domain.FormatRaw, AllowAnonymous: true}

	ok, err := svc.CanAccessRepo(context.Background(), "", nil, repo, "/", "browse")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRBAC_CanAccessRepo_AnonymousWriteDenied(t *testing.T) {
	// anonymous + write: AllowAnonymous only covers read actions
	svc := newRBACTestSvc(nil)
	repo := &domain.Repository{Name: "public-repo", Format: domain.FormatRaw, AllowAnonymous: true}

	ok, err := svc.CanAccessRepo(context.Background(), "", nil, repo, "/file.txt", "write")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRBAC_CanAccessRepo_NoUserID_Denied(t *testing.T) {
	svc := newRBACTestSvc(nil)
	repo := &domain.Repository{Name: "private-repo", Format: domain.FormatRaw}

	ok, err := svc.CanAccessRepo(context.Background(), "", nil, repo, "/secret.txt", "read")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRBAC_CanAccessRepo_PrivilegeMatch_RepoOnly(t *testing.T) {
	privs := []repository.PrivilegeWithSelector{
		{Actions: []string{"read"}, Expression: `repository == "myrepo"`},
	}
	svc := newRBACTestSvc(privs)
	repo := &domain.Repository{Name: "myrepo", Format: domain.FormatMaven2}

	ok, err := svc.CanAccessRepo(context.Background(), "user1", []string{"developer"}, repo, "/com/example/1.0/app.jar", "read")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRBAC_CanAccessRepo_PrivilegeMatch_WrongRepo_Denied(t *testing.T) {
	privs := []repository.PrivilegeWithSelector{
		{Actions: []string{"read"}, Expression: `repository == "other-repo"`},
	}
	svc := newRBACTestSvc(privs)
	repo := &domain.Repository{Name: "myrepo", Format: domain.FormatMaven2}

	ok, err := svc.CanAccessRepo(context.Background(), "user1", []string{"developer"}, repo, "/path", "read")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRBAC_CanAccessRepo_PrivilegeMatch_RepoAndPath(t *testing.T) {
	privs := []repository.PrivilegeWithSelector{
		{
			Actions:    []string{"read"},
			Expression: `repository == "myrepo" && path.startsWith("/com/example/")`,
		},
	}
	svc := newRBACTestSvc(privs)
	repo := &domain.Repository{Name: "myrepo", Format: domain.FormatMaven2}

	ok, err := svc.CanAccessRepo(context.Background(), "user1", []string{}, repo, "/com/example/1.0/app.jar", "read")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRBAC_CanAccessRepo_PrivilegeMatch_PathPrefixMismatch(t *testing.T) {
	privs := []repository.PrivilegeWithSelector{
		{
			Actions:    []string{"read"},
			Expression: `repository == "myrepo" && path.startsWith("/restricted/")`,
		},
	}
	svc := newRBACTestSvc(privs)
	repo := &domain.Repository{Name: "myrepo", Format: domain.FormatMaven2}

	ok, err := svc.CanAccessRepo(context.Background(), "user1", nil, repo, "/public/app.jar", "read")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRBAC_CanAccessRepo_NoPrivileges_Denied(t *testing.T) {
	svc := newRBACTestSvc(nil)
	repo := &domain.Repository{Name: "myrepo", Format: domain.FormatRaw}

	ok, err := svc.CanAccessRepo(context.Background(), "user1", nil, repo, "/file.txt", "read")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRBAC_CanAccessRepo_RepoError_PropagatesError(t *testing.T) {
	repoErr := errors.New("db error")
	svc := newRBACTestSvcWithErr(repoErr)
	repo := &domain.Repository{Name: "myrepo", Format: domain.FormatRaw}

	_, err := svc.CanAccessRepo(context.Background(), "user1", nil, repo, "/path", "read")
	assert.Error(t, err)
}

func TestRBAC_CanAccessRepo_Docker_PathConversion(t *testing.T) {
	// Docker path /v2/myimage/manifests/latest → CanAccessRepo converts to /myimage/
	privs := []repository.PrivilegeWithSelector{
		{
			Actions:    []string{"read"},
			Expression: `repository == "docker-repo" && path.startsWith("/myimage/")`,
		},
	}
	svc := newRBACTestSvc(privs)
	repo := &domain.Repository{Name: "docker-repo", Format: domain.FormatDocker}

	ok, err := svc.CanAccessRepo(context.Background(), "user1", nil, repo, "/v2/myimage/manifests/latest", "read")
	require.NoError(t, err)
	assert.True(t, ok)
}

// ── actionAllowed (tested via CanAccessRepo) ─────────────────

func TestRBAC_ActionAllowed_EmptyActionsAllowsAll(t *testing.T) {
	// Empty Actions means the privilege covers all actions.
	privs := []repository.PrivilegeWithSelector{
		{Actions: []string{}, Expression: `repository == "myrepo"`},
	}
	svc := newRBACTestSvc(privs)
	repo := &domain.Repository{Name: "myrepo", Format: domain.FormatRaw}

	ok, err := svc.CanAccessRepo(context.Background(), "user1", nil, repo, "/f", "delete")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRBAC_ActionAllowed_WritePrivilegeCannotRead(t *testing.T) {
	privs := []repository.PrivilegeWithSelector{
		{Actions: []string{"write"}, Expression: `repository == "myrepo"`},
	}
	svc := newRBACTestSvc(privs)
	repo := &domain.Repository{Name: "myrepo", Format: domain.FormatRaw}

	ok, err := svc.CanAccessRepo(context.Background(), "user1", nil, repo, "/f", "read")
	require.NoError(t, err)
	assert.False(t, ok)
}

// ── isReadAction (tested via CanAccessRepo + AllowAnonymous) ─

func TestRBAC_IsReadAction_GetIsRead(t *testing.T) {
	svc := newRBACTestSvc(nil)
	repo := &domain.Repository{Name: "pub", AllowAnonymous: true}

	ok, err := svc.CanAccessRepo(context.Background(), "", nil, repo, "/f", "read")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRBAC_IsReadAction_BrowseIsRead(t *testing.T) {
	svc := newRBACTestSvc(nil)
	repo := &domain.Repository{Name: "pub", AllowAnonymous: true}

	ok, err := svc.CanAccessRepo(context.Background(), "", nil, repo, "/", "browse")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRBAC_IsReadAction_WriteIsNotRead(t *testing.T) {
	svc := newRBACTestSvc(nil)
	repo := &domain.Repository{Name: "pub", AllowAnonymous: true}

	// write is not a read action → AllowAnonymous doesn't help → no userID → denied
	ok, err := svc.CanAccessRepo(context.Background(), "", nil, repo, "/f", "write")
	require.NoError(t, err)
	assert.False(t, ok)
}

// ── FilterRepos ───────────────────────────────────────────────

func TestRBAC_FilterRepos_AdminGetsAll(t *testing.T) {
	repos := []domain.Repository{
		{Name: "a"}, {Name: "b"},
	}
	svc := newRBACTestSvc(nil)

	got := svc.FilterRepos(context.Background(), "u1", []string{"nx-admin"}, repos)
	assert.Len(t, got, 2)
}

func TestRBAC_FilterRepos_AnonymousReposIncluded(t *testing.T) {
	repos := []domain.Repository{
		{Name: "public", AllowAnonymous: true},
		{Name: "private"},
	}
	svc := newRBACTestSvc(nil)

	got := svc.FilterRepos(context.Background(), "", nil, repos)
	require.Len(t, got, 1)
	assert.Equal(t, "public", got[0].Name)
}

func TestRBAC_FilterRepos_PrivilegeGrantsAccess(t *testing.T) {
	privs := []repository.PrivilegeWithSelector{
		{Actions: []string{"read"}, Expression: `repository == "allowed-repo"`},
	}
	repos := []domain.Repository{
		{Name: "allowed-repo"},
		{Name: "other-repo"},
	}
	svc := newRBACTestSvc(privs)

	got := svc.FilterRepos(context.Background(), "user1", nil, repos)
	require.Len(t, got, 1)
	assert.Equal(t, "allowed-repo", got[0].Name)
}

func TestRBAC_FilterRepos_NoPrivilegesNonAdmin(t *testing.T) {
	repos := []domain.Repository{
		{Name: "private1"}, {Name: "private2"},
	}
	svc := newRBACTestSvc(nil)

	got := svc.FilterRepos(context.Background(), "user1", nil, repos)
	assert.Empty(t, got)
}

func TestRBAC_FilterRepos_EmptyInput(t *testing.T) {
	svc := newRBACTestSvc(nil)
	got := svc.FilterRepos(context.Background(), "u1", []string{"nx-admin"}, nil)
	assert.Empty(t, got)
}

func TestRBAC_FilterRepos_PathOnlySelectorShowsRepo(t *testing.T) {
	// path-only expression in evalCELRepoOnly → show repo in list
	privs := []repository.PrivilegeWithSelector{
		{Actions: []string{"read"}, Expression: `path.startsWith("/public/")`},
	}
	repos := []domain.Repository{
		{Name: "some-repo"},
	}
	svc := newRBACTestSvc(privs)

	got := svc.FilterRepos(context.Background(), "user1", nil, repos)
	assert.Len(t, got, 1)
}

// ── FilterPaths ───────────────────────────────────────────────

func TestRBAC_FilterPaths_AdminGetsAll(t *testing.T) {
	paths := []string{"/a", "/b", "/c"}
	svc := newRBACTestSvc(nil)

	got := svc.FilterPaths(context.Background(), "u1", []string{"nx-admin"}, "repo", false, paths)
	assert.Equal(t, paths, got)
}

func TestRBAC_FilterPaths_AllowAnonymousGetsAll(t *testing.T) {
	paths := []string{"/a", "/b"}
	svc := newRBACTestSvc(nil)

	got := svc.FilterPaths(context.Background(), "", nil, "repo", true, paths)
	assert.Equal(t, paths, got)
}

func TestRBAC_FilterPaths_PrivilegeFiltersCorrectly(t *testing.T) {
	privs := []repository.PrivilegeWithSelector{
		{
			Actions:    []string{"browse"},
			Expression: `repository == "myrepo" && path.startsWith("/allowed/")`,
		},
	}
	svc := newRBACTestSvc(privs)

	paths := []string{"/allowed/file.txt", "/denied/secret.txt"}
	got := svc.FilterPaths(context.Background(), "user1", nil, "myrepo", false, paths)
	require.Len(t, got, 1)
	assert.Equal(t, "/allowed/file.txt", got[0])
}

func TestRBAC_FilterPaths_NoPrivilegesEmpty(t *testing.T) {
	svc := newRBACTestSvc(nil)
	paths := []string{"/a", "/b"}

	got := svc.FilterPaths(context.Background(), "user1", nil, "repo", false, paths)
	assert.Empty(t, got)
}

func TestRBAC_FilterPaths_EmptyInput(t *testing.T) {
	svc := newRBACTestSvc(nil)
	got := svc.FilterPaths(context.Background(), "u1", []string{"nx-admin"}, "repo", false, nil)
	assert.Empty(t, got)
}

// ── FilterDockerRows ──────────────────────────────────────────

func TestRBAC_FilterDockerRows_AdminGetsAll(t *testing.T) {
	rows := []domain.DockerBrowseRow{
		{ImageName: "img1", SamplePath: "/v2/img1/manifests/latest"},
		{ImageName: "img2", SamplePath: "/v2/img2/manifests/v1"},
	}
	svc := newRBACTestSvc(nil)

	got := svc.FilterDockerRows(context.Background(), "u1", []string{"nx-admin"}, "docker-repo", false, rows)
	assert.Len(t, got, 2)
}

func TestRBAC_FilterDockerRows_AllowAnonymousGetsAll(t *testing.T) {
	rows := []domain.DockerBrowseRow{
		{ImageName: "img1", SamplePath: "/v2/img1/manifests/latest"},
	}
	svc := newRBACTestSvc(nil)

	got := svc.FilterDockerRows(context.Background(), "", nil, "docker-repo", true, rows)
	assert.Len(t, got, 1)
}

func TestRBAC_FilterDockerRows_PrivilegeFiltersImages(t *testing.T) {
	privs := []repository.PrivilegeWithSelector{
		{
			Actions:    []string{"browse"},
			Expression: `repository == "docker-repo" && path.startsWith("/img1/")`,
		},
	}
	svc := newRBACTestSvc(privs)

	rows := []domain.DockerBrowseRow{
		{ImageName: "img1", SamplePath: "/v2/img1/manifests/latest"},
		{ImageName: "img2", SamplePath: "/v2/img2/manifests/v1"},
	}

	got := svc.FilterDockerRows(context.Background(), "user1", nil, "docker-repo", false, rows)
	require.Len(t, got, 1)
	assert.Equal(t, "img1", got[0].ImageName)
}

func TestRBAC_FilterDockerRows_NoPrivilegesEmpty(t *testing.T) {
	svc := newRBACTestSvc(nil)
	rows := []domain.DockerBrowseRow{
		{ImageName: "img1", SamplePath: "/v2/img1/manifests/latest"},
	}

	got := svc.FilterDockerRows(context.Background(), "user1", nil, "docker-repo", false, rows)
	assert.Empty(t, got)
}

func TestRBAC_FilterDockerRows_EmptyInput(t *testing.T) {
	svc := newRBACTestSvc(nil)
	got := svc.FilterDockerRows(context.Background(), "u1", []string{"nx-admin"}, "docker-repo", false, nil)
	assert.Empty(t, got)
}

func TestRBAC_FilterDockerRows_AllRowsForImageIncluded(t *testing.T) {
	// If ANY row for an image is accessible, ALL rows for that image are returned.
	privs := []repository.PrivilegeWithSelector{
		{
			Actions:    []string{"browse"},
			Expression: `repository == "docker-repo" && path.startsWith("/img1/")`,
		},
	}
	svc := newRBACTestSvc(privs)

	rows := []domain.DockerBrowseRow{
		{ImageName: "img1", SamplePath: "/v2/img1/manifests/latest"},
		{ImageName: "img1", SamplePath: "/v2/img1/blobs/sha256:abc"},
		{ImageName: "img2", SamplePath: "/v2/img2/manifests/v1"},
	}

	got := svc.FilterDockerRows(context.Background(), "user1", nil, "docker-repo", false, rows)
	// Both img1 rows should be returned, img2 excluded.
	assert.Len(t, got, 2)
	for _, r := range got {
		assert.Equal(t, "img1", r.ImageName)
	}
}

// ── FilterComponents ──────────────────────────────────────────

func TestRBAC_FilterComponents_AdminGetsAll(t *testing.T) {
	items := []domain.Component{
		{Repository: "r1", Name: "com.example:app"},
		{Repository: "r2", Name: "com.example:lib"},
	}
	svc := newRBACTestSvc(nil)

	got := svc.FilterComponents(context.Background(), "u1", []string{"nx-admin"}, items, nil)
	assert.Len(t, got, 2)
}

func TestRBAC_FilterComponents_AnonByRepo(t *testing.T) {
	items := []domain.Component{
		{Repository: "public", Name: "a"},
		{Repository: "private", Name: "b"},
	}
	svc := newRBACTestSvc(nil)
	anonMap := map[string]bool{"public": true}

	got := svc.FilterComponents(context.Background(), "", nil, items, anonMap)
	require.Len(t, got, 1)
	assert.Equal(t, "public", got[0].Repository)
}

func TestRBAC_FilterComponents_PrivilegeByName(t *testing.T) {
	privs := []repository.PrivilegeWithSelector{
		{
			Actions:    []string{"browse"},
			Expression: `repository == "myrepo" && path.startsWith("/com/example/")`,
		},
	}
	svc := newRBACTestSvc(privs)

	items := []domain.Component{
		{Repository: "myrepo", Name: "com/example/app"},
		{Repository: "myrepo", Name: "other/lib"},
	}

	got := svc.FilterComponents(context.Background(), "user1", nil, items, nil)
	require.Len(t, got, 1)
	assert.Equal(t, "com/example/app", got[0].Name)
}

func TestRBAC_FilterComponents_NoUserID_AnonNotAllowed(t *testing.T) {
	svc := newRBACTestSvc(nil)
	items := []domain.Component{{Repository: "private", Name: "app"}}

	got := svc.FilterComponents(context.Background(), "", nil, items, nil)
	assert.Empty(t, got)
}

func TestRBAC_FilterComponents_EmptyInput(t *testing.T) {
	svc := newRBACTestSvc(nil)
	got := svc.FilterComponents(context.Background(), "u1", []string{"nx-admin"}, nil, nil)
	assert.Empty(t, got)
}

// ── FilterAssets ─────────────────────────────────────────────

func TestRBAC_FilterAssets_AdminGetsAll(t *testing.T) {
	items := []domain.Asset{
		{Repository: "r1", Path: "/a.jar"},
		{Repository: "r2", Path: "/b.jar"},
	}
	svc := newRBACTestSvc(nil)

	got := svc.FilterAssets(context.Background(), "u1", []string{"nx-admin"}, items, nil)
	assert.Len(t, got, 2)
}

func TestRBAC_FilterAssets_AnonByRepo(t *testing.T) {
	items := []domain.Asset{
		{Repository: "public", Path: "/pub.jar"},
		{Repository: "private", Path: "/sec.jar"},
	}
	svc := newRBACTestSvc(nil)
	anonMap := map[string]bool{"public": true}

	got := svc.FilterAssets(context.Background(), "", nil, items, anonMap)
	require.Len(t, got, 1)
	assert.Equal(t, "public", got[0].Repository)
}

func TestRBAC_FilterAssets_PrivilegePathFilter(t *testing.T) {
	privs := []repository.PrivilegeWithSelector{
		{
			Actions:    []string{"browse"},
			Expression: `repository == "myrepo" && path.startsWith("/allowed/")`,
		},
	}
	svc := newRBACTestSvc(privs)

	items := []domain.Asset{
		{Repository: "myrepo", Path: "/allowed/app.jar"},
		{Repository: "myrepo", Path: "/denied/secret.jar"},
	}

	got := svc.FilterAssets(context.Background(), "user1", nil, items, nil)
	require.Len(t, got, 1)
	assert.Equal(t, "/allowed/app.jar", got[0].Path)
}

func TestRBAC_FilterAssets_DockerPathConversion(t *testing.T) {
	// Docker stored path /blobs/myimage/sha256:abc → converted to /myimage/ for matching.
	privs := []repository.PrivilegeWithSelector{
		{
			Actions:    []string{"browse"},
			Expression: `repository == "docker-repo" && path.startsWith("/myimage/")`,
		},
	}
	svc := newRBACTestSvc(privs)

	items := []domain.Asset{
		{Repository: "docker-repo", Path: "/blobs/myimage/sha256:abcdef"},
	}

	got := svc.FilterAssets(context.Background(), "user1", nil, items, nil)
	assert.Len(t, got, 1)
}

func TestRBAC_FilterAssets_NoUserID_AnonNotAllowed(t *testing.T) {
	svc := newRBACTestSvc(nil)
	items := []domain.Asset{{Repository: "private", Path: "/f.jar"}}

	got := svc.FilterAssets(context.Background(), "", nil, items, nil)
	assert.Empty(t, got)
}

func TestRBAC_FilterAssets_EmptyInput(t *testing.T) {
	svc := newRBACTestSvc(nil)
	got := svc.FilterAssets(context.Background(), "u1", []string{"nx-admin"}, nil, nil)
	assert.Empty(t, got)
}

// ── evalCEL edge cases ────────────────────────────────────────

func TestRBAC_EvalCEL_UnknownExpression_SafeDeny(t *testing.T) {
	privs := []repository.PrivilegeWithSelector{
		{Actions: []string{"read"}, Expression: `unknown_function("x")`},
	}
	svc := newRBACTestSvc(privs)
	repo := &domain.Repository{Name: "myrepo", Format: domain.FormatRaw}

	ok, err := svc.CanAccessRepo(context.Background(), "user1", nil, repo, "/f", "read")
	require.NoError(t, err)
	assert.False(t, ok, "unknown CEL expression must safely deny access")
}

func TestRBAC_EvalCEL_RepoOnlyExpression(t *testing.T) {
	privs := []repository.PrivilegeWithSelector{
		{Actions: []string{"read"}, Expression: `repository == "exact-repo"`},
	}
	svc := newRBACTestSvc(privs)
	repo := &domain.Repository{Name: "exact-repo", Format: domain.FormatRaw}

	ok, err := svc.CanAccessRepo(context.Background(), "u1", nil, repo, "/any", "read")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRBAC_EvalCEL_PathOnlyExpression(t *testing.T) {
	privs := []repository.PrivilegeWithSelector{
		{Actions: []string{"read"}, Expression: `path.startsWith("/public/")`},
	}
	svc := newRBACTestSvc(privs)
	repo := &domain.Repository{Name: "any-repo", Format: domain.FormatRaw}

	ok, err := svc.CanAccessRepo(context.Background(), "u1", nil, repo, "/public/file.txt", "read")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRBAC_EvalCEL_PathOnlyExpression_PrefixMismatch(t *testing.T) {
	privs := []repository.PrivilegeWithSelector{
		{Actions: []string{"read"}, Expression: `path.startsWith("/public/")`},
	}
	svc := newRBACTestSvc(privs)
	repo := &domain.Repository{Name: "any-repo", Format: domain.FormatRaw}

	ok, err := svc.CanAccessRepo(context.Background(), "u1", nil, repo, "/private/file.txt", "read")
	require.NoError(t, err)
	assert.False(t, ok)
}

// ── assetSamplePath (tested indirectly via FilterAssets) ──────

func TestRBAC_AssetSamplePath_ManifestPath(t *testing.T) {
	// /manifests/myimage/tag → /myimage/
	privs := []repository.PrivilegeWithSelector{
		{
			Actions:    []string{"browse"},
			Expression: `repository == "dr" && path.startsWith("/myimage/")`,
		},
	}
	svc := newRBACTestSvc(privs)
	items := []domain.Asset{
		{Repository: "dr", Path: "/manifests/myimage/latest"},
	}
	got := svc.FilterAssets(context.Background(), "user1", nil, items, nil)
	assert.Len(t, got, 1)
}

func TestRBAC_AssetSamplePath_V2Path(t *testing.T) {
	// /v2/myimage/blobs/sha256:... → /myimage/
	privs := []repository.PrivilegeWithSelector{
		{
			Actions:    []string{"browse"},
			Expression: `repository == "dr" && path.startsWith("/myimage/")`,
		},
	}
	svc := newRBACTestSvc(privs)
	items := []domain.Asset{
		{Repository: "dr", Path: "/v2/myimage/blobs/sha256:deadbeef"},
	}
	got := svc.FilterAssets(context.Background(), "user1", nil, items, nil)
	assert.Len(t, got, 1)
}

// ── FilterRepos privilege error handling ──────────────────────

func TestRBAC_FilterRepos_PrivError_LogsAndContinues(t *testing.T) {
	// When privilege loading fails the service logs a warning and returns
	// only public (AllowAnonymous) repos — no panic, no error returned.
	svc := newRBACTestSvcWithErr(errors.New("privilege load failure"))
	repos := []domain.Repository{
		{Name: "public", AllowAnonymous: true},
		{Name: "private"},
	}
	got := svc.FilterRepos(context.Background(), "user1", nil, repos)
	require.Len(t, got, 1)
	assert.Equal(t, "public", got[0].Name)
}
