//go:build integration

package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mintToken(t *testing.T, auth, body string) (int, string) {
	t.Helper()
	resp := authReq(t, http.MethodPost, "/api/v1/tokens", strings.NewReader(body), auth)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(raw, &tok)
	return resp.StatusCode, tok.Token
}

// GHSA-389h-4qc3-698w: a write-scoped token cannot delete, and must not be
// able to mint a token that can.
func TestTokenScopes_CannotMintEscalation_RealShape(t *testing.T) {
	createHostedRepo(t, "raw", "raw-scope", `{}`)
	admin := login(t, "admin", "admin123")
	code, body := putBody(t, admin, "/repository/raw-scope/f.txt", "f")
	require.Equal(t, http.StatusCreated, code, body)

	code, writeTok := mintToken(t, admin, `{"name":"ci-write","scopes":["write"]}`)
	require.Equal(t, http.StatusCreated, code)

	del := authReq(t, http.MethodDelete, "/repository/raw-scope/f.txt", nil, writeTok)
	del.Body.Close()
	require.Equal(t, http.StatusForbidden, del.StatusCode, "the write token cannot delete")

	code, _ = mintToken(t, writeTok, `{"name":"escalated"}`)
	assert.Equal(t, http.StatusForbidden, code, "unscoped")
	code, _ = mintToken(t, writeTok, `{"name":"escalated","scopes":["delete"]}`)
	assert.Equal(t, http.StatusForbidden, code, "delete")
	code, _ = mintToken(t, writeTok, `{"name":"narrower","scopes":["read"]}`)
	assert.Equal(t, http.StatusCreated, code, "a narrower token is fine")
}
