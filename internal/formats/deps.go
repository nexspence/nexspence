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
	BlobStore  storage.BlobStore
	BaseURL    string
	// Webhooks is optional — nil disables event delivery.
	Webhooks domain.WebhookDispatcher
}
