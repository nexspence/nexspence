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

func newTokenStack(users ...*domain.User) (*service.UserService, *service.TokenService) {
	userSvc := newUserSvc(users...)
	userRepo := testutil.NewUserRepo(users...)
	tokenRepo := testutil.NewUserTokenRepo()
	tokenSvc := service.NewTokenService(tokenRepo, userRepo)
	return userSvc, tokenSvc
}

func buildTokenAPIRouter(userSvc *service.UserService, tokenSvc *service.TokenService) *gin.Engine {
	r := gin.New()
	r.Use(handlers.AuthMiddleware(userSvc, tokenSvc))
	h := handlers.NewTokenHandler(tokenSvc, userSvc, 90)
	r.GET("/api/v1/user-tokens", h.List)
	r.POST("/api/v1/user-tokens", h.Create)
	r.DELETE("/api/v1/user-tokens/:id", h.Delete)
	r.GET("/protected", func(c *gin.Context) {
		u, _ := c.Get("username")
		c.JSON(http.StatusOK, gin.H{"user": u})
	})
	return r
}

func TestTokenAPI_CreateAndAuthenticate(t *testing.T) {
	alice := activeUser("alice", "pw")
	userSvc, tokenSvc := newTokenStack(alice)
	r := buildTokenAPIRouter(userSvc, tokenSvc)

	jwt := bearerToken(userSvc, "alice")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user-tokens",
		strings.NewReader(`{"name":"ci"}`))
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201 got %d body=%s", w.Code, w.Body.String())
	}

	var created domain.UserToken
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Token == "" {
		t.Fatal("plaintext token missing from response")
	}

	req = httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+created.Token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("token auth: want 200 got %d", w.Code)
	}
}

func TestTokenAPI_UnauthedCreateRejected(t *testing.T) {
	alice := activeUser("alice", "pw")
	userSvc, tokenSvc := newTokenStack(alice)
	r := buildTokenAPIRouter(userSvc, tokenSvc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user-tokens",
		strings.NewReader(`{"name":"ci"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d", w.Code)
	}
}

func TestTokenAPI_DeleteScopedToOwner(t *testing.T) {
	alice := activeUser("alice", "pw")
	bob := activeUser("bob", "pw")
	userSvc, tokenSvc := newTokenStack(alice, bob)
	r := buildTokenAPIRouter(userSvc, tokenSvc)

	aliceJWT := bearerToken(userSvc, "alice")
	bobJWT := bearerToken(userSvc, "bob")

	// Bob creates a token.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user-tokens",
		strings.NewReader(`{"name":"bob-ci"}`))
	req.Header.Set("Authorization", "Bearer "+bobJWT)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("bob create: %d", w.Code)
	}
	var bobToken domain.UserToken
	_ = json.Unmarshal(w.Body.Bytes(), &bobToken)

	// Alice tries to delete Bob's token — should 404.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/user-tokens/"+bobToken.ID, nil)
	req.Header.Set("Authorization", "Bearer "+aliceJWT)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("alice delete bob token: want 404 got %d", w.Code)
	}

	// Bob deletes his own.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/user-tokens/"+bobToken.ID, nil)
	req.Header.Set("Authorization", "Bearer "+bobJWT)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("bob delete: want 204 got %d", w.Code)
	}
}

func TestAuthMiddleware_AcceptsAPITokenViaBasic(t *testing.T) {
	alice := activeUser("alice", "pw")
	userSvc, tokenSvc := newTokenStack(alice)
	r := buildTokenAPIRouter(userSvc, tokenSvc)

	jwt := bearerToken(userSvc, "alice")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user-tokens",
		strings.NewReader(`{"name":"ci"}`))
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}
	var tok domain.UserToken
	_ = json.Unmarshal(w.Body.Bytes(), &tok)

	// Use the plaintext token as a Basic-auth password (CI-friendly: works
	// with tools that only speak Basic like older Maven or curl -u).
	req = httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.SetBasicAuth("alice", tok.Token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("basic token auth: want 200 got %d", w.Code)
	}
}
