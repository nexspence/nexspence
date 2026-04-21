package handlers_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func init() { gin.SetMode(gin.TestMode) }

const testSecret = "test-jwt-secret-1234"

func newUserSvc(users ...*domain.User) *service.UserService {
	authSvc := auth.NewService(testSecret, 24, bcryptCostTest)
	userRepo := testutil.NewUserRepo(users...)
	roleRepo := testutil.NewRoleRepo()
	return service.NewUserService(userRepo, roleRepo, authSvc, zap.NewNop().Sugar())
}

// bcryptCostTest uses cost=4 (minimum) to keep tests fast.
const bcryptCostTest = 4

func activeUser(username, password string) *domain.User {
	authSvc := auth.NewService(testSecret, 24, bcryptCostTest)
	hash, _ := authSvc.HashPassword(password)
	return &domain.User{
		ID:           "uid-" + username,
		Username:     username,
		PasswordHash: hash,
		Status:       domain.UserStatusActive,
	}
}

func bearerToken(svc *service.UserService, username string) string {
	authSvc := auth.NewService(testSecret, 24, bcryptCostTest)
	tok, _ := authSvc.GenerateToken("uid-"+username, username, nil)
	return tok
}

func buildAuthRouter(svc *service.UserService) *gin.Engine {
	r := gin.New()
	r.Use(handlers.AuthMiddleware(svc, nil))
	r.GET("/protected", func(c *gin.Context) {
		username, _ := c.Get("username")
		c.JSON(http.StatusOK, gin.H{"user": username})
	})
	return r
}

// ── AuthMiddleware ────────────────────────────────────────────

func TestAuthMiddleware_ValidBearer(t *testing.T) {
	user := activeUser("alice", "pass123")
	svc := newUserSvc(user)
	r := buildAuthRouter(svc)

	token := bearerToken(svc, "alice")
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_InvalidBearer_Returns401(t *testing.T) {
	svc := newUserSvc()
	r := buildAuthRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_NoAuth_Returns401(t *testing.T) {
	svc := newUserSvc()
	r := buildAuthRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_ValidBasicAuth(t *testing.T) {
	user := activeUser("bob", "secret")
	svc := newUserSvc(user)
	r := buildAuthRouter(svc)

	creds := base64.StdEncoding.EncodeToString([]byte("bob:secret"))
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Basic "+creds)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_WrongBasicPassword_Returns401(t *testing.T) {
	user := activeUser("charlie", "rightpassword")
	svc := newUserSvc(user)
	r := buildAuthRouter(svc)

	creds := base64.StdEncoding.EncodeToString([]byte("charlie:wrongpassword"))
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Basic "+creds)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ── OptionalAuth ──────────────────────────────────────────────

func buildOptionalAuthRouter(svc *service.UserService) *gin.Engine {
	r := gin.New()
	r.Use(handlers.OptionalAuth(svc, nil))
	r.GET("/open", func(c *gin.Context) {
		username, _ := c.Get("username")
		c.JSON(http.StatusOK, gin.H{"user": username})
	})
	return r
}

func TestOptionalAuth_NoAuth_Passes(t *testing.T) {
	svc := newUserSvc()
	r := buildOptionalAuthRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/open", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestOptionalAuth_ValidBearer_SetsUser(t *testing.T) {
	user := activeUser("dave", "pw")
	svc := newUserSvc(user)
	r := buildOptionalAuthRouter(svc)

	token := bearerToken(svc, "dave")
	req := httptest.NewRequest(http.MethodGet, "/open", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "dave")
}

// ── AuthHandler.Login ─────────────────────────────────────────

func TestLogin_ValidCredentials(t *testing.T) {
	user := activeUser("eve", "mypass")
	svc := newUserSvc(user)

	r := gin.New()
	authH := handlers.NewAuthHandler(svc, zap.NewNop().Sugar())
	r.POST("/api/v1/login", authH.Login)

	body := `{"username":"eve","password":"mypass"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login", stringReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"token"`)
}

func TestLogin_WrongPassword_Returns401(t *testing.T) {
	user := activeUser("frank", "correct")
	svc := newUserSvc(user)

	r := gin.New()
	authH := handlers.NewAuthHandler(svc, zap.NewNop().Sugar())
	r.POST("/api/v1/login", authH.Login)

	body := `{"username":"frank","password":"wrong"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login", stringReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func stringReader(s string) *strings.Reader { return strings.NewReader(s) }

var _ = stringReader // used in Login tests above
