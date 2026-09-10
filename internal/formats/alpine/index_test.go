package alpine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackUnpackIndexTarGz_RoundTrip(t *testing.T) {
	plain := "C:Q1xxx\nP:curl\nV:8.9.0-r0\nA:x86_64\nS:100\nI:200\n\n"

	packed, err := packIndexTarGz([]byte(plain))
	require.NoError(t, err)

	unpacked, err := unpackIndexTarGz(packed)
	require.NoError(t, err)
	assert.Equal(t, plain, unpacked)
}

func TestUnpackIndexTarGz_RejectsNonGzip(t *testing.T) {
	_, err := unpackIndexTarGz([]byte("not a tar.gz"))
	assert.Error(t, err)
}

func TestPathArch(t *testing.T) {
	// Single-level layout: the arch is the only directory component.
	assert.Equal(t, "x86_64", pathArch("/x86_64/APKINDEX.tar.gz"))
	assert.Equal(t, "aarch64", pathArch("/aarch64/curl-8.9.0-r0.apk"))
	assert.Equal(t, "", pathArch("/"))

	// Alpine's real published layout is "/<branch>/<repo>/<arch>/..." — the
	// arch is the LAST directory component, not the first one (PR #440
	// review: taking the first segment used to return "v3.20"/"edge" here).
	assert.Equal(t, "x86_64", pathArch("/v3.20/main/x86_64/APKINDEX.tar.gz"))
	assert.Equal(t, "aarch64", pathArch("/edge/community/aarch64/APKINDEX.tar.gz"))
}
