package auth_test

import (
	"testing"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSvc() *auth.Service {
	// bcryptCost=4 is minimum — fast in tests, not for production
	return auth.NewService("testsecret-long-enough-32bytes!!", 1, 4)
}

func TestGenerateToken_ValidateClaims(t *testing.T) {
	s := newSvc()
	token, err := s.GenerateToken("uid-1", "alice", []string{"admin", "deployer"})
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := s.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "uid-1", claims.UserID)
	assert.Equal(t, "alice", claims.Username)
	assert.ElementsMatch(t, []string{"admin", "deployer"}, claims.Roles)
}

func TestGenerateToken_NoRoles(t *testing.T) {
	s := newSvc()
	token, err := s.GenerateToken("uid-2", "bob", nil)
	require.NoError(t, err)

	claims, err := s.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "bob", claims.Username)
}

func TestValidateToken_Malformed(t *testing.T) {
	s := newSvc()
	_, err := s.ValidateToken("this.is.not.a.jwt")
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestValidateToken_EmptyString(t *testing.T) {
	s := newSvc()
	_, err := s.ValidateToken("")
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestValidateToken_WrongSecret(t *testing.T) {
	s1 := auth.NewService("secret-alpha-padding-32bytes!!", 1, 4)
	s2 := auth.NewService("secret-beta--padding-32bytes!!", 1, 4)

	token, err := s1.GenerateToken("uid-3", "carol", []string{"viewer"})
	require.NoError(t, err)

	_, err = s2.ValidateToken(token)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestHashPassword_CheckPassword(t *testing.T) {
	s := newSvc()
	hash, err := s.HashPassword("super-secret-pass")
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
	// Hashes are different even for the same input (bcrypt salt)
	hash2, _ := s.HashPassword("super-secret-pass")
	assert.NotEqual(t, hash, hash2)

	require.NoError(t, s.CheckPassword(hash, "super-secret-pass"))
}

func TestCheckPassword_WrongPassword(t *testing.T) {
	s := newSvc()
	hash, _ := s.HashPassword("correct-horse")
	err := s.CheckPassword(hash, "wrong-guess")
	assert.ErrorIs(t, err, auth.ErrBadPassword)
}

func TestCheckPassword_EmptyPassword(t *testing.T) {
	s := newSvc()
	hash, _ := s.HashPassword("notempty")
	err := s.CheckPassword(hash, "")
	assert.ErrorIs(t, err, auth.ErrBadPassword)
}
