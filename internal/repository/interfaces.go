// Package repository defines interfaces for all database access.
// All implementations live in repository/postgres/.
package repository

import (
	"context"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// RepositoryRepo manages repository definitions.
type RepositoryRepo interface {
	List(ctx context.Context, format, repoType string) ([]domain.Repository, error)
	Get(ctx context.Context, name string) (*domain.Repository, error)
	GetByID(ctx context.Context, id string) (*domain.Repository, error)
	Create(ctx context.Context, r *domain.Repository) error
	Update(ctx context.Context, r *domain.Repository) error
	Delete(ctx context.Context, name string) error
	// ListNamesByCleanupPolicyID returns repository names that reference the policy.
	ListNamesByCleanupPolicyID(ctx context.Context, policyID string) ([]string, error)
	// DetachCleanupPolicyID removes policyID from every repositories.cleanup_policy_ids array.
	DetachCleanupPolicyID(ctx context.Context, policyID string) error
	// ListByBlobStoreID returns repositories that use the given blob_store_id.
	ListByBlobStoreID(ctx context.Context, blobStoreID string) ([]domain.Repository, error)
	// HasAnyAnonymousDocker reports whether at least one Docker-format repository
	// has allow_anonymous=true. Used by the /v2/ root handler to decide whether
	// unauthenticated Docker clients should be allowed to proceed to per-repo RBAC.
	HasAnyAnonymousDocker(ctx context.Context) (bool, error)
}

// ComponentRepo manages component metadata.
type ComponentRepo interface {
	List(ctx context.Context, repoName string, limit int, offset int) (*domain.Page[domain.Component], error)
	// ListByRepoNames returns components from any of the given repositories (used for group repo browse/search).
	ListByRepoNames(ctx context.Context, repoNames []string, limit int, offset int) (*domain.Page[domain.Component], error)
	Get(ctx context.Context, id string) (*domain.Component, error)
	Search(ctx context.Context, p domain.SearchParams) (*domain.Page[domain.Component], error)
	// ListDockerBrowseRows returns Docker-format components with one asset path per row (for Tags vs Manifests vs Blobs).
	ListDockerBrowseRows(ctx context.Context, repoNames []string, maxRows int) ([]domain.DockerBrowseRow, error)
	Create(ctx context.Context, c *domain.Component) error
	Delete(ctx context.Context, id string) error
	// UpdateExtra merges JSON into components.extra (e.g. scan_result).
	UpdateExtra(ctx context.Context, id string, extra map[string]any) error
	// DeleteOrphans removes components in repoName that have no remaining assets.
	DeleteOrphans(ctx context.Context, repoName string) error
}

// AssetRepo manages artifact file records.
type AssetRepo interface {
	List(ctx context.Context, repoName string, limit int, offset int) (*domain.Page[domain.Asset], error)
	Get(ctx context.Context, id string) (*domain.Asset, error)
	GetByPath(ctx context.Context, repoName, path string) (*domain.Asset, error)
	SearchAssets(ctx context.Context, p domain.SearchParams) (*domain.Page[domain.Asset], error)
	// ListStale returns assets matching cleanup criteria:
	//   format "*" matches all; lastDownloadedDays/artifactAgeDays 0 = no filter.
	//   repoNames non-empty restricts to those repositories; empty = any repository (use with care).
	//   pathPrefix filters assets whose path starts with that prefix (empty = no filter).
	//   nameGlob is a glob pattern matched against the full asset path (* = any chars, ? = one char).
	ListStale(ctx context.Context, format string, repoNames []string, lastDownloadedDays, artifactAgeDays int, pathPrefix, nameGlob string, limit int) ([]domain.Asset, error)
	Create(ctx context.Context, a *domain.Asset) error
	Delete(ctx context.Context, id string) error
	IncrementDownload(ctx context.Context, id string) error
	// ListByComponentID returns all assets for a component (ordered by path).
	ListByComponentID(ctx context.Context, componentID string) ([]domain.Asset, error)
	// ListAllBlobKeys returns distinct blob_key values referenced by assets (for GC).
	ListAllBlobKeys(ctx context.Context) ([]string, error)
	// SumSizeByRepo returns total size_bytes of all assets in the repository.
	SumSizeByRepo(ctx context.Context, repoName string) (int64, error)
	// ListPathsByRepo returns unique directory-level path prefixes from assets
	// in the given repository. If q is non-empty, only paths containing q
	// (case-insensitive) are returned. Fetches up to 5000 raw paths from the DB
	// then expands directory prefixes in Go.
	ListPathsByRepo(ctx context.Context, repoName, q string) ([]string, error)
	// ListRawAssetPaths returns raw asset paths (not directory-expanded) for
	// the given repository. Used by format-specific path transformations (e.g. Docker).
	ListRawAssetPaths(ctx context.Context, repoName string) ([]string, error)
	// ListByRepoAndPath returns all assets in repoName whose path starts with pathPrefix.
	// Use pathPrefix="" to list all assets in the repo.
	ListByRepoAndPath(ctx context.Context, repoName, pathPrefix string) ([]domain.Asset, error)
	// CountByBlobKey returns the number of assets that reference blobKey, excluding the asset with excludeID.
	// Used to decide whether the physical blob file can be deleted.
	CountByBlobKey(ctx context.Context, blobKey, excludeID string) (int, error)
	// ListRawBrowseAssets returns all assets for the given raw-format repos with metadata for tree building.
	ListRawBrowseAssets(ctx context.Context, repoNames []string) ([]domain.RawBrowseAsset, error)
}

// ContentSelectorRepo manages content selector definitions (privilege-scoped paths).
type ContentSelectorRepo interface {
	List(ctx context.Context) ([]domain.ContentSelector, error)
	Get(ctx context.Context, id string) (*domain.ContentSelector, error)
	GetByName(ctx context.Context, name string) (*domain.ContentSelector, error)
	Create(ctx context.Context, s *domain.ContentSelector) error
	Update(ctx context.Context, s *domain.ContentSelector) error
	Delete(ctx context.Context, id string) error
	ListForUser(ctx context.Context, userID string) ([]domain.ContentSelector, error)
	AttachToPrivilege(ctx context.Context, privilegeName, selectorID string) error
	DetachFromPrivilege(ctx context.Context, privilegeName string) error
}

// PrivilegeRepo manages privilege definitions.
type PrivilegeRepo interface {
	List(ctx context.Context) ([]domain.Privilege, error)
	Get(ctx context.Context, id string) (*domain.Privilege, error)
	GetByName(ctx context.Context, name string) (*domain.Privilege, error)
	Create(ctx context.Context, p *domain.Privilege) error
	Update(ctx context.Context, p *domain.Privilege) error
	Delete(ctx context.Context, id string) error
	// ListByRole returns privileges assigned to a role via role_privileges.
	ListByRole(ctx context.Context, roleID string) ([]domain.Privilege, error)
	// PrivilegeRoleMap returns a map of privilege ID → role names that include it.
	// Used by the UI to display "Used in Roles" for each privilege.
	PrivilegeRoleMap(ctx context.Context) (map[string][]string, error)
}

// RoutingRuleRepo manages request routing rules.
type RoutingRuleRepo interface {
	List(ctx context.Context) ([]domain.RoutingRule, error)
	Get(ctx context.Context, id string) (*domain.RoutingRule, error)
	GetByName(ctx context.Context, name string) (*domain.RoutingRule, error)
	Create(ctx context.Context, rr *domain.RoutingRule) error
	Update(ctx context.Context, rr *domain.RoutingRule) error
	Delete(ctx context.Context, id string) error
}

// UserTokenRepo manages per-user API tokens.
type UserTokenRepo interface {
	ListByUser(ctx context.Context, userID string) ([]domain.UserToken, error)
	Get(ctx context.Context, id string) (*domain.UserToken, error)
	GetByHash(ctx context.Context, tokenHash string) (*domain.UserToken, error)
	Create(ctx context.Context, t *domain.UserToken) error
	Delete(ctx context.Context, id string) error
	TouchLastUsed(ctx context.Context, id string) error
}

// WebhookRepo manages outbound webhooks.
type WebhookRepo interface {
	List(ctx context.Context) ([]domain.Webhook, error)
	Get(ctx context.Context, id string) (*domain.Webhook, error)
	ListByEvent(ctx context.Context, event domain.WebhookEvent) ([]domain.Webhook, error)
	Create(ctx context.Context, w *domain.Webhook) error
	Update(ctx context.Context, w *domain.Webhook) error
	Delete(ctx context.Context, id string) error
}

// UserRepo manages user accounts.
type UserRepo interface {
	List(ctx context.Context, source string) ([]domain.User, error)
	Get(ctx context.Context, username string) (*domain.User, error)
	GetByID(ctx context.Context, id string) (*domain.User, error)
	Create(ctx context.Context, u *domain.User) error
	Update(ctx context.Context, u *domain.User) error
	UpdatePassword(ctx context.Context, username, hash string) error
	Delete(ctx context.Context, username string) error
	UpdateLastLogin(ctx context.Context, username string) error
	// SetOIDCTokens stores id_token and refresh_token for an OIDC user.
	// Pass empty strings to clear both columns (e.g. on logout).
	SetOIDCTokens(ctx context.Context, userID string, idToken, refreshToken string) error
	// GetOIDCIDToken returns the stored id_token for userID, or "" if unset.
	GetOIDCIDToken(ctx context.Context, userID string) (string, error)
}

// RoleRepo manages roles and privileges.
type RoleRepo interface {
	List(ctx context.Context) ([]domain.Role, error)
	Get(ctx context.Context, id string) (*domain.Role, error)
	Create(ctx context.Context, r *domain.Role) error
	Update(ctx context.Context, r *domain.Role) error
	Delete(ctx context.Context, id string) error
	GetUserRoles(ctx context.Context, userID string) ([]domain.Role, error)
	SetUserRoles(ctx context.Context, userID string, roleIDs []string) error
	// SetPrivileges replaces all role_privileges rows for the role.
	SetPrivileges(ctx context.Context, roleID string, privilegeIDs []string) error
	// ListPrivilegeIDsByRole returns privilege IDs for a role (lightweight, for JWT building).
	ListPrivilegeIDsByRole(ctx context.Context, roleID string) ([]string, error)
}

// CleanupPolicyRepo manages cleanup policies.
type CleanupPolicyRepo interface {
	List(ctx context.Context) ([]domain.CleanupPolicy, error)
	Get(ctx context.Context, id string) (*domain.CleanupPolicy, error)
	Create(ctx context.Context, p *domain.CleanupPolicy) error
	Update(ctx context.Context, p *domain.CleanupPolicy) error
	Delete(ctx context.Context, id string) error
}

// AuditQuery holds filter and pagination parameters for AuditRepo.List/Stream.
type AuditQuery struct {
	Domain   string     // empty = any
	Action   string     // empty = any
	Username string     // empty = any (exact match)
	From     *time.Time // inclusive lower bound; nil = no lower bound
	To       *time.Time // exclusive upper bound; nil = no upper bound
	Limit    int        // ignored by Stream. List impl applies its own default and cap.
	Offset   int        // ignored by Stream
}

// AuditRepo writes and reads audit log events.
type AuditRepo interface {
	Write(ctx context.Context, e *domain.AuditEvent) error
	List(ctx context.Context, q AuditQuery) (items []domain.AuditEvent, total int, err error)
	Stream(ctx context.Context, q AuditQuery, fn func(domain.AuditEvent) error) error
}

// PrivilegeWithSelector is returned by RBACRepo — one row per privilege attached to the user.
type PrivilegeWithSelector struct {
	Actions    []string // from privilege attrs.actions; empty = all actions
	Expression string   // CEL expression from content_selector
}

// RBACRepo resolves a user's effective privileges for access checks.
type RBACRepo interface {
	GetUserPrivilegesWithSelectors(ctx context.Context, userID string) ([]PrivilegeWithSelector, error)
}

// BlobRefRepo manages reference counts for deduplicated blobs in global_blobs.
type BlobRefRepo interface {
	Increment(ctx context.Context, blobKey string, sizeBytes int64) error
	Decrement(ctx context.Context, blobKey string) (bool, error)
	Get(ctx context.Context, blobKey string) (int, error)
}

// BlobStoreRepo manages blob store configuration.
type BlobStoreRepo interface {
	List(ctx context.Context) ([]domain.BlobStore, error)
	Get(ctx context.Context, name string) (*domain.BlobStore, error)
	GetByID(ctx context.Context, id string) (*domain.BlobStore, error)
	Create(ctx context.Context, b *domain.BlobStore) error
	Update(ctx context.Context, b *domain.BlobStore) error
	Delete(ctx context.Context, name string) error
	UpdateUsedBytes(ctx context.Context, name string, delta int64) error
}
