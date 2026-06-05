package service

import "github.com/nexspence-oss/nexspence/internal/domain"

// MultiDispatcher fans out a single Dispatch call to multiple downstream
// dispatchers (e.g. webhook delivery + in-process SSE broker).
// Nil entries are tolerated and skipped.
type MultiDispatcher struct {
	Targets []domain.WebhookDispatcher
}

// NewMultiDispatcher returns a dispatcher that fans out to the given targets.
func NewMultiDispatcher(targets ...domain.WebhookDispatcher) *MultiDispatcher {
	return &MultiDispatcher{Targets: targets}
}

// Dispatch forwards the payload to every non-nil target.
func (m *MultiDispatcher) Dispatch(payload domain.WebhookPayload) {
	for _, t := range m.Targets {
		if t != nil {
			t.Dispatch(payload)
		}
	}
}
