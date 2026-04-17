// Package repository defines interfaces for all database access.
// All implementations live in repository/postgres/.
package repository

import (
	"context"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// RepositoryRepo manages repository definitions.
type RepositoryRepo interface {
	List(ctx context.Context, format, repoType string) ([]domain.Repository, error)
	Get(ctx context.Context, name string) (*domain.Repository, error)
	GetByID(ctx context.Context, id string) (*domain.Repository, error)
	Create(ctx context.Context, r *domain.Repository) error
	Update(ctx context.Context, r *domain.Repository) error
	Delete(ctx context.Context, name string) error
}

// ComponentRepo manages component metadata.
type ComponentRepo interface {
	List(ctx context.Context, repoName string, limit int, offset int) (*domain.Page[domain.Component], error)
	Get(ctx context.Context, id string) (*domain.Component, error)
	Search(ctx context.Context, p domain.SearchParams) (*domain.Page[domain.Component], error)
	Create(ctx context.Context, c *domain.Component) error
	Delete(ctx context.Context, id string) error
}

// AssetRepo manages artifact file records.
type AssetRepo interface {
	List(ctx context.Context, repoName string, limit int, offset int) (*domain.Page[domain.Asset], error)
	Get(ctx context.Context, id string) (*domain.Asset, error)
	GetByPath(ctx context.Context, repoName, path string) (*domain.Asset, error)
	SearchAssets(ctx context.Context, p domain.SearchParams) (*domain.Page[domain.Asset], error)
	// ListStale returns assets matching cleanup criteria:
	//   format "*" matches all; lastDownloadedDays/artifactAgeDays 0 = no filter.
	ListStale(ctx context.Context, format string, lastDownloadedDays, artifactAgeDays int, limit int) ([]domain.Asset, error)
	Create(ctx context.Context, a *domain.Asset) error
	Delete(ctx context.Context, id string) error
	IncrementDownload(ctx context.Context, id string) error
}

// UserRepo manages user accounts.
type UserRepo interface {
	List(ctx context.Context, source string) ([]domain.User, error)
	Get(ctx context.Context, username string) (*domain.User, error)
	GetByID(ctx context.Context, id string) (*domain.User, error)
	Create(ctx context.Context, u *domain.User) error
	Update(ctx context.Context, u *domain.User) error
	UpdatePassword(ctx context.Context, username, hash string) error
	Delete(ctx context.Context, username string) error
	UpdateLastLogin(ctx context.Context, username string) error
}

// RoleRepo manages roles and privileges.
type RoleRepo interface {
	List(ctx context.Context) ([]domain.Role, error)
	Get(ctx context.Context, id string) (*domain.Role, error)
	Create(ctx context.Context, r *domain.Role) error
	Update(ctx context.Context, r *domain.Role) error
	Delete(ctx context.Context, id string) error
	GetUserRoles(ctx context.Context, userID string) ([]domain.Role, error)
	SetUserRoles(ctx context.Context, userID string, roleIDs []string) error
}

// CleanupPolicyRepo manages cleanup policies.
type CleanupPolicyRepo interface {
	List(ctx context.Context) ([]domain.CleanupPolicy, error)
	Get(ctx context.Context, id string) (*domain.CleanupPolicy, error)
	Create(ctx context.Context, p *domain.CleanupPolicy) error
	Update(ctx context.Context, p *domain.CleanupPolicy) error
	Delete(ctx context.Context, id string) error
}

// AuditRepo writes and reads audit log events.
type AuditRepo interface {
	Write(ctx context.Context, e *domain.AuditEvent) error
	List(ctx context.Context, domain, action string, limit, offset int) ([]domain.AuditEvent, error)
}

// BlobStoreRepo manages blob store configuration.
type BlobStoreRepo interface {
	List(ctx context.Context) ([]domain.BlobStore, error)
	Get(ctx context.Context, name string) (*domain.BlobStore, error)
	Create(ctx context.Context, b *domain.BlobStore) error
	Update(ctx context.Context, b *domain.BlobStore) error
	Delete(ctx context.Context, name string) error
	UpdateUsedBytes(ctx context.Context, name string, delta int64) error
}
