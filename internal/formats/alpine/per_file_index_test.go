package alpine

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // apk protocol checksum
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// One APKINDEX entry per file (#671): the same name and version built for
// another architecture or branch is a different file, and its entry carries
// its own checksum, sizes and dependencies.

func setupWithDeps(repo *domain.Repository) (*gin.Engine, formats.Deps) {
	comps, assets := testutil.NewComponentRepo(), testutil.NewAssetRepo()
	comps.HasAssets = assets.HasComponent
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
	return r, d
}

// buildApk makes an .apk whose .PKGINFO names acme-tool 1.0-r0 with the given
// architecture, installed size and dependency.
func buildApk(t *testing.T, arch string, size int, depend string) []byte {
	t.Helper()
	info := fmt.Sprintf("pkgname = acme-tool\npkgver = 1.0-r0\narch = %s\nsize = %d\npkgdesc = tool for %s\n", arch, size, arch)
	if depend != "" {
		info += "depend = " + depend + "\n"
	}
	control := tarWith(t, ".PKGINFO", []byte(info))
	data := tarWith(t, "usr/bin/acme-tool", []byte("binary for "+arch))
	return append(gzipMember(control), gzipMember(data)...)
}

func apkChecksum(t *testing.T, apk []byte) string {
	t.Helper()
	control, _, err := controlSegment(apk)
	require.NoError(t, err)
	sum := sha1.Sum(control) //nolint:gosec // apk protocol checksum
	return "Q1" + base64.StdEncoding.EncodeToString(sum[:])
}

func apkIndex(t *testing.T, r *gin.Engine, repoName, dir string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/repository/"+repoName+"/"+dir+"/APKINDEX.tar.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	plain, err := unpackIndexTarGz(w.Body.Bytes())
	require.NoError(t, err)
	return plain
}

func TestAlpine_Index_EachArchitectureKeepsItsOwnEntry(t *testing.T) {
	r, _ := setupWithDeps(testutil.SimpleRepo("apks-multi", "alpine"))
	x86 := buildApk(t, "x86_64", 1000, "so:libc.musl-x86_64.so.1")
	arm := buildApk(t, "aarch64", 2000, "so:libc.musl-aarch64.so.1")
	require.Equal(t, http.StatusCreated, putApk(r, "apks-multi", "/x86_64/acme-tool-1.0-r0.apk", x86))
	require.Equal(t, http.StatusCreated, putApk(r, "apks-multi", "/aarch64/acme-tool-1.0-r0.apk", arm))

	x86Index := apkIndex(t, r, "apks-multi", "x86_64")
	assert.Contains(t, x86Index, "C:"+apkChecksum(t, x86)+"\n")
	assert.Contains(t, x86Index, "I:1000\n")
	assert.Contains(t, x86Index, "D:so:libc.musl-x86_64.so.1\n")
	assert.Contains(t, x86Index, "T:tool for x86_64\n")

	armIndex := apkIndex(t, r, "apks-multi", "aarch64")
	assert.Contains(t, armIndex, "C:"+apkChecksum(t, arm)+"\n")
	assert.Contains(t, armIndex, "I:2000\n")
	assert.Contains(t, armIndex, "D:so:libc.musl-aarch64.so.1\n")
}

func TestAlpine_Index_EachBranchKeepsItsOwnEntry(t *testing.T) {
	r, _ := setupWithDeps(testutil.SimpleRepo("apks-branch", "alpine"))
	old := buildApk(t, "x86_64", 1000, "so:libssl.so.1.1")
	cur := buildApk(t, "x86_64", 1100, "so:libssl.so.3")
	require.Equal(t, http.StatusCreated, putApk(r, "apks-branch", "/v3.14/main/x86_64/acme-tool-1.0-r0.apk", old))
	require.Equal(t, http.StatusCreated, putApk(r, "apks-branch", "/v3.20/main/x86_64/acme-tool-1.0-r0.apk", cur))

	assert.Contains(t, apkIndex(t, r, "apks-branch", "v3.14/main/x86_64"), "D:so:libssl.so.1.1\n")
	assert.Contains(t, apkIndex(t, r, "apks-branch", "v3.20/main/x86_64"), "D:so:libssl.so.3\n")
}

// seedLegacy stores files the way the handler did before #671: one component
// per name and version, no group, holding whichever upload came last's
// metadata — here the aarch64 build's.
func seedLegacy(t *testing.T, d formats.Deps, repoName string, files map[string][]byte, shared []byte) string {
	t.Helper()
	ctx := context.Background()
	var compID string
	for p, apk := range files {
		res, err := base.StoreArtifact(ctx, d, repoName, p, "application/x-alpine-apk",
			base.Coords{Name: "acme-tool", Version: "1.0-r0"}, bytes.NewReader(apk), int64(len(apk)))
		require.NoError(t, err)
		compID = res.Asset.ComponentID
	}
	require.NoError(t, d.Components.UpdateExtra(ctx, compID, map[string]any{"checksum": apkChecksum(t, shared)}))
	return compID
}

func componentGone(t *testing.T, d formats.Deps, id string) bool {
	t.Helper()
	got, err := d.Components.Get(context.Background(), id)
	return err != nil || got == nil
}

// Legacy entries are repaired when the index is read: no re-upload needed,
// which a write-once repository would refuse anyway.
func TestAlpine_Index_RepairsLegacySharedComponent(t *testing.T) {
	repo := testutil.SimpleRepo("apks-legacy", "alpine")
	repo.FormatConfig = map[string]any{"write_policy": "allow_once"}
	r, d := setupWithDeps(repo)
	x86 := buildApk(t, "x86_64", 1000, "so:libc.musl-x86_64.so.1")
	arm := buildApk(t, "aarch64", 2000, "so:libc.musl-aarch64.so.1")
	legacy := seedLegacy(t, d, "apks-legacy", map[string][]byte{
		"/x86_64/acme-tool-1.0-r0.apk":  x86,
		"/aarch64/acme-tool-1.0-r0.apk": arm,
	}, arm)

	x86Index := apkIndex(t, r, "apks-legacy", "x86_64")
	assert.Contains(t, x86Index, "C:"+apkChecksum(t, x86)+"\n")
	assert.Contains(t, x86Index, "D:so:libc.musl-x86_64.so.1\n")
	moved, err := d.Assets.GetByPath(context.Background(), "apks-legacy", "/x86_64/acme-tool-1.0-r0.apk")
	require.NoError(t, err)
	assert.NotEqual(t, legacy, moved.ComponentID, "the repaired file has its own component")
	assert.False(t, componentGone(t, d, legacy), "the shared component still holds the aarch64 file")

	assert.Contains(t, apkIndex(t, r, "apks-legacy", "aarch64"), "C:"+apkChecksum(t, arm)+"\n")
	assert.True(t, componentGone(t, d, legacy), "an emptied shared component is removed")

	// Once moved, the entry is served from the file's own component.
	assert.Contains(t, apkIndex(t, r, "apks-legacy", "x86_64"), "C:"+apkChecksum(t, x86)+"\n")
}

// Re-uploading a legacy path moves it as well.
func TestAlpine_Upload_MovesLegacySharedComponentAsset(t *testing.T) {
	r, d := setupWithDeps(testutil.SimpleRepo("apks-legacy", "alpine"))
	x86 := buildApk(t, "x86_64", 1000, "")
	arm := buildApk(t, "aarch64", 2000, "")
	legacy := seedLegacy(t, d, "apks-legacy", map[string][]byte{
		"/x86_64/acme-tool-1.0-r0.apk":  x86,
		"/aarch64/acme-tool-1.0-r0.apk": arm,
	}, arm)

	require.Equal(t, http.StatusCreated, putApk(r, "apks-legacy", "/x86_64/acme-tool-1.0-r0.apk", x86))
	moved, err := d.Assets.GetByPath(context.Background(), "apks-legacy", "/x86_64/acme-tool-1.0-r0.apk")
	require.NoError(t, err)
	assert.NotEqual(t, legacy, moved.ComponentID)
	comp, err := d.Components.Get(context.Background(), moved.ComponentID)
	require.NoError(t, err)
	assert.Equal(t, "x86_64", comp.Group)
	assert.Equal(t, apkChecksum(t, x86), comp.Extra["checksum"])

	require.Equal(t, http.StatusCreated, putApk(r, "apks-legacy", "/aarch64/acme-tool-1.0-r0.apk", arm))
	assert.True(t, componentGone(t, d, legacy))
}

// Delete and re-upload a rebuild without dependencies: the old D: line must
// not survive into the new entry.
func TestAlpine_Upload_RebuildAfterDeleteDropsOldMetadata(t *testing.T) {
	r, _ := setupWithDeps(testutil.SimpleRepo("apks-rebuild", "alpine"))
	require.Equal(t, http.StatusCreated,
		putApk(r, "apks-rebuild", "/x86_64/acme-tool-1.0-r0.apk", buildApk(t, "x86_64", 1000, "so:libssl.so.1.1")))
	req := httptest.NewRequest(http.MethodDelete, "/repository/apks-rebuild/x86_64/acme-tool-1.0-r0.apk", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)

	require.Equal(t, http.StatusCreated,
		putApk(r, "apks-rebuild", "/x86_64/acme-tool-1.0-r0.apk", buildApk(t, "x86_64", 1200, "")))
	index := apkIndex(t, r, "apks-rebuild", "x86_64")
	assert.Contains(t, index, "I:1200\n")
	assert.False(t, strings.Contains(index, "\nD:"), "no dependency line from the deleted build:\n%s", index)
}

// A legacy file whose bytes cannot be read or parsed keeps the entry its
// component describes, rather than drop out of the index.
func TestAlpine_Index_UnrepairableLegacyKeepsComponentEntry(t *testing.T) {
	ctx := context.Background()
	r, d := setupWithDeps(testutil.SimpleRepo("apks-unrep", "alpine"))
	arm := buildApk(t, "aarch64", 2000, "")
	seedLegacy(t, d, "apks-unrep", map[string][]byte{
		"/x86_64/acme-tool-1.0-r0.apk":  []byte("not an apk at all"),
		"/aarch64/acme-tool-1.0-r0.apk": arm,
	}, arm)
	assert.Contains(t, apkIndex(t, r, "apks-unrep", "x86_64"), "C:"+apkChecksum(t, arm)+"\n",
		"unparsable bytes: the component's entry stays")

	gone, err := d.Assets.GetByPath(ctx, "apks-unrep", "/aarch64/acme-tool-1.0-r0.apk")
	require.NoError(t, err)
	require.NoError(t, d.BlobStore.Delete(ctx, gone.BlobKey))
	assert.Contains(t, apkIndex(t, r, "apks-unrep", "aarch64"), "C:"+apkChecksum(t, arm)+"\n",
		"unreadable blob: the component's entry stays")
}

// A repair whose move fails still serves the file's own metadata.
func TestAlpine_Index_RepairServesOwnMetadataWhenMoveFails(t *testing.T) {
	r, d := setupWithDeps(testutil.SimpleRepo("apks-movefail", "alpine"))
	x86 := buildApk(t, "x86_64", 1000, "")
	arm := buildApk(t, "aarch64", 2000, "")
	seedLegacy(t, d, "apks-movefail", map[string][]byte{
		"/x86_64/acme-tool-1.0-r0.apk":  x86,
		"/aarch64/acme-tool-1.0-r0.apk": arm,
	}, arm)
	d.Components.(*testutil.ComponentRepo).UpdateExtraErr = assert.AnError
	assert.Contains(t, apkIndex(t, r, "apks-movefail", "x86_64"), "C:"+apkChecksum(t, x86)+"\n")
}

func TestAlpine_Upload_RecordFailureIs500(t *testing.T) {
	r, d := setupWithDeps(testutil.SimpleRepo("apks-fail", "alpine"))
	d.Components.(*testutil.ComponentRepo).UpdateExtraErr = assert.AnError
	assert.Equal(t, http.StatusInternalServerError,
		putApk(r, "apks-fail", "/x86_64/acme-tool-1.0-r0.apk", buildApk(t, "x86_64", 1000, "")))
}

// PostgreSQL hands JSONB arrays and numbers back as []any and float64.
func TestAlpine_ExtraDecodingFromJSONB(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, extraStringSlice([]any{"a", 1, "b"}))
	assert.Equal(t, []string{"x"}, extraStringSlice([]string{"x"}))
	assert.Nil(t, extraStringSlice("not a list"))
	for _, v := range []any{int64(7), 7, float64(7)} {
		n, ok := extraInt64(v)
		assert.True(t, ok)
		assert.Equal(t, int64(7), n)
	}
	_, ok := extraInt64("7")
	assert.False(t, ok)
}

// namedApk is buildApk for another package name.
func namedApk(t *testing.T, name string) []byte {
	t.Helper()
	control := tarWith(t, ".PKGINFO", []byte("pkgname = "+name+"\npkgver = 1.0-r0\narch = x86_64\n"))
	return append(gzipMember(control), gzipMember(tarWith(t, "usr/bin/"+name, []byte(name)))...)
}

type countingDownloads struct{ n int }

func (c *countingDownloads) Add(string) { c.n++ }

// Repairing reads files back from storage; that is not a download, and must
// not keep them from download-age cleanup.
func TestAlpine_Index_RepairIsNotADownload(t *testing.T) {
	_, d := setupWithDeps(testutil.SimpleRepo("apks-dl", "alpine"))
	counter := &countingDownloads{}
	d.Downloads = counter
	h := New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	x86 := buildApk(t, "x86_64", 1000, "")
	seedLegacy(t, d, "apks-dl", map[string][]byte{"/x86_64/acme-tool-1.0-r0.apk": x86}, buildApk(t, "aarch64", 2000, ""))
	assert.Contains(t, apkIndex(t, r, "apks-dl", "x86_64"), "C:"+apkChecksum(t, x86)+"\n")
	assert.Zero(t, counter.n)
}

// A large legacy directory is repaired over several requests.
func TestAlpine_Index_LegacyRepairIsBoundedPerRequest(t *testing.T) {
	r, d := setupWithDeps(testutil.SimpleRepo("apks-many", "alpine"))
	files := map[string][]byte{}
	for i := 0; i < 40; i++ {
		name := fmt.Sprintf("pkg%02d", i)
		files["/x86_64/"+name+"-1.0-r0.apk"] = namedApk(t, name)
	}
	legacy := seedLegacy(t, d, "apks-many", files, buildApk(t, "aarch64", 1, ""))

	assert.Equal(t, 40, strings.Count(apkIndex(t, r, "apks-many", "x86_64"), "C:"))
	assert.False(t, componentGone(t, d, legacy), "not every file was repaired by the first request")
	assert.Equal(t, 40, strings.Count(apkIndex(t, r, "apks-many", "x86_64"), "C:"))
	assert.True(t, componentGone(t, d, legacy), "the second request finished the repair")
}

// Files whose blobs are gone are recorded once, so they do not take the
// repair slots of the files after them on every request.
func TestAlpine_Index_UnreadableLegacyDoesNotStarveOthers(t *testing.T) {
	ctx := context.Background()
	r, d := setupWithDeps(testutil.SimpleRepo("apks-gone", "alpine"))
	files := map[string][]byte{}
	for i := 0; i < 33; i++ {
		name := fmt.Sprintf("aaa%02d", i)
		files["/x86_64/"+name+"-1.0-r0.apk"] = namedApk(t, name)
	}
	good := buildApk(t, "x86_64", 1000, "")
	files["/x86_64/zzz-acme-tool-1.0-r0.apk"] = good
	seedLegacy(t, d, "apks-gone", files, buildApk(t, "aarch64", 2000, ""))
	for i := 0; i < 33; i++ {
		a, err := d.Assets.GetByPath(ctx, "apks-gone", fmt.Sprintf("/x86_64/aaa%02d-1.0-r0.apk", i))
		require.NoError(t, err)
		require.NoError(t, d.BlobStore.Delete(ctx, a.BlobKey))
	}
	apkIndex(t, r, "apks-gone", "x86_64")
	assert.Contains(t, apkIndex(t, r, "apks-gone", "x86_64"), "C:"+apkChecksum(t, good)+"\n",
		"the readable file is reached by the second request")
}
