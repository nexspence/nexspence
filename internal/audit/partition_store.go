// Package audit owns the audit-log partition rotator and any future
// audit-specific background jobs.
package audit

import (
	"context"
	"time"
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
