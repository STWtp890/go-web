package app

import "testing"

func TestConfigPathUsesDefault(t *testing.T) {
	t.Setenv("GIN_CONFIG_PATH", "")

	if got := configPath(); got != defaultConfigPath {
		t.Fatalf("configPath() = %q, want %q", got, defaultConfigPath)
	}
}

func TestConfigPathUsesEnvironment(t *testing.T) {
	const configuredPath = "testdata/config.yaml"
	t.Setenv("GIN_CONFIG_PATH", configuredPath)

	if got := configPath(); got != configuredPath {
		t.Fatalf("configPath() = %q, want %q", got, configuredPath)
	}
}
