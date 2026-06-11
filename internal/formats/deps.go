package formats

import (
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
}
