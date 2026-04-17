package service

import (
	"context"
	"fmt"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// UserService handles user management and authentication.
type UserService struct {
	users repository.UserRepo
	roles repository.RoleRepo
	auth  *auth.Service
}

func NewUserService(
	users repository.UserRepo,
	roles repository.RoleRepo,
	auth *auth.Service,
) *UserService {
	return &UserService{users: users, roles: roles, auth: auth}
}

// Login validates credentials and returns a JWT token.
func (s *UserService) Login(ctx context.Context, username, password string) (string, *domain.User, error) {
	u, err := s.users.Get(ctx, username)
	if err != nil {
		return "", nil, err
	}
	if u == nil {
		return "", nil, fmt.Errorf("%w: user %q", ErrNotFound, username)
	}
	if u.Status != domain.UserStatusActive {
		return "", nil, fmt.Errorf("%w: user account is not active", ErrInvalidInput)
	}
	if err := s.auth.CheckPassword(u.PasswordHash, password); err != nil {
		return "", nil, err
	}

	token, err := s.auth.GenerateToken(u.ID, u.Username, u.Roles)
	if err != nil {
		return "", nil, err
	}

	_ = s.users.UpdateLastLogin(ctx, username)
	return token, u, nil
}

// ValidateToken validates a JWT and returns the embedded claims.
func (s *UserService) ValidateToken(tokenStr string) (*auth.Claims, error) {
	return s.auth.ValidateToken(tokenStr)
}

func (s *UserService) List(ctx context.Context, source string) ([]domain.User, error) {
	return s.users.List(ctx, source)
}

func (s *UserService) Get(ctx context.Context, username string) (*domain.User, error) {
	u, err := s.users.Get(ctx, username)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, fmt.Errorf("%w: user %q", ErrNotFound, username)
	}
	return u, nil
}

func (s *UserService) GetByID(ctx context.Context, id string) (*domain.User, error) {
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, fmt.Errorf("%w: user id %q", ErrNotFound, id)
	}
	return u, nil
}

func (s *UserService) Create(ctx context.Context, u *domain.User, plainPassword string) error {
	if u.Username == "" {
		return fmt.Errorf("%w: username is required", ErrInvalidInput)
	}

	existing, err := s.users.Get(ctx, u.Username)
	if err != nil {
		return err
	}
	if existing != nil {
		return fmt.Errorf("%w: user %q", ErrAlreadyExists, u.Username)
	}

	if plainPassword != "" {
		hash, err := s.auth.HashPassword(plainPassword)
		if err != nil {
			return err
		}
		u.PasswordHash = hash
	}

	if u.Status == "" {
		u.Status = domain.UserStatusActive
	}
	if u.Source == "" {
		u.Source = domain.UserSourceLocal
	}

	return s.users.Create(ctx, u)
}

func (s *UserService) Update(ctx context.Context, username string, updates *domain.User) (*domain.User, error) {
	u, err := s.Get(ctx, username)
	if err != nil {
		return nil, err
	}

	if updates.Email != "" {
		u.Email = updates.Email
	}
	if updates.FirstName != "" {
		u.FirstName = updates.FirstName
	}
	if updates.LastName != "" {
		u.LastName = updates.LastName
	}
	if updates.Status != "" {
		u.Status = updates.Status
	}

	if err := s.users.Update(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *UserService) ChangePassword(ctx context.Context, username, oldPassword, newPassword string) error {
	u, err := s.Get(ctx, username)
	if err != nil {
		return err
	}
	if err := s.auth.CheckPassword(u.PasswordHash, oldPassword); err != nil {
		return err
	}
	hash, err := s.auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.users.UpdatePassword(ctx, username, hash)
}

func (s *UserService) SetPassword(ctx context.Context, username, newPassword string) error {
	_, err := s.Get(ctx, username)
	if err != nil {
		return err
	}
	hash, err := s.auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.users.UpdatePassword(ctx, username, hash)
}

func (s *UserService) Delete(ctx context.Context, username string) error {
	_, err := s.Get(ctx, username)
	if err != nil {
		return err
	}
	return s.users.Delete(ctx, username)
}

func (s *UserService) GetUserRoles(ctx context.Context, userID string) ([]domain.Role, error) {
	return s.roles.GetUserRoles(ctx, userID)
}

func (s *UserService) SetUserRoles(ctx context.Context, userID string, roleIDs []string) error {
	return s.roles.SetUserRoles(ctx, userID, roleIDs)
}
