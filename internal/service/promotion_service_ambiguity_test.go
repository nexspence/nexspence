package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Two rules on the same repository pair can both cover a component with
// different gates. Confirming only that the named rule covers it lets a caller
// pick the lax one and complete a promotion the strict one would have blocked
// (#366). The refusal is symmetric: the server cannot tell which rule the
// caller meant, so naming either is refused.
func TestPromotionService_Promote_RefusesAmbiguousSiblingRules(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	repoRepo.Create(ctx, testutil.SimpleRepo("staging", "raw"))
	repoRepo.Create(ctx, testutil.SimpleRepo("production", "raw"))

	comp := &domain.Component{
		ID: "comp-ambiguous", Repository: "staging", Format: "raw",
		Group: "com/example", Name: "mylib", Version: "1.0.0",
	}
	compRepo.AddComponent(comp)

	strict := &domain.PromotionRule{
		Name: "strict", FromRepo: "staging", ToRepo: "production", RequireScanPass: true,
	}
	lax := &domain.PromotionRule{Name: "lax", FromRepo: "staging", ToRepo: "production"}
	for _, r := range []*domain.PromotionRule{strict, lax} {
		if err := promoRepo.CreateRule(ctx, r); err != nil {
			t.Fatalf("CreateRule: %v", err)
		}
	}

	for _, tc := range []struct {
		name string
		rule *domain.PromotionRule
	}{
		{"naming the lax rule", lax},
		{"naming the strict rule", strict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Promote(ctx, tc.rule.ID, []string{comp.ID}, "user-1")
			if !errors.Is(err, service.ErrAmbiguousPromotionRule) {
				t.Fatalf("expected an ambiguity refusal, got: %v", err)
			}
			if !strings.Contains(err.Error(), "strict") || !strings.Contains(err.Error(), "lax") {
				t.Fatalf("the refusal must name both rules, got: %v", err)
			}
		})
	}

	reqs, err := promoRepo.ListRequests(ctx, "")
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(reqs) != 0 {
		t.Fatalf("a refused promotion must leave no request rows, got %d", len(reqs))
	}

	// Removing the ambiguity restores normal behavior: the strict rule falls
	// through to its own scan gate rather than being refused as ambiguous.
	if err := promoRepo.DeleteRule(ctx, lax.ID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	_, err = svc.Promote(ctx, strict.ID, []string{comp.ID}, "user-1")
	if err == nil || !strings.Contains(err.Error(), "scan required") {
		t.Fatalf("expected the strict rule's own scan gate, got: %v", err)
	}
}

// A sibling rule on a different target repository is not ambiguous: the two
// rules promote to different places, so naming one says which.
func TestPromotionService_Promote_SiblingToOtherRepo_IsNotAmbiguous(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	for _, name := range []string{"staging", "production", "archive"} {
		repoRepo.Create(ctx, testutil.SimpleRepo(name, "raw"))
	}
	comp := &domain.Component{
		ID: "comp-two-targets", Repository: "staging", Format: "raw",
		Group: "com/example", Name: "mylib", Version: "1.0.0",
	}
	compRepo.AddComponent(comp)

	toProd := &domain.PromotionRule{Name: "to-prod", FromRepo: "staging", ToRepo: "production"}
	toArchive := &domain.PromotionRule{Name: "to-archive", FromRepo: "staging", ToRepo: "archive"}
	for _, r := range []*domain.PromotionRule{toProd, toArchive} {
		if err := promoRepo.CreateRule(ctx, r); err != nil {
			t.Fatalf("CreateRule: %v", err)
		}
	}

	reqs, err := svc.Promote(ctx, toProd.ID, []string{comp.ID}, "user-1")
	if err != nil || len(reqs) != 1 {
		t.Fatalf("Promote must succeed with distinct targets, got %v (%d requests)", err, len(reqs))
	}
}

// A sibling on the same pair whose path filter excludes this component covers
// something else entirely — nothing to be ambiguous about.
func TestPromotionService_Promote_SiblingWithDisjointFilter_IsNotAmbiguous(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	repoRepo.Create(ctx, testutil.SimpleRepo("staging", "raw"))
	repoRepo.Create(ctx, testutil.SimpleRepo("production", "raw"))

	comp := &domain.Component{
		ID: "comp-scoped", Repository: "staging", Format: "raw",
		Group: "com/example", Name: "mylib", Version: "1.0.0",
	}
	compRepo.AddComponent(comp)

	open := &domain.PromotionRule{Name: "open", FromRepo: "staging", ToRepo: "production"}
	elsewhere := &domain.PromotionRule{
		Name: "elsewhere", FromRepo: "staging", ToRepo: "production",
		PathFilter: `path.startsWith("/com/other/")`,
	}
	for _, r := range []*domain.PromotionRule{open, elsewhere} {
		if err := promoRepo.CreateRule(ctx, r); err != nil {
			t.Fatalf("CreateRule: %v", err)
		}
	}

	reqs, err := svc.Promote(ctx, open.ID, []string{comp.ID}, "user-1")
	if err != nil || len(reqs) != 1 {
		t.Fatalf("Promote must succeed when the sibling's filter excludes the component, got %v (%d requests)", err, len(reqs))
	}
}

// The re-check at copy time matters here too: a sibling rule created while a
// request sat pending makes it ambiguous only at approval time.
func TestPromotionService_Approve_RefusesRuleThatBecameAmbiguous(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	repoRepo.Create(ctx, testutil.SimpleRepo("staging", "raw"))
	repoRepo.Create(ctx, testutil.SimpleRepo("production", "raw"))

	comp := &domain.Component{
		ID: "comp-pending", Repository: "staging", Format: "raw",
		Group: "com/example", Name: "mylib", Version: "1.0.0",
	}
	compRepo.AddComponent(comp)

	gated := &domain.PromotionRule{
		Name: "gated", FromRepo: "staging", ToRepo: "production", RequireManualApproval: true,
	}
	if err := promoRepo.CreateRule(ctx, gated); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	reqs, err := svc.Promote(ctx, gated.ID, []string{comp.ID}, "user-1")
	if err != nil || len(reqs) != 1 {
		t.Fatalf("Promote: %v (%d requests)", err, len(reqs))
	}

	// A second rule on the same pair appears while the request is pending.
	if err := promoRepo.CreateRule(ctx, &domain.PromotionRule{
		Name: "added-later", FromRepo: "staging", ToRepo: "production",
	}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	err = svc.Approve(ctx, reqs[0].ID, "admin-1")
	if !errors.Is(err, service.ErrAmbiguousPromotionRule) {
		t.Fatalf("expected Approve to refuse the now-ambiguous request, got: %v", err)
	}
}
