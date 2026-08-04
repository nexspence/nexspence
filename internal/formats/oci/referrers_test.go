package oci_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
)

const (
	cosignSigArtifactType = "application/vnd.dev.cosign.artifact.sig.v1+json"
	sbomArtifactType      = "application/spdx+json"
	ociIndexMediaType     = "application/vnd.oci.image.index.v1+json"
)

// referrerManifest is a manifest that points at another one through its subject
// descriptor — how cosign attaches a signature and how an SBOM attaches to a chart.
func referrerManifest(artifactType, subjectDigest string) string {
	return fmt.Sprintf(`{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "artifactType": %q,
  "config": {"mediaType": "application/vnd.oci.empty.v1+json", "digest": "sha256:ee", "size": 2},
  "layers": [{"mediaType": "application/octet-stream", "digest": "sha256:ff", "size": 7}],
  "subject": {"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": %q, "size": 100},
  "annotations": {"org.opencontainers.image.created": "2026-08-04T00:00:00Z"}
}`, artifactType, subjectDigest)
}

// pushManifestBody pushes one manifest and returns its digest.
func pushManifestBody(t *testing.T, r *gin.Engine, repoName, imageName, reference, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut,
		"/repository/"+repoName+"/v2/"+imageName+"/manifests/"+reference, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "manifest push should succeed")
	dgst := w.Header().Get("Docker-Content-Digest")
	require.Equal(t, digest(body), dgst)
	return dgst
}

// referrersIndex is the decoded referrers response.
type referrersIndex struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Manifests     json.RawMessage `json:"manifests"`
}

type referrersDescriptor struct {
	MediaType    string            `json:"mediaType"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	ArtifactType string            `json:"artifactType"`
	Annotations  map[string]string `json:"annotations"`
}

// getReferrers issues GET /v2/<image>/referrers/<digest> and decodes the index.
func getReferrers(t *testing.T, r *gin.Engine, repoName, imageName, subjectDigest, query string) (*httptest.ResponseRecorder, referrersIndex, []referrersDescriptor) {
	t.Helper()
	url := "/repository/" + repoName + "/v2/" + imageName + "/referrers/" + subjectDigest + query
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		return w, referrersIndex{}, nil
	}
	var idx referrersIndex
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &idx), "response should be an image index")
	var descs []referrersDescriptor
	require.NoError(t, json.Unmarshal(idx.Manifests, &descs))
	return w, idx, descs
}

func hostedOCIRepo(id, name string) *domain.Repository {
	return &domain.Repository{
		ID: id, Name: name, Format: domain.FormatOCI, Type: domain.TypeHosted, Online: true,
	}
}

// A cosign signature attached to a pushed image must be discoverable through the
// referrers API of the repository that holds it.
func TestReferrers_FindsSignatureBySubjectDigest(t *testing.T) {
	repo := hostedOCIRepo("r1", "oci-hosted")
	r, _ := setupWithDeps(repo)

	subjectDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "1.2.3", helmChartManifest)

	sig := referrerManifest(cosignSigArtifactType, subjectDigest)
	sigTag := strings.Replace(subjectDigest, ":", "-", 1) + ".sig"
	sigDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", sigTag, sig)

	w, idx, descs := getReferrers(t, r, "oci-hosted", "charts/nginx", subjectDigest, "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, ociIndexMediaType, w.Header().Get("Content-Type"))
	assert.Equal(t, 2, idx.SchemaVersion)
	assert.Equal(t, ociIndexMediaType, idx.MediaType)

	require.Len(t, descs, 1, "exactly one referrer")
	got := descs[0]
	assert.Equal(t, cosignSigArtifactType, got.ArtifactType)
	assert.Equal(t, sigDigest, got.Digest, "the descriptor names the referrer's own manifest")
	assert.Equal(t, int64(len(sig)), got.Size)
	assert.Positive(t, got.Size)
	assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", got.MediaType)
	assert.Equal(t, "2026-08-04T00:00:00Z", got.Annotations["org.opencontainers.image.created"])
}

// An unknown subject yields an empty index, never a 404: a 404 reads as "this
// registry has no referrers API" and sends cosign down a fallback path.
func TestReferrers_UnknownSubjectIsEmptyIndexNotNotFound(t *testing.T) {
	repo := hostedOCIRepo("r1", "oci-hosted")
	r, _ := setupWithDeps(repo)

	unknown := digest("nothing was ever pushed under this digest")
	w, _, descs := getReferrers(t, r, "oci-hosted", "charts/nginx", unknown, "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, descs)

	// A JSON null for manifests breaks clients that range over the list, so the
	// exact serialization matters, not just the decoded value.
	assert.JSONEq(t,
		`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`,
		w.Body.String())
	assert.Contains(t, w.Body.String(), `"manifests":[]`, "manifests must be [] and not null")
	assert.NotContains(t, w.Body.String(), `"manifests":null`)
}

// artifactType selects one kind of referrer and must be announced back to the
// client, so it can tell a filtered answer from a complete one.
func TestReferrers_FiltersByArtifactType(t *testing.T) {
	repo := hostedOCIRepo("r1", "oci-hosted")
	r, _ := setupWithDeps(repo)

	subjectDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "1.2.3", helmChartManifest)

	sig := referrerManifest(cosignSigArtifactType, subjectDigest)
	sigDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "sig", sig)
	sbom := referrerManifest(sbomArtifactType, subjectDigest)
	sbomDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "sbom", sbom)
	require.NotEqual(t, sigDigest, sbomDigest)

	// Unfiltered: both, and no filter header.
	w, _, descs := getReferrers(t, r, "oci-hosted", "charts/nginx", subjectDigest, "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, descs, 2)
	assert.Empty(t, w.Header().Get("OCI-Filters-Applied"),
		"an unfiltered answer must not claim a filter was applied")

	// Filtered: only the signature, and the header says so. The value is escaped
	// as a real client escapes it — a raw "+" in a query string decodes to a
	// space, and every media type here contains one.
	w2, _, filtered := getReferrers(t, r, "oci-hosted", "charts/nginx", subjectDigest,
		"?artifactType="+url.QueryEscape(cosignSigArtifactType))
	require.Equal(t, http.StatusOK, w2.Code)
	require.Len(t, filtered, 1)
	assert.Equal(t, cosignSigArtifactType, filtered[0].ArtifactType)
	assert.Equal(t, sigDigest, filtered[0].Digest)
	assert.Equal(t, "artifactType", w2.Header().Get("OCI-Filters-Applied"))

	// A filter nothing matches is still an empty index, not the unfiltered list.
	w3, _, none := getReferrers(t, r, "oci-hosted", "charts/nginx", subjectDigest,
		"?artifactType="+url.QueryEscape("application/vnd.example.nothing"))
	require.Equal(t, http.StatusOK, w3.Code)
	assert.Empty(t, none)
	assert.Equal(t, "artifactType", w3.Header().Get("OCI-Filters-Applied"))
}

// A manifest pushed by tag also gets a digest-alias component, and phase 1 puts
// the same oci_subject on both. The index must name the manifest once.
func TestReferrers_DeduplicatesTagAndDigestAlias(t *testing.T) {
	repo := hostedOCIRepo("r1", "oci-hosted")
	r, d := setupWithDeps(repo)

	subjectDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "1.2.3", helmChartManifest)
	sig := referrerManifest(cosignSigArtifactType, subjectDigest)
	sigTag := strings.Replace(subjectDigest, ":", "-", 1) + ".sig"
	sigDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", sigTag, sig)

	// Both components really exist and both really carry the subject — otherwise
	// this test would pass without any deduplication at all.
	requireSubject(t, d, "oci-hosted", "charts/nginx", sigTag, subjectDigest)
	requireSubject(t, d, "oci-hosted", "charts/nginx", sigDigest, subjectDigest)

	w, _, descs := getReferrers(t, r, "oci-hosted", "charts/nginx", subjectDigest, "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, descs, 1, "the tag component and its digest alias are one manifest")
	assert.Equal(t, sigDigest, descs[0].Digest)
}

// requireSubject asserts the stored component carries oci_subject.
func requireSubject(t *testing.T, d formats.Deps, repoName, name, version, subjectDigest string) {
	t.Helper()
	comp := componentOf(t, d, repoName, name, version)
	require.Equal(t, subjectDigest, comp.Extra["oci_subject"],
		"component %s:%s should carry the subject", name, version)
}

// The referrers dispatch must not swallow requests for an image whose own name
// ends in "referrers": /v2/lib/referrers/manifests/1.0.0 is a manifest push.
func TestReferrers_DoesNotSwallowImageNamedReferrers(t *testing.T) {
	repo := hostedOCIRepo("r1", "oci-hosted")
	r, _ := setupWithDeps(repo)

	pushManifestBody(t, r, "oci-hosted", "lib/referrers", "1.0.0", helmChartManifest)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/oci-hosted/v2/lib/referrers/manifests/1.0.0", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "a manifest GET under such a name must still route to manifests")
	assert.Equal(t, helmChartManifest, w.Body.String())
}

// Only GET (and HEAD, which gin serves through the same handler) reads referrers;
// a write verb is not part of the API.
func TestReferrers_RejectsNonGET(t *testing.T) {
	repo := hostedOCIRepo("r1", "oci-hosted")
	r, _ := setupWithDeps(repo)

	req := httptest.NewRequest(http.MethodPut,
		"/repository/oci-hosted/v2/charts/nginx/referrers/"+digest("x"), strings.NewReader("{}"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
