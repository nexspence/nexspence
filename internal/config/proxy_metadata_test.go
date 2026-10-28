package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoad_ProxyMetadataLimit_RejectsInvalid(t *testing.T) {
	for _, limit := range []string{"0", "-1", "9223372036854775807"} {
		t.Run(limit, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte("database:\n  dsn: postgres://localhost/db\nauth:\n  jwt_secret: a-unique-production-secret-at-least-32b\nproxy:\n  max_rewritten_metadata_bytes: "+limit+"\n"), 0600))
			_, err := Load(path)
			require.Error(t, err)
			require.Contains(t, err.Error(), "proxy.max_rewritten_metadata_bytes")
		})
	}
}

func TestLoad_ProxyMetadataLimit(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, env string
		want            int64
	}{
		{name: "default", want: 33554432},
		{name: "yaml", yaml: "proxy:\n  max_rewritten_metadata_bytes: 67108864\n", want: 67108864},
		{name: "environment without file", env: "134217728", want: 134217728},
		{name: "environment overrides yaml", yaml: "proxy:\n  max_rewritten_metadata_bytes: 67108864\n", env: "134217728", want: 134217728},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NEXSPENCE_DATABASE_DSN", "postgres://localhost/db")
			t.Setenv("NEXSPENCE_AUTH_JWT_SECRET", "a-unique-production-secret-at-least-32b")
			path := filepath.Join(t.TempDir(), "config.yaml")
			if tc.yaml != "" {
				require.NoError(t, os.WriteFile(path, []byte(tc.yaml), 0600))
			}
			if tc.env != "" {
				t.Setenv("NEXSPENCE_PROXY_MAX_REWRITTEN_METADATA_BYTES", tc.env)
			}
			cfg, err := Load(path)
			require.NoError(t, err)
			require.Equal(t, tc.want, cfg.Proxy.MaxRewrittenMetadataBytes)
			require.Empty(t, cfg.UnknownKeys)
		})
	}
}
