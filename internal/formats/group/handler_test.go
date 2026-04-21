package group_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/raw"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { gin.SetMode(gin.TestMode) }

func makeGroupRepo(name string, members ...string) *domain.Repository {
	memberSlice := make([]interface{}, len(members))
	for i, m := range members {
		memberSlice[i] = m
	}
	return &domain.Repository{
		ID: "repo-" + name, Name: name, Format: "raw",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": memberSlice},
	}
}

func buildEngine(repos ...*domain.Repository) *gin.Engine {
	repoRepo := testutil.NewRepoRepo(repos...)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}

	rawH := raw.New(d)
	registry := map[string]formats.FormatHandler{"raw": rawH}
	groupH := group.New(d, registry)

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repoName := c.Param("repoName")
		repo, _ := repoRepo.Get(c.Request.Context(), repoName)
		if repo == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
		} else {
			rawH.ServeHTTP(c)
		}
	})
	return r
}

func put(r *gin.Engine, repoName, path, body string) int {
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+path, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestGroup_FirstMemberServes(t *testing.T) {
	m1 := testutil.SimpleRepo("m1", "raw")
	m2 := testutil.SimpleRepo("m2", "raw")
	grp := makeGroupRepo("grp", "m1", "m2")
	r := buildEngine(m1, m2, grp)

	require.Equal(t, http.StatusCreated, put(r, "m1", "/file.txt", "hello from m1"))

	req := httptest.NewRequest(http.MethodGet, "/repository/grp/file.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "hello from m1", w.Body.String())
	assert.Equal(t, "m1", w.Header().Get("X-Nexspence-Source"))
}

func TestGroup_SkipsFirstMember_UsesSecond(t *testing.T) {
	m1 := testutil.SimpleRepo("skip1", "raw") // empty
	m2 := testutil.SimpleRepo("skip2", "raw") // has file
	grp := makeGroupRepo("grp2", "skip1", "skip2")
	r := buildEngine(m1, m2, grp)

	require.Equal(t, http.StatusCreated, put(r, "skip2", "/artifact.bin", "from second"))

	req := httptest.NewRequest(http.MethodGet, "/repository/grp2/artifact.bin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "from second", w.Body.String())
	assert.Equal(t, "skip2", w.Header().Get("X-Nexspence-Source"))
}

func TestGroup_AllMissing_Returns404(t *testing.T) {
	m1 := testutil.SimpleRepo("nn1", "raw")
	m2 := testutil.SimpleRepo("nn2", "raw")
	grp := makeGroupRepo("grp3", "nn1", "nn2")
	r := buildEngine(m1, m2, grp)

	req := httptest.NewRequest(http.MethodGet, "/repository/grp3/missing.bin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGroup_PUT_Returns405(t *testing.T) {
	m := testutil.SimpleRepo("m-ro", "raw")
	grp := makeGroupRepo("grp-ro", "m-ro")
	r := buildEngine(m, grp)

	req := httptest.NewRequest(http.MethodPut, "/repository/grp-ro/file.txt", strings.NewReader("x"))
	req.ContentLength = 1
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestGroup_EmptyMembers_Returns404(t *testing.T) {
	grp := &domain.Repository{
		ID: "grp-empty", Name: "grp-empty", Format: "raw",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []interface{}{}},
	}
	r := buildEngine(grp)

	req := httptest.NewRequest(http.MethodGet, "/repository/grp-empty/x.bin", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGroup_HEAD_NoBody(t *testing.T) {
	m := testutil.SimpleRepo("hm", "raw")
	grp := makeGroupRepo("grp-head", "hm")
	r := buildEngine(m, grp)

	require.Equal(t, http.StatusCreated, put(r, "hm", "/check.txt", "content"))

	req := httptest.NewRequest(http.MethodHead, "/repository/grp-head/check.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())
}
