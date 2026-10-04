// Package config loads and validates the document search service configuration.
//
// The service fails closed at startup: without a boundary key, a database DSN and
// a consumed document-service endpoint it does not start, because a search
// service that cannot authenticate its callers or advance its cursor would answer
// queries from stale or unauthorized data.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the complete document search configuration.
type Config struct {
	GRPCListenAddress string         `yaml:"grpc_listen_address"`
	HTTPListenAddress string         `yaml:"http_listen_address"`
	Postgres          PostgresConfig `yaml:"postgres"`
	Auth              AuthConfig     `yaml:"auth"`
	Source            SourceConfig   `yaml:"source"`
	Index             IndexConfig    `yaml:"index"`
	Vector            VectorConfig   `yaml:"vector"`
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
	CapabilityKeyFile string   `yaml:"capability_key_file"`
	Audience          string   `yaml:"audience"`
	AllowedCallers    []string `yaml:"allowed_callers"`
	// IndexerCaller is the service identity that may write the index. It is
	// separate from the search audience so a compromised searcher cannot rewrite
	// what it reads.
	EnableReflection bool `yaml:"enable_reflection"`
}

// SourceConfig describes the document service endpoint this consumer follows.
type SourceConfig struct {
	// Endpoint is the document service gRPC address.
	Endpoint string `yaml:"endpoint"`
	// CallerIdentity is the service identity presented to document-service.
	// It is not a free-form string: the document service validates it.
	CallerIdentity string `yaml:"caller_identity"`
	// CapabilityKeyFile signs the assertion presented to document-service.
	CapabilityKeyFile string `yaml:"capability_key_file"`
	// Audience expected by document-service.
	Audience string `yaml:"audience"`
	// Scope is the scope required to stream the document change Outbox.
	Scope string `yaml:"scope"`
	// BatchSize and PollInterval bound one read and the wait between reads.
	BatchSize    int    `yaml:"batch_size"`
	PollInterval string `yaml:"poll_interval"`
	// RequestTimeout bounds one read against the source service.
	RequestTimeout string `yaml:"request_timeout"`
	// ReconnectBackoff bounds the wait after a source failure.
	ReconnectBackoff string `yaml:"reconnect_backoff"`
	// Disabled turns consumption off; only valid for tests that apply events
	// through the API directly.
	Disabled bool `yaml:"disabled"`
}

// IndexConfig holds the index namespace. The collection alias belongs to this
// service alone; two services sharing a physical collection would silently merge
// two sources into one result set.
type IndexConfig struct {
	CollectionAlias string `yaml:"collection_alias"`
	StorageDomain   string `yaml:"storage_domain"`
	IndexProfile    string `yaml:"index_profile"`
	MaxChunks       int    `yaml:"max_chunks"`
	DefaultTopK     int    `yaml:"default_top_k"`
	MaxTopK         int    `yaml:"max_top_k"`
}

// VectorConfig is the vector half of the index: the Qdrant endpoint, the
// embedding profile, and the recall/fusion bounds the query path uses.
//
// The collection alias, storage domain, profile and dimensions recorded in
// document_search.index_generations are the authority for what the collection
// actually is; the values here must agree with that row, and the service refuses
// to start when they do not. The row is what a rebuild of a shared Qdrant
// deployment has to consult to find the corpus, while this file is what an
// operator edits.
type VectorConfig struct {
	// Enabled turns the vector flow on. When it is on the service requires a
	// reachable Qdrant at startup and answers vector queries from it; when it is
	// off the service is the keyword-only index it was before.
	Enabled bool `yaml:"enabled"`
	// Endpoint is the Qdrant gRPC address (host:port).
	Endpoint string `yaml:"endpoint"`
	// APIKey is the Qdrant API key, empty for the development instance.
	APIKey string `yaml:"api_key"`
	// UseTLS switches the client to TLS.
	UseTLS bool `yaml:"use_tls"`
	// Timeout bounds one Qdrant call. A vector call that exceeds it fails the
	// operation instead of holding the index transaction open.
	Timeout string `yaml:"timeout"`
	// Profile names the embedding function that produced the stored vectors.
	// Vectors are only comparable inside one profile, so a profile change
	// requires a new index generation.
	Profile string `yaml:"profile"`
	// Dimensions is the vector width of the profile.
	Dimensions int `yaml:"dimensions"`
	// Recall is how many points one query recalls before the SQL scope filter
	// keeps the documents the caller may actually see.
	Recall int `yaml:"recall"`
	// RRFK is the reciprocal-rank-fusion constant.
	RRFK int `yaml:"rrf_k"`
	// MaxKeywordCandidates is the width of the RRF fusion window: how many of the
	// top keyword matches take part in the fusion with the vector recall.
	//
	// It bounds how deep a recall can reorder the answer, not how much of the
	// answer a caller can read. The keyword matches beyond the window follow the
	// fused window in keyword order, so every page up to the total is still
	// readable and the reported total counts every keyword match.
	MaxKeywordCandidates int `yaml:"max_keyword_candidates"`
	// PruneGenerationsOnRebuild drops physical collections of this alias that no
	// index_generations row references once a rebuild has finished.
	PruneGenerationsOnRebuild bool `yaml:"prune_generations_on_rebuild"`
}

// Default returns the development defaults.
func Default() Config {
	return Config{
		GRPCListenAddress: "127.0.0.1:18082",
		HTTPListenAddress: "127.0.0.1:18092",
		Postgres: PostgresConfig{
			Schema:          "document_search",
			MaxOpenConns:    16,
			ConnMaxLifetime: "30m",
			ConnectTimeout:  "10s",
		},
		Auth: AuthConfig{Audience: "document-search"},
		Source: SourceConfig{
			Endpoint:         "127.0.0.1:18081",
			CallerIdentity:   "document-search",
			Audience:         "document-service",
			Scope:            "document-event-reader",
			BatchSize:        100,
			PollInterval:     "500ms",
			RequestTimeout:   "10s",
			ReconnectBackoff: "2s",
		},
		Index: IndexConfig{
			CollectionAlias: "go_web_document_v1",
			StorageDomain:   "document-search:documents:v1",
			IndexProfile:    "markdown-v1",
			MaxChunks:       200,
			DefaultTopK:     10,
			MaxTopK:         100,
		},
		Vector: VectorConfig{
			Enabled:                   true,
			Endpoint:                  "127.0.0.1:16334",
			Timeout:                   "10s",
			Profile:                   "local-hash-v1",
			Dimensions:                64,
			Recall:                    100,
			RRFK:                      60,
			MaxKeywordCandidates:      1000,
			PruneGenerationsOnRebuild: true,
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
				return Config{}, fmt.Errorf("document-search config: read %s: %w", path, err)
			}
		} else {
			decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
			decoder.KnownFields(true)
			if err := decoder.Decode(&config); err != nil {
				return Config{}, fmt.Errorf("document-search config: parse %s: %w", path, err)
			}
		}
	}
	applyEnvironment(&config)
	return config, nil
}

func applyEnvironment(config *Config) {
	setString(&config.GRPCListenAddress, "DOCUMENT_SEARCH_GRPC_LISTEN_ADDRESS")
	setString(&config.HTTPListenAddress, "DOCUMENT_SEARCH_HTTP_LISTEN_ADDRESS")
	setString(&config.Postgres.DSN, "DOCUMENT_SEARCH_POSTGRES_DSN")
	setString(&config.Postgres.Schema, "DOCUMENT_SEARCH_POSTGRES_SCHEMA")
	setString(&config.Auth.CapabilityKeyFile, "DOCUMENT_SEARCH_CAPABILITY_KEY_FILE")
	setString(&config.Auth.Audience, "DOCUMENT_SEARCH_AUDIENCE")
	setString(&config.Source.Endpoint, "DOCUMENT_SEARCH_SOURCE_ENDPOINT")
	setString(&config.Source.CallerIdentity, "DOCUMENT_SEARCH_SOURCE_CALLER_IDENTITY")
	setString(&config.Source.CapabilityKeyFile, "DOCUMENT_SEARCH_SOURCE_CAPABILITY_KEY_FILE")
	setString(&config.Source.Audience, "DOCUMENT_SEARCH_SOURCE_AUDIENCE")
	setString(&config.Source.Scope, "DOCUMENT_SEARCH_SOURCE_SCOPE")
	setString(&config.Index.CollectionAlias, "DOCUMENT_SEARCH_COLLECTION_ALIAS")
	setString(&config.Vector.Endpoint, "DOCUMENT_SEARCH_VECTOR_ENDPOINT")
	setString(&config.Vector.APIKey, "DOCUMENT_SEARCH_VECTOR_API_KEY")
	setString(&config.Vector.Timeout, "DOCUMENT_SEARCH_VECTOR_TIMEOUT")
	setString(&config.Vector.Profile, "DOCUMENT_SEARCH_VECTOR_PROFILE")
	setInt(&config.Vector.Dimensions, "DOCUMENT_SEARCH_VECTOR_DIMENSIONS")
	setInt(&config.Vector.Recall, "DOCUMENT_SEARCH_VECTOR_RECALL")
	setInt(&config.Vector.RRFK, "DOCUMENT_SEARCH_VECTOR_RRF_K")
	setInt(&config.Vector.MaxKeywordCandidates, "DOCUMENT_SEARCH_VECTOR_MAX_KEYWORD_CANDIDATES")
	setBool(&config.Vector.Enabled, "DOCUMENT_SEARCH_VECTOR_ENABLED")
	setBool(&config.Vector.UseTLS, "DOCUMENT_SEARCH_VECTOR_USE_TLS")
	setBool(&config.Vector.PruneGenerationsOnRebuild, "DOCUMENT_SEARCH_VECTOR_PRUNE_ON_REBUILD")
	setString(&config.ShutdownTimeout, "DOCUMENT_SEARCH_SHUTDOWN_TIMEOUT")
	if callers := strings.TrimSpace(os.Getenv("DOCUMENT_SEARCH_ALLOWED_CALLERS")); callers != "" {
		config.Auth.AllowedCallers = splitAndTrim(callers)
	}
	if disabled := strings.TrimSpace(os.Getenv("DOCUMENT_SEARCH_SOURCE_DISABLED")); disabled != "" {
		config.Source.Disabled = disabled == "1" || strings.EqualFold(disabled, "true")
	}
}

func setString(target *string, name string) {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		*target = value
	}
}

// setInt applies an integer override. A value that cannot be parsed is ignored
// rather than silently becoming zero, so a typo cannot turn a bound off.
func setInt(target *int, name string) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return
	}
	if value, err := strconv.Atoi(raw); err == nil {
		*target = value
	}
}

// setBool applies a boolean override. The accepted spellings are the ones the
// rest of this file already uses for the source switch.
func setBool(target *bool, name string) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return
	}
	*target = raw == "1" || strings.EqualFold(raw, "true")
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
		return errors.New("document-search config: grpc_listen_address is required")
	}
	if strings.TrimSpace(config.HTTPListenAddress) == "" {
		return errors.New("document-search config: http_listen_address is required")
	}
	if strings.TrimSpace(config.Postgres.DSN) == "" {
		return errors.New("document-search config: postgres.dsn is required")
	}
	if strings.TrimSpace(config.Postgres.Schema) != "document_search" {
		return fmt.Errorf("document-search config: postgres.schema must be document_search, got %q", config.Postgres.Schema)
	}
	if strings.TrimSpace(config.Auth.CapabilityKeyFile) == "" {
		return errors.New("document-search config: auth.capability_key_file is required")
	}
	if _, err := config.Audience(); err != nil {
		return err
	}
	if _, err := config.SourceConfig(); err != nil {
		return err
	}
	if _, err := config.IndexConfig(); err != nil {
		return err
	}
	if _, err := config.VectorConfig(); err != nil {
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
		return "", errors.New("document-search config: auth.audience is required")
	}
	return audience, nil
}

// AllowedCallers validates and returns the caller allow-list.
func (config Config) AllowedCallers() ([]string, error) {
	for _, caller := range config.Auth.AllowedCallers {
		switch caller {
		case "go-web", "py-agent", "document-service", "spacectl":
		default:
			return nil, fmt.Errorf("document-search config: auth.allowed_callers contains unregistered caller %q", caller)
		}
	}
	return config.Auth.AllowedCallers, nil
}

// SourceConfig validates and returns the source consumption settings.
func (config Config) SourceConfig() (SourceConfig, error) {
	source := config.Source
	if source.Disabled {
		return source, nil
	}
	if strings.TrimSpace(source.Endpoint) == "" {
		return SourceConfig{}, errors.New("document-search config: source.endpoint is required")
	}
	if strings.TrimSpace(source.CallerIdentity) == "" {
		return SourceConfig{}, errors.New("document-search config: source.caller_identity is required")
	}
	if strings.TrimSpace(source.CapabilityKeyFile) == "" {
		return SourceConfig{}, errors.New("document-search config: source.capability_key_file is required")
	}
	if strings.TrimSpace(source.Audience) == "" {
		return SourceConfig{}, errors.New("document-search config: source.audience is required")
	}
	if strings.TrimSpace(source.Scope) == "" {
		return SourceConfig{}, errors.New("document-search config: source.scope is required")
	}
	if source.BatchSize <= 0 || source.BatchSize > 1000 {
		return SourceConfig{}, errors.New("document-search config: source.batch_size must be between 1 and 1000")
	}
	for name, value := range map[string]string{
		"source.poll_interval":     source.PollInterval,
		"source.request_timeout":   source.RequestTimeout,
		"source.reconnect_backoff": source.ReconnectBackoff,
	} {
		if _, err := time.ParseDuration(value); err != nil {
			return SourceConfig{}, fmt.Errorf("document-search config: %s: %w", name, err)
		}
	}
	return source, nil
}

// IndexConfig validates and returns the index namespace settings.
func (config Config) IndexConfig() (IndexConfig, error) {
	index := config.Index
	if strings.TrimSpace(index.CollectionAlias) == "" {
		return IndexConfig{}, errors.New("document-search config: index.collection_alias is required")
	}
	if !strings.HasPrefix(index.CollectionAlias, "go_web_document") {
		return IndexConfig{}, fmt.Errorf("document-search config: index.collection_alias %q must stay in the go_web_document namespace", index.CollectionAlias)
	}
	if strings.TrimSpace(index.StorageDomain) == "" {
		return IndexConfig{}, errors.New("document-search config: index.storage_domain is required")
	}
	if index.DefaultTopK <= 0 || index.MaxTopK <= 0 || index.DefaultTopK > index.MaxTopK {
		return IndexConfig{}, errors.New("document-search config: index top_k bounds are invalid")
	}
	if index.MaxChunks <= 0 {
		return IndexConfig{}, errors.New("document-search config: index.max_chunks must be positive")
	}
	return index, nil
}

// VectorSettings is the validated vector configuration. It is a separate type
// from VectorConfig because the parsed timeout is what the callers need, and
// parsing must happen once, at validation time.
type VectorSettings struct {
	Enabled                   bool
	Host                      string
	Port                      int
	APIKey                    string
	UseTLS                    bool
	Timeout                   time.Duration
	Profile                   string
	Dimensions                int
	Recall                    int
	RRFK                      int
	MaxKeywordCandidates      int
	PruneGenerationsOnRebuild bool
}

// Vector index bounds that stay usable when the vector flow is switched off. The
// keyword arm is still fused - with one list - so its fusion window and the RRF
// constant apply in that configuration too, and a disabled flow must not silently
// shrink the keyword page.
const (
	DefaultVectorRecall              = 100
	DefaultVectorRRFK                = 60
	DefaultVectorMaxKeywordCanidates = 1000
)

// VectorConfig validates and returns the vector index settings.
//
// A disabled vector flow validates nothing else, which is what makes
// `vector.enabled: false` a usable development switch. It still reports usable
// fusion bounds: the keyword half of the answer is fused even when there is no
// vector arm, so a zero bound there would quietly change how many keyword
// matches the fusion window ranks.
func (config Config) VectorConfig() (VectorSettings, error) {
	vector := config.Vector
	if !vector.Enabled {
		settings := VectorSettings{
			Recall:               vector.Recall,
			RRFK:                 vector.RRFK,
			MaxKeywordCandidates: vector.MaxKeywordCandidates,
			Dimensions:           vector.Dimensions,
			Profile:              strings.TrimSpace(vector.Profile),
		}
		if settings.Recall <= 0 {
			settings.Recall = DefaultVectorRecall
		}
		if settings.RRFK <= 0 {
			settings.RRFK = DefaultVectorRRFK
		}
		if settings.MaxKeywordCandidates <= 0 {
			settings.MaxKeywordCandidates = DefaultVectorMaxKeywordCanidates
		}
		return settings, nil
	}
	endpoint := strings.TrimSpace(vector.Endpoint)
	if endpoint == "" {
		return VectorSettings{}, errors.New("document-search config: vector.endpoint is required when the vector flow is enabled")
	}
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return VectorSettings{}, fmt.Errorf("document-search config: vector.endpoint %q must be host:port: %w", endpoint, err)
	}
	if strings.TrimSpace(host) == "" {
		return VectorSettings{}, fmt.Errorf("document-search config: vector.endpoint %q has no host", endpoint)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber <= 0 || portNumber > 65535 {
		return VectorSettings{}, fmt.Errorf("document-search config: vector.endpoint %q has no usable port", endpoint)
	}
	profile := strings.TrimSpace(vector.Profile)
	if profile == "" {
		return VectorSettings{}, errors.New("document-search config: vector.profile is required")
	}
	dimensions := vector.Dimensions
	if dimensions <= 0 {
		return VectorSettings{}, errors.New("document-search config: vector.dimensions must be positive")
	}
	if dimensions != ProfileDimensions(profile) {
		return VectorSettings{}, fmt.Errorf(
			"document-search config: vector.dimensions is %d but profile %q produces %d dimensions",
			dimensions, profile, ProfileDimensions(profile),
		)
	}
	timeout, err := time.ParseDuration(vector.Timeout)
	if err != nil || timeout <= 0 {
		return VectorSettings{}, fmt.Errorf("document-search config: vector.timeout %q must be a positive duration", vector.Timeout)
	}
	if vector.Recall <= 0 || vector.Recall > 10000 {
		return VectorSettings{}, errors.New("document-search config: vector.recall must be between 1 and 10000")
	}
	if vector.RRFK <= 0 || vector.RRFK > 1000 {
		return VectorSettings{}, errors.New("document-search config: vector.rrf_k must be between 1 and 1000")
	}
	if vector.MaxKeywordCandidates <= 0 || vector.MaxKeywordCandidates > 100000 {
		return VectorSettings{}, errors.New("document-search config: vector.max_keyword_candidates must be between 1 and 100000")
	}
	return VectorSettings{
		Enabled:                   true,
		Host:                      host,
		Port:                      portNumber,
		APIKey:                    strings.TrimSpace(vector.APIKey),
		UseTLS:                    vector.UseTLS,
		Timeout:                   timeout,
		Profile:                   profile,
		Dimensions:                dimensions,
		Recall:                    vector.Recall,
		RRFK:                      vector.RRFK,
		MaxKeywordCandidates:      vector.MaxKeywordCandidates,
		PruneGenerationsOnRebuild: vector.PruneGenerationsOnRebuild,
	}, nil
}

// ProfileDimensions reports the vector width of a known embedding profile, or 0
// for a profile this build does not implement.
//
// The profile identity is a storage fact, not a free-form label: vectors written
// under one profile are meaningless under another, so the list here is the closed
// set of profiles the service is allowed to open a collection with. The
// dimension table lives in the config package because both the config validator
// and the collection-opening path in internal/application consult it.
func ProfileDimensions(profile string) int {
	switch strings.TrimSpace(profile) {
	case "local-hash-v1":
		return 64
	default:
		return 0
	}
}

// Shutdown validates and returns the shutdown grace period.
func (config Config) Shutdown() (time.Duration, error) {
	timeout, err := time.ParseDuration(config.ShutdownTimeout)
	if err != nil {
		return 0, fmt.Errorf("document-search config: shutdown_timeout: %w", err)
	}
	if timeout <= 0 {
		return 0, errors.New("document-search config: shutdown_timeout must be positive")
	}
	return timeout, nil
}

// BoundaryKey reads the shared boundary key material.
func (config Config) BoundaryKey() ([]byte, error) {
	return readBoundaryKey(config.Auth.CapabilityKeyFile, "document-search")
}

// SourceKey reads the key used to sign the assertion presented to the source.
func (config Config) SourceKey() ([]byte, error) {
	return readBoundaryKey(config.Source.CapabilityKeyFile, "document-search source")
}

func readBoundaryKey(path, owner string) ([]byte, error) {
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("%s config: read boundary key %s: %w", owner, path, err)
	}
	key := []byte(strings.TrimSpace(string(raw)))
	if len(key) < 32 {
		return nil, fmt.Errorf("%s config: boundary key %s must be at least 32 bytes", owner, path)
	}
	return key, nil
}
