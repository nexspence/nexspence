package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// captureDispatcher records the webhook payloads a delete produces.
type captureDispatcher struct{ events []domain.WebhookPayload }

func (d *captureDispatcher) Dispatch(p domain.WebhookPayload) { d.events = append(d.events, p) }

// deletedPaths lists the asset paths reported as deleted.
func (d *captureDispatcher) deletedPaths() []string {
	var out []string
	for _, e := range d.events {
		if e.Event != domain.EventArtifactDeleted {
			continue
		}
		if p, ok := e.Asset["path"].(string); ok {
			out = append(out, p)
		}
	}
	return out
}

// secondaryStoreID is the blob store the test repository's assets live on — a
// store that is NOT the handler's default.
const secondaryStoreID = "bs-secondary"

// mountBrowseOnSecondaryStore wires a BrowseHandler whose default store is an
// in-memory double, while the repository's assets live on a second, real store
// resolved through the blob store registry. Any delete that reaches the default
// store is deleting from the wrong place.
//
// Returns the engine, the asset repo, the DEFAULT store (which must stay
// untouched), the SECONDARY store (where the bytes really are), and the webhook
// dispatcher.
func mountBrowseOnSecondaryStore(t *testing.T) (
	*gin.Engine, *testutil.RepoRepo, *testutil.AssetRepo, *testutil.BlobStore, *storage.LocalBlobStore, *captureDispatcher,
) {
	t.Helper()
	repos := testutil.NewRepoRepo()
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()

	dir := t.TempDir()
	secondary, err := storage.NewLocalBlobStore(dir)
	require.NoError(t, err)
	blobs := testutil.NewBlobStoreRepo(&domain.BlobStore{
		ID: secondaryStoreID, Name: "secondary", Type: "local",
		Config: map[string]any{"path": dir},
	})

	defaultStore := testutil.NewBlobStore()
	hooks := &captureDispatcher{}
	rbacSvc := service.NewRBACService(emptyRBACRepo{}, repos, zap.NewNop().Sugar())
	h := handlers.NewBrowseHandler(formats.Deps{
		Repos:      repos,
		Components: comps,
		Assets:     assets,
		Blobs:      blobs,
		BlobStore:  defaultStore,
		Registry:   storage.NewRegistry(defaultStore),
		Webhooks:   hooks,
	}, rbacSvc)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "admin")
		c.Set("roles", []string{"nx-admin"})
		c.Next()
	})
	r.DELETE("/api/v1/browse/repositories/:name/path", h.DeleteByPath)
	r.DELETE("/api/v1/browse/repositories/:name/docker-tag", h.DeleteDockerTag)
	r.DELETE("/api/v1/browse/repositories/:name/docker-image", h.DeleteDockerImage)
	seedRepo(t, repos, &domain.Repository{
		ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted,
		BlobStoreID: strPtr(secondaryStoreID),
	})
	return r, repos, assets, defaultStore, secondary, hooks
}

func strPtr(s string) *string { return &s }

// putSecondary writes a blob to the secondary store.
func putSecondary(t *testing.T, s *storage.LocalBlobStore, key, content string) {
	t.Helper()
	require.NoError(t, s.Put(context.Background(), key, testutil.MakeReader(content), int64(len(content))))
}

// existsSecondary reports whether the secondary store still holds a blob.
func existsSecondary(t *testing.T, s *storage.LocalBlobStore, key string) bool {
	t.Helper()
	ok, err := s.Exists(context.Background(), key)
	require.NoError(t, err)
	return ok
}

// seedTaggedImage stores a manifest naming one config and one layer blob, plus
// the three asset records, all on the secondary store.
func seedTaggedImage(t *testing.T, assets *testutil.AssetRepo, secondary *storage.LocalBlobStore) {
	t.Helper()
	ctx := context.Background()
	putSecondary(t, secondary, "blob-manifest", `{"config":{"digest":"sha256:cfg"},"layers":[{"digest":"sha256:layer1"}]}`)
	putSecondary(t, secondary, "blob-cfg", "c")
	putSecondary(t, secondary, "blob-layer1", "l")

	for _, a := range []*domain.Asset{
		{Repository: "docker-host", Path: "/manifests/da/python/3.12", BlobKey: "blob-manifest",
			SHA256: "tagsha", BlobStoreID: secondaryStoreID, SizeBytes: 70},
		{Repository: "docker-host", Path: "/blobs/da/python/sha256:cfg", BlobKey: "blob-cfg",
			BlobStoreID: secondaryStoreID, SizeBytes: 1},
		{Repository: "docker-host", Path: "/blobs/da/python/sha256:layer1", BlobKey: "blob-layer1",
			BlobStoreID: secondaryStoreID, SizeBytes: 1},
	} {
		require.NoError(t, assets.Create(ctx, a))
	}
}

// A repository whose assets live on a non-default blob store must have its bytes
// deleted from THAT store. The manifest also has to be read back from it, or the
// config and layer digests come back empty and none of the layer blobs are ever
// swept.
func TestBrowse_DeleteDockerTag_DeletesFromTheAssetsOwnStore(t *testing.T) {
	r, _, assets, defaultStore, secondary, hooks := mountBrowseOnSecondaryStore(t)
	seedTaggedImage(t, assets, secondary)

	rec := do(t, r, http.MethodDelete,
		"/api/v1/browse/repositories/docker-host/docker-tag?image=da/python&ref=3.12", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)

	assert.False(t, existsSecondary(t, secondary, "blob-manifest"), "manifest blob must be gone from its own store")
	assert.False(t, existsSecondary(t, secondary, "blob-cfg"), "config blob must be gone from its own store")
	assert.False(t, existsSecondary(t, secondary, "blob-layer1"), "layer blob must be gone from its own store")
	assert.Empty(t, defaultStore.Deleted, "nothing may be deleted from the default store")

	assert.ElementsMatch(t, []string{
		"/manifests/da/python/3.12",
		"/blobs/da/python/sha256:cfg",
		"/blobs/da/python/sha256:layer1",
	}, hooks.deletedPaths(), "every deleted artifact is reported on the webhook bus")
}

// Deleting a whole image must clear its manifests and blobs from the store that
// holds them.
func TestBrowse_DeleteDockerImage_DeletesFromTheAssetsOwnStore(t *testing.T) {
	r, _, assets, defaultStore, secondary, hooks := mountBrowseOnSecondaryStore(t)
	seedTaggedImage(t, assets, secondary)

	rec := do(t, r, http.MethodDelete,
		"/api/v1/browse/repositories/docker-host/docker-image?image=da/python", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)

	assert.False(t, existsSecondary(t, secondary, "blob-manifest"))
	assert.False(t, existsSecondary(t, secondary, "blob-cfg"))
	assert.False(t, existsSecondary(t, secondary, "blob-layer1"))
	assert.Empty(t, defaultStore.Deleted, "nothing may be deleted from the default store")
	assert.Len(t, hooks.deletedPaths(), 3)
}

// Deleting by path prefix removes the bytes too, from the store that holds them.
func TestBrowse_DeleteByPath_DeletesFromTheAssetsOwnStore(t *testing.T) {
	r, _, assets, defaultStore, secondary, hooks := mountBrowseOnSecondaryStore(t)
	seedTaggedImage(t, assets, secondary)

	rec := do(t, r, http.MethodDelete,
		"/api/v1/browse/repositories/docker-host/path?path=/blobs/da/python/", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)

	assert.False(t, existsSecondary(t, secondary, "blob-cfg"))
	assert.False(t, existsSecondary(t, secondary, "blob-layer1"))
	assert.True(t, existsSecondary(t, secondary, "blob-manifest"), "a path outside the prefix is untouched")
	assert.Empty(t, defaultStore.Deleted, "nothing may be deleted from the default store")
	assert.ElementsMatch(t, []string{
		"/blobs/da/python/sha256:cfg",
		"/blobs/da/python/sha256:layer1",
	}, hooks.deletedPaths())
}

// A tag and its digest alias are two records of ONE manifest on ONE blob. Both
// records go, and the shared blob goes with the last of them — the order of the
// two deletions no longer matters, but the outcome must not change.
func TestBrowse_DeleteDockerTag_RemovesBothRecordsAndTheSharedBlob(t *testing.T) {
	r, _, assets, _, secondary, _ := mountBrowseOnSecondaryStore(t)
	ctx := context.Background()

	putSecondary(t, secondary, "blob-manifest", `{"config":{"digest":"sha256:cfg"},"layers":[]}`)
	putSecondary(t, secondary, "blob-cfg", "c")
	require.NoError(t, assets.Create(ctx, &domain.Asset{
		Repository: "docker-host", Path: "/manifests/da/python/3.12", BlobKey: "blob-manifest",
		SHA256: "abc123", BlobStoreID: secondaryStoreID, SizeBytes: 46,
	}))
	require.NoError(t, assets.Create(ctx, &domain.Asset{
		Repository: "docker-host", Path: "/manifests/da/python/sha256:abc123", BlobKey: "blob-manifest",
		SHA256: "abc123", BlobStoreID: secondaryStoreID, SizeBytes: 46,
	}))
	require.NoError(t, assets.Create(ctx, &domain.Asset{
		Repository: "docker-host", Path: "/blobs/da/python/sha256:cfg", BlobKey: "blob-cfg",
		BlobStoreID: secondaryStoreID, SizeBytes: 1,
	}))

	rec := do(t, r, http.MethodDelete,
		"/api/v1/browse/repositories/docker-host/docker-tag?image=da/python&ref=3.12", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)

	_, err := assets.GetByPath(ctx, "docker-host", "/manifests/da/python/3.12")
	assert.Error(t, err, "the tag record is gone")
	_, err = assets.GetByPath(ctx, "docker-host", "/manifests/da/python/sha256:abc123")
	assert.Error(t, err, "the digest alias record is gone with it")
	assert.False(t, existsSecondary(t, secondary, "blob-manifest"), "and the blob both shared is gone")
	assert.False(t, existsSecondary(t, secondary, "blob-cfg"), "the config blob it referenced too")
}
