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

// An install that sizes the pool in the DSN, below the default min_conns,
// keeps starting: the defaulted minimum is lowered to the maximum.
func TestApplyPoolSettings_DefaultMinIsClampedToADSNMax(t *testing.T) {
	dsn := testDSN + "&pool_max_conns=4"
	cfg := parsed(t, dsn)
	require.NoError(t, applyPoolSettings(cfg, dsn, PoolSettings{MaxConns: 20, MinConns: 5}))
	assert.Equal(t, int32(4), cfg.MaxConns)
	assert.Equal(t, int32(4), cfg.MinConns)
}

// The same holds when only the maximum is the operator's: the minimum was
// never chosen, so it gives way.
func TestApplyPoolSettings_DefaultMinIsClampedToAnExplicitMax(t *testing.T) {
	cfg := parsed(t, testDSN)
	require.NoError(t, applyPoolSettings(cfg, testDSN, PoolSettings{MaxConns: 3, MinConns: 5, MaxConnsSet: true}))
	assert.Equal(t, int32(3), cfg.MaxConns)
	assert.Equal(t, int32(3), cfg.MinConns)
}

// Both set by the operator, and contradictory: refuse rather than guess.
func TestApplyPoolSettings_ExplicitContradictionIsRefused(t *testing.T) {
	cfg := parsed(t, testDSN)
	err := applyPoolSettings(cfg, testDSN, PoolSettings{MaxConns: 4, MinConns: 10, MaxConnsSet: true, MinConnsSet: true})
	assert.ErrorContains(t, err, "database.min_conns (10) exceeds database.max_conns (4)")
}

func TestApplyPoolSettings_RejectsNegativeValues(t *testing.T) {
	cfg := parsed(t, testDSN)
	assert.ErrorContains(t, applyPoolSettings(cfg, testDSN, PoolSettings{MaxConns: -1}), "negative")
}

// The DSN is parsed, in both forms pgx accepts, not searched as text.
func TestDSNHasParam(t *testing.T) {
	cases := []struct {
		dsn  string
		want bool
	}{
		{"postgres://u:p@h:5432/db?sslmode=disable&pool_max_conns=50", true},
		{"postgresql://u:p@h/db?pool_max_conns=50", true},
		{"postgres://u:pool_max_conns=9@h:5432/db?sslmode=disable", false}, // in the password
		{"postgres://u:p@h:5432/db?sslmode=disable", false},
		{"host=h user=u password=p dbname=db pool_max_conns=7", true},
		{"host=h user=u password=pool_max_conns=7 dbname=db", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, dsnHasParam(c.dsn, "pool_max_conns"), c.dsn)
	}
}
