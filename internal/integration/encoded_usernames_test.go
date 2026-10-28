//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// Exercise the real HTTP router: handler-only tests miss Gin splitting a
// percent-encoded slash into a second route segment before authorization.
func TestEncodedUsernames_AdminOperations(t *testing.T) {
	admin := login(t, "admin", "admin123")
	developerRole := roleID(t, admin, "nx-developer")
	for _, username := range []string{"ci/repo", "ci/a+b", "ci/literal%2F", "ci/space name", "ci/roles", "ci/change-password"} {
		t.Run(username, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"userId": username, "password": "firstPass123!", "status": "active"})
			require.NoError(t, err)
			created := authReq(t, http.MethodPost, "/service/rest/v1/security/users", bytes.NewReader(body), admin)
			created.Body.Close()
			require.Equal(t, http.StatusCreated, created.StatusCode)
			t.Cleanup(func() {
				_, cleanupErr := pgtest.Pool(t).Exec(context.Background(), `DELETE FROM users WHERE username = $1`, username)
				require.NoError(t, cleanupErr)
			})
			path := "/service/rest/v1/security/users/" + url.PathEscape(username)
			assigned := authReq(t, http.MethodPut, path+"/roles", bytes.NewBufferString(`{"roleIds":["`+developerRole+`"]}`), admin)
			raw, err := io.ReadAll(assigned.Body)
			assigned.Body.Close()
			require.NoError(t, err)
			require.Equal(t, http.StatusNoContent, assigned.StatusCode, "%s", raw)

			got := authReq(t, http.MethodGet, path, nil, admin)
			var user struct {
				Username  string   `json:"userId"`
				Roles     []string `json:"roles"`
				FirstName string   `json:"firstName"`
			}
			err = json.NewDecoder(got.Body).Decode(&user)
			got.Body.Close()
			require.Equal(t, http.StatusOK, got.StatusCode)
			require.NoError(t, err)
			assert.Equal(t, username, user.Username)
			assert.Contains(t, user.Roles, "nx-developer")

			updated := authReq(t, http.MethodPut, path, bytes.NewBufferString(`{"firstName":"CI account"}`), admin)
			err = json.NewDecoder(updated.Body).Decode(&user)
			updated.Body.Close()
			require.Equal(t, http.StatusOK, updated.StatusCode)
			require.NoError(t, err)
			assert.Equal(t, "CI account", user.FirstName)

			changed := authReq(t, http.MethodPut, path+"/change-password", bytes.NewBufferString(`{"newPassword":"secondPass456!"}`), admin)
			changed.Body.Close()
			require.Equal(t, http.StatusNoContent, changed.StatusCode)

			deleted := authReq(t, http.MethodDelete, path, nil, admin)
			deleted.Body.Close()
			require.Equal(t, http.StatusNoContent, deleted.StatusCode)
			missing := authReq(t, http.MethodGet, path, nil, admin)
			missing.Body.Close()
			assert.Equal(t, http.StatusNotFound, missing.StatusCode)
		})
	}
}

func TestEncodedUsernames_RequireAdmin(t *testing.T) {
	admin := login(t, "admin", "admin123")
	dev := loginAsDeveloper(t, admin, "encoded-usernames-dev")
	for _, token := range []string{"", dev} {
		want := http.StatusForbidden
		if token == "" {
			want = http.StatusUnauthorized
		}
		for _, endpoint := range []struct{ method, suffix string }{
			{http.MethodGet, ""}, {http.MethodPut, ""}, {http.MethodDelete, ""},
			{http.MethodPut, "/roles"}, {http.MethodPut, "/change-password"},
		} {
			resp := authReq(t, endpoint.method, "/service/rest/v1/security/users/ci%2Frepo"+endpoint.suffix, bytes.NewBufferString(`{}`), token)
			resp.Body.Close()
			assert.Equal(t, want, resp.StatusCode, "%s %s", endpoint.method, endpoint.suffix)
		}
	}
}
