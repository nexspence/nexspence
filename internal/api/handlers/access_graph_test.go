package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func buildAccessGraphRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	csRepo := testutil.NewContentSelectorRepo()
	cs := &domain.ContentSelector{Name: "cs-maven", Expression: `format == "maven2"`}
	_ = csRepo.Create(context.Background(), cs) // cs.ID assigned as "cs-1"

	csID := cs.ID
	userRepo := testutil.NewUserRepo(
		&domain.User{ID: "u1", Username: "alice", Email: "alice@corp.com",
			Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
			Roles: []string{"dev-read"}},
	)
	roleRepo := testutil.NewRoleRepo(
		&domain.Role{ID: "r1", Name: "dev-read", Description: "read access",
			Privileges: []string{"p1"}, Roles: []string{}},
	)
	privRepo := testutil.NewPrivilegeRepo(
		&domain.Privilege{ID: "p1", Name: "mvn-read",
			Type:              domain.PrivilegeTypeRepositoryContentSelector,
			ContentSelectorID: &csID},
	)

	h := handlers.NewAccessGraphHandler(userRepo, roleRepo, privRepo, csRepo)
	r := gin.New()
	r.GET("/access-graph", h.Get)
	return r
}

func TestAccessGraphHandler_Get_200(t *testing.T) {
	r := buildAccessGraphRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/access-graph", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}

	var resp handlers.AccessGraphResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Users) != 1 || resp.Users[0].Username != "alice" {
		t.Fatalf("want 1 user alice, got %+v", resp.Users)
	}
	if len(resp.Users[0].RoleIDs) != 1 || resp.Users[0].RoleIDs[0] != "r1" {
		t.Fatalf("want roleIds=[r1] got %v", resp.Users[0].RoleIDs)
	}
	if len(resp.Roles) != 1 || resp.Roles[0].ID != "r1" {
		t.Fatalf("want role r1 got %+v", resp.Roles)
	}
	if len(resp.Privileges) != 1 || resp.Privileges[0].ID != "p1" {
		t.Fatalf("want priv p1 got %+v", resp.Privileges)
	}
	if len(resp.Selectors) != 1 || resp.Selectors[0].Name != "cs-maven" {
		t.Fatalf("want selector cs-maven got %+v", resp.Selectors)
	}
}

func TestAccessGraphHandler_Get_EmptyGraph(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := handlers.NewAccessGraphHandler(
		testutil.NewUserRepo(),
		testutil.NewRoleRepo(),
		testutil.NewPrivilegeRepo(),
		testutil.NewContentSelectorRepo(),
	)
	r := gin.New()
	r.GET("/access-graph", h.Get)

	req := httptest.NewRequest(http.MethodGet, "/access-graph", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", w.Code)
	}
	var resp handlers.AccessGraphResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Users) != 0 || len(resp.Roles) != 0 || len(resp.Privileges) != 0 || len(resp.Selectors) != 0 {
		t.Fatalf("want empty arrays, got %+v", resp)
	}
}
