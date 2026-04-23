package service_test

import (
	"context"
	"sync"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

type capturingDispatcher struct {
	mu     sync.Mutex
	events []domain.WebhookPayload
}

func (c *capturingDispatcher) Dispatch(p domain.WebhookPayload) {
	c.mu.Lock()
	c.events = append(c.events, p)
	c.mu.Unlock()
}

func (c *capturingDispatcher) snapshot() []domain.WebhookPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]domain.WebhookPayload(nil), c.events...)
}

func newRepoSvc() *service.RepositoryService {
	return service.NewRepositoryService(
		testutil.NewRepoRepo(),
		testutil.NewBlobStoreRepo(),
		testutil.NewBlobStore(),
		testutil.NewCleanupPolicyRepo(),
	)
}

func TestRepositoryService_Create_DispatchesRepoCreated(t *testing.T) {
	svc := newRepoSvc()
	d := &capturingDispatcher{}
	svc.WithWebhooks(d)

	repo := &domain.Repository{
		Name:   "my-repo",
		Format: domain.FormatRaw,
		Type:   domain.TypeHosted,
	}
	if err := svc.Create(context.Background(), repo); err != nil {
		t.Fatal(err)
	}

	events := d.snapshot()
	if len(events) == 0 {
		t.Fatal("expected repo.created event to be dispatched")
	}
	got := events[0]
	if got.Event != domain.EventRepoCreated {
		t.Errorf("event = %q, want %q", got.Event, domain.EventRepoCreated)
	}
	if got.Repository != "my-repo" {
		t.Errorf("repository = %q, want %q", got.Repository, "my-repo")
	}
}

func TestRepositoryService_Create_NoDispatch_WhenWebhooksNil(t *testing.T) {
	svc := newRepoSvc()
	repo := &domain.Repository{
		Name:   "safe-repo",
		Format: domain.FormatRaw,
		Type:   domain.TypeHosted,
	}
	if err := svc.Create(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
}
