package service_test

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func TestAllow_NilRule(t *testing.T) {
	if !service.Allow(nil, "/any/path") {
		t.Fatal("nil rule must allow all paths")
	}
}

func TestAllow_BlockMode_NoMatch(t *testing.T) {
	rule := &domain.RoutingRule{Mode: "BLOCK", Matchers: []string{`^/blocked/`}}
	if !service.Allow(rule, "/allowed/foo") {
		t.Fatal("BLOCK rule must allow non-matching path")
	}
}

func TestAllow_BlockMode_Match(t *testing.T) {
	rule := &domain.RoutingRule{Mode: "BLOCK", Matchers: []string{`^/blocked/`}}
	if service.Allow(rule, "/blocked/secret.jar") {
		t.Fatal("BLOCK rule must deny matching path")
	}
}

func TestAllow_AllowMode_Match(t *testing.T) {
	rule := &domain.RoutingRule{Mode: "ALLOW", Matchers: []string{`^/releases/`}}
	if !service.Allow(rule, "/releases/foo-1.0.jar") {
		t.Fatal("ALLOW rule must permit matching path")
	}
}

func TestAllow_AllowMode_NoMatch(t *testing.T) {
	rule := &domain.RoutingRule{Mode: "ALLOW", Matchers: []string{`^/releases/`}}
	if service.Allow(rule, "/snapshots/foo-SNAPSHOT.jar") {
		t.Fatal("ALLOW rule must block non-matching path")
	}
}

func TestRoutingRuleService_CreateValidate(t *testing.T) {
	svc := service.NewRoutingRuleService(testutil.NewRoutingRuleRepo())
	ctx := context.Background()

	// bad mode
	err := svc.Create(ctx, &domain.RoutingRule{Name: "r1", Mode: "INVALID"})
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}

	// bad regex
	err = svc.Create(ctx, &domain.RoutingRule{Name: "r1", Mode: "BLOCK", Matchers: []string{"[invalid"}})
	if err == nil {
		t.Fatal("expected error for invalid regex matcher")
	}

	// valid
	err = svc.Create(ctx, &domain.RoutingRule{Name: "r1", Mode: "BLOCK", Matchers: []string{`^/blocked/`}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rules, _ := svc.List(ctx)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
}

func TestAllow_CachedMatcher_RepeatedCalls(t *testing.T) {
	rule := &domain.RoutingRule{Mode: "ALLOW", Matchers: []string{`^/v2/library/.*`}}
	// repeated calls hit the compile cache; behavior must be stable
	for i := 0; i < 3; i++ {
		if !service.Allow(rule, "/v2/library/alpine/manifests/latest") {
			t.Fatal("expected allow")
		}
		if service.Allow(rule, "/maven2/foo.jar") {
			t.Fatal("expected block")
		}
	}
}

func TestAllow_InvalidMatcher_Skipped(t *testing.T) {
	rule := &domain.RoutingRule{Mode: "ALLOW", Matchers: []string{`(`}} // invalid regex
	if service.Allow(rule, "anything") {
		t.Fatal("invalid matcher must be skipped, ALLOW with no valid match → blocked")
	}
}

func TestRoutingRuleService_CRUD(t *testing.T) {
	svc := service.NewRoutingRuleService(testutil.NewRoutingRuleRepo())
	ctx := context.Background()

	r := &domain.RoutingRule{Name: "test", Mode: "ALLOW", Matchers: []string{`/releases/`}}
	if err := svc.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	if r.ID == "" {
		t.Fatal("ID must be set after create")
	}

	got, err := svc.Get(ctx, r.ID)
	if err != nil || got == nil {
		t.Fatalf("Get failed: %v", err)
	}

	if err := svc.Delete(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	rules, _ := svc.List(ctx)
	if len(rules) != 0 {
		t.Fatal("expected 0 rules after delete")
	}
}
