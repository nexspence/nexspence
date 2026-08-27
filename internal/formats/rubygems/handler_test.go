package rubygems_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/md5" //nolint:gosec // compact-index integrity hash, not security
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/formats/rubygems"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// buildTestGem assembles a structurally faithful .gem for the given spec YAML.
func buildTestGem(t *testing.T, specYAML string) []byte {
	t.Helper()
	var meta bytes.Buffer
	gz := gzip.NewWriter(&meta)
	_, err := gz.Write([]byte(specYAML))
	require.NoError(t, err)
	require.NoError(t, gz.Close())

	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for _, f := range []struct {
		name string
		body []byte
	}{
		{"metadata.gz", meta.Bytes()},
		{"data.tar.gz", []byte("stub")},
	} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body))}))
		_, err := tw.Write(f.body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return out.Bytes()
}

func gemYAML(name, version string, deps ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- !ruby/object:Gem::Specification\nname: %s\nversion: !ruby/object:Gem::Version\n  version: %s\nplatform: ruby\n", name, version)
	if len(deps) == 0 {
		b.WriteString("dependencies: []\n")
	} else {
		b.WriteString("dependencies:\n")
	}
	for _, d := range deps {
		fmt.Fprintf(&b, `- !ruby/object:Gem::Dependency
  name: %s
  requirement: !ruby/object:Gem::Requirement
    requirements:
    - - ">="
      - !ruby/object:Gem::Version
        version: '1.0'
  type: :runtime
`, d)
	}
	return b.String()
}

func setup(repo *domain.Repository) (*gin.Engine, formats.Deps) {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := rubygems.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, d
}

func publish(t *testing.T, r *gin.Engine, repo string, gem []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/repository/"+repo+"/api/v1/gems", bytes.NewReader(gem))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = int64(len(gem))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func get(r *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ── hosted ────────────────────────────────────────────────────────────────────

func TestRubyGems_PublishStoresAndServesTheGem(t *testing.T) {
	repo := testutil.SimpleRepo("gems-hosted", "rubygems")
	r, _ := setup(repo)

	gem := buildTestGem(t, gemYAML("demo", "1.0.0", "rack"))
	w := publish(t, r, "gems-hosted", gem)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	dl := get(r, "/repository/gems-hosted/gems/demo-1.0.0.gem")
	require.Equal(t, http.StatusOK, dl.Code)
	assert.Equal(t, gem, dl.Body.Bytes(), "the stored gem is byte-identical")
}

func TestRubyGems_CompactIndexListsPublishedGems(t *testing.T) {
	repo := testutil.SimpleRepo("gems-idx", "rubygems")
	r, _ := setup(repo)

	require.Equal(t, http.StatusOK, publish(t, r, "gems-idx", buildTestGem(t, gemYAML("alpha", "1.0.0"))).Code)
	require.Equal(t, http.StatusOK, publish(t, r, "gems-idx", buildTestGem(t, gemYAML("alpha", "1.1.0", "rack"))).Code)
	require.Equal(t, http.StatusOK, publish(t, r, "gems-idx", buildTestGem(t, gemYAML("beta", "0.9.0"))).Code)

	names := get(r, "/repository/gems-idx/names")
	require.Equal(t, http.StatusOK, names.Code)
	assert.Equal(t, "---\nalpha\nbeta\n", names.Body.String())

	info := get(r, "/repository/gems-idx/info/alpha")
	require.Equal(t, http.StatusOK, info.Code)
	lines := strings.Split(strings.TrimSpace(info.Body.String()), "\n")
	require.Len(t, lines, 3) // "---" + two versions
	assert.Equal(t, "---", lines[0])
	assert.True(t, strings.HasPrefix(lines[1], "1.0.0 |checksum:"), "no deps before the pipe: %q", lines[1])
	assert.True(t, strings.HasPrefix(lines[2], "1.1.0 rack:>= 1.0|checksum:"), "runtime dep listed: %q", lines[2])

	versions := get(r, "/repository/gems-idx/versions")
	require.Equal(t, http.StatusOK, versions.Code)
	body := versions.Body.String()
	require.True(t, strings.HasPrefix(body, "created_at:"), body)
	require.Contains(t, body, "\n---\n")

	// The per-gem line carries the MD5 of the /info file — that is how Bundler
	// validates the info it fetches.
	sum := md5.Sum(info.Body.Bytes()) //nolint:gosec
	assert.Contains(t, body, "alpha 1.0.0,1.1.0 "+hex.EncodeToString(sum[:])+"\n")
	assert.Contains(t, body, "beta 0.9.0 ")
}

func TestRubyGems_ChecksumInInfoMatchesTheStoredGem(t *testing.T) {
	repo := testutil.SimpleRepo("gems-sum", "rubygems")
	r, _ := setup(repo)

	gem := buildTestGem(t, gemYAML("summed", "3.1.4"))
	require.Equal(t, http.StatusOK, publish(t, r, "gems-sum", gem).Code)

	sum := sha256.Sum256(gem)
	info := get(r, "/repository/gems-sum/info/summed")
	assert.Contains(t, info.Body.String(), "checksum:"+hex.EncodeToString(sum[:]),
		"Bundler verifies the downloaded gem against this digest")
}

func TestRubyGems_YankRemovesTheVersion(t *testing.T) {
	repo := testutil.SimpleRepo("gems-yank", "rubygems")
	r, _ := setup(repo)

	require.Equal(t, http.StatusOK, publish(t, r, "gems-yank", buildTestGem(t, gemYAML("doomed", "1.0.0"))).Code)
	require.Equal(t, http.StatusOK, publish(t, r, "gems-yank", buildTestGem(t, gemYAML("doomed", "2.0.0"))).Code)

	form := url.Values{"gem_name": {"doomed"}, "version": {"1.0.0"}}
	req := httptest.NewRequest(http.MethodDelete, "/repository/gems-yank/api/v1/gems/yank",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	assert.Equal(t, http.StatusNotFound, get(r, "/repository/gems-yank/gems/doomed-1.0.0.gem").Code)
	info := get(r, "/repository/gems-yank/info/doomed")
	assert.NotContains(t, info.Body.String(), "1.0.0 ")
	assert.Contains(t, info.Body.String(), "2.0.0 ")
}

func TestRubyGems_InfoForUnknownGemIs404(t *testing.T) {
	repo := testutil.SimpleRepo("gems-404", "rubygems")
	r, _ := setup(repo)
	assert.Equal(t, http.StatusNotFound, get(r, "/repository/gems-404/info/nope").Code)
}

func TestRubyGems_PublishRejectsGarbage(t *testing.T) {
	repo := testutil.SimpleRepo("gems-bad", "rubygems")
	r, _ := setup(repo)
	w := publish(t, r, "gems-bad", []byte("not a gem"))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── proxy ─────────────────────────────────────────────────────────────────────

func proxySetup(t *testing.T, upstreamBody map[string]string) (*gin.Engine, formats.Deps, *httptest.Server) {
	t.Helper()
	orig := repoproxy.UpstreamClient
	repoproxy.UpstreamClient = &http.Client{}
	t.Cleanup(func() { repoproxy.UpstreamClient = orig })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := upstreamBody[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	repo := &domain.Repository{
		ID: "repo-gems-proxy", Name: "gems-proxy", Format: domain.RepoFormat("rubygems"),
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": srv.URL},
	}
	r, d := setup(repo)
	return r, d, srv
}

func TestRubyGems_ProxyPassesThroughCompactIndexAndGems(t *testing.T) {
	r, d, _ := proxySetup(t, map[string]string{
		"/versions":             "created_at: 2026-01-01T00:00:00Z\n---\nrails 7.0.0 aaaa\n",
		"/info/rails":           "---\n7.0.0 |checksum:bb\n",
		"/names":                "---\nrails\n",
		"/gems/rails-7.0.0.gem": "gem-bytes",
	})

	for path, want := range map[string]string{
		"/repository/gems-proxy/versions":             "rails 7.0.0 aaaa",
		"/repository/gems-proxy/info/rails":           "7.0.0 |checksum:bb",
		"/repository/gems-proxy/names":                "rails",
		"/repository/gems-proxy/gems/rails-7.0.0.gem": "gem-bytes",
	} {
		w := get(r, path)
		require.Equal(t, http.StatusOK, w.Code, path)
		assert.Contains(t, w.Body.String(), want, path)
	}

	// The cached gem carries real coordinates — the browse UI and any future
	// scanning depend on them (#336's lesson).
	comp, err := findComponent(d, "gems-proxy", "rails")
	require.NoError(t, err)
	assert.Equal(t, "7.0.0", comp.Version)
}

func TestRubyGems_ProxyRejectsPublish(t *testing.T) {
	r, _, _ := proxySetup(t, nil)
	w := publish(t, r, "gems-proxy", []byte("anything"))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func findComponent(d formats.Deps, repo, name string) (*domain.Component, error) {
	page, err := d.Components.Search(nil, domain.SearchParams{Repository: repo, Name: name, Limit: 10}) //nolint:staticcheck // mock ignores ctx
	if err != nil {
		return nil, err
	}
	for i := range page.Items {
		if page.Items[i].Name == name {
			return &page.Items[i], nil
		}
	}
	return nil, fmt.Errorf("component %q not found", name)
}

// The legacy Marshal indexes are not generated; modern gem/bundler fall back
// to the compact index on a clean 404 (verified live — a 405 produced only a
// noisier warning on the way to the same fallback).
func TestRubyGems_LegacySpecsAnswer404(t *testing.T) {
	repo := testutil.SimpleRepo("gems-legacy", "rubygems")
	r, _ := setup(repo)
	for _, p := range []string{"/specs.4.8.gz", "/latest_specs.4.8.gz", "/prerelease_specs.4.8.gz"} {
		assert.Equal(t, http.StatusNotFound, get(r, "/repository/gems-legacy"+p).Code, p)
	}
}
