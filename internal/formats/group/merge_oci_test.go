package group_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

const (
	ociCosignSigArtifactType = "application/vnd.dev.cosign.artifact.sig.v1+json"
	ociSBOMArtifactType      = "application/spdx+json"
	ociIndexMediaType        = "application/vnd.oci.image.index.v1+json"
)

// ociImageManifest is a plain image manifest — the subject a signature attaches to.
const ociImageManifest = `{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "config": {"mediaType": "application/vnd.oci.image.config.v1+json", "digest": "sha256:aa", "size": 2},
  "layers": [{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": "sha256:bb", "size": 9}]
}`

func ociDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ociReferrerManifest points at another manifest through its subject descriptor —
// how cosign attaches a signature. salt keeps two otherwise identical referrers
// distinct so a test can tell a union from a deduplication.
func ociReferrerManifest(artifactType, subjectDigest, salt string) string {
	return fmt.Sprintf(`{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "artifactType": %q,
  "config": {"mediaType": "application/vnd.oci.empty.v1+json", "digest": "sha256:ee", "size": 2},
  "layers": [{"mediaType": "application/octet-stream", "digest": "sha256:ff", "size": 7}],
  "subject": {"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": %q, "size": 100},
  "annotations": {"salt": %q}
}`, artifactType, subjectDigest, salt)
}

// hostedOCI is an online hosted OCI repository.
func hostedOCI(name string) *domain.Repository {
	return &domain.Repository{
		ID: "repo-" + name, Name: name, Format: domain.FormatOCI, Type: domain.TypeHosted, Online: true,
	}
}

// proxyOCI is an online OCI proxy repository pointed at remote.
func proxyOCI(name, remote string) *domain.Repository {
	return &domain.Repository{
		ID: "repo-" + name, Name: name, Format: domain.FormatOCI, Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remote},
	}
}

// ociGroup is an OCI group repository over the named members, in order.
func ociGroup(name string, members ...string) *domain.Repository {
	ms := make([]interface{}, len(members))
	for i, m := range members {
		ms[i] = m
	}
	return &domain.Repository{
		ID: "repo-" + name, Name: name, Format: domain.FormatOCI, Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": ms},
	}
}

// ociGroupEngine wires the real OCI handler behind a group handler, the way the
// router does.
func ociGroupEngine(repos ...*domain.Repository) *gin.Engine {
	repoRepo := testutil.NewRepoRepo(repos...)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	ociH := oci.New(d)
	registry := map[string]formats.FormatHandler{string(domain.FormatOCI): ociH}
	groupH := group.New(d, registry)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(requestctx.WithUser(c.Request.Context(), "test-user-id", "testuser"))
		c.Next()
	})
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		ociH.ServeHTTP(c)
	})
	return r
}

// pushOCIManifest pushes one manifest into a member and returns its digest.
func pushOCIManifest(t *testing.T, r *gin.Engine, repoName, imageName, reference, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut,
		"/repository/"+repoName+"/v2/"+imageName+"/manifests/"+reference, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "manifest push should succeed")
	return w.Header().Get("Docker-Content-Digest")
}

// pushSignature attaches a referrer to subject in the named member and returns
// the referrer's own digest, pushed under the cosign tag scheme.
func pushSignature(t *testing.T, r *gin.Engine, repoName, imageName, artifactType, subject, salt string) string {
	t.Helper()
	body := ociReferrerManifest(artifactType, subject, salt)
	tag := strings.Replace(subject, ":", "-", 1) + ".sig." + salt
	return pushOCIManifest(t, r, repoName, imageName, tag, body)
}

type ociDescriptor struct {
	MediaType    string            `json:"mediaType"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	ArtifactType string            `json:"artifactType"`
	Annotations  map[string]string `json:"annotations"`
}

type ociIndexDoc struct {
	SchemaVersion int             `json:"schemaVersion"`
	MediaType     string          `json:"mediaType"`
	Manifests     []ociDescriptor `json:"manifests"`
}

// getGroupReferrers asks the group for the referrers of subject.
func getGroupReferrers(r *gin.Engine, groupName, imageName, subject, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet,
		"/repository/"+groupName+"/v2/"+imageName+"/referrers/"+subject+query, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// decodeIndex decodes a referrers response, asserting it is a 200 image index.
func decodeIndex(t *testing.T, w *httptest.ResponseRecorder) ociIndexDoc {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var idx ociIndexDoc
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &idx), "response should be an image index")
	return idx
}

func digestsOf(idx ociIndexDoc) []string {
	out := make([]string, 0, len(idx.Manifests))
	for _, d := range idx.Manifests {
		out = append(out, d.Digest)
	}
	return out
}

// A signature pushed to the SECOND member must be visible through the group.
// The referrers endpoint never answers 404 — an unknown subject is an empty 200 —
// so first-non-404 fan-out would let the first member's empty index shadow every
// member behind it and report a signed image as unsigned.
func TestGroupMerge_OCIReferrers_SecondMemberSignatureIsFound(t *testing.T) {
	r := ociGroupEngine(ociGroup("grp", "m1", "m2"), hostedOCI("m1"), hostedOCI("m2"))

	subject := pushOCIManifest(t, r, "m2", "img", "1.0.0", ociImageManifest)
	sig := pushSignature(t, r, "m2", "img", ociCosignSigArtifactType, subject, "a")

	idx := decodeIndex(t, getGroupReferrers(r, "grp", "img", subject, ""))
	assert.Equal(t, ociIndexMediaType, idx.MediaType)
	assert.Equal(t, []string{sig}, digestsOf(idx),
		"the second member's signature must reach the client through the group")
}

// Referrers held by two members are unioned, and a manifest present in both is
// named once: the dedup key is the manifest digest, not the member it came from.
func TestGroupMerge_OCIReferrers_UnionsMembersAndDeduplicatesByDigest(t *testing.T) {
	r := ociGroupEngine(ociGroup("grp", "m1", "m2"), hostedOCI("m1"), hostedOCI("m2"))

	subject := ociDigest(ociImageManifest)
	shared := pushSignature(t, r, "m1", "img", ociCosignSigArtifactType, subject, "shared")
	sharedAgain := pushSignature(t, r, "m2", "img", ociCosignSigArtifactType, subject, "shared")
	require.Equal(t, shared, sharedAgain, "the same manifest bytes must have the same digest in both members")
	onlyM2 := pushSignature(t, r, "m2", "img", ociSBOMArtifactType, subject, "m2-only")

	idx := decodeIndex(t, getGroupReferrers(r, "grp", "img", subject, ""))
	assert.Equal(t, []string{shared, onlyM2}, digestsOf(idx),
		"both members contribute, the shared manifest is listed once, and the earlier member wins")
}

// Every member genuinely holding nothing is an empty index, never a 404 and
// never a null list: a 404 reads as "this registry has no referrers API".
func TestGroupMerge_OCIReferrers_AllMembersEmptyIsEmptyIndex(t *testing.T) {
	r := ociGroupEngine(ociGroup("grp", "m1", "m2"), hostedOCI("m1"), hostedOCI("m2"))

	w := getGroupReferrers(r, "grp", "img", ociDigest("nothing refers to this"), "")
	idx := decodeIndex(t, w)
	assert.Empty(t, idx.Manifests)
	assert.Contains(t, w.Body.String(), `"manifests":[]`, "manifests must be [] and not null")
}

// A member that could not be consulted makes the whole group answer 502. Serving
// the other members' referrers as if they were the complete list would report a
// possibly-incomplete answer as complete — a policy gate would read the short
// list as "unsigned".
func TestGroupMerge_OCIReferrers_UnconsultableMemberIsBadGatewayNotPartialList(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	remote := closed.URL
	closed.Close() // nothing listens on this port any more

	r := ociGroupEngine(ociGroup("grp", "m1", "down"), hostedOCI("m1"), proxyOCI("down", remote))

	subject := ociDigest(ociImageManifest)
	pushSignature(t, r, "m1", "img", ociCosignSigArtifactType, subject, "a")

	w := getGroupReferrers(r, "grp", "img", subject, "")
	require.Equal(t, http.StatusBadGateway, w.Code,
		"a member that could not be checked must not be silently dropped from the merge")
	assert.NotContains(t, w.Body.String(), `"manifests"`,
		"an incomplete answer must never be dressed up as an index")
}

// A rate-limited upstream told the proxy to come back later; it did not tell it
// the subject has no referrers. The group must not merge around that either.
func TestGroupMerge_OCIReferrers_RateLimitedMemberIsBadGateway(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	r := ociGroupEngine(ociGroup("grp", "limited", "m2"), proxyOCI("limited", upstream.URL), hostedOCI("m2"))

	subject := ociDigest(ociImageManifest)
	pushSignature(t, r, "m2", "img", ociCosignSigArtifactType, subject, "a")

	w := getGroupReferrers(r, "grp", "img", subject, "")
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.NotContains(t, w.Body.String(), `"manifests"`)
}

// The artifactType filter must still select through the group, or a cosign query
// would come back carrying every SBOM in every member.
func TestGroupMerge_OCIReferrers_ArtifactTypeFilterAppliesThroughGroup(t *testing.T) {
	r := ociGroupEngine(ociGroup("grp", "m1", "m2"), hostedOCI("m1"), hostedOCI("m2"))

	subject := ociDigest(ociImageManifest)
	sbom := pushSignature(t, r, "m1", "img", ociSBOMArtifactType, subject, "sbom")
	sig := pushSignature(t, r, "m2", "img", ociCosignSigArtifactType, subject, "sig")

	idx := decodeIndex(t, getGroupReferrers(r, "grp", "img", subject,
		"?artifactType="+url.QueryEscape(ociCosignSigArtifactType)))
	assert.Equal(t, []string{sig}, digestsOf(idx), "only the cosign signature matches the filter")
	assert.NotContains(t, digestsOf(idx), sbom)
}
