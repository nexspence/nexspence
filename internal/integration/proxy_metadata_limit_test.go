//go:build integration

package integration

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// Load the operator's env override and boot the real router. A configurable
// helper alone is insufficient: the setting must reach the format handlers.
func TestProxyNpm_ConfiguredMetadataLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"name":"vite","description":"`+strings.Repeat("a", 1024)+`"}`)
	}))
	defer upstream.Close()
	createProxyRepo(t, "npm", "npm-configured-limit", upstream.URL)
	token := login(t, "admin", "admin123")
	t.Setenv("NEXSPENCE_DATABASE_DSN", "postgres://unused/db")
	t.Setenv("NEXSPENCE_AUTH_JWT_SECRET", "integration-test-secret-32bytes!!")
	t.Setenv("NEXSPENCE_PROXY_MAX_REWRITTEN_METADATA_BYTES", "1024")
	cfg, err := config.Load(filepath.Join(t.TempDir(), "absent.yaml"))
	require.NoError(t, err)
	cfg.Storage.Local.BasePath = t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := api.NewRouter(ctx, cfg, pgtest.Pool(t), logger.New("error", "json"), "integration-test")
	srv := httptest.NewServer(handler)
	defer srv.Close()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/repository/npm-configured-limit/vite", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Contains(t, string(body), "1024 byte rewrite size limit")
}
