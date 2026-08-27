package npm_test

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

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/npm"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Minimum-package-age policy on an npm proxy (#323): versions younger than the
// configured age are invisible in metadata AND blocked on direct download.

// agePolicyUpstream serves a two-version packument (1.0.0 old, 2.0.0 published
// an hour ago) and both tarballs, counting tarball hits.
func agePolicyUpstream(t *testing.T, withDates bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var tarballHits atomic.Int32
	now := time.Now().UTC()
	doc := map[string]any{
		"name":      "pkg",
		"dist-tags": map[string]any{"latest": "2.0.0"},
		"versions": map[string]any{
			"1.0.0": map[string]any{"name": "pkg", "version": "1.0.0", "dist": map[string]any{"tarball": "https://up/pkg/-/pkg-1.0.0.tgz"}},
			"2.0.0": map[string]any{"name": "pkg", "version": "2.0.0", "dist": map[string]any{"tarball": "https://up/pkg/-/pkg-2.0.0.tgz"}},
		},
	}
	if withDates {
		doc["time"] = map[string]any{
			"created": now.Add(-90 * 24 * time.Hour).Format(time.RFC3339),
			"1.0.0":   now.Add(-90 * 24 * time.Hour).Format(time.RFC3339),
			"2.0.0":   now.Add(-1 * time.Hour).Format(time.RFC3339),
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/-/") {
			tarballHits.Add(1)
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("tarball-bytes"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(srv.Close)
	return srv, &tarballHits
}

// agedProxySetup is proxySetup with a 7-day minimum package age.
func agedProxySetup(upstream *httptest.Server) *gin.Engine {
	repo := &domain.Repository{
		ID: "npm-aged", Name: "npm-aged", Format: "npm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{
			"remote_url":          upstream.URL,
			"minimum_package_age": 7 * 24 * 3600,
		},
	}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := npm.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func TestNPM_AgePolicy_MetadataHidesYoungVersion(t *testing.T) {
	upstream, _ := agePolicyUpstream(t, true)
	r := agedProxySetup(upstream)

	req := httptest.NewRequest(http.MethodGet, "/repository/npm-aged/pkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var doc map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	versions := doc["versions"].(map[string]any)
	assert.Contains(t, versions, "1.0.0")
	assert.NotContains(t, versions, "2.0.0", "an hour-old version must be invisible behind a 7-day policy")
	assert.Equal(t, "1.0.0", doc["dist-tags"].(map[string]any)["latest"],
		"latest falls back to the newest version old enough to serve")
}

func TestNPM_AgePolicy_TarballOfYoungVersionIs403(t *testing.T) {
	upstream, tarballHits := agePolicyUpstream(t, true)
	r := agedProxySetup(upstream)

	req := httptest.NewRequest(http.MethodGet, "/repository/npm-aged/pkg/-/pkg-2.0.0.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code,
		"a direct URL must not bypass the metadata filter: %d %s", w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "minimum package age")
	assert.Zero(t, tarballHits.Load(), "the young tarball must never be fetched upstream")
}

func TestNPM_AgePolicy_TarballOfOldVersionServes(t *testing.T) {
	upstream, _ := agePolicyUpstream(t, true)
	r := agedProxySetup(upstream)

	req := httptest.NewRequest(http.MethodGet, "/repository/npm-aged/pkg/-/pkg-1.0.0.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "tarball-bytes", w.Body.String())
}

// Hybrid failure mode: an upstream that publishes no dates at all cannot be
// policed — the package is served rather than hidden wholesale.
func TestNPM_AgePolicy_UpstreamWithoutDates_SkipsPolicy(t *testing.T) {
	upstream, _ := agePolicyUpstream(t, false)
	r := agedProxySetup(upstream)

	req := httptest.NewRequest(http.MethodGet, "/repository/npm-aged/pkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	assert.Contains(t, doc["versions"].(map[string]any), "2.0.0",
		"no dates → policy skipped, the document is served as-is")

	req = httptest.NewRequest(http.MethodGet, "/repository/npm-aged/pkg/-/pkg-2.0.0.tgz", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "no dates → the download gate opens too")
}

// A proxy without the option keeps today's behavior byte for byte.
func TestNPM_AgePolicy_DisabledByDefault(t *testing.T) {
	upstream, _ := agePolicyUpstream(t, true)
	r, _ := proxySetup(upstream)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/repository/%s/pkg", "npm-proxy"), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	assert.Contains(t, doc["versions"].(map[string]any), "2.0.0")
}

// Prerelease versions carry hyphens ("1.0.0-beta.1"): the version must be cut
// at the package-name prefix, not at the last hyphen, or an OLD prerelease
// fails closed as "not in the publish history" — a false positive that blocks
// a perfectly aged artifact.
func TestNPM_AgePolicy_OldPrereleaseTarballServes(t *testing.T) {
	now := time.Now().UTC()
	doc := map[string]any{
		"name":      "pkg",
		"dist-tags": map[string]any{"latest": "1.0.0-beta.1"},
		"versions": map[string]any{
			"1.0.0-beta.1": map[string]any{"name": "pkg", "version": "1.0.0-beta.1", "dist": map[string]any{"tarball": "https://up/pkg/-/pkg-1.0.0-beta.1.tgz"}},
		},
		"time": map[string]any{
			"1.0.0-beta.1": now.Add(-90 * 24 * time.Hour).Format(time.RFC3339),
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/-/") {
			_, _ = w.Write([]byte("tarball-bytes"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(srv.Close)
	r := agedProxySetup(srv)

	req := httptest.NewRequest(http.MethodGet, "/repository/npm-aged/pkg/-/pkg-1.0.0-beta.1.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code,
		"a 90-day-old prerelease must serve: %d %s", w.Code, w.Body.String())
}

// Scoped packages reach upstream as "@scope%2Fname" (one escape). Found live:
// JoinURL re-escaped the "%" into "%252F" and registry.npmjs.org answered 405
// for every scoped package pulled through a proxy.
func TestNPM_Proxy_ScopedMetadata_SingleEscapeUpstream(t *testing.T) {
	var gotURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI
		if r.RequestURI != "/@types%2Fnode" {
			// Mimic registry.npmjs.org: anything else (double-escaped included)
			// is not a package route.
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"@types/node","dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{"name":"@types/node","version":"1.0.0"}}}`))
	}))
	t.Cleanup(srv.Close)
	r, _ := proxySetup(srv)

	req := httptest.NewRequest(http.MethodGet, "/repository/npm-proxy/@types/node", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code,
		"scoped metadata failed (upstream saw %q): %d %s", gotURI, w.Code, w.Body.String())
	assert.Equal(t, "/@types%2Fnode", gotURI)
	assert.Contains(t, w.Body.String(), `"@types/node"`)
}
