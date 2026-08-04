package oci_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deleteManifest issues DELETE /v2/<image>/manifests/<reference>.
func deleteManifest(t *testing.T, r *gin.Engine, repoName, imageName, reference string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete,
		"/repository/"+repoName+"/v2/"+imageName+"/manifests/"+reference, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// getManifest issues GET /v2/<image>/manifests/<reference>.
func getManifest(t *testing.T, r *gin.Engine, repoName, imageName, reference string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/repository/"+repoName+"/v2/"+imageName+"/manifests/"+reference, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// pushSignature attaches a cosign-shaped signature to subjectDigest under the
// tag cosign uses, and returns (tag, digest) of the signature manifest.
func pushSignature(t *testing.T, r *gin.Engine, repoName, imageName, subjectDigest string) (string, string) {
	t.Helper()
	sig := referrerManifest(cosignSigArtifactType, subjectDigest)
	sigTag := strings.Replace(subjectDigest, ":", "-", 1) + ".sig"
	return sigTag, pushManifestBody(t, r, repoName, imageName, sigTag, sig)
}

// A referrer is listed for as long as one of its manifest records is stored, so
// removing it from a subject's index means removing the manifest — both the tag
// it was pushed under and the digest record clients pull it by.
func TestReferrers_DeletingTheReferrerRemovesItFromTheIndex(t *testing.T) {
	repo := hostedOCIRepo("r-del-1", "oci-hosted")
	r, _ := setupWithDeps(repo)

	subjectDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "1.2.3", helmChartManifest)
	sigTag, sigDigest := pushSignature(t, r, "oci-hosted", "charts/nginx", subjectDigest)

	_, _, descs := getReferrers(t, r, "oci-hosted", "charts/nginx", subjectDigest, "")
	require.Len(t, descs, 1, "the signature is listed before it is deleted")

	require.Equal(t, http.StatusAccepted, deleteManifest(t, r, "oci-hosted", "charts/nginx", sigTag).Code)
	require.Equal(t, http.StatusAccepted, deleteManifest(t, r, "oci-hosted", "charts/nginx", sigDigest).Code)

	_, _, descs = getReferrers(t, r, "oci-hosted", "charts/nginx", subjectDigest, "")
	assert.Empty(t, descs, "a deleted signature is no longer a referrer of its subject")

	assert.Equal(t, http.StatusNotFound,
		getManifest(t, r, "oci-hosted", "charts/nginx", sigDigest).Code,
		"the signature manifest itself is gone")
}

// A manifest push stores two records of one manifest — the tag and the digest
// clients re-fetch by — and DELETE names one record, not the manifest. So
// deleting the tag a signature was pushed under leaves the signature stored,
// pullable by digest, and listed as a referrer. That is the OCI reading of a tag
// delete, and it is also why the underlying blob must survive the first delete:
// an index entry whose content had been destroyed would advertise a signature no
// client could fetch.
func TestReferrers_DeletingOnlyTheReferrerTagLeavesTheSignatureStored(t *testing.T) {
	repo := hostedOCIRepo("r-del-2", "oci-hosted")
	r, _ := setupWithDeps(repo)

	subjectDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "1.2.3", helmChartManifest)
	sigTag, sigDigest := pushSignature(t, r, "oci-hosted", "charts/nginx", subjectDigest)

	require.Equal(t, http.StatusAccepted, deleteManifest(t, r, "oci-hosted", "charts/nginx", sigTag).Code)

	assert.Equal(t, http.StatusNotFound,
		getManifest(t, r, "oci-hosted", "charts/nginx", sigTag).Code,
		"the tag is gone")

	byDigest := getManifest(t, r, "oci-hosted", "charts/nginx", sigDigest)
	require.Equal(t, http.StatusOK, byDigest.Code, "the manifest is still stored under its digest")
	assert.Equal(t, referrerManifest(cosignSigArtifactType, subjectDigest), byDigest.Body.String(),
		"and its bytes are intact — the blob is shared with the deleted tag record")

	_, _, descs := getReferrers(t, r, "oci-hosted", "charts/nginx", subjectDigest, "")
	require.Len(t, descs, 1, "a stored signature is still a referrer")
	assert.Equal(t, sigDigest, descs[0].Digest)
}

// Deleting the SUBJECT leaves its referrers listed. A signature is an artifact
// of its own that outlives the image it describes, and the index answers a
// question about a digest, not about a manifest this registry still holds — a
// client asking about a digest that was never here gets the same shape of
// answer.
func TestReferrers_DeletingTheSubjectLeavesItsReferrersListed(t *testing.T) {
	repo := hostedOCIRepo("r-del-3", "oci-hosted")
	r, _ := setupWithDeps(repo)

	subjectDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "1.2.3", helmChartManifest)
	sigTag, sigDigest := pushSignature(t, r, "oci-hosted", "charts/nginx", subjectDigest)

	// Delete the image whole: the tag and the digest record of the same manifest.
	require.Equal(t, http.StatusAccepted, deleteManifest(t, r, "oci-hosted", "charts/nginx", "1.2.3").Code)
	require.Equal(t, http.StatusAccepted, deleteManifest(t, r, "oci-hosted", "charts/nginx", subjectDigest).Code)
	require.Equal(t, http.StatusNotFound,
		getManifest(t, r, "oci-hosted", "charts/nginx", subjectDigest).Code,
		"the subject manifest is gone")

	w, _, descs := getReferrers(t, r, "oci-hosted", "charts/nginx", subjectDigest, "")
	assert.Equal(t, http.StatusOK, w.Code)
	require.Len(t, descs, 1, "the signature is still stored and still names that subject")
	assert.Equal(t, sigDigest, descs[0].Digest)
	assert.Equal(t, cosignSigArtifactType, descs[0].ArtifactType)

	// The signature is still pullable, which is what makes listing it honest.
	assert.Equal(t, http.StatusOK, getManifest(t, r, "oci-hosted", "charts/nginx", sigTag).Code)
}
