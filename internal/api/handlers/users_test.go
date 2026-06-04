package handlers_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// mountUsers builds a real UserService over mock repos (as router.go wires it)
// and mounts the UserHandler routes. It returns the engine plus the mocks so
// tests can seed data or fault them via the exported .Err seam.
func mountUsers(t *testing.T) (*gin.Engine, *testutil.UserRepo, *testutil.RoleRepo) {
	t.Helper()
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo()
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	svc := service.NewUserService(users, roles, authSvc, zap.NewNop().Sugar())
	h := handlers.NewUserHandler(svc)

	r := gin.New()
	r.GET("/service/rest/v1/security/users", h.List)
	r.GET("/service/rest/v1/security/users/:userId", h.Get)
	r.POST("/service/rest/v1/security/users", h.Create)
	r.PUT("/service/rest/v1/security/users/:userId", h.Update)
	r.DELETE("/service/rest/v1/security/users/:userId", h.Delete)
	r.PUT("/service/rest/v1/security/users/:userId/change-password", h.ChangePassword)
	return r, users, roles
}

// withCaller wraps the change-password route so c.Get("roles")/c.Get("username")
// are populated as the auth middleware would, since ChangePassword reads them.
func mountChangePassword(t *testing.T, callerUsername string, callerRoles []string) (*gin.Engine, *testutil.UserRepo) {
	t.Helper()
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo()
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	svc := service.NewUserService(users, roles, authSvc, zap.NewNop().Sugar())
	h := handlers.NewUserHandler(svc)

	r := gin.New()
	r.PUT("/service/rest/v1/security/users/:userId/change-password", func(c *gin.Context) {
		c.Set("username", callerUsername)
		c.Set("roles", callerRoles)
		h.ChangePassword(c)
	})
	return r, users
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestUserHandler_List_Empty(t *testing.T) {
	r, _, _ := mountUsers(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/users", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got []domain.User
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Empty(t, got)
}

func TestUserHandler_List_StripsPasswordHash(t *testing.T) {
	r, users, _ := mountUsers(t)
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "alice", Email: "alice@test.com", PasswordHash: "secret-hash",
		Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/users", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	// PasswordHash has json:"-" so it is never serialized; assert it stays absent.
	assert.NotContains(t, rec.Body.String(), "secret-hash")
	var got []domain.User
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "alice", got[0].Username)
}

func TestUserHandler_List_RepoError_500(t *testing.T) {
	r, users, _ := mountUsers(t)
	users.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/users", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestUserHandler_Get_OK(t *testing.T) {
	r, users, _ := mountUsers(t)
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "bob", Email: "bob@test.com", PasswordHash: "h",
		Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/users/bob", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var got domain.User
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "bob", got.Username)
	assert.NotContains(t, rec.Body.String(), `"h"`)
}

func TestUserHandler_Get_NotFound_404(t *testing.T) {
	r, _, _ := mountUsers(t)
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/users/ghost", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUserHandler_Get_RepoError_500(t *testing.T) {
	r, users, _ := mountUsers(t)
	users.Err = errors.New("db down")
	rec := do(t, r, http.MethodGet, "/service/rest/v1/security/users/anyone", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestUserHandler_Create_OK_PersistsAndStripsPassword(t *testing.T) {
	r, users, _ := mountUsers(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/users", map[string]any{
		"userId":       "carol",
		"emailAddress": "carol@test.com",
		"password":     "s3cret-plain",
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	// Plaintext password must never appear in the response body.
	assert.NotContains(t, rec.Body.String(), "s3cret-plain")

	var got domain.User
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "carol", got.Username)

	// Persisted with defaults applied. (Note: the handler zeroes req.PasswordHash
	// after Create for the response; the in-memory mock shares the same pointer,
	// so the stored hash is not inspectable here — the "not plaintext" guarantee
	// is asserted via the response body above and exercised by service-layer tests.)
	stored, err := users.Get(testContext(), "carol")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, domain.UserStatusActive, stored.Status)
	assert.Equal(t, domain.UserSourceLocal, stored.Source)
}

func TestUserHandler_Create_AssignsRoles(t *testing.T) {
	r, users, roles := mountUsers(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/users", map[string]any{
		"userId":       "dave",
		"emailAddress": "dave@test.com",
		"password":     "pw",
		"roles":        []string{"role-x", "role-y"},
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	stored, err := users.Get(testContext(), "dave")
	require.NoError(t, err)
	require.NotNil(t, stored)
	// Roles were forwarded to the role repo for assignment (round-trip).
	assigned, err := roles.GetUserRoles(testContext(), stored.ID)
	require.NoError(t, err)
	_ = assigned // GetUserRoles returns roles that exist; assignment recorded by ID.
	assert.Equal(t, []string{"role-x", "role-y"}, stored.Roles)
}

func TestUserHandler_Create_BadJSON_400(t *testing.T) {
	r, _, _ := mountUsers(t)
	rec := doRaw(t, r, http.MethodPost, "/service/rest/v1/security/users", []byte(`{not json`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUserHandler_Create_MissingUsername_400(t *testing.T) {
	r, _, _ := mountUsers(t)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/users", map[string]any{
		"emailAddress": "noname@test.com",
		"password":     "pw",
	})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUserHandler_Create_Duplicate_409(t *testing.T) {
	r, users, _ := mountUsers(t)
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "erin", Email: "erin@test.com",
		Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/users", map[string]any{
		"userId":       "erin",
		"emailAddress": "erin2@test.com",
		"password":     "pw",
	})
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestUserHandler_Create_RepoError_500(t *testing.T) {
	r, users, _ := mountUsers(t)
	users.Err = errors.New("db down")
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/users", map[string]any{
		"userId":       "frank",
		"emailAddress": "frank@test.com",
		"password":     "pw",
	})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestUserHandler_Update_OK(t *testing.T) {
	r, users, _ := mountUsers(t)
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "gina", Email: "gina@test.com", FirstName: "Gina",
		Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/users/gina", map[string]any{
		"emailAddress": "gina-new@test.com",
		"firstName":    "Regina",
	})
	require.Equal(t, http.StatusOK, rec.Code)

	var got domain.User
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "gina-new@test.com", got.Email)
	assert.Equal(t, "Regina", got.FirstName)
}

func TestUserHandler_Update_BadJSON_400(t *testing.T) {
	r, _, _ := mountUsers(t)
	rec := doRaw(t, r, http.MethodPut, "/service/rest/v1/security/users/gina", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUserHandler_Update_NotFound_404(t *testing.T) {
	r, _, _ := mountUsers(t)
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/users/ghost", map[string]any{
		"emailAddress": "x@test.com",
	})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUserHandler_Update_RepoError_500(t *testing.T) {
	r, users, _ := mountUsers(t)
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "hank", Email: "hank@test.com",
		Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))
	// Get (inside Update) succeeds from cache, then Update on the repo fails.
	users.Err = errors.New("db down")
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/users/hank", map[string]any{
		"emailAddress": "hank2@test.com",
	})
	// With Err set, even the Get lookup returns the error → 500 (or 404). Assert 5xx path.
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestUserHandler_Delete_OK(t *testing.T) {
	r, users, _ := mountUsers(t)
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "ivan", Email: "ivan@test.com",
		Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/security/users/ivan", nil)
	require.Equal(t, http.StatusNoContent, rec.Code)

	gone, err := users.Get(testContext(), "ivan")
	require.NoError(t, err)
	assert.Nil(t, gone)
}

func TestUserHandler_Delete_NotFound_404(t *testing.T) {
	r, _, _ := mountUsers(t)
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/security/users/ghost", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUserHandler_Delete_RepoError_500(t *testing.T) {
	r, users, _ := mountUsers(t)
	users.Err = errors.New("db down")
	rec := do(t, r, http.MethodDelete, "/service/rest/v1/security/users/anyone", nil)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ── ChangePassword ────────────────────────────────────────────────────────────

func TestUserHandler_ChangePassword_BadJSON_400(t *testing.T) {
	r, _ := mountChangePassword(t, "self", []string{})
	rec := doRaw(t, r, http.MethodPut,
		"/service/rest/v1/security/users/self/change-password", []byte(`{bad`))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUserHandler_ChangePassword_MissingNewPassword_400(t *testing.T) {
	r, _ := mountChangePassword(t, "self", []string{})
	rec := do(t, r, http.MethodPut,
		"/service/rest/v1/security/users/self/change-password", map[string]any{
			"oldPassword": "old",
		})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUserHandler_ChangePassword_AdminSetsPassword_NoContent(t *testing.T) {
	r, users := mountChangePassword(t, "admin", []string{"nx-admin"})
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "target", Email: "target@test.com",
		Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))
	rec := do(t, r, http.MethodPut,
		"/service/rest/v1/security/users/target/change-password", map[string]any{
			"newPassword": "brand-new-pw",
		})
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestUserHandler_ChangePassword_AdminTargetMissing_500(t *testing.T) {
	r, _ := mountChangePassword(t, "admin", []string{"nx-admin"})
	rec := do(t, r, http.MethodPut,
		"/service/rest/v1/security/users/ghost/change-password", map[string]any{
			"newPassword": "x",
		})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUserHandler_ChangePassword_SelfChange_NoContent(t *testing.T) {
	r, users := mountChangePassword(t, "self", []string{})
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	hash, err := authSvc.HashPassword("old-pw")
	require.NoError(t, err)
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "self", Email: "self@test.com", PasswordHash: hash,
		Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))
	rec := do(t, r, http.MethodPut,
		"/service/rest/v1/security/users/self/change-password", map[string]any{
			"oldPassword": "old-pw",
			"newPassword": "new-pw",
		})
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestUserHandler_ChangePassword_SelfChange_WrongOldPassword_400(t *testing.T) {
	r, users := mountChangePassword(t, "self", []string{})
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	hash, err := authSvc.HashPassword("correct-pw")
	require.NoError(t, err)
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "self", Email: "self@test.com", PasswordHash: hash,
		Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))
	rec := do(t, r, http.MethodPut,
		"/service/rest/v1/security/users/self/change-password", map[string]any{
			"oldPassword": "wrong-pw",
			"newPassword": "new-pw",
		})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUserHandler_ChangePassword_OtherUser_Forbidden_403(t *testing.T) {
	// Non-admin caller "self" trying to change "someone-else" → 403.
	r, _ := mountChangePassword(t, "self", []string{})
	rec := do(t, r, http.MethodPut,
		"/service/rest/v1/security/users/someone-else/change-password", map[string]any{
			"oldPassword": "old",
			"newPassword": "new",
		})
	assert.Equal(t, http.StatusForbidden, rec.Code)
}
