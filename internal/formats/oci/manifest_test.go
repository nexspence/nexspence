package oci

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A Helm chart pushed with `helm push oci://` carries its type in
// config.mediaType — artifactType is absent in the OCI 1.0 manifests Helm writes.
func TestParseManifestMeta_HelmChart(t *testing.T) {
	body := []byte(`{
	  "schemaVersion": 2,
	  "mediaType": "application/vnd.oci.image.manifest.v1+json",
	  "config": {"mediaType": "application/vnd.cncf.helm.config.v1+json", "digest": "sha256:aa", "size": 12},
	  "layers": [{"mediaType": "application/vnd.cncf.helm.chart.content.v1.tar+gzip", "digest": "sha256:bb", "size": 34}],
	  "annotations": {"org.opencontainers.image.title": "nginx", "org.opencontainers.image.version": "1.2.3"}
	}`)

	meta, ok := parseManifestMeta(body)

	require.True(t, ok)
	assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", meta.MediaType)
	assert.Equal(t, "application/vnd.cncf.helm.config.v1+json", meta.ArtifactType)
	assert.Empty(t, meta.Subject)
	assert.Equal(t, "1.2.3", meta.Annotations["org.opencontainers.image.version"])
}

// An ORAS artifact sets artifactType explicitly; it wins over config.mediaType.
func TestParseManifestMeta_ORASArtifact_PrefersArtifactType(t *testing.T) {
	body := []byte(`{
	  "schemaVersion": 2,
	  "mediaType": "application/vnd.oci.image.manifest.v1+json",
	  "artifactType": "application/vnd.wasm.content.layer.v1+wasm",
	  "config": {"mediaType": "application/vnd.oci.empty.v1+json", "digest": "sha256:cc", "size": 2}
	}`)

	meta, ok := parseManifestMeta(body)

	require.True(t, ok)
	assert.Equal(t, "application/vnd.wasm.content.layer.v1+wasm", meta.ArtifactType)
}

// A cosign signature points at the image it signs through subject.
func TestParseManifestMeta_SignatureCarriesSubject(t *testing.T) {
	body := []byte(`{
	  "schemaVersion": 2,
	  "mediaType": "application/vnd.oci.image.manifest.v1+json",
	  "artifactType": "application/vnd.dev.cosign.artifact.sig.v1+json",
	  "subject": {"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": "sha256:deadbeef", "size": 7}
	}`)

	meta, ok := parseManifestMeta(body)

	require.True(t, ok)
	assert.Equal(t, "sha256:deadbeef", meta.Subject)
}

func TestParseManifestMeta_NotJSON(t *testing.T) {
	_, ok := parseManifestMeta([]byte("not a manifest"))
	assert.False(t, ok)
}

func TestExtraFrom_OmitsEmptyFields(t *testing.T) {
	extra := extraFrom(manifestMeta{MediaType: "application/vnd.oci.image.manifest.v1+json"})

	assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", extra[extraMediaTypeKey])
	assert.NotContains(t, extra, extraArtifactTypeKey, "an empty field must not overwrite a value from an earlier push")
	assert.NotContains(t, extra, extraSubjectKey)
	assert.NotContains(t, extra, extraAnnotationsKey)
}
