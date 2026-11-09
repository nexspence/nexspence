package main

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Retiring the bootstrap account by deleting it did not retire it: the row
// was recreated on the next start with the configured password — which, on
// an instance that never changed it, is the shipped admin/admin123. Anyone
// who knows the default gets the account, and with it nx-admin, back.
//
// An instance that still has other users is not a fresh install, so a missing
// admin row there means the account was removed on purpose and must stay
// removed.
func TestEnsureBootstrapAdmin_DeletedAdmin_NotRecreated(t *testing.T) {
	authSvc := testAuthSvc()
	users := testutil.NewUserRepo(&domain.User{
		ID: "u-other", Username: "deploy", Status: domain.UserStatusActive,
	})
	roles := testutil.NewRoleRepo(&domain.Role{ID: "r-admin", Name: "nx-admin"})
	log := logger.New("error", "json")

	if err := ensureBootstrapAdmin(context.Background(), users, roles, authSvc, testBootstrapCfg(), log); err != nil {
		t.Fatalf("ensureBootstrapAdmin: %v", err)
	}

	if got, err := users.Get(context.Background(), "admin"); err == nil && got != nil {
		t.Fatal("a deleted bootstrap admin must not be recreated on the next start")
	}
}

// The way back for an operator who really does want the account again: ask
// for it explicitly.
func TestEnsureBootstrapAdmin_RecreateRequested_Recreated(t *testing.T) {
	authSvc := testAuthSvc()
	users := testutil.NewUserRepo(&domain.User{
		ID: "u-other", Username: "deploy", Status: domain.UserStatusActive,
	})
	roles := testutil.NewRoleRepo(&domain.Role{ID: "r-admin", Name: "nx-admin"})
	log := logger.New("error", "json")

	b := testBootstrapCfg()
	b.Recreate = true
	if err := ensureBootstrapAdmin(context.Background(), users, roles, authSvc, b, log); err != nil {
		t.Fatalf("ensureBootstrapAdmin: %v", err)
	}

	got, err := users.Get(context.Background(), "admin")
	if err != nil || got == nil {
		t.Fatalf("admin not recreated: err=%v", err)
	}
	if err := authSvc.CheckPassword(got.PasswordHash, "admin123"); err != nil {
		t.Fatalf("recreated admin password does not verify: %v", err)
	}
}

// A fresh install — no users at all — still gets its admin account.
func TestEnsureBootstrapAdmin_FreshInstall_Created(t *testing.T) {
	authSvc := testAuthSvc()
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo(&domain.Role{ID: "r-admin", Name: "nx-admin"})
	log := logger.New("error", "json")

	if err := ensureBootstrapAdmin(context.Background(), users, roles, authSvc, testBootstrapCfg(), log); err != nil {
		t.Fatalf("ensureBootstrapAdmin: %v", err)
	}

	got, err := users.Get(context.Background(), "admin")
	if err != nil || got == nil {
		t.Fatalf("admin not created on a fresh install: err=%v", err)
	}
}
