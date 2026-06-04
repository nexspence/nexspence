package terraform_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/terraform"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func setupDeps(deps formats.Deps) *gin.Engine {
	h := terraform.New(deps)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func noDepsEngine() *gin.Engine {
	return setupDeps(formats.Deps{
		Repos:      testutil.NewRepoRepo(),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	})
}

// TestTerraform_Name verifies the Name() return value.
func TestTerraform_Name(t *testing.T) {
	h := terraform.New(formats.Deps{
		Repos:      testutil.NewRepoRepo(),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	})
	assert.Equal(t, "terraform", h.Name())
}

// TestTerraform_Hosted_ProviderVersions_Empty verifies empty versions list.
func TestTerraform_Hosted_ProviderVersions_Empty(t *testing.T) {
	r := setup(hostedRepo("tf-extra-versions-empty"))
	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-extra-versions-empty/v1/providers/nothing/here/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	versions, ok := body["versions"].([]any)
	require.True(t, ok)
	assert.Empty(t, versions)
}

// TestTerraform_Hosted_ProviderVersions_BadPath checks 400 for malformed path.
func TestTerraform_Hosted_ProviderVersions_BadPath(t *testing.T) {
	r := setup(hostedRepo("tf-extra-vbad"))
	// after TrimPrefix("/v1/providers/") and TrimSuffix("/versions"): "onlyonepart" → SplitN gives 1 part → 400
	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-extra-vbad/v1/providers/onlyonepart/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestTerraform_Hosted_ProviderUpload_BadPath checks 400 for wrong upload part count.
func TestTerraform_Hosted_ProviderUpload_BadPath(t *testing.T) {
	r := setup(hostedRepo("tf-extra-upbad"))
	// only 5 parts after split (missing arch)
	req := httptest.NewRequest(http.MethodPut,
		"/repository/tf-extra-upbad/v1/providers/ns/type/1.0.0/upload/linux", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestTerraform_Hosted_ProviderDownload_HappyPath covers handleProviderDownload (0.0%).
func TestTerraform_Hosted_ProviderDownload_HappyPath(t *testing.T) {
	r := setup(hostedRepo("tf-extra-dl"))

	// Upload first.
	body := []byte("provider-bytes")
	req := httptest.NewRequest(http.MethodPut,
		"/repository/tf-extra-dl/v1/providers/acme/dns/2.0.0/upload/darwin/arm64",
		bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// Download redirect.
	req2 := httptest.NewRequest(http.MethodGet,
		"/repository/tf-extra-dl/v1/providers/acme/dns/2.0.0/download/darwin/arm64", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	assert.Equal(t, "darwin", resp["os"])
	assert.Equal(t, "arm64", resp["arch"])
	assert.Contains(t, resp["download_url"], "/repository/tf-extra-dl/v1/providers/acme/dns/2.0.0/darwin_arm64.zip")
}

// TestTerraform_Hosted_ProviderDownload_NotFound checks 404 when binary is absent.
func TestTerraform_Hosted_ProviderDownload_NotFound(t *testing.T) {
	r := setup(hostedRepo("tf-extra-dlnotfound"))
	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-extra-dlnotfound/v1/providers/acme/dns/9.9.9/download/linux/amd64", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestTerraform_Hosted_ProviderDownload_BadPath checks 400 for wrong part count.
func TestTerraform_Hosted_ProviderDownload_BadPath(t *testing.T) {
	r := setup(hostedRepo("tf-extra-dlbad"))
	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-extra-dlbad/v1/providers/ns/type/1.0.0/download/linux", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestTerraform_Hosted_ProviderUnknownEndpoint hits "unknown provider endpoint" branch.
func TestTerraform_Hosted_ProviderUnknownEndpoint(t *testing.T) {
	r := setup(hostedRepo("tf-extra-provunk"))
	// POST is not PUT/GET, so falls to the catch-all 404.
	req := httptest.NewRequest(http.MethodPost,
		"/repository/tf-extra-provunk/v1/providers/ns/myprov/something", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestTerraform_Hosted_ModuleVersions covers handleModuleVersions (0.0% coverage).
func TestTerraform_Hosted_ModuleVersions(t *testing.T) {
	r := setup(hostedRepo("tf-extra-modv"))

	// Upload a module first.
	body := []byte("module-bytes")
	req := httptest.NewRequest(http.MethodPut,
		"/repository/tf-extra-modv/v1/modules/myns/mymod/aws/1.2.3",
		bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	// List versions.
	req2 := httptest.NewRequest(http.MethodGet,
		"/repository/tf-extra-modv/v1/modules/myns/mymod/aws/versions", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)

	var body2 map[string]any
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&body2))
	modules, ok := body2["modules"].([]any)
	require.True(t, ok)
	assert.NotEmpty(t, modules)
}

// TestTerraform_Hosted_ModuleVersions_BadPath checks 400 for too few path segments.
func TestTerraform_Hosted_ModuleVersions_BadPath(t *testing.T) {
	r := setup(hostedRepo("tf-extra-modvbad"))
	// after TrimPrefix/TrimSuffix: "ns" → SplitN(3) gives 1 part → 400
	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-extra-modvbad/v1/modules/ns/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestTerraform_Hosted_ModuleUpload_BadPath checks 400 for too few segments.
func TestTerraform_Hosted_ModuleUpload_BadPath(t *testing.T) {
	r := setup(hostedRepo("tf-extra-modupbad"))
	// only 3 parts (ns/mymod/aws), need 4
	req := httptest.NewRequest(http.MethodPut,
		"/repository/tf-extra-modupbad/v1/modules/ns/mymod/aws", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestTerraform_Hosted_ModuleDownload_BadPath checks 400 when /download but wrong parts.
func TestTerraform_Hosted_ModuleDownload_BadPath(t *testing.T) {
	r := setup(hostedRepo("tf-extra-moddlbad"))
	// after TrimPrefix/TrimSuffix: "ns/mymod" → SplitN(4) gives 2 parts → 400
	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-extra-moddlbad/v1/modules/ns/mymod/download", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestTerraform_Hosted_ModuleUnknownEndpoint hits the "unknown module endpoint" branch.
func TestTerraform_Hosted_ModuleUnknownEndpoint(t *testing.T) {
	r := setup(hostedRepo("tf-extra-modunk"))
	// DELETE is not PUT/GET, falls to the 404 catch-all.
	req := httptest.NewRequest(http.MethodDelete,
		"/repository/tf-extra-modunk/v1/modules/ns/mymod/aws/1.0.0", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestTerraform_Proxy_NoRemoteURL checks the 400 branch when proxy repo has no remote_url.
func TestTerraform_Proxy_NoRemoteURL(t *testing.T) {
	repo := &domain.Repository{
		ID:     "tf-bad-proxy",
		Name:   "tf-bad-proxy",
		Format: "terraform",
		Type:   domain.TypeProxy,
		Online: true,
		// No ProxyConfig → RemoteURL returns error.
	}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	r := setupDeps(d)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-bad-proxy/v1/providers/hashicorp/aws/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestTerraform_Proxy_UpstreamNon200 covers the resp.StatusCode != 200 branch.
func TestTerraform_Proxy_UpstreamNon200(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	r := setup(proxyRepo("tf-proxy-extra-404", upstream.URL))
	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-proxy-extra-404/v1/providers/hashicorp/aws/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestTerraform_Proxy_XTerraformGet covers the X-Terraform-Get forwarding branch.
func TestTerraform_Proxy_XTerraformGet(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Terraform-Get", "https://example.com/module.tar.gz")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	r := setup(proxyRepo("tf-proxy-extra-xget", upstream.URL))
	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-proxy-extra-xget/v1/modules/hashicorp/consul/aws/0.1.0/download", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Contains(t, w.Header().Get("X-Terraform-Get"), "example.com")
}

// TestTerraform_Proxy_BadJSON exercises the json.Decode error branch in serveProxy.
func TestTerraform_Proxy_BadJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{not valid json}"))
	}))
	defer upstream.Close()

	r := setup(proxyRepo("tf-proxy-extra-badjson", upstream.URL))
	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-proxy-extra-badjson/v1/providers/hashicorp/aws/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

// TestTerraform_Proxy_DownloadRedirectCached covers the /v1/providers/+/download/ binary cache path.
func TestTerraform_Proxy_DownloadRedirectCached(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write([]byte("zip-binary-data"))
	}))
	defer upstream.Close()

	r := setup(proxyRepo("tf-proxy-extra-bin", upstream.URL))
	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-proxy-extra-bin/v1/providers/hashicorp/aws/5.0.0/download/linux/amd64", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// repoproxy streams the upstream response; any non-5xx is acceptable.
	assert.Less(t, w.Code, 500)
}

// TestTerraform_Proxy_RewriteDownloadURL checks that download_url is rewritten to the local base.
func TestTerraform_Proxy_RewriteDownloadURL(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"os":           "linux",
			"arch":         "amd64",
			"download_url": "https://releases.hashicorp.com/terraform-provider-aws/5.0.0/terraform-provider-aws_5.0.0_linux_amd64.zip",
		})
	}))
	defer upstream.Close()

	r := setup(proxyRepo("tf-proxy-extra-rewrite", upstream.URL))
	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-proxy-extra-rewrite/v1/providers/hashicorp/aws/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	dl, _ := body["download_url"].(string)
	assert.Contains(t, dl, "localhost:8080")
	assert.NotContains(t, dl, "releases.hashicorp.com")
}

// TestTerraform_ServeHTTP_RepoNil verifies nil repo falls through to hosted provider path.
func TestTerraform_ServeHTTP_RepoNil(t *testing.T) {
	// NewRepoRepo with no repos → Get returns nil, nil
	r := noDepsEngine()
	req := httptest.NewRequest(http.MethodGet,
		"/repository/missing-repo/v1/providers/ns/mytype/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// component repo is empty → 200 with empty versions list
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestTerraform_Proxy_ModuleTarGz exercises the module .tar.gz binary cache path.
func TestTerraform_Proxy_ModuleTarGz(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-tar")
		_, _ = w.Write([]byte("fake-tarball-bytes"))
	}))
	defer upstream.Close()

	r := setup(proxyRepo("tf-proxy-extra-mod", upstream.URL))
	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-proxy-extra-mod/v1/modules/hashicorp/consul/aws/0.1.0.tar.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Less(t, w.Code, 500)
}
