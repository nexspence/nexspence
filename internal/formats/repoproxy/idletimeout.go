package repoproxy

import (
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// idleBodyTimeout bounds how long a response body may go without producing a
// byte before it is force-closed. It protects against a connection that
// answers, then goes silent forever — without bounding how long a large,
// still-progressing transfer may take in total, which a whole-call
// Client.Timeout could not offer (see UpstreamClient's doc comment in
// repoproxy.go for the real multi-gigabyte transfer that timeout used to cut off).
const idleBodyTimeout = 2 * time.Minute

// idleGuardedTransport wraps an *http.Transport so every response it returns
// carries an idle-timeout watchdog on its body, regardless of which call site
// reads it. Applied once — to UpstreamClient's transport in repoproxy.go and
// to every client buildProxyClient constructs — this protects any format
// handler that calls ClientFor(repo).Do(req) and reads resp.Body directly
// (terraform, conda, nuget, helm, huggingface's metadata/HEAD paths), the
// same way repoproxy.go's own artifact-fetch path is protected, with nothing
// for a future format to remember to wrap by hand.
type idleGuardedTransport struct {
	*http.Transport
	idle time.Duration
}

func (t idleGuardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.Transport.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	resp.Body = newIdleTimeoutBody(resp.Body, t.idle)
	return resp, nil
}

// idleTimeoutBody force-closes its underlying body once idle passes with no
// Read progress. Unlike a per-Read goroutine+timer, this runs exactly one
// watchdog goroutine for the body's whole lifetime, stopped by Close — a
// design that reads in io.Copy's 32 KiB chunks would otherwise spawn one
// goroutine per chunk (on the order of 10^5 for a multi-gigabyte body), each
// one leaking a reference to the caller's buffer if it lost the timeout race.
type idleTimeoutBody struct {
	io.ReadCloser
	idle         time.Duration
	lastProgress atomic.Int64 // UnixNano, updated on every successful Read
	stop         chan struct{}
	stopOnce     sync.Once
}

func newIdleTimeoutBody(rc io.ReadCloser, idle time.Duration) *idleTimeoutBody {
	b := &idleTimeoutBody{ReadCloser: rc, idle: idle, stop: make(chan struct{})}
	b.lastProgress.Store(time.Now().UnixNano())
	go b.watch()
	return b
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.lastProgress.Store(time.Now().UnixNano())
	}
	return n, err
}

func (b *idleTimeoutBody) Close() error {
	b.stopOnce.Do(func() { close(b.stop) })
	return b.ReadCloser.Close()
}

// watch force-closes the body once idle has passed since the last successful
// Read. Closing unblocks whatever Read is currently pending on the underlying
// connection with an error of its own — the same mechanism the previous
// per-Read design relied on when the caller's own resp.Body.Close() tore the
// connection down, just triggered here instead of left to the caller.
func (b *idleTimeoutBody) watch() {
	t := time.NewTimer(b.idle)
	defer t.Stop()
	for {
		select {
		case <-b.stop:
			return
		case <-t.C:
			last := time.Unix(0, b.lastProgress.Load())
			if since := time.Since(last); since < b.idle {
				t.Reset(b.idle - since)
				continue
			}
			_ = b.ReadCloser.Close()
			return
		}
	}
}
