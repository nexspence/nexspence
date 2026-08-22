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
	// anonymousEnabled mirrors auth.anonymous_enabled. It is the instance-wide
	// switch above every per-repository AllowAnonymous opt-in: when false, no
	// unauthenticated request is served regardless of how a repository is
	// configured.
	anonymousEnabled bool
}

// NewRBACService constructs a service that evaluates repository access decisions.
// anonymousEnabled is the global auth.anonymous_enabled switch.
func NewRBACService(rbac repository.RBACRepo, repos repository.RepositoryRepo, log logger.Logger, anonymousEnabled bool) *RBACService {
	return &RBACService{rbac: rbac, repos: repos, log: log, anonymousEnabled: anonymousEnabled}
}

// CanAccessRepo checks whether the user (identified by userID + roles) may perform action
// on the given repo at path. repo must be pre-loaded by the caller.
// action: "read" | "browse" | "write" | "delete"
func (s *RBACService) CanAccessRepo(ctx context.Context, userID string, roles []string, repo *domain.Repository, path, action string) (bool, error) {
	if isAdmin(roles) {
		return true, nil
	}
	if s.anonymousAllowed(repo.AllowAnonymous) && isReadAction(action) {
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
	// Both labels of the OCI Distribution protocol store /manifests/... and
	// /blobs/... paths, so both need the same normalisation before matching.
	if repo.Format.IsOCIRegistry() {
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
		if s.anonymousAllowed(repo.AllowAnonymous) || matchPrivilegesRepoOnly(privs, repo.Name) {
			result = append(result, repo)
		}
	}
	return result
}

// ── helpers ──────────────────────────────────────────────────────────────────

// anonymousAllowed reports whether an unauthenticated request may be served for
// a repository whose AllowAnonymous flag is repoAllows. Both the instance-wide
// switch and the per-repository opt-in must agree.
func (s *RBACService) anonymousAllowed(repoAllows bool) bool {
	return s.anonymousEnabled && repoAllows
}

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
	if isAdmin(roles) || s.anonymousAllowed(allowAnonymous) {
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
	if isAdmin(roles) || s.anonymousAllowed(allowAnonymous) {
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

// FilterOCIImageNames returns only the image names of one docker/oci
// repository the caller may browse. Repo-level filtering (FilterRepos) is not
// enough for a listing of image NAMES: it deliberately ignores content
// selectors' path clauses, so a caller path-scoped to /team-a/ would see
// team-b's image names — the exact thing the selector exists to hide. The
// sample path is "/<name>/", the same convention FilterComponents uses so a
// selector path.startsWith("/da/bas/") matches the image "da/bas/python".
func (s *RBACService) FilterOCIImageNames(
	ctx context.Context,
	userID string, roles []string,
	repo *domain.Repository,
	names []string,
) []string {
	if isAdmin(roles) || s.anonymousAllowed(repo.AllowAnonymous) {
		return names
	}
	if userID == "" {
		return nil
	}
	privs, err := s.rbac.GetUserPrivilegesWithSelectors(ctx, userID)
	if err != nil {
		s.log.Warnw("failed to load privileges for catalog filter", "userID", userID, "err", err)
		return nil
	}
	result := make([]string, 0, len(names))
	for _, name := range names {
		if matchPrivileges(privs, repo.Name, "/"+name+"/", "browse") {
			result = append(result, name)
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
		if s.anonymousAllowed(allowAnonByRepo[comp.Repository]) {
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
		if s.anonymousAllowed(allowAnonByRepo[a.Repository]) {
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
// matching. Both shapes that reach it carry one of the OCI endpoint keywords
// (manifests/blobs/tags) somewhere in the path:
//
//  1. Live v2 request paths (repo prefix already stripped, with or without a
//     leading /v2/): /<image>/manifests/<ref>, /<image>/blobs/<digest>,
//     /<image>/tags/list — the keyword follows the image name.
//
//  2. Stored asset DB paths: /blobs/<image>/<digest> or /manifests/<image>/<ref>
//     — the keyword leads, the reference trails.
//
// An image name may itself begin with "manifests" or "blobs" as a legitimate
// leading segment, so matching whichever keyword occurrence happens to lead the
// string mistakes the image name for the protocol keyword and cuts in the wrong
// place (#294). The shapes are told apart by the LAST keyword occurrence
// instead: mid-string, it is the live shape's separator (cut before it, keep
// the image name); leading, the path is the stored shape (strip the keyword,
// drop the trailing reference).
func assetSamplePath(p string) string {
	rest := strings.TrimPrefix(p, "/v2/")
	if rest != p {
		rest = "/" + rest
	}
	last, lastKw := -1, ""
	for _, kw := range []string{"/manifests/", "/blobs/", "/tags/"} {
		if idx := strings.LastIndex(rest, kw); idx > last {
			last, lastKw = idx, kw
		}
	}
	switch {
	case last > 0:
		// Live shape: /<image>/<keyword>/<ref> — keep the image name.
		return rest[:last] + "/"
	case last == 0 && lastKw != "/tags/":
		// Stored shape: /<keyword>/<image>/<ref> — strip keyword and reference.
		inner := strings.TrimPrefix(rest, lastKw)
		if idx := strings.LastIndex(inner, "/"); idx > 0 {
			return "/" + inner[:idx] + "/"
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
