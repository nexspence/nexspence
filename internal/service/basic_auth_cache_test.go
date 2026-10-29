package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

func TestBasicAuthVerificationCacheExpiresAndDoesNotStoreCredentialsInKeys(t *testing.T) {
	now := time.Now()
	cache := newBasicAuthVerificationCacheWithKey([]byte("test-only-secret"), time.Minute, 2, func() time.Time { return now })
	user := &domain.User{Username: "alice", PasswordHash: "bcrypt-hash", Status: domain.UserStatusActive}
	cache.remember("alice", "plain-password", user)

	key := cache.credentialKey("alice", "plain-password")
	require.NotContains(t, key, "alice")
	require.NotContains(t, key, "plain-password")
	require.NotEqual(t, cache.credentialKey("alice\x00x", "pw"), cache.credentialKey("alice", "x\x00pw"))
	require.True(t, cache.valid("alice", "plain-password", user))

	now = now.Add(time.Minute)
	require.False(t, cache.valid("alice", "plain-password", user))
	require.Empty(t, cache.entries)
}

func TestBasicAuthVerificationCachePurgesEntriesWhenUserStateChanges(t *testing.T) {
	cache := newBasicAuthVerificationCacheWithKey([]byte("test-only-secret"), time.Minute, 4, time.Now)
	user := &domain.User{Username: "alice", PasswordHash: "hash-v1", Status: domain.UserStatusActive}
	cache.remember("alice", "old-password", user)
	cache.remember("alice", "another-password", user)

	user.PasswordHash = "hash-v2"
	require.False(t, cache.valid("alice", "new-password", user))
	require.Empty(t, cache.entries, "all successful verifications for the changed user must be discarded")
	require.Empty(t, cache.byUser)
}

func TestBasicAuthVerificationCacheDoesNotExceedConfiguredCapacity(t *testing.T) {
	cache := newBasicAuthVerificationCacheWithKey([]byte("test-only-secret"), time.Minute, 1, time.Now)
	cache.remember("alice", "password-a", &domain.User{Username: "alice", PasswordHash: "hash-a", Status: domain.UserStatusActive})
	cache.remember("bob", "password-b", &domain.User{Username: "bob", PasswordHash: "hash-b", Status: domain.UserStatusActive})

	require.Len(t, cache.entries, 1)
}
