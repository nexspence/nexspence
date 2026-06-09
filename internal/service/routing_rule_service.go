package service

import (
	"context"
	"fmt"
	"regexp"
	"sync"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// compiledMatchers caches compiled matcher regexes keyed by pattern string.
// Routing-rule Allow() runs per request on group repos; recompiling each call
// dominates the actual match, so cache the compiled form.
//
// Entries are never evicted: cardinality is bounded by the number of distinct
// patterns across admin-managed routing rules (operator-bounded, not request-
// bounded). If v2 multi-tenancy ever lets non-admins author matchers, replace
// this with a bounded/LRU cache to avoid unbounded growth.
var compiledMatchers sync.Map // string -> *regexp.Regexp

func compileMatcher(pattern string) (*regexp.Regexp, error) {
	if v, ok := compiledMatchers.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	actual, _ := compiledMatchers.LoadOrStore(pattern, re)
	return actual.(*regexp.Regexp), nil
}

// RoutingRuleService provides CRUD and path evaluation for routing rules.
type RoutingRuleService struct {
	repo repository.RoutingRuleRepo
}

// NewRoutingRuleService constructs a service for managing routing rules.
func NewRoutingRuleService(repo repository.RoutingRuleRepo) *RoutingRuleService {
	return &RoutingRuleService{repo: repo}
}

// List returns all routing rules.
func (s *RoutingRuleService) List(ctx context.Context) ([]domain.RoutingRule, error) {
	return s.repo.List(ctx)
}

// Get returns the routing rule with the given id.
func (s *RoutingRuleService) Get(ctx context.Context, id string) (*domain.RoutingRule, error) {
	return s.repo.Get(ctx, id)
}

// Create validates the rule mode and matcher regexes, then persists a new routing rule.
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

// Update validates the rule mode and matcher regexes, then persists changes to a routing rule.
func (s *RoutingRuleService) Update(ctx context.Context, r *domain.RoutingRule) error {
	if r.Mode != "ALLOW" && r.Mode != "BLOCK" {
		return fmt.Errorf("mode must be ALLOW or BLOCK")
	}
	if err := validateMatchers(r.Matchers); err != nil {
		return err
	}
	return s.repo.Update(ctx, r)
}

// Delete removes the routing rule with the given id.
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
		re, err := compileMatcher(m)
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
