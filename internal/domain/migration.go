package domain

import "time"

// MigrationJobStatus is the lifecycle state of a Nexus-to-Nexspence migration job.
type MigrationJobStatus string

// Migration job lifecycle states.
const (
	MigrationPending MigrationJobStatus = "pending"
	MigrationRunning MigrationJobStatus = "running"
	MigrationPaused  MigrationJobStatus = "paused"
	MigrationDone    MigrationJobStatus = "done"
	MigrationError   MigrationJobStatus = "error"
)

// MigrationJob tracks an import from a live Nexus instance, including selected
// scopes (repos/users/blobs/policies) and per-asset transfer progress.
type MigrationJob struct {
	ID              string
	SourceURL       string
	SourceUser      string
	Status          MigrationJobStatus
	MigrateRepos    bool
	MigrateUsers    bool
	MigrateBlobs    bool
	MigratePolicies bool
	TotalRepos      int
	DoneRepos       int
	TotalAssets     int64
	DoneAssets      int64
	TotalBytes      int64
	DoneBytes       int64
	ErrorCount      int
	LastError       *string
	StartedAt       *time.Time
	FinishedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
