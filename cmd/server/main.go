package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/nexspence-oss/nexspence/internal/api"
	"github.com/nexspence-oss/nexspence/internal/audit"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/db"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/metrics"
	"github.com/nexspence-oss/nexspence/internal/repository/postgres"
)

func main() {
	root := &cobra.Command{
		Use:   "nexspence",
		Short: "Nexspence — free universal artifact repository manager",
	}

	root.AddCommand(cmdServe(), cmdMigrate())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func cmdServe() *cobra.Command {
	var cfgPath string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Nexspence HTTP server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}

			log := logger.New(cfg.Log.Level, cfg.Log.Format)
			log.Info("starting nexspence", "version", Version, "addr", cfg.HTTP.Addr)
			if cfg.Auth.AnonymousEnabled {
				log.Warn("auth.anonymous_enabled is true — unauthenticated artifact access is allowed; set false to require authentication")
			}
			if config.IsDevDefaultJWTSecret(cfg.Auth.JWTSecret) {
				log.Warn("auth.jwt_secret is the shipped development default — set a unique secret (NEXSPENCE_AUTH_JWT_SECRET) before production use")
			}

			// Auto-migrate on every startup so the schema is always up-to-date.
			log.Info("running database migrations...")
			if err := db.Migrate(cfg.Database.DSN, "up"); err != nil {
				return fmt.Errorf("migrations failed: %w", err)
			}
			log.Info("migrations OK")

			pool, err := db.Connect(cmd.Context(), cfg.Database.DSN)
			if err != nil {
				return err
			}
			defer pool.Close()
			log.Info("database connected", "host", dbHost(cfg.Database.DSN))

			// Storage
			if cfg.Storage.DefaultType == "s3" {
				log.Info("storage", "type", "s3", "bucket", cfg.Storage.S3.Bucket, "endpoint", cfg.Storage.S3.Endpoint)
			} else {
				log.Info("storage", "type", "local", "path", cfg.Storage.Local.BasePath)
			}

			// LDAP
			if cfg.LDAP.Enabled {
				log.Info("ldap enabled", "host", cfg.LDAP.Host, "port", cfg.LDAP.Port, "use_tls", cfg.LDAP.UseTLS || cfg.LDAP.Port == 636, "insecure_skip_verify", cfg.LDAP.InsecureSkipVerify, "admin_group", cfg.LDAP.AdminGroup)
				if cfg.LDAP.InsecureSkipVerify {
					log.Warn("LDAP insecure_skip_verify is enabled — TLS certificate validation is OFF; use only with self-signed certs in development")
				}
				if ldapSvc := auth.NewLDAPService(cfg.LDAP); ldapSvc != nil {
					if err := ldapSvc.TestConnection(cmd.Context()); err != nil {
						log.Warn("ldap connection test FAILED", "err", err)
					} else {
						log.Info("ldap connection OK")
					}
				}
			} else {
				log.Info("ldap disabled")
			}

			// OIDC — startup discovery log. NewRouter will rebuild the service
			// at router construction; this is diagnostic-only.
			if cfg.OIDC.Enabled {
				log.Info("oidc enabled",
					"display", cfg.OIDC.DisplayName,
					"issuer", cfg.OIDC.Issuer,
					"provisioning", cfg.OIDC.Provisioning,
				)
				if _, err := auth.NewOIDCService(cmd.Context(), cfg.OIDC); err != nil {
					log.Warn("oidc discovery test FAILED", "err", err)
				} else {
					log.Info("oidc discovery OK")
				}
			} else {
				log.Info("oidc disabled")
			}

			// Audit retention — pre-create future partitions, drop expired,
			// observe row count. Synchronous first tick guarantees the
			// current month's partition exists before we accept traffic.
			rotator := audit.NewRotator(audit.NewPgPartitionStore(pool), cfg.Audit, log)
			rotator.RunOnce(cmd.Context())
			go rotator.Run(cmd.Context())
			log.Info("audit rotator started",
				"retention_days", cfg.Audit.RetentionDays,
				"soft_cap", cfg.Audit.SoftCap,
				"rotation_interval", cfg.Audit.RotationInterval.String(),
				"lookahead_months", cfg.Audit.LookaheadMonths,
			)

			if err := bootstrapAdmin(cmd.Context(), pool, cfg, log); err != nil {
				log.Error("bootstrap admin failed", "err", err)
				// Non-fatal — server still starts
			}

			if err := syncBlobStorePaths(cmd.Context(), pool, cfg, log); err != nil {
				log.Warn("blob store path sync failed", "err", err)
				// Non-fatal — server still starts
			}

			// Seed Prometheus gauges from DB on startup.
			{
				var artifacts, bytes, downloads int64
				_ = pool.QueryRow(cmd.Context(),
					`SELECT COUNT(*), COALESCE(SUM(size_bytes),0), COALESCE(SUM(download_count),0) FROM assets`,
				).Scan(&artifacts, &bytes, &downloads)
				metrics.UpdateGauges(artifacts, bytes, downloads)
				log.Info("metrics gauges seeded", "artifacts", artifacts, "bytes", bytes)
			}

			// Start background metrics sampler — stops on context cancellation.
			samplerCtx, cancelSampler := context.WithCancel(cmd.Context())
			defer cancelSampler()
			metrics.StartSampler(samplerCtx, pool)

			router := api.NewRouter(cfg, pool, log, Version)

			srv := &http.Server{
				Addr:         cfg.HTTP.Addr,
				Handler:      router,
				ReadTimeout:  time.Duration(cfg.HTTP.ReadTimeoutSec) * time.Second,
				WriteTimeout: time.Duration(cfg.HTTP.WriteTimeoutSec) * time.Second,
				IdleTimeout:  120 * time.Second,
			}

			// Start server in goroutine
			go func() {
				if cfg.HTTP.TLS.Enabled {
					if err := srv.ListenAndServeTLS(cfg.HTTP.TLS.CertFile, cfg.HTTP.TLS.KeyFile); !errors.Is(err, http.ErrServerClosed) {
						log.Error("https server error", "err", err)
						os.Exit(1)
					}
				} else {
					if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
						log.Error("http server error", "err", err)
						os.Exit(1)
					}
				}
			}()

			// Graceful shutdown
			quit := make(chan os.Signal, 1)
			signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
			<-quit

			log.Info("shutting down...")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			return srv.Shutdown(ctx)
		},
	}

	cmd.Flags().StringVarP(&cfgPath, "config", "c", "config.yaml", "Path to config file")
	return cmd
}

func cmdMigrate() *cobra.Command {
	var cfgPath string
	var direction string

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run database migrations",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			return db.Migrate(cfg.Database.DSN, direction)
		},
	}

	cmd.Flags().StringVarP(&cfgPath, "config", "c", "config.yaml", "Path to config file")
	cmd.Flags().StringVarP(&direction, "direction", "d", "up", "Migration direction: up | down | status")
	return cmd
}

// syncBlobStorePaths ensures every local blob store in DB has an absolute "path"
// derived from cfg.Storage.Local.BasePath. The migration seed uses relative paths
// (e.g. "./data/blobs/default") which resolve to the wrong location when the app
// runs in Docker (WORKDIR=/app, volume mounted at /data/blobs). This sync runs on
// every startup so the DB always reflects the path the app was configured with.
func syncBlobStorePaths(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, log logger.Logger) error {
	if cfg.Storage.DefaultType != "" && cfg.Storage.DefaultType != "local" {
		return nil
	}
	basePath := cfg.Storage.Local.BasePath
	if basePath == "" {
		basePath = "./data/blobs"
	}

	blobRepo := postgres.NewBlobStoreRepo(pool)
	stores, err := blobRepo.List(ctx)
	if err != nil {
		return err
	}
	for i := range stores {
		bs := &stores[i]
		if bs.Type != "local" {
			continue
		}
		expectedPath := filepath.Join(basePath, bs.Name)
		if bs.Config == nil {
			bs.Config = map[string]any{}
		}
		currentPath, _ := bs.Config["path"].(string)
		if currentPath == expectedPath {
			continue
		}
		bs.Config["path"] = expectedPath
		if updateErr := blobRepo.Update(ctx, bs); updateErr != nil {
			log.Warn("blob store path sync failed", "name", bs.Name, "err", updateErr)
		} else {
			log.Info("blob store path synced", "name", bs.Name, "old", currentPath, "new", expectedPath)
		}
	}
	return nil
}

// bootstrapAdmin ensures the admin user exists with the configured password.
// If the user already exists the password is updated to match config — so you
// can always reset the admin by changing config and restarting.
func bootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, log logger.Logger) error {
	b := cfg.Bootstrap
	if b.AdminUsername == "" || b.AdminPassword == "" {
		return nil
	}

	authSvc := auth.NewService(cfg.Auth.JWTSecret, cfg.Auth.JWTExpiryHours, cfg.Auth.BcryptCost)
	userRepo := postgres.NewUserRepo(pool)
	roleRepo := postgres.NewRoleRepo(pool)

	hash, err := authSvc.HashPassword(b.AdminPassword)
	if err != nil {
		return err
	}

	existing, err := userRepo.Get(ctx, b.AdminUsername)
	if err != nil {
		return err
	}

	if existing == nil {
		// Create fresh admin user
		u := &domain.User{
			Username:     b.AdminUsername,
			Email:        b.AdminEmail,
			FirstName:    b.AdminFirstName,
			PasswordHash: hash,
			Status:       domain.UserStatusActive,
			Source:       domain.UserSourceLocal,
		}
		if err := userRepo.Create(ctx, u); err != nil {
			return err
		}
		// Find nx-admin role and assign it
		adminRole, err := findRoleByName(ctx, roleRepo, "nx-admin")
		if err != nil || adminRole == nil {
			log.Warn("nx-admin role not found — skip role assignment")
		} else {
			_ = roleRepo.SetUserRoles(ctx, u.ID, []string{adminRole.ID})
		}
		log.Info("bootstrap: admin user created", "username", b.AdminUsername)
	} else {
		// Update password so config is always authoritative
		if err := userRepo.UpdatePassword(ctx, b.AdminUsername, hash); err != nil {
			return err
		}
		log.Info("bootstrap: admin password synced", "username", b.AdminUsername)
	}
	return nil
}

func findRoleByName(ctx context.Context, repo interface {
	List(context.Context) ([]domain.Role, error)
}, name string) (*domain.Role, error) {
	roles, err := repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range roles {
		if r.Name == name {
			return &r, nil
		}
	}
	return nil, nil //nolint:nilnil // bootstrap-only lookup; nil result signals "no such role" to the caller (no error condition)
}

// Version is injected at build time via -ldflags
var Version = "dev"

// dbHost extracts the host from a postgres DSN URL for safe log display.
func dbHost(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return dsn
	}
	return u.Host
}
