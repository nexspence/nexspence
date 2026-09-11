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

// newPasswordSvc builds a UserService whose auth.Service carries a configured
// minimum password length, mirroring how router.go wires
// auth.password_min_length. minLen 0 means "not wired" and disables the check.
func newPasswordSvc(t *testing.T, users []*domain.User, minLen int) (*service.UserService, *testutil.UserRepo) {
	t.Helper()
	userRepo := testutil.NewUserRepo(users...)
	roleRepo := testutil.NewRoleRepo()
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4).WithMinPasswordLength(minLen)
	svc := service.NewUserService(userRepo, roleRepo, authSvc, zap.NewNop().Sugar())
	return svc, userRepo
}

// sourceUserFixture builds a user with the given source and a known password.
func sourceUserFixture(id, username, password string, source domain.UserSource) *domain.User {
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	hash, _ := authSvc.HashPassword(password)
	return &domain.User{
		ID:           id,
		Username:     username,
		PasswordHash: hash,
		Status:       domain.UserStatusActive,
		Source:       source,
	}
}

// ── minimum length ────────────────────────────────────────────

func TestUserService_ChangePassword_RejectsTooShort(t *testing.T) {
	u := sourceUserFixture("id-1", "alice", "old-password", domain.UserSourceLocal)
	svc, userRepo := newPasswordSvc(t, []*domain.User{u}, 8)

	err := svc.ChangePassword(context.Background(), "alice", "old-password", "short")
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrPasswordTooShort)

	stored, err := userRepo.Get(context.Background(), "alice")
	require.NoError(t, err)
	assert.Equal(t, u.PasswordHash, stored.PasswordHash,
		"a rejected password change must not rewrite the stored hash")
}

func TestUserService_SetPassword_RejectsTooShort(t *testing.T) {
	u := sourceUserFixture("id-1", "alice", "old-password", domain.UserSourceLocal)
	svc, userRepo := newPasswordSvc(t, []*domain.User{u}, 8)

	err := svc.SetPassword(context.Background(), "alice", "short")
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrPasswordTooShort)

	stored, err := userRepo.Get(context.Background(), "alice")
	require.NoError(t, err)
	assert.Equal(t, u.PasswordHash, stored.PasswordHash,
		"a rejected admin reset must not rewrite the stored hash")
}

func TestUserService_Create_RejectsTooShort(t *testing.T) {
	// POST parity with the change-password verbs: a user-facing create with a
	// below-minimum password fails the same way (fail-closed, no silent write).
	svc, _ := newPasswordSvc(t, nil, 8)

	u := &domain.User{Username: "tiny"}
	err := svc.Create(context.Background(), u, "short")
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrPasswordTooShort)
}

func TestUserService_Create_EmptyPassword_AllowedWithMinLength(t *testing.T) {
	// Accounts without a local credential (SSO provisioning, migrated external
	// users) are created with an empty password — the minimum applies only to
	// a password that is actually being stored.
	svc, _ := newPasswordSvc(t, nil, 8)

	u := &domain.User{Username: "sso-user", Source: domain.UserSourceOIDC}
	require.NoError(t, svc.Create(context.Background(), u, ""))
}

func TestUserService_ChangePassword_MinLengthZero_NoEnforcement(t *testing.T) {
	// Unwired auth.Service (min length 0) must not reject anything — this is
	// what keeps the existing fixtures compiling unchanged.
	u := sourceUserFixture("id-1", "alice", "old-password", domain.UserSourceLocal)
	svc, _ := newPasswordSvc(t, []*domain.User{u}, 0)

	require.NoError(t, svc.ChangePassword(context.Background(), "alice", "old-password", "x"))
}

// ── non-local sources ─────────────────────────────────────────

func TestUserService_ChangePassword_RejectsNonLocalSource(t *testing.T) {
	for _, source := range []domain.UserSource{
		domain.UserSourceLDAP,
		domain.UserSourceOIDC,
		domain.UserSourceSAML,
	} {
		t.Run(string(source), func(t *testing.T) {
			u := sourceUserFixture("id-1", "sso-user", "old-password", source)
			svc, userRepo := newPasswordSvc(t, []*domain.User{u}, 8)

			// Even with the correct "old" password, an SSO/LDAP account has no
			// writable local credential — the login path never checks the hash.
			err := svc.ChangePassword(context.Background(), "sso-user", "old-password", "fresh-password")
			require.Error(t, err)
			assert.ErrorIs(t, err, service.ErrPasswordManagedExternally)

			stored, err := userRepo.Get(context.Background(), "sso-user")
			require.NoError(t, err)
			assert.Equal(t, u.PasswordHash, stored.PasswordHash,
				"UpdatePassword must not be reached for a non-local account")
		})
	}
}

func TestUserService_SetPassword_RejectsNonLocalSource(t *testing.T) {
	for _, source := range []domain.UserSource{
		domain.UserSourceLDAP,
		domain.UserSourceOIDC,
		domain.UserSourceSAML,
	} {
		t.Run(string(source), func(t *testing.T) {
			// No local hash at all: an admin reset used to write one that the
			// login flow would never check — a silent lie, now a clear error.
			u := &domain.User{
				ID:       "id-1",
				Username: "sso-user",
				Status:   domain.UserStatusActive,
				Source:   source,
			}
			svc, userRepo := newPasswordSvc(t, []*domain.User{u}, 8)

			err := svc.SetPassword(context.Background(), "sso-user", "fresh-password")
			require.Error(t, err)
			assert.ErrorIs(t, err, service.ErrPasswordManagedExternally)

			stored, err := userRepo.Get(context.Background(), "sso-user")
			require.NoError(t, err)
			assert.Empty(t, stored.PasswordHash,
				"UpdatePassword must not be reached for a non-local account")
		})
	}
}
