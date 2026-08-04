package oci

import "encoding/json"

// component.Extra keys holding the manifest metadata. They are what the
// referrers API and the browse UI read; nothing in the protocol depends on them.
const (
	extraMediaTypeKey    = "oci_media_type"
	extraArtifactTypeKey = "oci_artifact_type"
	extraSubjectKey      = "oci_subject"
	extraAnnotationsKey  = "oci_annotations"
	// extraSourceDigestKey records which manifest the other keys were derived
	// from, so a cached copy is re-typed exactly when its content changes.
	extraSourceDigestKey = "oci_source_digest"
)

// maxManifestBytes is the manifest size limit from the OCI Distribution Spec.
// It caps how much of a push body is buffered for parsing.
const maxManifestBytes = 4 << 20

// manifestMeta is the descriptive subset of an OCI manifest the registry stores.
type manifestMeta struct {
	MediaType    string
	ArtifactType string
	Subject      string
	Annotations  map[string]string
}

// parseManifestMeta reads the descriptive fields of a manifest. ok is false when
// the body is not a JSON object; an image index parses fine and simply yields no
// artifact type.
func parseManifestMeta(body []byte) (manifestMeta, bool) {
	var doc struct {
		MediaType    string `json:"mediaType"`
		ArtifactType string `json:"artifactType"`
		Config       struct {
			MediaType string `json:"mediaType"`
		} `json:"config"`
		Subject struct {
			Digest string `json:"digest"`
		} `json:"subject"`
		Annotations map[string]string `json:"annotations"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return manifestMeta{}, false
	}
	artifactType := doc.ArtifactType
	if artifactType == "" {
		// OCI 1.0 has no artifactType field: Helm and cosign put the type in
		// config.mediaType instead, and clients still read it from there.
		artifactType = doc.Config.MediaType
	}
	return manifestMeta{
		MediaType:    doc.MediaType,
		ArtifactType: artifactType,
		Subject:      doc.Subject.Digest,
		Annotations:  doc.Annotations,
	}, true
}

// extraFrom renders the metadata as component.Extra entries. Empty fields are
// left out: Extra is merged, not replaced, so writing a blank value would erase
// what an earlier push recorded.
func extraFrom(m manifestMeta) map[string]any {
	extra := make(map[string]any, 4)
	if m.MediaType != "" {
		extra[extraMediaTypeKey] = m.MediaType
	}
	if m.ArtifactType != "" {
		extra[extraArtifactTypeKey] = m.ArtifactType
	}
	if m.Subject != "" {
		extra[extraSubjectKey] = m.Subject
	}
	if len(m.Annotations) > 0 {
		annotations := make(map[string]any, len(m.Annotations))
		for k, v := range m.Annotations {
			annotations[k] = v
		}
		extra[extraAnnotationsKey] = annotations
	}
	return extra
}
