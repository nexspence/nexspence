package gomod_test

import (
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
	"golang.org/x/mod/module"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// Minimum package age on a Go proxy (#643): versions younger than the
// configured age are left out of @v/list and @latest and refused on
// .info/.mod/.zip. Publish times come from the upstream .info documents.

// goModule is one module of the fake upstream: version → publish time, ""
// for an .info without Time. latest is what @latest answers ("" = 404).
type goModule struct {
	times  map[string]string
	order  []string
	latest string
}

// goUpstream serves GOPROXY paths for modules keyed by their escaped path
// ("github.com/!azure/lib"). infoHits counts .info requests.
func goUpstream(t *testing.T, modules map[string]goModule) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var infoHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		var mod string
		var rest string
		switch {
		case strings.HasSuffix(p, "/@latest"):
			mod, rest = strings.TrimSuffix(p, "/@latest"), "@latest"
		case strings.Contains(p, "/@v/"):
			i := strings.Index(p, "/@v/")
			mod, rest = p[:i], p[i+len("/@v/"):]
		}
		m, ok := modules[mod]
		if !ok {
			http.NotFound(w, r)
			return
		}
		unescape := func(v string) string {
			u, err := module.UnescapeVersion(v)
			if err != nil {
				return v
			}
			return u
		}
		info := func(v string) {
			ts, ok := m.times[unescape(v)]
			if !ok {
				http.NotFound(w, r)
				return
			}
			doc := map[string]string{"Version": v}
			if ts != "" {
				doc["Time"] = ts
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doc)
		}
		switch {
		case rest == "list":
			w.Header().Set("Content-Type", "text/plain")
			for _, v := range m.order {
				fmt.Fprintln(w, v)
			}
		case rest == "@latest":
			if m.latest == "" {
				http.NotFound(w, r)
				return
			}
			info(m.latest)
		case strings.HasSuffix(rest, ".info"):
			infoHits.Add(1)
			info(strings.TrimSuffix(rest, ".info"))
		case strings.HasSuffix(rest, ".mod"), strings.HasSuffix(rest, ".zip"):
			v := strings.TrimSuffix(strings.TrimSuffix(rest, ".mod"), ".zip")
			if _, ok := m.times[unescape(v)]; !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte("module-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &infoHits
}

func ago(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }

const day = 24 * time.Hour

// lib has an old release, a young release and an old prerelease; latest is
// the young one.
func libModule() goModule {
	return goModule{
		times: map[string]string{
			"v1.0.0":       ago(90 * day),
			"v1.1.0-beta1": ago(60 * day),
			"v1.1.0":       ago(time.Hour),
		},
		order:  []string{"v1.0.0", "v1.1.0-beta1", "v1.1.0"},
		latest: "v1.1.0",
	}
}

func goProxyRepo(name, remote string, minAgeSecs int) *domain.Repository {
	repo := &domain.Repository{
		ID: name, Name: name, Format: "go", Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remote},
	}
	if minAgeSecs > 0 {
		repo.ProxyConfig["minimum_package_age"] = minAgeSecs
	}
	return repo
}

func get(r *gin.Engine, url string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

func agedGoProxy(t *testing.T, modules map[string]goModule) (*gin.Engine, *atomic.Int32) {
	t.Helper()
	upstream, hits := goUpstream(t, modules)
	return setup(goProxyRepo("go-aged", upstream.URL, 7*24*3600)), hits
}

func TestGo_AgePolicy_ListOmitsYoungVersions(t *testing.T) {
	r, _ := agedGoProxy(t, map[string]goModule{"example.com/lib": libModule()})

	w := get(r, "/repository/go-aged/example.com/lib/@v/list")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "v1.0.0\nv1.1.0-beta1\n", w.Body.String())
}

func TestGo_AgePolicy_LatestFallsBackToNewestOldRelease(t *testing.T) {
	r, _ := agedGoProxy(t, map[string]goModule{"example.com/lib": libModule()})

	w := get(r, "/repository/go-aged/example.com/lib/@latest")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var doc struct{ Version, Time string }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	assert.Equal(t, "v1.0.0", doc.Version, "a release outranks a newer prerelease, as the go command ranks them")
	assert.NotEmpty(t, doc.Time)
}

func TestGo_AgePolicy_OldLatestIsServedAsIs(t *testing.T) {
	m := libModule()
	m.latest = "v1.0.0"
	r, _ := agedGoProxy(t, map[string]goModule{"example.com/lib": m})

	w := get(r, "/repository/go-aged/example.com/lib/@latest")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"Version":"v1.0.0"`)
}

// A module without tags answers @latest with a pseudo-version that @v/list
// never names; when it is old enough it is served.
func TestGo_AgePolicy_UntaggedModuleLatest(t *testing.T) {
	pseudo := "v0.0.0-20240101000000-abcdefabcdef"
	r, _ := agedGoProxy(t, map[string]goModule{"example.com/notags": {
		times: map[string]string{pseudo: ago(200 * day)}, latest: pseudo,
	}})

	w := get(r, "/repository/go-aged/example.com/notags/@latest")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), pseudo)
}

func TestGo_AgePolicy_EveryVersionTooYoungIsNoVersion(t *testing.T) {
	r, _ := agedGoProxy(t, map[string]goModule{"example.com/fresh": {
		times:  map[string]string{"v0.1.0": ago(time.Hour)},
		order:  []string{"v0.1.0"},
		latest: "v0.1.0",
	}})

	w := get(r, "/repository/go-aged/example.com/fresh/@latest")
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "minimum package age")

	w = get(r, "/repository/go-aged/example.com/fresh/@v/list")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())
}

// A client that already knows the version must not bypass the list.
func TestGo_AgePolicy_YoungVersionFilesAreRefused(t *testing.T) {
	r, _ := agedGoProxy(t, map[string]goModule{"example.com/lib": libModule()})

	for _, suf := range []string{".info", ".mod", ".zip"} {
		w := get(r, "/repository/go-aged/example.com/lib/@v/v1.1.0"+suf)
		assert.Equal(t, http.StatusForbidden, w.Code, suf)
		assert.Contains(t, w.Body.String(), "minimum package age", suf)

		w = get(r, "/repository/go-aged/example.com/lib/@v/v1.0.0"+suf)
		assert.Equal(t, http.StatusOK, w.Code, suf+": "+w.Body.String())
	}
}

// An undated version in a dated module has an unknowable age: refused, and
// left out of the list.
func TestGo_AgePolicy_UndatedVersionInDatedModuleIsRefused(t *testing.T) {
	m := libModule()
	m.times["v1.0.1"] = ""
	m.order = append(m.order, "v1.0.1")
	r, _ := agedGoProxy(t, map[string]goModule{"example.com/lib": m})

	w := get(r, "/repository/go-aged/example.com/lib/@v/v1.0.1.zip")
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "no publish time")

	w = get(r, "/repository/go-aged/example.com/lib/@v/list")
	assert.NotContains(t, w.Body.String(), "v1.0.1")
}

// An upstream whose .info documents carry no Time cannot be policed: the
// module is served whole (hybrid failure mode, as for npm and PyPI).
func TestGo_AgePolicy_NoDatesAtAllServesModule(t *testing.T) {
	r, _ := agedGoProxy(t, map[string]goModule{"example.com/nodates": {
		times:  map[string]string{"v1.0.0": "", "v1.1.0": ""},
		order:  []string{"v1.0.0", "v1.1.0"},
		latest: "v1.1.0",
	}})

	w := get(r, "/repository/go-aged/example.com/nodates/@v/list")
	assert.Equal(t, "v1.0.0\nv1.1.0\n", w.Body.String())

	w = get(r, "/repository/go-aged/example.com/nodates/@latest")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "v1.1.0")

	w = get(r, "/repository/go-aged/example.com/nodates/@v/v1.1.0.zip")
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

// A version upstream does not have stays a 404, not a policy refusal.
func TestGo_AgePolicy_UnknownVersionIsNotFound(t *testing.T) {
	r, _ := agedGoProxy(t, map[string]goModule{"example.com/lib": libModule()})

	w := get(r, "/repository/go-aged/example.com/lib/@v/v9.9.9.zip")
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
}

// Module paths and versions with upper-case letters travel escaped.
func TestGo_AgePolicy_EscapedModulePath(t *testing.T) {
	r, _ := agedGoProxy(t, map[string]goModule{"github.com/!azure/lib": {
		times: map[string]string{"v1.0.0": ago(90 * day), "v1.0.1-RC": ago(time.Hour)},
		order: []string{"v1.0.0", "v1.0.1-RC"},
	}})

	w := get(r, "/repository/go-aged/github.com/!azure/lib/@v/list")
	assert.Equal(t, "v1.0.0\n", w.Body.String())

	w = get(r, "/repository/go-aged/github.com/!azure/lib/@v/v1.0.1-!r!c.zip")
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "was published")
	w = get(r, "/repository/go-aged/github.com/!azure/lib/@v/v1.0.0.zip")
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

// A known publish time is immutable: it is looked up once.
func TestGo_AgePolicy_VersionTimeIsCached(t *testing.T) {
	r, hits := agedGoProxy(t, map[string]goModule{"example.com/lib": libModule()})

	for range 3 {
		w := get(r, "/repository/go-aged/example.com/lib/@v/v1.0.0.mod")
		require.Equal(t, http.StatusOK, w.Code)
	}
	assert.Equal(t, int32(1), hits.Load())
}

func TestGo_AgePolicy_DisabledByDefault(t *testing.T) {
	upstream, _ := goUpstream(t, map[string]goModule{"example.com/lib": libModule()})
	r := setup(goProxyRepo("go-plain", upstream.URL, 0))

	w := get(r, "/repository/go-plain/example.com/lib/@v/list")
	assert.Equal(t, "v1.0.0\nv1.1.0-beta1\nv1.1.0\n", w.Body.String())
	w = get(r, "/repository/go-plain/example.com/lib/@v/v1.1.0.zip")
	assert.Equal(t, http.StatusOK, w.Code)
}
