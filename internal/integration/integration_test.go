//go:build integration

// Package integration contains end-to-end tests that require a running
// PostgreSQL database and blob storage. Run with:
//
//	docker compose up -d postgres
//	NEXSPENCE_DB_DSN="postgres://nexspence:nexspence@localhost:5432/nexspence" \
//	    go test ./internal/integration/... -tags integration -v
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/api"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/db"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testServer *httptest.Server

func TestMain(m *testing.M) {
	dsn := os.Getenv("NEXSPENCE_DB_DSN")
	if dsn == "" {
		dsn = "postgres://nexspence:nexspence@localhost:5432/nexspence?sslmode=disable"
	}

	// Run migrations
	if err := db.Migrate(dsn, "up"); err != nil {
		fmt.Fprintf(os.Stderr, "migration failed: %v\n", err)
		os.Exit(1)
	}

	pool, err := db.Connect(dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "db connect failed: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "integration-test-secret-32bytes!!"
	cfg.Auth.JWTExpiryHours = 1
	cfg.Auth.BcryptCost = 4
	cfg.Storage.Local.BasePath = os.TempDir() + "/nexspence-integration-test"
	cfg.HTTP.BaseURL = "http://localhost"
	cfg.Log.Level = "error"
	cfg.Bootstrap.AdminUsername = "admin"
	cfg.Bootstrap.AdminPassword = "admin123"
	cfg.Bootstrap.AdminEmail = "admin@test.local"

	log := logger.New("error", "json")
	handler := api.NewRouter(cfg, pool, log)
	testServer = httptest.NewServer(handler)
	defer testServer.Close()

	os.Exit(m.Run())
}

// login returns a Bearer token for the given credentials.
func login(t *testing.T, username, password string) string {
	t.Helper()
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	resp, err := http.Post(testServer.URL+"/api/v1/login", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	token, ok := result["token"].(string)
	require.True(t, ok, "response missing token")
	return token
}

func authReq(t *testing.T, method, path string, body io.Reader, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, testServer.URL+path, body)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

// ── Tests ─────────────────────────────────────────────────────

func TestStatusCheck(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/service/rest/v1/status/check")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestLoginAndMe(t *testing.T) {
	token := login(t, "admin", "admin123")
	assert.NotEmpty(t, token)

	resp := authReq(t, http.MethodGet, "/api/v1/me", nil, token)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var me map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&me))
	assert.Equal(t, "admin", me["userId"])
}

func TestRepositoryCRUD(t *testing.T) {
	token := login(t, "admin", "admin123")

	// Create hosted raw repo
	createBody := `{"name":"integration-raw","online":true,"storage":{"blobStoreName":"default","strictContentTypeValidation":false}}`
	resp := authReq(t, http.MethodPost, "/service/rest/v1/repositories/raw/hosted",
		bytes.NewBufferString(createBody), token)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// List repos — should contain the new one
	listResp := authReq(t, http.MethodGet, "/service/rest/v1/repositories", nil, token)
	defer listResp.Body.Close()
	assert.Equal(t, http.StatusOK, listResp.StatusCode)

	// Get by name
	getResp := authReq(t, http.MethodGet, "/service/rest/v1/repositories/integration-raw", nil, token)
	defer getResp.Body.Close()
	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	// Delete
	delResp := authReq(t, http.MethodDelete, "/service/rest/v1/repositories/integration-raw", nil, token)
	defer delResp.Body.Close()
	assert.Equal(t, http.StatusNoContent, delResp.StatusCode)
}

func TestRawArtifactPushPull(t *testing.T) {
	token := login(t, "admin", "admin123")

	// Create repo
	createBody := `{"name":"e2e-raw","online":true,"storage":{"blobStoreName":"default","strictContentTypeValidation":false}}`
	resp := authReq(t, http.MethodPost, "/service/rest/v1/repositories/raw/hosted",
		bytes.NewBufferString(createBody), token)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	// Push artifact
	artifactData := []byte("integration test artifact content")
	putReq, _ := http.NewRequest(http.MethodPut,
		testServer.URL+"/repository/e2e-raw/integration/test/artifact.bin",
		bytes.NewReader(artifactData))
	putReq.Header.Set("Authorization", "Bearer "+token)
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putReq.ContentLength = int64(len(artifactData))
	putResp, err := http.DefaultClient.Do(putReq)
	require.NoError(t, err)
	putResp.Body.Close()
	assert.Equal(t, http.StatusCreated, putResp.StatusCode)

	// Pull artifact
	getResp := authReq(t, http.MethodGet,
		"/repository/e2e-raw/integration/test/artifact.bin", nil, token)
	defer getResp.Body.Close()
	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	pulled, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	assert.Equal(t, artifactData, pulled)

	// Cleanup
	delResp := authReq(t, http.MethodDelete, "/service/rest/v1/repositories/e2e-raw", nil, token)
	delResp.Body.Close()
}

func TestMetricsEndpoint(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/api/v1/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var snap map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&snap))
	assert.Contains(t, snap, "requests_total")
	assert.Contains(t, snap, "uptime_seconds")
}
