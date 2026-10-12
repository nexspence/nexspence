package oci_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A tag's component metadata follows the manifest the tag holds (#668): kept
// when the same bytes are pushed again, dropped when the tag gets different
// bytes, whether by an overwrite or after the tag was deleted.

func TestPushManifest_RepushedTagDropsOldSubject(t *testing.T) {
	r, d := setupWithDeps(hostedOCIRepo("r1", "oci-hosted"))
	subject := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "1.2.3", helmChartManifest)
	pushManifestBody(t, r, "oci-hosted", "charts/nginx", "moving", referrerManifest(cosignSigArtifactType, subject))
	requireSubject(t, d, "oci-hosted", "charts/nginx", "moving", subject)

	pushManifestBody(t, r, "oci-hosted", "charts/nginx", "moving", helmChartManifest)
	comp := componentOf(t, d, "oci-hosted", "charts/nginx", "moving")
	assert.NotContains(t, comp.Extra, "oci_subject")
	_, _, descs := getReferrers(t, r, "oci-hosted", "charts/nginx", subject, "")
	assert.Len(t, descs, 1, "only the signature's digest alias still refers to the subject")
}

func TestPushManifest_DeletedThenRepushedTagDropsOldSubject(t *testing.T) {
	r, d := setupWithDeps(hostedOCIRepo("r1", "oci-hosted"))
	subject := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "1.2.3", helmChartManifest)
	pushManifestBody(t, r, "oci-hosted", "charts/nginx", "moving", referrerManifest(cosignSigArtifactType, subject))
	require.Equal(t, http.StatusAccepted, deleteManifest(t, r, "oci-hosted", "charts/nginx", "moving").Code)

	pushManifestBody(t, r, "oci-hosted", "charts/nginx", "moving", helmChartManifest)
	comp := componentOf(t, d, "oci-hosted", "charts/nginx", "moving")
	assert.NotContains(t, comp.Extra, "oci_subject",
		"the deleted tag's metadata must not attach to the manifest pushed in its place")
}

func TestPushManifest_IdenticalRepushKeepsExtra(t *testing.T) {
	r, d := setupWithDeps(hostedOCIRepo("r1", "oci-hosted"))
	pushManifestBody(t, r, "oci-hosted", "charts/nginx", "1.2.3", helmChartManifest)
	comp := componentOf(t, d, "oci-hosted", "charts/nginx", "1.2.3")
	require.NoError(t, d.Components.UpdateExtra(t.Context(), comp.ID, map[string]any{"scan_result": "ok"}))

	pushManifestBody(t, r, "oci-hosted", "charts/nginx", "1.2.3", helmChartManifest)
	assert.Equal(t, "ok", componentOf(t, d, "oci-hosted", "charts/nginx", "1.2.3").Extra["scan_result"])
}
