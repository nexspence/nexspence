package conda_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/conda"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Minimum package age on a Conda proxy (#643): builds younger than the
// configured age are left out of repodata.json and their files refused.
// Publish times come from each repodata entry's timestamp.

const day = 24 * time.Hour

func msAgo(d time.Duration) int64 { return time.Now().Add(-d).UnixMilli() }

// condaBuild is one repodata entry of the fake upstream; ts 0 omits the
// timestamp, conda selects the packages.conda map.
type condaBuild struct {
	file, name, version string
	ts                  int64
	conda               bool
	url                 string
}

// condaForgeRepodata renders builds in the shape conda.anaconda.org serves:
// info.subdir, both package maps with the full field set, removed, and
// repodata_version.
func condaForgeRepodata(platform string, builds []condaBuild) map[string]any {
	pkgs, pkgsConda := map[string]any{}, map[string]any{}
	for _, b := range builds {
		entry := map[string]any{
			"build": "pyhd8ed1ab_0", "build_number": 0,
			"depends": []any{"python >=3.9"},
			"license": "BSD-3-Clause", "license_family": "BSD",
			"md5":    "0123456789abcdef0123456789abcdef",
			"name":   b.name,
			"noarch": "python",
			"sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"size":   12345, "subdir": platform, "version": b.version,
		}
		if b.ts != 0 {
			entry["timestamp"] = b.ts
		}
		if b.url != "" {
			entry["url"] = b.url
		}
		if b.conda {
			pkgsConda[b.file] = entry
		} else {
			pkgs[b.file] = entry
		}
	}
	return map[string]any{
		"info":             map[string]any{"subdir": platform},
		"packages":         pkgs,
		"packages.conda":   pkgsConda,
		"removed":          []any{"old-1.0-0.tar.bz2"},
		"repodata_version": 1,
	}
}

type condaUpstream struct {
	srv          *httptest.Server
	down         atomic.Bool
	repodataHits atomic.Int32
	builds       []condaBuild
	extra        map[string]bool
}

func newCondaUpstream(t *testing.T, builds []condaBuild) *condaUpstream {
	t.Helper()
	u := &condaUpstream{builds: builds, extra: map[string]bool{}}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "noarch/repodata.json" || p == "noarch/current_repodata.json" {
			u.repodataHits.Add(1)
			if u.down.Load() {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(condaForgeRepodata("noarch", u.builds))
			return
		}
		for _, b := range u.builds {
			if p == "noarch/"+b.file || (b.url != "" && p == "noarch/"+b.url) {
				_, _ = w.Write([]byte("pkg:" + p))
				return
			}
		}
		if u.extra[p] {
			_, _ = w.Write([]byte("pkg:" + p))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

// requestsBuilds: two old builds and a young one (in packages.conda); "fresh"
// only young; "nodates" carries no timestamp at all; "seconds" is dated in
// seconds, as old channels wrote it.
func requestsBuilds() []condaBuild {
	return []condaBuild{
		{file: "requests-2.31.0-pyhd8ed1ab_0.tar.bz2", name: "requests", version: "2.31.0", ts: msAgo(300 * day)},
		{file: "requests-2.32.0-pyhd8ed1ab_0.conda", name: "requests", version: "2.32.0", ts: msAgo(90 * day), conda: true},
		{file: "requests-2.33.0-pyhd8ed1ab_0.conda", name: "requests", version: "2.33.0", ts: msAgo(time.Hour), conda: true},
		{file: "fresh-pkg-0.1.0-pyhd8ed1ab_0.conda", name: "fresh-pkg", version: "0.1.0", ts: msAgo(2 * time.Hour), conda: true},
		{file: "nodates-1.0-py_0.tar.bz2", name: "nodates", version: "1.0"},
		{file: "seconds-1.0-py_0.tar.bz2", name: "seconds", version: "1.0", ts: time.Now().Add(-400 * day).Unix()},
	}
}

type agedConda struct {
	r    *gin.Engine
	repo *domain.Repository
	up   *condaUpstream
}

func newAgedConda(t *testing.T, builds []condaBuild, minAgeSecs int) *agedConda {
	t.Helper()
	up := newCondaUpstream(t, builds)
	repo := &domain.Repository{
		ID: "conda-aged", Name: "conda-aged", Format: "conda", Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": up.srv.URL},
	}
	if minAgeSecs > 0 {
		repo.ProxyConfig["minimum_package_age"] = minAgeSecs
	}
	h := conda.New(formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	})
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return &agedConda{r: r, repo: repo, up: up}
}

func (a *agedConda) get(path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/conda-aged"+path, nil))
	return w
}

// servedFiles returns the filenames of both package maps.
func servedFiles(t *testing.T, body []byte) (pkgs, pkgsConda []string) {
	t.Helper()
	var doc struct {
		Packages      map[string]json.RawMessage `json:"packages"`
		PackagesConda map[string]json.RawMessage `json:"packages.conda"`
		Removed       []string                   `json:"removed"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	require.NotEmpty(t, doc.Removed, "the rest of the document survives")
	for f := range doc.Packages {
		pkgs = append(pkgs, f)
	}
	for f := range doc.PackagesConda {
		pkgsConda = append(pkgsConda, f)
	}
	return pkgs, pkgsConda
}

const weekSecs = 7 * 24 * 3600

func TestConda_AgePolicy_RepodataOmitsYoungBuilds(t *testing.T) {
	a := newAgedConda(t, requestsBuilds(), weekSecs)

	for _, doc := range []string{"/noarch/repodata.json"} {
		w := a.get(doc)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		pkgs, pkgsConda := servedFiles(t, w.Body.Bytes())
		assert.ElementsMatch(t, []string{"requests-2.31.0-pyhd8ed1ab_0.tar.bz2", "nodates-1.0-py_0.tar.bz2", "seconds-1.0-py_0.tar.bz2"}, pkgs)
		assert.ElementsMatch(t, []string{"requests-2.32.0-pyhd8ed1ab_0.conda"}, pkgsConda, "young builds leave packages.conda too")
		assert.Contains(t, w.Body.String(), `"timestamp":`, "kept entries keep their fields")
	}
}

// current_repodata.json holds only each package's newest build; it is
// refused so conda falls back to the filtered repodata.json.
func TestConda_AgePolicy_CurrentRepodataIsRefused(t *testing.T) {
	a := newAgedConda(t, requestsBuilds(), weekSecs)
	w := a.get("/noarch/current_repodata.json")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "repodata.json")
	assert.Equal(t, int32(0), a.up.repodataHits.Load())
}

func TestConda_AgePolicy_YoungFileIsRefused(t *testing.T) {
	a := newAgedConda(t, requestsBuilds(), weekSecs)

	for _, f := range []string{"requests-2.33.0-pyhd8ed1ab_0.conda", "fresh-pkg-0.1.0-pyhd8ed1ab_0.conda"} {
		w := a.get("/noarch/" + f)
		assert.Equal(t, http.StatusForbidden, w.Code, f)
		assert.Contains(t, w.Body.String(), "was published", f)
		assert.Contains(t, w.Body.String(), "minimum package age", f)
	}
	for _, f := range []string{"requests-2.31.0-pyhd8ed1ab_0.tar.bz2", "requests-2.32.0-pyhd8ed1ab_0.conda", "nodates-1.0-py_0.tar.bz2", "seconds-1.0-py_0.tar.bz2"} {
		w := a.get("/noarch/" + f)
		assert.Equal(t, http.StatusOK, w.Code, f+": "+w.Body.String())
	}
	assert.Equal(t, int32(1), a.up.repodataHits.Load(), "one streaming read dates every download")
}

// Serving repodata.json fills the gate's table: downloads that follow need
// no read of their own.
func TestConda_AgePolicy_ServedRepodataFeedsTheGate(t *testing.T) {
	a := newAgedConda(t, requestsBuilds(), weekSecs)
	require.Equal(t, http.StatusOK, a.get("/noarch/repodata.json").Code)
	assert.Equal(t, http.StatusOK, a.get("/noarch/requests-2.32.0-pyhd8ed1ab_0.conda").Code)
	assert.Equal(t, http.StatusForbidden, a.get("/noarch/requests-2.33.0-pyhd8ed1ab_0.conda").Code)
	assert.Equal(t, int32(1), a.up.repodataHits.Load())
}

// A file cached before the policy was switched on is still refused.
func TestConda_AgePolicy_CachedYoungFileIsRefused(t *testing.T) {
	a := newAgedConda(t, requestsBuilds(), 0)
	require.Equal(t, http.StatusOK, a.get("/noarch/requests-2.33.0-pyhd8ed1ab_0.conda").Code)

	a.repo.ProxyConfig["minimum_package_age"] = weekSecs
	w := a.get("/noarch/requests-2.33.0-pyhd8ed1ab_0.conda")
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
}

// An undated build of a dated package has an unknowable age.
func TestConda_AgePolicy_UndatedBuildOfDatedPackageIsRefused(t *testing.T) {
	builds := append(requestsBuilds(), condaBuild{file: "requests-2.30.0-pyhd8ed1ab_0.tar.bz2", name: "requests", version: "2.30.0"})
	a := newAgedConda(t, builds, weekSecs)

	w := a.get("/noarch/requests-2.30.0-pyhd8ed1ab_0.tar.bz2")
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "no timestamp")

	pkgs, _ := servedFiles(t, a.get("/noarch/repodata.json").Body.Bytes())
	assert.NotContains(t, pkgs, "requests-2.30.0-pyhd8ed1ab_0.tar.bz2")
}

// A file repodata.json does not list cannot be dated: refused while the
// document carries dates, served when it carries none.
func TestConda_AgePolicy_UnlistedFile(t *testing.T) {
	a := newAgedConda(t, requestsBuilds(), weekSecs)
	a.up.extra["noarch/ghost-1.0-py_0.conda"] = true
	w := a.get("/noarch/ghost-1.0-py_0.conda")
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "not in the upstream repodata.json")

	b := newAgedConda(t, []condaBuild{{file: "nodates-1.0-py_0.tar.bz2", name: "nodates", version: "1.0"}}, weekSecs)
	b.up.extra["noarch/ghost-1.0-py_0.conda"] = true
	assert.Equal(t, http.StatusOK, b.get("/noarch/ghost-1.0-py_0.conda").Code)
}

// An entry whose url names a nested path is gated under that path.
func TestConda_AgePolicy_NestedURL(t *testing.T) {
	a := newAgedConda(t, []condaBuild{
		{file: "old-1.0-0.conda", name: "nested", version: "1.0", ts: msAgo(100 * day), conda: true, url: "pkgs/old-1.0-0.conda"},
		{file: "new-2.0-0.conda", name: "nested", version: "2.0", ts: msAgo(time.Hour), conda: true, url: "pkgs/new-2.0-0.conda"},
	}, weekSecs)
	assert.Equal(t, http.StatusOK, a.get("/noarch/pkgs/old-1.0-0.conda").Code)
	assert.Equal(t, http.StatusForbidden, a.get("/noarch/pkgs/new-2.0-0.conda").Code)
}

// With upstream down, the last table read still dates a cached package.
func TestConda_AgePolicy_StaleTableWhileUpstreamDown(t *testing.T) {
	a := newAgedConda(t, requestsBuilds(), weekSecs)
	a.repo.ProxyConfig["metadata_max_age"] = 0.001
	require.Equal(t, http.StatusOK, a.get("/noarch/requests-2.32.0-pyhd8ed1ab_0.conda").Code)

	a.up.down.Store(true)
	time.Sleep(5 * time.Millisecond)
	assert.Equal(t, http.StatusOK, a.get("/noarch/requests-2.32.0-pyhd8ed1ab_0.conda").Code)
	assert.Equal(t, http.StatusForbidden, a.get("/noarch/requests-2.33.0-pyhd8ed1ab_0.conda").Code)
}

func TestConda_AgePolicy_UpstreamDownWithoutTableIsBadGateway(t *testing.T) {
	a := newAgedConda(t, requestsBuilds(), weekSecs)
	a.up.down.Store(true)
	w := a.get("/noarch/requests-2.32.0-pyhd8ed1ab_0.conda")
	assert.Equal(t, http.StatusBadGateway, w.Code, w.Body.String())
	// The failure is remembered briefly: the next download does not re-ask.
	w = a.get("/noarch/requests-2.32.0-pyhd8ed1ab_0.conda")
	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.Equal(t, int32(1), a.up.repodataHits.Load())
}

func TestConda_AgePolicy_DisabledByDefault(t *testing.T) {
	a := newAgedConda(t, requestsBuilds(), 0)
	_, pkgsConda := servedFiles(t, a.get("/noarch/repodata.json").Body.Bytes())
	assert.Contains(t, pkgsConda, "requests-2.33.0-pyhd8ed1ab_0.conda")
	assert.Equal(t, http.StatusOK, a.get("/noarch/current_repodata.json").Code)
	assert.Equal(t, http.StatusOK, a.get("/noarch/requests-2.33.0-pyhd8ed1ab_0.conda").Code)
}
