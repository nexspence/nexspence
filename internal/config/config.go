package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	HTTP      HTTPConfig      `mapstructure:"http"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Storage   StorageConfig   `mapstructure:"storage"`
	Auth      AuthConfig      `mapstructure:"auth"`
	LDAP      LDAPConfig      `mapstructure:"ldap"`
	OIDC      OIDCConfig      `mapstructure:"oidc"`
	SAML      SAMLConfig      `mapstructure:"saml"`
	Bootstrap BootstrapConfig `mapstructure:"bootstrap"`
	Log       LogConfig       `mapstructure:"log"`
	Search    SearchConfig    `mapstructure:"search"`
	Cleanup   CleanupConfig   `mapstructure:"cleanup"`
	Audit     AuditConfig     `mapstructure:"audit"`
	Docker    DockerConfig    `mapstructure:"docker"`
	Redis     RedisConfig     `mapstructure:"redis"`
}

type BootstrapConfig struct {
	AdminUsername  string `mapstructure:"admin_username"`
	AdminPassword  string `mapstructure:"admin_password"`
	AdminEmail     string `mapstructure:"admin_email"`
	AdminFirstName string `mapstructure:"admin_first_name"`
}

type HTTPConfig struct {
	Addr            string    `mapstructure:"addr"`
	ReadTimeoutSec  int       `mapstructure:"read_timeout_sec"`
	WriteTimeoutSec int       `mapstructure:"write_timeout_sec"`
	MaxBodyMB       int       `mapstructure:"max_body_mb"`
	TLS             TLSConfig `mapstructure:"tls"`
	BaseURL         string    `mapstructure:"base_url"`
}

type TLSConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
}

type DatabaseConfig struct {
	DSN        string `mapstructure:"dsn"`
	MaxConns   int    `mapstructure:"max_conns"`
	MinConns   int    `mapstructure:"min_conns"`
	MaxIdleSec int    `mapstructure:"max_idle_sec"`
}

type StorageConfig struct {
	// Default blob store type: "local" or "s3"
	DefaultType string      `mapstructure:"default_type"`
	Local       LocalConfig `mapstructure:"local"`
	S3          S3Config    `mapstructure:"s3"`
}

type LocalConfig struct {
	BasePath string `mapstructure:"base_path"`
}

type S3Config struct {
	Bucket          string `mapstructure:"bucket"`
	Region          string `mapstructure:"region"`
	Endpoint        string `mapstructure:"endpoint"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
	ForcePathStyle  bool   `mapstructure:"force_path_style"`
}

type AuthConfig struct {
	JWTSecret         string `mapstructure:"jwt_secret"`
	JWTExpiryHours    int    `mapstructure:"jwt_expiry_hours"`
	AnonymousEnabled  bool   `mapstructure:"anonymous_enabled"`
	PasswordMinLength int    `mapstructure:"password_min_length"`
	BcryptCost        int    `mapstructure:"bcrypt_cost"`
	TokenMaxDays      int    `mapstructure:"token_max_days"`
}

type LogConfig struct {
	Level  string `mapstructure:"level"`  // debug, info, warn, error
	Format string `mapstructure:"format"` // json, text
}

// LDAPConfig configures LDAP/Active Directory authentication.
type LDAPConfig struct {
	Enabled            bool            `mapstructure:"enabled"`
	Host               string          `mapstructure:"host"`
	Port               int             `mapstructure:"port"`
	UseTLS             bool            `mapstructure:"use_tls"`   // LDAPS (port 636)
	StartTLS           bool            `mapstructure:"start_tls"` // STARTTLS on plain conn
	InsecureSkipVerify bool            `mapstructure:"insecure_skip_verify"`
	BindDN             string          `mapstructure:"bind_dn"`
	BindPassword       string          `mapstructure:"bind_password"`
	SearchBase         string          `mapstructure:"search_base"`
	SearchFilter       string          `mapstructure:"search_filter"` // {0} → username
	UserAttributes     LDAPUserAttrMap `mapstructure:"user_attributes"`
	GroupBase          string          `mapstructure:"group_base"`
	GroupFilter        string          `mapstructure:"group_filter"`    // {dn} → user DN
	GroupAttribute     string          `mapstructure:"group_attribute"` // attr holding group name
	AutoCreateUsers    bool            `mapstructure:"auto_create_users"`
	TimeoutSec         int             `mapstructure:"timeout_sec"`
	// AdminGroup, when set, automatically grants the nx-admin role to any LDAP user
	// whose group membership includes this group name.
	AdminGroup string `mapstructure:"admin_group"`
	// RoleMappings maps LDAP group names to Nexspence role names (like OIDC/SAML).
	RoleMappings map[string]string `mapstructure:"role_mappings"`
}

type LDAPUserAttrMap struct {
	Email     string `mapstructure:"email"`
	FirstName string `mapstructure:"first_name"`
	LastName  string `mapstructure:"last_name"`
}

// OIDCConfig configures OIDC / OAuth2 SSO authentication.
// One provider per deployment; coexists with local + LDAP.
type OIDCConfig struct {
	Enabled         bool     `mapstructure:"enabled"`
	DisplayName     string   `mapstructure:"display_name"` // button text: "Sign in with {DisplayName}"
	Issuer          string   `mapstructure:"issuer"`
	ClientID        string   `mapstructure:"client_id"`
	ClientSecret    string   `mapstructure:"client_secret"`
	RedirectURL     string   `mapstructure:"redirect_url"`
	FrontendBaseURL string   `mapstructure:"frontend_base_url"`
	Scopes          []string `mapstructure:"scopes"`

	// Provisioning: jit (default) | allowlist | manual.
	Provisioning   string   `mapstructure:"provisioning"`
	EmailAllowlist []string `mapstructure:"email_allowlist"` // glob patterns (path.Match)

	// Role resolution.
	GroupsClaim  string            `mapstructure:"groups_claim"`  // default "groups"
	AdminGroup   string            `mapstructure:"admin_group"`   // claim value → nx-admin
	RoleMappings map[string]string `mapstructure:"role_mappings"` // claim value → Nexspence role name

	// Claim name overrides (provider-specific).
	UsernameClaim string `mapstructure:"username_claim"`
	EmailClaim    string `mapstructure:"email_claim"`
	NameClaim     string `mapstructure:"name_claim"`

	ShowLoginButton    bool   `mapstructure:"show_login_button"`
	CookieSecure       bool   `mapstructure:"cookie_secure"`
	CookieKey          string `mapstructure:"cookie_key"` // base64 32 bytes
	AllowedSkewSeconds int    `mapstructure:"allowed_skew_seconds"`

	// PublicIssuerURL, when set, replaces the internal Issuer URL in auth redirect
	// and SLO URLs sent to the browser. Needed when the IdP is reachable inside the
	// container network under a different hostname than in the browser (e.g., Keycloak
	// in Docker: internal=http://keycloak:8080/realms/x, public=http://localhost:8180/realms/x).
	// Token validation always uses the internal Issuer so iss-claim checks still pass.
	PublicIssuerURL string `mapstructure:"public_issuer_url"`
}

// ValidateOIDC returns nil when the OIDC config is usable.
// Called from Load() after unmarshal; exported for unit tests.
func ValidateOIDC(c OIDCConfig) error {
	if !c.Enabled {
		return nil
	}
	if c.Issuer == "" {
		return fmt.Errorf("oidc.issuer is required when oidc.enabled=true")
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		return fmt.Errorf("oidc.client_id and oidc.client_secret are required when oidc.enabled=true")
	}
	if c.RedirectURL == "" || c.FrontendBaseURL == "" {
		return fmt.Errorf("oidc.redirect_url and oidc.frontend_base_url are required when oidc.enabled=true")
	}
	if c.Provisioning == "allowlist" && len(c.EmailAllowlist) == 0 {
		return fmt.Errorf("oidc.email_allowlist must be non-empty when oidc.provisioning=allowlist")
	}
	keyBytes, err := base64.StdEncoding.DecodeString(c.CookieKey)
	if err != nil || len(keyBytes) != 32 {
		return fmt.Errorf("oidc.cookie_key must be base64-encoded 32 bytes")
	}
	return nil
}

// SAMLConfig configures SAML 2.0 SP-initiated SSO.
// One IdP per deployment; coexists with local, LDAP, and OIDC.
type SAMLConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	DisplayName     string `mapstructure:"display_name"`
	ShowLoginButton bool   `mapstructure:"show_login_button"`
	FrontendBaseURL string `mapstructure:"frontend_base_url"`

	// IdP metadata source — one of these is required when enabled=true.
	IDPMetadataURL string `mapstructure:"idp_metadata_url"`
	IDPMetadataXML string `mapstructure:"idp_metadata_xml"`

	// SP identity.
	SPEntityID string `mapstructure:"sp_entity_id"`
	ACSURL     string `mapstructure:"acs_url"`

	// SP signing key pair. If empty, an ephemeral RSA-2048 pair is generated at startup.
	SPCertPEM string `mapstructure:"sp_cert_pem"`
	SPKeyPEM  string `mapstructure:"sp_key_pem"`

	// Provisioning: jit (default) | allowlist | manual.
	Provisioning   string   `mapstructure:"provisioning"`
	EmailAllowlist []string `mapstructure:"email_allowlist"`

	// SAML attribute names.
	GroupsAttribute   string `mapstructure:"groups_attribute"`
	EmailAttribute    string `mapstructure:"email_attribute"`
	UsernameAttribute string `mapstructure:"username_attribute"`
	NameAttribute     string `mapstructure:"name_attribute"`

	// Role resolution.
	AdminGroup   string            `mapstructure:"admin_group"`
	RoleMappings map[string]string `mapstructure:"role_mappings"`

	// HMACKey is base64-encoded 32 bytes for signing RelayState. Auto-generated if empty.
	HMACKey string `mapstructure:"hmac_key"`
}

// ValidateSAML returns nil when the SAML config is usable.
func ValidateSAML(c SAMLConfig) error {
	if !c.Enabled {
		return nil
	}
	if c.SPEntityID == "" {
		return fmt.Errorf("saml.sp_entity_id is required when saml.enabled=true")
	}
	if c.ACSURL == "" {
		return fmt.Errorf("saml.acs_url is required when saml.enabled=true")
	}
	if c.IDPMetadataURL == "" && c.IDPMetadataXML == "" {
		return fmt.Errorf("saml.idp_metadata_url or saml.idp_metadata_xml is required when saml.enabled=true")
	}
	if c.Provisioning == "allowlist" && len(c.EmailAllowlist) == 0 {
		return fmt.Errorf("saml.email_allowlist must be non-empty when saml.provisioning=allowlist")
	}
	return nil
}

type SearchConfig struct {
	// Full-text search is built into PostgreSQL — no external deps
	// MinQueryLen is the minimum characters before trigram search kicks in
	MinQueryLen int `mapstructure:"min_query_len"`
}

type CleanupConfig struct {
	DefaultSchedule string `mapstructure:"default_schedule"`
}

type AuditConfig struct {
	RetentionDays    int           `mapstructure:"retention_days"`
	SoftCap          int64         `mapstructure:"soft_cap"`
	RotationInterval time.Duration `mapstructure:"rotation_interval"`
	LookaheadMonths  int           `mapstructure:"lookahead_months"`
}

type DockerConfig struct {
	SubdomainConnector SubdomainConnectorConfig `mapstructure:"subdomain_connector"`
}

type SubdomainConnectorConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	BaseDomain string `mapstructure:"base_domain"`
}

type RedisConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

func Load(path string) (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("http.addr", ":8081")
	v.SetDefault("http.read_timeout_sec", 1800)
	v.SetDefault("http.write_timeout_sec", 1800)
	v.SetDefault("http.max_body_mb", 1024)
	v.SetDefault("http.base_url", "http://localhost:8081")
	// Viper bug: AutomaticEnv + Unmarshal silently skips keys that have no
	// default/config-file value (not in AllKeys). Empty-string defaults ensure
	// these keys are always resolved from env vars when no config file is present.
	v.SetDefault("database.dsn", "")
	v.SetDefault("auth.jwt_secret", "")
	v.SetDefault("storage.s3.endpoint", "")
	v.SetDefault("storage.s3.bucket", "")
	v.SetDefault("storage.s3.region", "")
	v.SetDefault("storage.s3.access_key_id", "")
	v.SetDefault("storage.s3.secret_access_key", "")
	v.SetDefault("storage.s3.force_path_style", false)
	v.SetDefault("database.max_conns", 100)
	v.SetDefault("database.min_conns", 5)
	v.SetDefault("database.max_idle_sec", 300)
	v.SetDefault("storage.default_type", "local")
	v.SetDefault("storage.local.base_path", "./data/blobs")
	v.SetDefault("auth.jwt_expiry_hours", 24)
	v.SetDefault("auth.anonymous_enabled", true)
	v.SetDefault("auth.password_min_length", 8)
	v.SetDefault("auth.bcrypt_cost", 12)
	v.SetDefault("auth.token_max_days", 90)
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")
	v.SetDefault("search.min_query_len", 2)
	v.SetDefault("cleanup.default_schedule", "0 */6 * * *")
	v.SetDefault("audit.retention_days", 90)
	v.SetDefault("audit.soft_cap", int64(1_000_000))
	v.SetDefault("audit.rotation_interval", "24h")
	v.SetDefault("audit.lookahead_months", 2)
	v.SetDefault("docker.subdomain_connector.enabled", false)
	v.SetDefault("docker.subdomain_connector.base_domain", "")
	v.SetDefault("oidc.enabled", false)
	v.SetDefault("oidc.display_name", "SSO")
	v.SetDefault("oidc.public_issuer_url", "")
	v.SetDefault("oidc.scopes", []string{"openid", "profile", "email", "groups"})
	v.SetDefault("oidc.provisioning", "jit")
	v.SetDefault("oidc.groups_claim", "groups")
	v.SetDefault("oidc.username_claim", "preferred_username")
	v.SetDefault("oidc.email_claim", "email")
	v.SetDefault("oidc.name_claim", "name")
	v.SetDefault("oidc.show_login_button", true)
	v.SetDefault("oidc.cookie_secure", true)
	v.SetDefault("oidc.allowed_skew_seconds", 60)
	v.SetDefault("saml.enabled", false)
	v.SetDefault("saml.display_name", "SAML SSO")
	v.SetDefault("saml.show_login_button", true)
	v.SetDefault("saml.provisioning", "jit")
	v.SetDefault("saml.groups_attribute", "groups")
	v.SetDefault("saml.email_attribute", "email")
	v.SetDefault("saml.username_attribute", "uid")
	v.SetDefault("saml.name_attribute", "displayName")
	v.SetDefault("ldap.enabled", false)
	v.SetDefault("ldap.port", 389)
	v.SetDefault("ldap.search_filter", "(uid={0})")
	v.SetDefault("ldap.user_attributes.email", "mail")
	v.SetDefault("ldap.user_attributes.first_name", "givenName")
	v.SetDefault("ldap.user_attributes.last_name", "sn")
	v.SetDefault("ldap.group_attribute", "cn")
	v.SetDefault("ldap.auto_create_users", true)
	v.SetDefault("ldap.timeout_sec", 10)
	v.SetDefault("bootstrap.admin_username", "admin")
	v.SetDefault("bootstrap.admin_password", "admin123")
	v.SetDefault("bootstrap.admin_email", "admin@example.com")
	v.SetDefault("bootstrap.admin_first_name", "Admin")
	v.SetDefault("redis.enabled", false)
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.db", 0)

	// Config file
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	// Env override: NEXSPENCE_DATABASE_DSN, NEXSPENCE_AUTH_JWT_SECRET, etc.
	v.SetEnvPrefix("NEXSPENCE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		// Config file is optional when all required values come from env.
		// viper.ConfigFileNotFoundError: file not found via search paths.
		// *fs.PathError / errors.Is(ErrNotExist): explicit path given but file absent.
		var cfgNotFound viper.ConfigFileNotFoundError
		if !errors.As(err, &cfgNotFound) && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Database.DSN == "" {
		return nil, fmt.Errorf("database.dsn is required (or set NEXSPENCE_DATABASE_DSN)")
	}
	if cfg.Auth.JWTSecret == "" {
		return nil, fmt.Errorf("auth.jwt_secret is required (or set NEXSPENCE_AUTH_JWT_SECRET)")
	}
	if err := ValidateOIDC(cfg.OIDC); err != nil {
		return nil, err
	}
	if err := ValidateSAML(cfg.SAML); err != nil {
		return nil, err
	}

	return &cfg, nil
}
