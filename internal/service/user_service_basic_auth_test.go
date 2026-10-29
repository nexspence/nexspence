package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func TestAuthenticateBasicReusesSuccessfulVerificationAndReturnsCurrentRoles(t *testing.T) {
	user := &domain.User{ID: "user-1", Username: "alice", PasswordHash: "hash-v1", Status: domain.UserStatusActive, Source: domain.UserSourceLocal, Roles: []string{"reader"}}
	repo := testutil.NewUserRepo(user)
	svc := NewUserService(repo, testutil.NewRoleRepo(), auth.NewService("test-secret-32-chars-long-here!!", 1, 4), zap.NewNop().Sugar())
	checks := 0
	svc.checkPassword = func(hash, password string) error {
		checks++
		if hash != "hash-v1" || password != "secret" {
			return errors.New("invalid credentials")
		}
		return nil
	}

	first, err := svc.AuthenticateBasic(context.Background(), "alice", "secret")
	require.NoError(t, err)
	require.Equal(t, []string{"reader"}, first.Roles)

	user.Roles = []string{"publisher"}
	second, err := svc.AuthenticateBasic(context.Background(), "alice", "secret")
	require.NoError(t, err)
	require.Equal(t, []string{"publisher"}, second.Roles, "authorization data must come from the current user record")
	require.Equal(t, 1, checks, "bcrypt-equivalent verification should run once while the cache entry is valid")
}

func TestAuthenticateBasicDoesNotCacheFailedCredentials(t *testing.T) {
	user := &domain.User{ID: "user-1", Username: "alice", PasswordHash: "hash-v1", Status: domain.UserStatusActive, Source: domain.UserSourceLocal}
	svc := NewUserService(testutil.NewUserRepo(user), testutil.NewRoleRepo(), auth.NewService("test-secret-32-chars-long-here!!", 1, 4), zap.NewNop().Sugar())
	checks := 0
	svc.checkPassword = func(_, _ string) error {
		checks++
		return errors.New("invalid credentials")
	}

	for range 2 {
		_, err := svc.AuthenticateBasic(context.Background(), "alice", "wrong")
		require.Error(t, err)
	}
	require.Equal(t, 2, checks)
}

func TestAuthenticateBasicInvalidatesVerificationWhenCredentialStateChanges(t *testing.T) {
	user := &domain.User{ID: "user-1", Username: "alice", PasswordHash: "hash-v1", Status: domain.UserStatusActive, Source: domain.UserSourceLocal}
	svc := NewUserService(testutil.NewUserRepo(user), testutil.NewRoleRepo(), auth.NewService("test-secret-32-chars-long-here!!", 1, 4), zap.NewNop().Sugar())
	checks := 0
	svc.checkPassword = func(hash, password string) error {
		checks++
		if (hash == "hash-v1" && password == "old") || (hash == "hash-v2" && password == "new") {
			return nil
		}
		return errors.New("invalid credentials")
	}

	_, err := svc.AuthenticateBasic(context.Background(), "alice", "old")
	require.NoError(t, err)
	user.PasswordHash = "hash-v2"
	_, err = svc.AuthenticateBasic(context.Background(), "alice", "new")
	require.NoError(t, err)
	require.Equal(t, 2, checks, "a changed password hash must not reuse a prior verification")
}

func TestAuthenticateBasicInvalidatesVerificationWhenRevocationStateChanges(t *testing.T) {
	user := &domain.User{ID: "user-1", Username: "alice", PasswordHash: "hash-v1", Status: domain.UserStatusActive, Source: domain.UserSourceLocal}
	svc := NewUserService(testutil.NewUserRepo(user), testutil.NewRoleRepo(), auth.NewService("test-secret-32-chars-long-here!!", 1, 4), zap.NewNop().Sugar())
	checks := 0
	svc.checkPassword = func(_, _ string) error { checks++; return nil }

	_, err := svc.AuthenticateBasic(context.Background(), "alice", "secret")
	require.NoError(t, err)
	user.TokensValidAfter = user.TokensValidAfter.Add(time.Second)
	_, err = svc.AuthenticateBasic(context.Background(), "alice", "secret")
	require.NoError(t, err)
	require.Equal(t, 2, checks, "revocation state changes invalidate cached verification")

	user.Status = domain.UserStatusDisabled
	_, err = svc.AuthenticateBasic(context.Background(), "alice", "secret")
	require.Error(t, err)
	require.Equal(t, 2, checks, "disabled accounts are rejected without using cached verification")
}
