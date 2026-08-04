package oci_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
)

func groupMerger() *oci.Handler { return oci.New(formats.Deps{}) }

// Only the referrers index is merged across members. A manifest or a blob is a
// single artifact, and the first member holding it is the right answer.
func TestOCI_GroupIndexSourcePath(t *testing.T) {
	h := groupMerger()

	src, ok := h.GroupIndexSourcePath("/v2/charts/nginx/referrers/" + digest("subject"))
	assert.True(t, ok)
	assert.Equal(t, "/v2/charts/nginx/referrers/"+digest("subject"), src,
		"every member is asked the same question, so the source is the path itself")

	_, ok = h.GroupIndexSourcePath("/v2/charts/nginx/manifests/1.0.0")
	assert.False(t, ok)
	_, ok = h.GroupIndexSourcePath("/v2/charts/nginx/blobs/" + digest("layer"))
	assert.False(t, ok)
	_, ok = h.GroupIndexSourcePath("/v2/charts/nginx/tags/list")
	assert.False(t, ok)
	_, ok = h.GroupIndexSourcePath("/v2/")
	assert.False(t, ok)
	_, ok = h.GroupIndexSourcePath("/charts/nginx/referrers/" + digest("subject"))
	assert.False(t, ok, "a path outside /v2/ is not a referrers request")

	// An image legitimately named ".../referrers" must keep its own manifests.
	_, ok = h.GroupIndexSourcePath("/v2/lib/referrers/manifests/1.0.0")
	assert.False(t, ok)
}

// indexWith builds one member's referrers answer from raw descriptor bodies.
func indexWith(descs ...string) []byte {
	body := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[`
	for i, d := range descs {
		if i > 0 {
			body += ","
		}
		body += d
	}
	return []byte(body + "]}")
}

func mergedDescriptors(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var idx struct {
		SchemaVersion int              `json:"schemaVersion"`
		MediaType     string           `json:"mediaType"`
		Manifests     []map[string]any `json:"manifests"`
	}
	require.NoError(t, json.Unmarshal(body, &idx))
	assert.Equal(t, 2, idx.SchemaVersion)
	assert.Equal(t, "application/vnd.oci.image.index.v1+json", idx.MediaType)
	return idx.Manifests
}

// Every member's referrers reach the merged index, in member order.
func TestOCI_MergeGroupIndex_UnionsMembers(t *testing.T) {
	h := groupMerger()
	body, ct, err := h.MergeGroupIndex("grp", "/v2/img/referrers/"+digest("s"), []formats.GroupIndexPart{
		{Member: "m1", Body: indexWith(`{"digest":"sha256:aa","size":1}`)},
		{Member: "m2", Body: indexWith(`{"digest":"sha256:bb","size":2}`)},
	})
	require.NoError(t, err)
	assert.Equal(t, "application/vnd.oci.image.index.v1+json", ct)

	descs := mergedDescriptors(t, body)
	require.Len(t, descs, 2)
	assert.Equal(t, "sha256:aa", descs[0]["digest"])
	assert.Equal(t, "sha256:bb", descs[1]["digest"])
}

// The same manifest can legitimately sit in two members. It is the same
// referrer, so it is named once — keyed on the manifest digest, and the earlier
// member's descriptor is the one kept.
func TestOCI_MergeGroupIndex_DeduplicatesByDigestEarlierMemberWins(t *testing.T) {
	h := groupMerger()
	body, _, err := h.MergeGroupIndex("grp", "/v2/img/referrers/"+digest("s"), []formats.GroupIndexPart{
		{Member: "m1", Body: indexWith(`{"digest":"sha256:aa","size":1,"artifactType":"from-m1"}`)},
		{Member: "m2", Body: indexWith(
			`{"digest":"sha256:aa","size":1,"artifactType":"from-m2"}`,
			`{"digest":"sha256:bb","size":2}`)},
	})
	require.NoError(t, err)

	descs := mergedDescriptors(t, body)
	require.Len(t, descs, 2, "the shared manifest is one referrer, not two")
	assert.Equal(t, "sha256:aa", descs[0]["digest"])
	assert.Equal(t, "from-m1", descs[0]["artifactType"], "member order is priority")
	assert.Equal(t, "sha256:bb", descs[1]["digest"])
}

// Descriptors cross the merge as the bytes the member produced: re-encoding them
// through this package's struct would drop the spec fields it does not model,
// and a client reading platform or urls would silently lose them.
func TestOCI_MergeGroupIndex_KeepsFieldsThisPackageDoesNotModel(t *testing.T) {
	h := groupMerger()
	body, _, err := h.MergeGroupIndex("grp", "/v2/img/referrers/"+digest("s"), []formats.GroupIndexPart{
		{Member: "m1", Body: indexWith(
			`{"digest":"sha256:aa","size":1,"platform":{"os":"linux","architecture":"arm64"},` +
				`"urls":["https://example.test/a"]}`)},
	})
	require.NoError(t, err)

	descs := mergedDescriptors(t, body)
	require.Len(t, descs, 1)
	assert.Equal(t, map[string]any{"os": "linux", "architecture": "arm64"}, descs[0]["platform"])
	assert.Equal(t, []any{"https://example.test/a"}, descs[0]["urls"])
}

// Members with nothing to contribute merge into an empty index whose manifests
// is [] and not null: a null breaks clients that range over the list.
func TestOCI_MergeGroupIndex_AllEmptyIsEmptyList(t *testing.T) {
	h := groupMerger()
	body, _, err := h.MergeGroupIndex("grp", "/v2/img/referrers/"+digest("s"), []formats.GroupIndexPart{
		{Member: "m1", Body: indexWith()},
		{Member: "m2", Body: indexWith()},
	})
	require.NoError(t, err)
	assert.Contains(t, string(body), `"manifests":[]`)
	assert.Empty(t, mergedDescriptors(t, body))
}

// A member body that cannot be read is a member whose referrers are missing from
// the result. Skipping it the way the other formats skip a malformed part would
// hand back a short index, which is what a signature checker reads as "unsigned".
func TestOCI_MergeGroupIndex_UnreadableMemberBodyIsAnError(t *testing.T) {
	h := groupMerger()
	for name, part := range map[string]formats.GroupIndexPart{
		"not json":              {Member: "m1", Body: []byte("<html>captive portal</html>")},
		"descriptor not object": {Member: "m1", Body: indexWith(`"just a string"`)},
		"descriptor no digest":  {Member: "m1", Body: indexWith(`{"size":1}`)},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := h.MergeGroupIndex("grp", "/v2/img/referrers/"+digest("s"),
				[]formats.GroupIndexPart{part, {Member: "m2", Body: indexWith(`{"digest":"sha256:bb"}`)}})
			require.Error(t, err, "an unreadable member must not be silently dropped from the merge")
		})
	}
}

// A member that could not tell us what it holds must fail the group; 404 is the
// one non-2xx that is a fact about the subject rather than about the member.
func TestOCI_GroupIndexMemberFailureIsFatal(t *testing.T) {
	h := groupMerger()
	p := "/v2/img/referrers/" + digest("s")

	assert.False(t, h.GroupIndexMemberFailureIsFatal(p, http.StatusNotFound),
		"404 contributes nothing and is not a failure to look")
	for _, status := range []int{
		http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable,
	} {
		assert.True(t, h.GroupIndexMemberFailureIsFatal(p, status),
			"%d means the member could not be consulted", status)
	}

	assert.False(t, h.GroupIndexMemberFailureIsFatal("/v2/img/manifests/1.0.0", http.StatusBadGateway),
		"the policy belongs to the referrers index alone")
}
