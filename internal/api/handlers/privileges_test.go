package handlers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// mountPrivileges wires PrivilegeHandler to a test Gin engine.
func mountPrivileges(t *testing.T) (*gin.Engine, *testutil.PrivilegeRepo, *testutil.RoleRepo) {
	t.Helper()
	privs := testutil.NewPrivilegeRepo()
	roles := testutil.NewRoleRepo()
	h := handlers.NewPrivilegeHandler(privs, roles)
	r := gin.New()
	r.GET("/service/rest/v1/security/privileges", h.List)
	r.GET("/service/rest/v1/security/privileges/:id", h.Get)
	r.POST("/service/rest/v1/security/privileges", h.Create)
	r.PUT("/service/rest/v1/security/privileges/:id", h.Update)
	r.DELETE("/service/rest/v1/security/privileges/:id", h.Delete)
	r.PUT("/service/rest/v1/security/roles/:id/privileges", h.SetRolePrivileges)
	r.GET("/api/v1/security/privilege-role-map", h.RoleMap)
	r.GET("/service/rest/v1/security/roles/:id/privileges", h.ListRolePrivileges)
	// MyPrivileges needs "userID" in the gin context — inject via middleware.
	authed := r.Group("/api/v1/me")
	authed.Use(func(c *gin.Context) {
		c.Set("userID", "user-test")
		c.Next()
	})
	authed.GET("/privileges", h.MyPrivileges)
	return r, privs, roles
}

// ── List ──────────────────────────────────────────────────────

func TestPrivilegeHandler_List_Empty(t *testing.T) {
	r, _, _ := mountPrivileges(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/privileges", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []domain.Privilege
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got)
}

func TestPrivilegeHandler_List_RepoError_500(t *testing.T) {
	r, privs, _ := mountPrivileges(t)
	privs.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/privileges", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Get ───────────────────────────────────────────────────────

func TestPrivilegeHandler_Get_NotFound_404(t *testing.T) {
	r, _, _ := mountPrivileges(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/privileges/ghost", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPrivilegeHandler_Get_OK(t *testing.T) {
	r, privs, _ := mountPrivileges(t)
	p := &domain.Privilege{ID: "priv-1", Name: "read-maven", Type: domain.PrivilegeTypeWildcard}
	require.NoError(t, privs.Create(testContext(), p))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/privileges/"+p.ID, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got domain.Privilege
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "read-maven", got.Name)
}

func TestPrivilegeHandler_Get_RepoError_500(t *testing.T) {
	r, privs, _ := mountPrivileges(t)
	privs.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/privileges/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Create ────────────────────────────────────────────────────

func TestPrivilegeHandler_Create_OK(t *testing.T) {
	r, _, _ := mountPrivileges(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/privileges",
		map[string]any{"name": "p1", "type": "wildcard"})
	require.Equal(t, http.StatusCreated, rec.Code)
	var got domain.Privilege
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "p1", got.Name)
}

func TestPrivilegeHandler_Create_BadJSON_400(t *testing.T) {
	r, _, _ := mountPrivileges(t)
	rec := doRaw(t, r, http.MethodPost, "/service/rest/v1/security/privileges", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPrivilegeHandler_Create_EmptyName_400(t *testing.T) {
	r, _, _ := mountPrivileges(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/privileges",
		map[string]any{"type": "wildcard"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPrivilegeHandler_Create_EmptyType_400(t *testing.T) {
	r, _, _ := mountPrivileges(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/privileges",
		map[string]any{"name": "p1"})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPrivilegeHandler_Create_RepoError_500(t *testing.T) {
	r, privs, _ := mountPrivileges(t)
	privs.Err = errors.New("db down")
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/privileges",
		map[string]any{"name": "p1", "type": "wildcard"})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Update ────────────────────────────────────────────────────

func TestPrivilegeHandler_Update_OK(t *testing.T) {
	r, privs, _ := mountPrivileges(t)
	p := &domain.Privilege{ID: "priv-1", Name: "old", Type: domain.PrivilegeTypeWildcard}
	require.NoError(t, privs.Create(testContext(), p))
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/privileges/"+p.ID,
		map[string]any{"name": "new", "type": "wildcard"})
	require.Equal(t, http.StatusOK, rec.Code)
	var got domain.Privilege
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "new", got.Name)
}

func TestPrivilegeHandler_Update_BadJSON_400(t *testing.T) {
	r, _, _ := mountPrivileges(t)
	rec := doRaw(t, r, http.MethodPut, "/service/rest/v1/security/privileges/any", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPrivilegeHandler_Update_RepoError_500(t *testing.T) {
	r, privs, _ := mountPrivileges(t)
	privs.Err = errors.New("db down")
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/privileges/any",
		map[string]any{"name": "p1", "type": "wildcard"})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Delete ────────────────────────────────────────────────────

func TestPrivilegeHandler_Delete_OK(t *testing.T) {
	r, privs, _ := mountPrivileges(t)
	p := &domain.Privilege{ID: "del-1", Name: "todelete", Type: domain.PrivilegeTypeWildcard}
	require.NoError(t, privs.Create(testContext(), p))
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/security/privileges/"+p.ID, nil)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestPrivilegeHandler_Delete_RepoError_500(t *testing.T) {
	r, privs, _ := mountPrivileges(t)
	privs.Err = errors.New("db down")
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/security/privileges/any", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── SetRolePrivileges ─────────────────────────────────────────

func TestPrivilegeHandler_SetRolePrivileges_OK(t *testing.T) {
	r, _, _ := mountPrivileges(t)
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/roles/role-1/privileges",
		map[string]any{"privilegeIds": []string{"p1", "p2"}})
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestPrivilegeHandler_SetRolePrivileges_BadJSON_400(t *testing.T) {
	r, _, _ := mountPrivileges(t)
	rec := doRaw(t, r, http.MethodPut, "/service/rest/v1/security/roles/role-1/privileges", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPrivilegeHandler_SetRolePrivileges_RepoError_500(t *testing.T) {
	r, _, roles := mountPrivileges(t)
	roles.SetPrivilegesErr = errors.New("db down")
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/roles/role-1/privileges",
		map[string]any{"privilegeIds": []string{"p1"}})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── RoleMap ───────────────────────────────────────────────────

func TestPrivilegeHandler_RoleMap_OK(t *testing.T) {
	r, _, _ := mountPrivileges(t)
	rec := do(t, r, http.MethodGet, "/api/v1/security/privilege-role-map", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string][]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.NotNil(t, got)
}

func TestPrivilegeHandler_RoleMap_RepoError_500(t *testing.T) {
	r, privs, _ := mountPrivileges(t)
	privs.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/api/v1/security/privilege-role-map", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── ListRolePrivileges ────────────────────────────────────────

func TestPrivilegeHandler_ListRolePrivileges_OK(t *testing.T) {
	r, _, _ := mountPrivileges(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/roles/role-1/privileges", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []domain.Privilege
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got)
}

func TestPrivilegeHandler_ListRolePrivileges_RepoError_500(t *testing.T) {
	r, privs, _ := mountPrivileges(t)
	privs.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/roles/role-1/privileges", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── MyPrivileges ──────────────────────────────────────────────

func TestPrivilegeHandler_MyPrivileges_OK(t *testing.T) {
	r, _, _ := mountPrivileges(t)
	rec := do(t, r, http.MethodGet, "/api/v1/me/privileges", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []domain.Privilege
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got)
}

func TestPrivilegeHandler_MyPrivileges_Unauthenticated_401(t *testing.T) {
	// Mount without the userID-injection middleware.
	privs := testutil.NewPrivilegeRepo()
	roles := testutil.NewRoleRepo()
	h := handlers.NewPrivilegeHandler(privs, roles)
	r := gin.New()
	r.GET("/api/v1/me/privileges", h.MyPrivileges)
	rec := do(t, r, http.MethodGet, "/api/v1/me/privileges", nil)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestPrivilegeHandler_MyPrivileges_RoleRepoError_500(t *testing.T) {
	r, _, roles := mountPrivileges(t)
	roles.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/api/v1/me/privileges", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestPrivilegeHandler_MyPrivileges_PrivRepoError_500(t *testing.T) {
	// Seed a role assigned to the test user so GetUserRoles returns it,
	// then break ListByRole via PrivilegeRepo.Err.
	privs := testutil.NewPrivilegeRepo()
	roles := testutil.NewRoleRepo()
	role := &domain.Role{ID: "role-1", Name: "admin"}
	require.NoError(t, roles.Create(testContext(), role))
	require.NoError(t, roles.SetUserRoles(testContext(), "user-test", []string{"role-1"}))

	h := handlers.NewPrivilegeHandler(privs, roles)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", "user-test")
		c.Next()
	})
	r.GET("/api/v1/me/privileges", h.MyPrivileges)

	privs.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/api/v1/me/privileges", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
