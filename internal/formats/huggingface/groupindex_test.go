package huggingface_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/huggingface"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func merger() *huggingface.Handler {
	return huggingface.New(formats.Deps{Repos: testutil.NewRepoRepo()})
}

func TestGroupIndexSourcePath(t *testing.T) {
	h := merger()

	for _, p := range []string{
		"/api/models/myns/mymodel",
		"/api/models/bert-base-uncased",
		"/api/datasets/myns/mydataset/revision/v2",
		"/api/models/myns/mymodel/tree/main",
		"/api/spaces/myns/myspace/tree/main/src",
	} {
		source, ok := h.GroupIndexSourcePath(p)
		assert.True(t, ok, p)
		assert.Equal(t, p, source)
	}

	// A file download is an artifact: first-non-404 fan-out is the right
	// behavior there, so it is not claimed.
	for _, p := range []string{
		"/myns/mymodel/resolve/main/config.json",
		"/datasets/myns/mydataset/resolve/main/train.parquet",
		"/api/whoami-v2",
	} {
		_, ok := h.GroupIndexSourcePath(p)
		assert.False(t, ok, p)
	}
}

func TestMergeInfo_UnionsSiblingsAcrossMembers(t *testing.T) {
	h := merger()
	parts := []formats.GroupIndexPart{
		{Member: "hf-hosted", Body: []byte(`{"id":"myns/mymodel","sha":"aaaa","lastModified":"2026-09-01T00:00:00Z",
			"siblings":[{"rfilename":"config.json"},{"rfilename":"tokenizer.json"}]}`)},
		{Member: "hf-proxy", Body: []byte(`{"id":"myns/mymodel","sha":"bbbb",
			"siblings":[{"rfilename":"config.json"},{"rfilename":"model.safetensors"}]}`)},
	}

	body, ct, err := h.MergeGroupIndex("hf-group", "/api/models/myns/mymodel", parts)
	require.NoError(t, err)
	assert.Contains(t, ct, "application/json")

	var doc struct {
		ID       string `json:"id"`
		SHA      string `json:"sha"`
		Siblings []struct {
			RFilename string `json:"rfilename"`
		} `json:"siblings"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	assert.Equal(t, "myns/mymodel", doc.ID)
	// Member order is priority, so the first contributing member's own fields
	// survive — including the commit the whole document is dated by.
	assert.Equal(t, "aaaa", doc.SHA)

	var names []string
	for _, s := range doc.Siblings {
		names = append(names, s.RFilename)
	}
	// The union, deduped: a file only the second member has must be visible —
	// that is the shadowing this merger exists to prevent.
	assert.Equal(t, []string{"config.json", "tokenizer.json", "model.safetensors"}, names)
}

func TestMergeInfo_SkipsAMemberThatIsNotMetadata(t *testing.T) {
	h := merger()
	parts := []formats.GroupIndexPart{
		{Member: "broken", Body: []byte(`<html>gateway error</html>`)},
		{Member: "hf-hosted", Body: []byte(`{"id":"myns/mymodel","siblings":[{"rfilename":"config.json"}]}`)},
	}
	body, _, err := h.MergeGroupIndex("hf-group", "/api/models/myns/mymodel", parts)
	require.NoError(t, err)
	assert.Contains(t, string(body), "config.json")

	// With nothing readable at all, failing is better than answering an empty
	// repository — a client reads that as "this model has no files".
	_, _, err = h.MergeGroupIndex("hf-group", "/api/models/myns/mymodel",
		[]formats.GroupIndexPart{{Member: "broken", Body: []byte("nope")}})
	assert.Error(t, err)
}

func TestMergeTree_UnionsEntriesDedupedByPath(t *testing.T) {
	h := merger()
	parts := []formats.GroupIndexPart{
		{Member: "hf-hosted", Body: []byte(
			`[{"type":"file","oid":"aa","size":10,"path":"config.json"}]`)},
		{Member: "hf-proxy", Body: []byte(
			`[{"type":"file","oid":"bb","size":99,"path":"config.json"},` +
				`{"type":"file","oid":"cc","size":20,"path":"onnx/model.onnx"}]`)},
	}

	body, ct, err := h.MergeGroupIndex("hf-group", "/api/models/myns/mymodel/tree/main", parts)
	require.NoError(t, err)
	assert.Contains(t, ct, "application/json")

	var entries []struct {
		OID  string `json:"oid"`
		Size int64  `json:"size"`
		Path string `json:"path"`
	}
	require.NoError(t, json.Unmarshal(body, &entries))
	require.Len(t, entries, 2)
	// First member wins on a shared path — the same member whose file the
	// artifact fan-out then serves, so size and oid describe what is downloaded.
	assert.Equal(t, "config.json", entries[0].Path)
	assert.Equal(t, int64(10), entries[0].Size)
	assert.Equal(t, "onnx/model.onnx", entries[1].Path)
}
