package management

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
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
	if strings.Contains(sent[0], "account@example.com") {
		t.Fatalf("warning content must use note instead of account email: %s", sent[0])
	}
	if !strings.Contains(sent[0], "凭证名称: note-name") {
		t.Fatalf("warning content missing credential note: %s", sent[0])
	}
	if !strings.Contains(sent[0], "5小时限额: 15%") {
		t.Fatalf("warning content missing remaining quota: %s", sent[0])
	}
	expectedReset := time.Unix(1777777777, 0).Local().Format("2006-01-02 15:04")
	if !strings.Contains(sent[0], "重置时间: "+expectedReset) {
		t.Fatalf("warning content should include reset time: %s", sent[0])
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
			"note": "J ToC3 / 2026-05-13",
		},
		Metadata: map[string]any{"email": "account@example.com"},
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
	if !strings.Contains(sent[0], "5小时限额: 15%") {
		t.Fatalf("warning content must report five-hour limit: %s", sent[0])
	}
	if strings.Contains(sent[0], "account@example.com") {
		t.Fatalf("warning content must use credential note instead of account email: %s", sent[0])
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
					"primary_window": gin.H{"used_percent": 79, "limit_window_seconds": 18000},
				},
			},
		},
		{
			name:       "unsupported webhook",
			webhookURL: "https://example.com/webhook",
			threshold:  20,
			payload: gin.H{
				"rate_limit": gin.H{
					"primary_window": gin.H{"used_percent": 85, "limit_window_seconds": 18000},
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

func TestRedactedWeComWebhookErrorRemovesRobotKey(t *testing.T) {
	webhookURL := "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret-key"
	errText := redactedWeComWebhookError(webhookURL, context.Canceled)
	if strings.Contains(errText, "secret-key") {
		t.Fatalf("redacted error leaked webhook key: %s", errText)
	}

	errText = redactedWeComWebhookError(webhookURL, &urlErrorForTest{message: `Post "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret-key": dial failed`})
	if strings.Contains(errText, "secret-key") {
		t.Fatalf("redacted URL error leaked webhook key: %s", errText)
	}
	if !strings.Contains(errText, "key=%3Credacted%3E") {
		t.Fatalf("redacted URL error missing redacted key marker: %s", errText)
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
		authManager:        manager,
		quotaWarningSent:   make(map[string]struct{}),
		quotaWarningSender: func(_ context.Context, _ string, _ string) error { return nil },
	}

	h.quotaWarningFetcher = func(_ context.Context, auth *coreauth.Auth) (int, gin.H, error) {
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
		t.Fatalf("warning must not expose account email when note is present: %s", first)
	}

	h.SetConfig(&config.Config{
		QuotaWarning: config.QuotaWarning{
			WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test",
			Threshold:  25,
		},
	})
	waitForQuotaWarningSent(t, &sentMu, &sent, 2)
}

func TestSetConfigWebhookChangeAllowsWarningForSameWindowAndThreshold(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-auth",
		Index:    "codex-1",
		Provider: "codex",
		Attributes: map[string]string{
			"note": "webhook-note",
		},
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	h := &Handler{
		cfg: &config.Config{
			QuotaWarning: config.QuotaWarning{
				WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=old",
				Threshold:  20,
			},
		},
		authManager:      manager,
		quotaWarningSent: make(map[string]struct{}),
	}
	payload := gin.H{"rate_limit": gin.H{"primary_window": gin.H{
		"used_percent":         85,
		"limit_window_seconds": 18000,
		"reset_at":             1777777777,
	}}}
	h.quotaWarningFetcher = func(_ context.Context, auth *coreauth.Auth) (int, gin.H, error) {
		if auth.ID != "codex-auth" {
			t.Fatalf("unexpected auth: %s", auth.ID)
		}
		return 200, payload, nil
	}

	var sentMu sync.Mutex
	var sent []string
	h.quotaWarningSender = func(_ context.Context, webhookURL string, content string) error {
		sentMu.Lock()
		defer sentMu.Unlock()
		sent = append(sent, webhookURL+"|"+content)
		return nil
	}

	h.maybeSendCodexQuotaWarning(context.Background(), manager.List()[0], payload)
	waitForQuotaWarningSent(t, &sentMu, &sent, 1)

	h.SetConfig(&config.Config{
		QuotaWarning: config.QuotaWarning{
			WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=new",
			Threshold:  20,
		},
	})
	waitForQuotaWarningSent(t, &sentMu, &sent, 2)

	sentMu.Lock()
	second := sent[1]
	sentMu.Unlock()
	if !strings.Contains(second, "key=new") {
		t.Fatalf("warning after webhook change should target new webhook: %s", second)
	}
}

type urlErrorForTest struct {
	message string
}

func (e *urlErrorForTest) Error() string {
	return e.message
}

func TestQuotaWarningScannerRunsOnTick(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-auth",
		Index:    "codex-1",
		Provider: "codex",
		Attributes: map[string]string{
			"note": "scan-note",
		},
	}); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	h := &Handler{
		cfg: &config.Config{
			QuotaWarning: config.QuotaWarning{
				WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test",
				Threshold:  20,
			},
		},
		authManager:      manager,
		quotaWarningSent: make(map[string]struct{}),
	}

	fetched := make(chan struct{}, 1)
	h.quotaWarningFetcher = func(_ context.Context, auth *coreauth.Auth) (int, gin.H, error) {
		if auth.ID != "codex-auth" {
			t.Fatalf("unexpected auth: %s", auth.ID)
		}
		fetched <- struct{}{}
		return 200, gin.H{"rate_limit": gin.H{"primary_window": gin.H{
			"used_percent":         85,
			"limit_window_seconds": 18000,
			"reset_at":             1777777777,
		}}}, nil
	}

	sent := make(chan string, 1)
	h.quotaWarningSender = func(_ context.Context, _ string, content string) error {
		sent <- content
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.startQuotaWarningScanner(ctx, 10*time.Millisecond)

	select {
	case <-fetched:
	case <-time.After(time.Second):
		t.Fatal("expected scanner to fetch quota on tick")
	}

	select {
	case content := <-sent:
		if !strings.Contains(content, "凭证名称: scan-note") {
			t.Fatalf("unexpected warning content: %s", content)
		}
	case <-time.After(time.Second):
		t.Fatal("expected scanner to send quota warning")
	}
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
