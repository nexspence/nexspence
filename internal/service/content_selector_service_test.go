package service

import (
	"context"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func newSvc(t *testing.T) (*ContentSelectorService, *testutil.ContentSelectorRepo) {
	t.Helper()
	repo := testutil.NewContentSelectorRepo()
	svc, err := NewContentSelectorService(repo)
	if err != nil {
		t.Fatalf("NewContentSelectorService: %v", err)
	}
	return svc, repo
}

func TestContentSelector_CreateValidatesCEL(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()

	// Valid expression compiles and stores.
	sel := &domain.ContentSelector{
		Name:       "maven-only",
		Expression: `format == "maven2"`,
	}
	if err := svc.Create(ctx, sel); err != nil {
		t.Fatalf("valid expression rejected: %v", err)
	}
	if sel.ID == "" {
		t.Fatalf("selector ID not populated after Create")
	}

	// Syntactically invalid CEL is rejected with an error that mentions cel.
	bad := &domain.ContentSelector{Name: "bad-syntax", Expression: `format ==`}
	err := svc.Create(ctx, bad)
	if err == nil || !strings.Contains(err.Error(), "cel") {
		t.Fatalf("expected CEL error, got %v", err)
	}

	// A non-bool expression is also rejected — otherwise evaluation would
	// always take the deny branch and operators would blame Nexspence.
	nonBool := &domain.ContentSelector{Name: "non-bool", Expression: `"hello"`}
	if err := svc.Create(ctx, nonBool); err == nil {
		t.Fatalf("expected rejection of non-bool expression")
	}
}

func TestContentSelector_EvaluateMatchAllowsAndMissDenies(t *testing.T) {
	svc, repo := newSvc(t)
	ctx := context.Background()

	sel := &domain.ContentSelector{
		Name:       "acme-maven",
		Expression: `format == "maven2" && path.startsWith("/com/acme/")`,
	}
	if err := svc.Create(ctx, sel); err != nil {
		t.Fatal(err)
	}
	repo.UserSelectors["alice"] = []string{sel.ID}

	// Happy path: format + path match.
	gated, allowed, err := svc.Evaluate(ctx, "alice", "maven-hosted", "maven2", "/com/acme/widget/1.0/widget.jar")
	if err != nil || !gated || !allowed {
		t.Fatalf("expected gated=true allowed=true, got gated=%v allowed=%v err=%v", gated, allowed, err)
	}

	// Same user, different path — gated still true but denied.
	gated, allowed, _ = svc.Evaluate(ctx, "alice", "maven-hosted", "maven2", "/org/other/pkg/1.0/pkg.jar")
	if !gated || allowed {
		t.Fatalf("expected gated=true allowed=false, got gated=%v allowed=%v", gated, allowed)
	}
}

func TestContentSelector_VariantB_NoSelectorPrivilegesPassThrough(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()

	// User bob has no selectors attached at all.
	gated, allowed, err := svc.Evaluate(ctx, "bob", "maven-hosted", "maven2", "/anything")
	if err != nil {
		t.Fatal(err)
	}
	if gated {
		t.Fatalf("Variant B broken: expected gated=false for user without selector privileges")
	}
	if !allowed {
		t.Fatalf("non-gated caller should be allowed, got allowed=false")
	}

	// Anonymous (userID="") must also pass through without any DB round-trip
	// visible to callers. The repo should not be consulted.
	gated, allowed, _ = svc.Evaluate(ctx, "", "maven-hosted", "maven2", "/anything")
	if gated || !allowed {
		t.Fatalf("anonymous should skip the gate; got gated=%v allowed=%v", gated, allowed)
	}
}

func TestContentSelector_MultipleSelectorsUseOrSemantics(t *testing.T) {
	svc, repo := newSvc(t)
	ctx := context.Background()

	maven := &domain.ContentSelector{Name: "mvn", Expression: `format == "maven2"`}
	docker := &domain.ContentSelector{Name: "dk", Expression: `format == "docker"`}
	if err := svc.Create(ctx, maven); err != nil {
		t.Fatal(err)
	}
	if err := svc.Create(ctx, docker); err != nil {
		t.Fatal(err)
	}
	repo.UserSelectors["alice"] = []string{maven.ID, docker.ID}

	// Either selector matching is enough — Nexus-compatible OR semantics.
	_, allowed, _ := svc.Evaluate(ctx, "alice", "r", "docker", "/v2/alpine/manifests/latest")
	if !allowed {
		t.Fatal("expected allow when docker selector matches")
	}
	_, allowed, _ = svc.Evaluate(ctx, "alice", "r", "maven2", "/com/acme/pkg.jar")
	if !allowed {
		t.Fatal("expected allow when maven selector matches")
	}
	_, allowed, _ = svc.Evaluate(ctx, "alice", "r", "npm", "/pkg/-/pkg-1.0.0.tgz")
	if allowed {
		t.Fatal("npm format must not match either selector")
	}
}

func TestContentSelector_UpdateInvalidatesProgramCache(t *testing.T) {
	svc, repo := newSvc(t)
	ctx := context.Background()

	sel := &domain.ContentSelector{Name: "mvn", Expression: `format == "maven2"`}
	if err := svc.Create(ctx, sel); err != nil {
		t.Fatal(err)
	}
	repo.UserSelectors["alice"] = []string{sel.ID}

	// First eval against the original expression — matches.
	if _, allowed, _ := svc.Evaluate(ctx, "alice", "r", "maven2", "/x"); !allowed {
		t.Fatal("precondition: expected maven2 allow")
	}

	// Swap expression to require docker. The cache must be invalidated so
	// the next Evaluate reflects the new expression.
	sel.Expression = `format == "docker"`
	if err := svc.Update(ctx, sel); err != nil {
		t.Fatal(err)
	}
	if _, allowed, _ := svc.Evaluate(ctx, "alice", "r", "maven2", "/x"); allowed {
		t.Fatal("cache not invalidated after Update — stale maven2 match")
	}
	if _, allowed, _ := svc.Evaluate(ctx, "alice", "r", "docker", "/v2/x"); !allowed {
		t.Fatal("updated expression should allow docker")
	}
}

func TestContentSelector_CRUDRoundtrip(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()

	sel := &domain.ContentSelector{Name: "a", Expression: `true`}
	if err := svc.Create(ctx, sel); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Get(ctx, sel.ID)
	if got == nil || got.Name != "a" {
		t.Fatalf("Get returned %+v", got)
	}

	list, _ := svc.List(ctx)
	if len(list) != 1 {
		t.Fatalf("expected 1 selector, got %d", len(list))
	}

	// Update: change description, keep valid expression.
	sel.Description = "updated"
	sel.Expression = `false`
	if err := svc.Update(ctx, sel); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.Get(ctx, sel.ID)
	if got.Description != "updated" || got.Expression != "false" {
		t.Fatalf("Update not persisted: %+v", got)
	}

	if err := svc.Delete(ctx, sel.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.Get(ctx, sel.ID)
	if got != nil {
		t.Fatal("selector still present after Delete")
	}
}

func TestContentSelector_AttachDetachPrivilege(t *testing.T) {
	svc, repo := newSvc(t)
	ctx := context.Background()

	sel := &domain.ContentSelector{Name: "mvn", Expression: `true`}
	if err := svc.Create(ctx, sel); err != nil {
		t.Fatal(err)
	}

	if err := svc.AttachToPrivilege(ctx, "nx-repository-view-maven", sel.ID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if got := repo.PrivilegeSelector["nx-repository-view-maven"]; got != sel.ID {
		t.Fatalf("expected privilege attached to %s, got %s", sel.ID, got)
	}

	// Detach removes the mapping.
	if err := svc.DetachFromPrivilege(ctx, "nx-repository-view-maven"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if _, ok := repo.PrivilegeSelector["nx-repository-view-maven"]; ok {
		t.Fatal("privilege still attached after detach")
	}

	// Attach to missing selector is rejected.
	if err := svc.AttachToPrivilege(ctx, "nx-other", "does-not-exist"); err == nil {
		t.Fatal("expected rejection of unknown selector id")
	}
}
