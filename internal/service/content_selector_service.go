// Package service — content_selector_service.go
//
// ContentSelectorService owns CRUD for CEL-based content selectors and the
// auth-gate evaluator. Selectors decide whether an artifact path is visible
// to a given user based on repository format/path/name.
//
// Variant B semantics (by product decision): the gate only runs when the
// user actually has at least one privilege with a selector attached. If
// their effective privileges carry no selector, requests pass through
// unchanged — an incremental rollout path so existing roles keep working.
package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/cel-go/cel"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// ContentSelectorService handles CRUD and evaluation of content selectors.
// It caches compiled cel.Program values keyed by selectorID+expression so
// the hot path (artifact GET/HEAD) does not recompile on every request.
type ContentSelectorService struct {
	repo repository.ContentSelectorRepo
	env  *cel.Env

	mu       sync.RWMutex
	programs map[string]cachedProgram // key = selector.ID
}

type cachedProgram struct {
	expression string // invalidate when selector.expression changes
	program    cel.Program
}

// NewContentSelectorService builds a service and its CEL environment.
// The env declares three string vars: format, path, repository.
// Returns an error only if the CEL env fails to initialize — a bug in this
// code, not user input — so the caller can just panic at router wiring time.
func NewContentSelectorService(repo repository.ContentSelectorRepo) (*ContentSelectorService, error) {
	env, err := cel.NewEnv(
		cel.Variable("format", cel.StringType),
		cel.Variable("path", cel.StringType),
		cel.Variable("repository", cel.StringType),
	)
	if err != nil {
		return nil, fmt.Errorf("cel env: %w", err)
	}
	return &ContentSelectorService{
		repo:     repo,
		env:      env,
		programs: make(map[string]cachedProgram),
	}, nil
}

// compile parses, type-checks, and builds a cel.Program. The resulting
// expression must return bool; non-bool result types are rejected here.
func (s *ContentSelectorService) compile(expression string) (cel.Program, error) {
	ast, issues := s.env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("cel compile: %w", issues.Err())
	}
	if ast.OutputType() != cel.BoolType {
		return nil, fmt.Errorf("expression must return bool, got %s", ast.OutputType())
	}
	prg, err := s.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("cel program: %w", err)
	}
	return prg, nil
}

func (s *ContentSelectorService) List(ctx context.Context) ([]domain.ContentSelector, error) {
	return s.repo.List(ctx)
}

func (s *ContentSelectorService) Get(ctx context.Context, id string) (*domain.ContentSelector, error) {
	return s.repo.Get(ctx, id)
}

func (s *ContentSelectorService) Create(ctx context.Context, sel *domain.ContentSelector) error {
	if sel.Name == "" {
		return fmt.Errorf("name is required")
	}
	if sel.Expression == "" {
		return fmt.Errorf("expression is required")
	}
	if _, err := s.compile(sel.Expression); err != nil {
		return err
	}
	return s.repo.Create(ctx, sel)
}

func (s *ContentSelectorService) Update(ctx context.Context, sel *domain.ContentSelector) error {
	if sel.Name == "" {
		return fmt.Errorf("name is required")
	}
	if _, err := s.compile(sel.Expression); err != nil {
		return err
	}
	if err := s.repo.Update(ctx, sel); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.programs, sel.ID) // force recompile on next Evaluate
	s.mu.Unlock()
	return nil
}

func (s *ContentSelectorService) Delete(ctx context.Context, id string) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.programs, id)
	s.mu.Unlock()
	return nil
}

func (s *ContentSelectorService) AttachToPrivilege(ctx context.Context, privilegeName, selectorID string) error {
	existing, err := s.repo.Get(ctx, selectorID)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("content selector %q not found", selectorID)
	}
	return s.repo.AttachToPrivilege(ctx, privilegeName, selectorID)
}

func (s *ContentSelectorService) DetachFromPrivilege(ctx context.Context, privilegeName string) error {
	return s.repo.DetachFromPrivilege(ctx, privilegeName)
}

// program returns the compiled program for sel, using the cache when the
// stored expression has not changed.
func (s *ContentSelectorService) program(sel domain.ContentSelector) (cel.Program, error) {
	s.mu.RLock()
	cached, ok := s.programs[sel.ID]
	s.mu.RUnlock()
	if ok && cached.expression == sel.Expression {
		return cached.program, nil
	}
	prg, err := s.compile(sel.Expression)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.programs[sel.ID] = cachedProgram{expression: sel.Expression, program: prg}
	s.mu.Unlock()
	return prg, nil
}

// Evaluate runs all selectors reachable via the user's privileges against
// the request context. Variant B: when the user has no selector privileges,
// gated==false and the caller should skip the gate entirely.
//
// When gated==true, allowed reflects whether any selector returned true —
// a single match is enough to allow (OR semantics), matching Nexus.
func (s *ContentSelectorService) Evaluate(ctx context.Context, userID, repo, format, path string) (gated bool, allowed bool, err error) {
	if userID == "" {
		return false, true, nil
	}
	selectors, err := s.repo.ListForUser(ctx, userID)
	if err != nil {
		return false, false, err
	}
	if len(selectors) == 0 {
		return false, true, nil
	}
	vars := map[string]any{
		"format":     format,
		"path":       path,
		"repository": repo,
	}
	for _, sel := range selectors {
		prg, cerr := s.program(sel)
		if cerr != nil {
			// A selector that no longer compiles is treated as non-matching;
			// it must not silently allow or deny every request. An operator
			// will see the error when editing it via the API.
			continue
		}
		out, _, evalErr := prg.Eval(vars)
		if evalErr != nil {
			continue
		}
		if matched, ok := out.Value().(bool); ok && matched {
			return true, true, nil
		}
	}
	return true, false, nil
}
