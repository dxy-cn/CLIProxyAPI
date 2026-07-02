package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaultsAPIKeyBalanceInterval(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configFile, []byte("routing:\n  strategy: account-bind\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if got := cfg.Routing.APIKeyBalanceIntervalMinutesOrDefault(); got != DefaultAPIKeyBalanceIntervalMinutes {
		t.Fatalf("default api key balance interval = %d, want %d", got, DefaultAPIKeyBalanceIntervalMinutes)
	}
}

func TestLoadConfigReadsAPIKeyBalanceInterval(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configFile, []byte("routing:\n  strategy: account-bind\n  api-key-balance-interval-minutes: 45\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if got := cfg.Routing.APIKeyBalanceIntervalMinutesOrDefault(); got != 45 {
		t.Fatalf("api key balance interval = %d, want 45", got)
	}
}

func TestLoadConfigKeepsDisabledAPIKeyBalanceInterval(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configFile, []byte("routing:\n  strategy: account-bind\n  api-key-balance-interval-minutes: 0\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if got := cfg.Routing.APIKeyBalanceIntervalMinutesOrDefault(); got != 0 {
		t.Fatalf("api key balance interval = %d, want disabled", got)
	}
}
