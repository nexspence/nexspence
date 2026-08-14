package apt_test

import (
	"bytes"
	"compress/gzip"
	"crypto/md5" //nolint:gosec // apt protocol checksum
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/apt"
	"github.com/nexspence-oss/nexspence/internal/testutil"
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
	// Release/InRelease are rebuilt from the union: a member's own document
	// describes that member's indexes, not the group's (#221).
	src, ok = h.GroupIndexSourcePath("/dists/focal/Release")
	require.True(t, ok)
	assert.Equal(t, "/dists/focal/Release", src)
	src, ok = h.GroupIndexSourcePath("/dists/focal/InRelease")
	require.True(t, ok)
	assert.Equal(t, "/dists/focal/InRelease", src)

	_, ok = h.GroupIndexSourcePath("/dists/focal/Release.gpg")
	assert.False(t, ok, "a detached signature needs a key, so it stays on plain fan-out")
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

// ── Release merged from the union (#221) ─────────────────────

const (
	releaseAmd64 = "Origin: Nexspence\nSuite: focal\nCodename: focal\n" +
		"Date: Mon, 04 Aug 2026 10:00:00 UTC\nArchitectures: amd64 all\nComponents: main\n" +
		"SHA256:\n 1111 10 main/binary-amd64/Packages\n"
	releaseArm64 = "Origin: Nexspence\nSuite: focal\nCodename: focal\n" +
		"Date: Wed, 05 Aug 2026 11:00:00 UTC\nArchitectures: arm64 all\nComponents: main contrib\n" +
		"SHA256:\n 2222 20 main/binary-arm64/Packages\n"
)

func releaseParts() []formats.GroupIndexPart {
	return []formats.GroupIndexPart{
		{Member: "m1", Body: []byte(releaseAmd64)},
		{Member: "m2", Body: []byte(releaseArm64)},
	}
}

// The group's Release covers every architecture and component its members
// declare, and its checksums are taken over the bodies the GROUP serves.
func TestApt_MergeRelease_UnionsArchitecturesAndChecksumsFetchedBodies(t *testing.T) {
	h := apt.New(formats.Deps{})
	fetched := map[string][]byte{}
	fetch := func(p string) ([]byte, error) {
		if !strings.Contains(p, "binary-amd64") && !strings.Contains(p, "binary-arm64") {
			return nil, errors.New("no such index")
		}
		body := []byte("merged-index-for " + p)
		fetched[p] = body
		return body, nil
	}

	body, ct, err := h.MergeGroupIndexWithFetch("g", "/dists/focal/Release", releaseParts(), fetch)
	require.NoError(t, err)
	assert.Contains(t, ct, "text/plain")
	out := string(body)

	assert.Contains(t, out, "Architectures: amd64 arm64 all")
	assert.Contains(t, out, "Components: contrib main")
	assert.Contains(t, out, "Suite: focal")
	// The newest member Date wins, so the document tracks content, not the clock.
	assert.Contains(t, out, "Date: Wed, 05 Aug 2026 11:00:00 UTC")

	// Every fetched body is vouched for with its own hash and length.
	require.NotEmpty(t, fetched)
	for p, b := range fetched {
		rel := strings.TrimPrefix(p, "/dists/focal/")
		assert.Contains(t, out, fmt.Sprintf(" %x %d %s\n", sha256.Sum256(b), len(b), rel))
		assert.Contains(t, out, fmt.Sprintf(" %x %d %s\n", md5.Sum(b), len(b), rel)) //nolint:gosec // apt protocol checksum
	}
	// A member's own checksum lines never survive into the group's document.
	assert.NotContains(t, out, "1111")
	assert.NotContains(t, out, "2222")
}

// Without a fetcher there are no index bodies to vouch for, so the document is
// headers only — never a checksum the group cannot stand behind.
func TestApt_MergeRelease_WithoutFetcher_HasNoChecksums(t *testing.T) {
	h := apt.New(formats.Deps{})
	body, _, err := h.MergeGroupIndex("g", "/dists/focal/Release", releaseParts())
	require.NoError(t, err)
	out := string(body)
	assert.Contains(t, out, "Architectures: amd64 arm64 all")
	assert.NotContains(t, out, "binary-amd64/Packages")
}

// An unsigned group serves InRelease plain — the same document as Release.
func TestApt_MergeInRelease_UnsignedGroupServesPlain(t *testing.T) {
	h := apt.New(formats.Deps{Repos: testutil.NewRepoRepo(testutil.SimpleRepo("g", "apt"))})

	release, _, err := h.MergeGroupIndex("g", "/dists/focal/Release", releaseParts())
	require.NoError(t, err)
	inRelease, _, err := h.MergeGroupIndex("g", "/dists/focal/InRelease", releaseParts())
	require.NoError(t, err)
	assert.Equal(t, string(release), string(inRelease))
}

// A group with its own key signs the union inline. A member's key is never
// used: it cannot vouch for content that member did not serve.
func TestApt_MergeInRelease_SignedWithTheGroupsOwnKey(t *testing.T) {
	groupRepo, ring := signedRepo(t, "g")
	member, _ := signedRepo(t, "m1") // a member key must not be reached for
	h := apt.New(formats.Deps{Repos: testutil.NewRepoRepo(groupRepo, member)})

	body, _, err := h.MergeGroupIndex("g", "/dists/focal/InRelease", releaseParts())
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(body, []byte("-----BEGIN PGP SIGNED MESSAGE-----")),
		"a signed group clearsigns InRelease, got: %.60s", body)

	block, _ := clearsign.Decode(body)
	require.NotNil(t, block)
	_, err = openpgp.CheckDetachedSignature(ring, bytes.NewReader(block.Bytes), block.ArmoredSignature.Body, nil)
	require.NoError(t, err, "signed with the group's own key")
}

// Members that declare no parsable Date leave the group with nothing to pin the
// document to, so it falls back to now rather than to a zero timestamp.
func TestApt_MergeRelease_NoMemberDate_FallsBackToNow(t *testing.T) {
	h := apt.New(formats.Deps{})
	parts := []formats.GroupIndexPart{{Member: "m1", Body: []byte("Architectures: amd64\n")}}

	body, _, err := h.MergeGroupIndex("g", "/dists/focal/Release", parts)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "Date: Mon, 01 Jan 0001")
	assert.Contains(t, string(body), "Date: "+time.Now().UTC().Format("Mon, 02 Jan 2006"))
}
