package repoproxy

import (
	"fmt"
	"io"
	"net/http"
	"sync"
)

const proxyMaxInflightPerHost = 32

// Shared by direct and per-proxy clients: HTTP/2 streams consume permits just
// like HTTP/1 requests. A permit remains held until the response body closes.
var upstreamInflight inflightRegistry

type inflightRegistry struct {
	mu    sync.Mutex
	limit int
	hosts map[string]chan struct{}
}

// configure is startup-only; existing transports must not be serving requests.
func (r *inflightRegistry) configure(limit int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.limit = limit
	r.hosts = nil
}

func (r *inflightRegistry) semaphore(host string) chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hosts == nil {
		r.hosts = make(map[string]chan struct{})
	}
	if sem, ok := r.hosts[host]; ok {
		return sem
	}
	limit := r.limit
	if limit <= 0 {
		limit = proxyMaxInflightPerHost
	}
	sem := make(chan struct{}, limit)
	r.hosts[host] = sem
	return sem
}

type inflightLimitedTransport struct {
	// Transport is the wrapped idleGuardedTransport in production.
	Transport http.RoundTripper
	registry  *inflightRegistry
}

func (t inflightLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	sem := t.registry.semaphore(NormalizedHost(req.URL, ""))
	select {
	case <-req.Context().Done():
		return nil, fmt.Errorf("waiting for upstream request permit: %w", req.Context().Err())
	case sem <- struct{}{}:
	}
	// A canceled request must not reach an upstream even if its permit was
	// available at the same time as cancellation.
	if err := req.Context().Err(); err != nil {
		<-sem
		return nil, fmt.Errorf("acquiring upstream request permit: %w", err)
	}
	resp, err := t.Transport.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		<-sem
		return resp, err
	}
	resp.Body = &inflightBody{ReadCloser: resp.Body, release: func() { <-sem }}
	return resp, nil
}

func (t inflightLimitedTransport) CloseIdleConnections() {
	if closer, ok := t.Transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

type inflightBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *inflightBody) Close() error {
	defer b.once.Do(b.release)
	return b.ReadCloser.Close()
}
