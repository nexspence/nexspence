package service_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func newWebhookSvc() (*service.WebhookService, *testutil.WebhookRepo) {
	repo := testutil.NewWebhookRepo()
	// Inject a plain client so deliveries can reach loopback httptest servers
	// (the production client is SSRF-guarded and blocks 127.0.0.1).
	return service.NewWebhookService(repo).WithHTTPClient(&http.Client{Timeout: 10 * time.Second}), repo
}

func TestWebhookService_CRUD(t *testing.T) {
	svc, _ := newWebhookSvc()
	ctx := context.Background()

	wh := &domain.Webhook{
		Name:   "test",
		URL:    "http://example.com/hook",
		Events: []domain.WebhookEvent{domain.EventArtifactPublished},
	}
	if err := svc.Create(ctx, wh); err != nil {
		t.Fatal(err)
	}
	if wh.ID == "" {
		t.Fatal("expected ID after Create")
	}

	got, err := svc.Get(ctx, wh.ID)
	if err != nil || got == nil {
		t.Fatal("Get failed:", err)
	}
	if got.Name != "test" {
		t.Errorf("name mismatch: %s", got.Name)
	}

	wh.Name = "updated"
	if err := svc.Update(ctx, wh); err != nil {
		t.Fatal("Update:", err)
	}

	list, err := svc.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: got %d items, err=%v", len(list), err)
	}

	if err := svc.Delete(ctx, wh.ID); err != nil {
		t.Fatal("Delete:", err)
	}
	list, _ = svc.List(ctx)
	if len(list) != 0 {
		t.Error("expected empty list after Delete")
	}
}

func TestWebhookService_Create_Validation(t *testing.T) {
	svc, _ := newWebhookSvc()
	ctx := context.Background()

	cases := []struct {
		wh  domain.Webhook
		err string
	}{
		{domain.Webhook{URL: "http://x.com", Events: []domain.WebhookEvent{domain.EventRepoCreated}}, "name"},
		{domain.Webhook{Name: "x", Events: []domain.WebhookEvent{domain.EventRepoCreated}}, "url"},
		{domain.Webhook{Name: "x", URL: "http://x.com"}, "event"},
	}
	for _, tc := range cases {
		cp := tc.wh
		if err := svc.Create(ctx, &cp); err == nil {
			t.Errorf("expected error containing %q, got nil", tc.err)
		}
	}
}

func TestWebhookService_Dispatch_Fires(t *testing.T) {
	var (
		mu       sync.Mutex
		received []domain.WebhookPayload
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p domain.WebhookPayload
		_ = json.Unmarshal(body, &p)
		mu.Lock()
		received = append(received, p)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc, _ := newWebhookSvc()
	ctx := context.Background()
	_ = svc.Create(ctx, &domain.Webhook{
		Name:   "hook",
		URL:    srv.URL,
		Events: []domain.WebhookEvent{domain.EventArtifactPublished},
	})

	svc.Dispatch(domain.WebhookPayload{
		Event:      domain.EventArtifactPublished,
		Timestamp:  time.Now(),
		Repository: "myrepo",
	})

	// wait for async delivery
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(received))
	}
	if received[0].Repository != "myrepo" {
		t.Errorf("payload mismatch: %+v", received[0])
	}
}

func TestWebhookService_Dispatch_SkipsInactive(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc, repo := newWebhookSvc()
	ctx := context.Background()
	wh := &domain.Webhook{
		Name:   "hook",
		URL:    srv.URL,
		Events: []domain.WebhookEvent{domain.EventArtifactPublished},
	}
	_ = svc.Create(ctx, wh)
	// deactivate directly
	wh.Active = false
	_ = repo.Update(ctx, wh)

	svc.Dispatch(domain.WebhookPayload{Event: domain.EventArtifactPublished})
	time.Sleep(100 * time.Millisecond)
	if calls != 0 {
		t.Errorf("expected 0 deliveries to inactive hook, got %d", calls)
	}
}

func TestWebhookService_Dispatch_SkipsWrongEvent(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc, _ := newWebhookSvc()
	ctx := context.Background()
	_ = svc.Create(ctx, &domain.Webhook{
		Name:   "hook",
		URL:    srv.URL,
		Events: []domain.WebhookEvent{domain.EventRepoCreated},
	})

	svc.Dispatch(domain.WebhookPayload{Event: domain.EventArtifactPublished})
	time.Sleep(100 * time.Millisecond)
	if calls != 0 {
		t.Errorf("expected 0 deliveries, got %d", calls)
	}
}

func TestWebhookService_Dispatch_HMACSignature(t *testing.T) {
	var (
		mu        sync.Mutex
		sigHeader string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sigHeader = r.Header.Get("X-Nexspence-Signature")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc, _ := newWebhookSvc()
	ctx := context.Background()
	_ = svc.Create(ctx, &domain.Webhook{
		Name:   "secure",
		URL:    srv.URL,
		Secret: "mysecret",
		Events: []domain.WebhookEvent{domain.EventArtifactPublished},
	})

	svc.Dispatch(domain.WebhookPayload{Event: domain.EventArtifactPublished})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		h := sigHeader
		mu.Unlock()
		if h != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	h := sigHeader
	mu.Unlock()
	if h == "" {
		t.Fatal("expected X-Nexspence-Signature header")
	}
	if len(h) < 8 || h[:7] != "sha256=" {
		t.Errorf("unexpected signature format: %s", h)
	}
}

func TestWebhookService_Deliver_CorrectEventHeader(t *testing.T) {
	var (
		mu        sync.Mutex
		gotHeader string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotHeader = r.Header.Get("X-Nexspence-Event")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc, _ := newWebhookSvc()
	ctx := context.Background()
	_ = svc.Create(ctx, &domain.Webhook{
		Name:   "hook2",
		URL:    srv.URL,
		Events: []domain.WebhookEvent{domain.EventArtifactPublished},
	})
	svc.Dispatch(domain.WebhookPayload{Event: domain.EventArtifactPublished})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		h := gotHeader
		mu.Unlock()
		if h != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	h := gotHeader
	mu.Unlock()
	if h != "artifact.published" {
		t.Errorf("X-Nexspence-Event = %q, want %q", h, "artifact.published")
	}
}

func TestWebhookService_Test_ReturnsStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	svc, _ := newWebhookSvc()
	ctx := context.Background()
	wh := &domain.Webhook{
		Name:   "h",
		URL:    srv.URL,
		Events: []domain.WebhookEvent{domain.EventArtifactPublished},
	}
	_ = svc.Create(ctx, wh)

	res, err := svc.Test(ctx, wh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != http.StatusNoContent {
		t.Errorf("status = %d, want 204", res.Status)
	}
	if res.LatencyMs < 0 {
		t.Errorf("latency must be >= 0")
	}
}

func TestWebhookService_Test_NotFound(t *testing.T) {
	svc, _ := newWebhookSvc()
	_, err := svc.Test(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for unknown webhook id")
	}
}
