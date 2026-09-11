package repoproxy

// idleReader's own unit tests. Internal (package repoproxy, not repoproxy_test)
// because idleReader and idleBodyTimeout are unexported — this is the
// mechanism confirmed missing live when a real ~6.4 GB model checkpoint
// (gpt2-xl/model.safetensors) proxied against huggingface.co was cut off
// mid-transfer by the fixed 5-minute Client.Timeout these tests replace: an
// idle-based bound must let a slow-but-progressing transfer run indefinitely
// while still catching a connection that goes truly silent.

import (
	"errors"
	"io"
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

func TestIdleReader_PassesThroughNormalReads(t *testing.T) {
	r := idleReader{&stepReader{chunks: []string{"hello ", "world"}}, 200 * time.Millisecond}
	body, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(body))
}

// A transfer whose EVERY individual read completes well within the idle
// window must run to completion regardless of how long the whole thing takes
// — this is the exact property the fixed 5-minute Client.Timeout violated for
// a real multi-gigabyte file.
func TestIdleReader_ManySlowButProgressingReadsAllSucceed(t *testing.T) {
	chunks := make([]string, 8)
	for i := range chunks {
		chunks[i] = "x"
	}
	r := idleReader{&stepReader{chunks: chunks, delay: 30 * time.Millisecond}, 150 * time.Millisecond}
	body, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, "xxxxxxxx", string(body))
}

// blockingReader never returns from Read — a connection that answered, then
// went completely silent.
type blockingReader struct{ unblock chan struct{} }

func (b *blockingReader) Read(_ []byte) (int, error) {
	<-b.unblock // never closed within the test: simulates "forever"
	return 0, io.EOF
}

func TestIdleReader_StalledConnectionTimesOutPromptly(t *testing.T) {
	br := &blockingReader{unblock: make(chan struct{})}
	defer close(br.unblock) // let the leaked goroutine exit; see idleReader's own doc

	r := idleReader{br, 50 * time.Millisecond}
	start := time.Now()
	n, err := r.Read(make([]byte, 16))
	elapsed := time.Since(start)

	assert.Equal(t, 0, n)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no data received")
	// Bounded well under a real request timeout — a stalled connection is
	// caught fast, not after minutes.
	assert.Less(t, elapsed, 500*time.Millisecond)
}

func TestIdleReader_PropagatesUnderlyingError(t *testing.T) {
	boom := errors.New("boom")
	r := idleReader{&errReader{err: boom}, time.Second}
	_, err := r.Read(make([]byte, 8))
	assert.ErrorIs(t, err, boom)
}

type errReader struct{ err error }

func (e *errReader) Read(_ []byte) (int, error) { return 0, e.err }
