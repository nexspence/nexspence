package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// GHSA-pg67-wh39-mx9j: an apt repository's signing key and passphrase live in
// formatConfig and must never reach a reader of the repository.
func TestRedactedRepository_StripsSigningKeyAndPassphrase(t *testing.T) {
	in := Repository{Name: "apt-signed", Format: FormatApt, FormatConfig: map[string]any{
		"signing_key":            "-----BEGIN PGP PRIVATE KEY BLOCK-----",
		"signing_key_passphrase": "s3cret",
		"distribution":           "stable",
	}}
	out := RedactedRepository(in)
	assert.NotContains(t, out.FormatConfig, "signing_key")
	assert.NotContains(t, out.FormatConfig, "signing_key_passphrase")
	assert.Equal(t, true, out.FormatConfig["signing_key_set"])
	assert.Equal(t, true, out.FormatConfig["signing_key_passphrase_set"])
	assert.Equal(t, "stable", out.FormatConfig["distribution"])
	assert.Equal(t, "s3cret", in.FormatConfig["signing_key_passphrase"], "the input is untouched")
}

func TestRedactedRepository_NoSigningKeyLeavesNoMarker(t *testing.T) {
	out := RedactedRepository(Repository{FormatConfig: map[string]any{"distribution": "stable"}})
	assert.NotContains(t, out.FormatConfig, "signing_key_set")
	assert.Nil(t, RedactedRepository(Repository{}).FormatConfig)
}
