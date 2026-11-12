package repoproxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func checkLimits(t *testing.T, c *http.Client, limit int) {
	t.Helper()
	tr := c.Transport.(inflightLimitedTransport).Transport.(idleGuardedTransport).Transport
	if tr.MaxIdleConnsPerHost != 32 || tr.MaxConnsPerHost != limit || !tr.ForceAttemptHTTP2 {
		t.Fatalf("transport limits: idle=%d, max=%d, h2=%v", tr.MaxIdleConnsPerHost, tr.MaxConnsPerHost, tr.ForceAttemptHTTP2)
	}
}
func TestUpstreamClient_TransportLimits(t *testing.T) { checkLimits(t, newUpstreamClient(), 64) }
func TestBuildProxyClient_TransportLimits(t *testing.T) {
	for _, s := range []proxySettings{{httpProxy: "http://localhost:8080"}, {socks5Proxy: "localhost:1080"}} {
		c, err := buildProxyClient(s)
		if err != nil {
			t.Fatal(err)
		}
		checkLimits(t, c, 64)
	}
}
func TestSetUpstreamConnLimits_AppliesToSharedAndProxyClients(t *testing.T) {
	defer upstreamConnLimit.Store(0)
	defer upstreamInflight.configure(proxyMaxInflightPerHost)
	client := newUpstreamClient()
	var configured atomic.Bool
	if err := setUpstreamConnLimits(client, &configured, 4, 2); err != nil {
		t.Fatal(err)
	}
	checkLimits(t, client, 4)
	proxied, err := buildProxyClient(proxySettings{httpProxy: "http://localhost:8080"})
	if err != nil {
		t.Fatal(err)
	}
	checkLimits(t, proxied, 4)
}
func TestSetUpstreamConnLimits_SecondCallFails(t *testing.T) {
	defer upstreamConnLimit.Store(0)
	defer upstreamInflight.configure(proxyMaxInflightPerHost)
	client := newUpstreamClient()
	var configured atomic.Bool
	if err := setUpstreamConnLimits(client, &configured, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := setUpstreamConnLimits(client, &configured, 1, 1); err == nil {
		t.Fatal("second call succeeded")
	}
}
func TestSetUpstreamConnLimits_RejectsForeignTransport(t *testing.T) {
	client := newUpstreamClient()
	client.Transport = http.DefaultTransport
	var configured atomic.Bool
	if err := setUpstreamConnLimits(client, &configured, 1, 1); err == nil {
		t.Fatal("foreign transport accepted")
	}
	if configured.Load() {
		t.Fatal("invalid transport consumed configuration")
	}
}
func TestUpstreamClient_MaxConnsPerHost_Bounded(t *testing.T) {
	var mu sync.Mutex
	active, maximum := 0, 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		mu.Lock()
		defer mu.Unlock()
		switch state {
		case http.StateNew:
			active++
			if active > maximum {
				maximum = active
			}
		case http.StateClosed:
			active--
		}
	}
	server.Start()
	defer server.Close()
	tr := newUpstreamClient().Transport.(inflightLimitedTransport).Transport.(idleGuardedTransport).Clone()
	tr.MaxConnsPerHost = 4
	tr.Proxy = nil
	tr.DialContext = (&net.Dialer{}).DialContext
	client := &http.Client{Transport: idleGuardedTransport{Transport: tr, idle: idleBodyTimeout}}
	defer client.CloseIdleConnections()
	errs := make(chan error, 20)
	for range 20 {
		go func() {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
			if err == nil {
				var resp *http.Response
				resp, err = client.Do(req)
				if err == nil {
					_, err = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}
			errs <- err
		}()
	}
	for range 20 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if maximum > 4 {
		t.Fatalf("maximum connections: %d", maximum)
	}
}

func TestIdleGuardedTransport_HTTP2_IdleStreamDoesNotBlockConnection(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/idle" {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	tr := server.Client().Transport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = true
	client := &http.Client{Transport: idleGuardedTransport{Transport: tr, idle: 30 * time.Millisecond}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/idle", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ProtoMajor != 2 {
		t.Fatalf("protocol: %s", resp.Proto)
	}
	_, readErr := io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if readErr == nil {
		t.Fatal("idle stream did not time out")
	}
	req, err = http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/ok", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(body) != "ok" {
		t.Fatalf("second stream: %q, %v", body, err)
	}
}

func TestSetUpstreamConnLimits_RejectsInvalidSettings(t *testing.T) {
	for _, limits := range [][2]int{{-1, 1}, {1, -1}} {
		client := newUpstreamClient()
		var configured atomic.Bool
		if err := setUpstreamConnLimits(client, &configured, limits[0], limits[1]); err == nil {
			t.Fatal("negative limit accepted")
		}
		if configured.Load() {
			t.Fatal("invalid settings consumed configuration")
		}
	}
}

func TestSetUpstreamConnLimits_RejectsForeignGuard(t *testing.T) {
	client := newUpstreamClient()
	client.Transport = inflightLimitedTransport{Transport: http.DefaultTransport, registry: &inflightRegistry{}}
	var configured atomic.Bool
	if err := setUpstreamConnLimits(client, &configured, 1, 1); err == nil {
		t.Fatal("foreign guard accepted")
	}
}
