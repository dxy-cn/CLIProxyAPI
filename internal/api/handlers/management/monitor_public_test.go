package management

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestPublicMonitorAPIKeyMiddlewareAllowsConfiguredKeyWithoutManagementAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHandler(&proxyconfig.Config{
		SDKConfig: proxyconfig.SDKConfig{
			APIKeys: proxyconfig.FlexAPIKeyList{"sk-valid"},
		},
	}, "", nil)

	router := gin.New()
	router.GET("/public-monitor", h.PublicMonitorAPIKeyMiddleware(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/public-monitor?api_key=sk-valid", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPublicMonitorAPIKeyMiddlewareRejectsUnknownKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHandler(&proxyconfig.Config{
		SDKConfig: proxyconfig.SDKConfig{
			APIKeys: proxyconfig.FlexAPIKeyList{"sk-valid"},
		},
	}, "", nil)

	router := gin.New()
	router.GET("/public-monitor", h.PublicMonitorAPIKeyMiddleware(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/public-monitor?api_key=sk-missing", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPublicMonitorAPIKeyMiddlewareAllowsConfigFileEntry(t *testing.T) {
	gin.SetMode(gin.TestMode)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configYaml := []byte(`
auth:
  providers:
    config-api-key:
      api-key-entries:
        - name: Portal Key
          api-key: sk-config-valid
`)
	if err := os.WriteFile(configPath, configYaml, 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	h := NewHandler(&proxyconfig.Config{}, configPath, nil)

	router := gin.New()
	router.GET("/public-monitor", h.PublicMonitorAPIKeyMiddleware(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/public-monitor?api_key=sk-config-valid", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPublicMonitorAPIKeyMiddlewareForcesValidatedKeyFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHandler(&proxyconfig.Config{
		SDKConfig: proxyconfig.SDKConfig{
			APIKeys: proxyconfig.FlexAPIKeyList{"sk-valid"},
		},
	}, "", nil)

	router := gin.New()
	router.GET("/public-monitor", h.PublicMonitorAPIKeyMiddleware(), func(c *gin.Context) {
		filter := h.buildMonitorRecordFilter(c, nil, nil, "")
		if filter.APIKey != "sk-valid" {
			t.Fatalf("public monitor filter used %q", filter.APIKey)
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/public-monitor?api_key=sk-valid&api=sk-other", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPublicMonitorKeyTokenStatsUsesCurrentKeyMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const clientKey = "sk-current"
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configYaml := []byte(`
api-keys:
  - name: Current Key Name
    api-key: sk-current
    auth_identity: codex:chatgpt:acct-bound
  - name: Other Key Name
    api-key: sk-other
`)
	if err := os.WriteFile(configPath, configYaml, 0o600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	registered, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-bound",
		Provider: "codex",
		Metadata: map[string]any{
			"id_token":     testMonitorCodexJWT(t, "acct-bound", "pro"),
			"access_token": "access-token",
			"note":         "Bound Credential Note",
		},
	})
	if err != nil {
		t.Fatalf("register auth: %v", err)
	}

	base := time.Date(2026, 5, 18, 12, 0, 0, 0, time.Local)
	h := newMonitorTestHandler(
		testUsageRecordWithAuth(base.Add(-2*time.Hour), clientKey, registered.Index, false),
		testUsageRecordWithAuth(base.Add(-1*time.Hour), "sk-other", registered.Index, false),
	)
	h.cfg = &proxyconfig.Config{
		SDKConfig: proxyconfig.SDKConfig{
			APIKeys: proxyconfig.FlexAPIKeyList{clientKey, "sk-other"},
			APIKeyAuthIdentityBindings: map[string]string{
				clientKey: "codex:chatgpt:acct-bound",
			},
		},
		Routing: proxyconfig.RoutingConfig{Strategy: "account-bind"},
	}
	h.configFilePath = configPath
	h.authManager = manager

	router := gin.New()
	router.GET("/public-monitor/key-token-stats", h.PublicMonitorAPIKeyMiddleware(), h.GetPublicMonitorKeyTokenStats)

	req := httptest.NewRequest(http.MethodGet, "/public-monitor/key-token-stats?api_key="+clientKey, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		PanelTitle string `json:"panel_title"`
		CurrentKey struct {
			APIKey      string `json:"api_key"`
			APIKeyName  string `json:"api_key_name"`
			DisplayName string `json:"display_name"`
		} `json:"current_key"`
		Items []struct {
			APIKey       string `json:"api_key"`
			APIKeyName   string `json:"api_key_name"`
			DisplayName  string `json:"display_name"`
			IsCurrentKey bool   `json:"is_current_key"`
			AuthIndex    string `json:"auth_index"`
			AuthNote     string `json:"auth_note"`
			TotalTokens  int64  `json:"total_tokens"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.PanelTitle != "Bound Credential Note" {
		t.Fatalf("panel_title = %q, want bound credential note", resp.PanelTitle)
	}
	if resp.CurrentKey.APIKey != clientKey || resp.CurrentKey.APIKeyName != "Current Key Name" || resp.CurrentKey.DisplayName != "Current Key Name" {
		t.Fatalf("unexpected current_key: %+v", resp.CurrentKey)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("public response must contain keys bound to current credential, got %+v", resp.Items)
	}
	var item struct {
		APIKey       string `json:"api_key"`
		APIKeyName   string `json:"api_key_name"`
		DisplayName  string `json:"display_name"`
		IsCurrentKey bool   `json:"is_current_key"`
		AuthIndex    string `json:"auth_index"`
		AuthNote     string `json:"auth_note"`
		TotalTokens  int64  `json:"total_tokens"`
	}
	for _, candidate := range resp.Items {
		if candidate.APIKey == clientKey {
			item = candidate
			break
		}
	}
	if item.APIKey != clientKey || item.APIKeyName != "Current Key Name" || item.DisplayName != "Current Key Name" || !item.IsCurrentKey {
		t.Fatalf("unexpected current key item metadata: %+v", item)
	}
	if item.AuthIndex != registered.Index || item.AuthNote != "Bound Credential Note" {
		t.Fatalf("unexpected auth metadata: %+v", item)
	}
	if item.TotalTokens != 30 {
		t.Fatalf("current key tokens = %d, want 30", item.TotalTokens)
	}
}

func TestBoundCodexAuthForMonitorKeyRequiresAccountBind(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const clientKey = "sk-valid"
	manager := coreauth.NewManager(nil, nil, nil)
	registered, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-bound",
		Provider: "codex",
		Metadata: map[string]any{
			"id_token":     testMonitorCodexJWT(t, "acct-bound", "pro"),
			"access_token": "access-token",
		},
	})
	if err != nil {
		t.Fatalf("register auth: %v", err)
	}

	h := NewHandler(&proxyconfig.Config{
		SDKConfig: proxyconfig.SDKConfig{
			APIKeys: proxyconfig.FlexAPIKeyList{clientKey},
			APIKeyAuthIdentityBindings: map[string]string{
				clientKey: "codex:chatgpt:acct-bound",
			},
		},
		Routing: proxyconfig.RoutingConfig{Strategy: "account-bind"},
	}, "", manager)

	got := h.boundCodexAuthForMonitorKey(clientKey)
	if got == nil {
		t.Fatal("expected bound codex auth")
	}
	if got.Index != registered.Index {
		t.Fatalf("bound auth index = %q, want %q", got.Index, registered.Index)
	}
	if planType := codexPlanTypeForQuota(got); planType != "pro" {
		t.Fatalf("codex plan type = %q, want pro", planType)
	}

	h.cfg.Routing.Strategy = "round-robin"
	if got := h.boundCodexAuthForMonitorKey(clientKey); got != nil {
		t.Fatalf("binding must be inactive outside account-bind, got %q", got.Index)
	}
}

func TestPublicMonitorCodexQuotaRejectsUnboundKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHandler(&proxyconfig.Config{
		SDKConfig: proxyconfig.SDKConfig{
			APIKeys: proxyconfig.FlexAPIKeyList{"sk-valid"},
		},
		Routing: proxyconfig.RoutingConfig{Strategy: "account-bind"},
	}, "", coreauth.NewManager(nil, nil, nil))

	router := gin.New()
	router.GET("/public-monitor/quota", h.PublicMonitorAPIKeyMiddleware(), h.GetPublicMonitorCodexQuota)

	req := httptest.NewRequest(http.MethodGet, "/public-monitor/quota?api_key=sk-valid", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("unexpected status: got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestPublicCodexQuotaResponseWhitelistsPublicFields(t *testing.T) {
	payload := gin.H{
		"plan_type": "pro",
		"user_id":   "user-secret",
		"rate_limit": gin.H{
			"allowed":       true,
			"limit_reached": false,
			"account_id":    "acct-secret",
			"primary_window": gin.H{
				"used_percent":         5,
				"limit_window_seconds": 18000,
				"reset_after_seconds":  3600,
				"raw_limit_id":         "secret-window",
			},
			"secondary_window": gin.H{
				"usedPercent":        16,
				"limitWindowSeconds": 604800,
				"resetAt":            1777777777,
				"raw_limit_id":       "secret-weekly-window",
			},
		},
	}

	result := publicCodexQuotaResponse(payload, nil)

	if result["plan_type"] != "pro" {
		t.Fatalf("plan_type = %v, want pro", result["plan_type"])
	}
	if _, ok := result["user_id"]; ok {
		t.Fatal("unexpected leaked user_id")
	}
	rateLimit, ok := result["rate_limit"].(gin.H)
	if !ok {
		t.Fatalf("rate_limit type = %T, want gin.H", result["rate_limit"])
	}
	if _, ok := rateLimit["account_id"]; ok {
		t.Fatal("unexpected leaked account_id")
	}
	primary, ok := rateLimit["primary_window"].(gin.H)
	if !ok {
		t.Fatalf("primary_window type = %T, want gin.H", rateLimit["primary_window"])
	}
	if _, ok := primary["raw_limit_id"]; ok {
		t.Fatal("unexpected leaked primary raw_limit_id")
	}
	if primary["used_percent"] != 5 {
		t.Fatalf("primary used_percent = %v, want 5", primary["used_percent"])
	}
	secondary, ok := rateLimit["secondary_window"].(gin.H)
	if !ok {
		t.Fatalf("secondary_window type = %T, want gin.H", rateLimit["secondary_window"])
	}
	if _, ok := secondary["raw_limit_id"]; ok {
		t.Fatal("unexpected leaked secondary raw_limit_id")
	}
	if secondary["used_percent"] != 16 {
		t.Fatalf("secondary used_percent = %v, want 16", secondary["used_percent"])
	}
}

func testMonitorCodexJWT(t *testing.T, accountID, planType string) string {
	t.Helper()

	header, err := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal jwt header: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
			"chatgpt_plan_type":  planType,
		},
	})
	if err != nil {
		t.Fatalf("marshal jwt payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + "."
}
