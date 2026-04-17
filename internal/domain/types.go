// Package domain contains all core business types shared across layers.
// No external dependencies — only stdlib.
package domain

import (
	"time"
)

// ── Repository ───────────────────────────────────────────────

type RepoFormat string
type RepoType string

const (
	FormatMaven2 RepoFormat = "maven2"
	FormatNPM    RepoFormat = "npm"
	FormatDocker RepoFormat = "docker"
	FormatPyPI   RepoFormat = "pypi"
	FormatGo     RepoFormat = "go"
	FormatNuGet  RepoFormat = "nuget"
	FormatHelm   RepoFormat = "helm"
	FormatRaw    RepoFormat = "raw"
	FormatApt    RepoFormat = "apt"
	FormatYum    RepoFormat = "yum"

	TypeHosted RepoType = "hosted"
	TypeProxy  RepoType = "proxy"
	TypeGroup  RepoType = "group"
)

type Repository struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Format           RepoFormat        `json:"format"`
	Type             RepoType          `json:"type"`
	BlobStoreID      *string           `json:"blobStoreId,omitempty"`
	Online           bool              `json:"online"`
	FormatConfig     map[string]any    `json:"formatConfig,omitempty"`
	HTTPConfig       map[string]any    `json:"httpConfig,omitempty"`
	ProxyConfig      map[string]any    `json:"proxyConfig,omitempty"`
	CleanupPolicyIDs []string          `json:"cleanupPolicyIds,omitempty"`
	QuotaBytes       *int64            `json:"quotaBytes,omitempty"`
	RoutingRuleID    *string           `json:"routingRuleId,omitempty"`
	Description      string            `json:"description,omitempty"`
	URL              string            `json:"url,omitempty"` // computed
	CreatedAt        time.Time         `json:"createdAt"`
	UpdatedAt        time.Time         `json:"updatedAt"`
}

// ── Blob Store ───────────────────────────────────────────────

type BlobStore struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Type       string         `json:"type"` // "local" | "s3"
	Config     map[string]any `json:"config,omitempty"`
	QuotaBytes *int64         `json:"quotaBytes,omitempty"`
	UsedBytes  int64          `json:"usedBytes"`
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
}

// ── Component ────────────────────────────────────────────────

type Component struct {
	ID             string     `json:"id"`
	RepositoryID   string     `json:"repositoryId"`
	Repository     string     `json:"repository"` // name
	Format         string     `json:"format"`
	Group          string     `json:"group"`
	Name           string     `json:"name"`
	Version        string     `json:"version"`
	Extra          map[string]any `json:"extra,omitempty"`
	LastDownloaded *time.Time `json:"lastDownloaded,omitempty"`
	DownloadCount  int64      `json:"downloadCount"`
	Assets         []Asset    `json:"assets,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}

// ── Asset ────────────────────────────────────────────────────

type Asset struct {
	ID             string     `json:"id"`
	ComponentID    string     `json:"componentId"`
	RepositoryID   string     `json:"repositoryId"`
	Repository     string     `json:"repository"` // name
	Path           string     `json:"path"`
	BlobStoreID    string     `json:"blobStoreId"`
	BlobKey        string     `json:"-"` // internal
	SizeBytes      int64      `json:"fileSize"`
	ContentType    string     `json:"contentType"`
	SHA1           string     `json:"sha1,omitempty"`
	SHA256         string     `json:"sha256,omitempty"`
	MD5            string     `json:"md5,omitempty"`
	DownloadURL    string     `json:"downloadUrl,omitempty"` // computed
	LastModified   time.Time  `json:"lastModified"`
	LastDownloaded *time.Time `json:"lastDownloaded,omitempty"`
	DownloadCount  int64      `json:"downloadCount"`
	CreatedAt      time.Time  `json:"createdAt"`
}

// ── User ─────────────────────────────────────────────────────

type UserStatus string
type UserSource string

const (
	UserStatusActive   UserStatus = "active"
	UserStatusDisabled UserStatus = "disabled"

	UserSourceLocal UserSource = "local"
	UserSourceLDAP  UserSource = "ldap"
)

type User struct {
	ID           string     `json:"userId"`
	Username     string     `json:"userId"` // Nexus API uses "userId" as the identifier field
	Email        string     `json:"emailAddress"`
	PasswordHash string     `json:"-"`
	FirstName    string     `json:"firstName"`
	LastName     string     `json:"lastName"`
	Status       UserStatus `json:"status"`
	Source       UserSource `json:"source"`
	ExternalID   string     `json:"-"`
	Roles        []string   `json:"roles"` // role names
	LastLogin    *time.Time `json:"lastLogin,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

// ── Role ─────────────────────────────────────────────────────

type Role struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Privileges  []string  `json:"privileges"`
	Roles       []string  `json:"roles"` // nested roles
	ReadOnly    bool      `json:"readOnly"`
	Source      string    `json:"source,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ── Cleanup Policy ───────────────────────────────────────────

type CleanupPolicy struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	Format       string         `json:"format"` // "*" = all formats
	Criteria     map[string]any `json:"criteria"` // e.g. {"lastDownloadedDays":30,"artifactAgeDays":90}
	ScheduleCron string         `json:"scheduleCron,omitempty"`
	Enabled      bool           `json:"enabled"`
	DryRun       bool           `json:"dryRun"`
	LastRunAt    *time.Time     `json:"lastRunAt,omitempty"`
	LastRunFreed int64          `json:"lastRunFreedBytes,omitempty"`
	LastRunCount int            `json:"lastRunCount,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

// ── Audit Event ──────────────────────────────────────────────

type AuditEvent struct {
	ID         int64          `json:"id"`
	EventTime  time.Time      `json:"eventTime"`
	UserID     *string        `json:"userId,omitempty"`
	Username   string         `json:"username"`
	RemoteIP   string         `json:"remoteIp,omitempty"`
	UserAgent  string         `json:"userAgent,omitempty"`
	Domain     string         `json:"domain"`  // e.g. "REPOSITORY", "SECURITY", "USER"
	Action     string         `json:"action"`  // e.g. "CREATE", "DELETE", "LOGIN"
	EntityType string         `json:"entityType,omitempty"`
	EntityID   string         `json:"entityId,omitempty"`
	EntityName string         `json:"entityName,omitempty"`
	Context    map[string]any `json:"context,omitempty"`
	Result     string         `json:"result"` // "success" | "failure" | "denied"
}

// ── Pagination ───────────────────────────────────────────────

type Page[T any] struct {
	Items             []T     `json:"items"`
	ContinuationToken *string `json:"continuationToken"`
}

// ── Search params ────────────────────────────────────────────

type SearchParams struct {
	Repository  string
	Format      string
	Group       string
	Name        string
	Version     string
	SHA256      string
	// Maven
	MavenGroupID    string
	MavenArtifactID string
	MavenVersion    string
	// Docker
	DockerImageName string
	DockerImageTag  string
	// Pagination
	Offset int
	Limit  int
}
