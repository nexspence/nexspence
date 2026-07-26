package apt_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/apt"
)

const (
	stanzaCurl  = "Package: curl\nVersion: 8.0.0\nFilename: /pool/main/c/curl/curl_8.0.0_amd64.deb\n"
	stanzaNginx = "Package: nginx\nVersion: 1.25.0\nFilename: /pool/main/n/nginx/nginx_1.25.0_amd64.deb\n"
)

func TestApt_GroupIndexSourcePath(t *testing.T) {
	h := apt.New(formats.Deps{})

	src, ok := h.GroupIndexSourcePath("/dists/focal/main/binary-amd64/Packages")
	require.True(t, ok)
	assert.Equal(t, "/dists/focal/main/binary-amd64/Packages", src)

	// .gz fans out on the PLAIN document; the merger gzips the result.
	src, ok = h.GroupIndexSourcePath("/dists/focal/main/binary-amd64/Packages.gz")
	require.True(t, ok)
	assert.Equal(t, "/dists/focal/main/binary-amd64/Packages", src)

	_, ok = h.GroupIndexSourcePath("/pool/main/c/curl/curl_8.0.0_amd64.deb")
	assert.False(t, ok, ".deb downloads keep first-non-404")
	_, ok = h.GroupIndexSourcePath("/dists/focal/Release")
	assert.False(t, ok, "Release is boilerplate — not merged")
}

func TestApt_MergeGroupIndex_UnionsStanzas(t *testing.T) {
	h := apt.New(formats.Deps{})
	parts := []formats.GroupIndexPart{
		{Member: "m1", Body: []byte(stanzaCurl + "\n")},
		{Member: "m2", Body: []byte(stanzaCurl + "\n" + stanzaNginx + "\n")}, // curl duplicated
	}

	body, ct, err := h.MergeGroupIndex("g", "/dists/focal/main/binary-amd64/Packages", parts)
	require.NoError(t, err)
	assert.Contains(t, ct, "text/plain")
	out := string(body)
	assert.Contains(t, out, "Package: curl")
	assert.Contains(t, out, "Package: nginx")
	assert.Equal(t, 1, bytes.Count(body, []byte("Package: curl")), "dedup by Filename")
}

func TestApt_MergeGroupIndex_GzipOutput(t *testing.T) {
	h := apt.New(formats.Deps{})
	parts := []formats.GroupIndexPart{{Member: "m1", Body: []byte(stanzaCurl + "\n")}}

	body, ct, err := h.MergeGroupIndex("g", "/dists/focal/main/binary-amd64/Packages.gz", parts)
	require.NoError(t, err)
	assert.Contains(t, ct, "gzip")

	zr, err := gzip.NewReader(bytes.NewReader(body))
	require.NoError(t, err)
	plain, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Contains(t, string(plain), "Package: curl")
}

func TestApt_MergeGroupIndex_EmptyMemberContributesNothing(t *testing.T) {
	h := apt.New(formats.Deps{})
	parts := []formats.GroupIndexPart{
		{Member: "empty", Body: []byte("")}, // apt hosted answers 200-empty (#99 shadowing)
		{Member: "full", Body: []byte(stanzaNginx + "\n")},
	}
	body, _, err := h.MergeGroupIndex("g", "/dists/focal/main/binary-amd64/Packages", parts)
	require.NoError(t, err)
	assert.Contains(t, string(body), "Package: nginx")
}
