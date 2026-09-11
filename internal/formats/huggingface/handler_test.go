package huggingface_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/huggingface"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func init() { gin.SetMode(gin.TestMode) }

func setup(repo *domain.Repository) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := huggingface.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func do(r *gin.Engine, method, url, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func publish(t *testing.T, r *gin.Engine, url, body string) {
	t.Helper()
	require.Equal(t, http.StatusCreated, do(r, http.MethodPut, url, body).Code, url)
}

func TestPublishAndDownload_Model(t *testing.T) {
	r := setup(testutil.SimpleRepo("hf", "huggingface"))
	publish(t, r, "/repository/hf/myns/mymodel/resolve/main/config.json", `{"hidden":1}`)

	w := do(r, http.MethodGet, "/repository/hf/myns/mymodel/resolve/main/config.json", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, `{"hidden":1}`, w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")
	// huggingface_hub refuses a file whose response carries neither of these.
	assert.NotEmpty(t, w.Header().Get("ETag"))
	assert.Len(t, w.Header().Get("X-Repo-Commit"), 40)
}

// A canonical repo has no namespace at all ("bert-base-uncased"), so the path
// cannot be split by counting segments.
func TestPublishAndDownload_CanonicalRepoID(t *testing.T) {
	r := setup(testutil.SimpleRepo("hf", "huggingface"))
	publish(t, r, "/repository/hf/bert-base-uncased/resolve/main/vocab.txt", "hello")

	w := do(r, http.MethodGet, "/repository/hf/bert-base-uncased/resolve/main/vocab.txt", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "hello", w.Body.String())

	var info map[string]any
	w = do(r, http.MethodGet, "/repository/hf/api/models/bert-base-uncased", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &info))
	assert.Equal(t, "bert-base-uncased", info["id"])
}

func TestHEAD_AnswersMetadataWithoutBody(t *testing.T) {
	r := setup(testutil.SimpleRepo("hf", "huggingface"))
	publish(t, r, "/repository/hf/myns/mymodel/resolve/main/config.json", "abcdef")

	w := do(r, http.MethodHead, "/repository/hf/myns/mymodel/resolve/main/config.json", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "6", w.Header().Get("Content-Length"))
	assert.NotEmpty(t, w.Header().Get("ETag"))
	assert.Len(t, w.Header().Get("X-Repo-Commit"), 40)
	assert.Empty(t, w.Body.String())
}

func TestInfo_ListsSiblingsAndNotFoundForUnknownRepo(t *testing.T) {
	r := setup(testutil.SimpleRepo("hf", "huggingface"))
	publish(t, r, "/repository/hf/myns/mymodel/resolve/main/config.json", "{}")
	publish(t, r, "/repository/hf/myns/mymodel/resolve/main/onnx/model.onnx", "weights")

	w := do(r, http.MethodGet, "/repository/hf/api/models/myns/mymodel", "")
	require.Equal(t, http.StatusOK, w.Code)
	var info struct {
		ID       string `json:"id"`
		SHA      string `json:"sha"`
		Siblings []struct {
			RFilename string `json:"rfilename"`
		} `json:"siblings"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &info))
	assert.Equal(t, "myns/mymodel", info.ID)
	assert.Len(t, info.SHA, 40)
	var names []string
	for _, s := range info.Siblings {
		names = append(names, s.RFilename)
	}
	assert.ElementsMatch(t, []string{"config.json", "onnx/model.onnx"}, names)

	// An unpublished repo_id is a 404, not an empty 200 — an empty 200 would
	// shadow every later member of a group (#99).
	w = do(r, http.MethodGet, "/repository/hf/api/models/myns/absent", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// A repo_id that is a prefix of another one must not collect its files: the
// asset lookup filters by substring, so the exact prefix has to be re-checked.
func TestInfo_DoesNotLeakASimilarlyNamedRepo(t *testing.T) {
	r := setup(testutil.SimpleRepo("hf", "huggingface"))
	publish(t, r, "/repository/hf/myns/bert/resolve/main/config.json", "{}")
	publish(t, r, "/repository/hf/myns/bert-large/resolve/main/config.json", "{}")
	publish(t, r, "/repository/hf/myns/bert-large/resolve/main/weights.bin", "w")

	w := do(r, http.MethodGet, "/repository/hf/api/models/myns/bert", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, strings.Count(w.Body.String(), "rfilename"))
}

// The same repo_id as a model and as a dataset are different repositories.
func TestInfo_ModelAndDatasetAreSeparate(t *testing.T) {
	r := setup(testutil.SimpleRepo("hf", "huggingface"))
	publish(t, r, "/repository/hf/myns/thing/resolve/main/model.bin", "m")
	publish(t, r, "/repository/hf/datasets/myns/thing/resolve/main/train.parquet", "d")

	w := do(r, http.MethodGet, "/repository/hf/api/models/myns/thing", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "model.bin")
	assert.NotContains(t, w.Body.String(), "train.parquet")

	w = do(r, http.MethodGet, "/repository/hf/api/datasets/myns/thing", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "train.parquet")
	assert.NotContains(t, w.Body.String(), "model.bin")

	w = do(r, http.MethodGet, "/repository/hf/datasets/myns/thing/resolve/main/train.parquet", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "d", w.Body.String())
}

func TestTree_RecursiveAndOneLevel(t *testing.T) {
	r := setup(testutil.SimpleRepo("hf", "huggingface"))
	publish(t, r, "/repository/hf/myns/mymodel/resolve/main/config.json", "{}")
	publish(t, r, "/repository/hf/myns/mymodel/resolve/main/onnx/model.onnx", "weights")

	type entry struct {
		Type string `json:"type"`
		OID  string `json:"oid"`
		Size int64  `json:"size"`
		Path string `json:"path"`
	}

	// recursive=True is what list_repo_files asks with.
	w := do(r, http.MethodGet, "/repository/hf/api/models/myns/mymodel/tree/main?recursive=True", "")
	require.Equal(t, http.StatusOK, w.Code)
	var all []entry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &all))
	paths := map[string]entry{}
	for _, e := range all {
		paths[e.Path] = e
	}
	require.Len(t, paths, 2)
	assert.Equal(t, "file", paths["onnx/model.onnx"].Type)
	assert.Equal(t, int64(7), paths["onnx/model.onnx"].Size)
	// RepoFile reads "oid" unconditionally — an entry without one is a client
	// KeyError, not a missing detail.
	assert.Len(t, paths["config.json"].OID, 40)

	// Without it, one level: the nested file collapses into its directory.
	w = do(r, http.MethodGet, "/repository/hf/api/models/myns/mymodel/tree/main", "")
	require.Equal(t, http.StatusOK, w.Code)
	var level []entry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &level))
	require.Len(t, level, 2)
	kinds := map[string]string{}
	for _, e := range level {
		kinds[e.Path] = e.Type
	}
	assert.Equal(t, "file", kinds["config.json"])
	assert.Equal(t, "directory", kinds["onnx"])

	// A subdirectory listing still reports repository-relative paths.
	w = do(r, http.MethodGet, "/repository/hf/api/models/myns/mymodel/tree/main/onnx", "")
	require.Equal(t, http.StatusOK, w.Code)
	var sub []entry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sub))
	require.Len(t, sub, 1)
	assert.Equal(t, "onnx/model.onnx", sub[0].Path)
}

// snapshot_download reads the commit sha out of the metadata document and then
// asks for every file at THAT revision, so the sha has to resolve back to the
// branch the files were published to.
func TestResolveByCommitSHA_MapsBackToTheBranch(t *testing.T) {
	r := setup(testutil.SimpleRepo("hf", "huggingface"))
	publish(t, r, "/repository/hf/myns/mymodel/resolve/main/config.json", "one")

	w := do(r, http.MethodGet, "/repository/hf/api/models/myns/mymodel", "")
	require.Equal(t, http.StatusOK, w.Code)
	var info struct {
		SHA string `json:"sha"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &info))
	require.Len(t, info.SHA, 40)

	w = do(r, http.MethodGet, "/repository/hf/myns/mymodel/resolve/"+info.SHA+"/config.json", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "one", w.Body.String())
	// A revision that IS a commit reports itself, so the client's snapshot
	// directory and the file it downloaded agree.
	assert.Equal(t, info.SHA, w.Header().Get("X-Repo-Commit"))

	// A sha that names nothing here is a 404, never another revision's file.
	w = do(r, http.MethodGet,
		"/repository/hf/myns/mymodel/resolve/0123456789abcdef0123456789abcdef01234567/config.json", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// The commit is derived from the revision's contents, so re-publishing changes
// it — otherwise a client keeps serving its cached snapshot forever.
func TestCommitSHA_ChangesWithContents(t *testing.T) {
	r := setup(testutil.SimpleRepo("hf", "huggingface"))
	publish(t, r, "/repository/hf/myns/mymodel/resolve/main/config.json", "one")
	first := do(r, http.MethodHead, "/repository/hf/myns/mymodel/resolve/main/config.json", "").
		Header().Get("X-Repo-Commit")

	publish(t, r, "/repository/hf/myns/mymodel/resolve/main/config.json", "two-different")
	second := do(r, http.MethodHead, "/repository/hf/myns/mymodel/resolve/main/config.json", "").
		Header().Get("X-Repo-Commit")

	assert.NotEqual(t, first, second)
	assert.Len(t, second, 40)
}

// Two branches of the same repository are independent trees.
func TestRevisions_AreIndependent(t *testing.T) {
	r := setup(testutil.SimpleRepo("hf", "huggingface"))
	publish(t, r, "/repository/hf/myns/mymodel/resolve/main/config.json", "main-copy")
	publish(t, r, "/repository/hf/myns/mymodel/resolve/v2/config.json", "v2-copy")

	w := do(r, http.MethodGet, "/repository/hf/myns/mymodel/resolve/v2/config.json", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "v2-copy", w.Body.String())

	w = do(r, http.MethodGet, "/repository/hf/api/models/myns/mymodel/revision/v2", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "config.json")
}

func TestUnknownEndpointAndMethods(t *testing.T) {
	r := setup(testutil.SimpleRepo("hf", "huggingface"))

	assert.Equal(t, http.StatusNotFound, do(r, http.MethodGet, "/repository/hf/whatever", "").Code)
	// A repo_id is at most two segments, so a third one is not this grammar.
	assert.Equal(t, http.StatusNotFound,
		do(r, http.MethodGet, "/repository/hf/a/b/c/resolve/main/f.json", "").Code)
	assert.Equal(t, http.StatusMethodNotAllowed,
		do(r, http.MethodPut, "/repository/hf/api/models/myns/mymodel", "{}").Code)
	assert.Equal(t, http.StatusMethodNotAllowed,
		do(r, http.MethodDelete, "/repository/hf/myns/mymodel/resolve/main/config.json", "").Code)
}

// A plain `curl -X PUT --data-binary` labels its body
// application/x-www-form-urlencoded. Storing that and serving it back would
// mislabel every published file, so the extension decides.
func TestPublish_ExtensionDecidesContentType(t *testing.T) {
	r := setup(testutil.SimpleRepo("hf", "huggingface"))

	req := httptest.NewRequest(http.MethodPut,
		"/repository/hf/myns/mymodel/resolve/main/config.json", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.ContentLength = 2
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	got := do(r, http.MethodGet, "/repository/hf/myns/mymodel/resolve/main/config.json", "")
	assert.Contains(t, got.Header().Get("Content-Type"), "application/json")

	// The Hub's own payload extensions are unknown to Go's mime table, so
	// without dropping curl's default they would each be served as a form
	// encoding — which is what a live publish produced before this was fixed.
	for _, name := range []string{"model.safetensors", "model.onnx", "train.parquet", "weights.bin"} {
		req := httptest.NewRequest(http.MethodPut,
			"/repository/hf/myns/mymodel/resolve/main/"+name, strings.NewReader("x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.ContentLength = 1
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusCreated, w.Code, name)
		got := do(r, http.MethodGet, "/repository/hf/myns/mymodel/resolve/main/"+name, "")
		assert.Equal(t, "application/octet-stream", got.Header().Get("Content-Type"), name)
	}

	// A file whose extension says nothing keeps what the client declared.
	req = httptest.NewRequest(http.MethodPut,
		"/repository/hf/myns/mymodel/resolve/main/LICENSE", strings.NewReader("MIT"))
	req.Header.Set("Content-Type", "text/plain")
	req.ContentLength = 3
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	got = do(r, http.MethodGet, "/repository/hf/myns/mymodel/resolve/main/LICENSE", "")
	assert.Contains(t, got.Header().Get("Content-Type"), "text/plain")
}

// snapshot_download reads the commit out of the metadata document and then asks
// the TREE endpoint at that commit, before fetching any file — so the metadata
// endpoints have to map a sha back to its branch too, not just resolve/.
func TestMetadataEndpoints_AcceptACommitSHA(t *testing.T) {
	r := setup(testutil.SimpleRepo("hf", "huggingface"))
	publish(t, r, "/repository/hf/myns/mymodel/resolve/main/config.json", "{}")
	publish(t, r, "/repository/hf/myns/mymodel/resolve/main/onnx/model.onnx", "weights")

	w := do(r, http.MethodGet, "/repository/hf/api/models/myns/mymodel", "")
	require.Equal(t, http.StatusOK, w.Code)
	var info struct {
		SHA string `json:"sha"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &info))
	require.Len(t, info.SHA, 40)

	// The exact sequence the real client runs: tree at the sha, recursive.
	w = do(r, http.MethodGet,
		"/repository/hf/api/models/myns/mymodel/tree/"+info.SHA+"?recursive=true&expand=false", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "onnx/model.onnx")

	w = do(r, http.MethodGet, "/repository/hf/api/models/myns/mymodel/revision/"+info.SHA, "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "config.json")

	// An unrelated sha still resolves to nothing.
	w = do(r, http.MethodGet,
		"/repository/hf/api/models/myns/mymodel/tree/0123456789abcdef0123456789abcdef01234567", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}
