package config

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoad_ProxyConnections(t *testing.T) {
	t.Setenv("NEXSPENCE_DATABASE_DSN", "postgres://localhost/db")
	t.Setenv("NEXSPENCE_AUTH_JWT_SECRET", "a-unique-production-secret-at-least-32b")
	for _, tc := range []struct {
		key      string
		env      string
		fallback int
		get      func(*Config) int
	}{
		{"max_conns_per_host", "NEXSPENCE_PROXY_MAX_CONNS_PER_HOST", 64, func(c *Config) int { return c.Proxy.MaxConnsPerHost }},
		{"max_inflight_per_host", "NEXSPENCE_PROXY_MAX_INFLIGHT_PER_HOST", 32, func(c *Config) int { return c.Proxy.MaxInflightPerHost }},
	} {
		t.Run(tc.key, func(t *testing.T) {
			for _, source := range []string{"yaml", "environment"} {
				t.Run(source, func(t *testing.T) {
					for _, value := range []string{"", "0", "4", "-1"} {
						t.Run("value="+value, func(t *testing.T) {
							configPath := filepath.Join(t.TempDir(), "config.yaml")
							body := ""
							if source == "yaml" && value != "" {
								body = "proxy:\n  " + tc.key + ": " + value + "\n"
							}
							require.NoError(t, os.WriteFile(configPath, []byte(body), 0600))
							if source == "environment" {
								t.Setenv(tc.env, value)
							}
							cfg, err := Load(configPath)
							if value == "-1" {
								require.ErrorContains(t, err, "proxy."+tc.key+" must not be negative")
								return
							}
							require.NoError(t, err)
							want := tc.fallback
							if value != "" && value != "0" {
								want, err = strconv.Atoi(value)
								require.NoError(t, err)
							}
							require.Equal(t, want, tc.get(cfg))
						})
					}
				})
			}
		})
	}
}
