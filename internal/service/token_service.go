package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// TokenPrefix identifies tokens issued by Nexspence. Kept short so Basic-auth
// passwords are unlikely to collide, and long enough to be obvious in logs.
const TokenPrefix = "nxs_"

// TokenService manages user API tokens: creation, revocation, and
// authentication. The raw token value is only returned from Create; thereafter
// only its hash is stored.
type TokenService struct {
	tokens repository.UserTokenRepo
	users  repository.UserRepo
}

// NewTokenService constructs a service that issues and validates nxs_ API tokens.
func NewTokenService(tokens repository.UserTokenRepo, users repository.UserRepo) *TokenService {
	return &TokenService{tokens: tokens, users: users}
}

// hashToken returns the sha256 hex digest used as the lookup key. Because the
// raw tokens come from crypto/rand with 32 bytes of entropy a sha256 digest is
// sufficient — bcrypt per request would be prohibitively slow.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ListByUser returns all tokens owned by a user. TokenHash is not exposed.
func (s *TokenService) ListByUser(ctx context.Context, userID string) ([]domain.UserToken, error) {
	out, err := s.tokens.ListByUser(ctx, userID)
	if out == nil {
		out = []domain.UserToken{}
	}
	return out, err
}

// Create generates a new random token for user, persists its hash, and returns
// the populated record including the one-time plaintext value in Token.
func (s *TokenService) Create(ctx context.Context, userID, name string, scopes []string, expiresAt *time.Time) (*domain.UserToken, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: token name is required", ErrInvalidInput)
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, fmt.Errorf("%w: user %q", ErrNotFound, userID)
	}

	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, fmt.Errorf("generate random bytes: %w", err)
	}
	raw := TokenPrefix + hex.EncodeToString(buf[:])

	tok := &domain.UserToken{
		UserID:    userID,
		Name:      name,
		TokenHash: hashToken(raw),
		Scopes:    scopes,
		ExpiresAt: expiresAt,
	}
	if err := s.tokens.Create(ctx, tok); err != nil {
		return nil, err
	}
	tok.Token = raw
	tok.Username = u.Username
	return tok, nil
}

// Delete revokes a token. Callers must enforce ownership separately.
func (s *TokenService) Delete(ctx context.Context, id string) error {
	return s.tokens.Delete(ctx, id)
}

// Get returns a token by id (hash not exposed via handlers).
func (s *TokenService) Get(ctx context.Context, id string) (*domain.UserToken, error) {
	return s.tokens.Get(ctx, id)
}

// Authenticate validates a presented plaintext token, returns the owning user
// or an error. TouchLastUsed is called on success best-effort.
func (s *TokenService) Authenticate(ctx context.Context, raw string) (*domain.User, error) {
	if !strings.HasPrefix(raw, TokenPrefix) {
		return nil, fmt.Errorf("not an API token")
	}
	tok, err := s.tokens.GetByHash(ctx, hashToken(raw))
	if err != nil {
		return nil, err
	}
	if tok == nil {
		return nil, fmt.Errorf("unknown token")
	}
	if tok.ExpiresAt != nil && time.Now().After(*tok.ExpiresAt) {
		return nil, fmt.Errorf("token expired")
	}
	u, err := s.users.GetByID(ctx, tok.UserID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, fmt.Errorf("token owner not found")
	}
	if u.Status != domain.UserStatusActive {
		return nil, fmt.Errorf("user account disabled")
	}
	_ = s.tokens.TouchLastUsed(ctx, tok.ID)
	return u, nil
}
