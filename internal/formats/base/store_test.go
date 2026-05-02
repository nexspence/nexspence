package base_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deps builds a formats.Deps wired with testutil mocks.
func deps(repo *domain.Repository) (formats.Deps, *testutil.BlobStore, *testutil.ComponentRepo, *testutil.AssetRepo) {
	repos := testutil.NewRepoRepo(repo)
	blobs := testutil.NewBlobStoreRepo()
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()

	return formats.Deps{
		Repos:      repos,
		Blobs:      blobs,
		Components: comps,
		Assets:     assets,
		BlobStore:  blobStore,
		BaseURL:    "http://localhost:8080",
	}, blobStore, comps, assets
}

// ── BlobKey ───────────────────────────────────────────────────

func TestBlobKey_Deterministic(t *testing.T) {
	k1 := base.BlobKey("myrepo", "/path/to/file.jar")
	k2 := base.BlobKey("myrepo", "/path/to/file.jar")
	assert.Equal(t, k1, k2)
}

func TestBlobKey_DifferentInputs(t *testing.T) {
	k1 := base.BlobKey("repo-a", "/file.jar")
	k2 := base.BlobKey("repo-b", "/file.jar")
	k3 := base.BlobKey("repo-a", "/other.jar")
	assert.NotEqual(t, k1, k2)
	assert.NotEqual(t, k1, k3)
}

func TestBlobKey_IsHex64(t *testing.T) {
	k := base.BlobKey("repo", "/artifact.tgz")
	assert.Len(t, k, 64, "SHA-256 hex should be 64 chars")
	for _, c := range k {
		assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
			"unexpected char %q in blob key", c)
	}
}

func TestBlobKeyByDigest(t *testing.T) {
	k := base.BlobKeyByDigest("sha256:abc123")
	assert.Equal(t, "digest/sha256:abc123", k)
}

// ── StoreArtifact ─────────────────────────────────────────────

func TestStoreArtifact_HappyPath(t *testing.T) {
	repo := testutil.SimpleRepo("testrepo", "raw")
	d, blobStore, comps, assets := deps(repo)

	content := "hello world artifact"
	coords := base.Coords{Group: "/dir", Name: "file.txt", Version: "1.0"}

	result, err := base.StoreArtifact(context.Background(), d,
		"testrepo", "/dir/file.txt", "text/plain",
		coords,
		strings.NewReader(content), int64(len(content)))

	require.NoError(t, err)
	require.NotNil(t, result)

	// Checksums should be non-empty
	assert.Len(t, result.SHA256, 64)
	assert.Len(t, result.SHA1, 40)
	assert.Len(t, result.MD5, 32)
	assert.Equal(t, int64(len(content)), result.Size)

	// Blob should be in the store
	key := base.BlobKey("testrepo", "/dir/file.txt")
	assert.True(t, blobStore.Has(key))
	stored, err := blobStore.Read(key)
	require.NoError(t, err)
	assert.Equal(t, content, stored)

	// Component + asset records should exist
	compList, _ := comps.List(context.Background(), "", 100, 0)
	assert.Len(t, compList.Items, 1)

	assetList, _ := assets.List(context.Background(), "", 100, 0)
	assert.Len(t, assetList.Items, 1)
}

func TestStoreArtifact_RepoNotFound(t *testing.T) {
	repo := testutil.SimpleRepo("exists", "raw")
	d, _, _, _ := deps(repo)

	_, err := base.StoreArtifact(context.Background(), d,
		"does-not-exist", "/file.txt", "text/plain",
		base.Coords{Name: "file.txt"},
		strings.NewReader("data"), -1)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestStoreArtifact_OfflineRepo(t *testing.T) {
	repo := testutil.SimpleRepo("offline-repo", "raw")
	repo.Online = false
	d, _, _, _ := deps(repo)

	_, err := base.StoreArtifact(context.Background(), d,
		"offline-repo", "/file.txt", "text/plain",
		base.Coords{Name: "file.txt"},
		strings.NewReader("data"), -1)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "offline")
}

// ── FetchArtifact ─────────────────────────────────────────────

func TestFetchArtifact_HappyPath(t *testing.T) {
	repo := testutil.SimpleRepo("fetchrepo", "raw")
	d, blobStore, _, _ := deps(repo)

	// Store first
	content := "artifact content"
	_, err := base.StoreArtifact(context.Background(), d,
		"fetchrepo", "/lib/artifact.jar", "application/java-archive",
		base.Coords{Name: "artifact.jar", Version: "2.0"},
		strings.NewReader(content), int64(len(content)))
	require.NoError(t, err)

	// Now fetch
	rc, asset, err := base.FetchArtifact(context.Background(), d, "fetchrepo", "/lib/artifact.jar")
	require.NoError(t, err)
	require.NotNil(t, rc)
	require.NotNil(t, asset)
	defer rc.Close()

	assert.Equal(t, "application/java-archive", asset.ContentType)
	assert.Equal(t, int64(len(content)), asset.SizeBytes)

	// Verify blob was readable
	_ = blobStore // already asserted via FetchArtifact rc
}

func TestFetchArtifact_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("emptyrepo", "raw")
	d, _, _, _ := deps(repo)

	_, _, err := base.FetchArtifact(context.Background(), d, "emptyrepo", "/missing.jar")
	require.Error(t, err)
}

// ── DeleteArtifact ────────────────────────────────────────────

func TestDeleteArtifact_HappyPath(t *testing.T) {
	repo := testutil.SimpleRepo("deleterepo", "raw")
	d, blobStore, _, _ := deps(repo)

	content := "delete me"
	_, err := base.StoreArtifact(context.Background(), d,
		"deleterepo", "/to-delete.bin", "application/octet-stream",
		base.Coords{Name: "to-delete.bin"},
		strings.NewReader(content), int64(len(content)))
	require.NoError(t, err)

	key := base.BlobKey("deleterepo", "/to-delete.bin")
	assert.True(t, blobStore.Has(key))

	err = base.DeleteArtifact(context.Background(), d, "deleterepo", "/to-delete.bin")
	require.NoError(t, err)

	assert.False(t, blobStore.Has(key))
}

func TestDeleteArtifact_Idempotent(t *testing.T) {
	repo := testutil.SimpleRepo("idempotentrepo", "raw")
	d, _, _, _ := deps(repo)

	// Deleting a non-existent artifact should not error
	err := base.DeleteArtifact(context.Background(), d, "idempotentrepo", "/nonexistent.bin")
	assert.NoError(t, err)
}

// ── Group blob store routing ──────────────────────────────────

func groupBlobStore(id, name, policy string, memberIDs ...string) *domain.BlobStore {
	ids := make([]interface{}, len(memberIDs))
	for i, m := range memberIDs {
		ids[i] = m
	}
	return &domain.BlobStore{
		ID:   id,
		Name: name,
		Type: "group",
		Config: map[string]any{
			"fill_policy": policy,
			"member_ids":  ids,
		},
	}
}

func depsWithGroup(repo *domain.Repository, groupStore *domain.BlobStore, memberStores ...*domain.BlobStore) formats.Deps {
	allStores := append([]*domain.BlobStore{groupStore}, memberStores...)
	blobs := testutil.NewBlobStoreRepo(allStores...)
	defaultBS := testutil.NewBlobStore()
	reg := storage.NewRegistry(defaultBS)
	return formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      blobs,
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  defaultBS,
		Registry:   reg,
		BaseURL:    "http://localhost:8080",
	}
}

func TestStoreArtifact_GroupStore_AssetBlobStoreIDIsPhysicalMember(t *testing.T) {
	memberA := &domain.BlobStore{ID: "member-a", Name: "store-a", Type: "local",
		Config: map[string]any{"path": t.TempDir()}}
	memberB := &domain.BlobStore{ID: "member-b", Name: "store-b", Type: "local",
		Config: map[string]any{"path": t.TempDir()}}
	group := groupBlobStore("group-1", "my-group", "round_robin", "member-a", "member-b")

	bsID := "group-1"
	repo := &domain.Repository{
		ID: "repo-1", Name: "testrepo2", Format: "raw", Type: "hosted",
		Online: true, BlobStoreID: &bsID,
	}

	d := depsWithGroup(repo, group, memberA, memberB)

	result, err := base.StoreArtifact(context.Background(), d,
		"testrepo2", "/file.txt", "text/plain",
		base.Coords{Name: "file.txt"},
		strings.NewReader("hello"), 5)
	require.NoError(t, err)
	require.NotNil(t, result)

	if result.Asset.BlobStoreID == "group-1" {
		t.Fatal("asset.BlobStoreID must not be the group ID — it must be a physical member ID")
	}
	if result.Asset.BlobStoreID != "member-a" && result.Asset.BlobStoreID != "member-b" {
		t.Errorf("expected member-a or member-b, got %q", result.Asset.BlobStoreID)
	}
}

func TestStoreArtifact_GroupStore_RoundRobin_AlternatesMembers(t *testing.T) {
	memberA := &domain.BlobStore{ID: "rr-member-a", Name: "rr-store-a", Type: "local",
		Config: map[string]any{"path": t.TempDir()}}
	memberB := &domain.BlobStore{ID: "rr-member-b", Name: "rr-store-b", Type: "local",
		Config: map[string]any{"path": t.TempDir()}}
	group := groupBlobStore("group-rr", "rr-group", "round_robin", "rr-member-a", "rr-member-b")

	bsID := "group-rr"
	repo := &domain.Repository{
		ID: "repo-rr", Name: "rr-repo", Format: "raw", Type: "hosted",
		Online: true, BlobStoreID: &bsID,
	}
	d := depsWithGroup(repo, group, memberA, memberB)

	var selected []string
	for i := 0; i < 4; i++ {
		path := fmt.Sprintf("/rr-file%d.txt", i)
		res, err := base.StoreArtifact(context.Background(), d,
			"rr-repo", path, "text/plain",
			base.Coords{Name: fmt.Sprintf("file%d.txt", i)},
			strings.NewReader("data"), 4)
		require.NoError(t, err)
		selected = append(selected, res.Asset.BlobStoreID)
	}
	if selected[0] == selected[1] {
		t.Errorf("round-robin should alternate, got same member twice: %v", selected)
	}
	if selected[0] != selected[2] || selected[1] != selected[3] {
		t.Errorf("round-robin pattern broken: %v", selected)
	}
}
