package domain

import "time"

type MigrationJobStatus string

const (
	MigrationPending MigrationJobStatus = "pending"
	MigrationRunning MigrationJobStatus = "running"
	MigrationPaused  MigrationJobStatus = "paused"
	MigrationDone    MigrationJobStatus = "done"
	MigrationError   MigrationJobStatus = "error"
)

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
