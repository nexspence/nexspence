package service

import (
	"context"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// RBACService checks whether a user may access a repository resource.
type RBACService struct {
	rbac  repository.RBACRepo
	repos repository.RepositoryRepo
	log   logger.Logger
}

func NewRBACService(rbac repository.RBACRepo, repos repository.RepositoryRepo, log logger.Logger) *RBACService {
	return &RBACService{rbac: rbac, repos: repos, log: log}
}

// CanAccessRepo checks whether the user (identified by userID + roles) may perform action
// on the given repo at path. repo must be pre-loaded by the caller.
// action: "read" | "browse" | "write" | "delete"
func (s *RBACService) CanAccessRepo(ctx context.Context, userID string, roles []string, repo *domain.Repository, path, action string) (bool, error) {
	if isAdmin(roles) {
		return true, nil
	}
	if repo.AllowAnonymous && isReadAction(action) {
		return true, nil
	}
	if userID == "" {
		return false, nil
	}
	privs, err := s.rbac.GetUserPrivilegesWithSelectors(ctx, userID)
	if err != nil {
		return false, err
	}
	checkPath := path
	if repo.Format == domain.FormatDocker {
		checkPath = assetSamplePath(path)
	}
	return matchPrivileges(privs, repo.Name, checkPath, action), nil
}

// FilterRepos returns only repos the user can read. Loads privileges once.
// Note: if user has no privileges and repos aren't public, returns empty list (user must contact admin).
func (s *RBACService) FilterRepos(ctx context.Context, userID string, roles []string, repos []domain.Repository) []domain.Repository {
	if isAdmin(roles) {
		return repos
	}
	var privs []repository.PrivilegeWithSelector
	if userID != "" {
		var err error
		privs, err = s.rbac.GetUserPrivilegesWithSelectors(ctx, userID)
		if err != nil {
			s.log.Warnw("failed to load privileges for user", "userID", userID, "err", err)
		}
		s.log.Infow("rbac filter", "userID", userID, "privCount", len(privs), "repoCount", len(repos))
	}
	result := []domain.Repository{}
	for _, repo := range repos {
		// For repo listing: check repo-level access only, ignoring path restrictions.
		// Path restrictions are enforced at artifact download time via CanAccessRepo.
		if repo.AllowAnonymous || matchPrivilegesRepoOnly(privs, repo.Name) {
			result = append(result, repo)
		}
	}
	return result
}

// ── helpers ──────────────────────────────────────────────────────────────────

func isAdmin(roles []string) bool {
	for _, r := range roles {
		if r == "nx-admin" {
			return true
		}
	}
	return false
}

func isReadAction(action string) bool {
	return action == "read" || action == "browse"
}

func matchPrivileges(privs []repository.PrivilegeWithSelector, repoName, path, action string) bool {
	for _, p := range privs {
		if !actionAllowed(p.Actions, action) {
			continue
		}
		if evalCEL(p.Expression, repoName, path) {
			return true
		}
	}
	return false
}

func actionAllowed(allowed []string, action string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == action {
			return true
		}
	}
	return false
}

// evalCEL evaluates the two CEL patterns we generate without external library.
// Unknown expressions → false (safe deny).
func evalCEL(expr, repoName, path string) bool {
	expr = strings.TrimSpace(expr)
	if idx := strings.Index(expr, " && "); idx >= 0 {
		return evalRepoClause(strings.TrimSpace(expr[:idx]), repoName) &&
			evalPathClause(strings.TrimSpace(expr[idx+4:]), path)
	}
	if strings.HasPrefix(expr, "repository") {
		return evalRepoClause(expr, repoName)
	}
	if strings.HasPrefix(expr, "path") {
		return evalPathClause(expr, path)
	}
	return false
}

func evalRepoClause(expr, repoName string) bool {
	// repository == "X"
	s, e := strings.Index(expr, `"`), strings.LastIndex(expr, `"`)
	if s < 0 || e <= s {
		return false
	}
	return repoName == expr[s+1:e]
}

func evalPathClause(expr, path string) bool {
	// path.startsWith("Y")
	s, e := strings.Index(expr, `"`), strings.LastIndex(expr, `"`)
	if s < 0 || e <= s {
		return false
	}
	return strings.HasPrefix(path, expr[s+1:e])
}

// FilterPaths returns only the paths accessible to the user in the given repo.
// Used by PathTree browse endpoint to hide assets the user cannot read.
func (s *RBACService) FilterPaths(ctx context.Context, userID string, roles []string, repoName string, allowAnonymous bool, paths []string) []string {
	if isAdmin(roles) || allowAnonymous {
		return paths
	}
	var privs []repository.PrivilegeWithSelector
	if userID != "" {
		var err error
		privs, err = s.rbac.GetUserPrivilegesWithSelectors(ctx, userID)
		if err != nil {
			s.log.Warnw("failed to load privileges for path filter", "userID", userID, "err", err)
		}
	}
	result := []string{}
	for _, p := range paths {
		if matchPrivileges(privs, repoName, p, "browse") {
			result = append(result, p)
		}
	}
	return result
}

// FilterDockerRows returns only the docker browse rows accessible to the user.
// Access is checked at the image level: if the user can access ANY path of a given
// image (e.g. a blob), all rows for that image (Blobs, Manifests, Tags) are returned.
// This matches Docker semantics where access is granted per image, not per layer type.
func (s *RBACService) FilterDockerRows(ctx context.Context, userID string, roles []string, repoName string, allowAnonymous bool, rows []domain.DockerBrowseRow) []domain.DockerBrowseRow {
	if isAdmin(roles) || allowAnonymous {
		return rows
	}
	var privs []repository.PrivilegeWithSelector
	if userID != "" {
		var err error
		privs, err = s.rbac.GetUserPrivilegesWithSelectors(ctx, userID)
		if err != nil {
			s.log.Warnw("failed to load privileges for docker browse", "userID", userID, "err", err)
		}
	}

	// Group rows by image name.
	byImage := make(map[string][]domain.DockerBrowseRow)
	order := []string{}
	for _, row := range rows {
		if _, seen := byImage[row.ImageName]; !seen {
			order = append(order, row.ImageName)
		}
		byImage[row.ImageName] = append(byImage[row.ImageName], row)
	}

	// Include all rows for an image if ANY of its paths is accessible.
	result := []domain.DockerBrowseRow{}
	for _, imageName := range order {
		imageRows := byImage[imageName]
		for _, row := range imageRows {
			if matchPrivileges(privs, repoName, assetSamplePath(row.SamplePath), "browse") {
				result = append(result, imageRows...)
				break
			}
		}
	}
	return result
}

// FilterComponents returns only the components the user may browse.
// allowAnonByRepo maps repository-name → AllowAnonymous (caller pre-loads this).
// Sample path for content-selector matching uses "/<name>/" so that a Docker
// selector path.startsWith("/da/bas/") matches component name "da/bas/python".
func (s *RBACService) FilterComponents(
	ctx context.Context,
	userID string, roles []string,
	items []domain.Component,
	allowAnonByRepo map[string]bool,
) []domain.Component {
	if isAdmin(roles) {
		return items
	}
	var privs []repository.PrivilegeWithSelector
	if userID != "" {
		var err error
		privs, err = s.rbac.GetUserPrivilegesWithSelectors(ctx, userID)
		if err != nil {
			s.log.Warnw("failed to load privileges for component filter", "userID", userID, "err", err)
		}
	}
	result := make([]domain.Component, 0, len(items))
	for _, comp := range items {
		if allowAnonByRepo[comp.Repository] {
			result = append(result, comp)
			continue
		}
		if userID == "" {
			continue
		}
		if matchPrivileges(privs, comp.Repository, "/"+comp.Name+"/", "browse") {
			result = append(result, comp)
		}
	}
	return result
}

// FilterAssets returns only the assets the user may browse.
// For Docker blobs/manifests the stored path (/blobs/da/bas/python/sha256:…) is
// converted to an image-namespace path (/da/bas/python/) before matching so that
// content selectors written for the dockerpath format work correctly.
func (s *RBACService) FilterAssets(
	ctx context.Context,
	userID string, roles []string,
	items []domain.Asset,
	allowAnonByRepo map[string]bool,
) []domain.Asset {
	if isAdmin(roles) {
		return items
	}
	var privs []repository.PrivilegeWithSelector
	if userID != "" {
		var err error
		privs, err = s.rbac.GetUserPrivilegesWithSelectors(ctx, userID)
		if err != nil {
			s.log.Warnw("failed to load privileges for asset filter", "userID", userID, "err", err)
		}
	}
	result := make([]domain.Asset, 0, len(items))
	for _, a := range items {
		if allowAnonByRepo[a.Repository] {
			result = append(result, a)
			continue
		}
		if userID == "" {
			continue
		}
		if matchPrivileges(privs, a.Repository, assetSamplePath(a.Path), "browse") {
			result = append(result, a)
		}
	}
	return result
}

// assetSamplePath converts a Docker path into a path suitable for content-selector
// matching. Handles two forms:
//
//  1. Docker v2 API request paths: /v2/<image>/blobs/... or /v2/<image>/manifests/...
//     These arrive from RBACMiddleware when Docker push goes through
//     /repository/:repoName/v2/<image>/blobs/uploads/<uuid>.
//     Strip /v2/ and the endpoint keyword to get /<image>/.
//
//  2. Stored asset DB paths: /blobs/<image>/<digest> or /manifests/<image>/<ref>
//     Strip the leading prefix and the final segment to get /<image>/.
func assetSamplePath(p string) string {
	if strings.HasPrefix(p, "/v2/") {
		rest := strings.TrimPrefix(p, "/v2/")
		for _, kw := range []string{"/manifests/", "/blobs/", "/tags/"} {
			if idx := strings.Index(rest, kw); idx >= 0 {
				return "/" + rest[:idx] + "/"
			}
		}
		return p
	}
	for _, pfx := range []string{"/blobs/", "/manifests/"} {
		if strings.HasPrefix(p, pfx) {
			rest := strings.TrimPrefix(p, pfx)
			if idx := strings.LastIndex(rest, "/"); idx > 0 {
				return "/" + rest[:idx] + "/"
			}
		}
	}
	return p
}

// matchPrivilegesRepoOnly checks if any privilege grants read access to the given
// repository, ignoring path restrictions. Used only for the repository list view —
// path restrictions are enforced by CanAccessRepo at artifact-download time.
func matchPrivilegesRepoOnly(privs []repository.PrivilegeWithSelector, repoName string) bool {
	for _, p := range privs {
		if !actionAllowed(p.Actions, "read") {
			continue
		}
		if evalCELRepoOnly(p.Expression, repoName) {
			return true
		}
	}
	return false
}

// evalCELRepoOnly evaluates only the repository part of a CEL expression,
// stripping any path clause. A path-only selector is treated as matching all repos.
func evalCELRepoOnly(expr, repoName string) bool {
	expr = strings.TrimSpace(expr)
	// Compound "repo && path": evaluate only the repo clause.
	if idx := strings.Index(expr, " && "); idx >= 0 {
		expr = strings.TrimSpace(expr[:idx])
	}
	if strings.HasPrefix(expr, "repository") {
		return evalRepoClause(expr, repoName)
	}
	// Path-only selector: user has access to some artifact(s) — show repo in list.
	if strings.HasPrefix(expr, "path") {
		return true
	}
	return false
}
