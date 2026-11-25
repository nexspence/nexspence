package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/logger"
)

// The sidebar footer shows the product version to every visitor, so the
// version route must live on the public group; the system info next to it
// carries the storage configuration and must stay admin-only. Which group a
// route sits on is decided in the router, not in the handler — a handler
// test cannot tell the two apart, so this test drives the real router.
func TestRouter_Version_IsPublicAndSystemInfoStaysAdminOnly(t *testing.T) {
	h := newRouterForTest(t, "v2.13.4")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "v2.13.4", body["version"])
	assert.Equal(t, "Nexspence", body["product"])
	// Only the version travels: no storage paths and no other admin field.
	assert.Equal(t, []string{"product", "version"}, sortedKeys(body))
	assert.NotContains(t, strings.ToLower(rec.Body.String()), "base_path")
	assert.NotContains(t, strings.ToLower(rec.Body.String()), "storage")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil))
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "body: %s", rec.Body.String())
}

// newRouterForTest builds the real router over a pool that cannot be
// reached: only the handlers that read from the database need it, and the
// version endpoint is not one of them.
func newRouterForTest(t *testing.T, version string) http.Handler {
	t.Helper()
	cfg := &config.Config{}
	cfg.Storage.DefaultType = "local"
	cfg.Storage.Local.BasePath = t.TempDir()
	cfg.Scan.Enabled = false
	cfg.Auth.RateLimitEnabled = false

	pool, err := pgxpool.New(context.Background(),
		"postgres://nope:nope@127.0.0.1:1/nope?connect_timeout=1")
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	// The router starts background schedulers; the context ends them again.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	return NewRouter(ctx, cfg, pool, logger.New("error", "json"), version)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
