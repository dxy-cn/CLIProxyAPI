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
	if strings.Contains(sent[0], "剩余额度") {
		t.Fatalf("warning content must not use legacy remaining-quota label: %s", sent[0])
	}
	if !strings.Contains(sent[0], "5小时限额: 15%") {
		t.Fatalf("warning content missing remaining quota: %s", sent[0])
	}
	if strings.Contains(sent[0], "阈值:") {
		t.Fatalf("warning content must not include threshold line: %s", sent[0])
	}
	expectedReset := time.Unix(1777777777, 0).Local().Format("2006-01-02 15:04")
	if !strings.Contains(sent[0], "重置时间: "+expectedReset) {
		t.Fatalf("warning content should format reset_at timestamp: %s", sent[0])
	}
}

func TestMaybeSendCodexQuotaWarningFormatsResetAfterSecondsAsResetTime(t *testing.T) {
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

	if !strings.Contains(sent, "5小时限额: 15%") {
		t.Fatalf("warning content missing five-hour remaining quota: %s", sent)
	}
	resetText := quotaWarningLineValue(sent, "> 重置时间: ")
	resetTime, err := time.ParseInLocation("2006-01-02 15:04", resetText, time.Local)
	if err != nil {
		t.Fatalf("warning content should format reset_after_seconds as reset time: %s", sent)
	}
	want := time.Now().Add(3670 * time.Second)
	if resetTime.Before(want.Add(-time.Minute)) || resetTime.After(want.Add(time.Minute)) {
		t.Fatalf("reset time = %s, want around %s in content %s", resetText, want.Format("2006-01-02 15:04"), sent)
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
		Label:    "codex-below",
	})
	if err != nil {
		t.Fatalf("register below auth: %v", err)
	}
	above, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "above-auth",
		Index:    "codex-above",
		Provider: "codex",
		Label:    "codex-above",
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
	if !strings.Contains(first, "凭证: codex-below") {
		t.Fatalf("warning must target the below-threshold credential: %s", first)
	}
	if strings.Contains(first, "codex-above") {
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

func quotaWarningLineValue(content string, prefix string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
