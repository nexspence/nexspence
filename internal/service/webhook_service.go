package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// WebhookService handles CRUD for webhooks and fires delivery goroutines.
type WebhookService struct {
	repo   repository.WebhookRepo
	client *http.Client
}

func NewWebhookService(repo repository.WebhookRepo) *WebhookService {
	return &WebhookService{
		repo:   repo,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *WebhookService) List(ctx context.Context) ([]domain.Webhook, error) {
	wh, err := s.repo.List(ctx)
	if wh == nil {
		wh = []domain.Webhook{}
	}
	return wh, err
}

func (s *WebhookService) Get(ctx context.Context, id string) (*domain.Webhook, error) {
	return s.repo.Get(ctx, id)
}

func (s *WebhookService) Create(ctx context.Context, w *domain.Webhook) error {
	if w.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if w.URL == "" {
		return fmt.Errorf("%w: url is required", ErrInvalidInput)
	}
	if len(w.Events) == 0 {
		return fmt.Errorf("%w: at least one event is required", ErrInvalidInput)
	}
	w.Active = true
	return s.repo.Create(ctx, w)
}

func (s *WebhookService) Update(ctx context.Context, w *domain.Webhook) error {
	if w.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if w.URL == "" {
		return fmt.Errorf("%w: url is required", ErrInvalidInput)
	}
	if len(w.Events) == 0 {
		return fmt.Errorf("%w: at least one event is required", ErrInvalidInput)
	}
	return s.repo.Update(ctx, w)
}

func (s *WebhookService) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

// Dispatch fires the payload to all active webhooks subscribed to payload.Event.
// Delivery is asynchronous — errors are silently dropped.
func (s *WebhookService) Dispatch(payload domain.WebhookPayload) {
	go func() {
		hooks, err := s.repo.ListByEvent(context.Background(), payload.Event)
		if err != nil || len(hooks) == 0 {
			return
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return
		}
		for _, wh := range hooks {
			if !wh.Active {
				continue
			}
			s.deliver(wh, body)
		}
	}()
}

func (s *WebhookService) deliver(wh domain.Webhook, body []byte) {
	req, err := http.NewRequest(http.MethodPost, wh.URL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Nexspence-Event", string(wh.Events[0]))
	if wh.Secret != "" {
		mac := hmac.New(sha256.New, []byte(wh.Secret))
		mac.Write(body)
		req.Header.Set("X-Nexspence-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
