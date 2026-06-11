// Package service contains business logic for backup and restore of all repository data.
package service

import (
	"errors"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// BackupService exports and restores all repository data (metadata + blobs).
type BackupService struct {
	BlobStores repository.BlobStoreRepo
	Repos      repository.RepositoryRepo
	Users      repository.UserRepo
	Roles      repository.RoleRepo
	Policies   repository.CleanupPolicyRepo
	Components repository.ComponentRepo
	Assets     repository.AssetRepo
	BlobStore  storage.BlobStore
}

// Sentinel errors for per-repository operations.
var (
	ErrRepoNotFound = errors.New("repository not found")
	ErrRepoConflict = errors.New("repository already exists")
)

// RestoreStats reports what was restored.
type RestoreStats struct {
	BlobStores int `json:"blobStores"`
	Repos      int `json:"repositories"`
	Users      int `json:"users"`
	Roles      int `json:"roles"`
	Policies   int `json:"cleanupPolicies"`
	Components int `json:"components"`
	Assets     int `json:"assets"`
	Blobs      int `json:"blobs"`
}

// backupUser carries the password hash in backup archives (json:"-" hides it in normal API responses).
type backupUser struct {
	domain.User
	PasswordHash string `json:"passwordHash"`
}
