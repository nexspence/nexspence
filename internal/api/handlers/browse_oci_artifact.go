package handlers

import "strings"

// This file names the kind of artifact an OCI manifest holds, for display only.
// Nothing in the OCI Distribution protocol reads it: a manifest is served,
// referenced and deleted by its media type exactly as pushed, and a media type
// this table does not know is shown to the user verbatim rather than hidden or
// rewritten. The table exists so a Helm chart, a WASM module and a container
// image do not all read as the same anonymous entry in the browse tree.

// ociExactArtifactLabels holds the media types that must match whole, because
// the words in them recur across unrelated families. "image" in particular
// appears in an image index and in a BuildKit attestation manifest, neither of
// which is a container image, so it cannot be matched as a substring.
var ociExactArtifactLabels = map[string]string{
	// Container image configs. The OCI one is what `docker push` writes today;
	// the Docker one is what older daemons and Docker schema 2 manifests write,
	// and plenty of stored images still carry it.
	"application/vnd.oci.image.config.v1+json":       "image",
	"application/vnd.docker.container.image.v1+json": "image",
}

// ociArtifactLabelPatterns names the remaining families by substring, first
// match winning. Order carries meaning: cosign labels its SBOM and attestation
// attachments with media types that read like its signature ones, so those have
// to be recognized before any signature rule can claim them.
var ociArtifactLabelPatterns = []struct {
	substr string
	label  string
}{
	// cosign OCI 1.1 artifact types — .sbom and .att before .sig.
	{"cosign.artifact.sbom", "sbom"},
	{"cosign.artifact.att", "attestation"},
	{"cosign.artifact.sig", "signature"},
	// cosign's original signature layer type, and sigstore bundles.
	{"cosign.simplesigning", "signature"},
	{"sigstore.bundle", "signature"},
	// Helm charts pushed with `helm push oci://` — config and chart content.
	{"helm", "chart"},
	// WASM modules, from wasm-to-oci and from ORAS pushes of component binaries.
	{"wasm", "wasm"},
	// SBOMs: SPDX (cosign writes text/spdx*, ORAS and Syft write
	// application/spdx+json), CycloneDX, and Syft's native document.
	{"spdx", "sbom"},
	{"cyclonedx", "sbom"},
	{"syft", "sbom"},
	// Attestations: in-toto statements, their DSSE envelopes, and the manifests
	// BuildKit attaches to a build.
	{"in-toto", "attestation"},
	{"dsse", "attestation"},
	{"attestation", "attestation"},
	// A last catch for anything that spells out what it is.
	{"signature", "signature"},
}

// ociArtifactLabel turns the media type recorded in oci_artifact_type into a
// short label. An empty type stays empty — a component pushed before this
// metadata was recorded must not gain a label it never had — and an unknown
// type is returned exactly as it was given, so the browse tree never claims to
// know more about an artifact than the registry does.
func ociArtifactLabel(mediaType string) string {
	if strings.TrimSpace(mediaType) == "" {
		return ""
	}
	// Media types are case-insensitive and may carry parameters
	// ("application/vnd.cyclonedx+json; version=1.4"); neither may defeat a match.
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(mediaType, ";", 2)[0]))
	if label, ok := ociExactArtifactLabels[base]; ok {
		return label
	}
	for _, p := range ociArtifactLabelPatterns {
		if strings.Contains(base, p.substr) {
			return p.label
		}
	}
	return mediaType
}
