package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/config"
)

// fakeIdP is a minimal OIDC provider stub exposing discovery, JWKS, and /token.
type fakeIdP struct {
	server      *httptest.Server
	key         *rsa.PrivateKey
	kid         string
	claims      map[string]any
	issuedNonce string
	issuedAud   string
	issuedExp   int64
	signingKey  *rsa.PrivateKey // override for wrong-sig test
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	f := &fakeIdP{key: k, kid: "test-kid"}
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		base := f.server.URL
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                base,
			"authorization_endpoint":                base + "/authorize",
			"token_endpoint":                        base + "/token",
			"jwks_uri":                              base + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		n := base64.RawURLEncoding.EncodeToString(k.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes())
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA", "use": "sig", "alg": "RS256",
				"kid": f.kid, "n": n, "e": e,
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		signer := f.signingKey
		if signer == nil {
			signer = k
		}
		claims := jwt.MapClaims{
			"iss":   f.server.URL,
			"aud":   f.issuedAud,
			"exp":   f.issuedExp,
			"iat":   time.Now().Unix(),
			"sub":   "u-123",
			"nonce": f.issuedNonce,
		}
		for k, v := range f.claims {
			claims[k] = v
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = f.kid
		signed, _ := tok.SignedString(signer)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-placeholder",
			"token_type":   "Bearer",
			"id_token":     signed,
		})
	})

	f.server = httptest.NewServer(mux)
	return f
}

func (f *fakeIdP) close() { f.server.Close() }

func baseOIDCTestCfg(idp *fakeIdP) config.OIDCConfig {
	return config.OIDCConfig{
		Enabled:            true,
		Issuer:             idp.server.URL,
		ClientID:           "client-xyz",
		ClientSecret:       "secret",
		RedirectURL:        "https://app/cb",
		Scopes:             []string{"openid", "profile", "email", "groups"},
		UsernameClaim:      "preferred_username",
		EmailClaim:         "email",
		NameClaim:          "name",
		GroupsClaim:        "groups",
		AllowedSkewSeconds: 60,
	}
}

func TestOIDCService_HappyPath(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()

	nonce := "n-ok"
	idp.issuedNonce = nonce
	idp.issuedAud = "client-xyz"
	idp.issuedExp = time.Now().Add(5 * time.Minute).Unix()
	idp.claims = map[string]any{
		"preferred_username": "alice",
		"email":              "alice@example.com",
		"name":               "Alice Example",
		"given_name":         "Alice",
		"family_name":        "Example",
		"groups":             []any{"developers", "nexspence-admins"},
	}

	svc, err := NewOIDCService(context.Background(), baseOIDCTestCfg(idp))
	require.NoError(t, err)

	claims, _, err := svc.ExchangeAndVerify(context.Background(), "code-abc", "verifier-xyz", nonce)
	require.NoError(t, err)

	assert.Equal(t, "alice", claims.Username)
	assert.Equal(t, "alice@example.com", claims.Email)
	assert.Equal(t, "Alice", claims.FirstName)
	assert.Equal(t, "Example", claims.LastName)
	assert.ElementsMatch(t, []string{"developers", "nexspence-admins"}, claims.Groups)
	assert.Equal(t, "u-123", claims.Subject)
}

func TestOIDCService_NonceMismatch(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	idp.issuedNonce = "n-real"
	idp.issuedAud = "client-xyz"
	idp.issuedExp = time.Now().Add(5 * time.Minute).Unix()

	svc, err := NewOIDCService(context.Background(), baseOIDCTestCfg(idp))
	require.NoError(t, err)
	_, _, err = svc.ExchangeAndVerify(context.Background(), "code", "verifier", "n-expected")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOIDCVerification)
}

func TestOIDCService_ExpiredToken(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	idp.issuedNonce = "n"
	idp.issuedAud = "client-xyz"
	idp.issuedExp = time.Now().Add(-5 * time.Minute).Unix()

	svc, err := NewOIDCService(context.Background(), baseOIDCTestCfg(idp))
	require.NoError(t, err)
	_, _, err = svc.ExchangeAndVerify(context.Background(), "code", "verifier", "n")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOIDCVerification)
}

func TestOIDCService_WrongAudience(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	idp.issuedNonce = "n"
	idp.issuedAud = "some-other-client"
	idp.issuedExp = time.Now().Add(5 * time.Minute).Unix()

	svc, err := NewOIDCService(context.Background(), baseOIDCTestCfg(idp))
	require.NoError(t, err)
	_, _, err = svc.ExchangeAndVerify(context.Background(), "code", "verifier", "n")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOIDCVerification)
}

func TestOIDCService_WrongSignature(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	idp.signingKey = other
	idp.issuedNonce = "n"
	idp.issuedAud = "client-xyz"
	idp.issuedExp = time.Now().Add(5 * time.Minute).Unix()

	svc, err := NewOIDCService(context.Background(), baseOIDCTestCfg(idp))
	require.NoError(t, err)
	_, _, err = svc.ExchangeAndVerify(context.Background(), "code", "verifier", "n")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOIDCVerification)
}

func TestOIDCService_ClaimCustomization_GooglePattern(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	idp.issuedNonce = "n"
	idp.issuedAud = "client-xyz"
	idp.issuedExp = time.Now().Add(5 * time.Minute).Unix()
	idp.claims = map[string]any{"email": "bob@example.com"}

	cfg := baseOIDCTestCfg(idp)
	cfg.UsernameClaim = "email" // Google has no preferred_username
	svc, err := NewOIDCService(context.Background(), cfg)
	require.NoError(t, err)
	claims, _, err := svc.ExchangeAndVerify(context.Background(), "code", "verifier", "n")
	require.NoError(t, err)
	assert.Equal(t, "bob@example.com", claims.Username)
}

func TestOIDCService_TestConnection_OK(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	svc, err := NewOIDCService(context.Background(), baseOIDCTestCfg(idp))
	require.NoError(t, err)
	require.NoError(t, svc.TestConnection(context.Background()))
}

func TestNewOIDCService_UnreachableIssuer_Fails(t *testing.T) {
	cfg := config.OIDCConfig{Issuer: "http://127.0.0.1:1/nowhere", AllowedSkewSeconds: 60}
	_, err := NewOIDCService(context.Background(), cfg)
	require.Error(t, err)
}
