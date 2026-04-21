package handlers

import (
	"context"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// expandGroupMemberRepoNames returns member repository names when name refers to a group
// repository; otherwise a single-element slice with the original name.
// Group repos do not store components — metadata lives on members.
func expandGroupMemberRepoNames(ctx context.Context, repos repository.RepositoryRepo, name string) ([]string, error) {
	if name == "" {
		return nil, nil
	}
	rep, err := repos.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	if rep == nil || rep.Type != domain.TypeGroup {
		return []string{name}, nil
	}
	return domain.GroupMemberNames(rep), nil
}
