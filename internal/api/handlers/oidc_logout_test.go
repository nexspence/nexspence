package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

type stubOIDCLogout struct{ endSessionURL string }

func (s *stubOIDCLogout) AuthCodeURL(_, _, _ string) string { return "" }
func (s *stubOIDCLogout) ExchangeAndVerify(_ context.Context, _, _, _ string) (*auth.OIDCClaims, string, error) {
	return nil, "", nil
}
func (s *stubOIDCLogout) TestConnection(_ context.Context) error { return nil }
func (s *stubOIDCLogout) EndSessionEndpoint() string             { return s.endSessionURL }

func makeLogoutRouter(t *testing.T, oidcSvc auth.OIDCAuthenticator, userRepo *testutil.UserRepo, cfg config.OIDCConfig) *gin.Engine {
	t.Helper()
	log := zap.NewNop().Sugar()
	h := handlers.NewOIDCHandler(oidcSvc, nil, userRepo, nil, cfg, log)
	r := gin.New()
	r.GET("/api/v1/auth/oidc/logout", func(c *gin.Context) {
		c.Set("userID", "user-1") // simulate RequireAuth
	}, h.Logout)
	return r
}

func TestOIDCLogout_NonOIDCUser(t *testing.T) {
	userRepo := testutil.NewUserRepo(&domain.User{ID: "user-1", Username: "alice", Source: "local"})
	cfg := config.OIDCConfig{FrontendBaseURL: "http://app", ClientID: "nexspence"}
	r := makeLogoutRouter(t, &stubOIDCLogout{endSessionURL: "https://idp/logout"}, userRepo, cfg)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/auth/oidc/logout", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOIDCLogout_NoEndSessionEndpoint(t *testing.T) {
	userRepo := testutil.NewUserRepo(&domain.User{ID: "user-1", Username: "alice", Source: "oidc"})
	_ = userRepo.SetOIDCTokens(context.Background(), "user-1", "raw-id-token", "")

	cfg := config.OIDCConfig{FrontendBaseURL: "http://app", ClientID: "nexspence"}
	r := makeLogoutRouter(t, &stubOIDCLogout{endSessionURL: ""}, userRepo, cfg)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/auth/oidc/logout", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["logout_url"] != "http://app/login" {
		t.Fatalf("want fallback logout_url, got %q", resp["logout_url"])
	}
}

func TestOIDCLogout_Success(t *testing.T) {
	userRepo := testutil.NewUserRepo(&domain.User{ID: "user-1", Username: "alice", Source: "oidc"})
	_ = userRepo.SetOIDCTokens(context.Background(), "user-1", "raw-id-token", "")

	cfg := config.OIDCConfig{FrontendBaseURL: "http://app", ClientID: "nexspence"}
	r := makeLogoutRouter(t, &stubOIDCLogout{endSessionURL: "https://idp/end_session"}, userRepo, cfg)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/auth/oidc/logout", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	logoutURL := resp["logout_url"]
	if logoutURL == "" {
		t.Fatal("logout_url is empty")
	}
	if !strings.Contains(logoutURL, "id_token_hint=raw-id-token") {
		t.Errorf("logout_url missing id_token_hint: %s", logoutURL)
	}
	if !strings.Contains(logoutURL, "post_logout_redirect_uri=") {
		t.Errorf("logout_url missing post_logout_redirect_uri: %s", logoutURL)
	}
	if !strings.Contains(logoutURL, "client_id=nexspence") {
		t.Errorf("logout_url missing client_id: %s", logoutURL)
	}
}

func TestOIDCLogout_ClearsToken(t *testing.T) {
	userRepo := testutil.NewUserRepo(&domain.User{ID: "user-1", Username: "alice", Source: "oidc"})
	_ = userRepo.SetOIDCTokens(context.Background(), "user-1", "raw-id-token", "")

	cfg := config.OIDCConfig{FrontendBaseURL: "http://app", ClientID: "nexspence"}
	r := makeLogoutRouter(t, &stubOIDCLogout{endSessionURL: "https://idp/end_session"}, userRepo, cfg)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/auth/oidc/logout", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	tok, _ := userRepo.GetOIDCIDToken(context.Background(), "user-1")
	if tok != "" {
		t.Errorf("id_token should be cleared after logout, got %q", tok)
	}
}
