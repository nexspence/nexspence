package config

import "testing"

func TestLoad_ProxyConnections(t *testing.T) {
	t.Setenv("NEXSPENCE_DATABASE_DSN", "postgres://localhost/db")
	t.Setenv("NEXSPENCE_AUTH_JWT_SECRET", "a-unique-production-secret-at-least-32b")
	for _, value := range []string{"0", "4", "-1"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("NEXSPENCE_PROXY_MAX_CONNS_PER_HOST", value)
			cfg, err := Load("")
			if value == "-1" {
				if err == nil {
					t.Fatal("negative limit accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if value == "0" && cfg.Proxy.MaxConnsPerHost != 0 {
				t.Fatalf("default: %d", cfg.Proxy.MaxConnsPerHost)
			}
		})
	}
}
