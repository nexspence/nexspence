package apt_test

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func fetch(t *testing.T, r *gin.Engine, url string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, url)
	return w
}

// #103: the Packages index must honor the binary-<arch> path segment.
func TestApt_PackagesIndex_FiltersByArch(t *testing.T) {
	repo := testutil.SimpleRepo("debs-arch", "apt")
	r := setup(repo)

	require.Equal(t, http.StatusCreated, putDeb(r, "debs-arch", "/pool/main/curl_8.0.0_amd64.deb", "a"))
	require.Equal(t, http.StatusCreated, putDeb(r, "debs-arch", "/pool/main/vim_9.0_arm64.deb", "b"))
	require.Equal(t, http.StatusCreated, putDeb(r, "debs-arch", "/pool/main/tzdata_2024a_all.deb", "c"))

	amd := fetch(t, r, "/repository/debs-arch/dists/focal/main/binary-amd64/Packages").Body.String()
	assert.Contains(t, amd, "Package: curl")
	assert.NotContains(t, amd, "Package: vim", "arm64 deb must not appear in binary-amd64")
	assert.Contains(t, amd, "Package: tzdata", "arch 'all' appears in every index")

	arm := fetch(t, r, "/repository/debs-arch/dists/focal/main/binary-arm64/Packages").Body.String()
	assert.Contains(t, arm, "Package: vim")
	assert.NotContains(t, arm, "Package: curl")
}

// #103: apt verifies the Packages files against the checksums in Release.
func TestApt_Release_ChecksumsMatchServedPackages(t *testing.T) {
	repo := testutil.SimpleRepo("debs-rel", "apt")
	r := setup(repo)

	require.Equal(t, http.StatusCreated, putDeb(r, "debs-rel", "/pool/main/curl_8.0.0_amd64.deb", "curl-bytes"))

	release := fetch(t, r, "/repository/debs-rel/dists/focal/Release").Body.String()
	assert.Contains(t, release, "SHA256:")
	assert.Contains(t, release, "MD5Sum:")
	assert.Regexp(t, `Architectures:.*amd64`, release, "real archs listed")

	// The SHA256 line for main/binary-amd64/Packages must match the actually
	// served index (hash + size).
	pkgs := fetch(t, r, "/repository/debs-rel/dists/focal/main/binary-amd64/Packages").Body.Bytes()
	wantHash := fmt.Sprintf("%x", sha256.Sum256(pkgs))
	re := regexp.MustCompile(`(?m)^\s+([0-9a-f]{64})\s+(\d+)\s+main/binary-amd64/Packages$`)
	m := re.FindStringSubmatch(release)
	require.NotNil(t, m, "Release must list main/binary-amd64/Packages under SHA256:\n%s", release)
	assert.Equal(t, wantHash, m[1])
	assert.Equal(t, fmt.Sprintf("%d", len(pkgs)), m[2])
}
