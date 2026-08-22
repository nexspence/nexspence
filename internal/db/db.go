package db

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Connect opens a pgx connection pool. An optional QueryTracer (at most one)
// is attached to every connection — the hook OpenTelemetry's otelpgx uses to
// emit a span per query (#302). Passing none keeps the previous behavior.
func Connect(ctx context.Context, dsn string, tracers ...pgx.QueryTracer) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.ParseConfig: %w", err)
	}
	if len(tracers) > 1 {
		return nil, fmt.Errorf("db.Connect: at most one QueryTracer, got %d", len(tracers))
	}
	if len(tracers) == 1 && tracers[0] != nil {
		cfg.ConnConfig.Tracer = tracers[0]
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.NewWithConfig: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db ping: %w", err)
	}
	return pool, nil
}

// Migrate runs goose migrations in the given direction ("up", "down", "status").
func Migrate(dsn, direction string) error {
	// stdlib.OpenDB expects a pgx.ConnConfig, not pgxpool.Config
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse DSN: %w", err)
	}
	db := stdlib.OpenDB(*poolCfg.ConnConfig)
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}

	switch direction {
	case "up":
		return goose.Up(db, "migrations")
	case "down":
		return goose.Down(db, "migrations")
	case "status":
		return goose.Status(db, "migrations")
	default:
		return fmt.Errorf("unknown migration direction: %s (use up|down|status)", direction)
	}
}
