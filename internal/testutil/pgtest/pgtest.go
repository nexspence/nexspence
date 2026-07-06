//go:build integration

// Package pgtest boots an ephemeral PostgreSQL container for integration tests,
// applies the project's real migrations, and hands back a ready pgx pool.
package pgtest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"

	"github.com/nexspence-oss/nexspence/internal/audit"
	"github.com/nexspence-oss/nexspence/internal/config"
	appdb "github.com/nexspence-oss/nexspence/internal/db"
	"github.com/nexspence-oss/nexspence/internal/logger"
)

var (
	once     sync.Once
	pool     *pgxpool.Pool
	purge    func()
	startErr error
)

// Pool returns a shared, migrated pool. The container is started once per test
// binary; the first caller pays the startup cost. Fails the test on error.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	once.Do(start)
	if startErr != nil {
		t.Fatalf("pgtest: start postgres: %v", startErr)
	}
	return pool
}

// Cleanup tears down the container. Call from TestMain after m.Run().
func Cleanup() {
	if purge != nil {
		purge()
	}
}

func start() {
	dpool, err := dockertest.NewPool("")
	if err != nil {
		startErr = fmt.Errorf("connect to docker: %w", err)
		return
	}
	if err := dpool.Client.Ping(); err != nil {
		startErr = fmt.Errorf("docker ping (is Docker running?): %w", err)
		return
	}

	resource, err := dpool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "16-alpine",
		Env: []string{
			"POSTGRES_USER=test",
			"POSTGRES_PASSWORD=test",
			"POSTGRES_DB=nexspence_test",
			"listen_addresses='*'",
		},
	}, func(c *docker.HostConfig) {
		c.AutoRemove = true
		c.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	if err != nil {
		startErr = fmt.Errorf("start postgres container: %w", err)
		return
	}
	_ = resource.Expire(600) // self-destruct after 10m as a safety net

	hostPort := resource.GetHostPort("5432/tcp")
	dsn := fmt.Sprintf("postgres://test:test@%s/nexspence_test?sslmode=disable", hostPort)

	dpool.MaxWait = 60 * time.Second
	if err := dpool.Retry(func() error {
		p, perr := appdb.Connect(context.Background(), dsn)
		if perr != nil {
			return perr
		}
		p.Close()
		return nil
	}); err != nil {
		_ = dpool.Purge(resource)
		startErr = fmt.Errorf("wait for postgres: %w", err)
		return
	}

	if err := appdb.Migrate(dsn, "up"); err != nil {
		_ = dpool.Purge(resource)
		startErr = fmt.Errorf("migrate: %w", err)
		return
	}

	p, err := appdb.Connect(context.Background(), dsn)
	if err != nil {
		_ = dpool.Purge(resource)
		startErr = fmt.Errorf("connect pool: %w", err)
		return
	}

	// Migrations only ship the partitions that existed for audit_events at
	// authoring time. Production guarantees the current partition via
	// Rotator.RunOnce() at server startup (cmd/server/main.go) — mirror that
	// here so tests don't rot once the calendar rolls past the last
	// hand-written partition.
	auditCfg := config.AuditConfig{LookaheadMonths: 2}
	rotator := audit.NewRotator(audit.NewPgPartitionStore(p), auditCfg, logger.New("error", "json"))
	rotator.RunOnce(context.Background())

	pool = p
	purge = func() {
		pool.Close()
		_ = dpool.Purge(resource)
	}
}

// Truncate empties the named tables (RESTART IDENTITY CASCADE) so each test
// starts from a known-empty state. Call at the top of a test or via t.Cleanup.
func Truncate(t *testing.T, p *pgxpool.Pool, tables ...string) {
	t.Helper()
	if len(tables) == 0 {
		return
	}
	stmt := fmt.Sprintf("TRUNCATE %s RESTART IDENTITY CASCADE", strings.Join(tables, ", "))
	if _, err := p.Exec(context.Background(), stmt); err != nil {
		t.Fatalf("pgtest: truncate %v: %v", tables, err)
	}
}
