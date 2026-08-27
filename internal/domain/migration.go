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

// MigrationJob tracks an import from a live Nexus instance, including the
// selected scopes and per-asset transfer progress.
//
// Each Migrate* flag gates one stage of the run independently, so a job can
// copy everything or just a slice of it. MigratePolicies is the older, coarser
// flag kept for API compatibility: it is the default the three security scopes
// (privileges, roles, routing rules) fall back to when a request does not name
// them, and drives no stage of its own.
type MigrationJob struct {
	ID         string
	SourceURL  string
	SourceUser string
	// SourcePassword is the Nexus password sealed with the instance encryption
	// key. It is persisted so a job can resume after a process restart, and is
	// never included in an API response.
	SourcePassword string
	Status         MigrationJobStatus
	MigrateRepos   bool
	MigrateUsers   bool
	// UserRealms names the source realms user migration pulls accounts from,
	// via Nexus's own ?source= filter ("default" is the local realm). Empty
	// means local-only — the only realm guaranteed to make sense on a fresh
	// target: an externally-authenticated account migrated without its provider
	// configured is a permanently-unusable login (#342).
	UserRealms          []string
	MigrateBlobs        bool
	MigratePolicies     bool
	MigratePrivileges   bool
	MigrateRoles        bool
	MigrateRoutingRules bool
	TotalRepos          int
	DoneRepos           int
	TotalAssets         int64
	DoneAssets          int64
	TotalBytes          int64
	DoneBytes           int64
	ErrorCount          int
	LastError           *string
	StartedAt           *time.Time
	FinishedAt          *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// IsActive reports whether the job is one the runner owns — created but not
// yet finished, and not deliberately parked by an operator.
func (j MigrationJob) IsActive() bool {
	return j.Status == MigrationPending || j.Status == MigrationRunning
}

// IsResumable reports whether Resume may relaunch the job: parked by an
// operator, or failed and worth retrying after the cause is fixed. A finished
// job is not — relaunching it would relist the entire source for nothing.
func (j MigrationJob) IsResumable() bool {
	return j.Status == MigrationPaused || j.Status == MigrationError
}
