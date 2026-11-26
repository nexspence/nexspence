package terraform_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// The Terraform registry protocol, not a lookalike: the client parses the
// download document, verifies the checksum document against a detached
// signature made by a published key, and refuses the package when any of it
// is missing (#839).

// setupSigned builds a handler whose Terraform signing key is keyPath.
func setupSigned(repo *domain.Repository, keyPath string) *gin.Engine {
	return setupDeps(formats.Deps{
		Repos:               testutil.NewRepoRepo(repo),
		Blobs:               testutil.NewBlobStoreRepo(),
		Components:          testutil.NewComponentRepo(),
		Assets:              testutil.NewAssetRepo(),
		BlobStore:           testutil.NewBlobStore(),
		BaseURL:             "http://localhost:8080",
		TerraformSigningKey: keyPath,
	})
}

// uploadProvider stores the binary of one platform of a provider version.
func uploadProvider(t *testing.T, r *gin.Engine, repo, ns, typ, ver, osName, arch string, content []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut,
		"/repository/"+repo+"/v1/providers/"+ns+"/"+typ+"/"+ver+"/upload/"+osName+"/"+arch,
		bytes.NewReader(content))
	req.ContentLength = int64(len(content))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}

// get fetches a path from the repository and returns the recorder.
func get(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// digestOf is the hex SHA-256 of the stored bytes — the digest the protocol
// document has to carry.
func digestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// The download document is the protocol's document: the client reads the
// digest from "shasum", follows the checksum document and the signature, and
// verifies the signature with the published key.
func TestTerraform_ProviderDownload_AnswersTheProtocolDocument(t *testing.T) {
	repo := hostedRepo("tf-sum-download")
	r := setupSigned(repo, armoredPrivateKey(t))

	content := []byte("provider-zip-bytes")
	uploadProvider(t, r, repo.Name, "acme", "widget", "1.0.0", "linux", "amd64", content)

	w := get(t, r, "/repository/"+repo.Name+"/v1/providers/acme/widget/1.0.0/download/linux/amd64")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var doc map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&doc))

	assert.Equal(t, digestOf(content), doc["shasum"],
		"the protocol names the digest shasum")
	assert.NotContains(t, doc, "sha256_sum",
		"sha256_sum is not a protocol field: the client never reads it")
	assert.Equal(t, "terraform-provider-widget_1.0.0_linux_amd64.zip", doc["filename"])
	assert.Equal(t, "/repository/"+repo.Name+"/v1/providers/acme/widget/1.0.0/linux_amd64.zip",
		doc["download_url"])
	assert.Equal(t, "/repository/"+repo.Name+"/v1/providers/acme/widget/1.0.0/shasums",
		doc["shasums_url"])
	assert.Equal(t, "/repository/"+repo.Name+"/v1/providers/acme/widget/1.0.0/shasums.sig",
		doc["shasums_signature_url"])

	keys, ok := doc["signing_keys"].(map[string]any)
	require.True(t, ok, "the document must publish the signing key")
	list, ok := keys["gpg_public_keys"].([]any)
	require.True(t, ok)
	require.Len(t, list, 1)
	entry, ok := list[0].(map[string]any)
	require.True(t, ok)
	armored, ok := entry["ascii_armor"].(string)
	require.True(t, ok)
	assert.Contains(t, armored, "BEGIN PGP PUBLIC KEY BLOCK")

	// Every URL the client is told to follow is root-relative: the client
	// resolves it against the request it is answering.
	for _, key := range []string{"download_url", "shasums_url", "shasums_signature_url"} {
		value, _ := doc[key].(string)
		assert.True(t, strings.HasPrefix(value, "/"), "%s must be root-relative, got %q", key, value)
		assert.NotContains(t, value, "http")
	}
}

// The checksum document lists every platform of the version: the client looks
// its own file up in it by name and compares the digest.
func TestTerraform_ProviderSums_ListsEveryPlatformOfTheVersion(t *testing.T) {
	repo := hostedRepo("tf-sum-list")
	r := setupSigned(repo, armoredPrivateKey(t))

	linux := []byte("linux-provider-bytes")
	darwin := []byte("darwin-provider-bytes")
	uploadProvider(t, r, repo.Name, "acme", "widget", "1.0.0", "linux", "amd64", linux)
	uploadProvider(t, r, repo.Name, "acme", "widget", "1.0.0", "darwin", "arm64", darwin)

	w := get(t, r, "/repository/"+repo.Name+"/v1/providers/acme/widget/1.0.0/shasums")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")

	body := w.Body.String()
	require.True(t, strings.HasSuffix(body, "\n"), "the document ends with a newline")
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	require.Len(t, lines, 2, "one line per platform")

	// The lines are sorted by file name, and carry the digest of the file.
	assert.Equal(t, digestOf(darwin)+"  terraform-provider-widget_1.0.0_darwin_arm64.zip", lines[0])
	assert.Equal(t, digestOf(linux)+"  terraform-provider-widget_1.0.0_linux_amd64.zip", lines[1])

	// The file name in the document is the one the download document names:
	// the client matches the package it downloads against it.
	dl := get(t, r, "/repository/"+repo.Name+"/v1/providers/acme/widget/1.0.0/download/linux/amd64")
	require.Equal(t, http.StatusOK, dl.Code, dl.Body.String())
	var doc map[string]any
	require.NoError(t, json.NewDecoder(dl.Body).Decode(&doc))
	assert.Equal(t, doc["filename"], strings.Fields(lines[1])[1])
}

// The signature must be the same bytes every time: a signature carries the
// moment it was made, so signing on every request would hand the client a
// signature that does not match the document it fetched. The signature has to
// verify against the published key — that is the check the client makes.
func TestTerraform_ProviderSumsSignature_IsStableAndVerifies(t *testing.T) {
	repo := hostedRepo("tf-sum-signature")
	r := setupSigned(repo, armoredPrivateKey(t))

	uploadProvider(t, r, repo.Name, "acme", "widget", "1.0.0", "linux", "amd64",
		[]byte("provider-zip-bytes"))

	doc := get(t, r, "/repository/"+repo.Name+"/v1/providers/acme/widget/1.0.0/shasums")
	require.Equal(t, http.StatusOK, doc.Code, doc.Body.String())

	first := get(t, r, "/repository/"+repo.Name+"/v1/providers/acme/widget/1.0.0/shasums.sig")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	second := get(t, r, "/repository/"+repo.Name+"/v1/providers/acme/widget/1.0.0/shasums.sig")
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())

	assert.Equal(t, first.Body.String(), second.Body.String(),
		"the signature of one document must not change between requests")
	// The signature is binary: the client verifies it with
	// openpgp.CheckDetachedSignature, which rejects the armored form.
	assert.NotContains(t, first.Body.String(), "BEGIN PGP SIGNATURE")

	// The published key verifies the served signature over the served
	// document.
	dl := get(t, r, "/repository/"+repo.Name+"/v1/providers/acme/widget/1.0.0/download/linux/amd64")
	require.Equal(t, http.StatusOK, dl.Code, dl.Body.String())
	var docBody map[string]any
	require.NoError(t, json.NewDecoder(dl.Body).Decode(&docBody))
	keys := docBody["signing_keys"].(map[string]any)
	list := keys["gpg_public_keys"].([]any)
	entry := list[0].(map[string]any)
	armored := entry["ascii_armor"].(string)

	keyring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	require.NoError(t, err)
	verified, err := openpgp.CheckDetachedSignature(keyring,
		bytes.NewReader(doc.Body.Bytes()), bytes.NewReader(first.Body.Bytes()), nil)
	require.NoError(t, err)
	require.NotNil(t, verified)
}

// Without a signing key the registry cannot serve an installable provider.
// The digest is still served — the client needs it — and the endpoint the
// operator looks at names the setting that is missing.
func TestTerraform_ProviderSums_WithoutSigningKey_NamesTheMissingSetting(t *testing.T) {
	repo := hostedRepo("tf-sum-nokey")
	r := setup(repo)

	content := []byte("provider-zip-bytes")
	uploadProvider(t, r, repo.Name, "acme", "widget", "1.0.0", "linux", "amd64", content)

	dl := get(t, r, "/repository/"+repo.Name+"/v1/providers/acme/widget/1.0.0/download/linux/amd64")
	require.Equal(t, http.StatusOK, dl.Code, dl.Body.String())
	var doc map[string]any
	require.NoError(t, json.NewDecoder(dl.Body).Decode(&doc))
	assert.Equal(t, digestOf(content), doc["shasum"])
	assert.NotContains(t, doc, "shasums_url",
		"without a signer there is no signed document to point at")

	w := get(t, r, "/repository/"+repo.Name+"/v1/providers/acme/widget/1.0.0/shasums")
	assert.Equal(t, http.StatusNotImplemented, w.Code)
	assert.Contains(t, w.Body.String(), "terraform.signing_key",
		"the answer must name the setting the operator has to fill in")
}

// A key that cannot be read is not silently ignored: the registry would look
// installed while every provider install failed.
func TestTerraform_ProviderDownload_BrokenSigningKey_ServesTheError(t *testing.T) {
	repo := hostedRepo("tf-sum-brokenkey")
	r := setupSigned(repo, "/nonexistent/terraform-signing-key.asc")

	uploadProvider(t, r, repo.Name, "acme", "widget", "1.0.0", "linux", "amd64",
		[]byte("provider-zip-bytes"))

	w := get(t, r, "/repository/"+repo.Name+"/v1/providers/acme/widget/1.0.0/download/linux/amd64")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "terraform-signing-key.asc")
}

// The version list has to name the platforms a version exists for: the
// client refuses a provider whose platform the list does not mention.
func TestTerraform_ProviderVersions_ListsThePlatformsOfAVersion(t *testing.T) {
	repo := hostedRepo("tf-versions-platforms")
	r := setup(repo)

	uploadProvider(t, r, repo.Name, "acme", "widget", "1.0.0", "linux", "amd64",
		[]byte("provider-zip-bytes"))

	w := get(t, r, "/repository/"+repo.Name+"/v1/providers/acme/widget/versions")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	versions, ok := body["versions"].([]any)
	require.True(t, ok, "versions must be an array")
	require.Len(t, versions, 1)
	platforms, ok := versions[0].(map[string]any)["platforms"].([]any)
	require.True(t, ok, "platforms must be an array, not null")
	require.Len(t, platforms, 1)
	assert.Equal(t, map[string]any{"os": "linux", "arch": "amd64"}, platforms[0])

	// A second platform of the same version is listed as well: the component
	// of a version holds the file of every platform.
	uploadProvider(t, r, repo.Name, "acme", "widget", "1.0.0", "darwin", "arm64",
		[]byte("darwin-provider-bytes"))

	w = get(t, r, "/repository/"+repo.Name+"/v1/providers/acme/widget/versions")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	versions, _ = body["versions"].([]any)
	require.Len(t, versions, 1)
	platforms, ok = versions[0].(map[string]any)["platforms"].([]any)
	require.True(t, ok)
	require.Len(t, platforms, 2)
	assert.Equal(t, map[string]any{"os": "darwin", "arch": "arm64"}, platforms[0])
	assert.Equal(t, map[string]any{"os": "linux", "arch": "amd64"}, platforms[1])
}

// The upstream answers a module download with 204 and the archive URL in the
// X-Terraform-Get header. The header was dropped on the way, so the client
// never learned where the archive is.
func TestTerraform_Proxy_ForwardsTerraformGetOnA204Answer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Terraform-Get",
			"git::https://github.com/acme/terraform-widget-aws?ref=1.0.0")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	r := setup(proxyRepo("tf-proxy-xget-204", upstream.URL))
	w := get(t, r, "/repository/tf-proxy-xget-204/v1/modules/acme/widget/aws/1.0.0/download")

	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	location := w.Header().Get("X-Terraform-Get")
	require.NotEmpty(t, location, "the header must survive the 204 answer")
	// A module source is a git URL, and the client fetches it itself: there
	// is no host of this instance to point it at.
	assert.Equal(t, "git::https://github.com/acme/terraform-widget-aws?ref=1.0.0", location)
}

// An archive this instance serves is re-rooted at the instance: the client
// fetches it through the proxy, which is the point of the proxy.
func TestTerraform_Proxy_RerootsAnArchiveItServes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Terraform-Get",
			"https://registry.terraform.io/v1/modules/acme/widget/aws/1.0.0.tar.gz")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	r := setup(proxyRepo("tf-proxy-xget-targz", upstream.URL))
	w := get(t, r, "/repository/tf-proxy-xget-targz/v1/modules/acme/widget/aws/1.0.0/download")

	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, "/repository/tf-proxy-xget-targz/v1/modules/acme/widget/aws/1.0.0.tar.gz",
		w.Header().Get("X-Terraform-Get"))
}
