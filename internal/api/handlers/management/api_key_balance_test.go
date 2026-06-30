package management

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/usage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func TestAPIKeyBalanceScannerUsesOneHourIntervalAndFiveHourWindow(t *testing.T) {
	if apiKeyBalanceScanInterval != time.Hour {
		t.Fatalf("apiKeyBalanceScanInterval = %s, want 1h", apiKeyBalanceScanInterval)
	}
	if apiKeyBalanceWindow != 5*time.Hour {
		t.Fatalf("apiKeyBalanceWindow = %s, want 5h", apiKeyBalanceWindow)
	}
}

func TestRebalanceAPIKeyBindingsMovesOnlyKeysBoundToAutoBalanceCredentials(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	manager := coreauth.NewManager(nil, nil, nil)
	autoA := registerBalanceTestCodexAuth(t, manager, "a.json", "acct-a", true)
	autoB := registerBalanceTestCodexAuth(t, manager, "b.json", "acct-b", true)
	manual := registerBalanceTestCodexAuth(t, manager, "manual.json", "acct-manual", false)

	cfg := &config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys: config.FlexAPIKeyList{"sk-heavy", "sk-mid", "sk-small", "sk-manual"},
			APIKeyAuthIdentityBindings: map[string]string{
				"sk-heavy":  autoA.StableIdentity(),
				"sk-mid":    autoA.StableIdentity(),
				"sk-small":  autoB.StableIdentity(),
				"sk-manual": manual.StableIdentity(),
			},
		},
		Routing: config.RoutingConfig{Strategy: "account-bind"},
	}
	stats := usage.NewRequestStatistics()
	stats.Record(context.Background(), balanceUsageRecord(now.Add(-1*time.Hour), "sk-heavy", 90))
	stats.Record(context.Background(), balanceUsageRecord(now.Add(-1*time.Hour), "sk-mid", 50))
	stats.Record(context.Background(), balanceUsageRecord(now.Add(-1*time.Hour), "sk-small", 10))
	stats.Record(context.Background(), balanceUsageRecord(now.Add(-1*time.Hour), "sk-manual", 200))
	stats.Record(context.Background(), balanceUsageRecord(now.Add(-6*time.Hour), "sk-small", 500))

	h := &Handler{cfg: cfg, authManager: manager, usageStats: stats}
	result, err := h.rebalanceAPIKeyBindingsAt(context.Background(), now)
	if err != nil {
		t.Fatalf("rebalanceAPIKeyBindingsAt() error = %v", err)
	}

	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok (reason=%q)", result.Status, result.Reason)
	}
	if result.Credentials != 2 {
		t.Fatalf("credentials = %d, want 2", result.Credentials)
	}
	if result.Keys != 3 {
		t.Fatalf("keys = %d, want 3", result.Keys)
	}
	if result.Changed != 1 {
		t.Fatalf("changed = %d, want 1; assignments=%+v", result.Changed, result.Assignments)
	}
	if got := cfg.APIKeyAuthIdentityBindings["sk-heavy"]; got != autoA.StableIdentity() {
		t.Fatalf("sk-heavy binding = %q, want %q", got, autoA.StableIdentity())
	}
	if got := cfg.APIKeyAuthIdentityBindings["sk-mid"]; got != autoB.StableIdentity() {
		t.Fatalf("sk-mid binding = %q, want %q", got, autoB.StableIdentity())
	}
	if got := cfg.APIKeyAuthIdentityBindings["sk-small"]; got != autoB.StableIdentity() {
		t.Fatalf("sk-small binding = %q, want %q", got, autoB.StableIdentity())
	}
	if got := cfg.APIKeyAuthIdentityBindings["sk-manual"]; got != manual.StableIdentity() {
		t.Fatalf("manual credential binding changed to %q", got)
	}
}

func TestRebalanceAPIKeyBindingsBalancesKeyCountsWhenTokenDemandIsEqual(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	manager := coreauth.NewManager(nil, nil, nil)
	autoA := registerBalanceTestCodexAuth(t, manager, "a.json", "acct-a", true)
	autoB := registerBalanceTestCodexAuth(t, manager, "b.json", "acct-b", true)

	cfg := &config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys: config.FlexAPIKeyList{"sk-1", "sk-2", "sk-3", "sk-4"},
			APIKeyAuthIdentityBindings: map[string]string{
				"sk-1": autoA.StableIdentity(),
				"sk-2": autoA.StableIdentity(),
				"sk-3": autoA.StableIdentity(),
				"sk-4": autoA.StableIdentity(),
			},
		},
		Routing: config.RoutingConfig{Strategy: "account-bind"},
	}

	h := &Handler{cfg: cfg, authManager: manager, usageStats: usage.NewRequestStatistics()}
	result, err := h.rebalanceAPIKeyBindingsAt(context.Background(), now)
	if err != nil {
		t.Fatalf("rebalanceAPIKeyBindingsAt() error = %v", err)
	}

	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok (reason=%q)", result.Status, result.Reason)
	}
	if result.Changed != 2 {
		t.Fatalf("changed = %d, want 2; assignments=%+v", result.Changed, result.Assignments)
	}
	counts := map[string]int{}
	for _, identity := range cfg.APIKeyAuthIdentityBindings {
		counts[identity]++
	}
	if counts[autoA.StableIdentity()] != 2 || counts[autoB.StableIdentity()] != 2 {
		t.Fatalf("binding counts = %+v, want two keys per auto-balance credential", counts)
	}
}

func TestPlanAPIKeyBalancePrioritizesFiveHourTokenDemandBeforeKeyCount(t *testing.T) {
	credentials := []apiKeyBalanceCredential{
		{Identity: "codex:chatgpt:a"},
		{Identity: "codex:chatgpt:b"},
	}
	participants := []apiKeyBalanceParticipant{
		{APIKey: "sk-heavy", AuthIdentity: "codex:chatgpt:a", TotalTokens: 100},
		{APIKey: "sk-small-1", AuthIdentity: "codex:chatgpt:a", TotalTokens: 1},
		{APIKey: "sk-small-2", AuthIdentity: "codex:chatgpt:a", TotalTokens: 1},
		{APIKey: "sk-small-3", AuthIdentity: "codex:chatgpt:a", TotalTokens: 1},
	}

	plans := planAPIKeyBalance(participants, credentials)
	tokens := map[string]int64{}
	counts := map[string]int{}
	for _, plan := range plans {
		tokens[plan.ToIdentity] += plan.TotalTokens
		counts[plan.ToIdentity]++
	}

	if tokens["codex:chatgpt:a"] != 100 || tokens["codex:chatgpt:b"] != 3 {
		t.Fatalf("tokens = %+v, want heavy key isolated before balancing key counts", tokens)
	}
	if counts["codex:chatgpt:a"] != 1 || counts["codex:chatgpt:b"] != 3 {
		t.Fatalf("counts = %+v, want token balance to take precedence over key count balance", counts)
	}
}

func registerBalanceTestCodexAuth(t *testing.T, manager *coreauth.Manager, name, accountID string, autoBalance bool) *coreauth.Auth {
	t.Helper()
	auth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       name,
		FileName: name,
		Provider: "codex",
		Metadata: map[string]any{
			"type":         "codex",
			"auto_balance": autoBalance,
			"id_token": balanceTestJWT(t, map[string]any{
				"https://api.openai.com/auth": map[string]any{
					"chatgpt_account_id": accountID,
				},
			}),
		},
	})
	if err != nil {
		t.Fatalf("register auth %s: %v", name, err)
	}
	return auth
}

func balanceUsageRecord(ts time.Time, apiKey string, totalTokens int64) coreusage.Record {
	return coreusage.Record{
		APIKey:      apiKey,
		Model:       "gpt-5.4",
		Source:      "test",
		RequestedAt: ts,
		Detail: coreusage.Detail{
			TotalTokens: totalTokens,
		},
	}
}

func balanceTestJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal JWT header: %v", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal JWT payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
