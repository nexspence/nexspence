package oci_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

const (
	cosignSigArtifactType = "application/vnd.dev.cosign.artifact.sig.v1+json"
	sbomArtifactType      = "application/spdx+json"
	ociIndexMediaType     = "application/vnd.oci.image.index.v1+json"
)

// referrerManifest is a manifest that points at another one through its subject
// descriptor — how cosign attaches a signature and how an SBOM attaches to a chart.
func referrerManifest(artifactType, subjectDigest string) string {
	return fmt.Sprintf(`{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "artifactType": %q,
  "config": {"mediaType": "application/vnd.oci.empty.v1+json", "digest": "sha256:ee", "size": 2},
  "layers": [{"mediaType": "application/octet-stream", "digest": "sha256:ff", "size": 7}],
  "subject": {"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": %q, "size": 100},
  "annotations": {"org.opencontainers.image.created": "2026-08-04T00:00:00Z"}
}`, artifactType, subjectDigest)
}

// pushManifestBody pushes one manifest and returns its digest.
func pushManifestBody(t *testing.T, r *gin.Engine, repoName, imageName, reference, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut,
		"/repository/"+repoName+"/v2/"+imageName+"/manifests/"+reference, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "manifest push should succeed")
	dgst := w.Header().Get("Docker-Content-Digest")
	require.Equal(t, digest(body), dgst)
	return dgst
}

// referrersIndex is the decoded referrers response.
type referrersIndex struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Manifests     json.RawMessage `json:"manifests"`
}

type referrersDescriptor struct {
	MediaType    string            `json:"mediaType"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	ArtifactType string            `json:"artifactType"`
	Annotations  map[string]string `json:"annotations"`
}

// getReferrers issues GET /v2/<image>/referrers/<digest> and decodes the index.
func getReferrers(t *testing.T, r *gin.Engine, repoName, imageName, subjectDigest, query string) (*httptest.ResponseRecorder, referrersIndex, []referrersDescriptor) {
	t.Helper()
	url := "/repository/" + repoName + "/v2/" + imageName + "/referrers/" + subjectDigest + query
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		return w, referrersIndex{}, nil
	}
	var idx referrersIndex
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &idx), "response should be an image index")
	var descs []referrersDescriptor
	require.NoError(t, json.Unmarshal(idx.Manifests, &descs))
	return w, idx, descs
}

func hostedOCIRepo(id, name string) *domain.Repository {
	return &domain.Repository{
		ID: id, Name: name, Format: domain.FormatOCI, Type: domain.TypeHosted, Online: true,
	}
}

// A cosign signature attached to a pushed image must be discoverable through the
// referrers API of the repository that holds it.
func TestReferrers_FindsSignatureBySubjectDigest(t *testing.T) {
	repo := hostedOCIRepo("r1", "oci-hosted")
	r, _ := setupWithDeps(repo)

	subjectDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "1.2.3", helmChartManifest)

	sig := referrerManifest(cosignSigArtifactType, subjectDigest)
	sigTag := strings.Replace(subjectDigest, ":", "-", 1) + ".sig"
	sigDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", sigTag, sig)

	w, idx, descs := getReferrers(t, r, "oci-hosted", "charts/nginx", subjectDigest, "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, ociIndexMediaType, w.Header().Get("Content-Type"))
	assert.Equal(t, 2, idx.SchemaVersion)
	assert.Equal(t, ociIndexMediaType, idx.MediaType)

	require.Len(t, descs, 1, "exactly one referrer")
	got := descs[0]
	assert.Equal(t, cosignSigArtifactType, got.ArtifactType)
	assert.Equal(t, sigDigest, got.Digest, "the descriptor names the referrer's own manifest")
	assert.Equal(t, int64(len(sig)), got.Size)
	assert.Positive(t, got.Size)
	assert.Equal(t, "application/vnd.oci.image.manifest.v1+json", got.MediaType)
	assert.Equal(t, "2026-08-04T00:00:00Z", got.Annotations["org.opencontainers.image.created"])
}

// An unknown subject yields an empty index, never a 404: a 404 reads as "this
// registry has no referrers API" and sends cosign down a fallback path.
func TestReferrers_UnknownSubjectIsEmptyIndexNotNotFound(t *testing.T) {
	repo := hostedOCIRepo("r1", "oci-hosted")
	r, _ := setupWithDeps(repo)

	unknown := digest("nothing was ever pushed under this digest")
	w, _, descs := getReferrers(t, r, "oci-hosted", "charts/nginx", unknown, "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, descs)

	// A JSON null for manifests breaks clients that range over the list, so the
	// exact serialization matters, not just the decoded value.
	assert.JSONEq(t,
		`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`,
		w.Body.String())
	assert.Contains(t, w.Body.String(), `"manifests":[]`, "manifests must be [] and not null")
	assert.NotContains(t, w.Body.String(), `"manifests":null`)
}

// artifactType selects one kind of referrer and must be announced back to the
// client, so it can tell a filtered answer from a complete one.
func TestReferrers_FiltersByArtifactType(t *testing.T) {
	repo := hostedOCIRepo("r1", "oci-hosted")
	r, _ := setupWithDeps(repo)

	subjectDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "1.2.3", helmChartManifest)

	sig := referrerManifest(cosignSigArtifactType, subjectDigest)
	sigDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "sig", sig)
	sbom := referrerManifest(sbomArtifactType, subjectDigest)
	sbomDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "sbom", sbom)
	require.NotEqual(t, sigDigest, sbomDigest)

	// Unfiltered: both, and no filter header.
	w, _, descs := getReferrers(t, r, "oci-hosted", "charts/nginx", subjectDigest, "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, descs, 2)
	assert.Empty(t, w.Header().Get("OCI-Filters-Applied"),
		"an unfiltered answer must not claim a filter was applied")

	// Filtered: only the signature, and the header says so. The value is escaped
	// as a real client escapes it — a raw "+" in a query string decodes to a
	// space, and every media type here contains one.
	w2, _, filtered := getReferrers(t, r, "oci-hosted", "charts/nginx", subjectDigest,
		"?artifactType="+url.QueryEscape(cosignSigArtifactType))
	require.Equal(t, http.StatusOK, w2.Code)
	require.Len(t, filtered, 1)
	assert.Equal(t, cosignSigArtifactType, filtered[0].ArtifactType)
	assert.Equal(t, sigDigest, filtered[0].Digest)
	assert.Equal(t, "artifactType", w2.Header().Get("OCI-Filters-Applied"))

	// A filter nothing matches is still an empty index, not the unfiltered list.
	w3, _, none := getReferrers(t, r, "oci-hosted", "charts/nginx", subjectDigest,
		"?artifactType="+url.QueryEscape("application/vnd.example.nothing"))
	require.Equal(t, http.StatusOK, w3.Code)
	assert.Empty(t, none)
	assert.Equal(t, "artifactType", w3.Header().Get("OCI-Filters-Applied"))
}

// A manifest pushed by tag also gets a digest-alias component, and phase 1 puts
// the same oci_subject on both. The index must name the manifest once.
func TestReferrers_DeduplicatesTagAndDigestAlias(t *testing.T) {
	repo := hostedOCIRepo("r1", "oci-hosted")
	r, d := setupWithDeps(repo)

	subjectDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", "1.2.3", helmChartManifest)
	sig := referrerManifest(cosignSigArtifactType, subjectDigest)
	sigTag := strings.Replace(subjectDigest, ":", "-", 1) + ".sig"
	sigDigest := pushManifestBody(t, r, "oci-hosted", "charts/nginx", sigTag, sig)

	// Both components really exist and both really carry the subject — otherwise
	// this test would pass without any deduplication at all.
	requireSubject(t, d, "oci-hosted", "charts/nginx", sigTag, subjectDigest)
	requireSubject(t, d, "oci-hosted", "charts/nginx", sigDigest, subjectDigest)

	w, _, descs := getReferrers(t, r, "oci-hosted", "charts/nginx", subjectDigest, "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, descs, 1, "the tag component and its digest alias are one manifest")
	assert.Equal(t, sigDigest, descs[0].Digest)
}

// requireSubject asserts the stored component carries oci_subject.
func requireSubject(t *testing.T, d formats.Deps, repoName, name, version, subjectDigest string) {
	t.Helper()
	comp := componentOf(t, d, repoName, name, version)
	require.Equal(t, subjectDigest, comp.Extra["oci_subject"],
		"component %s:%s should carry the subject", name, version)
}

// The referrers dispatch must not swallow requests for an image whose own name
// ends in "referrers": /v2/lib/referrers/manifests/1.0.0 is a manifest push.
func TestReferrers_DoesNotSwallowImageNamedReferrers(t *testing.T) {
	repo := hostedOCIRepo("r1", "oci-hosted")
	r, _ := setupWithDeps(repo)

	pushManifestBody(t, r, "oci-hosted", "lib/referrers", "1.0.0", helmChartManifest)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/oci-hosted/v2/lib/referrers/manifests/1.0.0", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "a manifest GET under such a name must still route to manifests")
	assert.Equal(t, helmChartManifest, w.Body.String())
}

// ─── Proxy repositories ────────────────────────────────────────────────────

// proxyOCIRepo is an OCI proxy repository pointed at the given upstream base.
func proxyOCIRepo(id, name, remote string) *domain.Repository {
	return &domain.Repository{
		ID: id, Name: name, Format: domain.FormatOCI, Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remote},
	}
}

// upstreamSigDigest is the digest an upstream registry reports for the signature
// attached to the subject — a manifest this proxy has never cached.
const upstreamSigDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// upstreamReferrersIndex is what a real upstream returns from its referrers API.
const upstreamReferrersIndex = `{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.index.v1+json",
  "manifests": [
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
      "size": 419,
      "artifactType": "application/vnd.dev.cosign.artifact.sig.v1+json",
      "annotations": {"org.opencontainers.image.created": "2026-08-01T00:00:00Z"}
    }
  ]
}`

const emptyIndexJSON = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`

// A proxy repository must answer from the upstream, not from whichever referring
// manifests happen to sit in its cache: an under-reported index reads to cosign
// as "this image is unsigned", which is worse than no answer at all.
func TestReferrers_ProxyForwardsUpstreamIndex(t *testing.T) {
	subject := digest("an image manifest that lives upstream")
	var gotPath, gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		if r.URL.Path != "/v2/charts/nginx/referrers/"+subject {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", ociIndexMediaType)
		_, _ = w.Write([]byte(upstreamReferrersIndex))
	}))
	defer upstream.Close()

	r, _ := setupWithDeps(proxyOCIRepo("r2", "oci-proxy", upstream.URL))

	w, idx, descs := getReferrers(t, r, "oci-proxy", "charts/nginx", subject, "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "/v2/charts/nginx/referrers/"+subject, gotPath,
		"the upstream referrers endpoint must be asked, unescaped")
	assert.Empty(t, gotQuery)
	assert.Equal(t, ociIndexMediaType, w.Header().Get("Content-Type"))
	assert.Equal(t, ociIndexMediaType, idx.MediaType)
	require.Len(t, descs, 1, "the upstream's referrer must reach the client")
	assert.Equal(t, upstreamSigDigest, descs[0].Digest)
	assert.Equal(t, cosignSigArtifactType, descs[0].ArtifactType)
	assert.JSONEq(t, upstreamReferrersIndex, w.Body.String(), "the index is passed through verbatim")
}

// artifactType must reach the upstream, and the upstream's OCI-Filters-Applied
// must reach the client: without the header a client cannot tell a filtered list
// from a complete one, and the filter itself would silently be ignored.
func TestReferrers_ProxyForwardsArtifactTypeFilter(t *testing.T) {
	subject := digest("an image manifest that lives upstream")
	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", ociIndexMediaType)
		if r.URL.Query().Get("artifactType") == cosignSigArtifactType {
			w.Header().Set("OCI-Filters-Applied", "artifactType")
			_, _ = w.Write([]byte(upstreamReferrersIndex))
			return
		}
		_, _ = w.Write([]byte(emptyIndexJSON))
	}))
	defer upstream.Close()

	r, _ := setupWithDeps(proxyOCIRepo("r2", "oci-proxy", upstream.URL))

	w, _, descs := getReferrers(t, r, "oci-proxy", "charts/nginx", subject,
		"?artifactType="+url.QueryEscape(cosignSigArtifactType))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "artifactType="+url.QueryEscape(cosignSigArtifactType), gotQuery,
		"the filter must reach the upstream unmangled")
	require.Len(t, descs, 1)
	assert.Equal(t, "artifactType", w.Header().Get("OCI-Filters-Applied"),
		"the upstream's filter announcement must reach the client")
}

// An upstream with no referrers API answers 404. That is not an error to relay:
// a client asking "is this signed" needs a definite empty answer.
func TestReferrers_ProxyUpstreamWithoutEndpointIsEmptyIndex(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	r, _ := setupWithDeps(proxyOCIRepo("r2", "oci-proxy", upstream.URL))

	w, _, descs := getReferrers(t, r, "oci-proxy", "charts/nginx", digest("subject"), "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, descs)
	assert.JSONEq(t, emptyIndexJSON, w.Body.String())
	assert.Contains(t, w.Body.String(), `"manifests":[]`, "manifests must be [] and not null")
}

// An unreachable upstream must not become a 500: a signature-checking client
// cannot interpret a proxy's connectivity failure, so it gets an empty index.
func TestReferrers_ProxyUnreachableUpstreamIsEmptyIndex(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	remote := closed.URL
	closed.Close() // nothing listens on this port any more

	r, _ := setupWithDeps(proxyOCIRepo("r2", "oci-proxy", remote))

	w, _, descs := getReferrers(t, r, "oci-proxy", "charts/nginx", digest("subject"), "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, descs)
	assert.JSONEq(t, emptyIndexJSON, w.Body.String())
}

// ─── Proxy failures the client must be able to distinguish ─────────────────

// recordingDispatcher captures the proxy_error events the handler emits — the
// only reporting channel a format handler has (formats.Deps carries no logger).
type recordingDispatcher struct {
	events []domain.WebhookPayload
}

func (r *recordingDispatcher) Dispatch(p domain.WebhookPayload) { r.events = append(r.events, p) }

// setupWithWebhooks is setupWithDeps with a webhook dispatcher installed, so a
// test can assert the operator-facing record of an upstream failure.
func setupWithWebhooks(repo *domain.Repository, disp domain.WebhookDispatcher) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
		Webhooks:   disp,
	}
	h := oci.New(d)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := requestctx.WithUser(c.Request.Context(), "test-user-id", "testuser")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// ociErrors is the OCI error document shape.
type ociErrors struct {
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

// requireUpstreamAuthRelayed asserts the response is a 502 naming the upstream
// refusal, and that an empty index was NOT what the client got.
func requireUpstreamAuthRelayed(t *testing.T, w *httptest.ResponseRecorder, wantCode string, wantStatus int) {
	t.Helper()
	require.Equal(t, http.StatusBadGateway, w.Code,
		"an upstream refusal is not the same fact as 'this subject has no referrers'")
	assert.NotContains(t, w.Body.String(), `"manifests"`,
		"a refusal must never be dressed up as an index")
	var doc ociErrors
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc), "response should be an OCI error document")
	require.Len(t, doc.Errors, 1)
	assert.Equal(t, wantCode, doc.Errors[0].Code)
	assert.Contains(t, doc.Errors[0].Message, strconv.Itoa(wantStatus),
		"the message must name the upstream status so an operator can see it is a credentials problem")
}

// A 401 from upstream means "I could not check", which is a different fact from
// "there is nothing to check". Reporting it as an empty index would let a policy
// gate read a misconfigured proxy as proof that an image is unsigned.
func TestReferrers_ProxyUpstream401IsBadGatewayNotEmptyIndex(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	disp := &recordingDispatcher{}
	r := setupWithWebhooks(proxyOCIRepo("r2", "oci-proxy", upstream.URL), disp)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/oci-proxy/v2/charts/nginx/referrers/"+digest("subject"), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	requireUpstreamAuthRelayed(t, w, "UNAUTHORIZED", http.StatusUnauthorized)
	require.Len(t, disp.events, 1, "the failure must be recorded for the operator, not only returned")
	assert.Equal(t, domain.EventProxyError, disp.events[0].Event)
	assert.Equal(t, "oci-proxy", disp.events[0].Repository)
}

// A 403 is the same class of fact as a 401 — the proxy was not allowed to look —
// and carries the OCI code for a denied request.
func TestReferrers_ProxyUpstream403IsBadGatewayNotEmptyIndex(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer upstream.Close()

	disp := &recordingDispatcher{}
	r := setupWithWebhooks(proxyOCIRepo("r2", "oci-proxy", upstream.URL), disp)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/oci-proxy/v2/charts/nginx/referrers/"+digest("subject"), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	requireUpstreamAuthRelayed(t, w, "DENIED", http.StatusForbidden)
	require.Len(t, disp.events, 1)
	assert.Equal(t, domain.EventProxyError, disp.events[0].Event)
}

// An index too large to read is a truncated list, and a truncated list read as
// complete is the same unsafe direction as the auth case: it can only ever
// under-report referrers. It must be an error, never a short answer.
func TestReferrers_ProxyOversizedUpstreamIndexIsBadGateway(t *testing.T) {
	// A single valid descriptor padded past the cap: a legitimately huge index,
	// not garbage, so the size limit is what is under test and nothing else.
	huge := `{"schemaVersion":2,"mediaType":"` + ociIndexMediaType + `","manifests":[` +
		`{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + upstreamSigDigest +
		`","size":419,"annotations":{"pad":"` + strings.Repeat("x", 17<<20) + `"}}]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", ociIndexMediaType)
		_, _ = w.Write([]byte(huge))
	}))
	defer upstream.Close()

	r, _ := setupWithDeps(proxyOCIRepo("r2", "oci-proxy", upstream.URL))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/oci-proxy/v2/charts/nginx/referrers/"+digest("subject"), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadGateway, w.Code,
		"a list too large to read whole must not be served as if it were complete")
	assert.NotContains(t, w.Body.String(), `"manifests":[]`)
}

// A 200 carrying something that is not an index — a captive portal's HTML, an
// error page — is no referrers information, which is a fact a client can act on.
func TestReferrers_ProxyNonIndexBodyIsEmptyIndex(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>Sign in to your network</body></html>`))
	}))
	defer upstream.Close()

	r, _ := setupWithDeps(proxyOCIRepo("r2", "oci-proxy", upstream.URL))

	w, _, descs := getReferrers(t, r, "oci-proxy", "charts/nginx", digest("subject"), "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, descs)
	assert.JSONEq(t, emptyIndexJSON, w.Body.String())
	assert.Equal(t, ociIndexMediaType, w.Header().Get("Content-Type"),
		"upstream HTML must never reach the client under an index content type")
}

// A 500 from upstream is not an auth failure and keeps the specified behavior.
func TestReferrers_ProxyUpstreamServerErrorIsEmptyIndex(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	r, _ := setupWithDeps(proxyOCIRepo("r2", "oci-proxy", upstream.URL))

	w, _, descs := getReferrers(t, r, "oci-proxy", "charts/nginx", digest("subject"), "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, descs)
}

// Only GET (and HEAD, which gin serves through the same handler) reads referrers;
// a write verb is not part of the API.
func TestReferrers_RejectsNonGET(t *testing.T) {
	repo := hostedOCIRepo("r1", "oci-hosted")
	r, _ := setupWithDeps(repo)

	req := httptest.NewRequest(http.MethodPut,
		"/repository/oci-hosted/v2/charts/nginx/referrers/"+digest("x"), strings.NewReader("{}"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
