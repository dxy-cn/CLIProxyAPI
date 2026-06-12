package auth

import (
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestRefreshRegistryOnlyRegistersCodexAndClaudeCredentialFlows(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		if lead := cliproxyauth.ProviderRefreshLead(provider, nil); lead == nil {
			t.Fatalf("ProviderRefreshLead(%q) = nil, want registered refresh lead", provider)
		}
	}

	for _, provider := range []string{"kimi", "xai", "qwen", "gemini", "antigravity"} {
		if lead := cliproxyauth.ProviderRefreshLead(provider, nil); lead != nil {
			t.Fatalf("ProviderRefreshLead(%q) = %v, want nil", provider, *lead)
		}
	}
}
