package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func newTokenSvc(t *testing.T, users ...*domain.User) (*service.TokenService, *testutil.UserTokenRepo, *testutil.UserRepo) {
	t.Helper()
	userRepo := testutil.NewUserRepo(users...)
	tokenRepo := testutil.NewUserTokenRepo()
	svc := service.NewTokenService(tokenRepo, userRepo)
	return svc, tokenRepo, userRepo
}

func activeUser(id, username string) *domain.User {
	return &domain.User{
		ID:       id,
		Username: username,
		Status:   domain.UserStatusActive,
	}
}

func TestTokenService_Create_ReturnsPlaintextOnce(t *testing.T) {
	u := activeUser("u1", "alice")
	svc, repo, _ := newTokenSvc(t, u)

	tok, err := svc.Create(context.Background(), "u1", "ci-token", nil, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(tok.Token, service.TokenPrefix) {
		t.Fatalf("token missing prefix: %q", tok.Token)
	}
	if tok.TokenHash == tok.Token {
		t.Fatal("hash must differ from raw token")
	}
	stored, _ := repo.Get(context.Background(), tok.ID)
	if stored.TokenHash != tok.TokenHash {
		t.Fatal("hash mismatch in store")
	}
	if stored.Token != "" {
		t.Fatal("plaintext must not be persisted")
	}
}

func TestTokenService_Create_RequiresName(t *testing.T) {
	u := activeUser("u1", "alice")
	svc, _, _ := newTokenSvc(t, u)

	if _, err := svc.Create(context.Background(), "u1", "  ", nil, nil); err == nil {
		t.Fatal("expected error for blank name")
	}
}

func TestTokenService_Create_UnknownUser(t *testing.T) {
	svc, _, _ := newTokenSvc(t)
	if _, err := svc.Create(context.Background(), "missing", "x", nil, nil); err == nil {
		t.Fatal("expected error for unknown user")
	}
}

func TestTokenService_Authenticate_RoundTrip(t *testing.T) {
	u := activeUser("u1", "alice")
	svc, _, _ := newTokenSvc(t, u)

	tok, _ := svc.Create(context.Background(), "u1", "ci", nil, nil)

	got, err := svc.Authenticate(context.Background(), tok.Token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("want user %s got %s", u.ID, got.ID)
	}
}

func TestTokenService_Authenticate_RejectsNonPrefix(t *testing.T) {
	svc, _, _ := newTokenSvc(t)
	if _, err := svc.Authenticate(context.Background(), "some-jwt-without-prefix"); err == nil {
		t.Fatal("expected error for non-token string")
	}
}

func TestTokenService_Authenticate_UnknownToken(t *testing.T) {
	svc, _, _ := newTokenSvc(t)
	if _, err := svc.Authenticate(context.Background(), service.TokenPrefix+"deadbeef"); err == nil {
		t.Fatal("expected error for unknown token")
	}
}

func TestTokenService_Authenticate_ExpiredToken(t *testing.T) {
	u := activeUser("u1", "alice")
	svc, _, _ := newTokenSvc(t, u)

	past := time.Now().Add(-time.Hour)
	tok, _ := svc.Create(context.Background(), "u1", "expired", nil, &past)

	if _, err := svc.Authenticate(context.Background(), tok.Token); err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestTokenService_Authenticate_DisabledUser(t *testing.T) {
	u := &domain.User{ID: "u1", Username: "alice", Status: domain.UserStatusDisabled}
	svc, _, _ := newTokenSvc(t, u)

	tok, _ := svc.Create(context.Background(), "u1", "x", nil, nil)
	if _, err := svc.Authenticate(context.Background(), tok.Token); err == nil {
		t.Fatal("expected error for disabled user")
	}
}

func TestTokenService_Delete_RevokesToken(t *testing.T) {
	u := activeUser("u1", "alice")
	svc, _, _ := newTokenSvc(t, u)

	tok, _ := svc.Create(context.Background(), "u1", "x", nil, nil)
	if err := svc.Delete(context.Background(), tok.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.Authenticate(context.Background(), tok.Token); err == nil {
		t.Fatal("revoked token still authenticates")
	}
}

func TestTokenService_ListByUser_ScopedToOwner(t *testing.T) {
	alice := activeUser("u1", "alice")
	bob := activeUser("u2", "bob")
	svc, _, _ := newTokenSvc(t, alice, bob)

	_, _ = svc.Create(context.Background(), "u1", "a1", nil, nil)
	_, _ = svc.Create(context.Background(), "u1", "a2", nil, nil)
	_, _ = svc.Create(context.Background(), "u2", "b1", nil, nil)

	aliceList, _ := svc.ListByUser(context.Background(), "u1")
	bobList, _ := svc.ListByUser(context.Background(), "u2")
	if len(aliceList) != 2 {
		t.Fatalf("alice: want 2 tokens got %d", len(aliceList))
	}
	if len(bobList) != 1 {
		t.Fatalf("bob: want 1 token got %d", len(bobList))
	}
}
