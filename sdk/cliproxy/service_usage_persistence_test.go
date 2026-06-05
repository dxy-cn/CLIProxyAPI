package cliproxy

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestRun_FailsWhenUsagePersistenceInitFails(t *testing.T) {
	t.Setenv("MYSQLSTORE_DSN", "invalid dsn")
	t.Setenv("mysqlstore_dsn", "")

	tmpDir := t.TempDir()
	cfg := &config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys: config.FlexAPIKeyList{"test-key"},
		},
		AuthDir:                 filepath.Join(tmpDir, "auth"),
		UsagePersistenceEnabled: true,
		RemoteManagement: config.RemoteManagement{
			DisableControlPanel: true,
		},
	}

	service := &Service{cfg: cfg, configPath: filepath.Join(tmpDir, "config.yaml")}
	err := service.Run(context.Background())
	if err == nil {
		t.Fatalf("expected run to fail when usage persistence init fails")
	}
	if !strings.Contains(err.Error(), "initialize usage persistence") {
		t.Fatalf("unexpected error: %v", err)
	}
}
