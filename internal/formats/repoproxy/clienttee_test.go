package repoproxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

type blockedClient struct {
	*httptest.ResponseRecorder
	started chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (w *blockedClient) Write(p []byte) (int, error) {
	w.once.Do(func() {
		if len(p) > 0 {
			_, _ = w.ResponseRecorder.Write(p[:1])
		}
		close(w.started)
		<-w.resume
	})
	// The initial byte is only a signal; keep the recorder exact in a separate
	// writer below rather than asserting its contents in the blocking test.
	return len(p), nil
}
func runWithWriter(d formats.Deps, repo *domain.Repository, path string, writer http.ResponseWriter, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() {
		c, _ := gin.CreateTestContext(writer)
		c.Request = httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		done <- ServeGET(c, d, repo, path, "", base.Coords{}, "application/octet-stream", 0)
	}()
	return done
}
func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("signal timed out")
	}
}
func TestServeGET_SlowOwnerClient_DoesNotGateWaiter(t *testing.T) {
	testSlowOwner(t, false)
}
func TestServeGET_FillReleasesOnRegistration_NotOnHandlerReturn(t *testing.T) {
	testSlowOwner(t, true)
}
func testSlowOwner(t *testing.T, inspectRelease bool) {
	t.Helper()
	payload := bytes.Repeat([]byte("x"), 8<<20)
	var hits atomic.Int32
	rest := make(chan struct{})
	repo, d, _ := fillFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Length", "8388608")
		_, _ = w.Write(payload[:32768])
		w.(http.Flusher).Flush()
		<-rest
		_, _ = w.Write(payload[32768:])
	})
	writer := &blockedClient{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), resume: make(chan struct{})}
	owner := runWithWriter(d, repo, "/slow", writer, context.Background())
	waitSignal(t, writer.started)
	entry, release := cacheFills.acquire(base.BlobKey(repo.Name, "/slow") + "\x00\x00")
	require.Nil(t, release)
	close(rest)
	// The entry itself is the release hook. Its closure must follow registration
	// while the owner remains blocked inside its first client Write.
	waitSignal(t, entry.done)
	asset, err := d.Assets.GetByPath(context.Background(), repo.Name, "/slow")
	require.NoError(t, err)
	require.NotNil(t, asset)
	if inspectRelease {
		select {
		case <-owner:
			t.Fatal("owner returned before client was released")
		default:
		}
	}
	waiter := receiveFill(t, startFill(context.Background(), d, repo, http.MethodGet, "/slow"))
	require.NoError(t, waiter.err)
	require.Equal(t, payload, waiter.w.Body.Bytes())
	require.EqualValues(t, 1, hits.Load())
	close(writer.resume)
	require.NoError(t, <-owner)
}

type slowClient struct{ *httptest.ResponseRecorder }

func (w slowClient) Write(p []byte) (int, error) {
	time.Sleep(time.Millisecond)
	return w.ResponseRecorder.Write(p)
}
func TestServeGET_LaggingClient_CatchesUpFromCache(t *testing.T) {
	previous := clientTeeBufferOverride.Swap(1024)
	t.Cleanup(func() { clientTeeBufferOverride.Store(previous) })
	payload := bytes.Repeat([]byte("catch-up"), 128<<10)
	repo, d, _ := fillFixture(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) })
	writer := slowClient{httptest.NewRecorder()}
	require.NoError(t, <-runWithWriter(d, repo, "/lagging", writer, context.Background()))
	require.Equal(t, sha256.Sum256(payload), sha256.Sum256(writer.Body.Bytes()))
	asset, err := d.Assets.GetByPath(context.Background(), repo.Name, "/lagging")
	require.NoError(t, err)
	require.EqualValues(t, len(payload), asset.SizeBytes)
}

type abortClient struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
	once   sync.Once
}

func (w *abortClient) Write(p []byte) (int, error) {
	w.once.Do(w.cancel)
	return 0, errors.New("client disconnected")
}
func TestServeGET_ClientAbort_FillCompletesAndRegisters(t *testing.T) {
	payload := bytes.Repeat([]byte("detached"), 1<<20)
	var hits atomic.Int32
	repo, d, _ := fillFixture(t, func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); _, _ = w.Write(payload) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &abortClient{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	require.Error(t, <-runWithWriter(d, repo, "/abort", writer, ctx))
	asset, err := d.Assets.GetByPath(context.Background(), repo.Name, "/abort")
	require.NoError(t, err)
	require.NotNil(t, asset)
	waiter := receiveFill(t, startFill(context.Background(), d, repo, http.MethodGet, "/abort"))
	require.NoError(t, waiter.err)
	require.Equal(t, payload, waiter.w.Body.Bytes())
	require.EqualValues(t, 1, hits.Load())
}
func TestClientTee_BoundedOrderedPrefix(t *testing.T) {
	tee := newClientTee(4)
	_, err := tee.Write([]byte("123"))
	require.NoError(t, err)
	_, err = tee.Write([]byte("45"))
	require.NoError(t, err)
	_, err = tee.Write([]byte("6"))
	require.NoError(t, err)
	chunk, ok := tee.next()
	require.True(t, ok)
	require.Equal(t, "123", string(chunk))
	_, ok = tee.next()
	require.False(t, ok)
	lagging, sent := tee.progress()
	require.True(t, lagging)
	require.EqualValues(t, 3, sent)
}

func TestServeGET_WaitersRelayUpstream404WithoutRefetch(t *testing.T) {
	var hits atomic.Int32
	ready, release := make(chan struct{}), make(chan struct{})
	repo, d, lookups := fillFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			close(ready)
		}
		<-release
		w.WriteHeader(http.StatusNotFound)
	})
	results := []<-chan fillResult{startFill(context.Background(), d, repo, http.MethodGet, "/missing")}
	waitSignal(t, ready)
	awaitLookup(t, lookups)
	awaitLookup(t, lookups)
	for range 7 {
		results = append(results, startFill(context.Background(), d, repo, http.MethodGet, "/missing"))
	}
	for range 7 {
		awaitLookup(t, lookups)
	}
	close(release)
	for _, result := range results {
		r := receiveFill(t, result)
		require.NoError(t, r.err)
		require.Equal(t, 404, r.w.Code)
	}
	require.EqualValues(t, 1, hits.Load())
}

func TestServeGET_FillKeyIncludesAccept(t *testing.T) {
	var hits atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	repo, d, _ := fillFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		entered <- struct{}{}
		<-release
		_, _ = io.WriteString(w, "representation")
	})
	results := make(chan error, 2)
	for _, accept := range []string{"application/json", "application/xml"} {
		go func() {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/accept", nil)
			c.Request.Header.Set("Accept", accept)
			results <- ServeGET(c, d, repo, "/accept", "", base.Coords{}, "", 0)
		}()
	}
	waitSignal(t, entered)
	waitSignal(t, entered)
	close(release)
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	require.EqualValues(t, 2, hits.Load())
}

type snapshotAssets struct {
	repository.AssetRepo
	mu *sync.Mutex
}

func (a snapshotAssets) GetByPath(ctx context.Context, repo, path string) (*domain.Asset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	value, err := a.AssetRepo.GetByPath(ctx, repo, path)
	if value == nil {
		return nil, err
	}
	copyValue := *value
	return &copyValue, err
}
func (a snapshotAssets) TouchLastModified(ctx context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.AssetRepo.TouchLastModified(ctx, id)
}
func TestServeGET_StaleMetadata_RevalidatedOnce(t *testing.T) {
	var hits atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	repo, d, lookups := fillFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-Modified-Since") == "" {
			t.Error("missing conditional header")
		}
		close(entered)
		<-release
		w.WriteHeader(http.StatusNotModified)
	})
	d.Assets = snapshotAssets{AssetRepo: d.Assets, mu: &sync.Mutex{}}
	ctx := context.Background()
	key := base.BlobKey(repo.Name, "/maven-metadata.xml")
	seed := &domain.Asset{ID: "stale", Repository: repo.Name, RepositoryID: repo.ID, Path: "/maven-metadata.xml", BlobKey: key, SizeBytes: 4}
	require.NoError(t, d.Assets.Create(ctx, seed))
	seed.LastModified = time.Now().Add(-time.Hour)
	require.NoError(t, d.BlobStore.Put(ctx, key, bytes.NewBufferString("meta"), 4))
	request := func() <-chan fillResult {
		result := make(chan fillResult, 1)
		go func() {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/maven-metadata.xml", nil)
			err := ServeGET(c, d, repo, "/maven-metadata.xml", "", base.Coords{}, "text/xml", time.Second)
			result <- fillResult{w, err}
		}()
		return result
	}
	results := []<-chan fillResult{request()}
	waitSignal(t, entered)
	awaitLookup(t, lookups)
	awaitLookup(t, lookups)
	for range 7 {
		results = append(results, request())
	}
	for range 7 {
		awaitLookup(t, lookups)
	}
	close(release)
	for _, ch := range results {
		r := receiveFill(t, ch)
		require.NoError(t, r.err)
		require.Equal(t, 200, r.w.Code)
		require.Equal(t, "meta", r.w.Body.String())
	}
	require.EqualValues(t, 1, hits.Load())
}

func TestServeGET_WaitersRefetchAfterTransportError(t *testing.T) {
	var hits atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	repo, d, lookups := fillFixture(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "unused") })
	UpstreamClient = &http.Client{Transport: inflightTestTransport(func(*http.Request) (*http.Response, error) {
		if hits.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nil, errors.New("dial failed")
	})}
	owner := startFill(context.Background(), d, repo, http.MethodGet, "/transport-error")
	waitSignal(t, entered)
	awaitLookup(t, lookups)
	awaitLookup(t, lookups)
	waiter := startFill(context.Background(), d, repo, http.MethodGet, "/transport-error")
	awaitLookup(t, lookups)
	close(release)
	require.Equal(t, 502, receiveFill(t, owner).w.Code)
	require.Equal(t, 502, receiveFill(t, waiter).w.Code)
	require.EqualValues(t, 2, hits.Load())
}
