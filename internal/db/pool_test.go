package db

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDSN = "postgres://u:p@localhost:5432/db?sslmode=disable"

func parsed(t *testing.T, dsn string) *pgxpool.Config {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	require.NoError(t, err)
	return cfg
}

// The configured pool size reaches the pool (#639): before, it was read and
// dropped, and every instance ran at pgx's max(4, CPUs).
func TestApplyPoolSettings_ConfigReachesThePool(t *testing.T) {
	cfg := parsed(t, testDSN)
	require.NoError(t, applyPoolSettings(cfg, testDSN, PoolSettings{MaxConns: 100, MinConns: 5, MaxIdle: 300 * time.Second}))
	assert.Equal(t, int32(100), cfg.MaxConns)
	assert.Equal(t, int32(5), cfg.MinConns)
	assert.Equal(t, 300*time.Second, cfg.MaxConnIdleTime)
}

// Zero fields keep pgx's defaults.
func TestApplyPoolSettings_ZeroKeepsDefaults(t *testing.T) {
	want := parsed(t, testDSN)
	cfg := parsed(t, testDSN)
	require.NoError(t, applyPoolSettings(cfg, testDSN, PoolSettings{}))
	assert.Equal(t, want.MaxConns, cfg.MaxConns)
	assert.Equal(t, want.MinConns, cfg.MinConns)
	assert.Equal(t, want.MaxConnIdleTime, cfg.MaxConnIdleTime)
}

// A pool parameter written into the DSN wins over the configured one.
func TestApplyPoolSettings_DSNParametersWin(t *testing.T) {
	dsn := testDSN + "&pool_max_conns=50&pool_min_conns=2&pool_max_conn_idle_time=1m"
	cfg := parsed(t, dsn)
	require.NoError(t, applyPoolSettings(cfg, dsn, PoolSettings{MaxConns: 100, MinConns: 5, MaxIdle: 300 * time.Second}))
	assert.Equal(t, int32(50), cfg.MaxConns)
	assert.Equal(t, int32(2), cfg.MinConns)
	assert.Equal(t, time.Minute, cfg.MaxConnIdleTime)
}

func TestApplyPoolSettings_RejectsInconsistentSizes(t *testing.T) {
	cfg := parsed(t, testDSN)
	assert.ErrorContains(t, applyPoolSettings(cfg, testDSN, PoolSettings{MaxConns: 4, MinConns: 10}), "min_conns")
	cfg = parsed(t, testDSN)
	assert.ErrorContains(t, applyPoolSettings(cfg, testDSN, PoolSettings{MaxConns: -1}), "negative")
}
