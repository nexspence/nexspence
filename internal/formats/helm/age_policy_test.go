package helm_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/helm"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Minimum package age on a Helm proxy (#643): chart versions younger than the
// configured age are left out of index.yaml and their archives are refused.
// Publish times come from each index entry's `created`.

const day = 24 * time.Hour

func ago(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339Nano) }

// chartVersion is one index.yaml entry of the fake upstream; created "" omits
// the field.
type chartVersion struct{ name, version, created string }

// bitnamiIndex renders entries in the shape charts.bitnami.com serves:
// annotations with a block scalar, quoted created, digest, dependencies and
// absolute URLs under the repository.
func bitnamiIndex(base string, charts []chartVersion) string {
	var b strings.Builder
	b.WriteString("apiVersion: v1\nentries:\n")
	last := ""
	for _, cv := range charts {
		if cv.name != last {
			b.WriteString("  " + cv.name + ":\n")
			last = cv.name
		}
		b.WriteString("  - annotations:\n" +
			"      category: Database\n" +
			"      images: |\n" +
			"        - name: " + cv.name + "\n" +
			"          image: docker.io/bitnami/" + cv.name + ":" + cv.version + "-debian-12-r0\n" +
			"      licenses: Apache-2.0\n" +
			"    apiVersion: v2\n" +
			"    appVersion: " + cv.version + "\n")
		if cv.created != "" {
			b.WriteString("    created: \"" + cv.created + "\"\n")
		}
		b.WriteString("    dependencies:\n" +
			"    - name: common\n" +
			"      repository: oci://registry-1.docker.io/bitnamicharts\n" +
			"      tags:\n" +
			"      - bitnami-common\n" +
			"      version: 2.x.x\n" +
			"    description: " + cv.name + " chart\n" +
			"    digest: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n" +
			"    home: https://bitnami.com\n" +
			"    name: " + cv.name + "\n" +
			"    urls:\n" +
			"    - " + base + "/" + cv.name + "-" + cv.version + ".tgz\n" +
			"    version: " + cv.version + "\n")
	}
	b.WriteString("generated: \"2025-08-21T03:21:02.123456789Z\"\n")
	return b.String()
}

// helmUpstream serves index.yaml and every chart archive it lists (plus its
// .prov). down makes index.yaml answer 500.
type helmUpstream struct {
	srv        *httptest.Server
	down       atomic.Bool
	indexHits  atomic.Int32
	charts     []chartVersion
	extraFiles map[string]bool
}

func newHelmUpstream(t *testing.T, charts []chartVersion) *helmUpstream {
	t.Helper()
	u := &helmUpstream{charts: charts, extraFiles: map[string]bool{}}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.yaml" {
			u.indexHits.Add(1)
			if u.down.Load() {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/x-yaml")
			_, _ = w.Write([]byte(bitnamiIndex(u.srv.URL, u.charts)))
			return
		}
		file := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".prov")
		for _, cv := range u.charts {
			if file == cv.name+"-"+cv.version+".tgz" {
				_, _ = w.Write([]byte("chart:" + r.URL.Path))
				return
			}
		}
		if u.extraFiles[file] {
			_, _ = w.Write([]byte("chart:" + r.URL.Path))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

// redisCharts: an old release, a young release, an old one listed after it;
// "fresh" has only a young version; "nodates" carries no created at all.
func redisCharts() []chartVersion {
	return []chartVersion{
		{"redis", "20.1.0", ago(time.Hour)},
		{"redis", "20.0.0", ago(90 * day)},
		{"redis", "19.6.4", ago(200 * day)},
		{"fresh", "0.1.0", ago(2 * time.Hour)},
		{"nodates", "1.0.0", ""},
		{"nodates", "1.1.0", ""},
	}
}

type agedHelm struct {
	r    *gin.Engine
	repo *domain.Repository
	up   *helmUpstream
}

func newAgedHelm(t *testing.T, charts []chartVersion, minAgeSecs int, indexTTL time.Duration, groupMember bool) *agedHelm {
	t.Helper()
	up := newHelmUpstream(t, charts)
	repo := &domain.Repository{
		ID: "helm-aged", Name: "helm-aged", Format: "helm", Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": up.srv.URL},
	}
	if minAgeSecs > 0 {
		repo.ProxyConfig["minimum_package_age"] = minAgeSecs
	}
	h := helm.New(formats.Deps{
		Repos:             testutil.NewRepoRepo(repo),
		Blobs:             testutil.NewBlobStoreRepo(),
		Components:        testutil.NewComponentRepo(),
		Assets:            testutil.NewAssetRepo(),
		BlobStore:         testutil.NewBlobStore(),
		BaseURL:           testBaseURL,
		HelmIndexCacheTTL: indexTTL,
	})
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		if groupMember {
			c.Set(formats.GroupMemberKey, true)
		}
		h.ServeHTTP(c)
	})
	return &agedHelm{r: r, repo: repo, up: up}
}

func (a *agedHelm) get(path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/helm-aged"+path, nil))
	return w
}

// servedVersions parses the served index into chart → versions.
func servedVersions(t *testing.T, body []byte) map[string][]string {
	t.Helper()
	var idx struct {
		APIVersion string `yaml:"apiVersion"`
		Entries    map[string][]struct {
			Version string `yaml:"version"`
		} `yaml:"entries"`
	}
	require.NoError(t, yaml.Unmarshal(body, &idx), "the served index must stay valid YAML")
	require.Equal(t, "v1", idx.APIVersion)
	out := map[string][]string{}
	for name, versions := range idx.Entries {
		for _, v := range versions {
			out[name] = append(out[name], v.Version)
		}
	}
	return out
}

const weekSecs = 7 * 24 * 3600

func TestHelm_AgePolicy_IndexOmitsYoungVersions(t *testing.T) {
	a := newAgedHelm(t, redisCharts(), weekSecs, 5*time.Minute, false)

	w := a.get("/index.yaml")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	got := servedVersions(t, w.Body.Bytes())
	assert.Equal(t, []string{"20.0.0", "19.6.4"}, got["redis"], "the young release is hidden, order kept")
	assert.NotContains(t, got, "fresh", "a chart with no old-enough version disappears")
	assert.Equal(t, []string{"1.0.0", "1.1.0"}, got["nodates"], "an undated chart cannot be policed")

	// The rest of each entry survives the filter.
	body := w.Body.String()
	assert.Contains(t, body, "image: docker.io/bitnami/redis:20.0.0-debian-12-r0")
	assert.Contains(t, body, "digest: 0123456789abcdef")
	assert.Contains(t, body, testBaseURL+"/repository/helm-aged/redis-20.0.0.tgz")
}

func TestHelm_AgePolicy_YoungArchiveIsRefused(t *testing.T) {
	a := newAgedHelm(t, redisCharts(), weekSecs, 5*time.Minute, false)

	for _, p := range []string{"/redis-20.1.0.tgz", "/redis-20.1.0.tgz.prov", "/fresh-0.1.0.tgz"} {
		w := a.get(p)
		assert.Equal(t, http.StatusForbidden, w.Code, p)
		assert.Contains(t, w.Body.String(), "was published", p)
		assert.Contains(t, w.Body.String(), "minimum package age", p)
	}
	for _, p := range []string{"/redis-20.0.0.tgz", "/redis-20.0.0.tgz.prov", "/nodates-1.1.0.tgz"} {
		w := a.get(p)
		assert.Equal(t, http.StatusOK, w.Code, p+": "+w.Body.String())
	}
	assert.Equal(t, int32(1), a.up.indexHits.Load(), "the gate reads dates from the cached index table")
}

// A chart cached before the policy was switched on is still refused.
func TestHelm_AgePolicy_CachedYoungArchiveIsRefused(t *testing.T) {
	a := newAgedHelm(t, redisCharts(), 0, 5*time.Minute, false)
	require.Equal(t, http.StatusOK, a.get("/redis-20.1.0.tgz").Code)

	a.repo.ProxyConfig["minimum_package_age"] = weekSecs
	w := a.get("/redis-20.1.0.tgz")
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
}

// An undated version of a dated chart has an unknowable age.
func TestHelm_AgePolicy_UndatedVersionOfDatedChartIsRefused(t *testing.T) {
	charts := append([]chartVersion{{"redis", "18.0.0", ""}}, redisCharts()...)
	a := newAgedHelm(t, charts, weekSecs, 5*time.Minute, false)

	w := a.get("/redis-18.0.0.tgz")
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "no created date")

	got := servedVersions(t, a.get("/index.yaml").Body.Bytes())
	assert.NotContains(t, got["redis"], "18.0.0")
}

// A download the index does not list cannot be dated: refused while the
// index carries dates, served when it carries none.
func TestHelm_AgePolicy_UnlistedArchive(t *testing.T) {
	a := newAgedHelm(t, redisCharts(), weekSecs, 5*time.Minute, false)
	a.up.extraFiles["ghost-1.0.0.tgz"] = true
	w := a.get("/ghost-1.0.0.tgz")
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "not in the upstream index")

	b := newAgedHelm(t, []chartVersion{{"nodates", "1.0.0", ""}}, weekSecs, 5*time.Minute, false)
	b.up.extraFiles["ghost-1.0.0.tgz"] = true
	w = b.get("/ghost-1.0.0.tgz")
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

// With upstream down, the last index table fetched still dates a cached
// chart, so the cache keeps serving.
func TestHelm_AgePolicy_StaleTableWhileUpstreamDown(t *testing.T) {
	a := newAgedHelm(t, redisCharts(), weekSecs, time.Millisecond, false)
	require.Equal(t, http.StatusOK, a.get("/redis-20.0.0.tgz").Code)

	a.up.down.Store(true)
	time.Sleep(5 * time.Millisecond)
	w := a.get("/redis-20.0.0.tgz")
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = a.get("/redis-20.1.0.tgz")
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
}

func TestHelm_AgePolicy_UpstreamDownWithoutTableIsBadGateway(t *testing.T) {
	a := newAgedHelm(t, redisCharts(), weekSecs, 5*time.Minute, false)
	a.up.down.Store(true)
	w := a.get("/redis-20.0.0.tgz")
	assert.Equal(t, http.StatusBadGateway, w.Code, w.Body.String())
}

// Without the index cache (helm.index_cache_ttl 0) the gate fetches the index.
func TestHelm_AgePolicy_NoIndexCache(t *testing.T) {
	a := newAgedHelm(t, redisCharts(), weekSecs, 0, false)
	assert.Equal(t, http.StatusForbidden, a.get("/redis-20.1.0.tgz").Code)
	assert.Equal(t, http.StatusOK, a.get("/redis-20.0.0.tgz").Code)
}

// A group asks its members with GroupMemberKey set, which maps an upstream
// 403 to a miss; the policy's own 403 must still reach the client.
func TestHelm_AgePolicy_RefusalSurvivesGroupFanOut(t *testing.T) {
	a := newAgedHelm(t, redisCharts(), weekSecs, 5*time.Minute, true)
	w := a.get("/redis-20.1.0.tgz")
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
}

func TestHelm_AgePolicy_DisabledByDefault(t *testing.T) {
	a := newAgedHelm(t, redisCharts(), 0, 5*time.Minute, false)
	got := servedVersions(t, a.get("/index.yaml").Body.Bytes())
	assert.Equal(t, []string{"20.1.0", "20.0.0", "19.6.4"}, got["redis"])
	assert.Contains(t, got, "fresh")
	assert.Equal(t, http.StatusOK, a.get("/redis-20.1.0.tgz").Code)
}
