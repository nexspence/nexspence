// Package audit owns the audit-log partition rotator and any future
// audit-specific background jobs.
package audit

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Partition describes one partition of audit_events.
//
// Both `From` (inclusive) and `To` (exclusive) are at the start of the day at 00:00 UTC.
type Partition struct {
	Name string    // e.g. "audit_events_2026_05"
	From time.Time // inclusive
	To   time.Time // exclusive
}

// PartitionStore is the small surface the Rotator needs from the database.
// The postgres implementation lives in this package; tests use a fake.
type PartitionStore interface {
	// ListPartitions returns the existing partitions of audit_events.
	ListPartitions(ctx context.Context) ([]Partition, error)

	// CreatePartition creates a partition covering [from, to). It MUST be
	// idempotent — calling it for an existing range is a no-op.
	CreatePartition(ctx context.Context, name string, from, to time.Time) error

	// DropPartition DETACHes and DROPs the named partition.
	DropPartition(ctx context.Context, name string) error

	// CountRows returns the total number of rows in audit_events.
	CountRows(ctx context.Context) (int64, error)
}

// PartitionName returns the canonical name for the partition that covers
// the month containing `t`: e.g. 2026-05-12 → "audit_events_2026_05".
func PartitionName(t time.Time) string {
	t = t.UTC()
	return t.Format("audit_events_2006_01")
}

// MonthBounds returns the [from, to) range that covers the month containing `t`.
func MonthBounds(t time.Time) (from, to time.Time) {
	t = t.UTC()
	from = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	to = from.AddDate(0, 1, 0)
	return
}

// pgPartitionStore is the PostgreSQL implementation of PartitionStore.
type pgPartitionStore struct{ pool *pgxpool.Pool }

// NewPgPartitionStore returns a PartitionStore backed by the given pool.
func NewPgPartitionStore(pool *pgxpool.Pool) PartitionStore {
	return &pgPartitionStore{pool: pool}
}

// boundRE matches `FOR VALUES FROM ('YYYY-MM-DD') TO ('YYYY-MM-DD')`,
// which is exactly the form PostgreSQL returns for our partitions.
var boundRE = regexp.MustCompile(
	`FOR VALUES FROM \('(\d{4}-\d{2}-\d{2})'\) TO \('(\d{4}-\d{2}-\d{2})'\)`)

func (s *pgPartitionStore) ListPartitions(ctx context.Context) ([]Partition, error) {
	rows, err := s.pool.Query(ctx, `
        SELECT inhrelid::regclass::text AS name,
               pg_get_expr(c.relpartbound, c.oid) AS bound
          FROM pg_inherits i
          JOIN pg_class c ON c.oid = i.inhrelid
         WHERE i.inhparent = 'audit_events'::regclass`)
	if err != nil {
		return nil, fmt.Errorf("list audit partitions: %w", err)
	}
	defer rows.Close()

	var out []Partition
	for rows.Next() {
		var name, bound string
		if err := rows.Scan(&name, &bound); err != nil {
			return nil, err
		}
		m := boundRE.FindStringSubmatch(bound)
		if len(m) != 3 {
			// Non-conforming partition (e.g. DEFAULT); skip safely.
			continue
		}
		from, err := time.Parse("2006-01-02", m[1])
		if err != nil {
			continue
		}
		to, err := time.Parse("2006-01-02", m[2])
		if err != nil {
			continue
		}
		out = append(out, Partition{Name: name, From: from, To: to})
	}
	return out, rows.Err()
}

func (s *pgPartitionStore) CreatePartition(ctx context.Context, name string, from, to time.Time) error {
	// Identifiers can't be parameterised — the names are derived from time
	// values produced by this package, so they are safe to format.
	sql := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s
           PARTITION OF audit_events
           FOR VALUES FROM ('%s') TO ('%s')`,
		name,
		from.UTC().Format("2006-01-02"),
		to.UTC().Format("2006-01-02"),
	)
	if _, err := s.pool.Exec(ctx, sql); err != nil {
		return fmt.Errorf("create partition %s: %w", name, err)
	}
	return nil
}

func (s *pgPartitionStore) DropPartition(ctx context.Context, name string) error {
	if _, err := s.pool.Exec(ctx,
		fmt.Sprintf(`ALTER TABLE audit_events DETACH PARTITION %s`, name)); err != nil {
		return fmt.Errorf("detach %s: %w", name, err)
	}
	if _, err := s.pool.Exec(ctx,
		fmt.Sprintf(`DROP TABLE %s`, name)); err != nil {
		return fmt.Errorf("drop %s: %w", name, err)
	}
	return nil
}

func (s *pgPartitionStore) CountRows(ctx context.Context) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_events`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count audit_events: %w", err)
	}
	return n, nil
}
