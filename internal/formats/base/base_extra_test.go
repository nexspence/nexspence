package base_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ── HTTPStatusForError ────────────────────────────────────────

func TestHTTPStatusForError_QuotaExceeded(t *testing.T) {
	code := base.HTTPStatusForError(base.ErrQuotaExceeded)
	assert.Equal(t, http.StatusInsufficientStorage, code)
}

func TestHTTPStatusForError_WrappedQuotaExceeded(t *testing.T) {
	wrapped := fmt.Errorf("something: %w", base.ErrQuotaExceeded)
	assert.Equal(t, http.StatusInsufficientStorage, base.HTTPStatusForError(wrapped))
}

func TestHTTPStatusForError_NoSpaceLeft(t *testing.T) {
	wrapped := fmt.Errorf("store blob: %w", storage.ErrNoSpace)
	assert.Equal(t, http.StatusInsufficientStorage, base.HTTPStatusForError(wrapped))
}

func TestHTTPStatusForError_OtherError(t *testing.T) {
	code := base.HTTPStatusForError(errors.New("some other error"))
	assert.Equal(t, http.StatusInternalServerError, code)
}

// ── ResolveBlobStore ──────────────────────────────────────────

func TestResolveBlobStore_DefaultFallback(t *testing.T) {
	repo := testutil.SimpleRepo("rsb-repo", "raw")
	d, _, _, _ := deps(repo)

	id, name, store, err := base.ResolveBlobStore(context.Background(), d, repo)
	require.NoError(t, err)
	// testutil.BlobStoreRepo seeds a "default" store; id may be empty if
	// GetByID fails on it, but the store must always be non-nil.
	assert.NotNil(t, store)
	_ = id
	_ = name
}

func TestResolveBlobStore_WithBlobStoreID(t *testing.T) {
	bsID := "bs-1"
	bs := &domain.BlobStore{ID: bsID, Name: "my-store", Type: "local",
		Config: map[string]any{"path": t.TempDir()}}
	blobs := testutil.NewBlobStoreRepo(bs)
	blobStore := testutil.NewBlobStore()
	reg := storage.NewRegistry(blobStore)
	repo := &domain.Repository{
		ID: "r1", Name: "rsb-repo2", Format: "raw", Type: "hosted",
		Online: true, BlobStoreID: &bsID,
	}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      blobs,
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  blobStore,
		Registry:   reg,
		BaseURL:    "http://localhost",
	}

	id, name, store, err := base.ResolveBlobStore(context.Background(), d, repo)
	require.NoError(t, err)
	assert.Equal(t, bsID, id)
	assert.Equal(t, "my-store", name)
	assert.NotNil(t, store)
}

// ── DecrementBlobStoreUsage ───────────────────────────────────

func TestDecrementBlobStoreUsage_NilAsset(t *testing.T) {
	blobs := testutil.NewBlobStoreRepo()
	err := base.DecrementBlobStoreUsage(context.Background(), blobs, nil)
	assert.NoError(t, err)
}

func TestDecrementBlobStoreUsage_ZeroSize(t *testing.T) {
	blobs := testutil.NewBlobStoreRepo()
	asset := &domain.Asset{SizeBytes: 0}
	err := base.DecrementBlobStoreUsage(context.Background(), blobs, asset)
	assert.NoError(t, err)
}

func TestDecrementBlobStoreUsage_NoBlobStoreID(t *testing.T) {
	blobs := testutil.NewBlobStoreRepo()
	asset := &domain.Asset{SizeBytes: 100, BlobStoreID: ""}
	// No ID → falls back to "default" store decrement
	err := base.DecrementBlobStoreUsage(context.Background(), blobs, asset)
	assert.NoError(t, err)
}

func TestDecrementBlobStoreUsage_WithBlobStoreID(t *testing.T) {
	bs := &domain.BlobStore{ID: "dec-bs", Name: "dec-store", Type: "local",
		Config: map[string]any{}}
	blobs := testutil.NewBlobStoreRepo(bs)
	asset := &domain.Asset{SizeBytes: 42, BlobStoreID: "dec-bs"}
	err := base.DecrementBlobStoreUsage(context.Background(), blobs, asset)
	assert.NoError(t, err)
	// UsedBytes should have decreased
	stored, _ := blobs.GetByID(context.Background(), "dec-bs")
	assert.Equal(t, int64(-42), stored.UsedBytes)
}

// ── StoreArtifact quota branches ─────────────────────────────

func TestStoreArtifact_RepoQuotaExceeded(t *testing.T) {
	quota := int64(5)
	repo := testutil.SimpleRepo("quota-repo", "raw")
	repo.QuotaBytes = &quota
	d, _, _, _ := deps(repo)

	_, err := base.StoreArtifact(context.Background(), d,
		"quota-repo", "/big.bin", "application/octet-stream",
		base.Coords{Name: "big.bin"},
		strings.NewReader("1234567890"), 10) // 10 > 5
	require.Error(t, err)
	assert.True(t, errors.Is(err, base.ErrQuotaExceeded))
}

func TestStoreArtifact_BlobStoreQuotaExceeded(t *testing.T) {
	quota := int64(3)
	bs := &domain.BlobStore{
		ID: "tiny-bs", Name: "default", Type: "local",
		QuotaBytes: &quota, UsedBytes: 2,
		Config: map[string]any{},
	}
	blobs := testutil.NewBlobStoreRepo(bs)
	blobStore := testutil.NewBlobStore()

	repo := &domain.Repository{
		ID: "bsq-repo", Name: "bsq-repo", Format: "raw",
		Type: "hosted", Online: true,
	}

	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      blobs,
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  blobStore,
		BaseURL:    "http://localhost",
	}

	// 2 used + 5 would-be > 3 quota
	_, err := base.StoreArtifact(context.Background(), d,
		"bsq-repo", "/f.bin", "application/octet-stream",
		base.Coords{Name: "f.bin"},
		strings.NewReader("hello"), 5)
	require.Error(t, err)
	assert.True(t, errors.Is(err, base.ErrQuotaExceeded))
}

// ── Webhook branches ──────────────────────────────────────────

// mockDispatcher is an in-process webhook dispatcher for tests.
type mockDispatcher struct {
	Events []domain.WebhookPayload
}

func (m *mockDispatcher) Dispatch(p domain.WebhookPayload) {
	m.Events = append(m.Events, p)
}

func TestStoreArtifact_WebhookFired(t *testing.T) {
	repo := testutil.SimpleRepo("wh-store", "raw")
	d, _, _, _ := deps(repo)
	wh := &mockDispatcher{}
	d.Webhooks = wh

	content := "webhook payload test"
	_, err := base.StoreArtifact(context.Background(), d,
		"wh-store", "/wh.txt", "text/plain",
		base.Coords{Name: "wh.txt"},
		strings.NewReader(content), int64(len(content)))
	require.NoError(t, err)
	require.Len(t, wh.Events, 1)
	assert.Equal(t, domain.EventArtifactPublished, wh.Events[0].Event)
	assert.Equal(t, "wh-store", wh.Events[0].Repository)
}

func TestDeleteArtifact_WebhookFired(t *testing.T) {
	repo := testutil.SimpleRepo("wh-del", "raw")
	d, _, _, _ := deps(repo)
	wh := &mockDispatcher{}
	d.Webhooks = wh

	content := "to delete"
	_, err := base.StoreArtifact(context.Background(), d,
		"wh-del", "/del.txt", "text/plain",
		base.Coords{Name: "del.txt"},
		strings.NewReader(content), int64(len(content)))
	require.NoError(t, err)
	wh.Events = nil

	err = base.DeleteArtifact(context.Background(), d, "wh-del", "/del.txt")
	require.NoError(t, err)
	require.Len(t, wh.Events, 1)
	assert.Equal(t, domain.EventArtifactDeleted, wh.Events[0].Event)
}

// ── PhysicalStore ─────────────────────────────────────────────

func TestPhysicalStore_NilRegistry_ExplicitStoreReturnsError(t *testing.T) {
	defaultStore := testutil.NewBlobStore()
	d := formats.Deps{BlobStore: defaultStore}
	bs := &domain.BlobStore{ID: "some-id", Name: "some-store", Type: "local"}
	result, err := base.PhysicalStore(context.Background(), d, bs)
	assert.Nil(t, result)
	require.Error(t, err)
	assert.ErrorIs(t, err, base.ErrBlobStoreUnavailable)
}

func TestPhysicalStore_NilBS_FallsBackToDefault(t *testing.T) {
	defaultStore := testutil.NewBlobStore()
	reg := storage.NewRegistry(defaultStore)
	d := formats.Deps{BlobStore: defaultStore, Registry: reg}
	result, err := base.PhysicalStore(context.Background(), d, nil)
	require.NoError(t, err)
	assert.Equal(t, defaultStore, result)
}

func TestPhysicalStore_NamedStoreWithoutID_DoesNotUseDefault(t *testing.T) {
	defaultStore := testutil.NewBlobStore()
	d := formats.Deps{BlobStore: defaultStore, Registry: storage.NewRegistry(defaultStore)}
	result, err := base.PhysicalStore(context.Background(), d, &domain.BlobStore{Name: "azure"})
	assert.Nil(t, result)
	require.Error(t, err)
	assert.ErrorIs(t, err, base.ErrBlobStoreUnavailable)
}

func TestStoreArtifact_ExplicitAzureFailure_DoesNotUseDefaultOrRegister(t *testing.T) {
	failingAzure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "synthetic Azure outage", http.StatusServiceUnavailable)
	}))
	t.Cleanup(failingAzure.Close)

	defaultStore := &domain.BlobStore{ID: "default-id", Name: "default", Type: "local"}
	azureStore := &domain.BlobStore{
		ID: "azure-id", Name: "azure", Type: "azure",
		Config: map[string]any{
			"container":    "nexspence",
			"account_name": "devstoreaccount1",
			"sas_token":    "sv=2024-11-04&sig=synthetic",
			"endpoint":     failingAzure.URL,
		},
	}
	blobs := testutil.NewBlobStoreRepo(defaultStore, azureStore)
	defaultPhysical := testutil.NewBlobStore()
	assets := testutil.NewAssetRepo()
	bsID := azureStore.ID
	repo := &domain.Repository{
		ID: "azure-repo-id", Name: "azure-repo", Format: "raw", Type: domain.TypeHosted,
		Online: true, BlobStoreID: &bsID,
	}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      blobs,
		Components: testutil.NewComponentRepo(),
		Assets:     assets,
		BlobStore:  defaultPhysical,
		Registry:   storage.NewRegistry(defaultPhysical),
	}

	_, err := base.StoreArtifact(context.Background(), d, repo.Name, "/artifact.txt", "text/plain",
		base.Coords{Name: "artifact.txt"}, strings.NewReader("must not be stored"), int64(len("must not be stored")))
	require.Error(t, err)
	assert.ErrorIs(t, err, base.ErrBlobStoreUnavailable)
	assert.False(t, defaultPhysical.Has(base.BlobKey(repo.Name, "/artifact.txt")),
		"an unavailable explicit Azure store must not fall back to the default store")
	assetList, listErr := assets.List(context.Background(), "", 100, 0)
	require.NoError(t, listErr)
	assert.Empty(t, assetList.Items, "failed storage must not register an asset")
	storedDefault, getErr := blobs.GetByID(context.Background(), defaultStore.ID)
	require.NoError(t, getErr)
	assert.Zero(t, storedDefault.UsedBytes, "failed storage must not charge the default store")
	storedAzure, getErr := blobs.GetByID(context.Background(), azureStore.ID)
	require.NoError(t, getErr)
	assert.Zero(t, storedAzure.UsedBytes, "failed storage must not charge the failed Azure store")
}

func TestFetchArtifact_ExplicitAzureFailure_DoesNotReadDefault(t *testing.T) {
	failingAzure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "synthetic Azure outage", http.StatusServiceUnavailable)
	}))
	t.Cleanup(failingAzure.Close)

	defaultStore := &domain.BlobStore{ID: "default-fetch-id", Name: "default", Type: "local"}
	azureStore := &domain.BlobStore{
		ID: "azure-fetch-id", Name: "azure-fetch", Type: "azure",
		Config: map[string]any{
			"container":    "nexspence",
			"account_name": "devstoreaccount1",
			"sas_token":    "sv=2024-11-04&sig=synthetic",
			"endpoint":     failingAzure.URL,
		},
	}
	blobs := testutil.NewBlobStoreRepo(defaultStore, azureStore)
	defaultPhysical := testutil.NewBlobStore()
	assets := testutil.NewAssetRepo()
	key := "explicit-azure-fetch-key"
	require.NoError(t, defaultPhysical.Put(context.Background(), key, strings.NewReader("wrong store"), 11))
	asset := &domain.Asset{
		ID: "asset-azure-fetch", Repository: "fetch-azure", RepositoryID: "fetch-azure-id",
		Path: "/artifact.txt", BlobKey: key, BlobStoreID: azureStore.ID, SizeBytes: 11,
	}
	require.NoError(t, assets.Create(context.Background(), asset))
	repo := &domain.Repository{ID: "fetch-azure-id", Name: "fetch-azure", Format: "raw", Type: domain.TypeHosted, Online: true}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      blobs,
		Components: testutil.NewComponentRepo(),
		Assets:     assets,
		BlobStore:  defaultPhysical,
		Registry:   storage.NewRegistry(defaultPhysical),
	}

	rc, gotAsset, err := base.FetchArtifact(context.Background(), d, repo.Name, asset.Path)
	assert.Nil(t, rc)
	assert.Nil(t, gotAsset)
	require.Error(t, err)
	assert.ErrorIs(t, err, base.ErrBlobStoreUnavailable)
	assert.True(t, defaultPhysical.Has(key), "an unavailable explicit Azure store must not read the default store")
}

// ── checkQuota via StoreArtifact (streaming size path) ───────

func TestStoreArtifact_StreamingQuotaCheck(t *testing.T) {
	// Provide declaredSize=-1 so post-write quota check fires.
	quota := int64(3)
	bs := &domain.BlobStore{
		ID: "stream-bs", Name: "default", Type: "local",
		QuotaBytes: &quota, UsedBytes: 2,
		Config: map[string]any{},
	}
	blobs := testutil.NewBlobStoreRepo(bs)
	blobStore := testutil.NewBlobStore()

	repo := &domain.Repository{
		ID: "stream-repo", Name: "stream-repo", Format: "raw",
		Type: "hosted", Online: true,
	}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      blobs,
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  blobStore,
		BaseURL:    "http://localhost",
	}

	// declaredSize=-1 → size is determined post-write by physStore.Size
	// testutil.BlobStore.Size returns the actual written length
	_, err := base.StoreArtifact(context.Background(), d,
		"stream-repo", "/s.bin", "application/octet-stream",
		base.Coords{Name: "s.bin"},
		strings.NewReader("12345"), -1)
	// 2 + 5 > 3 → quota exceeded
	require.Error(t, err)
	assert.True(t, errors.Is(err, base.ErrQuotaExceeded))
}

// ── FetchArtifact error branch (asset found but blob missing) ─

func TestFetchArtifact_BlobMissing(t *testing.T) {
	// We store to get an asset record, then clear the blob store to simulate
	// a missing blob.
	repo := testutil.SimpleRepo("blob-miss", "raw")
	repos := testutil.NewRepoRepo(repo)
	blobs := testutil.NewBlobStoreRepo()
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()

	d := formats.Deps{
		Repos:      repos,
		Blobs:      blobs,
		Components: comps,
		Assets:     assets,
		BlobStore:  blobStore,
		BaseURL:    "http://localhost",
	}

	content := "data"
	_, err := base.StoreArtifact(context.Background(), d,
		"blob-miss", "/file.txt", "text/plain",
		base.Coords{Name: "file.txt"},
		strings.NewReader(content), int64(len(content)))
	require.NoError(t, err)

	// Remove blob from the store so FetchArtifact gets "not found" from Get()
	key := base.BlobKey("blob-miss", "/file.txt")
	_ = blobStore.Delete(context.Background(), key)

	_, _, fetchErr := base.FetchArtifact(context.Background(), d, "blob-miss", "/file.txt")
	require.Error(t, fetchErr)
	assert.Contains(t, fetchErr.Error(), "blob missing")
}

// ── groupMemberIDs via string-slice branch ───────────────────

func TestStoreArtifact_GroupStore_StringSliceMemberIDs(t *testing.T) {
	// groupMemberIDs has two branches: []string and []interface{}.
	// The existing store_test.go exercises []interface{} (from groupBlobStore helper).
	// This test exercises the []string branch by constructing the Config directly.
	memberA := &domain.BlobStore{ID: "str-member-a", Name: "str-store-a", Type: "local",
		Config: map[string]any{"path": t.TempDir()}}

	group := &domain.BlobStore{
		ID:   "str-group",
		Name: "str-grp",
		Type: "group",
		Config: map[string]any{
			"fill_policy": "write_to_first_fill",
			"member_ids":  []string{"str-member-a"},
		},
	}

	bsID := "str-group"
	repo := &domain.Repository{
		ID: "str-repo", Name: "str-repo", Format: "raw", Type: "hosted",
		Online: true, BlobStoreID: &bsID,
	}

	d := depsWithGroup(repo, group, memberA)

	result, err := base.StoreArtifact(context.Background(), d,
		"str-repo", "/f.txt", "text/plain",
		base.Coords{Name: "f.txt"},
		strings.NewReader("hello"), 5)
	require.NoError(t, err)
	assert.Equal(t, "str-member-a", result.Asset.BlobStoreID)
}

// ── groupFillPolicy fallback to round_robin ──────────────────

func TestStoreArtifact_GroupStore_DefaultFillPolicy(t *testing.T) {
	// group blob store with no fill_policy → groupFillPolicy returns "round_robin"
	memberA := &domain.BlobStore{ID: "dfp-member-a", Name: "dfp-store-a", Type: "local",
		Config: map[string]any{"path": t.TempDir()}}

	group := &domain.BlobStore{
		ID:   "dfp-group",
		Name: "dfp-grp",
		Type: "group",
		Config: map[string]any{
			// No fill_policy key
			"member_ids": []interface{}{"dfp-member-a"},
		},
	}

	bsID := "dfp-group"
	repo := &domain.Repository{
		ID: "dfp-repo", Name: "dfp-repo", Format: "raw", Type: "hosted",
		Online: true, BlobStoreID: &bsID,
	}

	d := depsWithGroup(repo, group, memberA)

	result, err := base.StoreArtifact(context.Background(), d,
		"dfp-repo", "/f2.txt", "text/plain",
		base.Coords{Name: "f2.txt"},
		strings.NewReader("world"), 5)
	require.NoError(t, err)
	assert.Equal(t, "dfp-member-a", result.Asset.BlobStoreID)
}

// ── Implicit blob store when none is named "default" ──────────

// An operator who deletes the seeded stores and keeps a single store of their
// own ("minio", say) leaves every repository pointing at no blob store at all.
// Uploading to one used to fail with "default blob store not found" (#402);
// the only configured store is now what an implicit reference means.
func TestStoreArtifact_NoDefaultStore_UsesTheOnlyOne(t *testing.T) {
	repo := testutil.SimpleRepo("raw-hosted", "raw")
	blobStore := testutil.NewBlobStore()
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(&domain.BlobStore{ID: "bs-only", Name: "only-store", Type: "local", Config: map[string]any{"path": t.TempDir()}}),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  blobStore,
		Registry:   storage.NewRegistry(blobStore),
		BaseURL:    "http://localhost",
	}

	content := "artifact bytes"
	_, err := base.StoreArtifact(context.Background(), d,
		"raw-hosted", "/dir/file.txt", "text/plain",
		base.Coords{Group: "/dir", Name: "file.txt", Version: "1.0"},
		strings.NewReader(content), int64(len(content)))
	require.NoError(t, err)

	assets, err := d.Assets.ListByRepoAndPath(context.Background(), "raw-hosted", "")
	require.NoError(t, err)
	require.NotEmpty(t, assets)
	assert.Equal(t, "bs-only", assets[0].BlobStoreID)
}

// Several stores and none named "default" is ambiguous, and picking one at
// random would scatter blobs. The write fails, and the message says what to do.
func TestStoreArtifact_SeveralStoresNoDefault_FailsWithGuidance(t *testing.T) {
	repo := testutil.SimpleRepo("raw-hosted", "raw")
	blobStore := testutil.NewBlobStore()
	d := formats.Deps{
		Repos: testutil.NewRepoRepo(repo),
		Blobs: testutil.NewBlobStoreRepo(
			&domain.BlobStore{ID: "bs-minio", Name: "minio", Type: "s3"},
			&domain.BlobStore{ID: "bs-cold", Name: "cold", Type: "s3"},
		),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  blobStore,
		Registry:   storage.NewRegistry(blobStore),
		BaseURL:    "http://localhost",
	}

	content := "artifact bytes"
	_, err := base.StoreArtifact(context.Background(), d,
		"raw-hosted", "/dir/file.txt", "text/plain",
		base.Coords{Group: "/dir", Name: "file.txt", Version: "1.0"},
		strings.NewReader(content), int64(len(content)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "assign repository.blobStoreId")
}
