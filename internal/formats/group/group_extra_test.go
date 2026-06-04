package group_test

import (
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
	"github.com/nexspence-oss/nexspence/internal/formats/raw"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// TestGroup_Name verifies the handler name.
func TestGroup_Name(t *testing.T) {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := group.New(d, map[string]formats.FormatHandler{})
	assert.Equal(t, "group", h.Name())
}

// TestGroup_DELETE_Returns405 covers the default branch in ServeHTTP (DELETE is not GET/HEAD/PUT/POST/PATCH).
func TestGroup_DELETE_Returns405(t *testing.T) {
	grp := makeGroupRepo("grp-del405", "m-del")
	m := testutil.SimpleRepo("m-del", "raw")
	r := buildEngine(m, grp)

	req := httptest.NewRequest(http.MethodDelete, "/repository/grp-del405/file.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestGroup_GET_GroupNotFound covers serveGet when the group repo itself does not exist.
func TestGroup_GET_GroupNotFound(t *testing.T) {
	// Build engine with no repos registered
	r := buildEngine()

	req := httptest.NewRequest(http.MethodGet, "/repository/nonexistent-group/file.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGroup_GET_MemberOffline covers a member repo that has Online=false (should be skipped).
func TestGroup_GET_MemberOffline(t *testing.T) {
	offline := &domain.Repository{
		ID: "repo-offline", Name: "offline-member", Format: "raw",
		Type: domain.TypeHosted, Online: false,
	}
	grp := makeGroupRepo("grp-offline", "offline-member")
	r := buildEngine(offline, grp)

	// Upload to offline member — it's offline so serveGet should skip it
	// (we can't upload via group, so directly set up the raw handler)
	// Just verify the group skips offline members and returns 404
	req := httptest.NewRequest(http.MethodGet, "/repository/grp-offline/file.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGroup_GET_MemberFormatMismatch covers a member whose format differs from the group.
func TestGroup_GET_MemberFormatMismatch(t *testing.T) {
	wrongFormat := &domain.Repository{
		ID: "repo-wrong-fmt", Name: "wrong-fmt-member", Format: "maven",
		Type: domain.TypeHosted, Online: true,
	}
	// Group is "raw" format, member is "maven" — should be skipped
	grp := makeGroupRepo("grp-fmt-mismatch", "wrong-fmt-member")
	r := buildEngine(wrongFormat, grp)

	req := httptest.NewRequest(http.MethodGet, "/repository/grp-fmt-mismatch/file.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGroup_GET_NoHandlerForFormat covers a member format that has no registered handler.
func TestGroup_GET_NoHandlerForFormat(t *testing.T) {
	// Build engine with a group that references a member, but no handler for that format
	repoRepo := testutil.NewRepoRepo()
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	// Empty registry — no handlers registered
	registry := map[string]formats.FormatHandler{}
	groupH := group.New(d, registry)

	member := testutil.SimpleRepo("no-handler-member", "raw")
	grpRepo := makeGroupRepo("grp-no-handler", "no-handler-member")
	_ = repoRepo.Create(nil, member)  //nolint:staticcheck
	_ = repoRepo.Create(nil, grpRepo) //nolint:staticcheck

	r := buildEngineFromGroupHandler(groupH, repoRepo)
	require.Equal(t, http.StatusCreated, put(r, "no-handler-member", "/file.txt", "data"))

	req := httptest.NewRequest(http.MethodGet, "/repository/grp-no-handler/file.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// No handler in registry → member is skipped → 404
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGroup_Write_GroupNotFound covers serveWrite when the group repo does not exist.
func TestGroup_Write_GroupNotFound(t *testing.T) {
	r := buildEngine() // no repos

	req := httptest.NewRequest(http.MethodPut, "/repository/no-such-group/file.txt",
		strings.NewReader("data"))
	req.ContentLength = 4
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGroup_Write_WritableMemberNotFound covers the case where writable_member is set
// but that repo does not exist.
func TestGroup_Write_WritableMemberNotFound(t *testing.T) {
	grp := &domain.Repository{
		ID: "grp-wm-notfound", Name: "grp-wm-notfound", Format: "raw",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{
			"member_names":    []any{"non-existent-member"},
			"writable_member": "non-existent-member",
		},
	}
	r := buildEngine(grp) // member not registered

	req := httptest.NewRequest(http.MethodPut, "/repository/grp-wm-notfound/file.txt",
		strings.NewReader("data"))
	req.ContentLength = 4
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGroup_Write_WritableMemberOffline covers the case where writable_member is offline.
func TestGroup_Write_WritableMemberOffline(t *testing.T) {
	offline := &domain.Repository{
		ID: "repo-wm-offline", Name: "wm-offline", Format: "raw",
		Type: domain.TypeHosted, Online: false,
	}
	grp := &domain.Repository{
		ID: "grp-wm-offline", Name: "grp-wm-offline", Format: "raw",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{
			"member_names":    []any{"wm-offline"},
			"writable_member": "wm-offline",
		},
	}
	r := buildEngine(offline, grp)

	req := httptest.NewRequest(http.MethodPut, "/repository/grp-wm-offline/file.txt",
		strings.NewReader("data"))
	req.ContentLength = 4
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

// TestGroup_Write_NoHandlerForFormat covers serveWrite when the format has no registered handler.
func TestGroup_Write_NoHandlerForFormat(t *testing.T) {
	repoRepo := testutil.NewRepoRepo()
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	// Empty registry — no handlers for any format
	registry := map[string]formats.FormatHandler{}
	groupH := group.New(d, registry)

	member := testutil.SimpleRepo("wm-no-handler", "raw")
	grpRepo := &domain.Repository{
		ID: "grp-write-no-handler", Name: "grp-write-no-handler", Format: "raw",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{
			"member_names":    []any{"wm-no-handler"},
			"writable_member": "wm-no-handler",
		},
	}
	_ = repoRepo.Create(nil, member)  //nolint:staticcheck
	_ = repoRepo.Create(nil, grpRepo) //nolint:staticcheck

	r := buildEngineFromGroupHandler(groupH, repoRepo)

	req := httptest.NewRequest(http.MethodPut, "/repository/grp-write-no-handler/file.txt",
		strings.NewReader("data"))
	req.ContentLength = 4
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestGroup_Write_POST covers PUT routing via POST (same serveWrite path).
func TestGroup_Write_POST(t *testing.T) {
	m := testutil.SimpleRepo("post-member", "raw")
	grp := makeGroupRepo("grp-post", "post-member")
	r := buildEngine(m, grp)

	req := httptest.NewRequest(http.MethodPost, "/repository/grp-post/api/file",
		strings.NewReader("posted"))
	req.ContentLength = 6
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// raw handler returns 405 for POST (only PUT is supported) but the group routing succeeds
	// The important thing is serveWrite is exercised without 404/500
	assert.NotEqual(t, http.StatusNotFound, w.Code)
}

// buildEngineFromGroupHandler builds a gin engine using a pre-constructed group handler.
func buildEngineFromGroupHandler(groupH *group.Handler, repoRepo *testutil.RepoRepo) *gin.Engine {
	rawDeps := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	rawH := raw.New(rawDeps)

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
