package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nexspence-oss/nexspence/internal/api"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/db"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
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
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}

			log := logger.New(cfg.Log.Level, cfg.Log.Format)
			log.Info("starting nexspence", "version", Version, "addr", cfg.HTTP.Addr)

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

			if err := bootstrapAdmin(cmd.Context(), pool, cfg, log); err != nil {
				log.Error("bootstrap admin failed", "err", err)
				// Non-fatal — server still starts
			}

			router := api.NewRouter(cfg, pool, log)

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
		RunE: func(cmd *cobra.Command, args []string) error {
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
	return nil, nil
}

// Version is injected at build time via -ldflags
var Version = "dev"
