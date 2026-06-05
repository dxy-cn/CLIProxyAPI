package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/apikeys"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func TestGetMonitorKeyTokenStats_AggregatesByAPIKey(t *testing.T) {
	base := time.Date(2026, 4, 21, 12, 0, 0, 0, time.Local)
	h := newMonitorTestHandler(
		testUsageRecordWithAuthAndSource(base.Add(-4*time.Hour), "api-a", "auth-1", "burn-source", false),
		testUsageRecordWithAuthAndSource(base.Add(-3*time.Hour), "api-b", "auth-1", "burn-source", false),
		testUsageRecordWithAuthAndSource(base.Add(-2*time.Hour), "api-a", "auth-2", "burn-source", false),
		testUsageRecordWithAuth(base.Add(-90*time.Minute), "api-d", "auth-2", false),
		testUsageRecordWithAuth(base.Add(-60*time.Minute), "api-c", "auth-2", true),
		testUsageRecordWithAuth(base.Add(-30*time.Hour), "api-old", "auth-1", false),
	)

	startQuery := url.QueryEscape(base.Add(-6 * time.Hour).Format(time.RFC3339))
	endQuery := url.QueryEscape(base.Format(time.RFC3339))
	rr := executeMonitorRequest(h.GetMonitorKeyTokenStats, "/monitor/key-token-stats?start_time="+startQuery+"&end_time="+endQuery)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Total       int              `json:"total"`
		TotalTokens int64            `json:"total_tokens"`
		Account     map[string]int64 `json:"account_totals"`
		Items       []struct {
			APIKey            string           `json:"api_key"`
			AuthIndex         string           `json:"auth_index"`
			Requests          int64            `json:"requests"`
			TotalTokens       int64            `json:"total_tokens"`
			AccountTokens     int64            `json:"account_tokens"`
			AccountTokenShare float64          `json:"account_token_share"`
			TotalTokenShare   float64          `json:"total_token_share"`
			AuthTokens        map[string]int64 `json:"auth_tokens"`
			SourceTokens      map[string]int64 `json:"source_tokens"`
			ModelTokens       map[string]struct {
				Requests     int64 `json:"requests"`
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
				CachedTokens int64 `json:"cached_tokens"`
				TotalTokens  int64 `json:"total_tokens"`
			} `json:"model_tokens"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Total != 4 {
		t.Fatalf("unexpected total: got %d want 4", resp.Total)
	}
	if resp.TotalTokens != 120 {
		t.Fatalf("unexpected total tokens: got %d want 120", resp.TotalTokens)
	}
	if resp.Account["auth-1"] != 60 || resp.Account["auth-2"] != 60 {
		t.Fatalf("unexpected account totals: %+v", resp.Account)
	}
	if len(resp.Items) != 4 {
		t.Fatalf("unexpected items count: got %d want 4", len(resp.Items))
	}

	first := resp.Items[0]
	if first.APIKey != "api-a" || first.AuthIndex != "auth-1" {
		t.Fatalf("unexpected first item identity: %+v", first)
	}
	if first.Requests != 2 || first.TotalTokens != 60 || first.AccountTokens != 60 {
		t.Fatalf("unexpected first aggregate: %+v", first)
	}
	if first.AccountTokenShare != 50 || first.TotalTokenShare != 50 {
		t.Fatalf("unexpected first shares: account=%.1f total=%.1f", first.AccountTokenShare, first.TotalTokenShare)
	}
	if first.AuthTokens["auth-1"] != 30 || first.AuthTokens["auth-2"] != 30 {
		t.Fatalf("unexpected first auth token breakdown: %+v", first.AuthTokens)
	}
	if first.SourceTokens["burn-source"] != 60 {
		t.Fatalf("unexpected first source token breakdown: %+v", first.SourceTokens)
	}
	if first.ModelTokens["model-a"].Requests != 2 ||
		first.ModelTokens["model-a"].InputTokens != 20 ||
		first.ModelTokens["model-a"].OutputTokens != 40 ||
		first.ModelTokens["model-a"].TotalTokens != 60 {
		t.Fatalf("unexpected first model token breakdown: %+v", first.ModelTokens)
	}
}

func TestGetMonitorKeyTokenStats_PublicMonitorScopesToBoundCredentialAndUsesStoredNames(t *testing.T) {
	gin.SetMode(gin.TestMode)

	base := time.Date(2026, 5, 27, 10, 0, 0, 0, time.Local)
	h := newMonitorTestHandler(
		testUsageRecordWithAuth(base.Add(-10*time.Minute), "sk-peer", "auth-shared", false),
		testUsageRecordWithAuth(base.Add(-5*time.Minute), "sk-other", "auth-other", false),
	)

	manager := coreauth.NewManager(nil, nil, nil)
	sharedAuth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-shared",
		Provider: "codex",
		FileName: "Toc-claudecode.json",
		Metadata: map[string]any{
			"note":     "Toc-claudecode",
			"id_token": testMonitorCodexJWT(t, "acct-shared", "pro"),
		},
	})
	if err != nil {
		t.Fatalf("register shared auth: %v", err)
	}
	otherAuth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-other",
		Provider: "codex",
		FileName: "Other-credential.json",
		Metadata: map[string]any{
			"id_token": testMonitorCodexJWT(t, "acct-other", "pro"),
		},
	})
	if err != nil {
		t.Fatalf("register other auth: %v", err)
	}

	h.authManager = manager
	h.cfg = &config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys: config.FlexAPIKeyList{"sk-current", "sk-peer", "sk-other"},
			APIKeyAuthIdentityBindings: map[string]string{
				"sk-current": sharedAuth.StableIdentity(),
				"sk-peer":    sharedAuth.StableIdentity(),
				"sk-other":   otherAuth.StableIdentity(),
			},
		},
		Routing: config.RoutingConfig{Strategy: "account-bind"},
	}
	h.apiKeyStore = &fakeAPIKeyStore{
		records: []apikeys.Record{
			{APIKey: "sk-current", Name: "吕长奇", AuthIdentity: sharedAuth.StableIdentity()},
			{APIKey: "sk-peer", Name: "toc-public", AuthIdentity: sharedAuth.StableIdentity()},
			{APIKey: "sk-other", Name: "other-key", AuthIdentity: otherAuth.StableIdentity()},
		},
	}

	router := gin.New()
	router.GET(
		"/public-monitor/key-token-stats",
		h.PublicMonitorAPIKeyMiddleware(),
		h.GetMonitorKeyTokenStats,
	)

	startQuery := url.QueryEscape(base.Add(-30 * time.Minute).Format(time.RFC3339))
	endQuery := url.QueryEscape(base.Format(time.RFC3339))
	req := httptest.NewRequest(
		http.MethodGet,
		"/public-monitor/key-token-stats?api_key=sk-current&start_time="+startQuery+"&end_time="+endQuery,
		nil,
	)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Total       int    `json:"total"`
		TotalTokens int64  `json:"total_tokens"`
		PanelTitle  string `json:"panel_title"`
		CurrentKey  struct {
			APIKey      string `json:"api_key"`
			APIKeyName  string `json:"api_key_name"`
			DisplayName string `json:"display_name"`
		} `json:"current_key"`
		Items []struct {
			APIKey       string `json:"api_key"`
			APIKeyName   string `json:"api_key_name"`
			DisplayName  string `json:"display_name"`
			IsCurrentKey bool   `json:"is_current_key"`
			TotalTokens  int64  `json:"total_tokens"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Total != 2 {
		t.Fatalf("unexpected total: got %d want 2", resp.Total)
	}
	if resp.TotalTokens != 30 {
		t.Fatalf("unexpected total tokens: got %d want 30", resp.TotalTokens)
	}
	if resp.PanelTitle != "Toc-claudecode" {
		t.Fatalf("panel_title = %q, want %q", resp.PanelTitle, "Toc-claudecode")
	}
	if resp.CurrentKey.APIKeyName != "吕长奇" || resp.CurrentKey.DisplayName != "吕长奇" {
		t.Fatalf("unexpected current key metadata: %+v", resp.CurrentKey)
	}
	if got, want := resp.CurrentKey.APIKey, util.HideAPIKey("sk-current"); got != want {
		t.Fatalf("current_key.api_key = %q, want %q", got, want)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("unexpected items count: got %d want 2", len(resp.Items))
	}

	type responseItem struct {
		APIKey       string
		APIKeyName   string
		DisplayName  string
		IsCurrentKey bool
		TotalTokens  int64
	}
	itemByName := make(map[string]responseItem, len(resp.Items))
	for _, item := range resp.Items {
		itemByName[item.APIKeyName] = responseItem(item)
	}

	currentItem, ok := itemByName["吕长奇"]
	if !ok {
		t.Fatalf("missing current key item: %+v", resp.Items)
	}
	if !currentItem.IsCurrentKey || currentItem.APIKeyName != "吕长奇" || currentItem.TotalTokens != 0 {
		t.Fatalf("unexpected current key item: %+v", currentItem)
	}
	if currentItem.APIKey != util.HideAPIKey("sk-current") {
		t.Fatalf("current key item api_key = %q, want %q", currentItem.APIKey, util.HideAPIKey("sk-current"))
	}

	peerItem, ok := itemByName["toc-public"]
	if !ok {
		t.Fatalf("missing peer key item: %+v", resp.Items)
	}
	if peerItem.DisplayName != "toc-public" || peerItem.TotalTokens != 30 {
		t.Fatalf("unexpected peer key item: %+v", peerItem)
	}
	if peerItem.APIKey != util.HideAPIKey("sk-peer") {
		t.Fatalf("peer key item api_key = %q, want %q", peerItem.APIKey, util.HideAPIKey("sk-peer"))
	}

	if _, exists := itemByName["other-key"]; exists {
		t.Fatalf("unexpected unrelated key included: %+v", itemByName["other-key"])
	}
}

func testUsageRecordWithAuth(ts time.Time, apiKey, authIndex string, failed bool) coreusage.Record {
	record := testUsageRecord(ts, apiKey, "model-a", "source-a", failed, 1000, 200)
	record.AuthIndex = authIndex
	return record
}

func testUsageRecordWithAuthAndSource(ts time.Time, apiKey, authIndex, source string, failed bool) coreusage.Record {
	record := testUsageRecord(ts, apiKey, "model-a", source, failed, 1000, 200)
	record.AuthIndex = authIndex
	return record
}
