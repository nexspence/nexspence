package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// GHSA-389h-4qc3-698w: a scoped API token may mint only tokens within its own
// scopes — never an unscoped one, which carries the account's full power.
func TestTokenAPI_ScopedTokenCannotMintWiderToken(t *testing.T) {
	alice := activeUser("alice", "pw")
	userSvc, tokenSvc := newTokenStack(alice)
	r := buildTokenAPIRouter(userSvc, tokenSvc)

	create := func(auth, body string) (int, domain.UserToken) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/user-tokens", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+auth)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var tok domain.UserToken
		_ = json.Unmarshal(w.Body.Bytes(), &tok)
		return w.Code, tok
	}

	code, writeTok := create(bearerToken(userSvc, "alice"), `{"name":"ci","scopes":["write"]}`)
	require.Equal(t, http.StatusCreated, code)
	require.NotEmpty(t, writeTok.Token)

	for _, body := range []string{
		`{"name":"x"}`,
		`{"name":"x","scopes":[]}`,
		`{"name":"x","scopes":["delete"]}`,
		`{"name":"x","scopes":["read","delete"]}`,
	} {
		code, _ := create(writeTok.Token, body)
		assert.Equal(t, http.StatusForbidden, code, body)
	}
	for _, body := range []string{
		`{"name":"x","scopes":["read"]}`,
		`{"name":"x","scopes":["write"]}`,
	} {
		code, _ := create(writeTok.Token, body)
		assert.Equal(t, http.StatusCreated, code, body)
	}

	// A session (JWT) is not capped by scopes.
	code, _ = create(bearerToken(userSvc, "alice"), `{"name":"full"}`)
	assert.Equal(t, http.StatusCreated, code)
}
