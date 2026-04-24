package auth

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i + 1)
	}
	return b
}

func TestCookieSealer_SealOpen_RoundTrip(t *testing.T) {
	s, err := NewCookieSealer(testKey())
	require.NoError(t, err)

	in := StateCookiePayload{
		State:        "abc",
		Nonce:        "xyz",
		CodeVerifier: "v123",
		ReturnTo:     "/foo",
		IssuedAt:     time.Now().Unix(),
	}
	sealed, err := s.Seal(in)
	require.NoError(t, err)
	assert.NotEmpty(t, sealed)

	out, err := s.Open(sealed)
	require.NoError(t, err)
	assert.Equal(t, in, *out)
}

func TestCookieSealer_TamperedCiphertext_Fails(t *testing.T) {
	s, err := NewCookieSealer(testKey())
	require.NoError(t, err)
	sealed, err := s.Seal(StateCookiePayload{State: "abc"})
	require.NoError(t, err)

	// Flip the last few characters.
	tampered := sealed[:len(sealed)-4] + "XXXX"
	_, err = s.Open(tampered)
	require.Error(t, err)
}

func TestCookieSealer_WrongKey_Fails(t *testing.T) {
	s1, err := NewCookieSealer(testKey())
	require.NoError(t, err)
	sealed, err := s1.Seal(StateCookiePayload{State: "abc"})
	require.NoError(t, err)

	otherKey := make([]byte, 32)
	for i := range otherKey {
		otherKey[i] = byte(255 - i)
	}
	s2, err := NewCookieSealer(otherKey)
	require.NoError(t, err)
	_, err = s2.Open(sealed)
	require.Error(t, err)
}

func TestNewCookieSealer_WrongKeySize_Fails(t *testing.T) {
	_, err := NewCookieSealer([]byte{1, 2, 3})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "32 bytes")
}

func TestCookieSealer_OpenGarbage_Fails(t *testing.T) {
	s, err := NewCookieSealer(testKey())
	require.NoError(t, err)

	// Not valid base64.
	_, err = s.Open("not-valid-base64-@@@")
	require.Error(t, err)

	// Valid base64 but too short to contain nonce + tag.
	_, err = s.Open(base64.RawURLEncoding.EncodeToString([]byte{1, 2, 3}))
	require.Error(t, err)
}
