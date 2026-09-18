//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// adminOnlyReadPaths are the read endpoints #498 found reachable by any
// authenticated user. Each has a write-side sibling that always required
// nx-admin; the read side must match.
var adminOnlyReadPaths = []string{
	"/api/v1/metrics",
	"/api/v1/metrics/history",
	"/api/v1/metrics/repos",
	"/service/rest/v1/security/roles",
	"/service/rest/v1/security/privileges",
	"/service/rest/v1/security/roles/nx-admin/privileges",
	"/api/v1/security/privilege-role-map",
	"/service/rest/v1/security/content-selectors",
	"/api/v1/replication/rules",
}

// roleID resolves a seeded role's ID by name; POST /users takes role IDs.
func roleID(t *testing.T, admin, name string) string {
	t.Helper()
	resp := authReq(t, http.MethodGet, "/service/rest/v1/security/roles", nil, admin)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var roles []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&roles))
	for _, r := range roles {
		if r.Name == name {
			return r.ID
		}
	}
	t.Fatalf("role %q not seeded", name)
	return ""
}

// loginAsDeveloper creates a throwaway nx-developer account and returns its
// Bearer token; the account is removed when the test ends.
func loginAsDeveloper(t *testing.T, admin, username string) string {
	t.Helper()
	body := `{"userId":"` + username + `","emailAddress":"` + username + `@example.com","password":"devPass123!","status":"active","roles":["` + roleID(t, admin, "nx-developer") + `"]}`
	createResp := authReq(t, http.MethodPost, "/service/rest/v1/security/users", bytes.NewBufferString(body), admin)
	createResp.Body.Close()
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	t.Cleanup(func() {
		d := authReq(t, http.MethodDelete, "/service/rest/v1/security/users/"+username, nil, admin)
		d.Body.Close()
	})
	// Same second-granularity tokens_valid_after race as TestSelfChangePassword.
	_, err := pgtest.Pool(t).Exec(context.Background(),
		`UPDATE users SET tokens_valid_after = now() - interval '1 minute' WHERE username = $1`, username)
	require.NoError(t, err)
	return login(t, username, "devPass123!")
}

func TestAdminOnlyReads_DeveloperGets403(t *testing.T) {
	admin := login(t, "admin", "admin123")
	dev := loginAsDeveloper(t, admin, "admin-reads-dev")

	for _, p := range adminOnlyReadPaths {
		resp := authReq(t, http.MethodGet, p, nil, dev)
		resp.Body.Close()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, "GET %s as nx-developer", p)
	}
}

func TestAdminOnlyReads_AdminStillAllowed(t *testing.T) {
	admin := login(t, "admin", "admin123")

	for _, p := range adminOnlyReadPaths {
		resp := authReq(t, http.MethodGet, p, nil, admin)
		resp.Body.Close()
		assert.NotEqual(t, http.StatusForbidden, resp.StatusCode, "GET %s as nx-admin", p)
		assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode, "GET %s as nx-admin", p)
	}
}
