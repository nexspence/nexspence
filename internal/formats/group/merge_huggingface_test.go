package group_test

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
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/huggingface"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// buildHFGroupEngine wires real huggingface handlers for members and a group
// over them.
func buildHFGroupEngine(t *testing.T, groupName string, memberNames ...string) *gin.Engine {
	t.Helper()

	repos := make([]*domain.Repository, 0, len(memberNames)+1)
	ms := make([]interface{}, len(memberNames))
	for i, name := range memberNames {
		repos = append(repos, testutil.SimpleRepo(name, "huggingface"))
		ms[i] = name
	}
	repos = append(repos, &domain.Repository{
		ID: "repo-" + groupName, Name: groupName, Format: "huggingface",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": ms},
	})

	repoRepo := testutil.NewRepoRepo(repos...)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	hfH := huggingface.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"huggingface": hfH})

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		hfH.ServeHTTP(c)
	})
	return r
}

func putHFFile(t *testing.T, r *gin.Engine, repoName, repoPath, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+repoPath, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, "upload %s: %s", repoPath, w.Body.String())
}

// A group's repository metadata must union every member's file list —
// first-non-404 fan-out would show one member's files and hide the rest.
func TestGroupMerge_HuggingFaceSiblingsUnionMembers(t *testing.T) {
	r := buildHFGroupEngine(t, "hf-group", "hf1", "hf2")
	putHFFile(t, r, "hf1", "/myns/mymodel/resolve/main/config.json", "{}")
	putHFFile(t, r, "hf2", "/myns/mymodel/resolve/main/model.safetensors", "weights")

	w := get(r, "/repository/hf-group/api/models/myns/mymodel")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
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
	assert.ElementsMatch(t, []string{"config.json", "model.safetensors"}, names)

	// And both members' files are actually reachable through the group by
	// branch, which is how hf_hub_download asks for them.
	assert.Equal(t, http.StatusOK, get(r, "/repository/hf-group/myns/mymodel/resolve/main/config.json").Code)
	got := get(r, "/repository/hf-group/myns/mymodel/resolve/main/model.safetensors")
	require.Equal(t, http.StatusOK, got.Code)
	assert.Equal(t, "weights", got.Body.String())
}

func TestGroupMerge_HuggingFaceTreeUnionsMembers(t *testing.T) {
	r := buildHFGroupEngine(t, "hf-group", "hf1", "hf2")
	putHFFile(t, r, "hf1", "/myns/mymodel/resolve/main/config.json", "{}")
	putHFFile(t, r, "hf2", "/myns/mymodel/resolve/main/onnx/model.onnx", "weights")

	w := get(r, "/repository/hf-group/api/models/myns/mymodel/tree/main?recursive=True")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var entries []struct {
		Type string `json:"type"`
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))
	paths := map[string]int64{}
	for _, e := range entries {
		paths[e.Path] = e.Size
	}
	assert.Equal(t, map[string]int64{"config.json": 2, "onnx/model.onnx": 7}, paths)
}

// A member holding nothing for this repo_id answers 404 and is skipped, rather
// than contributing an empty file list that hides the member behind it.
func TestGroupMerge_HuggingFaceSkipsMembersWithoutTheRepo(t *testing.T) {
	r := buildHFGroupEngine(t, "hf-group", "empty", "hf2")
	putHFFile(t, r, "hf2", "/myns/mymodel/resolve/main/config.json", "{}")

	w := get(r, "/repository/hf-group/api/models/myns/mymodel")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "config.json")

	// No member has it at all → the group says so.
	assert.Equal(t, http.StatusNotFound, get(r, "/repository/hf-group/api/models/myns/absent").Code)
}
