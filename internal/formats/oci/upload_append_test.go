package oci_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ── Instrumented blob stores ────────────────────────────────────────────────

// appendCountingStore is an in-memory blob store that records how often each
// operation is called, so a test can assert what a chunked push actually costs
// rather than only what it produces.
type appendCountingStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
	times map[string]time.Time

	gets, puts, appends, truncates, finalizes, aborts int
}

func newAppendCountingStore() *appendCountingStore {
	return &appendCountingStore{blobs: map[string][]byte{}, times: map[string]time.Time{}}
}

func (c *appendCountingStore) counts() (gets, puts, appends int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gets, c.puts, c.appends
}

func (c *appendCountingStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.puts++
	c.blobs[key] = data
	c.times[key] = time.Now()
	return nil
}

func (c *appendCountingStore) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gets++
	data, ok := c.blobs[key]
	if !ok {
		return nil, 0, fmt.Errorf("blob not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
}

func (c *appendCountingStore) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.blobs, key)
	delete(c.times, key)
	return nil
}

func (c *appendCountingStore) Exists(_ context.Context, key string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.blobs[key]
	return ok, nil
}

func (c *appendCountingStore) Size(_ context.Context, key string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, ok := c.blobs[key]
	if !ok {
		return 0, fmt.Errorf("blob not found: %s", key)
	}
	return int64(len(data)), nil
}

func (c *appendCountingStore) UsedBytes(_ context.Context) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var total int64
	for _, d := range c.blobs {
		total += int64(len(d))
	}
	return total, nil
}

func (c *appendCountingStore) ListKeys(_ context.Context) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.blobs))
	for k := range c.blobs {
		keys = append(keys, k)
	}
	return keys, nil
}

func (c *appendCountingStore) ListEntries(_ context.Context) ([]storage.BlobEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := make([]storage.BlobEntry, 0, len(c.blobs))
	for k, d := range c.blobs {
		entries = append(entries, storage.BlobEntry{Key: k, Size: int64(len(d)), ModTime: c.times[k]})
	}
	return entries, nil
}

// AppendBlob grows the blob without reading it back through Get — the whole
// point of the extension, and what the call counters here prove.
func (c *appendCountingStore) AppendBlob(_ context.Context, key string, r io.Reader) (int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.appends++
	c.blobs[key] = append(c.blobs[key], data...)
	c.times[key] = time.Now()
	return int64(len(c.blobs[key])), nil
}

func (c *appendCountingStore) TruncateBlob(_ context.Context, key string, size int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.truncates++
	data, ok := c.blobs[key]
	if !ok {
		return fmt.Errorf("blob not found: %s", key)
	}
	if size > int64(len(data)) {
		return fmt.Errorf("truncate %s: %d beyond staged %d", key, size, len(data))
	}
	c.blobs[key] = data[:size]
	return nil
}

func (c *appendCountingStore) AppendedSize(_ context.Context, key string) (int64, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, ok := c.blobs[key]
	if !ok {
		return 0, false, nil
	}
	return int64(len(data)), true, nil
}

func (c *appendCountingStore) FinalizeAppend(_ context.Context, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.finalizes++
	return nil
}

func (c *appendCountingStore) AbortAppend(_ context.Context, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.aborts++
	return nil
}

// plainStore exposes the same in-memory store *without* the append extension,
// so the fallback path stays covered. The wrapping is explicit rather than an
// embedded field: embedding would promote AppendBlob and the type assertion in
// uploadStore would take the streaming branch anyway.
type plainStore struct{ inner *appendCountingStore }

func (p *plainStore) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	return p.inner.Put(ctx, key, r, size)
}
func (p *plainStore) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	return p.inner.Get(ctx, key)
}
func (p *plainStore) Delete(ctx context.Context, key string) error { return p.inner.Delete(ctx, key) }
func (p *plainStore) Exists(ctx context.Context, key string) (bool, error) {
	return p.inner.Exists(ctx, key)
}
func (p *plainStore) Size(ctx context.Context, key string) (int64, error) {
	return p.inner.Size(ctx, key)
}
func (p *plainStore) UsedBytes(ctx context.Context) (int64, error) { return p.inner.UsedBytes(ctx) }
func (p *plainStore) ListKeys(ctx context.Context) ([]string, error) {
	return p.inner.ListKeys(ctx)
}
func (p *plainStore) ListEntries(ctx context.Context) ([]storage.BlobEntry, error) {
	return p.inner.ListEntries(ctx)
}

// ── Harness ─────────────────────────────────────────────────────────────────

func setupWithStore(store storage.BlobStore, maxUploadBytes int64) *gin.Engine {
	repo := &domain.Repository{
		ID: "repo-append", Name: "docker-hosted",
		Format: domain.FormatDocker, Type: domain.TypeHosted, Online: true,
	}
	d := formats.Deps{
		Repos:          testutil.NewRepoRepo(repo),
		Blobs:          testutil.NewBlobStoreRepo(),
		Components:     testutil.NewComponentRepo(),
		Assets:         testutil.NewAssetRepo(),
		BlobStore:      store,
		BaseURL:        "http://localhost:8080",
		MaxUploadBytes: maxUploadBytes,
	}
	h := oci.New(d)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := requestctx.WithUser(c.Request.Context(), "test-user-id", "testuser")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// pushChunkedBlob runs a full chunked push — POST, one PATCH per chunk, PUT — and
// returns the finalizing response.
func pushChunkedBlob(t *testing.T, r *gin.Engine, chunks []string) *httptest.ResponseRecorder {
	t.Helper()
	loc := startUpload(t, r)
	for i, chunk := range chunks {
		req := httptest.NewRequest(http.MethodPatch, loc, strings.NewReader(chunk))
		req.Header.Set("Content-Type", "application/octet-stream")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusAccepted, w.Code, "chunk %d: %s", i, w.Body.String())
	}
	sep := "?"
	if strings.Contains(loc, "?") {
		sep = "&"
	}
	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("%s%sdigest=%s", loc, sep, digest(strings.Join(chunks, ""))), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ── Tests ───────────────────────────────────────────────────────────────────

// A chunked push on an appendable store must never read the staged blob back:
// that read-modify-write is what made an N-chunk push move O(N²) bytes (#214).
func TestPatchUploadStreamsChunksWithoutReadingSessionBack(t *testing.T) {
	store := newAppendCountingStore()
	r := setupWithStore(store, 0)
	loc := startUpload(t, r)

	for i := 0; i < 5; i++ {
		w := patchChunk(t, r, loc, 128)
		require.Equal(t, http.StatusAccepted, w.Code, "chunk %d: %s", i, w.Body.String())
		assert.Equal(t, fmt.Sprintf("0-%d", 128*(i+1)-1), w.Header().Get("Range"))
	}

	gets, _, appends := store.counts()
	assert.Equal(t, 5, appends, "each chunk must be a single streaming append")
	assert.Zero(t, gets, "no chunk may read the staged session back")
}

// The bytes still have to arrive intact, chunk boundaries and all: the push is
// verified end to end through the digest the registry checks before storing.
func TestChunkedPushOnAppendableStoreStoresExactBytes(t *testing.T) {
	store := newAppendCountingStore()
	r := setupWithStore(store, 0)
	chunks := []string{"alpha", "-beta-", "gamma", "", "-delta"}

	w := pushChunkedBlob(t, r, chunks)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, digest(strings.Join(chunks, "")), w.Header().Get("Docker-Content-Digest"))
}

// A store without the extension keeps today's behavior, unchanged.
func TestChunkedPushFallsBackForNonAppendableStore(t *testing.T) {
	inner := newAppendCountingStore()
	r := setupWithStore(&plainStore{inner: inner}, 0)
	chunks := []string{"one", "-two", "-three"}

	w := pushChunkedBlob(t, r, chunks)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, digest(strings.Join(chunks, "")), w.Header().Get("Docker-Content-Digest"))
	gets, _, appends := inner.counts()
	assert.Zero(t, appends, "a plain store must never take the streaming branch")
	assert.Positive(t, gets, "the fallback stages a chunk by rewriting the session")
}

// The cap contract of #211 must survive the switch to streaming appends: a
// chunk that crosses the cap is rejected and costs the session nothing, even
// though a streaming append can only discover the overflow after writing it.
func TestStreamingAppendRejectsChunkOverLimitAndRollsBack(t *testing.T) {
	store := newAppendCountingStore()
	r := setupWithStore(store, 1024)
	loc := startUpload(t, r)

	first := patchChunk(t, r, loc, 800)
	require.Equal(t, http.StatusAccepted, first.Code, "body: %s", first.Body.String())
	assert.Equal(t, "0-799", first.Header().Get("Range"))

	second := patchChunk(t, r, loc, 800)
	require.Equal(t, http.StatusRequestEntityTooLarge, second.Code, "body: %s", second.Body.String())
	assert.Equal(t, "BLOB_UPLOAD_INVALID", dockerErrorCode(t, second.Body.String()))

	store.mu.Lock()
	truncates := store.truncates
	store.mu.Unlock()
	assert.Equal(t, 1, truncates, "the over-cap chunk must be rolled back, not left staged")

	third := patchChunk(t, r, loc, 224)
	require.Equal(t, http.StatusAccepted, third.Code,
		"rejected chunk consumed budget it never used: %s", third.Body.String())
	assert.Equal(t, "0-1023", third.Header().Get("Range"))

	fourth := patchChunk(t, r, loc, 1)
	assert.Equal(t, http.StatusRequestEntityTooLarge, fourth.Code,
		"session at the cap accepted more bytes: %s", fourth.Body.String())
}

// A finished session must release whatever the backend still holds for it —
// abandoned multipart parts are invisible to the blob-key GC sweep.
func TestFinalizedUploadAbortsAndDeletesSession(t *testing.T) {
	store := newAppendCountingStore()
	r := setupWithStore(store, 0)

	w := pushChunkedBlob(t, r, []string{"payload"})
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	store.mu.Lock()
	finalizes, aborts := store.finalizes, store.aborts
	store.mu.Unlock()
	assert.Positive(t, finalizes, "the staged bytes must be published before they are read")
	assert.Positive(t, aborts, "a removed session must release its backend resources")

	for _, k := range mustListKeys(t, store) {
		assert.False(t, strings.HasPrefix(k, "_uploads/"), "upload session %q outlived its push", k)
	}
}

func mustListKeys(t *testing.T, store *appendCountingStore) []string {
	t.Helper()
	keys, err := store.ListKeys(context.Background())
	require.NoError(t, err)
	return keys
}
