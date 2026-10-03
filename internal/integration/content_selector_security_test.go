//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

func createdID(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	require.Less(t, resp.StatusCode, 300, "%s", raw)
	var out struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	require.NotEmpty(t, out.ID, "%s", raw)
	return out.ID
}

// selectorUser creates a user whose only access is one content-selector
// privilege with the given expression and actions, and returns its token.
func selectorUser(t *testing.T, admin, username, expression string, actions ...string) string {
	t.Helper()
	csBody, _ := json.Marshal(map[string]any{"name": username + "-cs", "expression": expression})
	csID := createdID(t, authReq(t, http.MethodPost, "/service/rest/v1/security/content-selectors", bytes.NewReader(csBody), admin))
	privBody, _ := json.Marshal(map[string]any{
		"name": username + "-priv", "type": "repository-content-selector",
		"contentSelectorId": csID, "attrs": map[string]any{"actions": actions},
	})
	privID := createdID(t, authReq(t, http.MethodPost, "/service/rest/v1/security/privileges", bytes.NewReader(privBody), admin))
	roleBody, _ := json.Marshal(map[string]any{"name": username + "-role", "privileges": []string{privID}})
	rID := createdID(t, authReq(t, http.MethodPost, "/service/rest/v1/security/roles", bytes.NewReader(roleBody), admin))
	userBody, _ := json.Marshal(map[string]any{
		"userId": username, "emailAddress": username + "@example.com", "password": "selPass123!",
		"status": "active", "roles": []string{rID},
	})
	resp := authReq(t, http.MethodPost, "/service/rest/v1/security/users", bytes.NewReader(userBody), admin)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode, "%s", raw)
	t.Cleanup(func() {
		for _, p := range []string{"/service/rest/v1/security/users/" + username, "/service/rest/v1/security/roles/" + rID,
			"/service/rest/v1/security/privileges/" + privID, "/service/rest/v1/security/content-selectors/" + csID} {
			d := authReq(t, http.MethodDelete, p, nil, admin)
			d.Body.Close()
		}
	})
	_, err := pgtest.Pool(t).Exec(context.Background(),
		`UPDATE users SET tokens_valid_after = now() - interval '1 minute' WHERE username = $1`, username)
	require.NoError(t, err)
	return login(t, username, "selPass123!")
}

// rawDo sends a request whose path is used exactly as written: a client such
// as `curl --path-as-is` does not resolve "..".
func rawDo(t *testing.T, method, p, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, server(t).URL+"/", strings.NewReader(body))
	require.NoError(t, err)
	req.URL.Opaque = p
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// GHSA-jcgv-hchv-g397: a ".." segment must not carry a /team-a/ privilege
// into /team-b/ — not to read, overwrite or delete.
func TestContentSelector_TraversalCannotLeaveThePathScope_RealShape(t *testing.T) {
	createHostedRepo(t, "raw", "raw-priv", `{}`)
	admin := login(t, "admin", "admin123")
	for p, content := range map[string]string{"/team-a/ok.txt": "ok", "/team-b/secret.txt": "secret"} {
		code, body := putBody(t, admin, "/repository/raw-priv"+p, content)
		require.Equal(t, http.StatusCreated, code, body)
	}
	bob := selectorUser(t, admin, "sel-bob", `repository == "raw-priv" && path.startsWith("/team-a/")`, "read", "browse", "write", "delete")

	code, _ := rawDo(t, http.MethodGet, "/repository/raw-priv/team-a/ok.txt", bob, "")
	assert.Equal(t, http.StatusOK, code, "own prefix")
	code, _ = rawDo(t, http.MethodGet, "/repository/raw-priv/team-b/secret.txt", bob, "")
	assert.Equal(t, http.StatusForbidden, code, "other prefix")

	for _, dots := range []string{"..", "%2e%2e", "%2E%2E"} {
		target := "/repository/raw-priv/team-a/" + dots + "/team-b/secret.txt"
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			code, body := rawDo(t, method, target, bob, "overwritten")
			assert.Equal(t, http.StatusBadRequest, code, "%s %s: %s", method, target, body)
		}
	}
	code, body := rawDo(t, http.MethodGet, "/repository/raw-priv/team-b/secret.txt", admin, "")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "secret", body, fmt.Sprintf("the secret is intact"))
}

// GHSA-vpvp-9379-86x6: an exclusion must not grant what it excludes.
func TestContentSelector_NegationGrantsNothing_RealShape(t *testing.T) {
	createHostedRepo(t, "raw", "raw-neg", `{}`)
	admin := login(t, "admin", "admin123")
	code, body := putBody(t, admin, "/repository/raw-neg/a.txt", "a")
	require.Equal(t, http.StatusCreated, code, body)

	carol := selectorUser(t, admin, "sel-carol", `repository != "raw-neg"`, "read", "browse")
	code, _ = rawDo(t, http.MethodGet, "/repository/raw-neg/a.txt", carol, "")
	assert.Equal(t, http.StatusForbidden, code)

	dave := selectorUser(t, admin, "sel-dave", `repository == "raw-neg" && !path.startsWith("/a")`, "read", "browse")
	code, _ = rawDo(t, http.MethodGet, "/repository/raw-neg/a.txt", dave, "")
	assert.Equal(t, http.StatusForbidden, code)

	erin := selectorUser(t, admin, "sel-erin", `repository == "raw-neg"`, "read", "browse")
	code, _ = rawDo(t, http.MethodGet, "/repository/raw-neg/a.txt", erin, "")
	assert.Equal(t, http.StatusOK, code, "a positive selector still grants")
}
