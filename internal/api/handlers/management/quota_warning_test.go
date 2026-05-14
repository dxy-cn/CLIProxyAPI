package management

import (
	"context"
	"strings"
	"testing"

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
	if !strings.Contains(sent[0], "5小时剩余额度: 15%") {
		t.Fatalf("warning content missing remaining quota: %s", sent[0])
	}
	if strings.Contains(sent[0], "阈值:") {
		t.Fatalf("warning content must not include threshold line: %s", sent[0])
	}
	if !strings.Contains(sent[0], "重置: 1777777777") {
		t.Fatalf("warning content should prefer reset_at: %s", sent[0])
	}
}

func TestMaybeSendCodexQuotaWarningFormatsResetAfterSecondsAsMinutes(t *testing.T) {
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

	if !strings.Contains(sent, "5小时剩余额度: 15%") {
		t.Fatalf("warning content missing five-hour remaining quota: %s", sent)
	}
	if !strings.Contains(sent, "重置: 61分钟后") {
		t.Fatalf("warning content should format reset_after_seconds as minutes: %s", sent)
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
