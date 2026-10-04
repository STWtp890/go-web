// Package config loads and validates the QQ search service configuration.
//
// This service owns the raw QQ search data only. It fails closed at startup:
// without a boundary key, a database DSN and its own index namespace it does not
// start, because a QQ search service that cannot authenticate its callers would
// expose raw chat content to any caller.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the complete QQ search configuration.
type Config struct {
	GRPCListenAddress string         `yaml:"grpc_listen_address"`
	HTTPListenAddress string         `yaml:"http_listen_address"`
	Postgres          PostgresConfig `yaml:"postgres"`
	Auth              AuthConfig     `yaml:"auth"`
	Index             IndexConfig    `yaml:"index"`
	ShutdownTimeout   string         `yaml:"shutdown_timeout"`
}

// PostgresConfig points at the service's own schema and write account.
type PostgresConfig struct {
	DSN             string `yaml:"dsn"`
	Schema          string `yaml:"schema"`
	MaxOpenConns    int32  `yaml:"max_open_conns"`
	ConnMaxLifetime string `yaml:"conn_max_lifetime"`
	ConnectTimeout  string `yaml:"connect_timeout"`
}

// AuthConfig holds the boundary material.
type AuthConfig struct {
	// CapabilityKeyFile is the shared boundary key. py-agent signs both its
	// service assertion and the channel scope capability with it.
	CapabilityKeyFile string `yaml:"capability_key_file"`
	// Audience is this service's audience.
	Audience string `yaml:"audience"`
	// AllowedCallers restricts which services may call this entry point.
	AllowedCallers   []string `yaml:"allowed_callers"`
	EnableReflection bool     `yaml:"enable_reflection"`
}

// IndexConfig holds the two independent index namespaces. Messages and files must
// never share a collection: a message query returning a file would merge two data
// models that have different lifecycles.
type IndexConfig struct {
	MessageCollectionAlias string `yaml:"message_collection_alias"`
	MessageStorageDomain   string `yaml:"message_storage_domain"`
	FileCollectionAlias    string `yaml:"file_collection_alias"`
	FileStorageDomain      string `yaml:"file_storage_domain"`
	DefaultTopK            int    `yaml:"default_top_k"`
	MaxTopK                int    `yaml:"max_top_k"`
	MaxBatchSize           int    `yaml:"max_batch_size"`
}

// Default returns the development defaults.
func Default() Config {
	return Config{
		GRPCListenAddress: "127.0.0.1:18083",
		HTTPListenAddress: "127.0.0.1:18093",
		Postgres: PostgresConfig{
			Schema:          "qq_search",
			MaxOpenConns:    16,
			ConnMaxLifetime: "30m",
			ConnectTimeout:  "10s",
		},
		Auth: AuthConfig{Audience: "qq-search"},
		Index: IndexConfig{
			MessageCollectionAlias: "qq_source_messages_v1",
			MessageStorageDomain:   "qq-search:messages:v1",
			FileCollectionAlias:    "qq_source_files_v1",
			FileStorageDomain:      "qq-search:files:v1",
			DefaultTopK:            10,
			MaxTopK:                100,
			MaxBatchSize:           500,
		},
		ShutdownTimeout: "10s",
	}
}

// Load reads a YAML file over the defaults and applies environment overrides.
func Load(path string) (Config, error) {
	config := Default()
	if strings.TrimSpace(path) != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return Config{}, fmt.Errorf("qq-search config: read %s: %w", path, err)
			}
		} else {
			decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
			decoder.KnownFields(true)
			if err := decoder.Decode(&config); err != nil {
				return Config{}, fmt.Errorf("qq-search config: parse %s: %w", path, err)
			}
		}
	}
	applyEnvironment(&config)
	return config, nil
}

func applyEnvironment(config *Config) {
	setString(&config.GRPCListenAddress, "QQ_SEARCH_GRPC_LISTEN_ADDRESS")
	setString(&config.HTTPListenAddress, "QQ_SEARCH_HTTP_LISTEN_ADDRESS")
	setString(&config.Postgres.DSN, "QQ_SEARCH_POSTGRES_DSN")
	setString(&config.Postgres.Schema, "QQ_SEARCH_POSTGRES_SCHEMA")
	setString(&config.Auth.CapabilityKeyFile, "QQ_SEARCH_CAPABILITY_KEY_FILE")
	setString(&config.Auth.Audience, "QQ_SEARCH_AUDIENCE")
	setString(&config.Index.MessageCollectionAlias, "QQ_SEARCH_MESSAGE_COLLECTION_ALIAS")
	setString(&config.Index.FileCollectionAlias, "QQ_SEARCH_FILE_COLLECTION_ALIAS")
	setString(&config.ShutdownTimeout, "QQ_SEARCH_SHUTDOWN_TIMEOUT")
	if callers := strings.TrimSpace(os.Getenv("QQ_SEARCH_ALLOWED_CALLERS")); callers != "" {
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
		return errors.New("qq-search config: grpc_listen_address is required")
	}
	if strings.TrimSpace(config.HTTPListenAddress) == "" {
		return errors.New("qq-search config: http_listen_address is required")
	}
	if strings.TrimSpace(config.Postgres.DSN) == "" {
		return errors.New("qq-search config: postgres.dsn is required")
	}
	if strings.TrimSpace(config.Postgres.Schema) != "qq_search" {
		return fmt.Errorf("qq-search config: postgres.schema must be qq_search, got %q", config.Postgres.Schema)
	}
	if strings.TrimSpace(config.Auth.CapabilityKeyFile) == "" {
		return errors.New("qq-search config: auth.capability_key_file is required")
	}
	if _, err := config.Audience(); err != nil {
		return err
	}
	if _, err := config.AllowedCallers(); err != nil {
		return err
	}
	if _, err := config.IndexConfig(); err != nil {
		return err
	}
	if _, err := config.Shutdown(); err != nil {
		return err
	}
	return nil
}

// Audience validates and returns this service's audience.
func (config Config) Audience() (string, error) {
	audience := strings.TrimSpace(config.Auth.Audience)
	if audience == "" {
		return "", errors.New("qq-search config: auth.audience is required")
	}
	return audience, nil
}

// AllowedCallers validates and returns the caller allow-list.
func (config Config) AllowedCallers() ([]string, error) {
	for _, caller := range config.Auth.AllowedCallers {
		switch caller {
		case "py-agent", "document-service", "go-web":
		default:
			return nil, fmt.Errorf("qq-search config: auth.allowed_callers contains unregistered caller %q", caller)
		}
	}
	return config.Auth.AllowedCallers, nil
}

// IndexConfig validates and returns the two index namespaces.
func (config Config) IndexConfig() (IndexConfig, error) {
	index := config.Index
	if strings.TrimSpace(index.MessageCollectionAlias) == "" || strings.TrimSpace(index.FileCollectionAlias) == "" {
		return IndexConfig{}, errors.New("qq-search config: both index collections are required")
	}
	if index.MessageCollectionAlias == index.FileCollectionAlias {
		return IndexConfig{}, errors.New("qq-search config: message and file collections must differ")
	}
	if index.MessageStorageDomain == index.FileStorageDomain {
		return IndexConfig{}, errors.New("qq-search config: message and file storage domains must differ")
	}
	if !strings.HasPrefix(index.MessageCollectionAlias, "qq_source_") || !strings.HasPrefix(index.FileCollectionAlias, "qq_source_") {
		return IndexConfig{}, errors.New("qq-search config: index collections must stay in the qq_source_ namespace")
	}
	if index.DefaultTopK <= 0 || index.MaxTopK <= 0 || index.DefaultTopK > index.MaxTopK {
		return IndexConfig{}, errors.New("qq-search config: index top_k bounds are invalid")
	}
	if index.MaxBatchSize <= 0 || index.MaxBatchSize > 5000 {
		return IndexConfig{}, errors.New("qq-search config: index.max_batch_size must be between 1 and 5000")
	}
	return index, nil
}

// Shutdown validates and returns the shutdown grace period.
func (config Config) Shutdown() (time.Duration, error) {
	timeout, err := time.ParseDuration(config.ShutdownTimeout)
	if err != nil {
		return 0, fmt.Errorf("qq-search config: shutdown_timeout: %w", err)
	}
	if timeout <= 0 {
		return 0, errors.New("qq-search config: shutdown_timeout must be positive")
	}
	return timeout, nil
}

// BoundaryKey reads the shared boundary key material.
func (config Config) BoundaryKey() ([]byte, error) {
	path := strings.TrimSpace(config.Auth.CapabilityKeyFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("qq-search config: read boundary key %s: %w", path, err)
	}
	key := []byte(strings.TrimSpace(string(raw)))
	if len(key) < 32 {
		return nil, fmt.Errorf("qq-search config: boundary key %s must be at least 32 bytes", path)
	}
	return key, nil
}
