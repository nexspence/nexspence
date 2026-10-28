package rubygems_test

import (
	"crypto/md5" //nolint:gosec // compact-index ETag, not security
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/rubygems"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Minimum package age on a RubyGems proxy (#643): versions younger than the
// configured age are left out of /info/<gem> and refused on .gem and quick
// gemspec downloads. Dates come from the created_at requirement rubygems.org
// writes into each compact-index line, or from its JSON API when the line
// carries none.

const day = 24 * time.Hour

func ago(d time.Duration) time.Time { return time.Now().Add(-d).UTC() }

// gemVersion is one version of the fake upstream. created zero = undated.
type gemVersion struct {
	number, platform string
	created          time.Time
}

func (v gemVersion) full() string {
	if v.platform == "" || v.platform == "ruby" {
		return v.number
	}
	return v.number + "-" + v.platform
}

// fakeGem is one gem of the fake upstream. infoDates controls whether /info
// lines carry created_at (as rubygems.org's do); api whether the JSON API
// answers.
type fakeGem struct {
	versions  []gemVersion
	infoDates bool
	api       bool
}

// infoBody renders /info/<gem> the way rubygems.org does.
func (g fakeGem) infoBody() string {
	var b strings.Builder
	b.WriteString("---\n")
	for _, v := range g.versions {
		fmt.Fprintf(&b, "%s rack:>= 2.2.4|checksum:%x,ruby:>= 2.7.0", v.full(), sha256.Sum256([]byte(v.full())))
		if g.infoDates && !v.created.IsZero() {
			fmt.Fprintf(&b, ",created_at:%s", v.created.Format(time.RFC3339))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// apiBody renders /api/v1/versions/<gem>.json (the fields that matter).
func (g fakeGem) apiBody() []byte {
	var doc []map[string]any
	for _, v := range g.versions {
		e := map[string]any{"number": v.number, "platform": v.platform, "prerelease": false}
		if e["platform"] == "" {
			e["platform"] = "ruby"
		}
		if !v.created.IsZero() {
			e["created_at"] = v.created.Format("2006-01-02T15:04:05.000Z")
		}
		doc = append(doc, e)
	}
	b, _ := json.Marshal(doc)
	return b
}

type gemHits struct{ info, api atomic.Int32 }

// gemsUpstream serves the rubygems.org paths a proxy reaches, with the
// integrity headers rubygems.org sends on the compact index.
func gemsUpstream(t *testing.T, gems map[string]fakeGem) (*httptest.Server, *gemHits) {
	t.Helper()
	hits := &gemHits{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		compact := func(body string) {
			sum := md5.Sum([]byte(body)) //nolint:gosec
			sha := sha256.Sum256([]byte(body))
			w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
			w.Header().Set("Repr-Digest", `sha-256="`+base64.StdEncoding.EncodeToString(sha[:])+`"`)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(body))
		}
		switch {
		case p == "/versions":
			var b strings.Builder
			b.WriteString("created_at: 2026-01-01T00:00:00Z\n---\n")
			for name, g := range gems {
				var vs []string
				for _, v := range g.versions {
					vs = append(vs, v.full())
				}
				sum := md5.Sum([]byte(g.infoBody())) //nolint:gosec
				fmt.Fprintf(&b, "%s %s %x\n", name, strings.Join(vs, ","), sum)
			}
			compact(b.String())
		case strings.HasPrefix(p, "/info/"):
			hits.info.Add(1)
			g, ok := gems[strings.TrimPrefix(p, "/info/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			compact(g.infoBody())
		case strings.HasPrefix(p, "/api/v1/versions/"):
			hits.api.Add(1)
			g, ok := gems[strings.TrimSuffix(strings.TrimPrefix(p, "/api/v1/versions/"), ".json")]
			if !ok || !g.api {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(g.apiBody())
		case strings.HasPrefix(p, "/gems/"), strings.HasPrefix(p, "/quick/Marshal.4.8/"):
			file := p[strings.LastIndex(p, "/")+1:]
			for name := range gems {
				if strings.HasPrefix(file, name+"-") {
					_, _ = w.Write([]byte("gem-bytes " + p))
					return
				}
			}
			http.NotFound(w, r)
		case p == "/specs.4.8.gz":
			_, _ = w.Write([]byte("marshal"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

// rackGem has an old release, an old java build, and a young release.
func rackGem() fakeGem {
	return fakeGem{versions: []gemVersion{
		{number: "3.1.0", created: ago(90 * day)},
		{number: "3.1.0", platform: "java", created: ago(80 * day)},
		{number: "3.2.0", created: ago(time.Hour)},
		{number: "3.2.0", platform: "java", created: ago(time.Hour)},
	}, infoDates: true, api: true}
}

func gemsProxyRepo(name, remote string, minAgeSecs int) *domain.Repository {
	repo := &domain.Repository{
		ID: name, Name: name, Format: "rubygems", Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remote},
	}
	if minAgeSecs > 0 {
		repo.ProxyConfig["minimum_package_age"] = minAgeSecs
	}
	return repo
}

func agedGemsProxy(t *testing.T, gems map[string]fakeGem) (*gin.Engine, *gemHits) {
	t.Helper()
	upstream, hits := gemsUpstream(t, gems)
	r, _ := setup(gemsProxyRepo("gems-aged", upstream.URL, 7*24*3600))
	return r, hits
}

func TestRubyGems_AgePolicy_InfoOmitsYoungVersions(t *testing.T) {
	r, _ := agedGemsProxy(t, map[string]fakeGem{"rack": rackGem()})

	for _, pass := range []string{"miss", "cache hit"} {
		w := get(r, "/repository/gems-aged/info/rack")
		require.Equal(t, http.StatusOK, w.Code, pass)
		body := w.Body.String()
		lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
		require.Len(t, lines, 3, pass+": "+body)
		assert.Equal(t, "---", lines[0])
		assert.True(t, strings.HasPrefix(lines[1], "3.1.0 rack:>= 2.2.4|checksum:"), pass)
		assert.True(t, strings.HasPrefix(lines[2], "3.1.0-java "), pass)
		assert.NotContains(t, body, "3.2.0", pass)

		// The integrity headers describe the body served, not upstream's.
		sum := md5.Sum(w.Body.Bytes()) //nolint:gosec
		assert.Equal(t, `"`+hex.EncodeToString(sum[:])+`"`, w.Header().Get("ETag"), pass)
		sha := sha256.Sum256(w.Body.Bytes())
		want := "sha-256=:" + base64.StdEncoding.EncodeToString(sha[:]) + ":"
		assert.Equal(t, want, w.Header().Get("Repr-Digest"), pass)
		assert.Equal(t, want, w.Header().Get("Digest"), pass)
		assert.Empty(t, w.Header().Get("X-Checksum-SHA256"), pass)
	}
}

// Nothing filtered: the upstream document and its headers pass unchanged.
func TestRubyGems_AgePolicy_OldInfoIsUnchanged(t *testing.T) {
	old := fakeGem{versions: []gemVersion{{number: "1.0.0", created: ago(400 * day)}}, infoDates: true}
	r, _ := agedGemsProxy(t, map[string]fakeGem{"old": old})

	w := get(r, "/repository/gems-aged/info/old")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, old.infoBody(), w.Body.String())
	assert.Contains(t, w.Header().Get("Repr-Digest"), `sha-256="`, "upstream's own digest still matches")
}

// A client that already knows the version must not bypass /info.
func TestRubyGems_AgePolicy_YoungFilesAreRefused(t *testing.T) {
	r, _ := agedGemsProxy(t, map[string]fakeGem{"rack": rackGem()})

	for _, p := range []string{
		"/gems/rack-3.2.0.gem",
		"/gems/rack-3.2.0-java.gem",
		"/quick/Marshal.4.8/rack-3.2.0.gemspec.rz",
	} {
		w := get(r, "/repository/gems-aged"+p)
		assert.Equal(t, http.StatusForbidden, w.Code, p)
		assert.Contains(t, w.Body.String(), "was published", p)
		assert.Contains(t, w.Body.String(), "minimum package age", p)
	}
	for _, p := range []string{
		"/gems/rack-3.1.0.gem",
		"/gems/rack-3.1.0-java.gem",
		"/quick/Marshal.4.8/rack-3.1.0.gemspec.rz",
	} {
		w := get(r, "/repository/gems-aged"+p)
		assert.Equal(t, http.StatusOK, w.Code, p+": "+w.Body.String())
	}
}

// A young gem already in the cache is refused too.
func TestRubyGems_AgePolicy_CachedYoungGemIsRefused(t *testing.T) {
	upstream, _ := gemsUpstream(t, map[string]fakeGem{"rack": rackGem()})
	repo := gemsProxyRepo("gems-late", upstream.URL, 0)
	r, _ := setup(repo)

	require.Equal(t, http.StatusOK, get(r, "/repository/gems-late/gems/rack-3.2.0.gem").Code)
	repo.ProxyConfig["minimum_package_age"] = 7 * 24 * 3600
	w := get(r, "/repository/gems-late/gems/rack-3.2.0.gem")
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
}

// An undated version, or one the index does not list (yanked), in a dated gem
// has an unknowable age: hidden and refused.
func TestRubyGems_AgePolicy_UndatedOrUnlistedVersionInDatedGemIsRefused(t *testing.T) {
	g := rackGem()
	g.versions = append(g.versions, gemVersion{number: "3.1.1"})
	r, _ := agedGemsProxy(t, map[string]fakeGem{"rack": g})

	w := get(r, "/repository/gems-aged/info/rack")
	assert.NotContains(t, w.Body.String(), "3.1.1")

	for _, v := range []string{"3.1.1", "3.0.9"} {
		w = get(r, "/repository/gems-aged/gems/rack-"+v+".gem")
		assert.Equal(t, http.StatusForbidden, w.Code, v)
		assert.Contains(t, w.Body.String(), "no publish time", v)
	}
}

// An upstream whose /info lines carry no created_at is dated from its JSON API.
func TestRubyGems_AgePolicy_JSONAPIDatesAnUndatedIndex(t *testing.T) {
	g := rackGem()
	g.infoDates = false
	r, hits := agedGemsProxy(t, map[string]fakeGem{"rack": g})

	w := get(r, "/repository/gems-aged/info/rack")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "3.1.0-java ")
	assert.NotContains(t, w.Body.String(), "3.2.0")

	w = get(r, "/repository/gems-aged/gems/rack-3.2.0-java.gem")
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	w = get(r, "/repository/gems-aged/gems/rack-3.1.0-java.gem")
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, int32(1), hits.api.Load(), "the JSON API answer is cached for the metadata TTL")
}

// No dates anywhere: the gem cannot be policed and is served whole (hybrid
// failure mode, as for npm, PyPI and Go).
func TestRubyGems_AgePolicy_NoDatesAtAllServesGem(t *testing.T) {
	g := rackGem()
	g.infoDates, g.api = false, false
	r, hits := agedGemsProxy(t, map[string]fakeGem{"rack": g})

	w := get(r, "/repository/gems-aged/info/rack")
	assert.Equal(t, g.infoBody(), w.Body.String())

	for range 2 {
		w = get(r, "/repository/gems-aged/gems/rack-3.2.0.gem")
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}
	assert.Equal(t, int32(2), hits.info.Load(), "an undated gem is remembered for the metadata TTL")
}

func TestRubyGems_AgePolicy_UnknownGemIsNotFound(t *testing.T) {
	r, _ := agedGemsProxy(t, map[string]fakeGem{"rack": rackGem()})

	assert.Equal(t, http.StatusNotFound, get(r, "/repository/gems-aged/info/nope").Code)
	assert.Equal(t, http.StatusNotFound, get(r, "/repository/gems-aged/gems/nope-1.0.0.gem").Code)
}

// A known publish time is immutable: it is looked up once.
func TestRubyGems_AgePolicy_VersionTimeIsCached(t *testing.T) {
	r, hits := agedGemsProxy(t, map[string]fakeGem{"rack": rackGem()})

	for range 3 {
		require.Equal(t, http.StatusOK, get(r, "/repository/gems-aged/gems/rack-3.1.0.gem").Code)
	}
	assert.Equal(t, int32(1), hits.info.Load())
}

// The download gate reads the proxy's own cached /info before upstream.
func TestRubyGems_AgePolicy_GateUsesCachedInfo(t *testing.T) {
	r, hits := agedGemsProxy(t, map[string]fakeGem{"rack": rackGem()})

	require.Equal(t, http.StatusOK, get(r, "/repository/gems-aged/info/rack").Code)
	require.Equal(t, http.StatusOK, get(r, "/repository/gems-aged/gems/rack-3.1.0.gem").Code)
	assert.Equal(t, int32(1), hits.info.Load())
}

// The undated Marshal indexes answer 404 under the policy; gem and Bundler
// fall back to the compact index. /versions is served unchanged.
func TestRubyGems_AgePolicy_LegacyIndexesAre404AndVersionsUnchanged(t *testing.T) {
	r, _ := agedGemsProxy(t, map[string]fakeGem{"rack": rackGem()})

	for _, p := range []string{"/specs.4.8.gz", "/latest_specs.4.8.gz", "/prerelease_specs.4.8.gz", "/api/v1/dependencies"} {
		assert.Equal(t, http.StatusNotFound, get(r, "/repository/gems-aged"+p).Code, p)
	}
	w := get(r, "/repository/gems-aged/versions")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "3.2.0")
}

func TestRubyGems_AgePolicy_DisabledByDefault(t *testing.T) {
	upstream, _ := gemsUpstream(t, map[string]fakeGem{"rack": rackGem()})
	r, _ := setup(gemsProxyRepo("gems-plain", upstream.URL, 0))

	w := get(r, "/repository/gems-plain/info/rack")
	assert.Equal(t, rackGem().infoBody(), w.Body.String())
	assert.Contains(t, w.Header().Get("Repr-Digest"), `sha-256="`)
	assert.Equal(t, http.StatusOK, get(r, "/repository/gems-plain/gems/rack-3.2.0.gem").Code)
	assert.Equal(t, http.StatusOK, get(r, "/repository/gems-plain/specs.4.8.gz").Code)
}

// A group calls its proxy member through the member's handler, so the policy
// holds there too.
func TestRubyGems_AgePolicy_HoldsThroughGroup(t *testing.T) {
	upstream, _ := gemsUpstream(t, map[string]fakeGem{"rack": rackGem()})
	proxy := gemsProxyRepo("gems-aged", upstream.URL, 7*24*3600)
	g := &domain.Repository{
		ID: "gems-group", Name: "gems-group", Format: "rubygems", Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []any{"gems-aged"}},
	}
	repos := testutil.NewRepoRepo(proxy, g)
	d := formats.Deps{
		Repos: repos, Blobs: testutil.NewBlobStoreRepo(), Components: testutil.NewComponentRepo(),
		Assets: testutil.NewAssetRepo(), BlobStore: testutil.NewBlobStore(), BaseURL: "http://localhost:8080",
	}
	gemsH := rubygems.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"rubygems": gemsH})
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		if c.Param("repoName") == "gems-group" {
			groupH.ServeHTTP(c)
			return
		}
		gemsH.ServeHTTP(c)
	})

	w := get(r, "/repository/gems-group/info/rack")
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "3.2.0")
	assert.Equal(t, http.StatusForbidden, get(r, "/repository/gems-group/gems/rack-3.2.0.gem").Code)
	assert.Equal(t, http.StatusOK, get(r, "/repository/gems-group/gems/rack-3.1.0.gem").Code)
}
