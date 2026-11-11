package repoproxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

type observedAssets struct {
	repository.AssetRepo
	lookups chan struct{}
}

func (a observedAssets) GetByPath(ctx context.Context, repo, path string) (*domain.Asset, error) {
	asset, err := a.AssetRepo.GetByPath(ctx, repo, path)
	a.lookups <- struct{}{}
	return asset, err
}

type fillResult struct {
	w   *httptest.ResponseRecorder
	err error
}

func fillFixture(t *testing.T, handler http.HandlerFunc) (*domain.Repository, formats.Deps, chan struct{}) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	orig := UpstreamClient
	UpstreamClient = server.Client()
	t.Cleanup(func() { UpstreamClient = orig })
	repo := mkProxyRepo(t.Name(), map[string]any{"remote_url": server.URL})
	lookups := make(chan struct{}, 100)
	d := formats.Deps{Assets: observedAssets{testutil.NewAssetRepo(), lookups}, Components: testutil.NewComponentRepo(), Blobs: testutil.NewBlobStoreRepo(), BlobStore: testutil.NewBlobStore()}
	return repo, d, lookups
}
func startFill(ctx context.Context, d formats.Deps, repo *domain.Repository, method, path string) <-chan fillResult {
	result := make(chan fillResult, 1)
	go func() {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(method, path, nil).WithContext(ctx)
		err := ServeGET(c, d, repo, path, "", base.Coords{}, "application/octet-stream", 0)
		result <- fillResult{w, err}
	}()
	return result
}
func receiveFill(t *testing.T, result <-chan fillResult) fillResult {
	t.Helper()
	select {
	case r := <-result:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("fill did not finish")
		return fillResult{}
	}
}
func awaitLookup(t *testing.T, lookups <-chan struct{}) {
	t.Helper()
	select {
	case <-lookups:
	case <-time.After(5 * time.Second):
		t.Fatal("lookup did not run")
	}
}
func TestServeGET_ConcurrentMiss_FetchesUpstreamOnce(t *testing.T) {
	testConcurrentFill(t, http.MethodGet, false)
}
func TestServeGET_HEADMiss_DedupedWithGET(t *testing.T) {
	testConcurrentFill(t, http.MethodHead, false)
}
func testConcurrentFill(t *testing.T, method string, missingBlob bool) {
	t.Helper()
	var hits atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	repo, d, lookups := fillFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			close(started)
		}
		<-release
		_, _ = w.Write([]byte("artifact"))
	})
	if missingBlob {
		if err := d.Assets.Create(context.Background(), &domain.Asset{Repository: repo.Name, RepositoryID: repo.ID, Path: "/a", BlobKey: base.BlobKey(repo.Name, "/a")}); err != nil {
			t.Fatalf("seed asset: %v", err)
		}
	}
	results := []<-chan fillResult{startFill(context.Background(), d, repo, method, "/a")}
	<-started
	// The owner performs its initial lookup and its ownership recheck.
	awaitLookup(t, lookups)
	awaitLookup(t, lookups)
	for range 9 {
		results = append(results, startFill(context.Background(), d, repo, http.MethodGet, "/a"))
	}
	for range 9 {
		awaitLookup(t, lookups)
	}
	close(release)
	for i, result := range results {
		r := receiveFill(t, result)
		if r.err != nil || r.w.Code != 200 {
			t.Fatalf("response: %d, %v", r.w.Code, r.err)
		}
		if (i > 0 || method == http.MethodGet) && r.w.Body.String() != "artifact" {
			t.Fatalf("body %q", r.w.Body.String())
		}
	}
	page, err := d.Assets.List(context.Background(), repo.Name, 100, 0)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("registered assets: %v, %v", page, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream hits: %d", hits.Load())
	}
}
func TestServeGET_ConcurrentMiss_DifferentPaths_DoNotBlockEachOther(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	repo, d, _ := fillFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			close(started)
			<-release
		}
		_, _ = w.Write([]byte("ok"))
	})
	first := startFill(context.Background(), d, repo, http.MethodGet, "/a")
	<-started
	second := receiveFill(t, startFill(context.Background(), d, repo, http.MethodGet, "/b"))
	close(release)
	receiveFill(t, first)
	if second.err != nil || second.w.Code != 200 {
		t.Fatalf("second fill: %v", second)
	}
}
func TestServeGET_WaiterHonoursContextCancel(t *testing.T) {
	repo, d, lookups := fillFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected upstream request") })
	key := base.BlobKey(repo.Name, "/a")
	done, release := cacheFills.acquire(key)
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := startFill(ctx, d, repo, http.MethodGet, "/a")
	awaitLookup(t, lookups)
	cancel()
	if r := receiveFill(t, result); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("error: %v", r.err)
	}
	select {
	case <-done:
		t.Fatal("owner released by waiter")
	default:
	}
}
func TestServeGET_WaiterFallsBackWhenFillFails(t *testing.T) {
	var hits atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	repo, d, lookups := fillFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			close(started)
			<-release
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("retry"))
	})
	owner := startFill(context.Background(), d, repo, http.MethodGet, "/a")
	<-started
	awaitLookup(t, lookups)
	awaitLookup(t, lookups)
	waiter := startFill(context.Background(), d, repo, http.MethodGet, "/a")
	awaitLookup(t, lookups)
	close(release)
	receiveFill(t, owner)
	r := receiveFill(t, waiter)
	if r.err != nil || r.w.Body.String() != "retry" || hits.Load() != 2 {
		t.Fatalf("fallback: %v, hits %d", r, hits.Load())
	}
}

func TestServeGET_ConcurrentMiss_MissingBlob(t *testing.T) {
	testConcurrentFill(t, http.MethodGet, true)
}

func TestFillGroup_Acquire_ReleaseAfterPanic(t *testing.T) {
	var group fillGroup
	done, release := group.acquire("key")
	func() { defer func() { _ = recover() }(); defer release(); panic("fill failed") }()
	select {
	case <-done:
	default:
		t.Fatal("completion was not signaled")
	}
	_, nextRelease := group.acquire("key")
	if nextRelease == nil {
		t.Fatal("entry was not removed")
	}
	nextRelease()
}
