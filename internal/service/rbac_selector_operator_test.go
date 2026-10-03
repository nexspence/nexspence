package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

type selectorRBACRepo struct{ expr string }

func (s selectorRBACRepo) GetUserPrivilegesWithSelectors(context.Context, string) ([]repository.PrivilegeWithSelector, error) {
	return []repository.PrivilegeWithSelector{{Actions: []string{"read"}, Expression: s.expr}}, nil
}

func canRead(expr, repoName, path string) bool {
	repo := &domain.Repository{Name: repoName, Format: domain.FormatRaw, Type: domain.TypeHosted}
	svc := service.NewRBACService(selectorRBACRepo{expr: expr}, testutil.NewRepoRepo(repo), zap.NewNop().Sugar(), true)
	ok, _ := svc.CanAccessRepo(context.Background(), "u1", []string{"dev"}, repo, path, "read")
	return ok
}

func canList(expr, repoName string) bool {
	repo := domain.Repository{Name: repoName, Format: domain.FormatRaw, Type: domain.TypeHosted}
	svc := service.NewRBACService(selectorRBACRepo{expr: expr}, testutil.NewRepoRepo(&repo), zap.NewNop().Sugar(), true)
	return len(svc.FilterRepos(context.Background(), "u1", []string{"dev"}, []domain.Repository{repo})) == 1
}

// GHSA-vpvp-9379-86x6: the evaluator read the quoted value and ignored the
// operator, so an exclusion granted exactly what it excluded. Anything but the
// two supported shapes is denied.
func TestSelector_UnsupportedOperatorsAreDenied(t *testing.T) {
	for _, tc := range []struct{ expr, repo, path string }{
		{`repository != "raw-priv"`, "raw-priv", "/a.txt"},
		{`repository != "raw-priv"`, "other", "/a.txt"},
		{`repository == "raw-priv" && !path.startsWith("/internal/")`, "raw-priv", "/internal/x"},
		{`repository == "raw-priv" && !path.startsWith("/internal/")`, "raw-priv", "/public/x"},
		{`!path.startsWith("/internal/")`, "raw-priv", "/internal/x"},
		{`path.endsWith("/internal/")`, "raw-priv", "/internal/"},
		{`repository == "a" || repository == "raw-priv"`, "raw-priv", "/x"},
		{`repository.startsWith("raw-priv")`, "raw-priv", "/x"},
		{`repository == "raw-priv" && path.startsWith("/a/") && path.startsWith("/b/")`, "raw-priv", "/a/x"},
	} {
		assert.False(t, canRead(tc.expr, tc.repo, tc.path), "read %s%s under %s", tc.repo, tc.path, tc.expr)
	}
	assert.False(t, canList(`repository != "raw-priv"`, "raw-priv"))
	assert.False(t, canList(`repository != "raw-priv"`, "other"))
	assert.False(t, canList(`!path.startsWith("/internal/")`, "raw-priv"))
}

func TestSelector_SupportedShapesStillGrant(t *testing.T) {
	assert.True(t, canRead(`repository == "raw-priv"`, "raw-priv", "/x"))
	assert.True(t, canRead(`repository=="raw-priv"`, "raw-priv", "/x"))
	assert.False(t, canRead(`repository == "raw-priv"`, "other", "/x"))
	assert.True(t, canRead(`path.startsWith("/team-a/")`, "raw-priv", "/team-a/x"))
	assert.False(t, canRead(`path.startsWith("/team-a/")`, "raw-priv", "/team-b/x"))
	assert.True(t, canRead(`repository == "raw-priv" && path.startsWith("/team-a/")`, "raw-priv", "/team-a/x"))
	assert.False(t, canRead(`repository == "raw-priv" && path.startsWith("/team-a/")`, "raw-priv", "/team-b/x"))
	assert.True(t, canList(`repository == "raw-priv"`, "raw-priv"))
	assert.True(t, canList(`repository == "raw-priv" && path.startsWith("/team-a/")`, "raw-priv"))
	assert.True(t, canList(`path.startsWith("/team-a/")`, "raw-priv"))
}
