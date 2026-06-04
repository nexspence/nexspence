package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func mountRoles(t *testing.T) (*gin.Engine, *testutil.RoleRepo, *testutil.UserRepo) {
	t.Helper()
	roles := testutil.NewRoleRepo()
	users := testutil.NewUserRepo()
	h := handlers.NewRoleHandler(roles, users)
	r := gin.New()
	r.GET("/service/rest/v1/security/roles", h.List)
	r.POST("/service/rest/v1/security/roles", h.Create)
	r.PUT("/service/rest/v1/security/roles/:id", h.Update)
	r.DELETE("/service/rest/v1/security/roles/:id", h.Delete)
	r.PUT("/service/rest/v1/security/users/:userId/roles", h.SetUserRoles)
	return r, roles, users
}

func do(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestRoleHandler_List_Empty(t *testing.T) {
	r, _, _ := mountRoles(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/roles", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []domain.Role
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got)
}

func TestRoleHandler_Create_Then_List(t *testing.T) {
	r, _, _ := mountRoles(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/roles",
		map[string]any{"name": "dev", "description": "developers"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var created domain.Role
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	assert.Equal(t, "dev", created.Name)
	assert.Equal(t, "default", created.Source)

	rec = do(t, r, http.MethodGet, "/service/rest/v1/security/roles", nil)
	var got []domain.Role
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 1)
}

func TestRoleHandler_Create_BadJSON_400(t *testing.T) {
	r, _, _ := mountRoles(t)
	req := httptest.NewRequest(http.MethodPost, "/service/rest/v1/security/roles",
		bytes.NewReader([]byte(`{not json`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRoleHandler_Create_EmptyName_400(t *testing.T) {
	r, _, _ := mountRoles(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/roles",
		map[string]any{"description": "no name"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRoleHandler_Update(t *testing.T) {
	r, roles, _ := mountRoles(t)
	ro := &domain.Role{Name: "ops"}
	require.NoError(t, roles.Create(testContext(), ro))
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/roles/"+ro.ID,
		map[string]any{"name": "ops2", "description": "renamed"})
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRoleHandler_Delete(t *testing.T) {
	r, roles, _ := mountRoles(t)
	ro := &domain.Role{Name: "temp"}
	require.NoError(t, roles.Create(testContext(), ro))
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/security/roles/"+ro.ID, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestRoleHandler_SetUserRoles_UserNotFound_404(t *testing.T) {
	r, _, _ := mountRoles(t)
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/users/ghost/roles",
		map[string]any{"roleIds": []string{"r1"}})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRoleHandler_SetUserRoles_OK(t *testing.T) {
	r, _, users := mountRoles(t)
	u := &domain.User{Username: "alice", Email: "alice@test.com"}
	require.NoError(t, users.Create(testContext(), u))
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/users/alice/roles",
		map[string]any{"roleIds": []string{"r1", "r2"}})
	assert.Equal(t, http.StatusNoContent, rec.Code)
}
