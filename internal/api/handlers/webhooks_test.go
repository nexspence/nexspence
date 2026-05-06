package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func buildWebhookRouter(t *testing.T) (*gin.Engine, *testutil.WebhookRepo) {
	t.Helper()
	repo := testutil.NewWebhookRepo()
	svc := service.NewWebhookService(repo)
	h := handlers.NewWebhookHandler(svc)
	r := gin.New()
	r.GET("/webhooks/:id", h.Get)
	r.POST("/webhooks", h.Create)
	r.POST("/webhooks/:id/test", h.Test)
	return r, repo
}

func TestWebhookHandler_Get_200(t *testing.T) {
	r, _ := buildWebhookRouter(t)

	// Create a webhook first.
	body := `{"name":"ci","url":"http://example.com/hook","events":["repo.created"],"active":true}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201 got %d body=%s", w.Code, w.Body.String())
	}
	var created domain.Webhook
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// Get by ID.
	req = httptest.NewRequest(http.MethodGet, "/webhooks/"+created.ID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get: want 200 got %d", w.Code)
	}
	var got domain.Webhook
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.ID != created.ID {
		t.Fatalf("get: id mismatch: want %q got %q", created.ID, got.ID)
	}
}

func TestWebhookHandler_Get_404(t *testing.T) {
	r, _ := buildWebhookRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/webhooks/does-not-exist", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 got %d", w.Code)
	}
}

func TestWebhookHandler_Test_200(t *testing.T) {
	// Stand up a local receiver that always returns 200.
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	r, _ := buildWebhookRouter(t)

	// Create a webhook pointing at the local receiver.
	body := `{"name":"live","url":"` + receiver.URL + `","events":["repo.created"],"active":true}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var created domain.Webhook
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	// Fire the test ping.
	req = httptest.NewRequest(http.MethodPost, "/webhooks/"+created.ID+"/test", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("test: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	var res service.TestResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode TestResult: %v", err)
	}
	if res.Status != http.StatusOK {
		t.Fatalf("test result status: want 200 got %d", res.Status)
	}
}

func TestWebhookHandler_Test_404(t *testing.T) {
	r, _ := buildWebhookRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/no-such-id/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 got %d", w.Code)
	}
}

func TestWebhookHandler_Test_502(t *testing.T) {
	// Start a server then immediately close it — delivery will fail.
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	r, _ := buildWebhookRouter(t)

	body := `{"name":"dead","url":"` + closedURL + `","events":["repo.created"],"active":true}`
	req := httptest.NewRequest(http.MethodPost, "/webhooks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var created domain.Webhook
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	req = httptest.NewRequest(http.MethodPost, "/webhooks/"+created.ID+"/test", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502 got %d body=%s", w.Code, w.Body.String())
	}
}
