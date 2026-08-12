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

// ociEnvelopeTypes are the artifact types that describe a wrapper rather than
// its contents. A cosign attestation types its manifest after the DSSE envelope
// or sigstore bundle that carries the payload, so the type alone can only say
// "something signed" — the predicate annotation is what names the statement.
var ociEnvelopeTypes = []string{"sigstore.bundle", "dsse"}

// ociPredicateLabels names the in-toto predicates worth distinguishing, by
// substring of the predicate URI (SPDX is "https://spdx.dev/Document",
// CycloneDX "https://cyclonedx.org/bom"). Anything else stated in an envelope
// is an attestation of some kind, which is already more than the envelope type
// said on its own.
var ociPredicateLabels = []struct {
	substr string
	label  string
}{
	{"cyclonedx", "sbom"},
	{"spdx", "sbom"},
	{"syft", "sbom"},
}

// ociArtifactLabel turns the media type recorded in oci_artifact_type into a
// short label. An empty type stays empty — a component pushed before this
// metadata was recorded must not gain a label it never had — and an unknown
// type is returned exactly as it was given, so the browse tree never claims to
// know more about an artifact than the registry does.
//
// predicateType is the manifest's dev.sigstore.bundle.predicateType annotation,
// and is consulted only for the envelope types above: an artifact that already
// names itself is never re-labeled by a predicate it happens to carry.
func ociArtifactLabel(mediaType, predicateType string) string {
	if strings.TrimSpace(mediaType) == "" {
		return ""
	}
	if label, ok := ociPredicateLabel(mediaType, predicateType); ok {
		return label
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

// ociPredicateLabel names an envelope by what it wraps. ok is false whenever the
// predicate must not decide the label: a type that is not an envelope, or an
// envelope with no predicate recorded — a bundle we know nothing more about is
// honestly a signature, and guessing past that would invent a claim.
func ociPredicateLabel(mediaType, predicateType string) (string, bool) {
	predicate := strings.ToLower(strings.TrimSpace(predicateType))
	if predicate == "" {
		return "", false
	}
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(mediaType, ";", 2)[0]))
	envelope := false
	for _, e := range ociEnvelopeTypes {
		if strings.Contains(base, e) {
			envelope = true
			break
		}
	}
	if !envelope {
		return "", false
	}
	for _, p := range ociPredicateLabels {
		if strings.Contains(predicate, p.substr) {
			return p.label, true
		}
	}
	// Signed, and it states something — that is an attestation whatever the
	// statement turns out to be.
	return "attestation", true
}
