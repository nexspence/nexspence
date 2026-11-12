package repoproxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInflightLimit_BoundsConcurrentRequests_HTTP1(t *testing.T) {
	testInflightConcurrency(t, false)
}
func TestInflightLimit_BoundsConcurrentStreams_HTTP2(t *testing.T) { testInflightConcurrency(t, true) }
func testInflightConcurrency(t *testing.T, h2 bool) {
	t.Helper()
	var active, peak atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		if h2 && r.ProtoMajor != 2 {
			t.Errorf("expected HTTP/2, got %s", r.Proto)
		}
		time.Sleep(15 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	server.EnableHTTP2 = h2
	if h2 {
		server.StartTLS()
	} else {
		server.Start()
	}
	defer server.Close()
	tr := server.Client().Transport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = true
	registry := &inflightRegistry{limit: 2}
	client := &http.Client{Transport: inflightLimitedTransport{Transport: tr, registry: registry}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			resp, err := client.Get(server.URL)
			if err != nil {
				t.Error(err)
				return
			}
			_, err = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if p := peak.Load(); p != 2 {
		t.Fatalf("peak active handlers = %d, want 2", p)
	}
}

type inflightTestTransport func(*http.Request) (*http.Response, error)

func (f inflightTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestInflightLimit_ReleasesOnBodyClose(t *testing.T) {
	registry := &inflightRegistry{limit: 1}
	calls := make(chan struct{}, 2)
	tr := inflightLimitedTransport{registry: registry, Transport: inflightTestTransport(func(*http.Request) (*http.Response, error) {
		calls <- struct{}{}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.org", nil)
	first, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	<-calls
	result := make(chan struct{}, 1)
	go func() {
		r, e := tr.RoundTrip(req)
		if e != nil {
			t.Error(e)
			return
		}
		_ = r.Body.Close()
		result <- struct{}{}
	}()
	select {
	case <-calls:
		t.Fatal("permit released before body close")
	case <-time.After(20 * time.Millisecond):
	}
	_ = first.Body.Close()
	_ = first.Body.Close()
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("permit not released")
	}
}
func TestInflightLimit_HonoursContext(t *testing.T) {
	registry := &inflightRegistry{limit: 1}
	tr := inflightLimitedTransport{registry: registry, Transport: inflightTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.org", nil)
	first, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Body.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	resp, err := tr.RoundTrip(req.WithContext(ctx))
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline, got %v", err)
	}
}
func TestInflightLimit_ReleasesOnErrorOrMissingBody(t *testing.T) {
	for _, mode := range []string{"error", "nil", "bodyless"} {
		t.Run(mode, func(t *testing.T) {
			registry := &inflightRegistry{limit: 1}
			expected := errors.New("dial failed")
			tr := inflightLimitedTransport{registry: registry, Transport: inflightTestTransport(func(*http.Request) (*http.Response, error) {
				switch mode {
				case "error":
					return nil, expected
				case "nil":
					return nil, nil
				default:
					return &http.Response{}, nil
				}
			})}
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.org", nil)
			resp, err := tr.RoundTrip(req)
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if mode == "error" && !errors.Is(err, expected) {
				t.Fatal(err)
			}
			if len(registry.semaphore("example.org")) != 0 {
				t.Fatal("leaked permit")
			}
		})
	}
}
func TestInflightLimit_NormalizesHostAndSharesRegistry(t *testing.T) {
	registry := &inflightRegistry{limit: 1}
	base := inflightTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	first := inflightLimitedTransport{Transport: base, registry: registry}
	second := inflightLimitedTransport{Transport: base, registry: registry}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://EXAMPLE.org:443", nil)
	resp, err := first.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "https://example.org", nil)
	secondResp, err := second.RoundTrip(req)
	if secondResp != nil && secondResp.Body != nil {
		_ = secondResp.Body.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("host limits not shared: %v", err)
	}
}
