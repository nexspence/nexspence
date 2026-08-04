package formats

import (
	"context"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// Deps holds all dependencies injected into every format handler.
type Deps struct {
	Repos      repository.RepositoryRepo
	Components repository.ComponentRepo
	Assets     repository.AssetRepo
	Blobs      repository.BlobStoreRepo
	BlobStore  storage.BlobStore // default / fallback store
	Registry   *storage.Registry // optional: per-blob-store routing; nil disables
	BaseURL    string
	// Webhooks is optional — nil disables event delivery.
	Webhooks domain.WebhookDispatcher
	// Downloads is optional — nil disables download counting.
	Downloads domain.DownloadCounter
	// RoutingRules is optional — nil disables routing rule enforcement in group repos.
	RoutingRules repository.RoutingRuleRepo
	// RBAC is optional — nil disables caller-privilege checks a handler makes on
	// its own behalf. It does not disable RBACMiddleware, which guards the
	// request itself; this is for the paths a handler reaches that the request
	// URL does not name, such as a blob mount's client-supplied source.
	RBAC RBACChecker
}

// RBACChecker answers whether a caller may act on a path in a repository.
//
// It is declared here, as the subset of *service.RBACService a format handler
// needs, rather than imported from internal/service: that package already
// imports internal/formats/base, which imports this one, so depending on it the
// other way would close a cycle.
type RBACChecker interface {
	// CanAccessRepo mirrors service.RBACService.CanAccessRepo. action is one of
	// "read", "browse", "write", "delete"; path is the repository-relative asset
	// path, which the implementation normalizes for OCI repositories so it is
	// compared the way content selectors are written.
	CanAccessRepo(ctx context.Context, userID string, roles []string,
		repo *domain.Repository, path, action string) (bool, error)
}
