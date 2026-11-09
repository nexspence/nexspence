//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// The rate limiter used to be installed on the engine, before any
// authentication middleware, so it never saw the userID it documents itself
// to key on: every request was counted against the client address, and every
// user behind one address (a corporate NAT, a CI runner, a Kubernetes node)
// shared a single bucket. The issue that reported it (#721) saw a `pnpm
// install` through an npm group answered with 429 because of traffic it had
// not made.
//
// These tests boot the real router with a limit small enough to exhaust in a
// couple of requests. The tokens come from the shared server: it has no rate
// limit configured, so the logins that mint them cannot spend the bucket the
// test measures.

const (
	rateLimitBurst = 2
	rateLimitRPS   = 0.0001 // effectively no refill while the test runs
)

// rateLimitedServer boots the real router with the rate limit enabled.
func rateLimitedServer(t *testing.T) *httptest.Server {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "integration-test-secret-32bytes!!"
	cfg.Auth.JWTExpiryHours = 1
	cfg.Auth.BcryptCost = 4
	cfg.Auth.PasswordMinLength = 8
	cfg.Auth.AnonymousEnabled = true
	cfg.Auth.RateLimitEnabled = true
	cfg.Auth.RateLimitRPS = rateLimitRPS
	cfg.Auth.RateLimitBurst = rateLimitBurst
	cfg.Storage.Local.BasePath = t.TempDir()
	cfg.HTTP.BaseURL = "http://" + l.Addr().String()
	cfg.HTTP.MaxBodyMB = 64
	cfg.Log.Level = "error"

	handler := api.NewRouter(context.Background(), cfg, pgtest.Pool(t), logger.New("error", "json"), "integration-test")
	ts := httptest.NewUnstartedServer(handler)
	_ = ts.Listener.Close()
	ts.Listener = l
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}

// get issues one request carrying the given bearer token.
func getStatus(t *testing.T, srv *httptest.Server, path, token string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// createUser makes a second account, so the test has two identities to
// compare. The admin token comes from the shared server.
func createUser(t *testing.T, username, password string) string {
	t.Helper()
	token := login(t, "admin", "admin123")
	body, err := json.Marshal(map[string]string{
		"userId":       username,
		"emailAddress": username + "@test.local",
		"password":     password,
	})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost,
		server(t).URL+"/service/rest/v1/security/users", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create user: %s", raw)
	return token
}

// Two users behind one client address must not share a bucket: the limit is
// documented as "per authenticated user, or per client address for anonymous
// calls" (docs/security-rbac.md).
func TestRateLimit_AuthenticatedUsersHaveSeparateBuckets(t *testing.T) {
	const secondUser = "ratelimit-user-b"
	const password = "ratelimit-pass-1"
	adminToken := createUser(t, secondUser, password)

	// The second account's own token, minted on the shared server.
	secondToken := login(t, secondUser, password)

	srv := rateLimitedServer(t)

	// The first user spends the whole bucket.
	for i := 0; i < rateLimitBurst; i++ {
		assert.Equal(t, http.StatusOK, getStatus(t, srv, "/api/v1/me", adminToken),
			"request %d of the first user's bucket must pass", i+1)
	}
	assert.Equal(t, http.StatusTooManyRequests, getStatus(t, srv, "/api/v1/me", adminToken),
		"the first user must be limited once its own bucket is empty")

	// The second user has made no request at all. It shares the client
	// address with the first one, and that must not matter.
	assert.NotEqual(t, http.StatusTooManyRequests, getStatus(t, srv, "/api/v1/me", secondToken),
		"a second user behind the same client address must not inherit the first user's exhausted bucket")
}

// The same user from two addresses shares one bucket: the limit follows the
// identity, not the network path.
func TestRateLimit_SameUserSharesOneBucketAcrossAddresses(t *testing.T) {
	token := login(t, "admin", "admin123")
	srv := rateLimitedServer(t)

	for i := 0; i < rateLimitBurst; i++ {
		assert.Equal(t, http.StatusOK, getStatus(t, srv, "/api/v1/me", token))
	}

	// A second client address, same token.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/me", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode,
		"the same user must be limited on a second address as well")
}

// The login route has no identity to key on, so it stays limited by address:
// the bcrypt cost-12 login surface is why rate limiting is on by default.
func TestRateLimit_LoginStaysLimitedByAddress(t *testing.T) {
	srv := rateLimitedServer(t)

	status := loginStatus(t, srv, "admin", "wrong-password")
	assert.Equal(t, http.StatusUnauthorized, status,
		"the first login attempt must be answered, not limited")

	for i := 0; i < rateLimitBurst+1; i++ {
		status = loginStatus(t, srv, "admin", "wrong-password")
	}
	assert.Equal(t, http.StatusTooManyRequests, status,
		"repeated logins from one address must be limited")
}

// loginStatus posts a login and returns the status code.
func loginStatus(t *testing.T, srv *httptest.Server, username, password string) int {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	resp, err := http.Post(srv.URL+"/api/v1/login", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// A readiness probe answered with 429 marks the pod unready and takes the
// instance out of service. The probes must never be limited.
func TestRateLimit_HealthProbesAreNeverLimited(t *testing.T) {
	srv := rateLimitedServer(t)

	for i := 0; i < rateLimitBurst+3; i++ {
		assert.NotEqual(t, http.StatusTooManyRequests, getStatus(t, srv, "/healthz", ""),
			"probe %d must not be rate limited", i+1)
	}
}
