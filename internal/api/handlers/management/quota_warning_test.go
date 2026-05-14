package management

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestMaybeSendCodexQuotaWarningSendsOnceBelowThreshold(t *testing.T) {
	h := &Handler{
		cfg: &config.Config{
			QuotaWarning: config.QuotaWarning{
				WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test",
				Threshold:  20,
			},
		},
		quotaWarningSent: make(map[string]struct{}),
	}

	var sent []string
	h.quotaWarningSender = func(_ context.Context, webhookURL string, content string) error {
		sent = append(sent, webhookURL+"|"+content)
		return nil
	}

	auth := &coreauth.Auth{
		ID:       "auth-1",
		Index:    "codex-1",
		Provider: "codex",
		FileName: "codex-user.json",
		Attributes: map[string]string{
			"note": "note-name",
		},
		Metadata: map[string]any{"email": "account@example.com"},
	}
	payload := gin.H{
		"plan_type": "pro",
		"rate_limit": gin.H{
			"allowed":       true,
			"limit_reached": false,
			"primary_window": gin.H{
				"used_percent":         85,
				"limit_window_seconds": 18000,
				"reset_at":             1777777777,
			},
		},
	}

	h.maybeSendCodexQuotaWarning(context.Background(), auth, payload)
	h.maybeSendCodexQuotaWarning(context.Background(), auth, payload)

	if len(sent) != 1 {
		t.Fatalf("sent warnings = %d, want 1", len(sent))
	}
	if strings.Contains(sent[0], "窗口:") {
		t.Fatalf("warning content must not include standalone window line: %s", sent[0])
	}
	if strings.Contains(sent[0], "account@example.com") {
		t.Fatalf("warning content must use note instead of account email: %s", sent[0])
	}
	if strings.Contains(sent[0], "> 凭证:") {
		t.Fatalf("warning content must use credential-name label: %s", sent[0])
	}
	if !strings.Contains(sent[0], "凭证名称: note-name") {
		t.Fatalf("warning content missing credential note: %s", sent[0])
	}
	if strings.Contains(sent[0], "剩余额度") {
		t.Fatalf("warning content must not use legacy remaining-quota label: %s", sent[0])
	}
	if strings.Contains(sent[0], "5小时限额") {
		t.Fatalf("warning content must not use legacy five-hour limit label: %s", sent[0])
	}
	if !strings.Contains(sent[0], "5小时剩余: 15%") {
		t.Fatalf("warning content missing remaining quota: %s", sent[0])
	}
	if strings.Contains(sent[0], "阈值:") {
		t.Fatalf("warning content must not include threshold line: %s", sent[0])
	}
	if strings.Contains(sent[0], "重置时间") {
		t.Fatalf("warning content must not include reset time line: %s", sent[0])
	}
}

func TestMaybeSendCodexQuotaWarningOnlyChecksFiveHourLimit(t *testing.T) {
	h := &Handler{
		cfg: &config.Config{
			QuotaWarning: config.QuotaWarning{
				WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test",
				Threshold:  20,
			},
		},
		quotaWarningSent: make(map[string]struct{}),
	}

	var sent []string
	h.quotaWarningSender = func(_ context.Context, _ string, content string) error {
		sent = append(sent, content)
		return nil
	}

	auth := &coreauth.Auth{
		Provider: "codex",
		Attributes: map[string]string{
			"note": "✈️J ToC3 / 2026-05-13",
		},
		Metadata: map[string]any{"email": "panwenwen092@gmail.com"},
	}
	payload := gin.H{
		"rate_limit": gin.H{
			"primary_window": gin.H{
				"used_percent":         50,
				"limit_window_seconds": 18000,
				"reset_at":             1777777777,
			},
			"secondary_window": gin.H{
				"used_percent":         95,
				"limit_window_seconds": 604800,
				"reset_at":             1777777777,
			},
		},
	}

	h.maybeSendCodexQuotaWarning(context.Background(), auth, payload)
	if len(sent) != 0 {
		t.Fatalf("weekly limit must not trigger quota warning: %v", sent)
	}

	payload["rate_limit"].(gin.H)["primary_window"] = gin.H{
		"used_percent":         85,
		"limit_window_seconds": 18000,
		"reset_at":             1777777777,
	}
	h.maybeSendCodexQuotaWarning(context.Background(), auth, payload)
	if len(sent) != 1 {
		t.Fatalf("five-hour limit below threshold should trigger once, got %d", len(sent))
	}
	if strings.Contains(sent[0], "周限额") {
		t.Fatalf("warning content must not report weekly limit: %s", sent[0])
	}
	if !strings.Contains(sent[0], "5小时剩余: 15%") {
		t.Fatalf("warning content must report five-hour limit: %s", sent[0])
	}
	if strings.Contains(sent[0], "panwenwen092@gmail.com") {
		t.Fatalf("warning content must use credential note instead of account email: %s", sent[0])
	}
	if !strings.Contains(sent[0], "凭证名称: ✈️J ToC3 / 2026-05-13") {
		t.Fatalf("warning content must include credential note: %s", sent[0])
	}
	if strings.Contains(sent[0], "重置时间") {
		t.Fatalf("warning content must not include reset time line: %s", sent[0])
	}
}

func TestMaybeSendCodexQuotaWarningOmitsResetTime(t *testing.T) {
	h := &Handler{
		cfg: &config.Config{
			QuotaWarning: config.QuotaWarning{
				WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test",
				Threshold:  20,
			},
		},
		quotaWarningSent: make(map[string]struct{}),
	}

	var sent string
	h.quotaWarningSender = func(_ context.Context, _ string, content string) error {
		sent = content
		return nil
	}

	h.maybeSendCodexQuotaWarning(context.Background(), &coreauth.Auth{Provider: "codex"}, gin.H{
		"rate_limit": gin.H{
			"primary_window": gin.H{
				"used_percent":         85,
				"limit_window_seconds": 18000,
				"reset_after_seconds":  3670,
			},
		},
	})

	if !strings.Contains(sent, "5小时剩余: 15%") {
		t.Fatalf("warning content missing five-hour remaining quota: %s", sent)
	}
	if strings.Contains(sent, "重置时间") {
		t.Fatalf("warning content must not include reset time: %s", sent)
	}
}

func TestMaybeSendCodexQuotaWarningIgnoresAboveThresholdAndUnsupportedWebhook(t *testing.T) {
	tests := []struct {
		name       string
		webhookURL string
		threshold  int
		payload    gin.H
	}{
		{
			name:       "above threshold",
			webhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test",
			threshold:  20,
			payload: gin.H{
				"rate_limit": gin.H{
					"primary_window": gin.H{"used_percent": 79},
				},
			},
		},
		{
			name:       "unsupported webhook",
			webhookURL: "https://example.com/webhook",
			threshold:  20,
			payload: gin.H{
				"rate_limit": gin.H{
					"primary_window": gin.H{"used_percent": 85},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{
				cfg: &config.Config{
					QuotaWarning: config.QuotaWarning{
						WebhookURL: tt.webhookURL,
						Threshold:  tt.threshold,
					},
				},
				quotaWarningSent: make(map[string]struct{}),
			}
			h.quotaWarningSender = func(_ context.Context, _ string, _ string) error {
				t.Fatal("unexpected warning send")
				return nil
			}

			h.maybeSendCodexQuotaWarning(context.Background(), &coreauth.Auth{Provider: "codex"}, tt.payload)
		})
	}
}

func TestSetConfigThresholdChangeFetchesCodexCredentialsAndWarnsBelowNewThreshold(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	below, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "below-auth",
		Index:    "codex-below",
		Provider: "codex",
		Attributes: map[string]string{
			"note": "below-note",
		},
		Metadata: map[string]any{"email": "codex-below@example.com"},
	})
	if err != nil {
		t.Fatalf("register below auth: %v", err)
	}
	above, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "above-auth",
		Index:    "codex-above",
		Provider: "codex",
		Attributes: map[string]string{
			"note": "above-note",
		},
		Metadata: map[string]any{"email": "codex-above@example.com"},
	})
	if err != nil {
		t.Fatalf("register above auth: %v", err)
	}
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "gemini-auth",
		Index:    "gemini-1",
		Provider: "gemini",
	}); err != nil {
		t.Fatalf("register non-codex auth: %v", err)
	}

	h := &Handler{
		cfg: &config.Config{
			QuotaWarning: config.QuotaWarning{
				WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test",
				Threshold:  10,
			},
		},
		authManager:      manager,
		quotaWarningSent: make(map[string]struct{}),
	}

	h.quotaWarningQuotaFetcher = func(_ context.Context, auth *coreauth.Auth) (int, gin.H, error) {
		switch auth.ID {
		case below.ID:
			return 200, gin.H{"rate_limit": gin.H{"primary_window": gin.H{
				"used_percent":         85,
				"limit_window_seconds": 18000,
				"reset_at":             1777777777,
			}}}, nil
		case above.ID:
			return 200, gin.H{"rate_limit": gin.H{"primary_window": gin.H{
				"used_percent":         70,
				"limit_window_seconds": 18000,
				"reset_at":             1777777777,
			}}}, nil
		default:
			t.Errorf("unexpected quota fetch for auth %s provider %s", auth.ID, auth.Provider)
			return 0, nil, nil
		}
	}

	var sentMu sync.Mutex
	var sent []string
	h.quotaWarningSender = func(_ context.Context, _ string, content string) error {
		sentMu.Lock()
		defer sentMu.Unlock()
		sent = append(sent, content)
		return nil
	}

	h.SetConfig(&config.Config{
		QuotaWarning: config.QuotaWarning{
			WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test",
			Threshold:  20,
		},
	})
	waitForQuotaWarningSent(t, &sentMu, &sent, 1)
	sentMu.Lock()
	first := sent[0]
	sentMu.Unlock()
	if !strings.Contains(first, "凭证名称: below-note") {
		t.Fatalf("warning must target the below-threshold credential: %s", first)
	}
	if strings.Contains(first, "codex-above@example.com") || strings.Contains(first, "codex-below@example.com") {
		t.Fatalf("warning must not target above-threshold credential: %s", first)
	}

	h.SetConfig(&config.Config{
		QuotaWarning: config.QuotaWarning{
			WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test",
			Threshold:  25,
		},
	})
	waitForQuotaWarningSent(t, &sentMu, &sent, 2)
}

func TestSetConfigDoesNotFetchQuotaWhenWarningDisabled(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-auth",
		Index:    "codex-1",
		Provider: "codex",
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	h := &Handler{
		cfg: &config.Config{
			QuotaWarning: config.QuotaWarning{
				WebhookURL: "",
				Threshold:  0,
			},
		},
		authManager:      manager,
		quotaWarningSent: make(map[string]struct{}),
	}
	h.quotaWarningQuotaFetcher = func(_ context.Context, _ *coreauth.Auth) (int, gin.H, error) {
		t.Error("quota fetch must not run while quota warning is disabled")
		return 0, nil, nil
	}
	h.quotaWarningSender = func(_ context.Context, _ string, _ string) error {
		t.Error("warning must not be sent while quota warning is disabled")
		return nil
	}

	h.SetConfig(&config.Config{QuotaWarning: config.QuotaWarning{Threshold: 20}})
	time.Sleep(20 * time.Millisecond)
	h.SetConfig(&config.Config{QuotaWarning: config.QuotaWarning{
		WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test",
		Threshold:  0,
	}})
	time.Sleep(20 * time.Millisecond)
}

func waitForQuotaWarningSent(t *testing.T, mu *sync.Mutex, sent *[]string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(*sent)
		mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	got := len(*sent)
	mu.Unlock()
	t.Fatalf("sent warnings = %d, want %d", got, want)
}
