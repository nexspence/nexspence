package group_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	return ociGroupEngineWithDeps(nil, repos...)
}

// ociGroupEngineWithDeps is ociGroupEngine with a hook that may swap a
// dependency for a decorated one, which is how a test makes ONE member fail
// while the others answer normally — the mocks' error seams are per-process.
func ociGroupEngineWithDeps(customize func(*formats.Deps), repos ...*domain.Repository) *gin.Engine {
	repoRepo := testutil.NewRepoRepo(repos...)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	if customize != nil {
		customize(&d)
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

// ── tags/list ──────────────────────────────────────────────────────────────

type ociTagList struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

// getGroupTags asks the group for one image's tag list.
func getGroupTags(r *gin.Engine, groupName, imageName, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet,
		"/repository/"+groupName+"/v2/"+imageName+"/tags/list"+query, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeTags(t *testing.T, w *httptest.ResponseRecorder) ociTagList {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var doc ociTagList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc), "response should be a tag list")
	return doc
}

// One image's tags are spread over two members — the ordinary result of
// promoting v1 to a release repository while v2 is still in a staging one. The
// group must list both: first-non-404 fan-out answers with member one's list
// alone, and a tag list is read as the set of versions that exist.
func TestGroupMerge_OCITags_UnionsMembersSortedUnderTheImageName(t *testing.T) {
	r := ociGroupEngine(ociGroup("grp", "m1", "m2"), hostedOCI("m1"), hostedOCI("m2"))

	pushOCIManifest(t, r, "m1", "library/ubuntu", "v3", ociImageManifest)
	pushOCIManifest(t, r, "m1", "library/ubuntu", "v1", ociImageManifest)
	pushOCIManifest(t, r, "m2", "library/ubuntu", "v2", ociImageManifest)

	doc := decodeTags(t, getGroupTags(r, "grp", "library/ubuntu", ""))
	assert.Equal(t, "library/ubuntu", doc.Name,
		"the merged list names the image, which is what the client addressed")
	assert.Equal(t, []string{"v1", "v2", "v3"}, doc.Tags,
		"every member's tags reach the client, sorted")
}

// The same tag in two members is one tag. A registry that listed it twice would
// break a client that counts versions, and the spec's ?last= cursor assumes a
// strictly ascending list.
func TestGroupMerge_OCITags_TagInBothMembersIsListedOnce(t *testing.T) {
	r := ociGroupEngine(ociGroup("grp", "m1", "m2"), hostedOCI("m1"), hostedOCI("m2"))

	pushOCIManifest(t, r, "m1", "img", "1.0.0", ociImageManifest)
	pushOCIManifest(t, r, "m2", "img", "1.0.0", ociImageManifest)
	pushOCIManifest(t, r, "m2", "img", "2.0.0", ociImageManifest)

	doc := decodeTags(t, getGroupTags(r, "grp", "img", ""))
	assert.Equal(t, []string{"1.0.0", "2.0.0"}, doc.Tags, "the shared tag is one tag")
}

// An image no member holds is an empty list, never a null one: a null breaks
// clients that range over the tags, and the single-repository answer is an empty
// 200 too, so the group must not invent a 404 the client would read as "no such
// registry".
func TestGroupMerge_OCITags_NoMemberHoldsTheImageIsEmptyList(t *testing.T) {
	r := ociGroupEngine(ociGroup("grp", "m1", "m2"), hostedOCI("m1"), hostedOCI("m2"))

	w := getGroupTags(r, "grp", "img", "")
	doc := decodeTags(t, w)
	assert.Equal(t, "img", doc.Name)
	assert.Empty(t, doc.Tags)
	assert.Contains(t, w.Body.String(), `"tags":[]`, "tags must be [] and not null in the bytes")
}

// Paging is applied to the MERGED list, not to each member's own. A member asked
// for its first n tags contributes a truncated list, and the tags past its cut
// are then unreachable through every page of the group: no cursor the client can
// send brings them back. So the members are asked for their complete lists and
// the group cuts the page out of the union — which is also what makes the Link
// header's cursor mean the same thing on the next request.
func TestGroupMerge_OCITags_PagesTheMergedUnionNotEachMember(t *testing.T) {
	r := ociGroupEngine(ociGroup("grp", "m1", "m2"), hostedOCI("m1"), hostedOCI("m2"))

	for _, tag := range []string{"a", "b", "c"} {
		pushOCIManifest(t, r, "m1", "img", tag, ociImageManifest)
	}
	pushOCIManifest(t, r, "m2", "img", "d", ociImageManifest)

	w := getGroupTags(r, "grp", "img", "?n=2")
	doc := decodeTags(t, w)
	assert.Equal(t, []string{"a", "b"}, doc.Tags, "the first page of the union")
	assert.Equal(t, `</repository/grp/v2/img/tags/list?last=b&n=2>; rel="next"`, w.Header().Get("Link"),
		"the cursor names an entry of the merged list, on the group's own URL")

	// Following the link must reach "c" — the tag a per-member page would have
	// cut off, and the one no later request could recover.
	next := decodeTags(t, getGroupTags(r, "grp", "img", "?n=2&last=b"))
	assert.Equal(t, []string{"c", "d"}, next.Tags)
}

// A complete answer carries no Link header: its absence is what tells a client
// to stop paging.
func TestGroupMerge_OCITags_CompletePageHasNoLinkHeader(t *testing.T) {
	r := ociGroupEngine(ociGroup("grp", "m1", "m2"), hostedOCI("m1"), hostedOCI("m2"))

	pushOCIManifest(t, r, "m1", "img", "a", ociImageManifest)
	pushOCIManifest(t, r, "m2", "img", "b", ociImageManifest)

	w := getGroupTags(r, "grp", "img", "?n=10")
	assert.Equal(t, []string{"a", "b"}, decodeTags(t, w).Tags)
	assert.Empty(t, w.Header().Get("Link"), "nothing was truncated")
}

// failingComponents fails the tag search for one member and answers normally for
// every other, which is what a group hitting one broken member looks like.
type failingComponents struct {
	*testutil.ComponentRepo
	repo string
	err  error
}

func (f *failingComponents) Search(ctx context.Context, p domain.SearchParams) (*domain.Page[domain.Component], error) {
	if p.Repository == f.repo {
		return nil, f.err
	}
	return f.ComponentRepo.Search(ctx, p)
}

// A member that could not be consulted fails the whole group. On this endpoint a
// member with nothing to contribute answers 200 with an empty list, so a non-2xx
// is never "I hold no tags" — it is "I could not look". Serving the remaining
// members' tags as the answer would hand a retention job a short list, and the
// tags missing from it are the ones it deletes.
func TestGroupMerge_OCITags_UnconsultableMemberIsBadGatewayNotAShortList(t *testing.T) {
	r := ociGroupEngineWithDeps(func(d *formats.Deps) {
		d.Components = &failingComponents{
			ComponentRepo: d.Components.(*testutil.ComponentRepo),
			repo:          "broken",
			err:           errors.New("component store unreachable"),
		}
	}, ociGroup("grp", "m1", "broken"), hostedOCI("m1"), hostedOCI("broken"))

	pushOCIManifest(t, r, "m1", "img", "1.0.0", ociImageManifest)

	w := getGroupTags(r, "grp", "img", "")
	require.NotEqual(t, http.StatusOK, w.Code,
		"a member that could not be checked must not be silently dropped from the list")
	assert.NotContains(t, w.Body.String(), `"tags"`,
		"an incomplete list must never be dressed up as a tag list")
}

// Merging the LIST must not move the pull. A tag held by both members resolves
// to the earlier member's manifest, exactly as it did before the list was
// merged: the union answers "which versions exist", and the manifest request
// behind it still answers "whose".
func TestGroupMerge_OCITags_MergedListDoesNotChangeWhichMemberServesThePull(t *testing.T) {
	r := ociGroupEngine(ociGroup("grp", "m1", "m2"), hostedOCI("m1"), hostedOCI("m2"))

	first := pushOCIManifest(t, r, "m1", "img", "1.0.0", ociImageManifest)
	second := pushOCIManifest(t, r, "m2", "img", "1.0.0",
		ociReferrerManifest(ociSBOMArtifactType, ociDigest("other"), "m2"))
	require.NotEqual(t, first, second, "the two members must hold different bytes under the tag")

	assert.Equal(t, []string{"1.0.0"}, decodeTags(t, getGroupTags(r, "grp", "img", "")).Tags)

	req := httptest.NewRequest(http.MethodGet, "/repository/grp/v2/img/manifests/1.0.0", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, first, w.Header().Get("Docker-Content-Digest"), "the earlier member still wins the pull")
	assert.Equal(t, "m1", w.Header().Get("X-Nexspence-Source"))
}

// A member of another format is not a member of this group's protocol at all:
// the group skips it before it is ever called, so it neither contributes to nor
// breaks the merge. Same for an offline member — which is the one gap in the
// "never answer with a list one entry short" policy, because a member skipped by
// configuration is skipped silently.
func TestGroupMerge_OCITags_ForeignFormatAndOfflineMembersAreSkipped(t *testing.T) {
	maven := &domain.Repository{
		ID: "repo-mvn", Name: "mvn", Format: domain.FormatMaven2, Type: domain.TypeHosted, Online: true,
	}
	offline := hostedOCI("dark")
	offline.Online = false

	r := ociGroupEngine(ociGroup("grp", "mvn", "m1", "dark"), maven, hostedOCI("m1"), offline)
	pushOCIManifest(t, r, "m1", "img", "1.0.0", ociImageManifest)

	doc := decodeTags(t, getGroupTags(r, "grp", "img", ""))
	assert.Equal(t, []string{"1.0.0"}, doc.Tags,
		"the OCI member still answers; the foreign and offline members are not consulted")
}

// HEAD is not a tag-list method. The members say so with a 405, and the strict
// policy relays it rather than turning it into "no such image".
func TestGroupMerge_OCITags_HeadIsRelayedAsMethodNotAllowed(t *testing.T) {
	r := ociGroupEngine(ociGroup("grp", "m1", "m2"), hostedOCI("m1"), hostedOCI("m2"))
	pushOCIManifest(t, r, "m1", "img", "1.0.0", ociImageManifest)

	req := httptest.NewRequest(http.MethodHead, "/repository/grp/v2/img/tags/list", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// ── _catalog ───────────────────────────────────────────────────────────────

// getGroupCatalog asks the group for the image names it holds.
func getGroupCatalog(r *gin.Engine, groupName, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/repository/"+groupName+"/v2/_catalog"+query, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeCatalog(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var doc struct {
		Repositories []string `json:"repositories"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc), "response should be a catalog")
	return doc.Repositories
}

// The catalog of a group is the set of image names a client can pull from it,
// which is the union of its members'. Answering with the first member's alone
// hides every image that lives only further down the member list.
func TestGroupMerge_OCICatalog_UnionsMembersDeduplicatedSorted(t *testing.T) {
	r := ociGroupEngine(ociGroup("grp", "m1", "m2"), hostedOCI("m1"), hostedOCI("m2"))

	pushOCIManifest(t, r, "m1", "library/ubuntu", "22.04", ociImageManifest)
	pushOCIManifest(t, r, "m1", "charts/nginx", "1.2.3", ociImageManifest)
	// The same image in both members is one image name, not two.
	pushOCIManifest(t, r, "m2", "charts/nginx", "1.3.0", ociImageManifest)
	pushOCIManifest(t, r, "m2", "apps/api", "2.0.0", ociImageManifest)

	repos := decodeCatalog(t, getGroupCatalog(r, "grp", ""))
	assert.Equal(t, []string{"apps/api", "charts/nginx", "library/ubuntu"}, repos,
		"every member's image names, deduplicated and sorted")
}

// A group whose members hold nothing is an empty catalog, not a 404 and not a
// null list — the same document a hosted repository with no images returns.
func TestGroupMerge_OCICatalog_EmptyMembersIsEmptyList(t *testing.T) {
	r := ociGroupEngine(ociGroup("grp", "m1", "m2"), hostedOCI("m1"), hostedOCI("m2"))

	w := getGroupCatalog(r, "grp", "")
	assert.Empty(t, decodeCatalog(t, w))
	assert.Contains(t, w.Body.String(), `"repositories":[]`,
		"repositories must be [] and not null in the bytes")
}

// The catalog pages the merged union for the same reason the tag list does: a
// member that paged its own catalog would put the names past its cut out of
// reach of every page the client can ask for.
func TestGroupMerge_OCICatalog_PagesTheMergedUnionNotEachMember(t *testing.T) {
	r := ociGroupEngine(ociGroup("grp", "m1", "m2"), hostedOCI("m1"), hostedOCI("m2"))

	for _, img := range []string{"img/a", "img/b", "img/c"} {
		pushOCIManifest(t, r, "m1", img, "1.0.0", ociImageManifest)
	}
	pushOCIManifest(t, r, "m2", "img/d", "1.0.0", ociImageManifest)

	w := getGroupCatalog(r, "grp", "?n=2")
	assert.Equal(t, []string{"img/a", "img/b"}, decodeCatalog(t, w))
	assert.Equal(t, `</repository/grp/v2/_catalog?last=img%2Fb&n=2>; rel="next"`, w.Header().Get("Link"),
		"the cursor names an entry of the merged catalog, on the group's own URL")

	next := decodeCatalog(t, getGroupCatalog(r, "grp", "?n=2&last=img%2Fb"))
	assert.Equal(t, []string{"img/c", "img/d"}, next)
}

// failingImageNames fails the catalog query for one member and answers normally
// for every other.
type failingImageNames struct {
	*testutil.AssetRepo
	repo string
	err  error
}

func (f *failingImageNames) ListOCIImageNames(ctx context.Context, repoNames []string) ([]string, error) {
	for _, n := range repoNames {
		if n == f.repo {
			return nil, f.err
		}
	}
	return f.AssetRepo.ListOCIImageNames(ctx, repoNames)
}

// A member that could not be consulted fails the whole group. A catalog is what
// a mirroring or retention job enumerates, and the names missing from a short
// one are the images it does not copy — or does delete.
func TestGroupMerge_OCICatalog_UnconsultableMemberIsAnErrorNotAShortCatalog(t *testing.T) {
	r := ociGroupEngineWithDeps(func(d *formats.Deps) {
		d.Assets = &failingImageNames{
			AssetRepo: d.Assets.(*testutil.AssetRepo),
			repo:      "broken",
			err:       errors.New("asset store unreachable"),
		}
	}, ociGroup("grp", "m1", "broken"), hostedOCI("m1"), hostedOCI("broken"))

	pushOCIManifest(t, r, "m1", "library/ubuntu", "22.04", ociImageManifest)

	w := getGroupCatalog(r, "grp", "")
	require.NotEqual(t, http.StatusOK, w.Code,
		"a member that could not be checked must not be silently dropped from the catalog")
	assert.NotContains(t, w.Body.String(), `"repositories"`,
		"an incomplete catalog must never be dressed up as one")
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
