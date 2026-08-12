package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A referrer (signature, SBOM, attestation) only carries a subject digest
// pointing back at the image it describes, so the browse tree lists it as a
// sibling of that image with nothing to say they belong together. This endpoint
// is what lets the component panel show them grouped under their subject (#199).

const subjectDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

type ociReferrerResp struct {
	Subject   string `json:"subject"`
	Source    string `json:"source"`
	Referrers []struct {
		ComponentID  string `json:"componentId"`
		Reference    string `json:"reference"`
		Digest       string `json:"digest"`
		MediaType    string `json:"mediaType"`
		ArtifactType string `json:"artifactType"`
		Size         int64  `json:"size"`
	} `json:"referrers"`
}

// seedReferrer registers a component that names subjectDigest as its subject,
// plus the manifest asset the digest and size are read from.
func seedReferrer(t *testing.T, comps *testutil.ComponentRepo, assets *testutil.AssetRepo,
	repoName, image, version, artifactType, mediaType, sha string, extra map[string]any,
) {
	t.Helper()
	ex := map[string]any{
		"oci_subject":       subjectDigest,
		"oci_artifact_type": artifactType,
		"oci_media_type":    mediaType,
	}
	for k, v := range extra {
		ex[k] = v
	}
	require.NoError(t, comps.Create(context.Background(), &domain.Component{
		ID: "c-" + version, Repository: repoName, Format: "oci",
		Name: image, Version: version, Extra: ex,
	}))
	require.NoError(t, assets.Create(context.Background(), &domain.Asset{
		ID: "a-" + version, Repository: repoName, Path: "/manifests/" + image + "/" + version,
		SHA256: sha, SizeBytes: 512, ContentType: mediaType,
	}))
}

func ociRepo(t *testing.T, repos *testutil.RepoRepo, name string) {
	t.Helper()
	require.NoError(t, repos.Create(context.Background(), &domain.Repository{
		ID: "r-" + name, Name: name, Format: domain.FormatOCI, Type: domain.TypeHosted,
	}))
}

func getReferrers(t *testing.T, r *gin.Engine, repoName, image, reference string) (int, ociReferrerResp) {
	t.Helper()
	url := "/api/v1/browse/repositories/" + repoName + "/oci-referrers?image=" + image + "&reference=" + reference
	rec := do(t, r, http.MethodGet, url, nil)
	var body ociReferrerResp
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	}
	return rec.Code, body
}

func TestBrowseOCIReferrers_ListsReferrersWithFriendlyLabels(t *testing.T) {
	r, repos, comps, assets, _, _ := mountBrowse(t)
	ociRepo(t, repos, "oci-hosted")

	seedReferrer(t, comps, assets, "oci-hosted", "app/api", "sha256:aaa",
		"application/vnd.dev.cosign.artifact.sig.v1+json", "application/vnd.oci.image.manifest.v1+json", "aaa", nil)
	seedReferrer(t, comps, assets, "oci-hosted", "app/api", "sha256:bbb",
		"application/vnd.cyclonedx+json", "application/vnd.oci.image.manifest.v1+json", "bbb", nil)

	code, body := getReferrers(t, r, "oci-hosted", "app/api", subjectDigest)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, subjectDigest, body.Subject)
	require.Len(t, body.Referrers, 2, "both artifacts naming this image as their subject must be listed")

	byLabel := map[string]string{}
	for _, ref := range body.Referrers {
		byLabel[ref.ArtifactType] = ref.Digest
		assert.NotEmpty(t, ref.ComponentID, "each referrer must be navigable to its component")
		assert.EqualValues(t, 512, ref.Size)
	}
	assert.Equal(t, "sha256:aaa", byLabel["signature"])
	assert.Equal(t, "sha256:bbb", byLabel["sbom"], "an oras-attached CycloneDX SBOM must read as an sbom")
}

// A referrer pushed by tag has both a tag component and a digest-alias
// component; both name the same manifest, so the panel must list it once.
func TestBrowseOCIReferrers_DedupesTagAndDigestAliases(t *testing.T) {
	r, repos, comps, assets, _, _ := mountBrowse(t)
	ociRepo(t, repos, "oci-hosted")

	seedReferrer(t, comps, assets, "oci-hosted", "app/api", "sha256:ccc",
		"application/vnd.cyclonedx+json", "application/vnd.oci.image.manifest.v1+json", "ccc", nil)
	seedReferrer(t, comps, assets, "oci-hosted", "app/api", "sbom-latest",
		"application/vnd.cyclonedx+json", "application/vnd.oci.image.manifest.v1+json", "ccc", nil)

	code, body := getReferrers(t, r, "oci-hosted", "app/api", subjectDigest)
	require.Equal(t, http.StatusOK, code)
	assert.Len(t, body.Referrers, 1, "one manifest reachable by two references is still one referrer")
}

// A cosign attestation wraps its SBOM in a DSSE envelope, so the manifest's own
// artifactType is the sigstore bundle type and the predicate — what the artifact
// actually is — sits in an annotation. Reading only the artifactType labels an
// SBOM "signature" (#199, gotcha 1).
func TestBrowseOCIReferrers_SigstoreBundle_LabeledByPredicate(t *testing.T) {
	r, repos, comps, assets, _, _ := mountBrowse(t)
	ociRepo(t, repos, "oci-hosted")

	seedReferrer(t, comps, assets, "oci-hosted", "app/api", "sha256:ddd",
		"application/vnd.dev.sigstore.bundle.v0.3+json", "application/vnd.oci.image.manifest.v1+json", "ddd",
		map[string]any{"oci_annotations": map[string]any{
			"dev.sigstore.bundle.predicateType": "https://cyclonedx.org/bom",
		}})

	code, body := getReferrers(t, r, "oci-hosted", "app/api", subjectDigest)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, body.Referrers, 1)
	assert.Equal(t, "sbom", body.Referrers[0].ArtifactType,
		"a sigstore bundle whose predicate is an SBOM must read as an sbom, not a signature")
}

func TestBrowseOCIReferrers_MissingParams_400(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	ociRepo(t, repos, "oci-hosted")

	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/oci-hosted/oci-referrers?image=app/api", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a referrers lookup without a subject digest is not a lookup")

	rec = do(t, r, http.MethodGet, "/api/v1/browse/repositories/oci-hosted/oci-referrers?reference="+subjectDigest, nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a referrers lookup without an image is not a lookup")
}

func TestBrowseOCIReferrers_UnknownRepo_404(t *testing.T) {
	r, _, _, _, _, _ := mountBrowse(t)
	code, _ := getReferrers(t, r, "nope", "app/api", subjectDigest)
	assert.Equal(t, http.StatusNotFound, code)
}

func TestBrowseOCIReferrers_NonRegistryRepo_400(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	require.NoError(t, repos.Create(context.Background(), &domain.Repository{
		ID: "r-raw", Name: "raw-hosted", Format: domain.FormatRaw, Type: domain.TypeHosted,
	}))
	code, _ := getReferrers(t, r, "raw-hosted", "app/api", subjectDigest)
	assert.Equal(t, http.StatusBadRequest, code)
}

// A proxy answers from its cache, which holds only what was pulled through it.
// The panel must say so rather than present a partial list as the whole truth.
func TestBrowseOCIReferrers_ProxyRepo_MarksListAsCached(t *testing.T) {
	r, repos, comps, assets, _, _ := mountBrowse(t)
	require.NoError(t, repos.Create(context.Background(), &domain.Repository{
		ID: "r-proxy", Name: "oci-proxy", Format: domain.FormatOCI, Type: domain.TypeProxy,
		ProxyConfig: map[string]any{"remote_url": "https://registry.example.com"},
	}))
	seedReferrer(t, comps, assets, "oci-proxy", "app/api", "sha256:eee",
		"application/vnd.cyclonedx+json", "application/vnd.oci.image.manifest.v1+json", "eee", nil)

	code, body := getReferrers(t, r, "oci-proxy", "app/api", subjectDigest)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "cache", body.Source, "a proxy's referrers list is only what its cache happens to hold")
	assert.Len(t, body.Referrers, 1)
}

// The panel is normally opened on a tag, while referrers point at a digest.
// The lookup resolves the tag through its own manifest asset, so "what is
// attached to :latest?" is answerable without the user knowing the digest.
func TestBrowseOCIReferrers_TagReference_ResolvedToDigest(t *testing.T) {
	r, repos, comps, assets, _, _ := mountBrowse(t)
	ociRepo(t, repos, "oci-hosted")

	// The tagged image itself: its manifest digest is what referrers name.
	require.NoError(t, assets.Create(context.Background(), &domain.Asset{
		ID: "a-latest", Repository: "oci-hosted", Path: "/manifests/app/api/latest",
		SHA256:    "1111111111111111111111111111111111111111111111111111111111111111",
		SizeBytes: 1024, ContentType: "application/vnd.oci.image.manifest.v1+json",
	}))
	seedReferrer(t, comps, assets, "oci-hosted", "app/api", "sha256:fff",
		"application/vnd.cyclonedx+json", "application/vnd.oci.image.manifest.v1+json", "fff", nil)

	code, body := getReferrers(t, r, "oci-hosted", "app/api", "latest")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, subjectDigest, body.Subject, "the tag must resolve to the digest referrers point at")
	require.Len(t, body.Referrers, 1)
	assert.Equal(t, "sbom", body.Referrers[0].ArtifactType)
}

// A tag that names no manifest we hold is not an image with no referrers.
func TestBrowseOCIReferrers_UnknownTag_404(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	ociRepo(t, repos, "oci-hosted")
	code, _ := getReferrers(t, r, "oci-hosted", "app/api", "nosuchtag")
	assert.Equal(t, http.StatusNotFound, code)
}
