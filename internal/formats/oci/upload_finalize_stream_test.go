package oci_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/storage"
)

// ── A blob store that never materializes a blob ─────────────────────────────

// patternReader yields the same block over and over, so a test can push an
// arbitrarily large layer without an array that size existing anywhere.
type patternReader struct {
	block []byte
	off   int
}

func (p *patternReader) Read(b []byte) (int, error) {
	n := copy(b, p.block[p.off:])
	p.off += n
	if p.off == len(p.block) {
		p.off = 0
	}
	return n, nil
}

func synthBlock() []byte {
	block := make([]byte, 64<<10)
	for i := range block {
		block[i] = byte(i * 7)
	}
	return block
}

// synthReader replays exactly n bytes of the pattern.
func synthReader(n int64) io.Reader {
	return io.LimitReader(&patternReader{block: synthBlock()}, n)
}

// synthDigest is the OCI digest of what synthReader(n) yields.
func synthDigest(n int64) string {
	h := sha256.New()
	if _, err := io.Copy(h, synthReader(n)); err != nil {
		panic(err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// synthStore keeps only each blob's length: a write is copied to io.Discard
// and a read replays the pattern. Nothing in the store grows with the layer,
// which leaves the handler under test as the only thing that could — the
// point of #385.
type synthStore struct {
	mu    sync.Mutex
	sizes map[string]int64
	times map[string]time.Time
}

func newSynthStore() *synthStore {
	return &synthStore{sizes: map[string]int64{}, times: map[string]time.Time{}}
}

func (s *synthStore) set(key string, n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sizes[key] = n
	s.times[key] = time.Now()
}

func (s *synthStore) sizeOf(key string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.sizes[key]
	return n, ok
}

func (s *synthStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	n, err := io.Copy(io.Discard, r)
	if err != nil {
		return err
	}
	s.set(key, n)
	return nil
}

func (s *synthStore) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	n, ok := s.sizeOf(key)
	if !ok {
		return nil, 0, fmt.Errorf("blob not found: %s", key)
	}
	return io.NopCloser(synthReader(n)), n, nil
}

func (s *synthStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sizes, key)
	delete(s.times, key)
	return nil
}

func (s *synthStore) Exists(_ context.Context, key string) (bool, error) {
	_, ok := s.sizeOf(key)
	return ok, nil
}

func (s *synthStore) Size(_ context.Context, key string) (int64, error) {
	n, ok := s.sizeOf(key)
	if !ok {
		return 0, fmt.Errorf("blob not found: %s", key)
	}
	return n, nil
}

func (s *synthStore) UsedBytes(_ context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for _, n := range s.sizes {
		total += n
	}
	return total, nil
}

func (s *synthStore) ListKeys(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.sizes))
	for k := range s.sizes {
		keys = append(keys, k)
	}
	return keys, nil
}

func (s *synthStore) ListEntries(_ context.Context) ([]storage.BlobEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := make([]storage.BlobEntry, 0, len(s.sizes))
	for k, n := range s.sizes {
		entries = append(entries, storage.BlobEntry{Key: k, Size: n, ModTime: s.times[k]})
	}
	return entries, nil
}

func (s *synthStore) AppendBlob(_ context.Context, key string, r io.Reader) (int64, error) {
	n, err := io.Copy(io.Discard, r)
	if err != nil {
		return 0, err
	}
	cur, _ := s.sizeOf(key)
	s.set(key, cur+n)
	total, _ := s.sizeOf(key)
	return total, nil
}

func (s *synthStore) TruncateBlob(_ context.Context, key string, size int64) error {
	s.set(key, size)
	return nil
}

func (s *synthStore) AppendedSize(_ context.Context, key string) (int64, bool, error) {
	n, ok := s.sizeOf(key)
	return n, ok, nil
}

func (s *synthStore) FinalizeAppend(_ context.Context, _ string) error { return nil }
func (s *synthStore) AbortAppend(_ context.Context, _ string) error    { return nil }

// ── Tests ───────────────────────────────────────────────────────────────────

// synthLayerSize is deliberately far larger than anything the handler should
// need to hold: the old finalizeUpload read the whole session into one []byte
// before verifying its digest and handed that same buffer to StoreArtifact, so
// this push allocated at least this much (io.ReadAll's growth makes it more).
const synthLayerSize = 128 << 20 // 128 MiB

// pushSynthLayer runs POST → one streaming PATCH → PUT for a layer of n bytes
// and returns the finalizing response. The PATCH body is generated, never
// buffered, so the test harness itself allocates nothing layer-sized.
func pushSynthLayer(t *testing.T, r *gin.Engine, n int64, dgst string) *httptest.ResponseRecorder {
	t.Helper()
	loc := startUpload(t, r)

	req := httptest.NewRequest(http.MethodPatch, loc, synthReader(n))
	req.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code, "patch: %s", w.Body.String())

	sep := "?"
	if strings.Contains(loc, "?") {
		sep = "&"
	}
	req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("%s%sdigest=%s", loc, sep, dgst), nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Finalizing a push must stream the staged session, not materialize it. With
// the whole blob buffered, a handful of large layers committing at the same
// moment each held their own full copy in RAM — a memory-pressure vector on
// every push regardless of size (#385).
func TestFinalizeUploadDoesNotBufferTheWholeBlob(t *testing.T) {
	store := newSynthStore()
	r := setupWithStore(store, 0)
	dgst := synthDigest(synthLayerSize)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	w := pushSynthLayer(t, r, synthLayerSize, dgst)
	runtime.ReadMemStats(&after)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, dgst, w.Header().Get("Docker-Content-Digest"))
	assert.Equal(t, fmt.Sprintf("0-%d", synthLayerSize-1), w.Header().Get("Content-Range"))

	// The budget is generous — the streaming path allocates copy buffers, well
	// under a megabyte — but an order of magnitude below the layer itself, so
	// any version that materializes the blob fails this outright.
	const budget = synthLayerSize / 8
	allocated := after.TotalAlloc - before.TotalAlloc
	assert.Lessf(t, allocated, uint64(budget),
		"finalizing a %d-byte layer allocated %d bytes; it must stream, not buffer",
		int64(synthLayerSize), allocated)
}

// Streaming the verification must not weaken it: a wrong digest is still
// rejected before anything is written under it (#194), and the session
// survives so the client can retry the PUT with the right one.
func TestFinalizeUploadStreamingVerifyStillRejectsWrongDigest(t *testing.T) {
	store := newSynthStore()
	r := setupWithStore(store, 0)
	const n = 3 << 20 // 3 MiB: enough to cross the copy buffer many times

	wrong := synthDigest(n + 1)
	w := pushSynthLayer(t, r, n, wrong)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "DIGEST_INVALID")

	keys, err := store.ListKeys(context.Background())
	require.NoError(t, err)
	for _, k := range keys {
		assert.Truef(t, strings.HasPrefix(k, "_uploads/"),
			"a rejected push must leave nothing but its retryable session, found %q", k)
	}
}
