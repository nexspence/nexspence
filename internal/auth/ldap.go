package auth

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/nexspence-oss/nexspence/internal/config"
)

// LDAPUser holds the attributes returned after a successful LDAP bind.
type LDAPUser struct {
	DN             string
	Username       string
	Email          string
	FirstName      string
	LastName       string
	Groups         []string // group names (mapped via GroupAttribute)
	GroupSearchErr string   // non-empty when group search failed (diagnostic only)
}

// LDAPAuthenticator is the interface for LDAP operations (enables mocking in tests).
type LDAPAuthenticator interface {
	// Authenticate performs a bind with the given credentials and returns user attributes.
	Authenticate(ctx context.Context, username, password string) (*LDAPUser, error)
	// TestConnection verifies that the server is reachable and the service bind works.
	TestConnection(ctx context.Context) error
}

// LDAPService implements LDAPAuthenticator against a real LDAP/AD server.
type LDAPService struct {
	cfg config.LDAPConfig
}

// NewLDAPService returns an LDAPService configured from cfg.
// Returns nil if cfg.Enabled is false so callers can check for nil.
func NewLDAPService(cfg config.LDAPConfig) *LDAPService {
	if !cfg.Enabled {
		return nil
	}
	return &LDAPService{cfg: cfg}
}

func (s *LDAPService) dial(ctx context.Context) (*ldap.Conn, error) {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	timeout := time.Duration(s.cfg.TimeoutSec) * time.Second

	tlsCfg := &tls.Config{
		InsecureSkipVerify: s.cfg.InsecureSkipVerify, //nolint:gosec
		ServerName:         s.cfg.Host,
	}

	var conn *ldap.Conn
	var err error
	useTLS := s.cfg.UseTLS || s.cfg.Port == 636
	if useTLS {
		conn, err = ldap.DialURL("ldaps://"+addr, ldap.DialWithTLSConfig(tlsCfg))
	} else {
		conn, err = ldap.DialURL("ldap://" + addr)
	}
	if err != nil {
		return nil, fmt.Errorf("ldap dial %s: %w", addr, err)
	}
	conn.SetTimeout(timeout)

	if s.cfg.StartTLS && !s.cfg.UseTLS {
		if err := conn.StartTLS(tlsCfg); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("ldap starttls: %w", err)
		}
	}
	return conn, nil
}

func (s *LDAPService) serviceBind(conn *ldap.Conn) error {
	if s.cfg.BindDN == "" {
		return conn.UnauthenticatedBind("")
	}
	return conn.Bind(s.cfg.BindDN, s.cfg.BindPassword)
}

// Authenticate looks up the user in LDAP and performs a bind to verify the password.
func (s *LDAPService) Authenticate(ctx context.Context, username, password string) (*LDAPUser, error) {
	if password == "" {
		return nil, fmt.Errorf("empty password rejected")
	}

	conn, err := s.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	// Service bind to search for the user DN.
	if err := s.serviceBind(conn); err != nil {
		return nil, fmt.Errorf("ldap service bind: %w", err)
	}

	filter := strings.ReplaceAll(s.cfg.SearchFilter, "{0}", ldap.EscapeFilter(username))
	attrs := []string{
		"dn",
		s.cfg.UserAttributes.Email,
		s.cfg.UserAttributes.FirstName,
		s.cfg.UserAttributes.LastName,
	}

	req := ldap.NewSearchRequest(
		s.cfg.SearchBase, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		1, s.cfg.TimeoutSec, false,
		filter, attrs, nil,
	)
	res, err := conn.Search(req)
	if err != nil {
		return nil, fmt.Errorf("ldap search: %w", err)
	}
	if len(res.Entries) == 0 {
		return nil, fmt.Errorf("user %q not found in LDAP", username)
	}

	entry := res.Entries[0]
	userDN := entry.DN

	// Bind as the user to verify password.
	if err := conn.Bind(userDN, password); err != nil {
		return nil, fmt.Errorf("ldap bind: invalid credentials")
	}

	lu := &LDAPUser{
		DN:        userDN,
		Username:  username,
		Email:     entry.GetAttributeValue(s.cfg.UserAttributes.Email),
		FirstName: entry.GetAttributeValue(s.cfg.UserAttributes.FirstName),
		LastName:  entry.GetAttributeValue(s.cfg.UserAttributes.LastName),
	}

	// Fetch group memberships (best-effort; failures are non-fatal).
	if s.cfg.GroupBase != "" && s.cfg.GroupFilter != "" {
		if err := s.serviceBind(conn); err != nil {
			// Non-fatal: log and try to search under user credentials.
			_ = fmt.Errorf("ldap group search: service rebind failed (searching as user): %w", err)
		}
		groupFilter := strings.ReplaceAll(s.cfg.GroupFilter, "{dn}", ldap.EscapeFilter(userDN))
		groupReq := ldap.NewSearchRequest(
			s.cfg.GroupBase, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
			0, s.cfg.TimeoutSec, false,
			groupFilter, []string{s.cfg.GroupAttribute}, nil,
		)
		gRes, gErr := conn.Search(groupReq)
		if gErr != nil {
			lu.GroupSearchErr = gErr.Error()
		} else {
			for _, g := range gRes.Entries {
				if name := g.GetAttributeValue(s.cfg.GroupAttribute); name != "" {
					lu.Groups = append(lu.Groups, name)
				}
			}
		}
	}

	return lu, nil
}

// TestConnection verifies reachability and service bind.
func (s *LDAPService) TestConnection(ctx context.Context) error {
	conn, err := s.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return s.serviceBind(conn)
}
