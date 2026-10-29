package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sync"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

const (
	basicAuthCacheTTL        = time.Minute
	basicAuthCacheMaxEntries = 4096
)

type basicAuthCacheEntry struct {
	credentialKey    string
	userKey          string
	passwordHash     string
	status           domain.UserStatus
	tokensValidAfter time.Time
	expiresAt        time.Time
}

type basicAuthVerificationCache struct {
	mu      sync.Mutex
	key     []byte
	ttl     time.Duration
	maxSize int
	now     func() time.Time
	entries map[string]basicAuthCacheEntry
	byUser  map[string]map[string]struct{}
}

func newBasicAuthVerificationCache() *basicAuthVerificationCache {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil
	}
	return newBasicAuthVerificationCacheWithKey(key, basicAuthCacheTTL, basicAuthCacheMaxEntries, time.Now)
}

func newBasicAuthVerificationCacheWithKey(key []byte, ttl time.Duration, maxSize int, now func() time.Time) *basicAuthVerificationCache {
	return &basicAuthVerificationCache{
		key: append([]byte(nil), key...), ttl: ttl, maxSize: maxSize, now: now,
		entries: make(map[string]basicAuthCacheEntry), byUser: make(map[string]map[string]struct{}),
	}
}

func (c *basicAuthVerificationCache) credentialKey(username, password string) string {
	mac := hmac.New(sha256.New, c.key)
	mac.Write([]byte("credential\x00"))
	writeHMACField(mac, username)
	writeHMACField(mac, password)
	return hex.EncodeToString(mac.Sum(nil))
}

func (c *basicAuthVerificationCache) userKey(username string) string {
	mac := hmac.New(sha256.New, c.key)
	mac.Write([]byte("user\x00"))
	writeHMACField(mac, username)
	return hex.EncodeToString(mac.Sum(nil))
}

func writeHMACField(mac interface{ Write([]byte) (int, error) }, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = mac.Write(length[:])
	_, _ = mac.Write([]byte(value))
}

func (c *basicAuthVerificationCache) valid(username, password string, user *domain.User) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	userKey := c.userKey(username)
	for key := range c.byUser[userKey] {
		entry, exists := c.entries[key]
		if !exists || !entry.expiresAt.After(now) || !sameBasicAuthUserState(entry, user) {
			c.deleteLocked(key, entry.userKey)
		}
	}
	entry, exists := c.entries[c.credentialKey(username, password)]
	return exists && entry.expiresAt.After(now) && sameBasicAuthUserState(entry, user)
}

func (c *basicAuthVerificationCache) remember(username, password string, user *domain.User) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for key, entry := range c.entries {
		if !entry.expiresAt.After(now) {
			c.deleteLocked(key, entry.userKey)
		}
	}
	key := c.credentialKey(username, password)
	userKey := c.userKey(username)
	if old, exists := c.entries[key]; exists {
		c.deleteLocked(key, old.userKey)
	}
	if len(c.entries) >= c.maxSize {
		return
	}
	c.entries[key] = basicAuthCacheEntry{
		credentialKey: key, userKey: userKey, passwordHash: user.PasswordHash,
		status: user.Status, tokensValidAfter: user.TokensValidAfter, expiresAt: now.Add(c.ttl),
	}
	if c.byUser[userKey] == nil {
		c.byUser[userKey] = make(map[string]struct{})
	}
	c.byUser[userKey][key] = struct{}{}
}

func (c *basicAuthVerificationCache) deleteLocked(key, userKey string) {
	delete(c.entries, key)
	if keys := c.byUser[userKey]; keys != nil {
		delete(keys, key)
		if len(keys) == 0 {
			delete(c.byUser, userKey)
		}
	}
}

func sameBasicAuthUserState(entry basicAuthCacheEntry, user *domain.User) bool {
	return entry.passwordHash == user.PasswordHash && entry.status == user.Status && entry.tokensValidAfter.Equal(user.TokensValidAfter)
}
