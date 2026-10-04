package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The vector configuration is what turns "hybrid search" into a checked
// statement: an endpoint that cannot be dialled, a profile this build does not
// implement, or a width that disagrees with the profile must all fail before the
// service opens a collection.

func TestDefaultVectorConfigIsUsable(t *testing.T) {
	settings, err := Default().VectorConfig()
	if err != nil {
		t.Fatalf("Default().VectorConfig(): %v", err)
	}
	if !settings.Enabled {
		t.Fatal("the vector flow is disabled by default")
	}
	if settings.Host != "127.0.0.1" || settings.Port != 16334 {
		t.Fatalf("endpoint = %s:%d, want 127.0.0.1:16334", settings.Host, settings.Port)
	}
	if settings.Profile != "local-hash-v1" || settings.Dimensions != 64 {
		t.Fatalf("profile = %q/%d, want local-hash-v1/64", settings.Profile, settings.Dimensions)
	}
	if settings.Timeout != 10*time.Second {
		t.Fatalf("timeout = %s, want 10s", settings.Timeout)
	}
	if settings.Recall <= 0 || settings.RRFK <= 0 || settings.MaxKeywordCandidates <= 0 {
		t.Fatalf("recall/rrf_k/max_keyword_candidates = %d/%d/%d, want positive defaults",
			settings.Recall, settings.RRFK, settings.MaxKeywordCandidates)
	}
	if !settings.PruneGenerationsOnRebuild {
		t.Fatal("rebuild does not prune superseded collections by default")
	}
}

func TestDisabledVectorFlowOnlyNeedsTheSwitch(t *testing.T) {
	cfg := Default()
	cfg.Vector.Enabled = false
	// Everything else is broken on purpose: a disabled flow must not validate
	// values the service will never use.
	cfg.Vector.Endpoint = ""
	cfg.Vector.Profile = "not-a-profile"
	cfg.Vector.Dimensions = 0
	settings, err := cfg.VectorConfig()
	if err != nil {
		t.Fatalf("a disabled vector flow rejected its configuration: %v", err)
	}
	if settings.Enabled {
		t.Fatal("the returned settings claim the vector flow is enabled")
	}
}

func TestVectorConfigRejectsUnusableValues(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		message string
	}{
		{
			name:    "no endpoint",
			mutate:  func(cfg *Config) { cfg.Vector.Endpoint = "  " },
			message: "vector.endpoint is required",
		},
		{
			name:    "endpoint without a port",
			mutate:  func(cfg *Config) { cfg.Vector.Endpoint = "127.0.0.1" },
			message: "must be host:port",
		},
		{
			name:    "endpoint with an unusable port",
			mutate:  func(cfg *Config) { cfg.Vector.Endpoint = "127.0.0.1:70000" },
			message: "no usable port",
		},
		{
			name:    "unknown profile",
			mutate:  func(cfg *Config) { cfg.Vector.Profile = "text-embedding-3-small" },
			message: "produces 0 dimensions",
		},
		{
			name:    "dimension mismatch",
			mutate:  func(cfg *Config) { cfg.Vector.Dimensions = 128 },
			message: "produces 64 dimensions",
		},
		{
			name:    "zero dimensions",
			mutate:  func(cfg *Config) { cfg.Vector.Dimensions = 0 },
			message: "vector.dimensions must be positive",
		},
		{
			name:    "unparsable timeout",
			mutate:  func(cfg *Config) { cfg.Vector.Timeout = "soon" },
			message: "must be a positive duration",
		},
		{
			name:    "zero recall",
			mutate:  func(cfg *Config) { cfg.Vector.Recall = 0 },
			message: "vector.recall must be between",
		},
		{
			name:    "zero rrf_k",
			mutate:  func(cfg *Config) { cfg.Vector.RRFK = 0 },
			message: "vector.rrf_k must be between",
		},
		{
			name:    "zero keyword candidates",
			mutate:  func(cfg *Config) { cfg.Vector.MaxKeywordCandidates = 0 },
			message: "vector.max_keyword_candidates must be between",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := Default()
			testCase.mutate(&cfg)
			_, err := cfg.VectorConfig()
			if err == nil {
				t.Fatal("the configuration was accepted")
			}
			if !strings.Contains(err.Error(), testCase.message) {
				t.Fatalf("error %q does not mention %q", err.Error(), testCase.message)
			}
		})
	}
}

// TestValidateCoversTheVectorFlow pins that Config.Validate - the single gate the
// process root calls - reports a broken vector configuration too.
func TestValidateCoversTheVectorFlow(t *testing.T) {
	cfg := Default()
	cfg.Postgres.DSN = "postgres://example"
	cfg.Auth.CapabilityKeyFile = "boundary.key"
	cfg.Source.Disabled = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the default configuration is not valid: %v", err)
	}
	cfg.Vector.Profile = "not-a-profile"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted an unimplemented embedding profile")
	}
}

// TestShippedConfigurationsLoad pins the two configuration files the service is
// actually run with: a key that does not match the struct, or a value the
// validator rejects, would otherwise only surface when the container starts.
func TestShippedConfigurationsLoad(t *testing.T) {
	for _, name := range []string{"config.yaml", "config.docker.yaml"} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(filepath.Join("..", "..", "configs", name))
			if err != nil {
				t.Fatalf("load %s: %v", name, err)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("validate %s: %v", name, err)
			}
			settings, err := cfg.VectorConfig()
			if err != nil {
				t.Fatalf("vector settings of %s: %v", name, err)
			}
			if !settings.Enabled {
				t.Fatalf("%s disables the vector flow", name)
			}
			if settings.Profile != Default().Vector.Profile || settings.Dimensions != Default().Vector.Dimensions {
				t.Fatalf("%s uses profile %q/%d, want the default %q/%d",
					name, settings.Profile, settings.Dimensions, Default().Vector.Profile, Default().Vector.Dimensions)
			}
			if settings.Host == "" || settings.Port == 0 {
				t.Fatalf("%s has no usable Qdrant endpoint", name)
			}
			if _, err := cfg.IndexConfig(); err != nil {
				t.Fatalf("index settings of %s: %v", name, err)
			}
		})
	}
}

// TestProfileDimensionsIsClosed pins the profile table both the validator and the
// collection-opening path consult.
func TestProfileDimensionsIsClosed(t *testing.T) {
	if dimensions := ProfileDimensions("local-hash-v1"); dimensions != 64 {
		t.Fatalf("local-hash-v1 has %d dimensions, want 64", dimensions)
	}
	for _, unknown := range []string{"", "local-hash-v2", "unknown"} {
		if dimensions := ProfileDimensions(unknown); dimensions != 0 {
			t.Fatalf("unknown profile %q reported %d dimensions, want 0", unknown, dimensions)
		}
	}
}
