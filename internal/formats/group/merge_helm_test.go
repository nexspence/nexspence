package group_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/helm"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Hosted Helm answers 200 on /index.yaml even when empty. Without a
// GroupIndexMerger that 200 hid every later member — the same #99 shadowing
// pypi/maven already fixed. Two hosted members must both appear in the group
// catalog, which is what `helm search` / `helm pull --repo <group>` read.
func TestGroupMerge_HelmEndToEnd(t *testing.T) {
	m1 := testutil.SimpleRepo("helm-hosted", "helm")
	m2 := testutil.SimpleRepo("helm-proxy", "helm")
	g := &domain.Repository{
		ID: "repo-helm", Name: "helm", Format: "helm",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []interface{}{"helm-hosted", "helm-proxy"}},
	}

	repoRepo := testutil.NewRepoRepo(m1, m2, g)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	helmH := helm.New(d)
	registry := map[string]formats.FormatHandler{"helm": helmH}
	groupH := group.New(d, registry)

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		helmH.ServeHTTP(c)
	})

	putChart := func(repoName, filename, content string) {
		req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+"/"+filename,
			strings.NewReader(content))
		req.Header.Set("Content-Type", "application/x-tar")
		req.ContentLength = int64(len(content))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Contains(t, []int{http.StatusCreated, http.StatusOK}, w.Code, w.Body.String())
	}
	putChart("helm-hosted", "widget-1.2.3.tgz", "hosted-chart")
	putChart("helm-proxy", "ingress-4.0.0.tgz", "proxy-chart")

	w := get(r, "/repository/helm/index.yaml")
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "helm-hosted,helm-proxy", w.Header().Get("X-Nexspence-Source"))

	var doc struct {
		Entries map[string][]map[string]any `yaml:"entries"`
	}
	require.NoError(t, yaml.Unmarshal(w.Body.Bytes(), &doc))
	require.Contains(t, doc.Entries, "widget")
	require.Contains(t, doc.Entries, "ingress", "proxy member must not be shadowed by hosted index.yaml 200")

	urls, _ := doc.Entries["widget"][0]["urls"].([]any)
	require.NotEmpty(t, urls)
	assert.Equal(t, "http://localhost:8080/repository/helm/widget-1.2.3.tgz", urls[0])
}

func helmProxyRepo(name, remote string) *domain.Repository {
	return &domain.Repository{
		ID: name, Name: name, Format: "helm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remote},
	}
}

// A member that 403s on a foreign tarball must not hide a later member that
// actually has the chart. After the pull, the tarball is cached on the member
// that served it.
func TestGroupMerge_HelmTarballSkipsForbiddenMember(t *testing.T) {
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.yaml" {
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write([]byte("apiVersion: v1\nentries: {}\n"))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("denied"))
	}))
	defer denied.Close()

	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/ingress-4.0.0.tgz" {
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write([]byte("ingress-bytes"))
			return
		}
		http.NotFound(w, r)
	}))
	defer releases.Close()

	okRemote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte("apiVersion: v1\n" +
			"entries:\n" +
			"  ingress:\n" +
			"    - name: ingress\n" +
			"      version: \"4.0.0\"\n" +
			"      urls:\n" +
			"        - " + releases.URL + "/releases/ingress-4.0.0.tgz\n"))
	}))
	defer okRemote.Close()

	hosted := testutil.SimpleRepo("helm-hosted", "helm")
	deniedRepo := helmProxyRepo("helm-remote-bitnami", denied.URL)
	okRepo := helmProxyRepo("helm-remote-ok", okRemote.URL)
	g := &domain.Repository{
		ID: "repo-helm", Name: "helm", Format: "helm",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []interface{}{"helm-hosted", "helm-remote-bitnami", "helm-remote-ok"}},
	}

	repoRepo := testutil.NewRepoRepo(hosted, deniedRepo, okRepo, g)
	comps := testutil.NewComponentRepo()
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: comps,
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	helmH := helm.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"helm": helmH})

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		helmH.ServeHTTP(c)
	})

	idx := get(r, "/repository/helm/index.yaml")
	require.Equal(t, http.StatusOK, idx.Code, idx.Body.String())
	assert.Contains(t, idx.Body.String(), "ingress")
	assert.Contains(t, idx.Body.String(), "/repository/helm/ingress-4.0.0.tgz")

	w := get(r, "/repository/helm/ingress-4.0.0.tgz")
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "ingress-bytes", w.Body.String())
	assert.Equal(t, "helm-remote-ok", w.Header().Get("X-Nexspence-Source"))

	page, err := comps.List(t.Context(), "helm-remote-ok", 100, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "tarball must be cached on the member that served it")
	assert.Equal(t, "ingress", page.Items[0].Name)
}

// A member whose index.yaml cannot be read either — a private remote answering
// 403 to everything — is the case where the member cannot tell "not mine" from
// "not allowed". During fan-out that 403 still has to read as a miss, or the
// group stops at the first misconfigured member and the chart disappears.
func TestGroupMerge_HelmTarballSkipsUnreadableMember(t *testing.T) {
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("denied"))
	}))
	defer denied.Close()

	okRemote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write([]byte("apiVersion: v1\n" +
				"entries:\n" +
				"  ingress:\n" +
				"    - name: ingress\n" +
				"      version: \"4.0.0\"\n" +
				"      urls:\n" +
				"        - ingress-4.0.0.tgz\n"))
		case "/ingress-4.0.0.tgz":
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write([]byte("ingress-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer okRemote.Close()

	deniedRepo := helmProxyRepo("helm-remote-private", denied.URL)
	okRepo := helmProxyRepo("helm-remote-ok", okRemote.URL)
	g := &domain.Repository{
		ID: "repo-helm", Name: "helm", Format: "helm",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []interface{}{"helm-remote-private", "helm-remote-ok"}},
	}

	repoRepo := testutil.NewRepoRepo(deniedRepo, okRepo, g)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	helmH := helm.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"helm": helmH})

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		helmH.ServeHTTP(c)
	})

	w := get(r, "/repository/helm/ingress-4.0.0.tgz")
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "ingress-bytes", w.Body.String())
	assert.Equal(t, "helm-remote-ok", w.Header().Get("X-Nexspence-Source"))

	// Addressed directly, the same member reports the 403 it got.
	direct := get(r, "/repository/helm-remote-private/ingress-4.0.0.tgz")
	assert.Equal(t, http.StatusForbidden, direct.Code, direct.Body.String())
}
