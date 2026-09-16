package repoproxy

// idleTimeoutBody's and idleGuardedTransport's own unit tests. Internal
// (package repoproxy, not repoproxy_test) because both are unexported — this
// is the mechanism confirmed missing live when a real ~6.4 GB model checkpoint
// (gpt2-xl/model.safetensors) proxied against huggingface.co was cut off
// mid-transfer by the fixed 5-minute Client.Timeout these tests replace: an
// idle-based bound must let a slow-but-progressing transfer run indefinitely
// while still catching a connection that goes truly silent — and it must do
// so with one watchdog per body, applied at the transport so every caller of
// ClientFor(repo)/UpstreamClient is covered, not only repoproxy.go's own copy
// sites (the maintainer's PR #450 review finding).

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stepReader emits the given chunks, waiting delay before each one — a
// reader under the test's own control, standing in for a slow-but-alive (or a
// stalled) upstream connection.
type stepReader struct {
	chunks []string
	delay  time.Duration
	i      int
}

func (s *stepReader) Read(p []byte) (int, error) {
	if s.i >= len(s.chunks) {
		return 0, io.EOF
	}
	time.Sleep(s.delay)
	n := copy(p, s.chunks[s.i])
	s.i++
	return n, nil
}

func (s *stepReader) Close() error { return nil }

func TestIdleTimeoutBody_PassesThroughNormalReads(t *testing.T) {
	b := newIdleTimeoutBody(&stepReader{chunks: []string{"hello ", "world"}}, 200*time.Millisecond)
	defer func() { _ = b.Close() }()
	body, err := io.ReadAll(b)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(body))
}

// A transfer whose EVERY individual read completes well within the idle
// window must run to completion regardless of how long the whole thing takes
// — this is the exact property the fixed 5-minute Client.Timeout violated for
// a real multi-gigabyte file.
func TestIdleTimeoutBody_ManySlowButProgressingReadsAllSucceed(t *testing.T) {
	chunks := make([]string, 8)
	for i := range chunks {
		chunks[i] = "x"
	}
	b := newIdleTimeoutBody(&stepReader{chunks: chunks, delay: 30 * time.Millisecond}, 150*time.Millisecond)
	defer func() { _ = b.Close() }()
	body, err := io.ReadAll(b)
	require.NoError(t, err)
	assert.Equal(t, "xxxxxxxx", string(body))
}

// blockingReader never returns from Read on its own — a connection that
// answered, then went completely silent — but, like a real net/http body,
// DOES unblock a pending Read once Close tears down the underlying
// connection. This is the mechanism idleTimeoutBody's watchdog relies on:
// it force-closes the body, and the net/http body itself is what turns that
// into an error from the blocked Read.
type blockingReader struct {
	unblock chan struct{}
	closed  atomic.Bool
}

func (b *blockingReader) Read(_ []byte) (int, error) {
	<-b.unblock
	return 0, errors.New("use of closed connection")
}

func (b *blockingReader) Close() error {
	if b.closed.CompareAndSwap(false, true) {
		close(b.unblock)
	}
	return nil
}

func TestIdleTimeoutBody_StalledConnectionTimesOutPromptly(t *testing.T) {
	br := &blockingReader{unblock: make(chan struct{})}
	b := newIdleTimeoutBody(br, 50*time.Millisecond)
	defer func() { _ = b.Close() }()

	start := time.Now()
	n, err := b.Read(make([]byte, 16))
	elapsed := time.Since(start)

	assert.Equal(t, 0, n)
	require.Error(t, err)
	// Bounded well under a real request timeout — a stalled connection is
	// caught fast, not after minutes.
	assert.Less(t, elapsed, 500*time.Millisecond)
}

func TestIdleTimeoutBody_PropagatesUnderlyingError(t *testing.T) {
	boom := errors.New("boom")
	b := newIdleTimeoutBody(&errReader{err: boom}, time.Second)
	defer func() { _ = b.Close() }()
	_, err := b.Read(make([]byte, 8))
	assert.ErrorIs(t, err, boom)
}

type errReader struct{ err error }

func (e *errReader) Read(_ []byte) (int, error) { return 0, e.err }
func (e *errReader) Close() error               { return nil }

// A per-Read goroutine+timer design spawns one goroutine per Read — io.Copy's
// 32 KiB buffer against a multi-gigabyte body would mean on the order of 10^5
// of them. This confirms the replacement runs exactly one watchdog goroutine
// for the body's entire lifetime, exiting promptly once Close is called.
func TestIdleTimeoutBody_SingleWatchdogAcrossManyReads(t *testing.T) {
	chunks := make([]string, 500)
	for i := range chunks {
		chunks[i] = "a"
	}
	before := runtime.NumGoroutine()

	b := newIdleTimeoutBody(&stepReader{chunks: chunks}, time.Second)
	_, err := io.ReadAll(b)
	require.NoError(t, err)
	require.NoError(t, b.Close())

	require.Eventually(t, func() bool {
		return runtime.NumGoroutine() <= before+1 // allow scheduler slack
	}, time.Second, 10*time.Millisecond, "watchdog goroutine leaked past Close")
}

// Regression for the maintainer's PR #450 review: before this fix, only
// repoproxy.go's own copy sites wrapped resp.Body in an idle guard by hand, so
// a caller that reads resp.Body directly after ClientFor(repo).Do(req) —
// exactly what terraform, conda, nuget and helm's handlers do, and what this
// PR's own huggingface.serveProxyAPI/upstreamRequest do too — had no bound at
// all once the whole-call Client.Timeout was removed from the shared client.
// Applying the guard at the transport instead protects every such caller
// automatically, with nothing to remember to wrap at the next call site.
func TestIdleGuardedTransport_ProtectsDirectBodyReads(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done() // never writes a body: a stalled-after-headers upstream
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: idleGuardedTransport{Transport: &http.Transport{}, idle: 100 * time.Millisecond},
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	start := time.Now()
	_, err = io.ReadAll(resp.Body) // a direct, unwrapped read — the terraform/conda/nuget/helm pattern
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, 2*time.Second)
}
