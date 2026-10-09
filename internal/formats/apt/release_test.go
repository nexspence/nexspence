package apt_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/apt"
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

// releaseChecksum returns the SHA256 hash and size a Release lists for rel, or
// fails the test when the document carries no such line.
func releaseChecksum(t *testing.T, release, rel string) (string, string) {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s+([0-9a-f]{64})\s+(\d+)\s+` + regexp.QuoteMeta(rel) + `$`)
	m := re.FindStringSubmatch(release)
	require.NotNil(t, m, "Release must list %s under SHA256:\n%s", rel, release)
	return m[1], m[2]
}

// assertVouches checks that the Release lists rel with the hash and size of
// the index the repository actually serves at that path.
func assertVouches(t *testing.T, r *gin.Engine, repoName, release, rel string) {
	t.Helper()
	body := fetch(t, r, "/repository/"+repoName+"/dists/focal/"+rel).Body.Bytes()
	hash, size := releaseChecksum(t, release, rel)
	assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(body)), hash, rel)
	assert.Equal(t, fmt.Sprintf("%d", len(body)), size, rel)
}

// A repository whose debs are all Architecture: all — metapackages, data,
// scripts — is read by apt through binary-all alone. Release declares "all",
// so it has to vouch for that index: a signed Release with no checksum lines
// is refused outright ("provides only weak security information").
func TestApt_Release_AllOnlyRepositoryVouchesForBinaryAll(t *testing.T) {
	repo := testutil.SimpleRepo("debs-all", "apt")
	r := setup(repo)

	require.Equal(t, http.StatusCreated, putDeb(r, "debs-all", "/pool/main/example-meta_1.0_all.deb", "meta"))

	release := fetch(t, r, "/repository/debs-all/dists/focal/Release").Body.String()
	assert.Contains(t, release, "Architectures: all\n")
	assertVouches(t, r, "debs-all", release, "main/binary-all/Packages")
	assertVouches(t, r, "debs-all", release, "main/binary-all/Packages.gz")

	idx := fetch(t, r, "/repository/debs-all/dists/focal/main/binary-all/Packages").Body.String()
	assert.Contains(t, idx, "Package: example-meta", "the binary-all index carries the arch-all deb")
}

// Every architecture the Architectures line declares is vouched for, "all"
// included, and each checksum matches the index served at that path.
func TestApt_Release_VouchesForEveryDeclaredArchitecture(t *testing.T) {
	repo := testutil.SimpleRepo("debs-mixed", "apt")
	r := setup(repo)

	require.Equal(t, http.StatusCreated, putDeb(r, "debs-mixed", "/pool/main/curl_8.0.0_amd64.deb", "a"))
	require.Equal(t, http.StatusCreated, putDeb(r, "debs-mixed", "/pool/main/vim_9.0_arm64.deb", "b"))
	require.Equal(t, http.StatusCreated, putDeb(r, "debs-mixed", "/pool/main/tzdata_2024a_all.deb", "c"))

	release := fetch(t, r, "/repository/debs-mixed/dists/focal/Release").Body.String()
	assert.Contains(t, release, "Architectures: amd64 arm64 all\n")
	for _, arch := range []string{"amd64", "arm64", "all"} {
		assertVouches(t, r, "debs-mixed", release, "main/binary-"+arch+"/Packages")
		assertVouches(t, r, "debs-mixed", release, "main/binary-"+arch+"/Packages.gz")
	}

	all := fetch(t, r, "/repository/debs-mixed/dists/focal/main/binary-all/Packages").Body.String()
	assert.Contains(t, all, "Package: tzdata")
	assert.NotContains(t, all, "Package: curl", "binary-all lists only arch-all debs")
	assert.NotContains(t, all, "Package: vim", "binary-all lists only arch-all debs")
}

// pagedAssets pages List the way postgres does (ORDER BY path LIMIT/OFFSET);
// the shared fake returns everything, which would hide a first-page cut.
type pagedAssets struct{ *testutil.AssetRepo }

func (p pagedAssets) List(ctx context.Context, repoName string, limit, offset int) (*domain.Page[domain.Asset], error) {
	page, err := p.AssetRepo.List(ctx, repoName, limit, offset)
	if err != nil {
		return nil, err
	}
	items := page.Items
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	if offset > len(items) {
		offset = len(items)
	}
	items = items[offset:]
	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}
	return &domain.Page[domain.Asset]{Items: items}, nil
}

// The architectures come from every deb the Packages indexes are built from,
// not from a first page of the repository: an arch whose debs sort past it
// would be served but never vouched for, and apt would reject its index.
func TestApt_Release_ArchitecturesCoverEveryDebNotAFirstPage(t *testing.T) {
	repo := testutil.SimpleRepo("debs-big", "apt")
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     pagedAssets{testutil.NewAssetRepo()},
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := apt.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	for i := 0; i < 1000; i++ {
		path := fmt.Sprintf("/pool/main/pkg%04d_1.0_amd64.deb", i)
		require.Equal(t, http.StatusCreated, putDeb(r, "debs-big", path, fmt.Sprintf("p%d", i)))
	}
	// Sorts after all 1000 amd64 paths, so a single first page never sees it.
	require.Equal(t, http.StatusCreated, putDeb(r, "debs-big", "/pool/main/zz-late_1.0_arm64.deb", "late"))

	release := fetch(t, r, "/repository/debs-big/dists/focal/Release").Body.String()
	assert.Contains(t, release, "Architectures: amd64 arm64 all\n")
	assertVouches(t, r, "debs-big", release, "main/binary-arm64/Packages")
}
