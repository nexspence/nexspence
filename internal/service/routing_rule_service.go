package service

import (
	"context"
	"fmt"
	"regexp"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// RoutingRuleService provides CRUD and path evaluation for routing rules.
type RoutingRuleService struct {
	repo repository.RoutingRuleRepo
}

func NewRoutingRuleService(repo repository.RoutingRuleRepo) *RoutingRuleService {
	return &RoutingRuleService{repo: repo}
}

func (s *RoutingRuleService) List(ctx context.Context) ([]domain.RoutingRule, error) {
	return s.repo.List(ctx)
}

func (s *RoutingRuleService) Get(ctx context.Context, id string) (*domain.RoutingRule, error) {
	return s.repo.Get(ctx, id)
}

func (s *RoutingRuleService) Create(ctx context.Context, r *domain.RoutingRule) error {
	if r.Name == "" {
		return fmt.Errorf("name is required")
	}
	if r.Mode != "ALLOW" && r.Mode != "BLOCK" {
		return fmt.Errorf("mode must be ALLOW or BLOCK")
	}
	if err := validateMatchers(r.Matchers); err != nil {
		return err
	}
	return s.repo.Create(ctx, r)
}

func (s *RoutingRuleService) Update(ctx context.Context, r *domain.RoutingRule) error {
	if r.Mode != "ALLOW" && r.Mode != "BLOCK" {
		return fmt.Errorf("mode must be ALLOW or BLOCK")
	}
	if err := validateMatchers(r.Matchers); err != nil {
		return err
	}
	return s.repo.Update(ctx, r)
}

func (s *RoutingRuleService) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

// Allow reports whether the given path should be allowed through according to the rule.
// mode=ALLOW: path must match at least one matcher.
// mode=BLOCK: path must not match any matcher.
// An empty matchers list with mode=ALLOW blocks everything; with mode=BLOCK allows everything.
func Allow(rule *domain.RoutingRule, path string) bool {
	if rule == nil {
		return true
	}
	matched := matchesAny(rule.Matchers, path)
	if rule.Mode == "ALLOW" {
		return matched
	}
	// mode == BLOCK
	return !matched
}

func matchesAny(matchers []string, path string) bool {
	for _, m := range matchers {
		re, err := regexp.Compile(m)
		if err != nil {
			continue
		}
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

func validateMatchers(matchers []string) error {
	for _, m := range matchers {
		if _, err := regexp.Compile(m); err != nil {
			return fmt.Errorf("invalid matcher regex %q: %w", m, err)
		}
	}
	return nil
}
