package conda_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/conda"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// One repodata.json entry per file, each with its own package's
// info/index.json (#677).

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
	h := conda.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, d
}

// condaPackage builds a .conda: a zip whose info-<stem>.tar.zst member holds
// info/index.json (and, before it, another info file a reader must skip).
func condaPackage(t *testing.T, stem string, index map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(index)
	require.NoError(t, err)
	var tb bytes.Buffer
	tw := tar.NewWriter(&tb)
	for name, body := range map[string][]byte{"info/about.json": []byte(`{}`)} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}))
		_, err = tw.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "info/index.json", Mode: 0o644, Size: int64(len(raw))}))
	_, err = tw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	var zb bytes.Buffer
	enc, err := zstd.NewWriter(&zb)
	require.NoError(t, err)
	_, err = enc.Write(tb.Bytes())
	require.NoError(t, err)
	require.NoError(t, enc.Close())

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for name, body := range map[string][]byte{
		"metadata.json":             []byte(`{"conda_pkg_format_version": 2}`),
		"pkg-" + stem + ".tar.zst":  []byte("payload"),
		"info-" + stem + ".tar.zst": zb.Bytes(),
	} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		require.NoError(t, err)
		_, err = w.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return out.Bytes()
}

func numpyIndex(build, py string, buildNumber int) map[string]any {
	return map[string]any{
		"name": "numpy", "version": "1.26.0", "build": build, "build_number": buildNumber,
		"subdir": "linux-64", "depends": []string{"python >=" + py + ",<" + py + ".0a0", "libopenblas"},
		"constrains": []string{"numpy-base <0a0"}, "license": "BSD-3-Clause", "timestamp": 1700000000000,
	}
}

func putPkg(t *testing.T, r *gin.Engine, repoName, p string, body []byte) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+p, bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

type repodata struct {
	Packages      map[string]map[string]any `json:"packages"`
	PackagesConda map[string]map[string]any `json:"packages.conda"`
}

func getRepodata(t *testing.T, r *gin.Engine, repoName, subdir string) repodata {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/repository/"+repoName+"/"+subdir+"/repodata.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var doc repodata
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	return doc
}

func TestConda_Repodata_ListsEveryBuildWithItsOwnMetadata(t *testing.T) {
	r, _ := setupWithDeps(hostedRepo("conda-builds"))
	py310 := condaPackage(t, "numpy-1.26.0-py310h_0", numpyIndex("py310h_0", "3.10", 0))
	py311 := condaPackage(t, "numpy-1.26.0-py311h_1", numpyIndex("py311h_1", "3.11", 1))
	require.Equal(t, http.StatusCreated, putPkg(t, r, "conda-builds", "/linux-64/numpy-1.26.0-py310h_0.conda", py310))
	require.Equal(t, http.StatusCreated, putPkg(t, r, "conda-builds", "/linux-64/numpy-1.26.0-py311h_1.conda", py311))
	// The .tar.bz2 of a build shares its component with the .conda.
	bz2 := mustDecodeHex(realTarBz2Hex) // numpy 1.24.0 py311_0
	require.Equal(t, http.StatusCreated, putPkg(t, r, "conda-builds", "/linux-64/numpy-1.24.0-py311_0.tar.bz2", bz2))
	require.Equal(t, http.StatusCreated, putPkg(t, r, "conda-builds", "/linux-64/numpy-1.24.0-py311_0.conda",
		condaPackage(t, "numpy-1.24.0-py311_0", map[string]any{"name": "numpy", "version": "1.24.0", "build": "py311_0",
			"build_number": 0, "subdir": "linux-64", "depends": []string{"python >=3.11"}})))

	doc := getRepodata(t, r, "conda-builds", "linux-64")
	require.Len(t, doc.PackagesConda, 3)
	require.Len(t, doc.Packages, 1)

	e310 := doc.PackagesConda["numpy-1.26.0-py310h_0.conda"]
	assert.Equal(t, "py310h_0", e310["build"])
	assert.Equal(t, float64(0), e310["build_number"])
	assert.Contains(t, e310["depends"], "python >=3.10,<3.10.0a0")
	assert.Equal(t, []any{"numpy-base <0a0"}, e310["constrains"], "fields of info/index.json pass through")
	assert.Equal(t, "BSD-3-Clause", e310["license"])
	assert.Equal(t, float64(1700000000000), e310["timestamp"])
	assert.Len(t, e310["sha256"], 64)
	assert.Equal(t, float64(len(py310)), e310["size"])

	e311 := doc.PackagesConda["numpy-1.26.0-py311h_1.conda"]
	assert.Equal(t, "py311h_1", e311["build"])
	assert.Equal(t, float64(1), e311["build_number"])
	assert.Contains(t, e311["depends"], "python >=3.11,<3.11.0a0")

	assert.Equal(t, "py311_0", doc.Packages["numpy-1.24.0-py311_0.tar.bz2"]["build"])
	assert.NotEqual(t, doc.Packages["numpy-1.24.0-py311_0.tar.bz2"]["sha256"],
		doc.PackagesConda["numpy-1.24.0-py311_0.conda"]["sha256"], "each file carries its own checksum")
}

func TestConda_Repodata_NoarchFieldPassesThrough(t *testing.T) {
	r, _ := setupWithDeps(hostedRepo("conda-noarch"))
	idx := map[string]any{"name": "acme-tool", "version": "2.0", "build": "pyhd8ed1ab_0", "build_number": 0,
		"subdir": "noarch", "noarch": "python", "depends": []string{"python >=3.8"}}
	require.Equal(t, http.StatusCreated, putPkg(t, r, "conda-noarch", "/noarch/acme-tool-2.0-pyhd8ed1ab_0.conda",
		condaPackage(t, "acme-tool-2.0-pyhd8ed1ab_0", idx)))
	e := getRepodata(t, r, "conda-noarch", "noarch").PackagesConda["acme-tool-2.0-pyhd8ed1ab_0.conda"]
	assert.Equal(t, "python", e["noarch"])
	assert.Equal(t, "acme-tool", e["name"])
}

func TestParseMeta_CondaReadsInfoIndex(t *testing.T) {
	pkg := condaPackage(t, "numpy-1.26.0-py311h_1", numpyIndex("py311h_1", "3.11", 1))
	meta, err := conda.ParseMeta("numpy-1.26.0-py311h_1.conda", bytes.NewReader(pkg), int64(len(pkg)))
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, "numpy", meta.Name)
	assert.Equal(t, "py311h_1", meta.Build)
	assert.Equal(t, 1, meta.BuildNumber)
	assert.Contains(t, meta.Depends, "libopenblas")
	assert.Equal(t, "BSD-3-Clause", meta.Index["license"])
}

func TestParseMeta_CondaWithoutInfoMemberFallsBack(t *testing.T) {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	w, err := zw.Create("metadata.json")
	require.NoError(t, err)
	_, _ = w.Write([]byte("{}"))
	require.NoError(t, zw.Close())
	meta, err := conda.ParseMeta("x-1.0-0.conda", bytes.NewReader(out.Bytes()), int64(out.Len()))
	assert.NoError(t, err)
	assert.Nil(t, meta)
}

func TestConda_Upload_FilenameSplitsFromTheRight(t *testing.T) {
	r, _ := setupWithDeps(hostedRepo("conda-names"))
	for _, f := range []string{"scikit-learn-1.3.0-py311_0.conda", "python-dateutil-2.8.2-pyhd8ed1ab_0.tar.bz2"} {
		require.Equal(t, http.StatusCreated, putPkg(t, r, "conda-names", "/linux-64/"+f, []byte("not a package")))
	}
	doc := getRepodata(t, r, "conda-names", "linux-64")
	sk := doc.PackagesConda["scikit-learn-1.3.0-py311_0.conda"]
	assert.Equal(t, "scikit-learn", sk["name"])
	assert.Equal(t, "1.3.0", sk["version"])
	du := doc.Packages["python-dateutil-2.8.2-pyhd8ed1ab_0.tar.bz2"]
	assert.Equal(t, "python-dateutil", du["name"])
	assert.Equal(t, "pyhd8ed1ab_0", du["build"])
}

func TestConda_Upload_RecordFailureIs500(t *testing.T) {
	r, d := setupWithDeps(hostedRepo("conda-fail"))
	d.Components.(*testutil.ComponentRepo).UpdateExtraErr = assert.AnError
	assert.Equal(t, http.StatusInternalServerError, putPkg(t, r, "conda-fail", "/linux-64/numpy-1.26.0-py310h_0.conda",
		condaPackage(t, "numpy-1.26.0-py310h_0", numpyIndex("py310h_0", "3.10", 0))))
}

// seedLegacy stores files the way the handler did before #677: one component
// per subdir, name and version, with the last upload's build/depends on it.
func seedLegacy(t *testing.T, d formats.Deps, repoName string, files map[string][]byte, lastBuild string, lastDepends []string) string {
	t.Helper()
	ctx := context.Background()
	var compID string
	for p, body := range files {
		res, err := base.StoreArtifact(ctx, d, repoName, p, "application/zip",
			base.Coords{Group: "linux-64", Name: "numpy", Version: "1.26.0"}, bytes.NewReader(body), int64(len(body)))
		require.NoError(t, err)
		compID = res.Asset.ComponentID
	}
	// As PostgreSQL hands JSONB back: numbers as float64, lists as []any.
	deps := make([]any, 0, len(lastDepends))
	for _, dep := range lastDepends {
		deps = append(deps, dep)
	}
	require.NoError(t, d.Components.UpdateExtra(ctx, compID,
		map[string]any{"build": lastBuild, "build_number": float64(1), "depends": deps}))
	return compID
}

func TestConda_Repodata_RepairsLegacySharedComponent(t *testing.T) {
	ctx := context.Background()
	r, d := setupWithDeps(hostedRepo("conda-legacy"))
	legacy := seedLegacy(t, d, "conda-legacy", map[string][]byte{
		"/linux-64/numpy-1.26.0-py310h_0.conda": condaPackage(t, "numpy-1.26.0-py310h_0", numpyIndex("py310h_0", "3.10", 0)),
		"/linux-64/numpy-1.26.0-py311h_1.conda": condaPackage(t, "numpy-1.26.0-py311h_1", numpyIndex("py311h_1", "3.11", 1)),
	}, "py311h_1", []string{"python >=3.11,<3.11.0a0"})

	doc := getRepodata(t, r, "conda-legacy", "linux-64")
	require.Len(t, doc.PackagesConda, 2, "both files are listed, not one per component")
	e310 := doc.PackagesConda["numpy-1.26.0-py310h_0.conda"]
	assert.Equal(t, "py310h_0", e310["build"])
	assert.Contains(t, e310["depends"], "python >=3.10,<3.10.0a0", "read from the file, not the shared component")

	moved, err := d.Assets.GetByPath(ctx, "conda-legacy", "/linux-64/numpy-1.26.0-py310h_0.conda")
	require.NoError(t, err)
	comp, err := d.Components.Get(ctx, moved.ComponentID)
	require.NoError(t, err)
	assert.Equal(t, "1.26.0-py310h_0", comp.Version)
	got, _ := d.Components.Get(ctx, legacy)
	assert.Nil(t, got, "the emptied shared component is removed")
}

// A legacy file that cannot be read is described by its name; the old
// component's dependencies are kept only for the build they were recorded for.
func TestConda_Repodata_UnreadableLegacyFallsBackToFilename(t *testing.T) {
	r, d := setupWithDeps(hostedRepo("conda-legacy-bad"))
	seedLegacy(t, d, "conda-legacy-bad", map[string][]byte{
		"/linux-64/numpy-1.26.0-py310h_0.conda": []byte("garbage"),
		"/linux-64/numpy-1.26.0-py311h_1.conda": []byte("garbage too"),
	}, "py311h_1", []string{"python >=3.11,<3.11.0a0"})

	doc := getRepodata(t, r, "conda-legacy-bad", "linux-64")
	e310 := doc.PackagesConda["numpy-1.26.0-py310h_0.conda"]
	assert.Equal(t, "py310h_0", e310["build"])
	assert.NotContains(t, e310["depends"], "python >=3.11,<3.11.0a0", "another build's dependencies are not borrowed")
	e311 := doc.PackagesConda["numpy-1.26.0-py311h_1.conda"]
	assert.Equal(t, "py311h_1", e311["build"])
	assert.Contains(t, e311["depends"], "python >=3.11,<3.11.0a0")
	assert.Equal(t, float64(1), e311["build_number"])
}

// A large legacy channel is repaired over several requests; every file is
// listed on each of them.
func TestConda_Repodata_LegacyRepairIsBoundedPerRequest(t *testing.T) {
	ctx := context.Background()
	r, d := setupWithDeps(hostedRepo("conda-legacy-many"))
	files := map[string][]byte{}
	for i := 0; i < 40; i++ {
		stem := fmt.Sprintf("numpy-1.26.0-b%02d_0", i)
		files["/linux-64/"+stem+".conda"] = condaPackage(t, stem,
			map[string]any{"name": "numpy", "version": "1.26.0", "build": fmt.Sprintf("b%02d_0", i), "subdir": "linux-64"})
	}
	legacy := seedLegacy(t, d, "conda-legacy-many", files, "b39_0", nil)

	assert.Len(t, getRepodata(t, r, "conda-legacy-many", "linux-64").PackagesConda, 40)
	got, _ := d.Components.Get(ctx, legacy)
	assert.NotNil(t, got, "not every file was repaired by the first request")
	assert.Len(t, getRepodata(t, r, "conda-legacy-many", "linux-64").PackagesConda, 40)
	got, _ = d.Components.Get(ctx, legacy)
	assert.Nil(t, got, "the second request finished the repair")
}

type countingDownloads struct{ n int }

func (c *countingDownloads) Add(string) { c.n++ }

// Repairing reads files back from storage; that is not a download, and must
// not keep them from download-age cleanup.
func TestConda_Repodata_RepairIsNotADownload(t *testing.T) {
	_, d := setupWithDeps(hostedRepo("conda-legacy-dl"))
	counter := &countingDownloads{}
	d.Downloads = counter
	h := conda.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	seedLegacy(t, d, "conda-legacy-dl", map[string][]byte{
		"/linux-64/numpy-1.26.0-py310h_0.conda": condaPackage(t, "numpy-1.26.0-py310h_0", numpyIndex("py310h_0", "3.10", 0)),
	}, "py310h_0", nil)
	assert.Equal(t, "py310h_0", getRepodata(t, r, "conda-legacy-dl", "linux-64").PackagesConda["numpy-1.26.0-py310h_0.conda"]["build"])
	assert.Zero(t, counter.n)
}

// A legacy file whose blob is gone is recorded from its name once, so it does
// not take a repair slot from the files after it on every request.
func TestConda_Repodata_UnfetchableLegacyIsRecordedOnce(t *testing.T) {
	ctx := context.Background()
	r, d := setupWithDeps(hostedRepo("conda-legacy-gone"))
	files := map[string][]byte{}
	for i := 0; i < 33; i++ {
		stem := fmt.Sprintf("aaa-1.0-b%02d_0", i)
		files["/linux-64/"+stem+".conda"] = []byte("x")
	}
	files["/linux-64/numpy-1.26.0-py310h_0.conda"] = condaPackage(t, "numpy-1.26.0-py310h_0", numpyIndex("py310h_0", "3.10", 0))
	seedLegacy(t, d, "conda-legacy-gone", files, "zzz", nil)
	for i := 0; i < 33; i++ {
		a, err := d.Assets.GetByPath(ctx, "conda-legacy-gone", fmt.Sprintf("/linux-64/aaa-1.0-b%02d_0.conda", i))
		require.NoError(t, err)
		require.NoError(t, d.BlobStore.Delete(ctx, a.BlobKey))
	}

	first := getRepodata(t, r, "conda-legacy-gone", "linux-64").PackagesConda
	assert.Len(t, first, 34)
	assert.Equal(t, "b00_0", first["aaa-1.0-b00_0.conda"]["build"])
	second := getRepodata(t, r, "conda-legacy-gone", "linux-64").PackagesConda
	assert.Contains(t, second["numpy-1.26.0-py310h_0.conda"]["depends"], "python >=3.10,<3.10.0a0",
		"the readable file is reached by the second request")
}

// A zip that claims more members than a .conda has is not read.
func TestParseMeta_CondaZipDirectoryIsBounded(t *testing.T) {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for i := 0; i < 100; i++ {
		_, err := zw.Create(fmt.Sprintf("f%03d", i))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	_, err := conda.ParseMeta("x-1.0-0.conda", bytes.NewReader(out.Bytes()), int64(out.Len()))
	assert.ErrorContains(t, err, "central directory")
}
