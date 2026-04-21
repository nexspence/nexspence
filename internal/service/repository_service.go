package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrInvalidInput  = errors.New("invalid input")
)

// RepositoryService handles business logic for Nexus-compatible repository management.
type RepositoryService struct {
	repos     repository.RepositoryRepo
	blobs     repository.BlobStoreRepo
	blobStore storage.BlobStore
	policies  repository.CleanupPolicyRepo
}

func NewRepositoryService(
	repos repository.RepositoryRepo,
	blobs repository.BlobStoreRepo,
	blobStore storage.BlobStore,
	policies repository.CleanupPolicyRepo,
) *RepositoryService {
	return &RepositoryService{repos: repos, blobs: blobs, blobStore: blobStore, policies: policies}
}

func (s *RepositoryService) List(ctx context.Context, format, repoType string) ([]domain.Repository, error) {
	return s.repos.List(ctx, format, repoType)
}

func (s *RepositoryService) Get(ctx context.Context, name string) (*domain.Repository, error) {
	r, err := s.repos.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, fmt.Errorf("%w: repository %q", ErrNotFound, name)
	}
	return r, nil
}

func (s *RepositoryService) Create(ctx context.Context, r *domain.Repository) error {
	// Validate
	if r.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidInput)
	}
	if r.Format == "" {
		return fmt.Errorf("%w: format is required", ErrInvalidInput)
	}
	if r.Type == "" {
		return fmt.Errorf("%w: type is required", ErrInvalidInput)
	}

	// Check duplicate
	existing, err := s.repos.Get(ctx, r.Name)
	if err != nil {
		return err
	}
	if existing != nil {
		return fmt.Errorf("%w: repository %q", ErrAlreadyExists, r.Name)
	}

	if r.Type == domain.TypeProxy {
		if r.ProxyConfig == nil {
			return fmt.Errorf("%w: proxy repositories require proxy_config with remote_url", ErrInvalidInput)
		}
		raw, ok := r.ProxyConfig["remote_url"]
		s, strOK := raw.(string)
		if !ok || !strOK || s == "" {
			return fmt.Errorf("%w: proxy_config.remote_url must be a non-empty string", ErrInvalidInput)
		}
	}

	if r.Type == domain.TypeGroup {
		if err := s.validateGroupMembers(ctx, r); err != nil {
			return err
		}
	}

	if err := s.validateCleanupPolicies(ctx, r.Format, r.CleanupPolicyIDs); err != nil {
		return err
	}

	// Validate blob store exists (for hosted/proxy)
	if r.BlobStoreID != nil && *r.BlobStoreID != "" {
		bs, err := s.blobs.Get(ctx, *r.BlobStoreID)
		if err != nil {
			return err
		}
		if bs == nil {
			return fmt.Errorf("%w: blob store %q", ErrNotFound, *r.BlobStoreID)
		}
	}

	r.Online = true
	return s.repos.Create(ctx, r)
}

func (s *RepositoryService) Update(ctx context.Context, name string, updates *domain.Repository) (*domain.Repository, error) {
	r, err := s.Get(ctx, name)
	if err != nil {
		return nil, err
	}

	// Apply allowed updates
	if updates.Online != r.Online {
		r.Online = updates.Online
	}
	if updates.Description != "" {
		r.Description = updates.Description
	}
	if updates.FormatConfig != nil {
		r.FormatConfig = updates.FormatConfig
	}
	if updates.HTTPConfig != nil {
		r.HTTPConfig = updates.HTTPConfig
	}
	if updates.ProxyConfig != nil {
		r.ProxyConfig = updates.ProxyConfig
	}
	if updates.QuotaBytes != nil {
		r.QuotaBytes = updates.QuotaBytes
	}
	if updates.CleanupPolicyIDs != nil {
		r.CleanupPolicyIDs = updates.CleanupPolicyIDs
	}
	r.AllowAnonymous = updates.AllowAnonymous

	if err := s.validateCleanupPolicies(ctx, r.Format, r.CleanupPolicyIDs); err != nil {
		return nil, err
	}

	if r.Type == domain.TypeGroup {
		if err := s.validateGroupMembers(ctx, r); err != nil {
			return nil, err
		}
	}

	if err := s.repos.Update(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *RepositoryService) validateCleanupPolicies(ctx context.Context, repoFormat domain.RepoFormat, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		p, err := s.policies.Get(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: cleanup policy %q does not exist", ErrInvalidInput, id)
			}
			return err
		}
		if p == nil {
			return fmt.Errorf("%w: cleanup policy %q does not exist", ErrInvalidInput, id)
		}
		pf := p.Format
		if pf != "" && pf != "*" && pf != string(repoFormat) {
			return fmt.Errorf("%w: cleanup policy %q targets format %s but repository is %s",
				ErrInvalidInput, p.Name, pf, repoFormat)
		}
	}
	return nil
}

func (s *RepositoryService) validateGroupMembers(ctx context.Context, group *domain.Repository) error {
	names := domain.GroupMemberNames(group)
	if len(names) == 0 {
		return fmt.Errorf("%w: group repositories require formatConfig.member_names with at least one member", ErrInvalidInput)
	}
	for _, name := range names {
		m, err := s.repos.Get(ctx, name)
		if err != nil {
			return err
		}
		if m == nil {
			return fmt.Errorf("%w: group member repository %q does not exist", ErrInvalidInput, name)
		}
		if m.Type == domain.TypeGroup {
			return fmt.Errorf("%w: group member %q cannot be a group repository", ErrInvalidInput, name)
		}
		if m.Format != group.Format {
			return fmt.Errorf("%w: group member %q has format %q, expected %q", ErrInvalidInput, name, m.Format, group.Format)
		}
	}
	return nil
}

func (s *RepositoryService) Delete(ctx context.Context, name string) error {
	r, err := s.Get(ctx, name)
	if err != nil {
		return err
	}
	_ = r
	return s.repos.Delete(ctx, name)
}
