package oci_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ── Helpers ───────────────────────────────────────────────────

// mountDeps builds one handler over several repositories and returns both URL
// shapes production serves it under: the long /repository/<repo>/v2/... form and
// the short /v2/<repo>/... form. A mount names its source with a client-side
// repository name whose spelling depends on which of the two the client pushed
// against, so both have to be exercised against the same stored state.
func mountDeps(repos ...*domain.Repository) (long, short *gin.Engine, d formats.Deps, store *testutil.BlobStore) {
	return mountDepsRBAC(nil, repos...)
}

// mountDepsRBAC is mountDeps with a privilege checker wired in, and with the
// caller's identity put on the gin context the way OptionalAuth and
// RBACMiddleware leave it for the handler chain.
func mountDepsRBAC(rbac formats.RBACChecker, repos ...*domain.Repository) (long, short *gin.Engine, d formats.Deps, store *testutil.BlobStore) {
	store = testutil.NewBlobStore()
	d = formats.Deps{
		Repos:      testutil.NewRepoRepo(repos...),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  store,
		BaseURL:    "http://localhost:8080",
		RBAC:       rbac,
	}
	h := oci.New(d)

	auth := func(c *gin.Context) {
		ctx := requestctx.WithUser(c.Request.Context(), "test-user-id", "testuser")
		c.Request = c.Request.WithContext(ctx)
		// Deliberately not "nx-admin": an admin short-circuits every check in
		// CanAccessRepo, which would make the selector tests prove nothing.
		c.Set("userID", "test-user-id")
		c.Set("roles", []string{"nx-developer"})
		c.Next()
	}

	long = gin.New()
	long.Use(auth)
	long.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	short = gin.New()
	short.Use(auth)
	short.Any("/v2/:repoName/*dockerpath", func(c *gin.Context) {
		c.Params = gin.Params{
			{Key: "repoName", Value: c.Param("repoName")},
			{Key: "path", Value: "/v2" + c.Param("dockerpath")},
		}
		h.ServeHTTP(c)
	})
	return long, short, d, store
}

// postMount issues the mount request itself: POST to the target image's upload
// endpoint carrying ?mount=&from=.
func postMount(r *gin.Engine, uploadsURL, dgst, from string) *httptest.ResponseRecorder {
	target := fmt.Sprintf("%s?mount=%s&from=%s", uploadsURL, dgst, from)
	req := httptest.NewRequest(http.MethodPost, target, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func blobStoreKeys(t *testing.T, store *testutil.BlobStore) []string {
	t.Helper()
	keys, err := store.ListKeys(context.Background())
	require.NoError(t, err)
	sort.Strings(keys)
	return keys
}

func usedBytes(t *testing.T, d formats.Deps) int64 {
	t.Helper()
	bs, err := d.Blobs.Get(context.Background(), "default")
	require.NoError(t, err)
	return bs.UsedBytes
}

// ── The happy path ────────────────────────────────────────────

// The point of a mount is that the client sends no bytes at all: the registry
// answers the POST with the finished blob instead of an upload session.
func TestMount_ExistingBlob_Returns201AndOpensNoUploadSession(t *testing.T) {
	repo := testutil.SimpleRepo("mnt1", "docker")
	r, _, _, store := mountDeps(repo)

	const layer = "a layer shared by two images"
	dgst := pushBlob(t, r, "mnt1", "library/alpine", layer)
	before := blobStoreKeys(t, store)

	w := postMount(r, "/repository/mnt1/v2/library/ubuntu/blobs/uploads/", dgst, "library/alpine")
	require.Equal(t, http.StatusCreated, w.Code, "a mount that can be served returns 201, not 202: %s", w.Body.String())
	assert.Equal(t, "/repository/mnt1/v2/library/ubuntu/blobs/"+dgst, w.Header().Get("Location"),
		"Location names the mounted blob under the same /v2/ prefix the client authenticated against")
	assert.Equal(t, dgst, w.Header().Get("Docker-Content-Digest"))

	// A 201 with an upload session behind it would leak a staged blob per mount;
	// sessions live under the _uploads/ prefix in the blob store.
	after := blobStoreKeys(t, store)
	assert.Equal(t, before, after, "a mount stores nothing new — no upload session, no second copy")
	for _, k := range after {
		assert.False(t, strings.HasPrefix(k, "_uploads/"), "no upload session may be created by a mount, found %q", k)
	}
}

// A mounted layer must be pullable from the target image, and must be the very
// same stored object: the blob key is what proves the bytes were not copied.
func TestMount_MountedBlobIsPullableAndSharesTheSourceBlobKey(t *testing.T) {
	repo := testutil.SimpleRepo("mnt2", "docker")
	r, _, d, _ := mountDeps(repo)

	const layer = "bytes that must never be uploaded twice"
	dgst := pushBlob(t, r, "mnt2", "library/alpine", layer)

	w := postMount(r, "/repository/mnt2/v2/library/ubuntu/blobs/uploads/", dgst, "library/alpine")
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	req := httptest.NewRequest(http.MethodGet, "/repository/mnt2/v2/library/ubuntu/blobs/"+dgst, nil)
	pull := httptest.NewRecorder()
	r.ServeHTTP(pull, req)
	require.Equal(t, http.StatusOK, pull.Code, "the mounted blob must be pullable from the target image")
	assert.Equal(t, layer, pull.Body.String())

	ctx := context.Background()
	src, err := d.Assets.GetByPath(ctx, "mnt2", "/blobs/library/alpine/"+dgst)
	require.NoError(t, err)
	dst, err := d.Assets.GetByPath(ctx, "mnt2", "/blobs/library/ubuntu/"+dgst)
	require.NoError(t, err, "the mount registers an asset under the target image")

	assert.Equal(t, src.BlobKey, dst.BlobKey,
		"the mounted asset points at the source's stored object; a different key means the bytes were duplicated")
	assert.NotEqual(t, base.BlobKey("mnt2", "/blobs/library/ubuntu/"+dgst), dst.BlobKey,
		"a mount must not claim a key of its own — that key holds nothing")
	assert.Equal(t, src.SizeBytes, dst.SizeBytes)
	assert.Equal(t, src.SHA256, dst.SHA256)
	assert.Equal(t, src.SHA1, dst.SHA1)
	assert.Equal(t, src.MD5, dst.MD5)
	assert.Equal(t, src.BlobStoreID, dst.BlobStoreID,
		"the alias has to name the store the bytes actually live in, or the pull looks in the wrong place")
}

// A mount racing the delete of its source's last reference must not answer 201
// while the bytes are being freed: the client — told the registry has the
// layer — never uploads its own copy, and the first pull 404s. The mount's
// existence check and row insert must hold the blob-key lock the deleter holds.
func TestMount_RacingLastReferenceDelete_NeverServesDanglingRow(t *testing.T) {
	repo := testutil.SimpleRepo("mntrace", "docker")
	r, _, d, store := mountDeps(repo)
	ctx := context.Background()

	const layer = "bytes about to vanish"
	dgst := pushBlob(t, r, "mntrace", "library/alpine", layer)
	assets := d.Assets.(*testutil.AssetRepo)

	srcPath := "/blobs/library/alpine/" + dgst
	src, err := assets.GetByPath(ctx, "mntrace", srcPath)
	require.NoError(t, err)

	// Pause the deleter right after its reference count — it has just decided
	// "I am the last reference" — fire the mount during the pause, and let the
	// delete finish afterwards. When the lock serializes the two, the mount
	// blocks instead and the pause simply times out.
	var once sync.Once
	mountResult := make(chan *httptest.ResponseRecorder, 1)
	mountDone := make(chan struct{})
	assets.CountByBlobKeyHook = func(string, string) {
		once.Do(func() {
			go func() {
				mountResult <- postMount(r, "/repository/mntrace/v2/library/ubuntu/blobs/uploads/", dgst, "library/alpine")
				close(mountDone)
			}()
			select {
			case <-mountDone:
			case <-time.After(500 * time.Millisecond):
			}
		})
	}

	require.NoError(t, base.DeleteArtifact(ctx, d, "mntrace", srcPath))
	w := <-mountResult

	if w.Code == http.StatusCreated {
		assert.True(t, store.Has(src.BlobKey),
			"mount answered 201 but the bytes are gone — the client will never re-upload a layer it was told the registry has")
	}
	if dst, gerr := assets.GetByPath(ctx, "mntrace", "/blobs/library/ubuntu/"+dgst); gerr == nil && dst != nil {
		assert.True(t, store.Has(dst.BlobKey),
			"the mounted asset row points at bytes that no longer exist")
	}
}

// ── The `from` spellings a real client sends ──────────────────

// docker sets from=reference.Path(sourceRef) — everything after the registry
// host. Pushing against the short path form (host/<repo>/<image>) that is
// "<repo>/<image>", so the Nexspence repository arrives inside the value.
func TestMount_ShortPathForm_FromCarriesTheNexspenceRepository(t *testing.T) {
	repo := testutil.SimpleRepo("mnt3", "docker")
	long, short, _, _ := mountDeps(repo)

	const layer = "short-path layer"
	dgst := pushBlob(t, long, "mnt3", "library/alpine", layer)

	w := postMount(short, "/v2/mnt3/library/ubuntu/blobs/uploads/", dgst, "mnt3/library/alpine")
	require.Equal(t, http.StatusCreated, w.Code,
		"from=<repo>/<image> is what docker push against /v2/<repo>/<image> sends: %s", w.Body.String())
	assert.Equal(t, "/v2/mnt3/library/ubuntu/blobs/"+dgst, w.Header().Get("Location"))
}

// Against the long path form (host/repository/<repo>/<image>) the same rule
// yields "repository/<repo>/<image>".
func TestMount_LongPathForm_FromCarriesTheRepositoryPrefix(t *testing.T) {
	repo := testutil.SimpleRepo("mnt4", "docker")
	r, _, _, _ := mountDeps(repo)

	const layer = "long-path layer"
	dgst := pushBlob(t, r, "mnt4", "library/alpine", layer)

	w := postMount(r, "/repository/mnt4/v2/library/ubuntu/blobs/uploads/", dgst, "repository/mnt4/library/alpine")
	require.Equal(t, http.StatusCreated, w.Code,
		"from=repository/<repo>/<image> is what the long path form yields: %s", w.Body.String())
}

// An image whose own name begins with the repository name must still be
// mountable by its bare name: the subdomain connector rewrites only the path,
// so `from` arrives without any repository prefix at all.
func TestMount_BareImageNameThatShadowsTheRepositoryPrefix(t *testing.T) {
	repo := testutil.SimpleRepo("mnt5", "docker")
	r, _, _, _ := mountDeps(repo)

	const layer = "layer of an awkwardly named image"
	dgst := pushBlob(t, r, "mnt5", "mnt5/alpine", layer)

	// "mnt5/alpine" reads as both <repo>/alpine and as the literal image name.
	w := postMount(r, "/repository/mnt5/v2/library/ubuntu/blobs/uploads/", dgst, "mnt5/alpine")
	require.Equal(t, http.StatusCreated, w.Code,
		"the literal image name must be tried too, not only the repository-stripped reading: %s", w.Body.String())
}

// ── Fallbacks ─────────────────────────────────────────────────

// The spec lets a registry decline any mount; declining costs the client only
// bandwidth, so every case that cannot be served safely lands here.
func TestMount_UnknownSourceBlob_FallsBackToAWorkingUploadSession(t *testing.T) {
	repo := testutil.SimpleRepo("mnt6", "docker")
	r, _, _, _ := mountDeps(repo)

	const layer = "never pushed anywhere yet"
	dgst := digest(layer)

	w := postMount(r, "/repository/mnt6/v2/library/ubuntu/blobs/uploads/", dgst, "library/alpine")
	require.Equal(t, http.StatusAccepted, w.Code, "an unmountable blob falls back to a normal upload")
	loc := w.Header().Get("Location")
	require.NotEmpty(t, loc, "the fallback must hand back a fresh session to PATCH into")
	require.NotEmpty(t, w.Header().Get("Docker-Upload-UUID"))

	// And that session has to actually work end to end.
	patch := httptest.NewRequest(http.MethodPatch, loc, strings.NewReader(layer))
	patch.ContentLength = int64(len(layer))
	wp := httptest.NewRecorder()
	r.ServeHTTP(wp, patch)
	require.Equal(t, http.StatusAccepted, wp.Code)

	put := httptest.NewRequest(http.MethodPut, loc+"?digest="+dgst, nil)
	wput := httptest.NewRecorder()
	r.ServeHTTP(wput, put)
	require.Equal(t, http.StatusCreated, wput.Code, wput.Body.String())

	get := httptest.NewRequest(http.MethodGet, "/repository/mnt6/v2/library/ubuntu/blobs/"+dgst, nil)
	wg := httptest.NewRecorder()
	r.ServeHTTP(wg, get)
	require.Equal(t, http.StatusOK, wg.Code)
	assert.Equal(t, layer, wg.Body.String())
}

// A malformed digest is not a blob name; it must not be handed to a path lookup.
func TestMount_MalformedDigest_FallsBack(t *testing.T) {
	repo := testutil.SimpleRepo("mnt7", "docker")
	r, _, _, _ := mountDeps(repo)
	pushBlob(t, r, "mnt7", "library/alpine", "a layer")

	for _, bad := range []string{"sha256:deadbeef", "../../etc/passwd", "notadigest"} {
		w := postMount(r, "/repository/mnt7/v2/library/ubuntu/blobs/uploads/", bad, "library/alpine")
		assert.Equal(t, http.StatusAccepted, w.Code, "mount=%q must not be treated as a digest", bad)
	}
}

// ── The access boundary ───────────────────────────────────────

// RBACMiddleware authorizes the request against the repository in the URL, and
// the handler never sees the caller's privileges. So the mount source must stay
// inside that repository: naming another Nexspence repository — even one that
// really holds the blob — must not read out of it.
func TestMount_FromAnotherNexspenceRepository_IsRefused(t *testing.T) {
	target := testutil.SimpleRepo("mnt8", "docker")
	secret := testutil.SimpleRepo("secret", "docker")
	r, _, d, _ := mountDeps(target, secret)

	const layer = "a layer the caller was never authorized to read"
	dgst := pushBlob(t, r, "secret", "library/alpine", layer)

	for _, from := range []string{
		"secret/library/alpine",
		"repository/secret/library/alpine",
	} {
		w := postMount(r, "/repository/mnt8/v2/library/ubuntu/blobs/uploads/", dgst, from)
		assert.Equal(t, http.StatusAccepted, w.Code,
			"from=%q names a repository the request was not authorized against; it must not mount", from)
	}

	_, err := d.Assets.GetByPath(context.Background(), "mnt8", "/blobs/library/ubuntu/"+dgst)
	assert.Error(t, err, "nothing may be registered in the target repository from a source outside it")
}

// rbacRepoStub hands back one fixed privilege set, as the postgres RBACRepo
// would for a user holding those grants. The real *service.RBACService is driven
// on top of it so these tests exercise the actual selector evaluation and the
// actual OCI path normalisation, not a stand-in for them.
type rbacRepoStub struct {
	privs []repository.PrivilegeWithSelector
}

func (s rbacRepoStub) GetUserPrivilegesWithSelectors(context.Context, string) ([]repository.PrivilegeWithSelector, error) {
	return s.privs, nil
}

// publicOnlyRBAC grants the caller everything under /public/ in repoName, and
// nothing anywhere else.
func publicOnlyRBAC(repoName string) formats.RBACChecker {
	return service.NewRBACService(rbacRepoStub{privs: []repository.PrivilegeWithSelector{{
		Actions:    []string{"read", "browse", "write"},
		Expression: `repository == "` + repoName + `" && path.startsWith("/public/")`,
	}}}, nil, zap.NewNop().Sugar(), true)
}

// The security case. A content selector that stops a pull but not a mount is not
// a control at all: anyone holding the digest — and digests travel in manifests,
// build logs and CI output — could copy the blob into a path they can read and
// pull it from there.
func TestMount_SourceOutsideTheCallersSelector_IsRefusedWithoutRegistering(t *testing.T) {
	repo := testutil.SimpleRepo("mnt12", "docker")
	r, _, d, _ := mountDepsRBAC(publicOnlyRBAC("mnt12"), repo)

	const layer = "a layer only /private/ may read"
	dgst := pushBlob(t, r, "mnt12", "private/secret-image", layer)

	w := postMount(r, "/repository/mnt12/v2/public/app/blobs/uploads/", dgst, "private/secret-image")

	// 202 rather than 403: the fallback is spec-legal, costs the client only the
	// bandwidth it was going to spend, and — unlike a 403 — says nothing about
	// whether that digest is in the registry at all.
	assert.Equal(t, http.StatusAccepted, w.Code,
		"an unreadable source falls back to a normal upload; a 403 would confirm the blob is here")

	// The status alone proves nothing — a missing blob produces the same 202.
	// What must be true is that nothing was registered.
	_, err := d.Assets.GetByPath(context.Background(), "mnt12", "/blobs/public/app/"+dgst)
	assert.Error(t, err, "no asset may be registered from a source the caller cannot read")
}

// The mirror: the same caller, the same registry, a source its selector allows.
// Without this the test above would also pass on an implementation that simply
// refuses every mount.
func TestMount_SourceInsideTheCallersSelector_Mounts(t *testing.T) {
	repo := testutil.SimpleRepo("mnt13", "docker")
	r, _, d, _ := mountDepsRBAC(publicOnlyRBAC("mnt13"), repo)

	const layer = "a layer the caller may read"
	dgst := pushBlob(t, r, "mnt13", "public/base", layer)

	w := postMount(r, "/repository/mnt13/v2/public/app/blobs/uploads/", dgst, "public/base")
	require.Equal(t, http.StatusCreated, w.Code,
		"the selector allows /public/, so this mount must still be served: %s", w.Body.String())

	dst, err := d.Assets.GetByPath(context.Background(), "mnt13", "/blobs/public/app/"+dgst)
	require.NoError(t, err)
	src, err := d.Assets.GetByPath(context.Background(), "mnt13", "/blobs/public/base/"+dgst)
	require.NoError(t, err)
	assert.Equal(t, src.BlobKey, dst.BlobKey)
}

// The dependency is optional, like Webhooks and Downloads: a handler built
// without it keeps working. This is what lets every other test in the package
// construct formats.Deps by hand.
func TestMount_NilRBAC_StillMounts(t *testing.T) {
	repo := testutil.SimpleRepo("mnt14", "docker")
	r, _, d, _ := mountDepsRBAC(nil, repo)
	require.Nil(t, d.RBAC, "this test is only meaningful with no checker wired in")

	const layer = "no checker configured"
	dgst := pushBlob(t, r, "mnt14", "private/secret-image", layer)

	w := postMount(r, "/repository/mnt14/v2/public/app/blobs/uploads/", dgst, "private/secret-image")
	assert.Equal(t, http.StatusCreated, w.Code,
		"a nil checker must mean 'not configured', not 'deny everything': %s", w.Body.String())
}

// A registry-wide GC or a manual blob deletion can leave an asset row whose
// bytes are gone. Mounting off it would hand the client a 201 for a layer that
// 404s on pull, and the client would never upload the bytes it still has.
func TestMount_SourceAssetWithMissingBlob_FallsBackInsteadOfPromisingABrokenLayer(t *testing.T) {
	repo := testutil.SimpleRepo("mnt9", "docker")
	r, _, d, store := mountDeps(repo)

	const layer = "bytes about to vanish from the store"
	dgst := pushBlob(t, r, "mnt9", "library/alpine", layer)

	ctx := context.Background()
	src, err := d.Assets.GetByPath(ctx, "mnt9", "/blobs/library/alpine/"+dgst)
	require.NoError(t, err)
	require.NoError(t, store.Delete(ctx, src.BlobKey))

	w := postMount(r, "/repository/mnt9/v2/library/ubuntu/blobs/uploads/", dgst, "library/alpine")
	assert.Equal(t, http.StatusAccepted, w.Code,
		"the source asset survives but its bytes do not, so the mount cannot be honored")

	_, err = d.Assets.GetByPath(ctx, "mnt9", "/blobs/library/ubuntu/"+dgst)
	assert.Error(t, err, "no asset may point at a blob key that holds nothing")
}

// ── Usage accounting ──────────────────────────────────────────

// The bytes are already on disk, so a mount writes none — and used_bytes, which
// is what checkQuota reads to decide whether a write fits, is how full the store
// is. A second asset on a blob the store already holds moves it by nothing
// (issue #146).
func TestMount_WritesNoBytesAndCountsNoUsage(t *testing.T) {
	repo := testutil.SimpleRepo("mnt10", "docker")
	r, _, d, store := mountDeps(repo)

	const layer = "a layer worth counting exactly once"
	dgst := pushBlob(t, r, "mnt10", "library/alpine", layer)

	keysBefore := blobStoreKeys(t, store)
	physicalBefore, err := store.UsedBytes(context.Background())
	require.NoError(t, err)
	countedBefore := usedBytes(t, d)

	w := postMount(r, "/repository/mnt10/v2/library/ubuntu/blobs/uploads/", dgst, "library/alpine")
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	assert.Equal(t, keysBefore, blobStoreKeys(t, store), "a mount writes no object")
	physicalAfter, err := store.UsedBytes(context.Background())
	require.NoError(t, err)
	assert.Equal(t, physicalBefore, physicalAfter, "a mount adds no bytes to the store")

	assert.Equal(t, countedBefore, usedBytes(t, d),
		"no bytes were stored, so the store is no fuller than it was (#146)")
}

// Mounting makes two images depend on one stored object, so deleting either one
// must not take the bytes out from under the other (#144). The counter follows
// the bytes: it moves only when the object itself goes.
func TestMount_DeletingTheSourceLeavesTheMountedBlobPullable(t *testing.T) {
	repo := testutil.SimpleRepo("mnt11", "docker")
	r, _, d, _ := mountDeps(repo)

	const layer = "one object, two images"
	dgst := pushBlob(t, r, "mnt11", "library/alpine", layer)
	w := postMount(r, "/repository/mnt11/v2/library/ubuntu/blobs/uploads/", dgst, "library/alpine")
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	counted := usedBytes(t, d)

	del := httptest.NewRequest(http.MethodDelete, "/repository/mnt11/v2/library/alpine/blobs/"+dgst, nil)
	wd := httptest.NewRecorder()
	r.ServeHTTP(wd, del)
	require.Equal(t, http.StatusAccepted, wd.Code)

	get := httptest.NewRequest(http.MethodGet, "/repository/mnt11/v2/library/ubuntu/blobs/"+dgst, nil)
	wg := httptest.NewRecorder()
	r.ServeHTTP(wg, get)
	require.Equal(t, http.StatusOK, wg.Code, "the mounted image still needs the bytes the source registered")
	assert.Equal(t, layer, wg.Body.String())

	assert.Equal(t, counted, usedBytes(t, d),
		"the object is still stored for the mounted image, so its size is still used")

	// Deleting the last asset on the object frees it, and the size with it.
	delMount := httptest.NewRequest(http.MethodDelete, "/repository/mnt11/v2/library/ubuntu/blobs/"+dgst, nil)
	wdm := httptest.NewRecorder()
	r.ServeHTTP(wdm, delMount)
	require.Equal(t, http.StatusAccepted, wdm.Code)
	assert.Equal(t, counted-int64(len(layer)), usedBytes(t, d),
		"the last reference gives the object's size back to the store")
}
