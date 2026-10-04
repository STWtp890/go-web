// Package config loads and validates the document service configuration.
//
// Configuration is parsed once at startup and fails closed: a missing boundary
// key, database DSN or listener address stops the process instead of degrading
// into an unauthenticated or unbounded service.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the complete document service configuration.
type Config struct {
	GRPCListenAddress string         `yaml:"grpc_listen_address"`
	HTTPListenAddress string         `yaml:"http_listen_address"`
	Postgres          PostgresConfig `yaml:"postgres"`
	Auth              AuthConfig     `yaml:"auth"`
	Outbox            OutboxConfig   `yaml:"outbox"`
	ShutdownTimeout   string         `yaml:"shutdown_timeout"`
}

// PostgresConfig points at the service's own schema and write account.
type PostgresConfig struct {
	DSN             string `yaml:"dsn"`
	Schema          string `yaml:"schema"`
	MaxOpenConns    int32  `yaml:"max_open_conns"`
	MaxIdleConns    int32  `yaml:"max_idle_conns"`
	ConnMaxLifetime string `yaml:"conn_max_lifetime"`
	ConnectTimeout  string `yaml:"connect_timeout"`
}

// AuthConfig holds the trusted entry point material.
type AuthConfig struct {
	// CapabilityKeyFile is the shared boundary key. It must not be inlined into
	// configuration files that are committed.
	CapabilityKeyFile string `yaml:"capability_key_file"`
	// Audience is this service's audience; a credential minted for another
	// service must fail as unauthenticated here.
	Audience string `yaml:"audience"`
	// AllowedCallers restricts which services may call this entry point.
	AllowedCallers []string `yaml:"allowed_callers"`
	// CapabilityTTL bounds how long a minted resource capability stays valid.
	CapabilityTTL string `yaml:"capability_ttl"`
	// Issuer is the issuer name written into minted capabilities.
	Issuer string `yaml:"issuer"`
	// EnableReflection exposes the gRPC reflection API. It is off by default and
	// only turned on for local debugging, because it lets any caller enumerate
	// the administrative surface of the fact source.
	EnableReflection bool `yaml:"enable_reflection"`
}

// OutboxConfig bounds the event stream the search consumer reads.
type OutboxConfig struct {
	MaxBatchSize int `yaml:"max_batch_size"`
	// PollInterval is how often a following stream re-reads the Outbox.
	PollInterval string `yaml:"poll_interval"`
}

// Default returns the development defaults. They are safe to run locally and are
// overridden by the mounted configuration file and environment variables.
func Default() Config {
	return Config{
		GRPCListenAddress: "127.0.0.1:18081",
		HTTPListenAddress: "127.0.0.1:18091",
		Postgres: PostgresConfig{
			Schema:          "document_service",
			MaxOpenConns:    16,
			MaxIdleConns:    4,
			ConnMaxLifetime: "30m",
			ConnectTimeout:  "10s",
		},
		Auth: AuthConfig{
			Audience:      "document-service",
			Issuer:        "document-service",
			CapabilityTTL: "2m",
		},
		Outbox:          OutboxConfig{MaxBatchSize: 200, PollInterval: "500ms"},
		ShutdownTimeout: "10s",
	}
}

// Load reads a YAML file over the defaults and applies environment overrides.
// A missing file is not an error: the container supplies either a file or
// environment variables, and a development run may use neither.
func Load(path string) (Config, error) {
	config := Default()
	if strings.TrimSpace(path) != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return Config{}, fmt.Errorf("document-service config: read %s: %w", path, err)
			}
		} else {
			decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
			decoder.KnownFields(true)
			if err := decoder.Decode(&config); err != nil {
				return Config{}, fmt.Errorf("document-service config: parse %s: %w", path, err)
			}
		}
	}
	applyEnvironment(&config)
	return config, nil
}

func applyEnvironment(config *Config) {
	setString(&config.GRPCListenAddress, "DOCUMENT_SERVICE_GRPC_LISTEN_ADDRESS")
	setString(&config.HTTPListenAddress, "DOCUMENT_SERVICE_HTTP_LISTEN_ADDRESS")
	setString(&config.Postgres.DSN, "DOCUMENT_SERVICE_POSTGRES_DSN")
	setString(&config.Postgres.Schema, "DOCUMENT_SERVICE_POSTGRES_SCHEMA")
	setString(&config.Auth.CapabilityKeyFile, "DOCUMENT_SERVICE_CAPABILITY_KEY_FILE")
	setString(&config.Auth.Audience, "DOCUMENT_SERVICE_AUDIENCE")
	setString(&config.Auth.Issuer, "DOCUMENT_SERVICE_ISSUER")
	setString(&config.Auth.CapabilityTTL, "DOCUMENT_SERVICE_CAPABILITY_TTL")
	setString(&config.ShutdownTimeout, "DOCUMENT_SERVICE_SHUTDOWN_TIMEOUT")
	if callers := strings.TrimSpace(os.Getenv("DOCUMENT_SERVICE_ALLOWED_CALLERS")); callers != "" {
		config.Auth.AllowedCallers = splitAndTrim(callers)
	}
}

func setString(target *string, name string) {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		*target = value
	}
}

func splitAndTrim(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// Validate fails closed on anything the service cannot run without.
func (config Config) Validate() error {
	if strings.TrimSpace(config.GRPCListenAddress) == "" {
		return errors.New("document-service config: grpc_listen_address is required")
	}
	if strings.TrimSpace(config.HTTPListenAddress) == "" {
		return errors.New("document-service config: http_listen_address is required")
	}
	if strings.TrimSpace(config.Postgres.DSN) == "" {
		return errors.New("document-service config: postgres.dsn is required")
	}
	if strings.TrimSpace(config.Postgres.Schema) == "" {
		return errors.New("document-service config: postgres.schema is required")
	}
	if strings.TrimSpace(config.Auth.Audience) == "" {
		return errors.New("document-service config: auth.audience is required")
	}
	if strings.TrimSpace(config.Auth.CapabilityKeyFile) == "" {
		return errors.New("document-service config: auth.capability_key_file is required")
	}
	for _, caller := range config.Auth.AllowedCallers {
		switch caller {
		case "go-web", "py-agent", "document-service", "spacectl":
		default:
			return fmt.Errorf("document-service config: auth.allowed_callers contains unregistered caller %q", caller)
		}
	}
	if _, err := config.OutboxConfig(); err != nil {
		return err
	}
	if _, err := config.CapabilityTTL(); err != nil {
		return err
	}
	if _, err := config.Shutdown(); err != nil {
		return err
	}
	return nil
}

// OutboxConfig validates and returns the Outbox settings.
func (config Config) OutboxConfig() (OutboxConfig, error) {
	if config.Outbox.MaxBatchSize <= 0 || config.Outbox.MaxBatchSize > 1000 {
		return OutboxConfig{}, errors.New("document-service config: outbox.max_batch_size must be between 1 and 1000")
	}
	if _, err := time.ParseDuration(config.Outbox.PollInterval); err != nil {
		return OutboxConfig{}, fmt.Errorf("document-service config: outbox.poll_interval: %w", err)
	}
	return config.Outbox, nil
}

// CapabilityTTL validates and returns the capability lifetime.
func (config Config) CapabilityTTL() (time.Duration, error) {
	ttl, err := time.ParseDuration(config.Auth.CapabilityTTL)
	if err != nil {
		return 0, fmt.Errorf("document-service config: auth.capability_ttl: %w", err)
	}
	if ttl <= 0 || ttl > time.Hour {
		return 0, errors.New("document-service config: auth.capability_ttl must be positive and at most 1h")
	}
	return ttl, nil
}

// Shutdown validates and returns the shutdown grace period.
func (config Config) Shutdown() (time.Duration, error) {
	timeout, err := time.ParseDuration(config.ShutdownTimeout)
	if err != nil {
		return 0, fmt.Errorf("document-service config: shutdown_timeout: %w", err)
	}
	if timeout <= 0 {
		return 0, errors.New("document-service config: shutdown_timeout must be positive")
	}
	return timeout, nil
}

// BoundaryKey reads the shared boundary key material. A short or unreadable key
// is a startup failure.
func (config Config) BoundaryKey() ([]byte, error) {
	path := strings.TrimSpace(config.Auth.CapabilityKeyFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("document-service config: read boundary key %s: %w", path, err)
	}
	key := []byte(strings.TrimSpace(string(raw)))
	if len(key) < 32 {
		return nil, fmt.Errorf("document-service config: boundary key %s must be at least 32 bytes", path)
	}
	return key, nil
}
