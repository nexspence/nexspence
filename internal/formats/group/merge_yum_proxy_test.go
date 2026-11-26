package group_test

import (
	"bytes"
	"compress/gzip"
	"encoding/xml"
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
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/yum"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A group merges its members at the canonical repodata names — the names a
// hosted repository serves. An upstream names its documents by the checksum of
// their contents, so a proxy member that relayed the client's path verbatim
// answered 404 for every canonical name and contributed nothing: the upstream's
// packages were invisible in the group.
//
// The proxy resolves the canonical name through the upstream's own repomd.xml,
// and the group's merged index carries the packages of both members.

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
</repomd>`

// upstreamRepodata is a fake upstream that names its documents the way a real
// mirror does.
func upstreamRepodata(t *testing.T) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, err := gw.Write([]byte(upstreamPrimaryXML))
	require.NoError(t, err)
	require.NoError(t, gw.Close())
	primaryGz := buf.Bytes()

	mux := http.NewServeMux()
	mux.HandleFunc("/repodata/repomd.xml", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, upstreamRepomdXML)
	})
	mux.HandleFunc("/repodata/11111111-primary.xml.gz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(primaryGz)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// buildMixedYumGroupEngine builds a group of one hosted and one proxy member.
func buildMixedYumGroupEngine(t *testing.T, groupName, proxyName, remoteURL string) *gin.Engine {
	t.Helper()

	hosted := testutil.SimpleRepo(hostedName(groupName), "yum")
	proxy := &domain.Repository{
		ID: "repo-" + proxyName, Name: proxyName, Format: domain.RepoFormat("yum"),
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remoteURL},
	}
	groupRepo := &domain.Repository{
		ID: "repo-" + groupName, Name: groupName, Format: "yum",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{
			"member_names": []any{hosted.Name, proxy.Name},
		},
	}

	repoRepo := testutil.NewRepoRepo(hosted, proxy, groupRepo)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	yumH := yum.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"yum": yumH})

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		yumH.ServeHTTP(c)
	})
	return r
}

func hostedName(groupName string) string { return groupName + "-hosted" }

// A group of a hosted and a proxy member carries the packages of both.
func TestGroupMerge_YumCarriesTheProxyMembersUpstreamPackages(t *testing.T) {
	srv := upstreamRepodata(t)
	r := buildMixedYumGroupEngine(t, "ymix", "ymix-proxy", srv.URL)

	putRpmTo(t, r, "ymix-hosted", "curl-8.0.0-1.x86_64.rpm")

	w := get(r, "/repository/ymix/repodata/primary.xml")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	body := w.Body.String()
	assert.Contains(t, body, "nginx", "the upstream's package is in the merged index")
	assert.Contains(t, body, "curl", "the hosted member's package is in the merged index")
	assert.Contains(t, body, "Packages/n/nginx-1.25.0-1.el9.x86_64.rpm",
		"the upstream's package keeps its location")
	assert.Contains(t, body, "High performance web server",
		"the upstream's own description survives the merge")

	var doc struct {
		Count    int `xml:"packages,attr"`
		Packages []struct {
			Name string `xml:"name"`
		} `xml:"package"`
	}
	require.NoError(t, xml.Unmarshal(w.Body.Bytes(), &doc))
	assert.Equal(t, 2, doc.Count)
	assert.Equal(t, 2, len(doc.Packages))
}

// The group's own repomd.xml describes the documents the group serves — the
// merged ones — so dnf's checksum verification passes.
func TestGroupMerge_YumRepomdCoversTheMergedPrimary(t *testing.T) {
	srv := upstreamRepodata(t)
	r := buildMixedYumGroupEngine(t, "ymix2", "ymix2-proxy", srv.URL)

	putRpmTo(t, r, "ymix2-hosted", "curl-8.0.0-1.x86_64.rpm")

	repomd := get(r, "/repository/ymix2/repodata/repomd.xml")
	require.Equal(t, http.StatusOK, repomd.Code, repomd.Body.String())

	var doc struct {
		Data []struct {
			Type     string `xml:"type,attr"`
			Location struct {
				Href string `xml:"href,attr"`
			} `xml:"location"`
		} `xml:"data"`
	}
	require.NoError(t, xml.Unmarshal(repomd.Body.Bytes(), &doc))
	require.NotEmpty(t, doc.Data)

	var primaryHref string
	for _, d := range doc.Data {
		if d.Type == "primary" {
			primaryHref = d.Location.Href
		}
	}
	require.NotEmpty(t, primaryHref, "the group advertises a primary document")

	gz := get(r, "/repository/ymix2/"+primaryHref)
	require.Equal(t, http.StatusOK, gz.Code, primaryHref)
	zr, err := gzip.NewReader(bytes.NewReader(gz.Body.Bytes()))
	require.NoError(t, err)
	decompressed, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Contains(t, string(decompressed), "nginx",
		"the compressed document the group serves carries the upstream's packages")
}

// A group whose only member is a proxy still merges what that member holds.
func TestGroupMerge_YumProxyOnlyGroupCarriesTheUpstreamPackages(t *testing.T) {
	srv := upstreamRepodata(t)
	r := buildMixedYumGroupEngine(t, "yproxy", "yproxy-proxy", srv.URL)

	w := get(r, "/repository/yproxy/repodata/primary.xml")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "nginx")
}

// The group's merged document is a union — the same package in both members is
// listed once.
//
// The identity of a package in the merged index is its location, so the hosted
// member has to hold the package under the path the upstream holds it under.
func TestGroupMerge_YumMergedIndexDeduplicatesTheSamePackage(t *testing.T) {
	srv := upstreamRepodata(t)
	r := buildMixedYumGroupEngine(t, "ydup", "ydup-proxy", srv.URL)

	body := "rpm-payload-nginx"
	req := httptest.NewRequest(http.MethodPut,
		"/repository/ydup-hosted/Packages/n/nginx-1.25.0-1.el9.x86_64.rpm",
		strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	merged := get(r, "/repository/ydup/repodata/primary.xml")
	require.Equal(t, http.StatusOK, merged.Code, merged.Body.String())
	assert.Equal(t, 1, strings.Count(merged.Body.String(), "<name>nginx</name>"),
		"a package both members hold is listed once")
}
