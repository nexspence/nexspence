package apt

import (
	"bytes"
	"crypto/md5"  //nolint:gosec // apt protocol checksum
	"crypto/sha1" //nolint:gosec // apt protocol checksum
	"crypto/sha256"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

type ctlEnv struct {
	r      *gin.Engine
	comps  *testutil.ComponentRepo
	assets *testutil.AssetRepo
	repo   *domain.Repository
}

func newCtlEnv(t *testing.T, name string, writePolicy domain.WritePolicy) *ctlEnv {
	t.Helper()
	repo := testutil.SimpleRepo(name, "apt")
	if writePolicy != "" {
		repo.FormatConfig = map[string]any{domain.WritePolicyKey: string(writePolicy)}
	}
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: comps,
		Assets:     assets,
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return &ctlEnv{r: r, comps: comps, assets: assets, repo: repo}
}

func (e *ctlEnv) put(path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/repository/"+e.repo.Name+path, bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func (e *ctlEnv) del(path string) int {
	req := httptest.NewRequest(http.MethodDelete, "/repository/"+e.repo.Name+path, nil)
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w.Code
}

func (e *ctlEnv) get(t *testing.T, path string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/repository/"+e.repo.Name+path, nil)
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, path)
	return w.Body.String()
}

func ctlFor(pkg, version, arch, extra string) string {
	return fmt.Sprintf("Package: %s\nVersion: %s\nArchitecture: %s\nMaintainer: Foo Developers <foo@example.org>\n%sDescription: %s\n long text\n", pkg, version, arch, extra, pkg)
}

func debFor(t *testing.T, pkg, version, arch, extra, payload string) []byte {
	return buildDeb(t, debOpts{control: ctlFor(pkg, version, arch, extra), compression: "gz", data: payload})
}

// The filename's "version" disagrees with the control file: apt must see the
// control's Version and every relationship field, or exact pins elsewhere in
// the set can never be satisfied (#637).
func TestUpload_ControlFileDrivesTheStanza(t *testing.T) {
	e := newCtlEnv(t, "ctl", "")
	deb := buildDeb(t, debOpts{control: libControl, compression: "zst", data: "lib"})
	path := "/pool/main/libfoo2_2.3.1-1+build5_amd64.deb"
	require.Equal(t, http.StatusCreated, e.put(path, deb).Code)

	idx := e.get(t, "/dists/stable/main/binary-amd64/Packages")
	assert.Contains(t, idx, "Package: libfoo2\n")
	assert.Contains(t, idx, "Version: 2.3.1-1\n")
	assert.NotContains(t, idx, "build5\n", "the filename's version never reaches apt")
	assert.Contains(t, idx, "Depends: libfoo-common (= 2.3.1-1), libc6 (>= 2.35)\n")
	assert.Contains(t, idx, "Provides: libfoo-abi-2 (= 2.3.1-1)\n")
	assert.Contains(t, idx, "Conflicts: libfoo1\n")
	assert.Contains(t, idx, "Installed-Size: 2048\n")
	assert.Contains(t, idx, "Description: foo runtime library\n Shared library for foo.\n .\n Second paragraph")

	// Server-computed fields describe the bytes actually stored.
	assert.Contains(t, idx, "Filename: "+path+"\n")
	assert.Contains(t, idx, fmt.Sprintf("Size: %d\n", len(deb)))
	assert.Contains(t, idx, fmt.Sprintf("MD5sum: %x\n", md5.Sum(deb))) //nolint:gosec // apt protocol checksum
	assert.Contains(t, idx, fmt.Sprintf("SHA1: %x\n", sha1.Sum(deb)))  //nolint:gosec // apt protocol checksum
	assert.Contains(t, idx, fmt.Sprintf("SHA256: %x\n", sha256.Sum256(deb)))
	assert.Equal(t, 1, strings.Count(idx, "Package: "), "one stanza")

	// The stored file is the upload, byte for byte — the head read for parsing
	// is replayed in front of the rest.
	assert.Equal(t, string(deb), e.get(t, path))

	comps, _ := e.comps.Search(t.Context(), domain.SearchParams{Repository: "ctl"})
	require.Len(t, comps.Items, 1)
	assert.Equal(t, "libfoo2", comps.Items[0].Name)
	assert.Equal(t, "2.3.1-1", comps.Items[0].Version)
	assert.Equal(t, "amd64", comps.Items[0].Group, "the control Architecture keys the component")
}

// The same Package/Version for two architectures: each keeps its own control
// paragraph and lands in its own binary-<arch> index; Release lists both.
func TestUpload_PerArchitectureControlIsKeptApart(t *testing.T) {
	e := newCtlEnv(t, "ctl-arch", "")
	amd := debFor(t, "foo", "2.0", "amd64", "Depends: libc6 (>= 2.35)\n", "a")
	arm := buildDeb(t, debOpts{control: ctlFor("foo", "2.0", "arm64", "Depends: libc6 (>= 2.31)\n"), compression: "zst"})
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_2.0_amd64.deb", amd).Code)
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_2.0_arm64.deb", arm).Code)

	amdIdx := e.get(t, "/dists/stable/main/binary-amd64/Packages")
	armIdx := e.get(t, "/dists/stable/main/binary-arm64/Packages")
	assert.Contains(t, amdIdx, "libc6 (>= 2.35)")
	assert.NotContains(t, amdIdx, "libc6 (>= 2.31)")
	assert.Contains(t, armIdx, "libc6 (>= 2.31)")
	assert.NotContains(t, armIdx, "libc6 (>= 2.35)")

	release := e.get(t, "/dists/stable/Release")
	assert.Contains(t, release, "Architectures: amd64 arm64 all\n")
}

// The control file, not the filename, decides the architecture an index and
// the Release file it under.
func TestUpload_ArchitectureComesFromControl(t *testing.T) {
	e := newCtlEnv(t, "ctl-misnamed", "")
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo-data_1.0_amd64.deb", debFor(t, "foo-data", "1.0", "all", "", "d")).Code)

	assert.Contains(t, e.get(t, "/dists/stable/main/binary-all/Packages"), "Package: foo-data\n")
	assert.Contains(t, e.get(t, "/dists/stable/main/binary-arm64/Packages"), "Package: foo-data\n", "arch all is in every index")
	assert.Contains(t, e.get(t, "/dists/stable/Release"), "Architectures: all\n", "no phantom amd64 from the filename")
}

// Two files claiming one Package/Version/Architecture cannot both be indexed;
// the second is refused. Re-sending the same path is the write policy's call.
func TestUpload_SecondFileWithTheSameIdentityIsRefused(t *testing.T) {
	e := newCtlEnv(t, "ctl-dup", "")
	first := debFor(t, "foo", "2.3.1", "amd64", "", "a")
	second := debFor(t, "foo", "2.3.1", "amd64", "", "b")
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_2.3.1-build5_amd64.deb", first).Code)

	w := e.put("/pool/main/foo_2.3.1-build6_amd64.deb", second)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "foo_2.3.1-build5_amd64.deb", "names the file holding the identity")

	assert.Equal(t, http.StatusCreated, e.put("/pool/main/foo_2.3.1-build5_amd64.deb", second).Code,
		"same path, allow policy: a redeploy")
	idx := e.get(t, "/dists/stable/main/binary-amd64/Packages")
	assert.Equal(t, 1, strings.Count(idx, "Package: foo\n"))
	assert.Contains(t, idx, fmt.Sprintf("SHA256: %x\n", sha256.Sum256(second)))
}

// A path keeps the identity it was stored under. A redeploy naming another one
// is refused, and leaves the stored file alone, rather than deleting the
// original before the replacement is known to have been stored.
func TestUpload_RedeployNamingAnotherIdentityIsRefused(t *testing.T) {
	e := newCtlEnv(t, "ctl-move", "")
	path := "/pool/main/foo.deb"
	orig := debFor(t, "foo", "1.0", "amd64", "", "v1")
	require.Equal(t, http.StatusCreated, e.put(path, orig).Code)

	w := e.put(path, debFor(t, "foo", "1.1", "amd64", "", "v2"))
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "delete it before uploading")
	assert.Equal(t, string(orig), e.get(t, path), "the original stays")
	assert.Contains(t, e.get(t, "/dists/stable/main/binary-amd64/Packages"), "Version: 1.0\n")

	// Deleting first frees the path for the new identity.
	require.Equal(t, http.StatusNoContent, e.del(path))
	require.Equal(t, http.StatusCreated, e.put(path, debFor(t, "foo", "1.1", "amd64", "", "v2")).Code)
	idx := e.get(t, "/dists/stable/main/binary-amd64/Packages")
	assert.Contains(t, idx, "Version: 1.1\n")
	assert.NotContains(t, idx, "Version: 1.0\n")
}

// Under write-once a same-identity redeploy is refused by the policy as usual.
func TestUpload_WriteOnceRefusesARedeploy(t *testing.T) {
	e := newCtlEnv(t, "ctl-once", domain.WritePolicyAllowOnce)
	path := "/pool/main/foo_1.0_amd64.deb"
	orig := debFor(t, "foo", "1.0", "amd64", "", "v1")
	require.Equal(t, http.StatusCreated, e.put(path, orig).Code)
	w := e.put(path, debFor(t, "foo", "1.0", "amd64", "", "v1 again"))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "does not allow updating")
	assert.Equal(t, string(orig), e.get(t, path), "the original stays")
}

// A deb stored before control parsing (filename coordinates, no Group) still
// holds its identity, and its path.
func TestUpload_LegacyFileStillHoldsItsIdentity(t *testing.T) {
	e := newCtlEnv(t, "ctl-legacy", "")
	legacy := "/pool/main/foo_1.0_amd64.deb"
	require.Equal(t, http.StatusCreated, e.put(legacy, []byte("legacy body, not an ar archive")).Code)

	w := e.put("/pool/main/foo_1.0-rebuild_amd64.deb", debFor(t, "foo", "1.0", "amd64", "", "x"))
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "foo_1.0_amd64.deb")

	// Another architecture of the same version is a different identity.
	assert.Equal(t, http.StatusCreated, e.put("/pool/main/foo_1.0_arm64.deb", debFor(t, "foo", "1.0", "arm64", "", "y")).Code)

	// The legacy path is upgraded by deleting it and uploading the real deb.
	assert.Equal(t, http.StatusConflict, e.put(legacy, debFor(t, "foo", "1.0", "amd64", "Depends: foo-common\n", "z")).Code)
	require.Equal(t, http.StatusNoContent, e.del(legacy))
	require.Equal(t, http.StatusCreated, e.put(legacy, debFor(t, "foo", "1.0", "amd64", "Depends: foo-common\n", "z")).Code)
	assert.Contains(t, e.get(t, "/dists/stable/main/binary-amd64/Packages"), "Depends: foo-common\n")
}

// Concurrent uploads of one identity to different paths: exactly one wins,
// the rest are refused, and the index lists one stanza.
func TestUpload_ConcurrentClaimsOfOneIdentity(t *testing.T) {
	e := newCtlEnv(t, "ctl-race", "")
	const n = 8
	debs := make([][]byte, n)
	for i := range debs {
		debs[i] = debFor(t, "foo", "3.0", "amd64", "", fmt.Sprint(i))
	}
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = e.put(fmt.Sprintf("/pool/main/foo_3.0-try%d_amd64.deb", i), debs[i]).Code
		}(i)
	}
	wg.Wait()
	created, conflicts := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		}
	}
	assert.Equal(t, 1, created, "codes: %v", codes)
	assert.Equal(t, n-1, conflicts, "codes: %v", codes)
	assert.Equal(t, 1, strings.Count(e.get(t, "/dists/stable/main/binary-amd64/Packages"), "Package: foo\n"))
}

// Whatever puts a second file under one identity (promotion copies bypass the
// upload handler), apt still sees a single stanza: two would be candidates it
// cannot tell apart.
func TestPackagesIndex_OneStanzaPerIdentity(t *testing.T) {
	e := newCtlEnv(t, "ctl-twin", "")
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_1.0_amd64.deb", debFor(t, "foo", "1.0", "amd64", "", "a")).Code)
	comps, _ := e.comps.Search(t.Context(), domain.SearchParams{Repository: "ctl-twin"})
	require.Len(t, comps.Items, 1)
	require.NoError(t, e.assets.Create(t.Context(), &domain.Asset{
		ComponentID: comps.Items[0].ID, Repository: "ctl-twin", Path: "/pool/main/zz-copy.deb", SizeBytes: 1,
	}))

	idx := e.get(t, "/dists/stable/main/binary-amd64/Packages")
	assert.Equal(t, 1, strings.Count(idx, "Package: foo\n"))
	assert.Contains(t, idx, "Filename: /pool/main/foo_1.0_amd64.deb\n", "the first path wins")
}

func TestUpload_BrokenDebIsRefusedAndNothingStored(t *testing.T) {
	e := newCtlEnv(t, "ctl-bad", "")
	bad := buildDeb(t, debOpts{control: "Package: foo\nArchitecture: amd64\n", compression: "gz"})
	w := e.put("/pool/main/foo_1.0_amd64.deb", bad)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Version")
	assert.NotContains(t, e.get(t, "/dists/stable/main/binary-amd64/Packages"), "foo")
}

func TestUpload_OversizedControlIs413(t *testing.T) {
	e := newCtlEnv(t, "ctl-big", "")
	huge := ctlFor("foo", "1.0", "amd64", "X-Pad: "+strings.Repeat("y", maxControlFile)+"\n")
	w := e.put("/pool/main/foo_1.0_amd64.deb", buildDeb(t, debOpts{control: huge, compression: "gz"}))
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestUpload_ControlNotRecordedIs500(t *testing.T) {
	e := newCtlEnv(t, "ctl-500", "")
	e.comps.UpdateExtraErr = errors.New("db down")
	w := e.put("/pool/main/foo_1.0_amd64.deb", debFor(t, "foo", "1.0", "amd64", "", "a"))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// A lookup failure while checking the identity fails the upload closed.
func TestUpload_IdentityLookupFailureIs500(t *testing.T) {
	e := newCtlEnv(t, "ctl-lookup", "")
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_1.0_amd64.deb", debFor(t, "foo", "1.0", "amd64", "", "a")).Code)
	e.assets.Err = errors.New("db down")
	w := e.put("/pool/main/foo_1.0-other_amd64.deb", debFor(t, "foo", "1.0", "amd64", "", "b"))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// A root upload is filed under the control's Package, not the filename's.
func TestUpload_RootUploadUsesControlPackageForThePool(t *testing.T) {
	e := newCtlEnv(t, "ctl-root", "")
	deb := debFor(t, "libfoo", "3.0", "amd64", "", "r")
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "renamed-by-ci.deb")
	require.NoError(t, err)
	_, _ = fw.Write(deb)
	require.NoError(t, mw.Close())
	req := httptest.NewRequest(http.MethodPost, "/repository/ctl-root/", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	idx := e.get(t, "/dists/stable/main/binary-amd64/Packages")
	assert.Contains(t, idx, "Package: libfoo\nVersion: 3.0\n")
	assert.Contains(t, idx, "Filename: /pool/main/libf/libfoo/renamed-by-ci.deb\n")
}

// Rich stanzas (multi-line Description with " ." lines) survive a group merge
// whole: stanzas are split on blank lines, and a control paragraph has none.
func TestGroupMerge_KeepsControlStanzasIntact(t *testing.T) {
	h := New(formats.Deps{})
	a := "Package: libfoo2\nVersion: 1.0\nArchitecture: amd64\nDepends: libfoo-common (= 1.0)\nDescription: lib\n first\n .\n second\nFilename: /pool/main/a.deb\nSize: 1\n"
	b := "Package: libfoo-common\nVersion: 1.0\nArchitecture: all\nDescription: common\n text\nFilename: /pool/main/b.deb\nSize: 1\n"
	body, _, err := h.MergeGroupIndex("g", "/dists/stable/main/binary-amd64/Packages", []formats.GroupIndexPart{
		{Member: "m1", Body: []byte(a + "\n")},
		{Member: "m2", Body: []byte(b + "\n" + a + "\n")},
	})
	require.NoError(t, err)
	out := string(body)
	assert.Equal(t, 1, strings.Count(out, "Package: libfoo2\n"), "deduped by Filename")
	assert.Contains(t, out, a)
	assert.Contains(t, out, b)
}

// A body that is not a deb at all is named by its filename, and still cannot
// take an identity a real deb holds at another path.
func TestUpload_NonDebBodyCannotTakeAHeldIdentity(t *testing.T) {
	e := newCtlEnv(t, "ctl-nondeb", "")
	require.Equal(t, http.StatusCreated, e.put("/pool/main/a/foo_1.0_amd64.deb", debFor(t, "foo", "1.0", "amd64", "", "real")).Code)
	w := e.put("/pool/main/b/foo_1.0_amd64.deb", []byte("not an ar archive"))
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, 1, strings.Count(e.get(t, "/dists/stable/main/binary-amd64/Packages"), "Package: foo\n"))
}

// Lookups of what a path already holds fail the upload closed.
func TestUpload_PathLookupFailuresAre500(t *testing.T) {
	e := newCtlEnv(t, "ctl-path-err", "")
	e.assets.GetByPathErr = errors.New("db down")
	assert.Equal(t, http.StatusInternalServerError, e.put("/pool/main/foo_1.0_amd64.deb", debFor(t, "foo", "1.0", "amd64", "", "a")).Code)

	e = newCtlEnv(t, "ctl-comp-err", "")
	require.Equal(t, http.StatusCreated, e.put("/pool/main/foo_1.0_amd64.deb", debFor(t, "foo", "1.0", "amd64", "", "a")).Code)
	e.comps.GetErr = errors.New("db down")
	assert.Equal(t, http.StatusInternalServerError, e.put("/pool/main/foo_1.0_amd64.deb", debFor(t, "foo", "1.0", "amd64", "", "b")).Code)
}
