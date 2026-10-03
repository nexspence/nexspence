package oci_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func blobStatus(r *gin.Engine, repoName, imageName, dgst string) int {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodHead,
		"/repository/"+repoName+"/v2/"+imageName+"/blobs/"+dgst, nil))
	return w.Code
}

func imageManifest(config string, layers ...string) string {
	s := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":1},"layers":[`, config)
	for i, l := range layers {
		if i > 0 {
			s += ","
		}
		s += fmt.Sprintf(`{"mediaType":"application/vnd.oci.image.layer.v1.tar","digest":%q,"size":1}`, l)
	}
	return s + "]}"
}

// #620: deleting a manifest by digest deletes it — every tag resolving to it
// goes too, and the blobs no remaining manifest uses are released. A layer
// another tag still uses stays.
func TestDeleteManifestByDigest_RemovesItsTagsAndReleasesBlobs(t *testing.T) {
	r := setup(testutil.SimpleRepo("deldg", "docker"))
	base := pushBlob(t, r, "deldg", "app", "base-layer")
	own := pushBlob(t, r, "deldg", "app", "own-layer")
	cfg1 := pushBlob(t, r, "deldg", "app", "config-1")
	cfg2 := pushBlob(t, r, "deldg", "app", "config-2")
	m1 := imageManifest(cfg1, base, own)
	dgst := pushManifestBody(t, r, "deldg", "app", "v1", m1)
	pushManifestBody(t, r, "deldg", "app", "latest", m1)
	pushManifestBody(t, r, "deldg", "app", "v2", imageManifest(cfg2, base))

	require.Equal(t, http.StatusAccepted, deleteManifest(t, r, "deldg", "app", dgst).Code)

	for _, ref := range []string{dgst, "v1", "latest"} {
		assert.Equal(t, http.StatusNotFound, getManifest(t, r, "deldg", "app", ref).Code, ref)
	}
	assert.Equal(t, http.StatusOK, getManifest(t, r, "deldg", "app", "v2").Code)
	assert.Equal(t, http.StatusNotFound, blobStatus(r, "deldg", "app", own))
	assert.Equal(t, http.StatusNotFound, blobStatus(r, "deldg", "app", cfg1))
	assert.Equal(t, http.StatusOK, blobStatus(r, "deldg", "app", base), "v2 still uses the base layer")
	assert.Equal(t, http.StatusOK, blobStatus(r, "deldg", "app", cfg2))
}

// Deleting by tag only untags: a deployment pinned to the digest still pulls.
func TestDeleteManifestByTag_KeepsTheDigest(t *testing.T) {
	r := setup(testutil.SimpleRepo("deltg", "docker"))
	layer := pushBlob(t, r, "deltg", "app", "layer")
	cfg := pushBlob(t, r, "deltg", "app", "config")
	dgst := pushManifestBody(t, r, "deltg", "app", "v1", imageManifest(cfg, layer))

	require.Equal(t, http.StatusAccepted, deleteManifest(t, r, "deltg", "app", "v1").Code)
	assert.Equal(t, http.StatusNotFound, getManifest(t, r, "deltg", "app", "v1").Code)
	assert.Equal(t, http.StatusOK, getManifest(t, r, "deltg", "app", dgst).Code)
	assert.Equal(t, http.StatusOK, blobStatus(r, "deltg", "app", layer))
}
