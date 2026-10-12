package cargo_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/cargo"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Minimum package age on a Cargo proxy (#643): versions younger than the
// configured age are left out of the sparse-index file and refused on
// download. Dates come from the "pubtime" index.crates.io writes into each
// index line, or from the registry's web API ("api" in its config.json) when
// the lines carry none.

const day = 24 * time.Hour

func ago(d time.Duration) time.Time { return time.Now().Add(-d).UTC() }

// crateVersion is one version of the fake registry. created zero = undated.
type crateVersion struct {
	vers    string
	created time.Time
}

// fakeCrate is one crate of the fake registry. pubtime controls whether its
// index lines carry pubtime (as index.crates.io's do); api whether the web
// API knows it.
type fakeCrate struct {
	versions []crateVersion
	pubtime  bool
	api      bool
}

// indexBody renders the sparse-index file the way index.crates.io does.
func (c fakeCrate) indexBody(name string) string {
	var b strings.Builder
	for _, v := range c.versions {
		rec := map[string]any{
			"name": name, "vers": v.vers, "deps": []any{}, "features": map[string]any{}, "yanked": false,
			"cksum": fmt.Sprintf("%x", sha256.Sum256([]byte(v.vers))),
		}
		if c.pubtime && !v.created.IsZero() {
			rec["pubtime"] = v.created.Format(time.RFC3339)
		}
		line, _ := json.Marshal(rec)
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

type cargoHits struct{ index, api, apiNoUA atomic.Int32 }

// cratesRegistry runs the three hosts of crates.io: the sparse index (whose
// config.json names the other two), the download host and the web API, which
// refuses a request without a User-Agent and pages its answer.
func cratesRegistry(t *testing.T, crates map[string]fakeCrate, withAPI bool) (*httptest.Server, *cargoHits) {
	t.Helper()
	hits := &cargoHits{}
	dlHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // crates/<name>/<vers>/download
		if len(parts) != 4 || parts[0] != "crates" {
			http.NotFound(w, r)
			return
		}
		if _, ok := crates[parts[1]]; !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("crate-bytes " + parts[1] + " " + parts[2]))
	}))
	t.Cleanup(dlHost.Close)

	apiHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			hits.apiNoUA.Add(1)
			http.Error(w, "We require that all requests include a `User-Agent` header.", http.StatusForbidden)
			return
		}
		name, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/crates/"), "/versions")
		c, known := crates[name]
		if !ok || !known || !c.api {
			http.NotFound(w, r)
			return
		}
		hits.api.Add(1)
		// Two versions per page, newest first, as crates.io pages them.
		const perPage = 2
		start, _ := strconv.Atoi(r.URL.Query().Get("seek"))
		var page []map[string]any
		for i := len(c.versions) - 1 - start; i >= 0 && len(page) < perPage; i-- {
			v := c.versions[i]
			e := map[string]any{"num": v.vers, "yanked": false}
			if !v.created.IsZero() {
				e["created_at"] = v.created.Format("2006-01-02T15:04:05.000000Z07:00")
			}
			page = append(page, e)
		}
		meta := map[string]any{"total": len(c.versions), "next_page": nil}
		if start+perPage < len(c.versions) {
			meta["next_page"] = fmt.Sprintf("?per_page=%d&seek=%d", perPage, start+perPage)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"versions": page, "meta": meta})
	}))
	t.Cleanup(apiHost.Close)

	indexHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/config.json" {
			cfg := map[string]string{"dl": dlHost.URL + "/crates"}
			if withAPI {
				cfg["api"] = apiHost.URL
			}
			_ = json.NewEncoder(w).Encode(cfg)
			return
		}
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		c, ok := crates[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		hits.index.Add(1)
		w.Header().Set("ETag", `"upstream-etag"`)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(c.indexBody(name)))
	}))
	t.Cleanup(indexHost.Close)
	return indexHost, hits
}

// serdeCrate has two old releases and a young one.
func serdeCrate() fakeCrate {
	return fakeCrate{versions: []crateVersion{
		{vers: "1.0.0", created: ago(400 * day)},
		{vers: "1.0.1", created: ago(90 * day)},
		{vers: "1.0.2", created: ago(time.Hour)},
	}, pubtime: true, api: true}
}

func cargoProxyRepo(name, remote string, minAgeSecs int) *domain.Repository {
	repo := &domain.Repository{
		ID: name, Name: name, Format: "cargo", Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remote},
	}
	if minAgeSecs > 0 {
		repo.ProxyConfig["minimum_package_age"] = minAgeSecs
	}
	return repo
}

func cargoRouter(repos ...*domain.Repository) *gin.Engine {
	d := formats.Deps{
		Repos: testutil.NewRepoRepo(repos...), Blobs: testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(), Assets: testutil.NewAssetRepo(),
		BlobStore: testutil.NewBlobStore(), BaseURL: "http://localhost:8080",
	}
	h := cargo.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"cargo": h})
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		if repo, _ := d.Repos.Get(c.Request.Context(), c.Param("repoName")); repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		h.ServeHTTP(c)
	})
	return r
}

func cargoGet(r *gin.Engine, url string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

func agedCargoProxy(t *testing.T, crates map[string]fakeCrate, withAPI bool) (*gin.Engine, *cargoHits) {
	t.Helper()
	upstream, hits := cratesRegistry(t, crates, withAPI)
	return cargoRouter(cargoProxyRepo("cargo-aged", upstream.URL, 7*24*3600)), hits
}

// indexVersions lists the vers of each line of a sparse-index body.
func indexVersions(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec struct{ Vers string }
		require.NoError(t, json.Unmarshal([]byte(line), &rec), line)
		out = append(out, rec.Vers)
	}
	return out
}

func TestCargo_AgePolicy_IndexOmitsYoungVersions(t *testing.T) {
	r, _ := agedCargoProxy(t, map[string]fakeCrate{"serde": serdeCrate()}, true)

	for _, pass := range []string{"miss", "cache hit"} {
		w := cargoGet(r, "/repository/cargo-aged/index/se/rd/serde")
		require.Equal(t, http.StatusOK, w.Code, pass)
		assert.Equal(t, []string{"1.0.0", "1.0.1"}, indexVersions(t, w.Body.String()), pass)
		// Kept lines are upstream's bytes.
		assert.True(t, strings.HasPrefix(serdeCrate().indexBody("serde"), w.Body.String()), pass)
		sum := sha256.Sum256(w.Body.Bytes())
		assert.Equal(t, `"`+hex.EncodeToString(sum[:])+`"`, w.Header().Get("ETag"), pass)
	}
}

// A client that already knows the version must not bypass the index.
func TestCargo_AgePolicy_YoungCrateIsRefused(t *testing.T) {
	r, _ := agedCargoProxy(t, map[string]fakeCrate{"serde": serdeCrate()}, true)

	w := cargoGet(r, "/repository/cargo-aged/api/v1/crates/serde/1.0.2/download")
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "was published")
	assert.Contains(t, w.Body.String(), "minimum package age")

	w = cargoGet(r, "/repository/cargo-aged/api/v1/crates/serde/1.0.1/download")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "crate-bytes serde 1.0.1", w.Body.String())
}

// A young crate already in the cache is refused too.
func TestCargo_AgePolicy_CachedYoungCrateIsRefused(t *testing.T) {
	upstream, _ := cratesRegistry(t, map[string]fakeCrate{"serde": serdeCrate()}, true)
	repo := cargoProxyRepo("cargo-late", upstream.URL, 0)
	r := cargoRouter(repo)

	require.Equal(t, http.StatusOK, cargoGet(r, "/repository/cargo-late/api/v1/crates/serde/1.0.2/download").Code)
	repo.ProxyConfig["minimum_package_age"] = 7 * 24 * 3600
	w := cargoGet(r, "/repository/cargo-late/api/v1/crates/serde/1.0.2/download")
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
}

// An undated version, or one the index does not list, in a dated crate has an
// unknowable age: hidden and refused.
func TestCargo_AgePolicy_UndatedOrUnlistedVersionInDatedCrateIsRefused(t *testing.T) {
	c := serdeCrate()
	c.versions = append(c.versions, crateVersion{vers: "1.0.3"})
	r, _ := agedCargoProxy(t, map[string]fakeCrate{"serde": c}, true)

	w := cargoGet(r, "/repository/cargo-aged/index/se/rd/serde")
	assert.Equal(t, []string{"1.0.0", "1.0.1"}, indexVersions(t, w.Body.String()))

	for _, v := range []string{"1.0.3", "0.9.0"} {
		w = cargoGet(r, "/repository/cargo-aged/api/v1/crates/serde/"+v+"/download")
		assert.Equal(t, http.StatusForbidden, w.Code, v)
		assert.Contains(t, w.Body.String(), "no publish time", v)
	}
}

// A registry whose index lines carry no pubtime is dated from its web API,
// found through the "api" of its config.json, paged and with a User-Agent.
func TestCargo_AgePolicy_WebAPIDatesAnUndatedIndex(t *testing.T) {
	c := serdeCrate()
	c.pubtime = false
	r, hits := agedCargoProxy(t, map[string]fakeCrate{"serde": c}, true)

	w := cargoGet(r, "/repository/cargo-aged/index/se/rd/serde")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{"1.0.0", "1.0.1"}, indexVersions(t, w.Body.String()))

	assert.Equal(t, http.StatusForbidden, cargoGet(r, "/repository/cargo-aged/api/v1/crates/serde/1.0.2/download").Code)
	assert.Equal(t, http.StatusOK, cargoGet(r, "/repository/cargo-aged/api/v1/crates/serde/1.0.0/download").Code)
	assert.Equal(t, int32(2), hits.api.Load(), "two pages, once: the answer is cached for the metadata TTL")
	assert.Zero(t, hits.apiNoUA.Load())
}

// No pubtime and no API: the crate cannot be policed and is served whole
// (hybrid failure mode, as for npm, PyPI and Go).
func TestCargo_AgePolicy_NoDatesAtAllServesCrate(t *testing.T) {
	c := serdeCrate()
	c.pubtime = false
	r, hits := agedCargoProxy(t, map[string]fakeCrate{"serde": c}, false)

	w := cargoGet(r, "/repository/cargo-aged/index/se/rd/serde")
	assert.Equal(t, c.indexBody("serde"), w.Body.String())
	assert.Equal(t, `"upstream-etag"`, w.Header().Get("ETag"))

	for range 2 {
		w = cargoGet(r, "/repository/cargo-aged/api/v1/crates/serde/1.0.2/download")
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}
	assert.Equal(t, int32(2), hits.index.Load(), "an undated crate is remembered for the metadata TTL")
}

func TestCargo_AgePolicy_UnknownCrateIsNotFound(t *testing.T) {
	r, _ := agedCargoProxy(t, map[string]fakeCrate{"serde": serdeCrate()}, true)

	assert.Equal(t, http.StatusNotFound, cargoGet(r, "/repository/cargo-aged/index/no/pe/nope").Code)
	assert.Equal(t, http.StatusNotFound, cargoGet(r, "/repository/cargo-aged/api/v1/crates/nope/1.0.0/download").Code)
}

// A known publish time is immutable: it is looked up once; and the gate reads
// the proxy's own cached index file before upstream.
func TestCargo_AgePolicy_VersionTimeIsCachedAndCachedIndexIsRead(t *testing.T) {
	r, hits := agedCargoProxy(t, map[string]fakeCrate{"serde": serdeCrate()}, true)

	require.Equal(t, http.StatusOK, cargoGet(r, "/repository/cargo-aged/index/se/rd/serde").Code)
	for range 3 {
		require.Equal(t, http.StatusOK, cargoGet(r, "/repository/cargo-aged/api/v1/crates/serde/1.0.0/download").Code)
	}
	assert.Equal(t, int32(1), hits.index.Load())
}

// Short names use the 1/2/3 index directories.
func TestCargo_AgePolicy_ShortCrateNames(t *testing.T) {
	r, _ := agedCargoProxy(t, map[string]fakeCrate{"cc": serdeCrate(), "syn": serdeCrate()}, true)

	assert.Equal(t, []string{"1.0.0", "1.0.1"}, indexVersions(t, cargoGet(r, "/repository/cargo-aged/index/2/cc").Body.String()))
	assert.Equal(t, http.StatusForbidden, cargoGet(r, "/repository/cargo-aged/api/v1/crates/syn/1.0.2/download").Code)
	assert.Equal(t, http.StatusOK, cargoGet(r, "/repository/cargo-aged/api/v1/crates/syn/1.0.1/download").Code)
}

func TestCargo_AgePolicy_DisabledByDefault(t *testing.T) {
	upstream, _ := cratesRegistry(t, map[string]fakeCrate{"serde": serdeCrate()}, true)
	r := cargoRouter(cargoProxyRepo("cargo-plain", upstream.URL, 0))

	w := cargoGet(r, "/repository/cargo-plain/index/se/rd/serde")
	assert.Equal(t, serdeCrate().indexBody("serde"), w.Body.String())
	assert.Equal(t, http.StatusOK, cargoGet(r, "/repository/cargo-plain/api/v1/crates/serde/1.0.2/download").Code)
}

// A group calls its proxy member through the member's handler, so the policy
// holds there too.
func TestCargo_AgePolicy_HoldsThroughGroup(t *testing.T) {
	upstream, _ := cratesRegistry(t, map[string]fakeCrate{"serde": serdeCrate()}, true)
	r := cargoRouter(cargoProxyRepo("cargo-aged", upstream.URL, 7*24*3600), &domain.Repository{
		ID: "cargo-group", Name: "cargo-group", Format: "cargo", Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []any{"cargo-aged"}},
	})

	w := cargoGet(r, "/repository/cargo-group/index/se/rd/serde")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{"1.0.0", "1.0.1"}, indexVersions(t, w.Body.String()))
	assert.Equal(t, http.StatusForbidden, cargoGet(r, "/repository/cargo-group/api/v1/crates/serde/1.0.2/download").Code)
	assert.Equal(t, http.StatusOK, cargoGet(r, "/repository/cargo-group/api/v1/crates/serde/1.0.1/download").Code)
}
