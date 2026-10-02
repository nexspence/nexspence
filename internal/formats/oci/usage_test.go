package oci_test

import (
	"context"
	"net/http"

	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A manifest push stores two objects: the tag the client pushed and the
// sha256: digest alias a pull re-fetches by. The alias needs its own copy so a
// re-push of the tag cannot change what the old digest serves (#594), and
// used_bytes counts both (issue #146).
func TestPutManifest_StoresTagAndDigestAsSeparateObjects(t *testing.T) {
	repo := testutil.SimpleRepo("usage1", "docker")
	r, _, d, store := mountDeps(repo)

	body := referrerManifest(sbomArtifactType, "sha256:"+
		"0000000000000000000000000000000000000000000000000000000000000000")
	pushManifestBody(t, r, "usage1", "library/app", "1.0", body)

	physical, err := store.UsedBytes(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(2*len(body)), physical, "tag and digest are two stored objects")

	assert.Equal(t, int64(2*len(body)), usedBytes(t, d))
}

// Each asset gives its own object's size back when it goes.
func TestPutManifest_DeletingBothAssetsGivesEachSizeBack(t *testing.T) {
	repo := testutil.SimpleRepo("usage2", "docker")
	r, _, d, _ := mountDeps(repo)

	body := referrerManifest(sbomArtifactType, "sha256:"+
		"1111111111111111111111111111111111111111111111111111111111111111")
	dgst := pushManifestBody(t, r, "usage2", "library/app", "1.0", body)
	require.Equal(t, int64(2*len(body)), usedBytes(t, d))

	require.Equal(t, http.StatusAccepted, deleteManifest(t, r, "usage2", "library/app", "1.0").Code)
	assert.Equal(t, int64(len(body)), usedBytes(t, d), "the digest alias keeps its own object")
	require.Equal(t, http.StatusOK, getManifest(t, r, "usage2", "library/app", dgst).Code)

	require.Equal(t, http.StatusAccepted, deleteManifest(t, r, "usage2", "library/app", dgst).Code)
	assert.Equal(t, int64(0), usedBytes(t, d))
}

// #594: re-pushing a tag overwrites the tag's object in place. The previous
// manifest's digest must keep serving that manifest, byte for byte.
func TestPutManifest_RepushedTagKeepsOldDigestContent(t *testing.T) {
	repo := testutil.SimpleRepo("repush", "docker")
	r, _, _, _ := mountDeps(repo)

	bodyA := referrerManifest(sbomArtifactType, "sha256:"+
		"2222222222222222222222222222222222222222222222222222222222222222")
	bodyB := referrerManifest(sbomArtifactType, "sha256:"+
		"3333333333333333333333333333333333333333333333333333333333333333")
	dgstA := pushManifestBody(t, r, "repush", "library/app", "latest", bodyA)
	dgstB := pushManifestBody(t, r, "repush", "library/app", "latest", bodyB)
	require.NotEqual(t, dgstA, dgstB)

	old := getManifest(t, r, "repush", "library/app", dgstA)
	require.Equal(t, http.StatusOK, old.Code)
	assert.Equal(t, bodyA, old.Body.String(), "the old digest serves the old manifest")

	assert.Equal(t, bodyB, getManifest(t, r, "repush", "library/app", "latest").Body.String())
	assert.Equal(t, bodyB, getManifest(t, r, "repush", "library/app", dgstB).Body.String())
}

// An alias stored before #594 shares the tag's object. Re-pushing that tag
// first moves the alias onto an object of its own.
func TestPutManifest_RepushDetachesLegacySharedAlias(t *testing.T) {
	repo := testutil.SimpleRepo("legacy", "docker")
	r, _, d, store := mountDeps(repo)
	ctx := context.Background()

	bodyA := referrerManifest(sbomArtifactType, "sha256:"+
		"4444444444444444444444444444444444444444444444444444444444444444")
	dgstA := pushManifestBody(t, r, "legacy", "library/app", "latest", bodyA)

	// Rebuild the old layout: the alias row points at the tag's object.
	tag, err := d.Assets.GetByPath(ctx, "legacy", "/manifests/library/app/latest")
	require.NoError(t, err)
	alias, err := d.Assets.GetByPath(ctx, "legacy", "/manifests/library/app/"+dgstA)
	require.NoError(t, err)
	require.NoError(t, store.Delete(ctx, alias.BlobKey))
	alias.BlobKey = tag.BlobKey
	require.NoError(t, d.Assets.Create(ctx, alias))

	bodyB := referrerManifest(sbomArtifactType, "sha256:"+
		"5555555555555555555555555555555555555555555555555555555555555555")
	pushManifestBody(t, r, "legacy", "library/app", "latest", bodyB)

	old := getManifest(t, r, "legacy", "library/app", dgstA)
	require.Equal(t, http.StatusOK, old.Code)
	assert.Equal(t, bodyA, old.Body.String())

	moved, err := d.Assets.GetByPath(ctx, "legacy", "/manifests/library/app/"+dgstA)
	require.NoError(t, err)
	assert.NotEqual(t, tag.BlobKey, moved.BlobKey, "the alias has its own object now")
}
