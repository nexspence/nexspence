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
	"github.com/nexspence-oss/nexspence/internal/repository"
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

// mountSelfChangePassword mounts the self-service route (no :userId param,
// PUT /api/v1/me/change-password) with the acting user set in context, as the
// authed group + AuthMiddleware would.
func mountSelfChangePassword(t *testing.T, callerUsername string, callerRoles []string) (*gin.Engine, *testutil.UserRepo) {
	t.Helper()
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo()
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	svc := service.NewUserService(users, roles, authSvc, zap.NewNop().Sugar())
	h := handlers.NewUserHandler(svc)

	r := gin.New()
	r.PUT("/api/v1/me/change-password", func(c *gin.Context) {
		c.Set("username", callerUsername)
		c.Set("roles", callerRoles)
		h.ChangePassword(c)
	})
	return r, users
}

func TestUserHandler_SelfChangePassword_OwnPassword_NoContent(t *testing.T) {
	r, users := mountSelfChangePassword(t, "self", []string{})
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	hash, err := authSvc.HashPassword("old-pw")
	require.NoError(t, err)
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "self", Email: "self@test.com", PasswordHash: hash,
		Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))
	// No :userId param on the self route → handler falls back to acting user.
	rec := do(t, r, http.MethodPut, "/api/v1/me/change-password", map[string]any{
		"oldPassword": "old-pw",
		"newPassword": "new-pw",
	})
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestUserHandler_SelfChangePassword_AdminStillNeedsOldPassword(t *testing.T) {
	// The self route verifies the current password whatever the caller's role.
	// Branching on the role instead sent an admin's own change through the
	// no-old-password admin verb, so the "Current password" the profile modal
	// asks for was accepted with any value — a stolen session (or an unattended
	// browser) could take the account over without knowing it.
	r, users := mountSelfChangePassword(t, "admin", []string{"nx-admin"})
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	hash, err := authSvc.HashPassword("real-old-pw")
	require.NoError(t, err)
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "admin", Email: "admin@test.com", PasswordHash: hash,
		Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))

	rec := do(t, r, http.MethodPut, "/api/v1/me/change-password", map[string]any{
		"oldPassword": "wrong-pw",
		"newPassword": "attacker-chosen-pw",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)

	stored, err := users.Get(testContext(), "admin")
	require.NoError(t, err)
	assert.Error(t, authSvc.CheckPassword(stored.PasswordHash, "attacker-chosen-pw"),
		"a wrong current password must not rewrite the stored hash")
	assert.NoError(t, authSvc.CheckPassword(stored.PasswordHash, "real-old-pw"))
}

func TestUserHandler_SelfChangePassword_AdminCorrectOldPassword_NoContent(t *testing.T) {
	// The flip side: the admin's own change still works with the right password.
	r, users := mountSelfChangePassword(t, "admin", []string{"nx-admin"})
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	hash, err := authSvc.HashPassword("real-old-pw")
	require.NoError(t, err)
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "admin", Email: "admin@test.com", PasswordHash: hash,
		Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))

	rec := do(t, r, http.MethodPut, "/api/v1/me/change-password", map[string]any{
		"oldPassword": "real-old-pw",
		"newPassword": "brand-new-pw",
	})
	require.Equal(t, http.StatusNoContent, rec.Code)

	stored, err := users.Get(testContext(), "admin")
	require.NoError(t, err)
	assert.NoError(t, authSvc.CheckPassword(stored.PasswordHash, "brand-new-pw"))
}

func TestUserHandler_AdminRoute_ResetsOwnPasswordWithoutOldPassword(t *testing.T) {
	// The admin route keeps the no-old-password reset, including on the admin's
	// own account — that is the Users-page reset button, and narrowing the self
	// route must not take it away.
	r, users := mountChangePassword(t, "admin", []string{"nx-admin"})
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	hash, err := authSvc.HashPassword("real-old-pw")
	require.NoError(t, err)
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "admin", Email: "admin@test.com", PasswordHash: hash,
		Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))

	rec := do(t, r, http.MethodPut,
		"/service/rest/v1/security/users/admin/change-password", map[string]any{
			"newPassword": "brand-new-pw",
		})
	require.Equal(t, http.StatusNoContent, rec.Code)

	stored, err := users.Get(testContext(), "admin")
	require.NoError(t, err)
	assert.NoError(t, authSvc.CheckPassword(stored.PasswordHash, "brand-new-pw"))
}

func TestUserHandler_SelfChangePassword_CannotTargetOther(t *testing.T) {
	// The admin route requires the :userId param. A non-admin hitting the admin
	// route for another user is forbidden (proves self cannot change others).
	r, _ := mountChangePassword(t, "self", []string{})
	rec := do(t, r, http.MethodPut,
		"/service/rest/v1/security/users/someone-else/change-password", map[string]any{
			"oldPassword": "old",
			"newPassword": "new",
		})
	assert.Equal(t, http.StatusForbidden, rec.Code)
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
	// Pre-seed the two roles so GetUserRoles can resolve them after assignment —
	// this makes the round-trip assertion verify that Create actually called
	// SetUserRoles, not just that the request struct carried the role list.
	require.NoError(t, roles.Create(testContext(), &domain.Role{ID: "role-x", Name: "x"}))
	require.NoError(t, roles.Create(testContext(), &domain.Role{ID: "role-y", Name: "y"}))

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

	// Verify the assignment was persisted to the role repo by user ID (proves
	// the service forwarded the roles to SetUserRoles).
	assigned, err := roles.GetUserRoles(testContext(), stored.ID)
	require.NoError(t, err)
	gotIDs := make([]string, len(assigned))
	for i, a := range assigned {
		gotIDs[i] = a.ID
	}
	assert.ElementsMatch(t, []string{"role-x", "role-y"}, gotIDs)
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

func TestUserHandler_Create_PasswordTooShort_400(t *testing.T) {
	// POST parity with the change-password verbs: on a service wired with the
	// configured minimum (as router.go does), a short initial password is a
	// 400 — not the 500 an unmapped service error used to produce.
	users := testutil.NewUserRepo()
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4).WithMinPasswordLength(8)
	h := handlers.NewUserHandler(service.NewUserService(users, testutil.NewRoleRepo(), authSvc, zap.NewNop().Sugar()))
	r := gin.New()
	r.POST("/service/rest/v1/security/users", h.Create)
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/users", map[string]any{
		"userId":   "tiny",
		"password": "short",
	})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "too short")

	got, err := users.Get(testContext(), "tiny")
	require.ErrorIs(t, err, repository.ErrNotFound)
	assert.Nil(t, got)
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

// Email uniqueness is enforced only by the DB, so a duplicate used to escape as
// a raw constraint error and be answered with a 500 carrying SQL internals.
func TestUserHandler_Create_DuplicateEmail_409(t *testing.T) {
	r, users, _ := mountUsers(t)
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "erin", Email: "shared@test.com",
		Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/users", map[string]any{
		"userId":       "erin-svc",
		"emailAddress": "shared@test.com",
		"password":     "pw",
	})
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "email")
	assert.NotContains(t, rec.Body.String(), "SQLSTATE")
}

// The same collision on the sibling verb: moving one account onto another's
// email is a conflict, not an internal failure.
func TestUserHandler_Update_DuplicateEmail_409(t *testing.T) {
	r, users, _ := mountUsers(t)
	for _, u := range []*domain.User{
		{Username: "u1", Email: "taken@test.com", Status: domain.UserStatusActive, Source: domain.UserSourceLocal},
		{Username: "u2", Email: "own@test.com", Status: domain.UserStatusActive, Source: domain.UserSourceLocal},
	} {
		require.NoError(t, users.Create(testContext(), u))
	}
	rec := do(t, r, http.MethodPut, "/service/rest/v1/security/users/u2", map[string]any{
		"emailAddress": "taken@test.com",
	})
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.NotContains(t, rec.Body.String(), "SQLSTATE")
}

// Any number of email-less accounts stays legal — the DB index is partial.
func TestUserHandler_Create_EmptyEmailsDoNotCollide(t *testing.T) {
	r, users, _ := mountUsers(t)
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "ldap-a", Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
	}))
	rec := do(t, r, http.MethodPost, "/service/rest/v1/security/users", map[string]any{
		"userId":   "ldap-b",
		"password": "pw",
	})
	assert.Equal(t, http.StatusCreated, rec.Code)
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
	require.ErrorIs(t, err, repository.ErrNotFound)
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

func TestUserHandler_ChangePassword_AdminResetNonLocal_403(t *testing.T) {
	// An admin resetting an oidc account is refused server-side: the identity
	// provider owns that credential, writing a local hash would do nothing.
	r, users := mountChangePassword(t, "admin", []string{"nx-admin"})
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "sso-user", Email: "sso@test.com",
		Status: domain.UserStatusActive, Source: domain.UserSourceOIDC,
	}))
	rec := do(t, r, http.MethodPut,
		"/service/rest/v1/security/users/sso-user/change-password", map[string]any{
			"newPassword": "brand-new-pw",
		})
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "identity provider")

	stored, err := users.Get(testContext(), "sso-user")
	require.NoError(t, err)
	assert.Empty(t, stored.PasswordHash)
}

func TestUserHandler_SelfChangePassword_NonLocal_403(t *testing.T) {
	// A self-change on an ldap account must answer "password is managed by
	// the identity provider" — not bcrypt's confusing "invalid password"
	// from the empty local hash.
	r, users := mountSelfChangePassword(t, "ldap-user", []string{})
	require.NoError(t, users.Create(testContext(), &domain.User{
		Username: "ldap-user", Email: "ldap@test.com",
		Status: domain.UserStatusActive, Source: domain.UserSourceLDAP,
	}))
	rec := do(t, r, http.MethodPut, "/api/v1/me/change-password", map[string]any{
		"oldPassword": "whatever",
		"newPassword": "brand-new-pw",
	})
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "identity provider")
}

func TestUserHandler_ChangePassword_RejectsTooShort(t *testing.T) {
	// Both verbs on a service wired with the configured minimum (as router.go
	// does) answer 400 before anything is written.
	mount := func(t *testing.T, caller string, roles []string) (*gin.Engine, *testutil.UserRepo) {
		t.Helper()
		users := testutil.NewUserRepo()
		roleRepo := testutil.NewRoleRepo()
		authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4).WithMinPasswordLength(8)
		h := handlers.NewUserHandler(service.NewUserService(users, roleRepo, authSvc, zap.NewNop().Sugar()))
		r := gin.New()
		wrap := func(c *gin.Context) {
			c.Set("username", caller)
			c.Set("roles", roles)
			h.ChangePassword(c)
		}
		r.PUT("/service/rest/v1/security/users/:userId/change-password", wrap)
		r.PUT("/api/v1/me/change-password", wrap)
		return r, users
	}

	t.Run("admin reset", func(t *testing.T) {
		r, users := mount(t, "admin", []string{"nx-admin"})
		require.NoError(t, users.Create(testContext(), &domain.User{
			Username: "target", Email: "target@test.com",
			Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
		}))
		rec := do(t, r, http.MethodPut,
			"/service/rest/v1/security/users/target/change-password", map[string]any{
				"newPassword": "short",
			})
		require.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "too short")

		stored, err := users.Get(testContext(), "target")
		require.NoError(t, err)
		assert.Empty(t, stored.PasswordHash)
	})

	t.Run("self change", func(t *testing.T) {
		r, users := mount(t, "self", []string{})
		authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
		hash, err := authSvc.HashPassword("old-password")
		require.NoError(t, err)
		require.NoError(t, users.Create(testContext(), &domain.User{
			Username: "self", Email: "self@test.com", PasswordHash: hash,
			Status: domain.UserStatusActive, Source: domain.UserSourceLocal,
		}))
		rec := do(t, r, http.MethodPut, "/api/v1/me/change-password", map[string]any{
			"oldPassword": "old-password",
			"newPassword": "short",
		})
		require.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "too short")
	})
}
