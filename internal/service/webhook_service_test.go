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
	return service.NewWebhookService(repo), repo
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
	var sigHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sigHeader = r.Header.Get("X-Nexspence-Signature")
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
		if sigHeader != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if sigHeader == "" {
		t.Fatal("expected X-Nexspence-Signature header")
	}
	if len(sigHeader) < 8 || sigHeader[:7] != "sha256=" {
		t.Errorf("unexpected signature format: %s", sigHeader)
	}
}
