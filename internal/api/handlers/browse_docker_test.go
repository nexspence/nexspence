package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// mountBrowse wires a BrowseHandler over mock repos to a test Gin engine.
// An admin-injecting middleware makes the RBAC FilterDockerRows/FilterPaths passes
// a passthrough (admins are never filtered), so the success-path branches run fully.
func mountBrowse(t *testing.T) (*gin.Engine, *testutil.RepoRepo, *testutil.ComponentRepo, *testutil.AssetRepo, *testutil.BlobStoreRepo, *testutil.BlobStore) {
	t.Helper()
	repos := testutil.NewRepoRepo()
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	blobs := testutil.NewBlobStoreRepo()
	store := testutil.NewBlobStore()
	rbacSvc := service.NewRBACService(emptyRBACRepo{}, repos, zap.NewNop().Sugar(), true)
	h := handlers.NewBrowseHandler(formats.Deps{
		Repos:      repos,
		Components: comps,
		Assets:     assets,
		Blobs:      blobs,
		BlobStore:  store,
	}, rbacSvc)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "admin")
		c.Set("roles", []string{"nx-admin"})
		c.Next()
	})
	r.GET("/api/v1/browse/repositories/:name/docker-tree", h.DockerTree)
	r.GET("/api/v1/browse/repositories/:name/oci-referrers", h.OCIReferrers)
	r.GET("/api/v1/browse/repositories/:name/path-tree", h.PathTree)
	r.DELETE("/api/v1/browse/repositories/:name/path", h.DeleteByPath)
	r.DELETE("/api/v1/browse/repositories/:name/docker-tag", h.DeleteDockerTag)
	r.DELETE("/api/v1/browse/repositories/:name/docker-image", h.DeleteDockerImage)
	r.GET("/api/v1/browse/repositories/:name/raw-tree", h.RawTree)
	return r, repos, comps, assets, blobs, store
}

// scopedRBACRepo grants the given privileges to every user — the stand-in for
// a role whose privilege carries a content selector.
type scopedRBACRepo struct {
	privs []repository.PrivilegeWithSelector
}

func (s scopedRBACRepo) GetUserPrivilegesWithSelectors(_ context.Context, _ string) ([]repository.PrivilegeWithSelector, error) {
	return s.privs, nil
}

// mountBrowseScoped mounts the browse handler for a NON-admin caller whose
// privileges carry a content selector, so the RBAC filter actually filters.
// Anonymous access is off: the filter is what decides what the caller sees.
func mountBrowseScoped(t *testing.T, privs []repository.PrivilegeWithSelector) (*gin.Engine, *testutil.RepoRepo, *testutil.ComponentRepo) {
	t.Helper()
	repos := testutil.NewRepoRepo()
	comps := testutil.NewComponentRepo()
	rbacSvc := service.NewRBACService(scopedRBACRepo{privs}, repos, zap.NewNop().Sugar(), false)
	h := handlers.NewBrowseHandler(formats.Deps{
		Repos:      repos,
		Components: comps,
		Assets:     testutil.NewAssetRepo(),
		Blobs:      testutil.NewBlobStoreRepo(),
		BlobStore:  testutil.NewBlobStore(),
	}, rbacSvc)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "scoped")
		c.Set("roles", []string{"reader"})
		c.Next()
	})
	r.GET("/api/v1/browse/repositories/:name/docker-tree", h.DockerTree)
	return r, repos, comps
}

// browseNode mirrors the JSON shape of dockerBrowseNode / rawBrowseNode for assertions.
type browseNode struct {
	Kind         string `json:"kind"`
	Label        string `json:"label"`
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	ImageRef     string `json:"imageRef"`
	Version      string `json:"version"`
	ComponentID  string `json:"componentId"`
	ArtifactType string `json:"artifactType"`
	// Lazy marks a node whose content is loaded on demand — an image node in
	// the name level of the Docker tree.
	Lazy     bool         `json:"lazy"`
	Children []browseNode `json:"children"`
}

type browseTreeResp struct {
	Repository string     `json:"repository"`
	Format     string     `json:"format"`
	Root       browseNode `json:"root"`
}

// findChild returns the first child with the given label, or zero value + false.
func findChild(n browseNode, label string) (browseNode, bool) {
	for _, ch := range n.Children {
		if ch.Label == label {
			return ch, true
		}
	}
	return browseNode{}, false
}

// ── DockerTree ────────────────────────────────────────────────

// The Docker tree is loaded by levels: without a parameter it holds the IMAGE
// NAMES only — each one a node the caller expands — and the content of one
// image comes on request (#777).
func TestBrowse_DockerTree_OK(t *testing.T) {
	r, repos, comps, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})
	comps.DockerRowsByRepo = map[string][]domain.DockerBrowseRow{
		"docker-host": {
			{ComponentID: "c1", ImageName: "da/python", Version: "3.12", SamplePath: "/manifests/da/python/3.12"},
		},
	}

	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/docker-host/docker-tree", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "docker-host", got.Repository)
	assert.Equal(t, "docker", got.Format)
	assert.Equal(t, "folder", got.Root.Kind)

	// /da/ → python — an image node: it names the image and is expandable, but
	// its content is not inlined into the name level.
	da, ok := findChild(got.Root, "da")
	require.True(t, ok)
	python, ok := findChild(da, "python")
	require.True(t, ok)
	assert.Equal(t, "da/python", python.ImageRef)
	assert.True(t, python.Lazy, "an image node is loaded on demand")
	assert.Empty(t, python.Children, "the image content does not belong to the name level")

	// The content of one image comes on request.
	rec = do(t, r, http.MethodGet, "/api/v1/browse/repositories/docker-host/docker-tree?image=da/python", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var img browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &img))
	assert.Equal(t, "folder", img.Root.Kind)
	tags, ok := findChild(img.Root, "Tags")
	require.True(t, ok, "version 3.12 is a tag, not a digest")
	leaf, ok := findChild(tags, "3.12")
	require.True(t, ok)
	assert.Equal(t, "tag", leaf.Kind)
	assert.Equal(t, "da/python", leaf.ImageRef)
	assert.Equal(t, "/da/python/Tags/3.12", leaf.Path)
	assert.Equal(t, "c1", leaf.ComponentID)
}

// The content of one image holds that image's rows only: the rows of every
// other image stay out of the response.
func TestBrowse_DockerTree_ImageLevelHoldsOneImage(t *testing.T) {
	r, repos, comps, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})
	comps.DockerRowsByRepo = map[string][]domain.DockerBrowseRow{
		"docker-host": {
			{ComponentID: "c1", ImageName: "img-a", Version: "1.0", SamplePath: "/manifests/img-a/1.0"},
			{ComponentID: "c2", ImageName: "img-b", Version: "2.0", SamplePath: "/manifests/img-b/2.0"},
		},
	}

	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/docker-host/docker-tree?image=img-a", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

	tags, ok := findChild(got.Root, "Tags")
	require.True(t, ok)
	leaf, ok := findChild(tags, "1.0")
	require.True(t, ok)
	assert.Equal(t, "c1", leaf.ComponentID)

	_, hasOther := findChild(got.Root, "img-b")
	assert.False(t, hasOther, "another image must not ride along")
}

// An image the caller may not see answers like an image that does not exist:
// an empty tree, so a missing image and a hidden one are indistinguishable.
func TestBrowse_DockerTree_ImageLevel_HiddenImageIsEmpty(t *testing.T) {
	r, repos, comps := mountBrowseScoped(t, []repository.PrivilegeWithSelector{{
		Actions:    []string{"browse", "read"},
		Expression: `repository == "docker-host" && path.startsWith("/z-team/")`,
	}})
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})
	comps.DockerRowsByRepo = map[string][]domain.DockerBrowseRow{
		"docker-host": {
			{ComponentID: "c1", ImageName: "z-team/runtime", Version: "1.0", SamplePath: "/manifests/z-team/runtime/1.0"},
			{ComponentID: "c2", ImageName: "a-img-000", Version: "1.0", SamplePath: "/manifests/a-img-000/1.0"},
		},
	}

	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/docker-host/docker-tree?image=a-img-000", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got.Root.Children, "a hidden image answers empty")
}

// A caller whose content selector covers one namespace sees exactly the images
// under it — the tree is filtered BEFORE it is built, so the answer is never
// an empty tree over a cut-off list (#777).
func TestBrowse_DockerTree_PathScopedUserSeesOnlyItsImages(t *testing.T) {
	r, repos, comps := mountBrowseScoped(t, []repository.PrivilegeWithSelector{{
		Actions:    []string{"browse", "read"},
		Expression: `repository == "docker-host" && path.startsWith("/z-team/")`,
	}})
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})
	comps.DockerRowsByRepo = map[string][]domain.DockerBrowseRow{
		"docker-host": {
			{ComponentID: "c1", ImageName: "z-team/runtime", Version: "1.0", SamplePath: "/manifests/z-team/runtime/1.0"},
			{ComponentID: "c2", ImageName: "a-img-000", Version: "1.0", SamplePath: "/manifests/a-img-000/1.0"},
		},
	}

	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/docker-host/docker-tree", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

	zteam, ok := findChild(got.Root, "z-team")
	require.True(t, ok, "the caller's own namespace must be there")
	runtime, ok := findChild(zteam, "runtime")
	require.True(t, ok)
	assert.Equal(t, "z-team/runtime", runtime.ImageRef)

	_, hasOther := findChild(got.Root, "a-img-000")
	assert.False(t, hasOther, "an image outside the selector must not be listed")
}

// The name level counts IMAGES, not components: a repository with more
// components than the old 3000-row cut still lists every one of them (#777).
func TestBrowse_DockerTree_ManyImagesAreNotCut(t *testing.T) {
	r, repos, comps, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})

	const images, blobsPerImage = 400, 8
	rows := make([]domain.DockerBrowseRow, 0, images*blobsPerImage)
	for i := 0; i < images; i++ {
		name := fmt.Sprintf("a-img-%03d", i)
		for j := 0; j < blobsPerImage; j++ {
			rows = append(rows, domain.DockerBrowseRow{
				ComponentID: fmt.Sprintf("c-%s-%d", name, j),
				ImageName:   name,
				Version:     fmt.Sprintf("sha256:%s-%d", name, j),
				SamplePath:  fmt.Sprintf("/blobs/%s/sha256:%s-%d", name, name, j),
			})
		}
	}
	require.Greater(t, len(rows), 3000, "the fixture must outgrow the old cut")
	comps.DockerRowsByRepo = map[string][]domain.DockerBrowseRow{"docker-host": rows}

	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/docker-host/docker-tree", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Len(t, got.Root.Children, images, "every image must be listed")
}

// TestBrowse_DockerTree_Categories drives dockerBrowseCategory across all three
// classifications: a tag (Tags), a digest manifest (Manifests), and a blob (Blobs).
func TestBrowse_DockerTree_Categories(t *testing.T) {
	r, repos, comps, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})
	comps.DockerRowsByRepo = map[string][]domain.DockerBrowseRow{
		"docker-host": {
			{ComponentID: "t", ImageName: "img", Version: "latest", SamplePath: "/manifests/img/latest"},
			{ComponentID: "m", ImageName: "img", Version: "sha256:deadbeef", SamplePath: "/manifests/img/sha256:deadbeef"},
			{ComponentID: "b", ImageName: "img", Version: "sha256:layerblob", SamplePath: "/blobs/img/sha256:layerblob"},
		},
	}

	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/docker-host/docker-tree?image=img", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

	tags, ok := findChild(got.Root, "Tags")
	require.True(t, ok)
	tag, ok := findChild(tags, "latest")
	require.True(t, ok)
	assert.Equal(t, "tag", tag.Kind)

	manifests, ok := findChild(got.Root, "Manifests")
	require.True(t, ok)
	man, ok := findChild(manifests, "sha256:deadbeef")
	require.True(t, ok)
	assert.Equal(t, "manifest", man.Kind)

	blobs, ok := findChild(got.Root, "Blobs")
	require.True(t, ok)
	blob, ok := findChild(blobs, "sha256:layerblob")
	require.True(t, ok)
	assert.Equal(t, "blob", blob.Kind)
}

// TestBrowse_DockerTree_DuplicateRow exercises the leaf-dedup short-circuit in
// insertDockerBrowseRow: two identical rows produce a single leaf.
func TestBrowse_DockerTree_DuplicateRow(t *testing.T) {
	r, repos, comps, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})
	row := domain.DockerBrowseRow{ComponentID: "c1", ImageName: "img", Version: "1.0", SamplePath: "/manifests/img/1.0"}
	comps.DockerRowsByRepo = map[string][]domain.DockerBrowseRow{"docker-host": {row, row}}

	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/docker-host/docker-tree?image=img", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	tags, ok := findChild(got.Root, "Tags")
	require.True(t, ok)
	assert.Len(t, tags.Children, 1, "duplicate rows must collapse to one leaf")
}

func TestBrowse_DockerTree_GroupExpansion(t *testing.T) {
	r, repos, comps, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "m1", Name: "dk-a", Format: domain.FormatDocker, Type: domain.TypeHosted})
	seedRepo(t, repos, &domain.Repository{ID: "m2", Name: "dk-b", Format: domain.FormatDocker, Type: domain.TypeHosted})
	seedRepo(t, repos, &domain.Repository{
		ID: "g1", Name: "dk-group", Format: domain.FormatDocker, Type: domain.TypeGroup,
		FormatConfig: map[string]any{"member_names": []string{"dk-a", "dk-b"}},
	})
	comps.DockerRowsByRepo = map[string][]domain.DockerBrowseRow{
		"dk-a": {{ComponentID: "ca", ImageName: "img-a", Version: "1.0", SamplePath: "/manifests/img-a/1.0"}},
		"dk-b": {{ComponentID: "cb", ImageName: "img-b", Version: "2.0", SamplePath: "/manifests/img-b/2.0"}},
		// elsewhere row must NOT appear:
		"dk-other": {{ComponentID: "cx", ImageName: "img-x", Version: "9.9", SamplePath: "/manifests/img-x/9.9"}},
	}

	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/dk-group/docker-tree", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	_, hasA := findChild(got.Root, "img-a")
	_, hasB := findChild(got.Root, "img-b")
	_, hasX := findChild(got.Root, "img-x")
	assert.True(t, hasA, "union must include member dk-a")
	assert.True(t, hasB, "union must include member dk-b")
	assert.False(t, hasX, "non-member repos must be excluded")
}

func TestBrowse_DockerTree_GroupNoMembers_EmptyOK(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "g1", Name: "empty-dk-group", Format: domain.FormatDocker, Type: domain.TypeGroup})
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/empty-dk-group/docker-tree", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got browseTreeResp
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got.Root.Children)
}

func TestBrowse_DockerTree_RepoNotFound_404(t *testing.T) {
	r, _, _, _, _, _ := mountBrowse(t)
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/ghost/docker-tree", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestBrowse_DockerTree_RepoError_404(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	repos.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/any/docker-tree", nil)
	// handler treats (err != nil || repo == nil) as not found.
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestBrowse_DockerTree_WrongFormat_400(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/raw-host/docker-tree", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBrowse_DockerTree_ListError_500(t *testing.T) {
	r, repos, comps, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})
	comps.DockerBrowseErr = errors.New("query failed")
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/docker-host/docker-tree", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── PathTree ──────────────────────────────────────────────────

func TestBrowse_PathTree_Raw_OK(t *testing.T) {
	r, repos, _, assets, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "raw-host", Path: "/a/b/file.txt"}))
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/raw-host/path-tree", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got struct {
		Paths []string `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Contains(t, got.Paths, "/a/")
	assert.Contains(t, got.Paths, "/a/b/")
}

func TestBrowse_PathTree_Docker_OK(t *testing.T) {
	r, repos, _, assets, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "docker-host", Path: "/manifests/da/bas/python/latest"}))
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/docker-host/path-tree", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got struct {
		Paths []string `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	// dockerImageDirs strips /manifests/ and the last segment, yielding namespace dirs.
	assert.Contains(t, got.Paths, "/da/")
	assert.Contains(t, got.Paths, "/da/bas/")
	assert.Contains(t, got.Paths, "/da/bas/python/")
}

func TestBrowse_PathTree_RepoNotFound_404(t *testing.T) {
	r, _, _, _, _, _ := mountBrowse(t)
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/ghost/path-tree", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestBrowse_PathTree_DockerListError_500(t *testing.T) {
	r, repos, _, assets, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})
	assets.BrowseErr = errors.New("list raw paths failed")
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/docker-host/path-tree", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestBrowse_PathTree_RawListError_500(t *testing.T) {
	r, repos, _, assets, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	assets.BrowseErr = errors.New("list paths failed")
	rec := do(t, r, http.MethodGet, "/api/v1/browse/repositories/raw-host/path-tree", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── DeleteByPath ──────────────────────────────────────────────

func TestBrowse_DeleteByPath_OK(t *testing.T) {
	r, repos, _, assets, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "raw-host", Path: "/keep/a.txt"}))
	require.NoError(t, assets.Create(testContext(), &domain.Asset{Repository: "raw-host", Path: "/drop/b.txt"}))
	rec := do(t, r, http.MethodDelete, "/api/v1/browse/repositories/raw-host/path?path=/drop/", nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestBrowse_DeleteByPath_MissingParam_400(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	rec := do(t, r, http.MethodDelete, "/api/v1/browse/repositories/raw-host/path", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBrowse_DeleteByPath_ListError_500(t *testing.T) {
	r, repos, _, assets, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "r1", Name: "raw-host", Format: domain.FormatRaw, Type: domain.TypeHosted})
	assets.BrowseErr = errors.New("list failed")
	rec := do(t, r, http.MethodDelete, "/api/v1/browse/repositories/raw-host/path?path=/x/", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── DeleteDockerTag ───────────────────────────────────────────

func TestBrowse_DeleteDockerTag_MissingParam_400(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})
	rec := do(t, r, http.MethodDelete, "/api/v1/browse/repositories/docker-host/docker-tag?image=da/python", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBrowse_DeleteDockerTag_ManifestNotFound_404(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})
	rec := do(t, r, http.MethodDelete, "/api/v1/browse/repositories/docker-host/docker-tag?image=da/python&ref=3.12", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestBrowse_DeleteDockerTag_OK(t *testing.T) {
	r, repos, _, assets, _, store := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})

	// Manifest blob references one layer digest.
	manifestJSON := `{"config":{"digest":"sha256:cfg"},"layers":[{"digest":"sha256:layer1"}]}`
	require.NoError(t, store.PutBytes(testContext(), "blob-manifest", []byte(manifestJSON)))
	require.NoError(t, store.PutBytes(testContext(), "blob-cfg", []byte("c")))
	require.NoError(t, store.PutBytes(testContext(), "blob-layer1", []byte("l")))

	require.NoError(t, assets.Create(testContext(), &domain.Asset{
		Repository: "docker-host", Path: "/manifests/da/python/3.12", BlobKey: "blob-manifest", SHA256: "tagsha",
	}))
	require.NoError(t, assets.Create(testContext(), &domain.Asset{
		Repository: "docker-host", Path: "/blobs/da/python/sha256:cfg", BlobKey: "blob-cfg",
	}))
	require.NoError(t, assets.Create(testContext(), &domain.Asset{
		Repository: "docker-host", Path: "/blobs/da/python/sha256:layer1", BlobKey: "blob-layer1",
	}))

	rec := do(t, r, http.MethodDelete, "/api/v1/browse/repositories/docker-host/docker-tag?image=da/python&ref=3.12", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	// manifest + both unreferenced layer blobs deleted.
	assert.Contains(t, store.Deleted, "blob-manifest")
	assert.Contains(t, store.Deleted, "blob-cfg")
	assert.Contains(t, store.Deleted, "blob-layer1")
}

// ── DeleteDockerImage ─────────────────────────────────────────

func TestBrowse_DeleteDockerImage_MissingParam_400(t *testing.T) {
	r, repos, _, _, _, _ := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})
	rec := do(t, r, http.MethodDelete, "/api/v1/browse/repositories/docker-host/docker-image", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBrowse_DeleteDockerImage_OK(t *testing.T) {
	r, repos, _, assets, _, store := mountBrowse(t)
	seedRepo(t, repos, &domain.Repository{ID: "d1", Name: "docker-host", Format: domain.FormatDocker, Type: domain.TypeHosted})
	require.NoError(t, store.PutBytes(testContext(), "blob-m", []byte("m")))
	require.NoError(t, store.PutBytes(testContext(), "blob-l", []byte("l")))
	require.NoError(t, assets.Create(testContext(), &domain.Asset{
		Repository: "docker-host", Path: "/manifests/da/python/3.12", BlobKey: "blob-m",
	}))
	require.NoError(t, assets.Create(testContext(), &domain.Asset{
		Repository: "docker-host", Path: "/blobs/da/python/sha256:l", BlobKey: "blob-l",
	}))

	rec := do(t, r, http.MethodDelete, "/api/v1/browse/repositories/docker-host/docker-image?image=da/python", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Contains(t, store.Deleted, "blob-m")
	assert.Contains(t, store.Deleted, "blob-l")
}
