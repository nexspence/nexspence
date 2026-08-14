package group_test

import (
	"bytes"
	"compress/gzip"
	"crypto/md5" //nolint:gosec // apt protocol checksum
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/apt"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// buildAptGroupEngine wires real apt handlers for members and a group over them.
func buildAptGroupEngine(t *testing.T, groupName string, memberNames ...string) *gin.Engine {
	t.Helper()

	repos := make([]*domain.Repository, 0, len(memberNames)+1)
	ms := make([]interface{}, len(memberNames))
	for i, name := range memberNames {
		repos = append(repos, testutil.SimpleRepo(name, "apt"))
		ms[i] = name
	}
	repos = append(repos, &domain.Repository{
		ID: "repo-" + groupName, Name: groupName, Format: "apt",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": ms},
	})

	repoRepo := testutil.NewRepoRepo(repos...)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	aptH := apt.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"apt": aptH})

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		aptH.ServeHTTP(c)
	})
	return r
}

func putDeb(t *testing.T, r *gin.Engine, repoName, filename string) {
	t.Helper()
	body := "deb-payload-" + filename
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+"/pool/main/x/"+filename, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Contains(t, []int{http.StatusCreated, http.StatusOK}, w.Code, "upload %s: %s", filename, w.Body.String())
}

// releaseField returns the value of a single-line "Field: value" header.
func releaseField(t *testing.T, release, field string) string {
	t.Helper()
	for _, line := range strings.Split(release, "\n") {
		if v, found := strings.CutPrefix(line, field+":"); found {
			return strings.TrimSpace(v)
		}
	}
	t.Fatalf("field %q not found in Release:\n%s", field, release)
	return ""
}

// releaseChecksums parses one checksum section into "relative path" → "hash size".
func releaseChecksums(release, section string) map[string]string {
	out := map[string]string{}
	inSection := false
	for _, line := range strings.Split(release, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), ":") && !strings.HasPrefix(line, " ") {
			inSection = strings.TrimSpace(line) == section+":"
			continue
		}
		if !inSection || !strings.HasPrefix(line, " ") {
			continue
		}
		f := strings.Fields(line)
		if len(f) == 3 {
			out[f[2]] = f[0] + " " + f[1]
		}
	}
	return out
}

// A group's Release must describe the group: every architecture its members
// contribute, and checksums over the very bytes the group's own Packages
// endpoints serve. Relaying one member's Release makes apt reject the repo with
// a hash-sum mismatch, or silently miss the other members' architectures.
func TestGroupMerge_AptReleaseDescribesTheMergedIndexes(t *testing.T) {
	r := buildAptGroupEngine(t, "ag", "ad1", "ad2")
	putDeb(t, r, "ad1", "curl_8.0.0_amd64.deb")
	putDeb(t, r, "ad2", "vim_9.0_arm64.deb")

	w := get(r, "/repository/ag/dists/stable/Release")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	release := w.Body.String()

	archs := strings.Fields(releaseField(t, release, "Architectures"))
	assert.Contains(t, archs, "amd64", "the group carries both members' architectures")
	assert.Contains(t, archs, "arm64")

	sha := releaseChecksums(release, "SHA256")
	md := releaseChecksums(release, "MD5Sum")
	require.NotEmpty(t, sha, "Release declares SHA256 checksums")

	for _, arch := range []string{"amd64", "arm64"} {
		for _, suffix := range []string{"", ".gz"} {
			rel := "main/binary-" + arch + "/Packages" + suffix
			served := get(r, "/repository/ag/dists/stable/"+rel)
			require.Equal(t, http.StatusOK, served.Code, rel)
			b := served.Body.Bytes()

			assert.Equal(t, fmt.Sprintf("%x %d", sha256.Sum256(b), len(b)), sha[rel],
				"SHA256 in Release must match the %s the group actually serves", rel)
			assert.Equal(t, fmt.Sprintf("%x %d", md5.Sum(b), len(b)), md[rel], //nolint:gosec // apt protocol checksum
				"MD5Sum in Release must match the %s the group actually serves", rel)
		}
	}

	// Both members' packages are reachable through the indexes Release covers.
	amd := get(r, "/repository/ag/dists/stable/main/binary-amd64/Packages").Body.String()
	arm := get(r, "/repository/ag/dists/stable/main/binary-arm64/Packages").Body.String()
	assert.Contains(t, amd, "curl")
	assert.Contains(t, arm, "vim")
}

// InRelease is the same document; unsigned groups serve it plain, which is what
// [trusted=yes] sources expect.
func TestGroupMerge_AptInReleaseMatchesRelease(t *testing.T) {
	r := buildAptGroupEngine(t, "ag2", "ae1", "ae2")
	putDeb(t, r, "ae1", "curl_8.0.0_amd64.deb")
	putDeb(t, r, "ae2", "vim_9.0_arm64.deb")

	rel := get(r, "/repository/ag2/dists/stable/Release")
	inrel := get(r, "/repository/ag2/dists/stable/InRelease")
	require.Equal(t, http.StatusOK, inrel.Code)
	assert.Equal(t, rel.Body.String(), inrel.Body.String())
}

// A one-member group is that member: its Release must be the member's own.
func TestGroupMerge_AptSingleMemberReleaseMatchesMember(t *testing.T) {
	r := buildAptGroupEngine(t, "ag3", "af1")
	putDeb(t, r, "af1", "curl_8.0.0_amd64.deb")

	direct := get(r, "/repository/af1/dists/stable/Release")
	viaGroup := get(r, "/repository/ag3/dists/stable/Release")
	require.Equal(t, http.StatusOK, viaGroup.Code)
	assert.Equal(t, direct.Body.String(), viaGroup.Body.String())
}

// The gzip flavor the checksums cover has to be the same document, not a
// separately merged one.
func TestGroupMerge_AptPackagesGzUnzipsToPlain(t *testing.T) {
	r := buildAptGroupEngine(t, "ag4", "ah1", "ah2")
	putDeb(t, r, "ah1", "curl_8.0.0_amd64.deb")
	putDeb(t, r, "ah2", "wget_1.21_amd64.deb")

	plain := get(r, "/repository/ag4/dists/stable/main/binary-amd64/Packages").Body.Bytes()
	gz := get(r, "/repository/ag4/dists/stable/main/binary-amd64/Packages.gz").Body.Bytes()

	zr, err := gzip.NewReader(bytes.NewReader(gz))
	require.NoError(t, err)
	unzipped, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Equal(t, string(plain), string(unzipped))
}
