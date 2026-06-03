package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func buildTagsRouter(comps *testutil.ComponentRepo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handlers.NewComponentHandler(comps, testutil.NewAssetRepo(), testutil.NewRepoRepo(), "http://localhost")
	r.PUT("/service/rest/v1/components/:id/tags", h.SetTags)
	return r
}

func TestSetTags_OK(t *testing.T) {
	comps := testutil.NewComponentRepo()
	comps.AddComponent(&domain.Component{ID: "comp-1", Name: "spring-core", Tags: []string{}})

	body, _ := json.Marshal(map[string]any{"tags": []string{"prod", "team:backend"}})
	req := httptest.NewRequest(http.MethodPut, "/service/rest/v1/components/comp-1/tags", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	buildTagsRouter(comps).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string][]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp["tags"]) != 2 || resp["tags"][0] != "prod" {
		t.Fatalf("unexpected tags: %v", resp["tags"])
	}
}

func TestSetTags_InvalidJSON(t *testing.T) {
	comps := testutil.NewComponentRepo()
	req := httptest.NewRequest(http.MethodPut, "/service/rest/v1/components/x/tags", bytes.NewReader([]byte(`{bad`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	buildTagsRouter(comps).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestSetTags_EmptyTagsTrimmed(t *testing.T) {
	comps := testutil.NewComponentRepo()
	comps.AddComponent(&domain.Component{ID: "comp-2", Name: "nginx", Tags: []string{"old"}})

	body, _ := json.Marshal(map[string]any{"tags": []string{"  stable  ", "", "  "}})
	req := httptest.NewRequest(http.MethodPut, "/service/rest/v1/components/comp-2/tags", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	buildTagsRouter(comps).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string][]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp["tags"]) != 1 || resp["tags"][0] != "stable" {
		t.Fatalf("expected [stable], got %v", resp["tags"])
	}
}
