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

func buildSelectorRouter(t *testing.T) (*gin.Engine, *testutil.ContentSelectorRepo) {
	t.Helper()
	repo := testutil.NewContentSelectorRepo()
	svc, err := service.NewContentSelectorService(repo)
	if err != nil {
		t.Fatal(err)
	}
	h := handlers.NewContentSelectorHandler(svc)
	r := gin.New()
	r.GET("/cs", h.List)
	r.GET("/cs/:id", h.Get)
	r.POST("/cs", h.Create)
	r.PUT("/cs/:id", h.Update)
	r.DELETE("/cs/:id", h.Delete)
	r.PUT("/priv/:id/cs/:selectorId", h.AttachToPrivilege)
	r.DELETE("/priv/:id/cs", h.DetachFromPrivilege)
	return r, repo
}

func TestSelectorHandler_CreateListGetDelete(t *testing.T) {
	r, _ := buildSelectorRouter(t)

	// Create.
	body := `{"name":"mvn","description":"maven only","expression":"format == \"maven2\""}`
	req := httptest.NewRequest(http.MethodPost, "/cs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201 got %d body=%s", w.Code, w.Body.String())
	}
	var created domain.ContentSelector
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == "" {
		t.Fatal("id not returned")
	}

	// List returns one.
	req = httptest.NewRequest(http.MethodGet, "/cs", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var list []domain.ContentSelector
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("list: want 1 got %d", len(list))
	}

	// Get returns the same record.
	req = httptest.NewRequest(http.MethodGet, "/cs/"+created.ID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d", w.Code)
	}

	// Delete.
	req = httptest.NewRequest(http.MethodDelete, "/cs/"+created.ID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
}

func TestSelectorHandler_CreateRejectsBadCEL(t *testing.T) {
	r, _ := buildSelectorRouter(t)
	body := `{"name":"bad","expression":"format =="}` // syntax error
	req := httptest.NewRequest(http.MethodPost, "/cs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for invalid CEL, got %d", w.Code)
	}
}

func TestSelectorHandler_AttachDetachPrivilege(t *testing.T) {
	r, repo := buildSelectorRouter(t)

	// Seed one selector.
	body := `{"name":"mvn","expression":"true"}`
	req := httptest.NewRequest(http.MethodPost, "/cs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var sel domain.ContentSelector
	_ = json.Unmarshal(w.Body.Bytes(), &sel)

	// Attach to a privilege.
	req = httptest.NewRequest(http.MethodPut, "/priv/nx-view-maven/cs/"+sel.ID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("attach: want 204 got %d body=%s", w.Code, w.Body.String())
	}
	if got := repo.PrivilegeSelector["nx-view-maven"]; got != sel.ID {
		t.Fatalf("attach not persisted: got %q", got)
	}

	// Detach.
	req = httptest.NewRequest(http.MethodDelete, "/priv/nx-view-maven/cs", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("detach: want 204 got %d", w.Code)
	}
}
