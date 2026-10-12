package oci_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Pull gate (#641): a docker/oci repository with formatConfig.pull_block_severity
// refuses a manifest GET/HEAD while the image's latest scan has a finding at or
// above that severity.

const (
	gateScanner   = "scanner-admin"
	ociIndexType  = "application/vnd.oci.image.index.v1+json"
	ociManifestCT = "application/vnd.oci.image.manifest.v1+json"
)

type gateEnv struct {
	r      *gin.Engine
	assets *testutil.AssetRepo
	scans  *testutil.ScanResultRepo
	user   string // username the next request authenticates as
}

func gatedRepo(name string, typ domain.RepoType, severity string) *domain.Repository {
	repo := &domain.Repository{ID: "repo-" + name, Name: name, Format: domain.FormatDocker, Type: typ, Online: true}
	if severity != "" {
		repo.FormatConfig = map[string]any{domain.PullBlockSeverityKey: severity}
	}
	return repo
}

func setupGate(repos ...*domain.Repository) *gateEnv {
	env := &gateEnv{assets: testutil.NewAssetRepo(), scans: testutil.NewScanResultRepo(), user: "testuser"}
	d := formats.Deps{
		Repos:        testutil.NewRepoRepo(repos...),
		Blobs:        testutil.NewBlobStoreRepo(),
		Components:   testutil.NewComponentRepo(),
		Assets:       env.assets,
		BlobStore:    testutil.NewBlobStore(),
		BaseURL:      "http://localhost:8080",
		ScanResults:  env.scans,
		ScanPullUser: gateScanner,
	}
	h := oci.New(d)
	env.r = gin.New()
	env.r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(requestctx.WithUser(c.Request.Context(), "uid-"+env.user, env.user))
		c.Next()
	})
	env.r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return env
}

func (e *gateEnv) push(t *testing.T, repoName, imageName, reference, contentType, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut,
		"/repository/"+repoName+"/v2/"+imageName+"/manifests/"+reference, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "push %s: %s", reference, w.Body.String())
	return w.Header().Get("Docker-Content-Digest")
}

func (e *gateEnv) pull(method, repoName, imageName, reference string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, httptest.NewRequest(method,
		"/repository/"+repoName+"/v2/"+imageName+"/manifests/"+reference, nil))
	return w
}

// scan records a scan of the manifest stored under reference, age ago.
func (e *gateEnv) scan(t *testing.T, repoName, imageName, reference string, status domain.ScanStatus, s domain.ScanSummary, age time.Duration) {
	t.Helper()
	a, err := e.assets.GetByPath(context.Background(), repoName, "/manifests/"+imageName+"/"+reference)
	require.NoError(t, err)
	require.NoError(t, e.scans.Insert(context.Background(), &domain.ScanResultRow{
		ComponentID: a.ComponentID, Scanner: "trivy", Status: status,
		Malicious: s.Malicious, Critical: s.Critical, High: s.High, Medium: s.Medium, Low: s.Low, Unknown: s.Unknown,
		ScannedAt: time.Now().Add(-age),
	}))
}

func deniedMessage(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	require.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	var payload struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload), "body: %s", w.Body.String())
	require.Len(t, payload.Errors, 1)
	assert.Equal(t, "DENIED", payload.Errors[0].Code)
	return payload.Errors[0].Message
}

func TestPullGate_HostedThresholds(t *testing.T) {
	cases := []struct {
		name      string
		threshold string
		summary   domain.ScanSummary
		blocked   bool
		mentions  string
	}{
		{"off serves a critical image", "", domain.ScanSummary{Critical: 5}, false, ""},
		{"clean image passes", "low", domain.ScanSummary{}, false, ""},
		{"below threshold passes", "high", domain.ScanSummary{Medium: 3, Low: 8}, false, ""},
		{"at threshold refused", "high", domain.ScanSummary{High: 2}, true, "2 high vulnerabilities"},
		{"above threshold names the worst", "medium", domain.ScanSummary{Critical: 1, Medium: 4}, true, "1 critical vulnerability"},
		{"negligible/unknown never refused", "low", domain.ScanSummary{Unknown: 12}, false, ""},
		{"malicious refused at critical", "critical", domain.ScanSummary{Malicious: 1}, true, "1 malicious package report"},
		{"malicious threshold ignores CVEs", "malicious", domain.ScanSummary{Critical: 9}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := setupGate(gatedRepo("hosted", domain.TypeHosted, tc.threshold))
			env.push(t, "hosted", "app", "1.0", ociManifestCT, imageManifest(digest("cfg"), digest("layer")))
			env.scan(t, "hosted", "app", "1.0", domain.ScanStatusOK, tc.summary, time.Minute)

			w := env.pull(http.MethodGet, "hosted", "app", "1.0")
			if !tc.blocked {
				assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
				return
			}
			msg := deniedMessage(t, w)
			assert.Contains(t, msg, tc.mentions)
			assert.Contains(t, msg, fmt.Sprintf("%q or higher", tc.threshold))
			assert.Contains(t, msg, `repository "hosted"`)
		})
	}
}

func TestPullGate_HeadRefusedWithoutBody(t *testing.T) {
	env := setupGate(gatedRepo("hosted", domain.TypeHosted, "high"))
	env.push(t, "hosted", "app", "1.0", ociManifestCT, imageManifest(digest("cfg"), digest("layer")))
	env.scan(t, "hosted", "app", "1.0", domain.ScanStatusOK, domain.ScanSummary{Critical: 1}, time.Minute)

	w := env.pull(http.MethodHead, "hosted", "app", "1.0")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Empty(t, w.Body.String())
	assert.Empty(t, w.Header().Get("Docker-Content-Digest"), "a refused manifest must not leak its digest")
}

func TestPullGate_HostedRefusesUnscanned(t *testing.T) {
	env := setupGate(gatedRepo("hosted", domain.TypeHosted, "critical"))
	env.push(t, "hosted", "app", "1.0", ociManifestCT, imageManifest(digest("cfg"), digest("layer")))

	msg := deniedMessage(t, env.pull(http.MethodGet, "hosted", "app", "1.0"))
	assert.Contains(t, msg, "no finished vulnerability scan")

	// A failed scan looked at nothing; it is not a clean one.
	env.scan(t, "hosted", "app", "1.0", domain.ScanStatusFailed, domain.ScanSummary{}, time.Minute)
	deniedMessage(t, env.pull(http.MethodGet, "hosted", "app", "1.0"))

	// The latest scan decides: a newer clean one lets the image through.
	env.scan(t, "hosted", "app", "1.0", domain.ScanStatusOK, domain.ScanSummary{High: 4}, 0)
	assert.Equal(t, http.StatusOK, env.pull(http.MethodGet, "hosted", "app", "1.0").Code)
}

func TestPullGate_ScannerPullsAreExempt(t *testing.T) {
	env := setupGate(gatedRepo("hosted", domain.TypeHosted, "low"))
	env.push(t, "hosted", "app", "1.0", ociManifestCT, imageManifest(digest("cfg"), digest("layer")))

	env.user = gateScanner
	assert.Equal(t, http.StatusOK, env.pull(http.MethodGet, "hosted", "app", "1.0").Code,
		"Trivy reads the image through this API; refusing it would leave the image unscannable")
	env.user = "someone-else"
	assert.Equal(t, http.StatusForbidden, env.pull(http.MethodGet, "hosted", "app", "1.0").Code)
}

func TestPullGate_MissingManifestIsStillUnknown(t *testing.T) {
	env := setupGate(gatedRepo("hosted", domain.TypeHosted, "low"))
	w := env.pull(http.MethodGet, "hosted", "app", "nope")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "MANIFEST_UNKNOWN", dockerErrorCode(t, w.Body.String()))
}

func TestPullGate_DigestJudgedByItsTag(t *testing.T) {
	env := setupGate(gatedRepo("hosted", domain.TypeHosted, "high"))
	dg := env.push(t, "hosted", "app", "1.0", ociManifestCT, imageManifest(digest("cfg"), digest("layer")))

	// Unscanned by tag or by digest alike.
	deniedMessage(t, env.pull(http.MethodGet, "hosted", "app", dg))

	env.scan(t, "hosted", "app", "1.0", domain.ScanStatusOK, domain.ScanSummary{Critical: 2}, time.Minute)
	msg := deniedMessage(t, env.pull(http.MethodGet, "hosted", "app", dg))
	assert.Contains(t, msg, "2 critical")
	assert.Contains(t, msg, "app@"+dg)

	// A second tag on the same manifest scanned clean later: the newest scan of
	// that content decides.
	env.push(t, "hosted", "app", "stable", ociManifestCT, imageManifest(digest("cfg"), digest("layer")))
	env.scan(t, "hosted", "app", "stable", domain.ScanStatusOK, domain.ScanSummary{}, 0)
	assert.Equal(t, http.StatusOK, env.pull(http.MethodGet, "hosted", "app", dg).Code)
}

// A multi-arch pull asks for the index by tag, then one platform manifest by
// digest. The platform manifest has no tag of its own; the index tag's scan
// judges it, unless it was scanned itself.
func TestPullGate_PlatformManifestJudgedByItsIndex(t *testing.T) {
	env := setupGate(gatedRepo("hosted", domain.TypeHosted, "high"))
	amd := imageManifest(digest("cfg-amd64"), digest("layer-amd64"))
	arm := imageManifest(digest("cfg-arm64"), digest("layer-arm64"))
	amdDigest := env.push(t, "hosted", "app", digest(amd), ociManifestCT, amd)
	armDigest := env.push(t, "hosted", "app", digest(arm), ociManifestCT, arm)
	index := fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"manifests":[`+
		`{"mediaType":%q,"digest":%q,"size":1,"platform":{"architecture":"amd64","os":"linux"}},`+
		`{"mediaType":%q,"digest":%q,"size":1,"platform":{"architecture":"arm64","os":"linux"}}]}`,
		ociIndexType, ociManifestCT, amdDigest, ociManifestCT, armDigest)
	env.push(t, "hosted", "app", "2.0", ociIndexType, index)

	// Nothing scanned yet: hosted refuses the platform manifest too.
	deniedMessage(t, env.pull(http.MethodGet, "hosted", "app", amdDigest))

	env.scan(t, "hosted", "app", "2.0", domain.ScanStatusOK, domain.ScanSummary{High: 1}, time.Minute)
	deniedMessage(t, env.pull(http.MethodGet, "hosted", "app", "2.0"))
	deniedMessage(t, env.pull(http.MethodGet, "hosted", "app", amdDigest))

	// A platform manifest scanned on its own is judged by that scan.
	env.scan(t, "hosted", "app", armDigest, domain.ScanStatusOK, domain.ScanSummary{}, 0)
	assert.Equal(t, http.StatusOK, env.pull(http.MethodGet, "hosted", "app", armDigest).Code)
	deniedMessage(t, env.pull(http.MethodGet, "hosted", "app", amdDigest))
}

// A signature or SBOM is not an image Trivy scans; refusing it as unscanned
// would break `cosign verify` against a gated repository.
func TestPullGate_NonImageArtifactNotRefusedAsUnscanned(t *testing.T) {
	env := setupGate(gatedRepo("hosted", domain.TypeHosted, "low"))
	subject := env.push(t, "hosted", "app", "1.0", ociManifestCT, imageManifest(digest("cfg"), digest("layer")))
	sig := referrerManifest(cosignSigArtifactType, subject)
	sigDigest := env.push(t, "hosted", "app", digest(sig), ociManifestCT, sig)

	assert.Equal(t, http.StatusOK, env.pull(http.MethodGet, "hosted", "app", sigDigest).Code)
	deniedMessage(t, env.pull(http.MethodGet, "hosted", "app", "1.0"))
}

func TestPullGate_ProxyLetsUnscannedThroughAndRefusesDirtyCache(t *testing.T) {
	body := imageManifest(digest("cfg"), digest("layer"))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/library/app/manifests/1.0" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", ociManifestCT)
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()
	proxy := gatedRepo("proxy", domain.TypeProxy, "high")
	proxy.ProxyConfig = map[string]any{"remote_url": upstream.URL}
	env := setupGate(proxy)

	// First pull brings the image in; there is nothing to judge it by yet.
	w := env.pull(http.MethodGet, "proxy", "library/app", "1.0")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	// A failed scan is still no finished scan: a proxy does not refuse on it.
	env.scan(t, "proxy", "library/app", "1.0", domain.ScanStatusFailed, domain.ScanSummary{}, time.Minute)
	assert.Equal(t, http.StatusOK, env.pull(http.MethodGet, "proxy", "library/app", "1.0").Code)

	env.scan(t, "proxy", "library/app", "1.0", domain.ScanStatusOK, domain.ScanSummary{High: 3}, 0)
	msg := deniedMessage(t, env.pull(http.MethodGet, "proxy", "library/app", "1.0"))
	assert.Contains(t, msg, `repository "proxy"`)
}
