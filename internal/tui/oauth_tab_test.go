package tui

import "testing"

func TestOAuthTabProvidersOnlyExposeCodexAndAnthropicCredentialFlows(t *testing.T) {
	got := make([]string, 0, len(oauthProviders))
	for _, provider := range oauthProviders {
		got = append(got, provider.apiPath)
	}

	want := []string{"anthropic-auth-url", "codex-auth-url"}
	if len(got) != len(want) {
		t.Fatalf("oauthProviders = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("oauthProviders = %v, want %v", got, want)
		}
	}
}
