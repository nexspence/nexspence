package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// The registry browse tree is the same for both labels of the OCI protocol.
func TestDockerTree_OCIFormatRepo_IsAccepted(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	require.NoError(t, repos.Create(context.Background(), &domain.Repository{
		ID: "r1", Name: "oci-hosted", Format: domain.FormatOCI, Type: domain.TypeHosted,
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/browse/repositories/oci-hosted/docker-tree", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "an oci repository must have a browse tree")
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "oci", body["format"])
}

// A component stored in an oci repository must reach the tree, not just the
// format gate: the browse rows are the tree's only data source.
func TestDockerTree_OCIFormatRepo_ComponentAppearsInTree(t *testing.T) {
	r, repos, comps, _, _, _ := mountBrowse(t)
	require.NoError(t, repos.Create(context.Background(), &domain.Repository{
		ID: "r1", Name: "oci-hosted", Format: domain.FormatOCI, Type: domain.TypeHosted,
	}))
	comps.DockerRowsByRepo = map[string][]domain.DockerBrowseRow{
		"oci-hosted": {
			{ComponentID: "c1", ImageName: "charts/nginx", Version: "1.2.3", SamplePath: "/manifests/charts/nginx/1.2.3"},
		},
	}

	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/oci-hosted/docker-tree", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

	charts, ok := findChild(got.Root, "charts")
	require.True(t, ok, "the oci component must appear in the browse tree")
	nginx, ok := findChild(charts, "nginx")
	require.True(t, ok)
	tags, ok := findChild(nginx, "Tags")
	require.True(t, ok)
	leaf, ok := findChild(tags, "1.2.3")
	require.True(t, ok)
	assert.Equal(t, "c1", leaf.ComponentID)
}

// ── artifact types in the browse tree ─────────────────────────────────────────

// ociLeaf drives one browse row through the tree and returns its leaf, so the
// artifact-type tests read as "this media type produces this label".
func ociLeaf(t *testing.T, row domain.DockerBrowseRow) browseNode {
	t.Helper()
	r, repos, comps, _, _, _ := mountBrowse(t)
	require.NoError(t, repos.Create(context.Background(), &domain.Repository{
		ID: "r1", Name: "oci-hosted", Format: domain.FormatOCI, Type: domain.TypeHosted,
	}))
	comps.DockerRowsByRepo = map[string][]domain.DockerBrowseRow{"oci-hosted": {row}}

	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/oci-hosted/docker-tree", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

	img, ok := findChild(got.Root, row.ImageName)
	require.True(t, ok, "image %q must appear in the tree", row.ImageName)
	cat, ok := findChild(img, "Tags")
	require.True(t, ok)
	leaf, ok := findChild(cat, row.Version)
	require.True(t, ok)
	return leaf
}

// A chart pushed with `helm push oci://` carries the Helm config media type. The
// tree must say "chart" rather than show nothing.
func TestDockerTree_ArtifactType_HelmChartIsLabelledChart(t *testing.T) {
	leaf := ociLeaf(t, domain.DockerBrowseRow{
		ComponentID: "c1", ImageName: "nginx", Version: "1.2.3",
		SamplePath:   "/manifests/nginx/1.2.3",
		ArtifactType: "application/vnd.cncf.helm.config.v1+json",
	})
	assert.Equal(t, "chart", leaf.ArtifactType)
}

// Everything pushed before this feature existed has no oci_artifact_type. Those
// rows must produce no artifact type at all — not an empty-looking one.
func TestDockerTree_ArtifactType_AbsentStaysAbsent(t *testing.T) {
	leaf := ociLeaf(t, domain.DockerBrowseRow{
		ComponentID: "c1", ImageName: "nginx", Version: "1.2.3",
		SamplePath: "/manifests/nginx/1.2.3",
	})
	assert.Empty(t, leaf.ArtifactType, "a component with no OCI metadata must not gain a label")
}

// The recognition table is presentation only: it shortens the media types the
// registry actually sees, and passes anything else through verbatim.
func TestDockerTree_ArtifactType_RecognitionTable(t *testing.T) {
	cases := []struct {
		mediaType string
		want      string
	}{
		// Helm charts — `helm push oci://`.
		{"application/vnd.cncf.helm.config.v1+json", "chart"},
		{"application/vnd.cncf.helm.chart.content.v1.tar+gzip", "chart"},
		// Container images — OCI and Docker config media types.
		{"application/vnd.oci.image.config.v1+json", "image"},
		{"application/vnd.docker.container.image.v1+json", "image"},
		// WASM — wasm-to-oci config and layer types.
		{"application/vnd.wasm.config.v1+json", "wasm"},
		{"application/vnd.wasm.content.layer.v1+wasm", "wasm"},
		// SBOMs — SPDX (cosign's text/spdx family and the ORAS artifactType),
		// CycloneDX, Syft, and cosign's own SBOM attachment type.
		{"application/spdx+json", "sbom"},
		{"text/spdx", "sbom"},
		{"text/spdx+json", "sbom"},
		{"application/vnd.cyclonedx+json", "sbom"},
		{"application/vnd.cyclonedx+xml", "sbom"},
		{"application/vnd.syft+json", "sbom"},
		{"application/vnd.dev.cosign.artifact.sbom.v1+json", "sbom"},
		// Signatures — cosign simplesigning, the OCI 1.1 artifactType, and
		// sigstore bundles.
		{"application/vnd.dev.cosign.simplesigning.v1+json", "signature"},
		{"application/vnd.dev.cosign.artifact.sig.v1+json", "signature"},
		{"application/vnd.dev.sigstore.bundle.v0.3+json", "signature"},
		// Attestations — in-toto statements, DSSE envelopes, BuildKit and cosign.
		{"application/vnd.in-toto+json", "attestation"},
		{"application/vnd.in-toto.provenance+dsse", "attestation"},
		{"application/vnd.dsse.envelope.v1+json", "attestation"},
		{"application/vnd.docker.attestation.manifest.v1+json", "attestation"},
		{"application/vnd.dev.cosign.artifact.att.v1+json", "attestation"},
		// Anything else is shown exactly as the registry stored it.
		{"application/vnd.acme.model.config.v1+json", "application/vnd.acme.model.config.v1+json"},
		{"application/vnd.oci.empty.v1+json", "application/vnd.oci.empty.v1+json"},
	}
	for _, tc := range cases {
		t.Run(tc.mediaType, func(t *testing.T) {
			leaf := ociLeaf(t, domain.DockerBrowseRow{
				ComponentID: "c1", ImageName: "art", Version: "1.0",
				SamplePath:   "/manifests/art/1.0",
				ArtifactType: tc.mediaType,
			})
			assert.Equal(t, tc.want, leaf.ArtifactType)
		})
	}
}

// Media types carry parameters (`; version=1.4`) and arbitrary case in practice;
// recognition must not be defeated by either.
func TestDockerTree_ArtifactType_ParametersAndCaseIgnored(t *testing.T) {
	leaf := ociLeaf(t, domain.DockerBrowseRow{
		ComponentID: "c1", ImageName: "art", Version: "1.0",
		SamplePath:   "/manifests/art/1.0",
		ArtifactType: "Application/vnd.cyclonedx+json; version=1.4",
	})
	assert.Equal(t, "sbom", leaf.ArtifactType)
}
