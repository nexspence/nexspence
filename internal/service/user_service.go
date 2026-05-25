package service

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// UserService handles user management and authentication.
type UserService struct {
	users          repository.UserRepo
	roles          repository.RoleRepo
	auth           *auth.Service
	ldap    auth.LDAPAuthenticator // nil when LDAP is disabled
	ldapCfg config.LDAPConfig     // empty when LDAP is disabled
	oidc           auth.OIDCAuthenticator // nil when OIDC is disabled
	oidcCfg        config.OIDCConfig      // empty when OIDC is disabled
	saml           auth.SAMLAuthenticator // nil when SAML is disabled
	samlCfg        config.SAMLConfig      // empty when SAML is disabled
	log            logger.Logger
}

func NewUserService(
	users repository.UserRepo,
	roles repository.RoleRepo,
	auth *auth.Service,
	log logger.Logger,
) *UserService {
	return &UserService{users: users, roles: roles, auth: auth, log: log}
}

// WithLDAP attaches an LDAP authenticator and its config.
// Returns the same service for chaining.
func (s *UserService) WithLDAP(l auth.LDAPAuthenticator, cfg config.LDAPConfig) *UserService {
	s.ldap = l
	s.ldapCfg = cfg
	return s
}

// WithOIDC attaches an OIDC authenticator and its config.
// Returns the same service for chaining.
func (s *UserService) WithOIDC(a auth.OIDCAuthenticator, cfg config.OIDCConfig) *UserService {
	s.oidc = a
	s.oidcCfg = cfg
	return s
}

// WithSAML attaches a SAML authenticator and its config.
func (s *UserService) WithSAML(a auth.SAMLAuthenticator, cfg config.SAMLConfig) *UserService {
	s.saml = a
	s.samlCfg = cfg
	return s
}

// Login validates credentials and returns a JWT token.
// If LDAP is enabled, users with source=ldap (or unknown users) are authenticated
// against the LDAP server; on success their local record is created/updated.
func (s *UserService) Login(ctx context.Context, username, password string) (string, *domain.User, error) {
	u, err := s.users.Get(ctx, username)
	if err != nil {
		return "", nil, err
	}

	// Route to LDAP when: user is unknown and LDAP is on, or user has source=ldap.
	if s.ldap != nil && (u == nil || u.Source == domain.UserSourceLDAP) {
		// Normalize username to lowercase for LDAP to prevent duplicate rows
		// caused by different capitalizations (e.g. "svcDevOps" vs "svcdevops").
		normalized := strings.ToLower(username)
		if u == nil && normalized != username {
			u, err = s.users.Get(ctx, normalized)
			if err != nil {
				return "", nil, err
			}
		}
		return s.loginLDAP(ctx, normalized, password, u)
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

// loginLDAP authenticates via LDAP and upserts the local user record.
func (s *UserService) loginLDAP(ctx context.Context, username, password string, existing *domain.User) (string, *domain.User, error) {
	lu, err := s.ldap.Authenticate(ctx, username, password)
	if err != nil {
		return "", nil, fmt.Errorf("LDAP authentication failed: %w", err)
	}

	if existing == nil {
		// Auto-create local record for the LDAP user.
		existing = &domain.User{
			Username:  username,
			Email:     lu.Email,
			FirstName: lu.FirstName,
			LastName:  lu.LastName,
			Status:    domain.UserStatusActive,
			Source:    domain.UserSourceLDAP,
		}
		if err := s.users.Create(ctx, existing); err != nil {
			return "", nil, fmt.Errorf("create ldap user: %w", err)
		}
	} else {
		// Keep profile in sync with LDAP directory.
		if lu.Email != "" {
			existing.Email = lu.Email
		}
		if lu.FirstName != "" {
			existing.FirstName = lu.FirstName
		}
		if lu.LastName != "" {
			existing.LastName = lu.LastName
		}
		_ = s.users.Update(ctx, existing)
	}

	if existing.Status != domain.UserStatusActive {
		return "", nil, fmt.Errorf("%w: user account is not active", ErrInvalidInput)
	}

	if lu.GroupSearchErr != "" {
		s.log.Warnw("ldap group search failed", "username", username, "err", lu.GroupSearchErr)
	}
	s.log.Infow("ldap user authenticated", "username", username, "ldap_groups", lu.Groups, "user_dn", lu.DN)

	// Best-effort: sync roles from LDAP groups (by name, role_mappings, admin_group).
	if err := s.syncLDAPRoles(ctx, existing.ID, lu.Groups); err != nil {
		s.log.Warnw("syncLDAPRoles failed", "username", username, "err", err)
	}

	// Reload roles so the JWT reflects any just-granted nx-admin role.
	if fresh, err2 := s.roles.GetUserRoles(ctx, existing.ID); err2 == nil {
		names := make([]string, 0, len(fresh))
		for _, r := range fresh {
			names = append(names, r.Name)
		}
		existing.Roles = names
	}

	s.log.Infow("ldap login complete", "username", username, "roles", existing.Roles)

	token, err := s.auth.GenerateToken(existing.ID, existing.Username, existing.Roles)
	if err != nil {
		return "", nil, err
	}
	_ = s.users.UpdateLastLogin(ctx, username)
	return token, existing, nil
}

// syncLDAPRoles replaces the user's roles with those derived from LDAP group membership.
// Resolution order (all applied, REPLACE semantics):
//  1. admin_group match → nx-admin
//  2. role_mappings[group] → mapped role name
//  3. group name matches a role name exactly → that role
func (s *UserService) syncLDAPRoles(ctx context.Context, userID string, ldapGroups []string) error {
	want := make(map[string]struct{})
	for _, g := range ldapGroups {
		// 1. admin group
		if s.ldapCfg.AdminGroup != "" && ldapGroupMatch(g, s.ldapCfg.AdminGroup) {
			want["nx-admin"] = struct{}{}
		}
		// 2. explicit role_mappings
		for mapKey, roleName := range s.ldapCfg.RoleMappings {
			if roleName != "" && ldapGroupMatch(g, mapKey) {
				want[roleName] = struct{}{}
			}
		}
		// 3. implicit by-name: group name = role name
		want[g] = struct{}{}
	}

	allRoles, err := s.roles.List(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(want))
	for _, r := range allRoles {
		if _, ok := want[r.Name]; ok {
			ids = append(ids, r.ID)
		}
	}
	s.log.Infow("ldap role sync", "user", userID, "ldap_groups", ldapGroups, "assigned_role_ids", ids)
	return s.roles.SetUserRoles(ctx, userID, ids)
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

	if err := s.users.Create(ctx, u); err != nil {
		return err
	}
	if len(u.Roles) > 0 {
		if err := s.roles.SetUserRoles(ctx, u.ID, u.Roles); err != nil {
			return err
		}
	}
	return nil
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

// LoginOIDC upserts the user and assigns roles based on OIDC claims.
// Roles are REPLACED (not merged) on every login — IdP is source of truth.
func (s *UserService) LoginOIDC(ctx context.Context, claims *auth.OIDCClaims, rawIDToken string) (string, *domain.User, error) {
	if s.oidc == nil {
		return "", nil, fmt.Errorf("%w: oidc not configured", ErrInvalidInput)
	}
	username := strings.ToLower(strings.TrimSpace(claims.Username))
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if username == "" || email == "" {
		return "", nil, fmt.Errorf("%w: claims missing username or email", ErrInvalidInput)
	}

	existing, err := s.users.Get(ctx, username)
	if err != nil {
		return "", nil, err
	}
	if existing != nil && existing.Source != domain.UserSourceOIDC {
		return "", nil, fmt.Errorf("%w: username %q is claimed by %s",
			ErrProvisioningConflict, username, existing.Source)
	}

	if existing == nil {
		if err := s.checkProvisioning(email); err != nil {
			return "", nil, err
		}
		existing = &domain.User{
			Username:  username,
			Email:     email,
			FirstName: claims.FirstName,
			LastName:  claims.LastName,
			Status:    domain.UserStatusActive,
			Source:    domain.UserSourceOIDC,
		}
		if err := s.users.Create(ctx, existing); err != nil {
			return "", nil, fmt.Errorf("create oidc user: %w", err)
		}
	} else {
		existing.Email = email
		existing.FirstName = claims.FirstName
		existing.LastName = claims.LastName
		_ = s.users.Update(ctx, existing)
	}

	if existing.Status != domain.UserStatusActive {
		return "", nil, fmt.Errorf("%w: user account is not active", ErrInvalidInput)
	}

	if err := s.syncOIDCRoles(ctx, existing.ID, claims.Groups); err != nil {
		s.log.Warnw("syncOIDCRoles failed", "username", username, "err", err)
	}

	// Reload roles from DB — SetUserRoles does not update in-memory user.
	if fresh, err2 := s.roles.GetUserRoles(ctx, existing.ID); err2 == nil {
		names := make([]string, 0, len(fresh))
		for _, r := range fresh {
			names = append(names, r.Name)
		}
		existing.Roles = names
	}

	s.log.Infow("oidc login complete", "username", username, "roles", existing.Roles)

	token, err := s.auth.GenerateTokenWithMethod(existing.ID, existing.Username, existing.Roles, "oidc")
	if err != nil {
		return "", nil, err
	}
	if err2 := s.users.SetOIDCTokens(ctx, existing.ID, rawIDToken, ""); err2 != nil {
		s.log.Warnw("SetOIDCTokens failed", "username", username, "err", err2)
	}
	_ = s.users.UpdateLastLogin(ctx, username)
	return token, existing, nil
}

// checkProvisioning gates new-user creation based on oidc.provisioning mode.
func (s *UserService) checkProvisioning(email string) error {
	mode := s.oidcCfg.Provisioning
	if mode == "" {
		mode = "jit"
	}
	switch mode {
	case "jit":
		return nil
	case "allowlist":
		for _, pat := range s.oidcCfg.EmailAllowlist {
			ok, _ := path.Match(strings.ToLower(pat), email)
			if ok {
				return nil
			}
		}
		return fmt.Errorf("%w: email %q not in allowlist", ErrProvisioningRejected, email)
	case "manual":
		return fmt.Errorf("%w: user must be pre-created by an admin", ErrProvisioningRejected)
	default:
		return fmt.Errorf("%w: unknown provisioning mode %q", ErrInvalidInput, mode)
	}
}

// syncOIDCRoles replaces the user's roles with those derived from claims.
// Collection: admin_group match → nx-admin; role_mappings lookup by claim value
// (with DN-aware comparison via oidcGroupMatch) → mapped role name.
func (s *UserService) syncOIDCRoles(ctx context.Context, userID string, groups []string) error {
	want := make(map[string]struct{})
	for _, g := range groups {
		if s.oidcCfg.AdminGroup != "" && oidcGroupMatch(g, s.oidcCfg.AdminGroup) {
			want["nx-admin"] = struct{}{}
		}
		for mapKey, roleName := range s.oidcCfg.RoleMappings {
			if roleName != "" && oidcGroupMatch(g, mapKey) {
				want[roleName] = struct{}{}
			}
		}
	}

	allRoles, err := s.roles.List(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(want))
	matched := make(map[string]bool)
	for _, r := range allRoles {
		if _, ok := want[r.Name]; ok {
			ids = append(ids, r.ID)
			matched[r.Name] = true
		}
	}
	for name := range want {
		if !matched[name] {
			s.log.Warnw("oidc role mapping references unknown role", "role", name)
		}
	}
	return s.roles.SetUserRoles(ctx, userID, ids)
}

// oidcGroupMatch is bidirectional: both args may be plain names or DN-encoded
// ("CN=value,OU=..."). The first RDN value of a DN is compared.
func oidcGroupMatch(a, b string) bool {
	return strings.EqualFold(stripCNPrefix(a), stripCNPrefix(b))
}

func stripCNPrefix(s string) string {
	if idx := strings.IndexByte(s, '='); idx >= 0 {
		val := s[idx+1:]
		if end := strings.IndexByte(val, ','); end >= 0 {
			val = val[:end]
		}
		return val
	}
	return s
}

// ldapGroupMatch compares a group name returned by LDAP against the configured
// admin_group value. It handles two forms:
//   - plain name:  "nexus-administrators"
//   - full LDAP DN: "CN=nexus-administrators,OU=…,DC=…" — only the first RDN value is used
//
// Comparison is case-insensitive in both cases.
func ldapGroupMatch(name, configured string) bool {
	if strings.EqualFold(name, configured) {
		return true
	}
	// Extract value of the first RDN attribute from a DN (e.g. "CN=foo,OU=bar" → "foo").
	if idx := strings.IndexByte(configured, '='); idx >= 0 {
		val := configured[idx+1:]
		if end := strings.IndexByte(val, ','); end >= 0 {
			val = val[:end]
		}
		if strings.EqualFold(name, val) {
			return true
		}
	}
	return false
}

// LoginSAML upserts the user and assigns roles based on SAML assertion claims.
// Roles are REPLACED on every login — IdP is source of truth.
func (s *UserService) LoginSAML(ctx context.Context, claims *auth.SAMLClaims) (string, *domain.User, error) {
	if s.saml == nil {
		return "", nil, fmt.Errorf("%w: saml not configured", ErrInvalidInput)
	}
	username := strings.ToLower(strings.TrimSpace(claims.Username))
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if username == "" || email == "" {
		return "", nil, fmt.Errorf("%w: saml claims missing username or email", ErrInvalidInput)
	}

	existing, err := s.users.Get(ctx, username)
	if err != nil {
		return "", nil, err
	}
	if existing != nil && existing.Source != domain.UserSourceSAML {
		return "", nil, fmt.Errorf("%w: username %q is claimed by %s",
			ErrProvisioningConflict, username, existing.Source)
	}

	if existing == nil {
		if err := s.checkSAMLProvisioning(email); err != nil {
			return "", nil, err
		}
		existing = &domain.User{
			Username:  username,
			Email:     email,
			FirstName: claims.Name, // store display name in FirstName
			Status:    domain.UserStatusActive,
			Source:    domain.UserSourceSAML,
		}
		if err := s.users.Create(ctx, existing); err != nil {
			return "", nil, fmt.Errorf("create saml user: %w", err)
		}
	} else {
		existing.Email = email
		existing.FirstName = claims.Name
		_ = s.users.Update(ctx, existing)
	}

	if existing.Status != domain.UserStatusActive {
		return "", nil, fmt.Errorf("%w: user account is not active", ErrInvalidInput)
	}

	if err := s.syncSAMLRoles(ctx, existing.ID, claims.Groups); err != nil {
		s.log.Warnw("syncSAMLRoles failed", "username", username, "err", err)
	}

	// Reload roles from DB — SetUserRoles does not update the in-memory user.
	if fresh, err2 := s.roles.GetUserRoles(ctx, existing.ID); err2 == nil {
		names := make([]string, 0, len(fresh))
		for _, r := range fresh {
			names = append(names, r.Name)
		}
		existing.Roles = names
	}

	s.log.Infow("saml login complete", "username", username, "roles", existing.Roles)

	token, err := s.auth.GenerateTokenWithMethod(existing.ID, existing.Username, existing.Roles, "saml")
	if err != nil {
		return "", nil, err
	}
	_ = s.users.UpdateLastLogin(ctx, username)
	return token, existing, nil
}

// checkSAMLProvisioning gates new-user creation based on saml.provisioning mode.
func (s *UserService) checkSAMLProvisioning(email string) error {
	mode := s.samlCfg.Provisioning
	if mode == "" {
		mode = "jit"
	}
	switch mode {
	case "jit":
		return nil
	case "allowlist":
		for _, pat := range s.samlCfg.EmailAllowlist {
			ok, _ := path.Match(strings.ToLower(pat), email)
			if ok {
				return nil
			}
		}
		return fmt.Errorf("%w: email %q not in allowlist", ErrProvisioningRejected, email)
	case "manual":
		return fmt.Errorf("%w: user must be pre-created by an admin", ErrProvisioningRejected)
	default:
		return fmt.Errorf("%w: unknown saml provisioning mode %q", ErrInvalidInput, mode)
	}
}

// syncSAMLRoles replaces the user's roles derived from SAML groups.
// REPLACE semantics: IdP is source of truth.
func (s *UserService) syncSAMLRoles(ctx context.Context, userID string, groups []string) error {
	want := make(map[string]struct{})
	for _, g := range groups {
		if s.samlCfg.AdminGroup != "" && strings.EqualFold(g, s.samlCfg.AdminGroup) {
			want["nx-admin"] = struct{}{}
		}
		for mapKey, roleName := range s.samlCfg.RoleMappings {
			if roleName != "" && strings.EqualFold(g, mapKey) {
				want[roleName] = struct{}{}
			}
		}
	}

	allRoles, err := s.roles.List(ctx)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(want))
	matched := make(map[string]bool)
	for _, r := range allRoles {
		if _, ok := want[r.Name]; ok {
			ids = append(ids, r.ID)
			matched[r.Name] = true
		}
	}
	for name := range want {
		if !matched[name] {
			s.log.Warnw("saml role mapping references unknown role", "role", name)
		}
	}
	return s.roles.SetUserRoles(ctx, userID, ids)
}
