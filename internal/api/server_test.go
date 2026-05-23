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
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	internallogging "github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/managementasset"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/redisqueue"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	internalusage "github.com/router-for-me/CLIProxyAPI/v7/internal/usage"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v7/sdk/access"
	sdkapi "github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
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

func TestManagementUsageRequiresManagementAuthAndPopsArray(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")

	prevQueueEnabled := redisqueue.Enabled()
	redisqueue.SetEnabled(false)
	t.Cleanup(func() {
		redisqueue.SetEnabled(false)
		redisqueue.SetEnabled(prevQueueEnabled)
	})

	server := newTestServer(t)

	redisqueue.Enqueue([]byte(`{"id":1}`))
	redisqueue.Enqueue([]byte(`{"id":2}`))

	missingKeyReq := httptest.NewRequest(http.MethodGet, "/v0/management/usage-queue?count=2", nil)
	missingKeyRR := httptest.NewRecorder()
	server.engine.ServeHTTP(missingKeyRR, missingKeyReq)
	if missingKeyRR.Code != http.StatusUnauthorized {
		t.Fatalf("missing key status = %d, want %d body=%s", missingKeyRR.Code, http.StatusUnauthorized, missingKeyRR.Body.String())
	}

	legacyReq := httptest.NewRequest(http.MethodGet, "/v0/management/usage?count=2", nil)
	legacyReq.Header.Set("Authorization", "Bearer test-management-key")
	legacyRR := httptest.NewRecorder()
	server.engine.ServeHTTP(legacyRR, legacyReq)
	if legacyRR.Code != http.StatusNotFound {
		t.Fatalf("legacy usage status = %d, want %d body=%s", legacyRR.Code, http.StatusNotFound, legacyRR.Body.String())
	}

	authReq := httptest.NewRequest(http.MethodGet, "/v0/management/usage-queue?count=2", nil)
	authReq.Header.Set("Authorization", "Bearer test-management-key")
	authRR := httptest.NewRecorder()
	server.engine.ServeHTTP(authRR, authReq)
	if authRR.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want %d body=%s", authRR.Code, http.StatusOK, authRR.Body.String())
	}

	var payload []json.RawMessage
	if errUnmarshal := json.Unmarshal(authRR.Body.Bytes(), &payload); errUnmarshal != nil {
		t.Fatalf("unmarshal response: %v body=%s", errUnmarshal, authRR.Body.String())
	}
	if len(payload) != 2 {
		t.Fatalf("response records = %d, want 2", len(payload))
	}
	for i, raw := range payload {
		var record struct {
			ID int `json:"id"`
		}
		if errUnmarshal := json.Unmarshal(raw, &record); errUnmarshal != nil {
			t.Fatalf("unmarshal record %d: %v", i, errUnmarshal)
		}
		if record.ID != i+1 {
			t.Fatalf("record %d id = %d, want %d", i, record.ID, i+1)
		}
	}

	if remaining := redisqueue.PopOldest(1); len(remaining) != 0 {
		t.Fatalf("remaining queue = %q, want empty", remaining)
	}
}

func TestHomeEnabledHidesManagementEndpointsAndControlPanel(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")

	server := newTestServer(t)
	server.cfg.Home.Enabled = true

	t.Run("management endpoints return 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v0/management/config", nil)
		req.Header.Set("Authorization", "Bearer test-management-key")
		rr := httptest.NewRecorder()
		server.engine.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusNotFound, rr.Body.String())
		}
	})

	t.Run("management control panel returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/management.html", nil)
		rr := httptest.NewRecorder()
		server.engine.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusNotFound, rr.Body.String())
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

func TestModelsWithClientVersionReturnsCodexCatalog(t *testing.T) {
	modelRegistry := registry.GetGlobalRegistry()
	clientID := "test-client-version-catalog"
	modelRegistry.RegisterClient(clientID, "openai", []*registry.ModelInfo{
		{
			ID:            "gpt-5.5",
			Object:        "model",
			Created:       1776902400,
			OwnedBy:       "openai",
			Type:          "openai",
			DisplayName:   "GPT 5.5",
			Description:   "Frontier model for complex coding, research, and real-world work.",
			ContextLength: 272000,
			Thinking:      &registry.ThinkingSupport{Levels: []string{"low", "medium", "high", "xhigh"}},
		},
		{
			ID:            "custom-codex-model-test",
			Object:        "model",
			OwnedBy:       "test",
			Type:          "openai",
			DisplayName:   "Custom Codex Model",
			Description:   "Custom model from registry",
			ContextLength: 123456,
			Thinking:      &registry.ThinkingSupport{Levels: []string{"none", "minimal", "low", "medium", "unsupported", "high", "xhigh"}},
		},
		{ID: "grok-imagine-image-quality", Object: "model", OwnedBy: "xai", Type: "openai"},
		{ID: "gpt-image-2", Object: "model", OwnedBy: "openai", Type: "openai"},
		{ID: "grok-imagine-image", Object: "model", OwnedBy: "xai", Type: "openai"},
		{ID: "grok-imagine-video", Object: "model", OwnedBy: "xai", Type: "openai"},
	})
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(clientID)
	})

	server := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/models?client_version", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("User-Agent", "claude-cli/1.0")

	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp struct {
		Models []map[string]any `json:"models"`
		Object string           `json:"object"`
		Data   []any            `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v; body=%s", err, rr.Body.String())
	}
	if resp.Object != "" || resp.Data != nil {
		t.Fatalf("expected codex catalog format without object/data, got object=%q data=%v", resp.Object, resp.Data)
	}
	if len(resp.Models) == 0 {
		t.Fatal("expected codex catalog models")
	}

	var gpt55 map[string]any
	var custom map[string]any
	for _, model := range resp.Models {
		switch slug, _ := model["slug"].(string); slug {
		case "gpt-5.5":
			gpt55 = model
		case "custom-codex-model-test":
			custom = model
		}
	}
	if gpt55 == nil {
		t.Fatal("expected gpt-5.5 codex catalog entry")
	}
	if _, ok := gpt55["minimal_client_version"]; !ok {
		t.Fatal("expected minimal_client_version in codex catalog")
	}
	serviceTiers, ok := gpt55["service_tiers"].([]any)
	if !ok || len(serviceTiers) != 1 {
		t.Fatalf("expected gpt-5.5 priority service tier, got %#v", gpt55["service_tiers"])
	}
	if custom == nil {
		t.Fatal("expected custom model codex catalog entry")
	}
	if got, _ := custom["display_name"].(string); got != "Custom Codex Model" {
		t.Fatalf("custom display_name = %q, want Custom Codex Model", got)
	}
	if got, _ := custom["description"].(string); got != "Custom model from registry" {
		t.Fatalf("custom description = %q, want Custom model from registry", got)
	}
	if got, _ := custom["context_window"].(float64); got != 123456 {
		t.Fatalf("custom context_window = %v, want 123456", custom["context_window"])
	}
	assertCodexSupportedReasoningLevels(t, custom, []string{"none", "low", "medium", "high", "xhigh"})
	if custom["base_instructions"] != gpt55["base_instructions"] {
		t.Fatal("expected custom model to use gpt-5.5 base_instructions fallback")
	}
	if _, ok := custom["available_in_plans"].([]any); !ok {
		t.Fatalf("expected custom model to use gpt-5.5 available_in_plans fallback, got %#v", custom["available_in_plans"])
	}
	if got, _ := custom["prefer_websockets"].(bool); got {
		t.Fatalf("custom prefer_websockets = %v, want false", custom["prefer_websockets"])
	}
	if _, ok := custom["apply_patch_tool_type"]; ok {
		t.Fatal("expected custom model to omit apply_patch_tool_type")
	}
	if _, ok := custom["upgrade"]; ok {
		t.Fatal("expected custom model to omit upgrade")
	}
	if _, ok := custom["availability_nux"]; ok {
		t.Fatal("expected custom model to omit availability_nux")
	}

	hiddenModels := map[string]bool{
		"grok-imagine-image-quality": false,
		"gpt-image-2":                false,
		"grok-imagine-image":         false,
		"grok-imagine-video":         false,
	}
	for _, model := range resp.Models {
		slug, _ := model["slug"].(string)
		if _, ok := hiddenModels[slug]; !ok {
			continue
		}
		if visibility, _ := model["visibility"].(string); visibility != "hide" {
			t.Fatalf("%s visibility = %q, want hide", slug, visibility)
		}
		hiddenModels[slug] = true
	}
	for slug, found := range hiddenModels {
		if !found {
			t.Fatalf("expected hidden model %s in codex catalog", slug)
		}
	}
}

func assertCodexSupportedReasoningLevels(t *testing.T, model map[string]any, want []string) {
	t.Helper()

	rawLevels, ok := model["supported_reasoning_levels"].([]any)
	if !ok {
		t.Fatalf("expected supported_reasoning_levels, got %#v", model["supported_reasoning_levels"])
	}
	if len(rawLevels) != len(want) {
		t.Fatalf("supported_reasoning_levels length = %d, want %d: %#v", len(rawLevels), len(want), rawLevels)
	}
	for index, rawLevel := range rawLevels {
		levelEntry, ok := rawLevel.(map[string]any)
		if !ok {
			t.Fatalf("supported_reasoning_levels[%d] = %#v, want object", index, rawLevel)
		}
		if got, _ := levelEntry["effort"].(string); got != want[index] {
			t.Fatalf("supported_reasoning_levels[%d].effort = %q, want %q", index, got, want[index])
		}
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

func TestUpdateClientsRefreshesAccountBindBindings(t *testing.T) {
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
		FileName: "codex-after-api-key-save.json",
		Metadata: map[string]any{
			"id_token": testCodexJWT(t, "acct-after-save"),
		},
	})
	if err != nil {
		t.Fatalf("register auth: %v", err)
	}

	s := NewServer(cfg, authManager, sdkaccess.NewManager(), filepath.Join(tmpDir, "config.yaml"))
	updated := *cfg
	updated.SDKConfig.APIKeyAuthIdentityBindings = map[string]string{
		clientKey: "codex:chatgpt:acct-after-save",
	}
	s.UpdateClients(&updated)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("apiKey", clientKey)

	s.accountBindMiddleware()(c)

	if c.IsAborted() {
		t.Fatalf("middleware should not abort after UpdateClients refreshes binding config; status=%d", c.Writer.Status())
	}
	if got := sdkapi.BoundAuthIndexFromContext(c.Request.Context()); got != registered.Index {
		t.Fatalf("updated auth_identity binding resolved to %q, want current auth_index %q", got, registered.Index)
	}
}

func TestAccountBindMiddleware_UsesAuthenticatedUserAPIKey(t *testing.T) {
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
				clientKey: "codex:chatgpt:acct-user-key",
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
		FileName: "codex-user-key.json",
		Metadata: map[string]any{
			"id_token": testCodexJWT(t, "acct-user-key"),
		},
	})
	if err != nil {
		t.Fatalf("register auth: %v", err)
	}

	s := NewServer(cfg, authManager, sdkaccess.NewManager(), filepath.Join(tmpDir, "config.yaml"))
	s.UpdateBindingConfig(cfg)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("userApiKey", clientKey)

	s.accountBindMiddleware()(c)

	if c.IsAborted() {
		t.Fatalf("middleware should not abort when authenticated API key resolves; status=%d", c.Writer.Status())
	}
	if got := sdkapi.BoundAuthIndexFromContext(c.Request.Context()); got != registered.Index {
		t.Fatalf("authenticated API key binding resolved to %q, want current auth_index %q", got, registered.Index)
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
