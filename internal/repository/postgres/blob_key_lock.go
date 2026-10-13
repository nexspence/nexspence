package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type lockScopeKey struct{}

type lockScope struct {
	pool *pgxpool.Pool
	conn *pgxpool.Conn
}

// lockQuerier preserves autocommit semantics while allowing callbacks to use
// their advisory-lock session instead of acquiring additional pool connections.
type lockQuerier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Begin(context.Context) (pgx.Tx, error)
}

func lockDB(ctx context.Context, pool *pgxpool.Pool) lockQuerier {
	if scope, ok := ctx.Value(lockScopeKey{}).(*lockScope); ok && scope.pool == pool {
		return scope.conn
	}
	return pool
}

// WithBlobKeyLock serializes read-then-act operations across server instances.
// Nested locks and callback queries share one session, avoiding pool starvation
// even with both store and repository quota locks. Session locks preserve the
// callback's existing autocommit behavior (blob I/O cannot be rolled back).
func (r *assetRepo) WithBlobKeyLock(ctx context.Context, blobKey string, fn func(context.Context) error) (err error) {
	if scope, ok := ctx.Value(lockScopeKey{}).(*lockScope); ok && scope.pool == r.db {
		// The outer holder can always execute its queries on this connection, even
		// when every other pool connection is waiting on another nested lock.
		if _, err := scope.conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, blobKey); err != nil {
			closeLockConnection(scope.conn)
			return fmt.Errorf("acquire nested blob lock: %w", err)
		}
		defer func() { err = releaseBlobLock(scope.conn, blobKey, err) }()
		return fn(ctx)
	}

	for {
		conn, acquireErr := r.db.Acquire(ctx)
		if acquireErr != nil {
			return fmt.Errorf("acquire blob lock connection: %w", acquireErr)
		}
		var locked bool
		queryErr := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, blobKey).Scan(&locked)
		if queryErr != nil {
			// A canceled query may have acquired the session lock before its result
			// arrived. Never put an uncertain session back into the pool.
			closeLockConnection(conn)
			conn.Release()
			return fmt.Errorf("try blob lock: %w", queryErr)
		}
		if locked {
			defer conn.Release()
			defer func() { err = releaseBlobLock(conn, blobKey, err) }()
			return fn(context.WithValue(ctx, lockScopeKey{}, &lockScope{pool: r.db, conn: conn}))
		}
		// Waiters must not pin connections needed by the lock holder. Polling is
		// database coordinated; no process-local mutex or singleflight is needed.
		conn.Release()
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func releaseBlobLock(conn *pgxpool.Conn, key string, callbackErr error) error {
	// Request cancellation must not leave a session lock in the pool.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var unlocked bool
	err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key).Scan(&unlocked)
	if err != nil || !unlocked {
		closeLockConnection(conn)
		if callbackErr == nil {
			if err != nil {
				return fmt.Errorf("release blob lock: %w", err)
			}
			return fmt.Errorf("blob lock was not held: %s", key)
		}
	}
	return callbackErr
}

func closeLockConnection(conn *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = conn.Conn().Close(ctx)
}
