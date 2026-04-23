package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

type capturingDispatcher struct {
	events []domain.WebhookPayload
}

func (c *capturingDispatcher) Dispatch(p domain.WebhookPayload) {
	c.events = append(c.events, p)
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

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(d.events) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if len(d.events) == 0 {
		t.Fatal("expected repo.created event to be dispatched")
	}
	got := d.events[0]
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
