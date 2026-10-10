package db

import (
	"context"
	"embed"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// PoolSettings sizes the connection pool: database.max_conns, min_conns and
// max_idle_sec. A zero field leaves pgx's default, and a pool parameter written
// into the DSN itself (pool_max_conns, pool_min_conns,
// pool_max_conn_idle_time) wins over the matching field, as pgx documents it.
type PoolSettings struct {
	MaxConns int
	MinConns int
	MaxIdle  time.Duration

	// MaxConnsSet and MinConnsSet say the operator chose the value rather
	// than inheriting the default (config.DatabaseConfig).
	MaxConnsSet bool
	MinConnsSet bool
}

// Connect opens a pgx connection pool sized by the DSN alone (pgx's default is
// max(4, CPUs) connections). An optional QueryTracer (at most one) is attached
// to every connection — the hook OpenTelemetry's otelpgx uses to emit a span
// per query (#302). Passing none keeps the previous behavior.
func Connect(ctx context.Context, dsn string, tracers ...pgx.QueryTracer) (*pgxpool.Pool, error) {
	return ConnectPool(ctx, dsn, PoolSettings{}, tracers...)
}

// ConnectPool is Connect with the configured pool size applied (#639).
func ConnectPool(ctx context.Context, dsn string, pool PoolSettings, tracers ...pgx.QueryTracer) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.ParseConfig: %w", err)
	}
	if err := applyPoolSettings(cfg, dsn, pool); err != nil {
		return nil, err
	}
	if len(tracers) > 1 {
		return nil, fmt.Errorf("db.Connect: at most one QueryTracer, got %d", len(tracers))
	}
	if len(tracers) == 1 && tracers[0] != nil {
		cfg.ConnConfig.Tracer = tracers[0]
	}
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.NewWithConfig: %w", err)
	}
	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, fmt.Errorf("db ping: %w", err)
	}
	return p, nil
}

// applyPoolSettings writes the configured pool size into cfg, except where the
// DSN names the same pool parameter itself.
//
// A minimum above the maximum is clamped to it, unless the operator set both
// in the configuration that way: then it is a mistake worth refusing. A
// defaulted min_conns meeting a small pool_max_conns in the DSN, or a small
// max_conns of the operator's, is not, and must not stop a working install
// from starting.
func applyPoolSettings(cfg *pgxpool.Config, dsn string, pool PoolSettings) error {
	if pool.MaxConns < 0 || pool.MinConns < 0 || pool.MaxIdle < 0 {
		return fmt.Errorf("database pool settings must not be negative")
	}
	maxInDSN := dsnHasParam(dsn, "pool_max_conns")
	minInDSN := dsnHasParam(dsn, "pool_min_conns")
	if pool.MaxConns > 0 && !maxInDSN {
		cfg.MaxConns = int32(min(pool.MaxConns, math.MaxInt32)) //nolint:gosec // bounded above
	}
	if pool.MinConns > 0 && !minInDSN {
		cfg.MinConns = int32(min(pool.MinConns, math.MaxInt32)) //nolint:gosec // bounded above
	}
	if pool.MaxIdle > 0 && !dsnHasParam(dsn, "pool_max_conn_idle_time") {
		cfg.MaxConnIdleTime = pool.MaxIdle
	}
	if cfg.MinConns > cfg.MaxConns {
		if pool.MinConnsSet && pool.MaxConnsSet && !maxInDSN && !minInDSN {
			return fmt.Errorf("database.min_conns (%d) exceeds database.max_conns (%d)", cfg.MinConns, cfg.MaxConns)
		}
		cfg.MinConns = cfg.MaxConns
	}
	return nil
}

// dsnHasParam reports whether the DSN sets key, in either form pgx accepts: a
// URL query parameter, or a key=value pair of a keyword/value DSN. It parses
// rather than searching the text, so a password containing "pool_max_conns="
// is not mistaken for the parameter.
func dsnHasParam(dsn, key string) bool {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return false
		}
		_, ok := u.Query()[key]
		return ok
	}
	for _, field := range strings.Fields(dsn) {
		if k, _, ok := strings.Cut(field, "="); ok && k == key {
			return true
		}
	}
	return false
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
