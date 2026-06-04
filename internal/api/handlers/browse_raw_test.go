package handlers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// ── RawTree ───────────────────────────────────────────────────

func TestBrowse_RawTree_OK(t *testing.T) {
	r, repos, _, assets, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	assets.RawRowsByRepo = map[string][]domain.RawBrowseAsset{
		"raw-host": {
			{Path: "/docs/readme.txt", SizeBytes: 12, SHA256: "abc", ContentType: "text/plain", ComponentID: "c1"},
			{Path: "/docs/guide.txt", SizeBytes: 34},
		},
	}

	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/raw-host/raw-tree", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "raw-host", got.Repository)
	assert.Equal(t, "raw", got.Format)

	docs, ok := findChild(got.Root, "docs")
	require.True(t, ok)
	assert.Equal(t, "folder", docs.Kind)
	assert.Equal(t, "/docs", docs.Path)

	readme, ok := findChild(docs, "readme.txt")
	require.True(t, ok)
	assert.Equal(t, "file", readme.Kind)
	assert.Equal(t, "/docs/readme.txt", readme.Path)
	assert.Equal(t, int64(12), readme.Size)
	assert.Equal(t, "abc", readme.SHA256)
	assert.Equal(t, "c1", readme.ComponentID)

	// both files present under the same folder.
	_, hasGuide := findChild(docs, "guide.txt")
	assert.True(t, hasGuide)
}

func TestBrowse_RawTree_GroupExpansion(t *testing.T) {
	r, repos, _, assets, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "m1", Name: "raw-a", Format: domain.FormatRaw, Type: domain.TypeHosted})
	seedRepo(t, repos, &domain.Repository{ID: "m2", Name: "raw-b", Format: domain.FormatRaw, Type: domain.TypeHosted})
	seedRepo(t, repos, &domain.Repository{
		ID: "g1", Name: "raw-group", Format: domain.FormatRaw, Type: domain.TypeGroup,
		FormatConfig: map[string]any{"member_names": []string{"raw-a", "raw-b"}},
	})
	assets.RawRowsByRepo = map[string][]domain.RawBrowseAsset{
		"raw-a":     {{Path: "/from-a.txt"}},
		"raw-b":     {{Path: "/from-b.txt"}},
		"raw-other": {{Path: "/outsider.txt"}},
	}

	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/raw-group/raw-tree", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	_, hasA := findChild(got.Root, "from-a.txt")
	_, hasB := findChild(got.Root, "from-b.txt")
	_, hasOut := findChild(got.Root, "outsider.txt")
	assert.True(t, hasA, "union must include raw-a")
	assert.True(t, hasB, "union must include raw-b")
	assert.False(t, hasOut, "non-member repos excluded")
}

func TestBrowse_RawTree_GroupNoMembers_EmptyOK(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "g1", Name: "empty-raw-group", Format: domain.FormatRaw, Type: domain.TypeGroup})
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/empty-raw-group/raw-tree", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got.Root.Children)
}

func TestBrowse_RawTree_EmptyTree_OK(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/raw-host/raw-tree", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got.Root.Children)
}

func TestBrowse_RawTree_RepoNotFound_404(t *testing.T) {
	r, _, _, _, _, _ := mountBrowse(t)
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/ghost/raw-tree", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestBrowse_RawTree_WrongFormat_400(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/docker-host/raw-tree", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBrowse_RawTree_ListError_500(t *testing.T) {
	r, repos, _, assets, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	assets.RawBrowseErr = errors.New("query failed")
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/raw-host/raw-tree", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
