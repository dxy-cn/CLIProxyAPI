package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveConfigPreserveCommentsPreservesAPIKeyObjectMetadata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configYAML := strings.TrimSpace(`
debug: false
api-keys:
  - api-key: sk-client
    name: client
    auth_identity: codex:chatgpt:acct-stable
    extra-note: keep-me
`) + "\n"
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	cfg.Debug = true

	if err = SaveConfigPreserveComments(configPath, cfg); err != nil {
		t.Fatalf("SaveConfigPreserveComments() error = %v", err)
	}

	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read saved config: %v", err)
	}

	savedYAML := string(saved)
	for _, snippet := range []string{
		"debug: true",
		"name: client",
		"auth_identity: codex:chatgpt:acct-stable",
		"extra-note: keep-me",
	} {
		if !strings.Contains(savedYAML, snippet) {
			t.Fatalf("saved config missing %q:\n%s", snippet, savedYAML)
		}
	}
}

func TestSaveConfigPreserveCommentsPreservesConfigAPIKeyProviderBlock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configYAML := strings.TrimSpace(`
debug: false
auth:
  providers:
    config-api-key:
      api-key-entries:
        - api-key: sk-client
          name: client
          auth_identity: codex:chatgpt:acct-stable
          extra-note: keep-me
`) + "\n"
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	cfg.Debug = true

	if err = SaveConfigPreserveComments(configPath, cfg); err != nil {
		t.Fatalf("SaveConfigPreserveComments() error = %v", err)
	}

	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read saved config: %v", err)
	}

	savedYAML := string(saved)
	for _, snippet := range []string{
		"debug: true",
		"auth:",
		"config-api-key:",
		"name: client",
		"auth_identity: codex:chatgpt:acct-stable",
		"extra-note: keep-me",
	} {
		if !strings.Contains(savedYAML, snippet) {
			t.Fatalf("saved config missing %q:\n%s", snippet, savedYAML)
		}
	}
}

func TestLoadConfigParsesModelPrices(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	configYAML := strings.TrimSpace(`
model-prices:
  gpt-5.5:
    input: 5
    output: 30
    cache: 0.5
`) + "\n"
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}

	price := cfg.ModelPrices["gpt-5.5"]
	if price.Input != 5 || price.Output != 30 || price.Cache != 0.5 {
		t.Fatalf("unexpected model price: %#v", price)
	}
}
