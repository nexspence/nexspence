package oci_test

import (
	"context"
	"net/http"

	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A manifest push stores one object and registers two assets on it: the tag the
// client pushed and the sha256: digest alias a pull re-fetches by. used_bytes is
// how full the store is, so it moves by one manifest size (issue #146).
func TestPutManifest_CountsTheManifestOnce(t *testing.T) {
	repo := testutil.SimpleRepo("usage1", "docker")
	r, _, d, store := mountDeps(repo)

	body := referrerManifest(sbomArtifactType, "sha256:"+
		"0000000000000000000000000000000000000000000000000000000000000000")
	pushManifestBody(t, r, "usage1", "library/app", "1.0", body)

	physical, err := store.UsedBytes(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(len(body)), physical, "one manifest, one stored object")

	assert.Equal(t, int64(len(body)), usedBytes(t, d),
		"the digest alias names the same object; it does not double the store's usage")
}

// Both assets have to go before the object does, and the size comes off exactly
// once — with the object.
func TestPutManifest_DeletingBothAssetsGivesTheSizeBackOnce(t *testing.T) {
	repo := testutil.SimpleRepo("usage2", "docker")
	r, _, d, _ := mountDeps(repo)

	body := referrerManifest(sbomArtifactType, "sha256:"+
		"1111111111111111111111111111111111111111111111111111111111111111")
	dgst := pushManifestBody(t, r, "usage2", "library/app", "1.0", body)
	require.Equal(t, int64(len(body)), usedBytes(t, d))

	require.Equal(t, http.StatusAccepted, deleteManifest(t, r, "usage2", "library/app", "1.0").Code)
	assert.Equal(t, int64(len(body)), usedBytes(t, d),
		"the digest alias still reads the object, so its bytes are still stored")

	require.Equal(t, http.StatusAccepted, deleteManifest(t, r, "usage2", "library/app", dgst).Code)
	assert.Equal(t, int64(0), usedBytes(t, d),
		"the last asset takes the object away, and its size with it")
}
