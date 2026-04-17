package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	HTTP      HTTPConfig      `mapstructure:"http"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Storage   StorageConfig   `mapstructure:"storage"`
	Auth      AuthConfig      `mapstructure:"auth"`
	Bootstrap BootstrapConfig `mapstructure:"bootstrap"`
	Log       LogConfig       `mapstructure:"log"`
	Search    SearchConfig    `mapstructure:"search"`
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
	DSN         string `mapstructure:"dsn"`
	MaxConns    int    `mapstructure:"max_conns"`
	MinConns    int    `mapstructure:"min_conns"`
	MaxIdleSec  int    `mapstructure:"max_idle_sec"`
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
	JWTSecret          string `mapstructure:"jwt_secret"`
	JWTExpiryHours     int    `mapstructure:"jwt_expiry_hours"`
	AnonymousEnabled   bool   `mapstructure:"anonymous_enabled"`
	PasswordMinLength  int    `mapstructure:"password_min_length"`
	BcryptCost         int    `mapstructure:"bcrypt_cost"`
}

type LogConfig struct {
	Level  string `mapstructure:"level"`  // debug, info, warn, error
	Format string `mapstructure:"format"` // json, text
}

type SearchConfig struct {
	// Full-text search is built into PostgreSQL — no external deps
	// MinQueryLen is the minimum characters before trigram search kicks in
	MinQueryLen int `mapstructure:"min_query_len"`
}

func Load(path string) (*Config, error) {
	v := viper.New()

	// Defaults
	v.SetDefault("http.addr", ":8081")
	v.SetDefault("http.read_timeout_sec", 1800)
	v.SetDefault("http.write_timeout_sec", 1800)
	v.SetDefault("http.max_body_mb", 1024)
	v.SetDefault("http.base_url", "http://localhost:8081")
	v.SetDefault("database.max_conns", 100)
	v.SetDefault("database.min_conns", 5)
	v.SetDefault("database.max_idle_sec", 300)
	v.SetDefault("storage.default_type", "local")
	v.SetDefault("storage.local.base_path", "./data/blobs")
	v.SetDefault("auth.jwt_expiry_hours", 24)
	v.SetDefault("auth.anonymous_enabled", true)
	v.SetDefault("auth.password_min_length", 8)
	v.SetDefault("auth.bcrypt_cost", 12)
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")
	v.SetDefault("search.min_query_len", 2)
	v.SetDefault("bootstrap.admin_username", "admin")
	v.SetDefault("bootstrap.admin_password", "admin123")
	v.SetDefault("bootstrap.admin_email", "admin@example.com")
	v.SetDefault("bootstrap.admin_first_name", "Admin")

	// Config file
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	// Env override: NEXSPENCE_DATABASE_DSN, NEXSPENCE_AUTH_JWT_SECRET, etc.
	v.SetEnvPrefix("NEXSPENCE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		// Config file is optional if all required values come from env
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
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

	return &cfg, nil
}
