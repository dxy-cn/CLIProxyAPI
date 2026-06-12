package api

import (
	"os"
	"strings"
	"testing"
)

func TestManagementOAuthRoutesOnlyExposeCodexAndAnthropicCredentialFlows(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	text := string(source)

	for _, required := range []string{
		`mgmt.GET("/anthropic-auth-url", s.mgmt.RequestAnthropicToken)`,
		`mgmt.GET("/codex-auth-url", s.mgmt.RequestCodexToken)`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("management OAuth route %q is missing", required)
		}
	}

	for _, forbidden := range []string{
		`gemini-auth-url`,
		`antigravity-auth-url`,
		`kimi-auth-url`,
		`xai-auth-url`,
		`RequestGeminiCLIToken`,
		`RequestAntigravityToken`,
		`RequestKimiToken`,
		`RequestXAIToken`,
		`WriteOAuthCallbackFileForPendingSession(s.cfg.AuthDir, "gemini"`,
		`WriteOAuthCallbackFileForPendingSession(s.cfg.AuthDir, "antigravity"`,
		`WriteOAuthCallbackFileForPendingSession(s.cfg.AuthDir, "xai"`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("management OAuth routes must only expose Codex/Anthropic; found %q", forbidden)
		}
	}
}
