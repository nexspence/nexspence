package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// newUserCRUDSvc constructs a UserService with optional seed users and roles.
func newUserCRUDSvc(users []*domain.User, roles []*domain.Role) (*service.UserService, *testutil.UserRepo, *testutil.RoleRepo) {
	userRepo := testutil.NewUserRepo(users...)
	roleRepo := testutil.NewRoleRepo(roles...)
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	svc := service.NewUserService(userRepo, roleRepo, authSvc, zap.NewNop().Sugar())
	return svc, userRepo, roleRepo
}

func activeUserFixture(id, username, password string) *domain.User {
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	hash, _ := authSvc.HashPassword(password)
	return &domain.User{
		ID:           id,
		Username:     username,
		PasswordHash: hash,
		Status:       domain.UserStatusActive,
		Source:       domain.UserSourceLocal,
	}
}

// ── Create ────────────────────────────────────────────────────

func TestUserCRUD_Create_Success(t *testing.T) {
	svc, userRepo, _ := newUserCRUDSvc(nil, nil)

	u := &domain.User{Username: "alice", Email: "alice@example.com"}
	err := svc.Create(context.Background(), u, "password123")
	require.NoError(t, err)
	assert.NotEmpty(t, u.ID, "Create must populate the ID")
	assert.NotEmpty(t, u.PasswordHash, "Create must hash the password")

	// User should be stored in repo.
	stored, _ := userRepo.Get(context.Background(), "alice")
	require.NotNil(t, stored)
	assert.Equal(t, domain.UserStatusActive, stored.Status)
	assert.Equal(t, domain.UserSourceLocal, stored.Source)
}

func TestUserCRUD_Create_EmptyUsername_Error(t *testing.T) {
	svc, _, _ := newUserCRUDSvc(nil, nil)

	u := &domain.User{Username: ""}
	err := svc.Create(context.Background(), u, "pass")
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrInvalidInput)
}

func TestUserCRUD_Create_DuplicateUsername_Error(t *testing.T) {
	existing := activeUserFixture("id-1", "bob", "pass")
	svc, _, _ := newUserCRUDSvc([]*domain.User{existing}, nil)

	u := &domain.User{Username: "bob"}
	err := svc.Create(context.Background(), u, "newpass")
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrAlreadyExists)
}

func TestUserCRUD_Create_NoPassword_NoHash(t *testing.T) {
	svc, _, _ := newUserCRUDSvc(nil, nil)

	u := &domain.User{Username: "carol"}
	err := svc.Create(context.Background(), u, "")
	require.NoError(t, err)
	assert.Empty(t, u.PasswordHash, "empty password must not produce a hash")
}

func TestUserCRUD_Create_WithRoles_SetsUserRoles(t *testing.T) {
	role := &domain.Role{ID: "role-dev", Name: "developer"}
	svc, _, roleRepo := newUserCRUDSvc(nil, []*domain.Role{role})

	u := &domain.User{Username: "dave", Roles: []string{"role-dev"}}
	err := svc.Create(context.Background(), u, "pass")
	require.NoError(t, err)

	// Roles should be assigned.
	assigned, _ := roleRepo.GetUserRoles(context.Background(), u.ID)
	assert.Len(t, assigned, 1)
	assert.Equal(t, "developer", assigned[0].Name)
}

// ── List ──────────────────────────────────────────────────────

func TestUserCRUD_List_Empty(t *testing.T) {
	svc, _, _ := newUserCRUDSvc(nil, nil)

	users, err := svc.List(context.Background(), "")
	require.NoError(t, err)
	assert.Empty(t, users)
}

func TestUserCRUD_List_ReturnsAll(t *testing.T) {
	u1 := activeUserFixture("id-1", "alice", "p")
	u2 := activeUserFixture("id-2", "bob", "p")
	svc, _, _ := newUserCRUDSvc([]*domain.User{u1, u2}, nil)

	users, err := svc.List(context.Background(), "")
	require.NoError(t, err)
	assert.Len(t, users, 2)
}

// ── Get ───────────────────────────────────────────────────────

func TestUserCRUD_Get_Found(t *testing.T) {
	u := activeUserFixture("id-1", "alice", "p")
	svc, _, _ := newUserCRUDSvc([]*domain.User{u}, nil)

	got, err := svc.Get(context.Background(), "alice")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "alice", got.Username)
}

func TestUserCRUD_Get_NotFound_Error(t *testing.T) {
	svc, _, _ := newUserCRUDSvc(nil, nil)

	_, err := svc.Get(context.Background(), "nobody")
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrNotFound)
}

// ── GetByID ───────────────────────────────────────────────────

func TestUserCRUD_GetByID_Found(t *testing.T) {
	u := activeUserFixture("id-42", "alice", "p")
	svc, _, _ := newUserCRUDSvc([]*domain.User{u}, nil)

	got, err := svc.GetByID(context.Background(), "id-42")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "alice", got.Username)
}

func TestUserCRUD_GetByID_NotFound_Error(t *testing.T) {
	svc, _, _ := newUserCRUDSvc(nil, nil)

	_, err := svc.GetByID(context.Background(), "no-such-id")
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrNotFound)
}

// ── Update ────────────────────────────────────────────────────

func TestUserCRUD_Update_Success(t *testing.T) {
	u := activeUserFixture("id-1", "alice", "p")
	u.Email = "alice@old.com"
	svc, userRepo, _ := newUserCRUDSvc([]*domain.User{u}, nil)

	updated, err := svc.Update(context.Background(), "alice", &domain.User{Email: "alice@new.com"})
	require.NoError(t, err)
	assert.Equal(t, "alice@new.com", updated.Email)

	stored, _ := userRepo.Get(context.Background(), "alice")
	assert.Equal(t, "alice@new.com", stored.Email)
}

func TestUserCRUD_Update_NotFound_Error(t *testing.T) {
	svc, _, _ := newUserCRUDSvc(nil, nil)

	_, err := svc.Update(context.Background(), "nobody", &domain.User{Email: "x@x.com"})
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrNotFound)
}

// ── ChangePassword ────────────────────────────────────────────

func TestUserCRUD_ChangePassword_Success(t *testing.T) {
	u := activeUserFixture("id-1", "alice", "oldpass")
	svc, userRepo, _ := newUserCRUDSvc([]*domain.User{u}, nil)

	err := svc.ChangePassword(context.Background(), "alice", "oldpass", "newpass")
	require.NoError(t, err)

	// Verify new password works.
	stored, _ := userRepo.Get(context.Background(), "alice")
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	assert.NoError(t, authSvc.CheckPassword(stored.PasswordHash, "newpass"))
}

func TestUserCRUD_ChangePassword_WrongOldPassword_Error(t *testing.T) {
	u := activeUserFixture("id-1", "alice", "correctpass")
	svc, _, _ := newUserCRUDSvc([]*domain.User{u}, nil)

	err := svc.ChangePassword(context.Background(), "alice", "wrongpass", "newpass")
	require.Error(t, err)
}

func TestUserCRUD_ChangePassword_UserNotFound_Error(t *testing.T) {
	svc, _, _ := newUserCRUDSvc(nil, nil)

	err := svc.ChangePassword(context.Background(), "nobody", "old", "new")
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrNotFound)
}

// ── SetPassword ───────────────────────────────────────────────

func TestUserCRUD_SetPassword_Success(t *testing.T) {
	u := activeUserFixture("id-1", "alice", "oldpass")
	svc, userRepo, _ := newUserCRUDSvc([]*domain.User{u}, nil)

	err := svc.SetPassword(context.Background(), "alice", "brandnewpass")
	require.NoError(t, err)

	stored, _ := userRepo.Get(context.Background(), "alice")
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	assert.NoError(t, authSvc.CheckPassword(stored.PasswordHash, "brandnewpass"))
}

func TestUserCRUD_SetPassword_UserNotFound_Error(t *testing.T) {
	svc, _, _ := newUserCRUDSvc(nil, nil)

	err := svc.SetPassword(context.Background(), "nobody", "pass")
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrNotFound)
}

// ── Delete ────────────────────────────────────────────────────

func TestUserCRUD_Delete_Success(t *testing.T) {
	u := activeUserFixture("id-1", "alice", "p")
	svc, userRepo, _ := newUserCRUDSvc([]*domain.User{u}, nil)

	err := svc.Delete(context.Background(), "alice")
	require.NoError(t, err)

	deleted, _ := userRepo.Get(context.Background(), "alice")
	assert.Nil(t, deleted)
}

func TestUserCRUD_Delete_NotFound_Error(t *testing.T) {
	svc, _, _ := newUserCRUDSvc(nil, nil)

	err := svc.Delete(context.Background(), "nobody")
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrNotFound)
}

// ── GetUserRoles ──────────────────────────────────────────────

func TestUserCRUD_GetUserRoles_ReturnsRoles(t *testing.T) {
	role := &domain.Role{ID: "r1", Name: "developer"}
	svc, _, roleRepo := newUserCRUDSvc(nil, []*domain.Role{role})

	// Manually assign roles in the repo.
	_ = roleRepo.SetUserRoles(context.Background(), "user-id-1", []string{"r1"})

	roles, err := svc.GetUserRoles(context.Background(), "user-id-1")
	require.NoError(t, err)
	require.Len(t, roles, 1)
	assert.Equal(t, "developer", roles[0].Name)
}

func TestUserCRUD_GetUserRoles_NoRoles_Empty(t *testing.T) {
	svc, _, _ := newUserCRUDSvc(nil, nil)

	roles, err := svc.GetUserRoles(context.Background(), "user-with-no-roles")
	require.NoError(t, err)
	assert.Empty(t, roles)
}

// ── SetUserRoles ──────────────────────────────────────────────

func TestUserCRUD_SetUserRoles_Success(t *testing.T) {
	role := &domain.Role{ID: "r1", Name: "developer"}
	svc, _, roleRepo := newUserCRUDSvc(nil, []*domain.Role{role})

	err := svc.SetUserRoles(context.Background(), "user-id-1", []string{"r1"})
	require.NoError(t, err)

	roles, _ := roleRepo.GetUserRoles(context.Background(), "user-id-1")
	assert.Len(t, roles, 1)
}

func TestUserCRUD_SetUserRoles_EmptyIDs_ClearsRoles(t *testing.T) {
	role := &domain.Role{ID: "r1", Name: "developer"}
	svc, _, roleRepo := newUserCRUDSvc(nil, []*domain.Role{role})
	_ = roleRepo.SetUserRoles(context.Background(), "user-id-1", []string{"r1"})

	err := svc.SetUserRoles(context.Background(), "user-id-1", []string{})
	require.NoError(t, err)

	roles, _ := roleRepo.GetUserRoles(context.Background(), "user-id-1")
	assert.Empty(t, roles)
}

// ── ValidateToken ─────────────────────────────────────────────

func TestUserCRUD_ValidateToken_ValidToken(t *testing.T) {
	u := activeUserFixture("id-1", "alice", "pass")
	svc, _, _ := newUserCRUDSvc([]*domain.User{u}, nil)

	// Login to get a real token issued by this service's auth.Service.
	token, _, err := svc.Login(context.Background(), "alice", "pass")
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := svc.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "alice", claims.Username)
	assert.Equal(t, "id-1", claims.UserID)
}

func TestUserCRUD_ValidateToken_InvalidToken_Error(t *testing.T) {
	svc, _, _ := newUserCRUDSvc(nil, nil)

	_, err := svc.ValidateToken("not.a.valid.jwt.token")
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestUserCRUD_ValidateToken_WrongSecret_Error(t *testing.T) {
	// Token signed with a different secret should be rejected.
	otherAuth := auth.NewService("completely-different-secret-here!", 1, 4)
	token, err := otherAuth.GenerateToken("id-x", "mallory", nil)
	require.NoError(t, err)

	svc, _, _ := newUserCRUDSvc(nil, nil)
	_, err = svc.ValidateToken(token)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}
