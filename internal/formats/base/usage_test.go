package base_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// usedBytes reads the counter checkQuota reads.
func usedBytes(t *testing.T, d formats.Deps, storeName string) int64 {
	t.Helper()
	bs, err := d.Blobs.Get(context.Background(), storeName)
	require.NoError(t, err)
	return bs.UsedBytes
}

// An OCI manifest push registers two assets on ONE blob: the tag path and the
// digest-alias path. used_bytes is how full the store is, and the store holds
// one copy — so the counter moves by one manifest size, not two (#146).
func TestRegisterStoredBlob_SecondAssetOnSameBlobKey_CountsBytesOnce(t *testing.T) {
	repo := testutil.SimpleRepo("usage-alias", "oci")
	d, _, _, _ := deps(repo)
	ctx := context.Background()

	content := `{"schemaVersion":2}`
	res, err := base.StoreArtifact(ctx, d,
		"usage-alias", "/manifests/app/1.0", "application/vnd.oci.image.manifest.v1+json",
		base.Coords{Name: "app", Version: "1.0"},
		strings.NewReader(content), int64(len(content)))
	require.NoError(t, err)
	require.Equal(t, int64(len(content)), usedBytes(t, d, "default"))

	// The digest alias: a second asset record on the SAME blob key.
	_, err = base.RegisterStoredBlob(ctx, d, repo,
		"/manifests/app/sha256:"+res.SHA256, "application/vnd.oci.image.manifest.v1+json",
		base.Coords{Name: "app", Version: "sha256:" + res.SHA256},
		res.Asset.BlobKey, res.SHA256, res.SHA1, res.MD5, res.Size, "", "")
	require.NoError(t, err)

	assert.Equal(t, int64(len(content)), usedBytes(t, d, "default"),
		"the alias stores no second copy, so it adds no second size")
}

// A caller that pins the store by id — the OCI digest alias pins the store the
// manifest bytes went to, a mount pins its source's — says where the bytes are.
// used_bytes is keyed by name, so the name has to be looked up rather than the
// registration silently counting against nothing.
func TestRegisterStoredBlob_StoreIDWithoutName_CountsAgainstThatStore(t *testing.T) {
	repo := testutil.SimpleRepo("usage-byid", "raw")
	d, _, _, _ := deps(repo)
	ctx := context.Background()

	def, err := d.Blobs.Get(ctx, "default")
	require.NoError(t, err)

	content := "counted against the pinned store"
	_, err = base.RegisterStoredBlob(ctx, d, repo,
		"/pinned.bin", "application/octet-stream",
		base.Coords{Name: "pinned.bin"},
		base.BlobKey("usage-byid", "/pinned.bin"), "sha", "sha1", "md5",
		int64(len(content)), def.ID, "")
	require.NoError(t, err)

	assert.Equal(t, int64(len(content)), usedBytes(t, d, "default"))
}

// Re-publishing a path overwrites the bytes at the same blob key. The store now
// holds the new size instead of the old one, so the counter moves by the
// difference — not by the whole new size on top of the old.
func TestStoreArtifact_OverwritingAPath_CountsOnlyTheSizeDifference(t *testing.T) {
	repo := testutil.SimpleRepo("usage-overwrite", "raw")
	d, blobStore, _, _ := deps(repo)
	ctx := context.Background()

	first := "small"
	_, err := base.StoreArtifact(ctx, d,
		"usage-overwrite", "/pkg.bin", "application/octet-stream",
		base.Coords{Name: "pkg.bin"},
		strings.NewReader(first), int64(len(first)))
	require.NoError(t, err)

	second := "a much larger payload"
	_, err = base.StoreArtifact(ctx, d,
		"usage-overwrite", "/pkg.bin", "application/octet-stream",
		base.Coords{Name: "pkg.bin"},
		strings.NewReader(second), int64(len(second)))
	require.NoError(t, err)

	physical, err := blobStore.UsedBytes(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(len(second)), physical, "one object holds the newest bytes")
	assert.Equal(t, int64(len(second)), usedBytes(t, d, "default"),
		"the counter tracks what is stored, not the sum of everything ever written")
}

// Deletion has to match the registration side or the counter drifts: the bytes
// only leave the store with the last asset that references them.
func TestDeleteArtifact_SharedBlobKey_DecrementsOnlyWhenTheBytesGo(t *testing.T) {
	repo := testutil.SimpleRepo("usage-del", "oci")
	d, blobStore, _, _ := deps(repo)
	ctx := context.Background()

	content := `{"schemaVersion":2}`
	tagPath := "/manifests/app/1.0"
	res, err := base.StoreArtifact(ctx, d,
		"usage-del", tagPath, "application/vnd.oci.image.manifest.v1+json",
		base.Coords{Name: "app", Version: "1.0"},
		strings.NewReader(content), int64(len(content)))
	require.NoError(t, err)

	digestPath := "/manifests/app/sha256:" + res.SHA256
	_, err = base.RegisterStoredBlob(ctx, d, repo,
		digestPath, "application/vnd.oci.image.manifest.v1+json",
		base.Coords{Name: "app", Version: "sha256:" + res.SHA256},
		res.Asset.BlobKey, res.SHA256, res.SHA1, res.MD5, res.Size, "", "")
	require.NoError(t, err)

	// First delete: the alias still needs the bytes, so nothing is freed.
	require.NoError(t, base.DeleteArtifact(ctx, d, "usage-del", tagPath))
	require.True(t, blobStore.Has(res.Asset.BlobKey), "the alias still references the blob")
	assert.Equal(t, int64(len(content)), usedBytes(t, d, "default"),
		"no bytes left the store, so the counter must not move")

	// Second delete: the bytes go, and so does their size.
	require.NoError(t, base.DeleteArtifact(ctx, d, "usage-del", digestPath))
	require.False(t, blobStore.Has(res.Asset.BlobKey))
	assert.Equal(t, int64(0), usedBytes(t, d, "default"),
		"the last reference frees the blob and its size")
}

// The user-visible symptom: a store whose counter double-counted an OCI
// manifest refuses the next push although the bytes fit.
func TestStoreArtifact_ManifestAliasDoesNotEatTheQuota(t *testing.T) {
	const manifest = `{"schemaVersion":2}`
	quota := int64(2 * len(manifest)) // room for the manifest and one more of its size
	bs := &domain.BlobStore{
		ID: "quota-bs", Name: "default", Type: "local",
		QuotaBytes: &quota, Config: map[string]any{},
	}
	repo := testutil.SimpleRepo("usage-quota", "oci")
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(bs),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost",
	}
	ctx := context.Background()

	res, err := base.StoreArtifact(ctx, d,
		"usage-quota", "/manifests/app/1.0", "application/vnd.oci.image.manifest.v1+json",
		base.Coords{Name: "app", Version: "1.0"},
		strings.NewReader(manifest), int64(len(manifest)))
	require.NoError(t, err)
	_, err = base.RegisterStoredBlob(ctx, d, repo,
		"/manifests/app/sha256:"+res.SHA256, "application/vnd.oci.image.manifest.v1+json",
		base.Coords{Name: "app", Version: "sha256:" + res.SHA256},
		res.Asset.BlobKey, res.SHA256, res.SHA1, res.MD5, res.Size, "", "")
	require.NoError(t, err)

	// One manifest is stored; half the quota is still free.
	_, err = base.StoreArtifact(ctx, d,
		"usage-quota", "/manifests/app/2.0", "application/vnd.oci.image.manifest.v1+json",
		base.Coords{Name: "app", Version: "2.0"},
		strings.NewReader(manifest), int64(len(manifest)))
	require.NoError(t, err, "the store has room for these bytes and must accept them")
}
