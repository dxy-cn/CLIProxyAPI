package cmd

import (
	"os"
	"strings"
	"testing"
)

func TestAuthManagerOnlyRegistersCodexAndClaudeCredentialFlows(t *testing.T) {
	source, err := os.ReadFile("auth_manager.go")
	if err != nil {
		t.Fatalf("read auth_manager.go: %v", err)
	}
	text := string(source)

	for _, required := range []string{"NewCodexAuthenticator", "NewClaudeAuthenticator"} {
		if !strings.Contains(text, required) {
			t.Fatalf("auth manager must register %s", required)
		}
	}
	for _, forbidden := range []string{"NewKimiAuthenticator", "NewXAIAuthenticator", "NewGeminiAuthenticator", "NewAntigravityAuthenticator"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("auth manager must not register unsupported credential flow %s", forbidden)
		}
	}
}
