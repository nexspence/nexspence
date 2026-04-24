package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// StateCookiePayload is sealed into the "oidc_state" cookie between the
// authorization redirect and the IdP callback. Not secret data, but tamper-
// proofing is required so a client cannot extend TTL or swap state.
type StateCookiePayload struct {
	State        string `json:"s"`
	Nonce        string `json:"n"`
	CodeVerifier string `json:"v"`
	ReturnTo     string `json:"r"`
	IssuedAt     int64  `json:"t"`
}

// CookieSealer seals and opens StateCookiePayload with AES-256-GCM.
type CookieSealer struct {
	aead cipher.AEAD
}

// NewCookieSealer requires a 32-byte key (AES-256).
func NewCookieSealer(key []byte) (*CookieSealer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("cookie key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &CookieSealer{aead: aead}, nil
}

// Seal returns base64url(nonce||ciphertext) safe for cookie use.
func (s *CookieSealer) Seal(p StateCookiePayload) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := s.aead.Seal(nonce, nonce, raw, nil) // nonce prepended
	return base64.RawURLEncoding.EncodeToString(ct), nil
}

// Open reverses Seal, returning an error on tampered or expired-format data.
func (s *CookieSealer) Open(sealed string) (*StateCookiePayload, error) {
	buf, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return nil, err
	}
	ns := s.aead.NonceSize()
	if len(buf) < ns+s.aead.Overhead() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := buf[:ns], buf[ns:]
	raw, err := s.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, err
	}
	var p StateCookiePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
