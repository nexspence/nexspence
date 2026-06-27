package main

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func testBootstrapCfg() config.BootstrapConfig {
	return config.BootstrapConfig{
		AdminUsername:  "admin",
		AdminPassword:  "admin123",
		AdminEmail:     "admin@example.com",
		AdminFirstName: "Admin",
	}
}

func testAuthSvc() *auth.Service {
	// bcrypt cost 4 (MinCost) keeps these tests fast.
	return auth.NewService("test-secret-test-secret-test-1234", 8, 4)
}

// Regression: a fresh deploy seeds the admin row with a placeholder hash (NOT
// admin123). Bootstrap must apply the configured password so admin/admin123 can
// log in. Previously bootstrap saw "admin already exists" and left the broken
// placeholder in place.
func TestEnsureBootstrapAdmin_SeedPlaceholder_PasswordApplied(t *testing.T) {
	authSvc := testAuthSvc()
	users := testutil.NewUserRepo(&domain.User{
		ID:           "u-admin",
		Username:     "admin",
		PasswordHash: seedPlaceholderAdminHash,
		Status:       domain.UserStatusActive,
		Source:       domain.UserSourceLocal,
	})
	log := logger.New("error", "json")

	if err := ensureBootstrapAdmin(context.Background(), users, testutil.NewRoleRepo(), authSvc, testBootstrapCfg(), log); err != nil {
		t.Fatalf("ensureBootstrapAdmin: %v", err)
	}

	got, _ := users.Get(context.Background(), "admin")
	if got.PasswordHash == seedPlaceholderAdminHash {
		t.Fatal("password hash still the seed placeholder; configured password was not applied")
	}
	if err := authSvc.CheckPassword(got.PasswordHash, "admin123"); err != nil {
		t.Fatalf("admin123 does not verify against the stored hash: %v", err)
	}
}

// A genuinely changed admin password must never be clobbered by bootstrap
// (preserves the #36 hardening intent: rotate via API, not config+restart).
func TestEnsureBootstrapAdmin_ChangedPassword_NotModified(t *testing.T) {
	authSvc := testAuthSvc()
	custom, err := authSvc.HashPassword("operator-secret")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := testutil.NewUserRepo(&domain.User{
		ID:           "u-admin",
		Username:     "admin",
		PasswordHash: custom,
		Status:       domain.UserStatusActive,
	})
	log := logger.New("error", "json")

	if err := ensureBootstrapAdmin(context.Background(), users, testutil.NewRoleRepo(), authSvc, testBootstrapCfg(), log); err != nil {
		t.Fatalf("ensureBootstrapAdmin: %v", err)
	}

	got, _ := users.Get(context.Background(), "admin")
	if got.PasswordHash != custom {
		t.Fatal("changed admin password was overwritten by bootstrap")
	}
	if err := authSvc.CheckPassword(got.PasswordHash, "admin123"); err == nil {
		t.Fatal("admin123 must NOT verify against a changed password")
	}
}

// No admin row at all: bootstrap creates one with the configured password and
// the nx-admin role. (mock Get returns ErrNotFound, which must be treated as
// "not found", not a hard error.)
func TestEnsureBootstrapAdmin_Missing_Created(t *testing.T) {
	authSvc := testAuthSvc()
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo(&domain.Role{ID: "r-admin", Name: "nx-admin"})
	log := logger.New("error", "json")

	if err := ensureBootstrapAdmin(context.Background(), users, roles, authSvc, testBootstrapCfg(), log); err != nil {
		t.Fatalf("ensureBootstrapAdmin: %v", err)
	}

	got, err := users.Get(context.Background(), "admin")
	if err != nil || got == nil {
		t.Fatalf("admin not created: err=%v", err)
	}
	if err := authSvc.CheckPassword(got.PasswordHash, "admin123"); err != nil {
		t.Fatalf("created admin password does not verify: %v", err)
	}
}
