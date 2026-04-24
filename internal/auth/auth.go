// Package auth handles JWT token creation/validation and password hashing.
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidToken = errors.New("invalid or expired token")
	ErrBadPassword  = errors.New("incorrect password")
)

// Claims is the JWT payload.
type Claims struct {
	UserID     string   `json:"uid"`
	Username   string   `json:"sub"`
	Roles      []string `json:"roles"`
	AuthMethod string   `json:"auth_method,omitempty"`
	jwt.RegisteredClaims
}

// Service handles auth operations.
type Service struct {
	secret     []byte
	expiryHrs  int
	bcryptCost int
}

func NewService(secret string, expiryHrs, bcryptCost int) *Service {
	return &Service{
		secret:     []byte(secret),
		expiryHrs:  expiryHrs,
		bcryptCost: bcryptCost,
	}
}

// GenerateToken creates a signed JWT for the given user.
func (s *Service) GenerateToken(userID, username string, roles []string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   userID,
		Username: username,
		Roles:    roles,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(s.expiryHrs) * time.Hour)),
			Issuer:    "nexspence",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

// GenerateTokenWithMethod is like GenerateToken but embeds an auth_method claim.
// Used by OIDC login to let the frontend detect SSO sessions.
func (s *Service) GenerateTokenWithMethod(userID, username string, roles []string, method string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:     userID,
		Username:   username,
		Roles:      roles,
		AuthMethod: method,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(s.expiryHrs) * time.Hour)),
			Issuer:    "nexspence",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.secret)
}

// ValidateToken parses and validates a JWT, returning its claims.
func (s *Service) ValidateToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// HashPassword returns a bcrypt hash of the plaintext password.
func (s *Service) HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), s.bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CheckPassword compares a plaintext password against a bcrypt hash.
func (s *Service) CheckPassword(hash, plain string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)); err != nil {
		return ErrBadPassword
	}
	return nil
}
