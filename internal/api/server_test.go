package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gin "github.com/gin-gonic/gin"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	internallogging "github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/managementasset"
	internalusage "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v6/sdk/access"
	sdkapi "github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()

	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("failed to create auth dir: %v", err)
	}

	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: sdkconfig.FlexAPIKeyList{"test-key"},
		},
		Port:                   0,
		AuthDir:                authDir,
		Debug:                  true,
		LoggingToFile:          false,
		UsageStatisticsEnabled: false,
		RemoteManagement:       proxyconfig.RemoteManagement{DisableControlPanel: true},
	}

	authManager := auth.NewManager(nil, nil, nil)
	accessManager := sdkaccess.NewManager()

	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("model-prices: {}\n"), 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}
	return NewServer(cfg, authManager, accessManager, configPath)
}

func TestPublicMonitorRouteAllowsConfiguredKeyWithoutManagementAuth(t *testing.T) {
	server := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v0/management/public/custom/monitor/kpi?api_key=test-key", nil)
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPublicMonitorRouteRejectsUnknownKey(t *testing.T) {
	server := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v0/management/public/custom/monitor/kpi?api_key=missing-key", nil)
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPublicMonitorRouteExposesUserMonitorEndpoints(t *testing.T) {
	server := newTestServer(t)

	for _, path := range []string{
		"/v0/management/public/custom/monitor/model-prices?api_key=test-key",
		"/v0/management/public/custom/monitor/model-distribution?api_key=test-key",
		"/v0/management/public/custom/monitor/key-token-stats?api_key=test-key",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		server.engine.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status for %s: got %d body=%s", path, rr.Code, rr.Body.String())
		}
	}
}

func TestUserMonitorRouteServesControlPanelAsset(t *testing.T) {
	server := newTestServer(t)
	server.cfg.RemoteManagement.DisableControlPanel = false

	filePath := managementasset.FilePath(server.configFilePath)
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("failed to create static dir: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("<html>management panel</html>"), 0o644); err != nil {
		t.Fatalf("failed to write management asset: %v", err)
	}

	for _, path := range []string{"/management.html", "/user/monitor"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		server.engine.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status for %s: got %d body=%s", path, rr.Code, rr.Body.String())
		}
		if body := rr.Body.String(); !strings.Contains(body, "management panel") {
			t.Fatalf("response body for %s missing management asset: %s", path, body)
		}
	}
}

func TestUsagePersistenceEnabledHotReload(t *testing.T) {
	t.Setenv("MYSQLSTORE_DSN", "")
	t.Setenv("mysqlstore_dsn", "")

	internalusage.CloseDatabasePlugin()
	defer internalusage.CloseDatabasePlugin()

	server := newTestServer(t)

	disabled := *server.cfg
	disabled.UsagePersistenceEnabled = false
	server.UpdateClients(&disabled)
	if internalusage.GetDatabasePlugin() != nil {
		t.Fatalf("expected database plugin to be nil when disabled")
	}

	enabled := disabled
	enabled.UsagePersistenceEnabled = true
	server.UpdateClients(&enabled)
	firstPlugin := internalusage.GetDatabasePlugin()
	if firstPlugin == nil {
		t.Fatalf("expected database plugin to be initialized when enabled")
	}
	if _, err := os.Stat(filepath.Join(enabled.AuthDir, "usage.db")); err != nil {
		t.Fatalf("expected sqlite usage db to exist: %v", err)
	}

	disabledAgain := enabled
	disabledAgain.UsagePersistenceEnabled = false
	server.UpdateClients(&disabledAgain)
	if internalusage.GetDatabasePlugin() != nil {
		t.Fatalf("expected database plugin to be nil after disabling")
	}

	enabledAgain := disabledAgain
	enabledAgain.UsagePersistenceEnabled = true
	server.UpdateClients(&enabledAgain)
	secondPlugin := internalusage.GetDatabasePlugin()
	if secondPlugin == nil {
		t.Fatalf("expected database plugin to be initialized after re-enabling")
	}
	if secondPlugin == firstPlugin {
		t.Fatalf("expected database plugin to be re-initialized after re-enabling")
	}
}

func TestHealthz(t *testing.T) {
	server := newTestServer(t)

	t.Run("GET", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rr := httptest.NewRecorder()
		server.engine.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
		}

		var resp struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse response JSON: %v; body=%s", err, rr.Body.String())
		}
		if resp.Status != "ok" {
			t.Fatalf("unexpected response status: got %q want %q", resp.Status, "ok")
		}
	})

	t.Run("HEAD", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodHead, "/healthz", nil)
		rr := httptest.NewRecorder()
		server.engine.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("unexpected status code: got %d want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
		}
		if rr.Body.Len() != 0 {
			t.Fatalf("expected empty body for HEAD request, got %q", rr.Body.String())
		}
	})
}

func TestAmpProviderModelRoutes(t *testing.T) {
	testCases := []struct {
		name         string
		path         string
		wantStatus   int
		wantContains string
	}{
		{
			name:         "openai root models",
			path:         "/api/provider/openai/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "groq root models",
			path:         "/api/provider/groq/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "openai models",
			path:         "/api/provider/openai/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"object":"list"`,
		},
		{
			name:         "anthropic models",
			path:         "/api/provider/anthropic/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"data"`,
		},
		{
			name:         "google models v1",
			path:         "/api/provider/google/v1/models",
			wantStatus:   http.StatusOK,
			wantContains: `"models"`,
		},
		{
			name:         "google models v1beta",
			path:         "/api/provider/google/v1beta/models",
			wantStatus:   http.StatusOK,
			wantContains: `"models"`,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			server := newTestServer(t)

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer test-key")

			rr := httptest.NewRecorder()
			server.engine.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("unexpected status code for %s: got %d want %d; body=%s", tc.path, rr.Code, tc.wantStatus, rr.Body.String())
			}
			if body := rr.Body.String(); !strings.Contains(body, tc.wantContains) {
				t.Fatalf("response body for %s missing %q: %s", tc.path, tc.wantContains, body)
			}
		})
	}
}

func TestDefaultRequestLoggerFactory_UsesResolvedLogDirectory(t *testing.T) {
	t.Setenv("WRITABLE_PATH", "")
	t.Setenv("writable_path", "")

	originalWD, errGetwd := os.Getwd()
	if errGetwd != nil {
		t.Fatalf("failed to get current working directory: %v", errGetwd)
	}

	tmpDir := t.TempDir()
	if errChdir := os.Chdir(tmpDir); errChdir != nil {
		t.Fatalf("failed to switch working directory: %v", errChdir)
	}
	defer func() {
		if errChdirBack := os.Chdir(originalWD); errChdirBack != nil {
			t.Fatalf("failed to restore working directory: %v", errChdirBack)
		}
	}()

	// Force ResolveLogDirectory to fallback to auth-dir/logs by making ./logs not a writable directory.
	if errWriteFile := os.WriteFile(filepath.Join(tmpDir, "logs"), []byte("not-a-directory"), 0o644); errWriteFile != nil {
		t.Fatalf("failed to create blocking logs file: %v", errWriteFile)
	}

	configDir := filepath.Join(tmpDir, "config")
	if errMkdirConfig := os.MkdirAll(configDir, 0o755); errMkdirConfig != nil {
		t.Fatalf("failed to create config dir: %v", errMkdirConfig)
	}
	configPath := filepath.Join(configDir, "config.yaml")

	authDir := filepath.Join(tmpDir, "auth")
	if errMkdirAuth := os.MkdirAll(authDir, 0o700); errMkdirAuth != nil {
		t.Fatalf("failed to create auth dir: %v", errMkdirAuth)
	}

	cfg := &proxyconfig.Config{
		SDKConfig: proxyconfig.SDKConfig{
			RequestLog: false,
		},
		AuthDir:           authDir,
		ErrorLogsMaxFiles: 10,
	}

	logger := defaultRequestLoggerFactory(cfg, configPath)
	fileLogger, ok := logger.(*internallogging.FileRequestLogger)
	if !ok {
		t.Fatalf("expected *FileRequestLogger, got %T", logger)
	}

	errLog := fileLogger.LogRequestWithOptions(
		"/v1/chat/completions",
		http.MethodPost,
		map[string][]string{"Content-Type": []string{"application/json"}},
		[]byte(`{"input":"hello"}`),
		http.StatusBadGateway,
		map[string][]string{"Content-Type": []string{"application/json"}},
		[]byte(`{"error":"upstream failure"}`),
		nil,
		nil,
		nil,
		nil,
		nil,
		true,
		"issue-1711",
		time.Now(),
		time.Now(),
	)
	if errLog != nil {
		t.Fatalf("failed to write forced error request log: %v", errLog)
	}

	authLogsDir := filepath.Join(authDir, "logs")
	authEntries, errReadAuthDir := os.ReadDir(authLogsDir)
	if errReadAuthDir != nil {
		t.Fatalf("failed to read auth logs dir %s: %v", authLogsDir, errReadAuthDir)
	}
	foundErrorLogInAuthDir := false
	for _, entry := range authEntries {
		if strings.HasPrefix(entry.Name(), "error-") && strings.HasSuffix(entry.Name(), ".log") {
			foundErrorLogInAuthDir = true
			break
		}
	}
	if !foundErrorLogInAuthDir {
		t.Fatalf("expected forced error log in auth fallback dir %s, got entries: %+v", authLogsDir, authEntries)
	}

	configLogsDir := filepath.Join(configDir, "logs")
	configEntries, errReadConfigDir := os.ReadDir(configLogsDir)
	if errReadConfigDir != nil && !os.IsNotExist(errReadConfigDir) {
		t.Fatalf("failed to inspect config logs dir %s: %v", configLogsDir, errReadConfigDir)
	}
	for _, entry := range configEntries {
		if strings.HasPrefix(entry.Name(), "error-") && strings.HasSuffix(entry.Name(), ".log") {
			t.Fatalf("unexpected forced error log in config dir %s", configLogsDir)
		}
	}
}

// TestAccountBindMiddleware_PerKeyBindingIgnoredWithoutAccountBindStrategy verifies that
// api-keys auth_identity metadata has no runtime effect unless routing.strategy is account-bind.
func TestAccountBindMiddleware_PerKeyBindingIgnoredWithoutAccountBindStrategy(t *testing.T) {
	const (
		clientKey = "sk-apache-0i6G7JPCeBwOVSqbF"
		boundRef  = "codex:chatgpt:acct-bound"
	)

	s := newTestServer(t)

	// routing.strategy stays at the default (round-robin). Per-key binding must not apply.
	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: sdkconfig.FlexAPIKeyList{clientKey},
			APIKeyAuthIdentityBindings: map[string]string{
				clientKey: boundRef,
			},
		},
	}
	s.UpdateBindingConfig(cfg)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("apiKey", clientKey)

	s.accountBindMiddleware()(c)

	if c.IsAborted() {
		t.Fatalf("middleware should not abort when account-bind is disabled; status=%d", c.Writer.Status())
	}
	if got := sdkapi.BoundAuthIndexFromContext(c.Request.Context()); got != "" {
		t.Fatalf("per-key binding must be ignored outside account-bind: got %q", got)
	}
}

func TestAccountBindMiddleware_AuthIdentityBindingResolvesCurrentAuthIndex(t *testing.T) {
	const clientKey = "sk-client"

	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("failed to create auth dir: %v", err)
	}

	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: sdkconfig.FlexAPIKeyList{clientKey},
			APIKeyAuthIdentityBindings: map[string]string{
				clientKey: "codex:chatgpt:acct-stable",
			},
		},
		Port:                   0,
		AuthDir:                authDir,
		Debug:                  true,
		LoggingToFile:          false,
		UsageStatisticsEnabled: false,
		RemoteManagement:       proxyconfig.RemoteManagement{DisableControlPanel: true},
		Routing:                proxyconfig.RoutingConfig{Strategy: "account-bind"},
	}

	authManager := auth.NewManager(nil, nil, nil)
	registered, err := authManager.Register(context.Background(), &auth.Auth{
		ID:       "auth-codex",
		Provider: "codex",
		FileName: "codex-after-refresh.json",
		Metadata: map[string]any{
			"id_token": testCodexJWT(t, "acct-stable"),
		},
	})
	if err != nil {
		t.Fatalf("register auth: %v", err)
	}
	if registered.Index == "" {
		t.Fatalf("registered auth_index must not be empty")
	}

	s := NewServer(cfg, authManager, sdkaccess.NewManager(), filepath.Join(tmpDir, "config.yaml"))
	s.UpdateBindingConfig(cfg)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("apiKey", clientKey)

	s.accountBindMiddleware()(c)

	if c.IsAborted() {
		t.Fatalf("middleware should not abort when auth_identity resolves; status=%d", c.Writer.Status())
	}
	got := sdkapi.BoundAuthIndexFromContext(c.Request.Context())
	if got != registered.Index {
		t.Fatalf("auth_identity binding resolved to %q, want current auth_index %q", got, registered.Index)
	}
}

func TestAccountBindMiddleware_DefaultModelAccountIgnoredWithoutAccountBindStrategy(t *testing.T) {
	s := newTestServer(t)

	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: sdkconfig.FlexAPIKeyList{"sk-client"},
		},
		Routing: proxyconfig.RoutingConfig{DefaultModelAccount: "codex:chatgpt:acct-default"},
	}
	s.UpdateBindingConfig(cfg)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("apiKey", "sk-client")

	s.accountBindMiddleware()(c)

	if c.IsAborted() {
		t.Fatalf("middleware should not abort when account-bind is disabled; status=%d", c.Writer.Status())
	}
	if got := sdkapi.BoundAuthIndexFromContext(c.Request.Context()); got != "" {
		t.Fatalf("default-model-account must be ignored outside account-bind: got %q", got)
	}
}

func TestAccountBindMiddleware_DefaultModelAccountAppliesWithAccountBindStrategy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("failed to create auth dir: %v", err)
	}

	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: sdkconfig.FlexAPIKeyList{"sk-client"},
		},
		Port:                   0,
		AuthDir:                authDir,
		Debug:                  true,
		LoggingToFile:          false,
		UsageStatisticsEnabled: false,
		RemoteManagement:       proxyconfig.RemoteManagement{DisableControlPanel: true},
		Routing: proxyconfig.RoutingConfig{
			Strategy:            "account-bind",
			DefaultModelAccount: "codex:chatgpt:acct-default",
		},
	}

	authManager := auth.NewManager(nil, nil, nil)
	registered, err := authManager.Register(context.Background(), &auth.Auth{
		ID:       "auth-default",
		Provider: "codex",
		FileName: "codex-default.json",
		Metadata: map[string]any{
			"id_token": testCodexJWT(t, "acct-default"),
		},
	})
	if err != nil {
		t.Fatalf("register auth: %v", err)
	}
	if registered.Index == "" {
		t.Fatalf("registered auth_index must not be empty")
	}

	s := NewServer(cfg, authManager, sdkaccess.NewManager(), filepath.Join(tmpDir, "config.yaml"))
	s.UpdateBindingConfig(cfg)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("apiKey", "sk-client")

	s.accountBindMiddleware()(c)

	if c.IsAborted() {
		t.Fatalf("middleware should not abort when default-model-account resolves; status=%d", c.Writer.Status())
	}
	if got := sdkapi.BoundAuthIndexFromContext(c.Request.Context()); got != registered.Index {
		t.Fatalf("default-model-account not injected: got %q, want %q", got, registered.Index)
	}
}

func TestAccountBindMiddleware_LegacyDefaultModelAccountRejectedInAccountBindStrategy(t *testing.T) {
	s := newTestServer(t)

	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: sdkconfig.FlexAPIKeyList{"sk-client"},
		},
		Routing: proxyconfig.RoutingConfig{
			Strategy:            "account-bind",
			DefaultModelAccount: "idx-legacy",
		},
	}
	s.UpdateBindingConfig(cfg)

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("apiKey", "sk-client")

	s.accountBindMiddleware()(c)

	if !c.IsAborted() {
		t.Fatalf("legacy default-model-account must not satisfy strict account-bind mode")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("strict rejection should be 400, got %d", rec.Code)
	}
	if got := sdkapi.BoundAuthIndexFromContext(c.Request.Context()); got != "" {
		t.Fatalf("legacy default-model-account must not inject auth_index: got %q", got)
	}
}

// TestAccountBindMiddleware_StrictModeRejectsUnboundKey verifies that strict "account-bind"
// mode still rejects client keys without a binding or default-model-account.
func TestAccountBindMiddleware_StrictModeRejectsUnboundKey(t *testing.T) {
	s := newTestServer(t)

	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: sdkconfig.FlexAPIKeyList{"sk-orphan"},
		},
		Routing: proxyconfig.RoutingConfig{Strategy: "account-bind"},
	}
	s.UpdateBindingConfig(cfg)

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("apiKey", "sk-orphan")

	s.accountBindMiddleware()(c)

	if !c.IsAborted() {
		t.Fatalf("strict mode must reject unbound keys")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("strict rejection should be 400, got %d", rec.Code)
	}
}

func testCodexJWT(t *testing.T, accountID string) string {
	t.Helper()

	header, err := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal JWT header: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
	if err != nil {
		t.Fatalf("marshal JWT payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
