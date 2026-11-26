package yum_test

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/yum"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A real mirror names its metadata documents by the checksum of their
// contents — "repodata/707889cc…-primary.xml.gz" — and not by the canonical
// names a hosted repository serves. A proxy that relayed the client's path
// verbatim therefore answered 404 for every canonical name, and a group that
// merges its members at those names saw nothing of the upstream's packages.
//
// The proxy resolves the canonical name through upstream's own repomd.xml.

const upstreamPrimaryXML = `<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" packages="1">
  <package type="rpm">
    <name>nginx</name>
    <arch>x86_64</arch>
    <version epoch="0" ver="1.25.0" rel="1.el9"/>
    <checksum type="sha256" pkgid="YES">1111111111111111111111111111111111111111111111111111111111111111</checksum>
    <summary>High performance web server</summary>
    <description>The nginx reverse proxy and web server</description>
    <url>http://nginx.org/</url>
    <time file="1700000000" build="1699999999"/>
    <size package="1000" installed="2000" archive="3000"/>
    <location href="Packages/n/nginx-1.25.0-1.el9.x86_64.rpm"/>
  </package>
</metadata>`

const upstreamRepomdXML = `<?xml version="1.0" encoding="UTF-8"?>
<repomd xmlns="http://linux.duke.edu/metadata/repo">
  <revision>9.9</revision>
  <data type="primary">
    <location href="repodata/11111111-primary.xml.gz"/>
    <checksum type="sha256">2222222222222222222222222222222222222222222222222222222222222222</checksum>
    <timestamp>1700000000</timestamp>
    <size>320</size>
  </data>
  <data type="filelists">
    <location href="repodata/33333333-filelists.xml.gz"/>
    <checksum type="sha256">4444444444444444444444444444444444444444444444444444444444444444</checksum>
    <timestamp>1700000000</timestamp>
    <size>64</size>
  </data>
</repomd>`

func gzipBytes(t *testing.T, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, err := gw.Write([]byte(data))
	require.NoError(t, err)
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

// upstreamWithRepomd is a fake upstream that names its documents the way a
// real mirror does.
func upstreamWithRepomd(t *testing.T) *httptest.Server {
	t.Helper()
	primaryGz := gzipBytes(t, upstreamPrimaryXML)
	mux := http.NewServeMux()
	mux.HandleFunc("/repodata/repomd.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, upstreamRepomdXML)
	})
	mux.HandleFunc("/repodata/11111111-primary.xml.gz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-gzip")
		_, _ = w.Write(primaryGz)
	})
	mux.HandleFunc("/Packages/n/nginx-1.25.0-1.el9.x86_64.rpm", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "rpm-bytes")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// upstreamWithoutRepomd serves the canonical names itself and has no
// repomd.xml at all.
func upstreamWithoutRepomd(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repodata/primary.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, upstreamPrimaryXML)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func setupYumProxyWith(t *testing.T, remoteURL string) *gin.Engine {
	t.Helper()
	repo := &domain.Repository{
		ID: "repo-yum-proxy", Name: "yum-proxy", Format: domain.RepoFormat("yum"),
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remoteURL},
	}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := yum.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func getYum(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// The canonical name is answered with the document upstream publishes it as —
// decompressed, because that is what the name asks for.
func TestYum_Proxy_ServesTheCanonicalRepodataThroughUpstreamRepomd(t *testing.T) {
	srv := upstreamWithRepomd(t)
	r := setupYumProxyWith(t, srv.URL)

	w := getYum(t, r, "/repository/yum-proxy/repodata/primary.xml")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Type"), "application/xml")
	assert.Equal(t, upstreamPrimaryXML, w.Body.String(),
		"the canonical name is answered with the upstream's own document, decompressed")
}

// The compressed name is answered with the upstream's compressed bytes.
func TestYum_Proxy_ServesTheCanonicalRepodataCompressed(t *testing.T) {
	srv := upstreamWithRepomd(t)
	r := setupYumProxyWith(t, srv.URL)

	w := getYum(t, r, "/repository/yum-proxy/repodata/primary.xml.gz")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "application/x-gzip", w.Header().Get("Content-Type"))

	zr, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	require.NoError(t, err)
	decompressed, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Equal(t, upstreamPrimaryXML, string(decompressed))
}

// The checksum-prefixed name upstream publishes is still relayed verbatim — a
// client that read the proxy's own repomd.xml asks for it.
func TestYum_Proxy_RelaysTheChecksumPrefixedNameVerbatim(t *testing.T) {
	srv := upstreamWithRepomd(t)
	r := setupYumProxyWith(t, srv.URL)

	w := getYum(t, r, "/repository/yum-proxy/repodata/11111111-primary.xml.gz")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "<metadata",
		"the upstream's own compressed document is relayed as it came")
}

// An upstream without a repomd.xml is served the canonical path as it is,
// which is the right answer for an upstream that publishes those names.
func TestYum_Proxy_FallsBackToTheCanonicalPathWithoutUpstreamRepomd(t *testing.T) {
	srv := upstreamWithoutRepomd(t)
	r := setupYumProxyWith(t, srv.URL)

	w := getYum(t, r, "/repository/yum-proxy/repodata/primary.xml")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "nginx")
}

// repomd.xml itself is the index that names the documents — it is the
// upstream's own document, not a merged one.
func TestYum_Proxy_RelaysTheUpstreamRepomd(t *testing.T) {
	srv := upstreamWithRepomd(t)
	r := setupYumProxyWith(t, srv.URL)

	w := getYum(t, r, "/repository/yum-proxy/repodata/repomd.xml")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "11111111-primary.xml.gz")
}

// A proxy stays read-only: the canonical name is a read path like any other.
func TestYum_Proxy_RefusesWritesOnTheCanonicalName(t *testing.T) {
	srv := upstreamWithRepomd(t)
	r := setupYumProxyWith(t, srv.URL)

	req := httptest.NewRequest(http.MethodPut,
		"/repository/yum-proxy/repodata/primary.xml", strings.NewReader("x"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// The resolution reads the upstream's repomd.xml — once per canonical
// document — and the document it names is the one that is fetched.
func TestYum_Proxy_ResolvesTheDocumentThroughTheUpstreamRepomd(t *testing.T) {
	var repomdHits int
	primaryGz := gzipBytes(t, upstreamPrimaryXML)
	mux := http.NewServeMux()
	mux.HandleFunc("/repodata/repomd.xml", func(w http.ResponseWriter, _ *http.Request) {
		repomdHits++
		_, _ = io.WriteString(w, upstreamRepomdXML)
	})
	mux.HandleFunc("/repodata/11111111-primary.xml.gz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(primaryGz)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	r := setupYumProxyWith(t, srv.URL)
	w := getYum(t, r, "/repository/yum-proxy/repodata/primary.xml")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 1, repomdHits, "the upstream's repomd.xml is read once per request")
	assert.Contains(t, w.Body.String(), "nginx")
}

// The repoproxy package serves the bytes of the upstream's document under the
// name the client asked for, in the flavor it asked for.
func TestYum_Proxy_ServesTheFlavorTheClientAskedFor(t *testing.T) {
	srv := upstreamWithRepomd(t)
	r := setupYumProxyWith(t, srv.URL)

	plain := getYum(t, r, "/repository/yum-proxy/repodata/primary.xml")
	compressed := getYum(t, r, "/repository/yum-proxy/repodata/primary.xml.gz")

	require.Equal(t, http.StatusOK, plain.Code)
	require.Equal(t, http.StatusOK, compressed.Code)
	assert.False(t, strings.HasPrefix(plain.Body.String(), "\x1f\x8b"),
		"the plain name is answered with plain XML")
	assert.True(t, strings.HasPrefix(compressed.Body.String(), "\x1f\x8b"),
		"the compressed name is answered with compressed bytes")
}

// A upstream that names no such document is relayed the canonical path, which
// answers 404 when the upstream does not have it either.
func TestYum_Proxy_UnknownDocumentFallsBackToTheCanonicalPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repodata/repomd.xml", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<repomd xmlns="http://linux.duke.edu/metadata/repo"><revision>1</revision></repomd>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	r := setupYumProxyWith(t, srv.URL)
	w := getYum(t, r, "/repository/yum-proxy/repodata/other.xml")
	assert.Equal(t, http.StatusNotFound, w.Code, fmt.Sprintf("body: %s", w.Body.String()))
}
