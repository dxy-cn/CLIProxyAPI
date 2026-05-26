package management

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestBuildAuthFileEntryExposesStableAuthIdentity(t *testing.T) {
	t.Parallel()

	handler := NewHandler(&config.Config{}, "", nil)
	entry := handler.buildAuthFileEntry(&coreauth.Auth{
		ID:       "codex-auth",
		Provider: "codex",
		FileName: "codex-after-refresh.json",
		Metadata: map[string]any{
			"id_token": testManagementCodexJWT(t, "acct-stable"),
		},
		Attributes: map[string]string{
			"path": t.TempDir() + "/codex-after-refresh.json",
		},
	})

	if entry == nil {
		t.Fatalf("buildAuthFileEntry returned nil")
	}
	if got := entry["auth_identity"]; got != "codex:chatgpt:acct-stable" {
		t.Fatalf("auth_identity = %v, want %q", got, "codex:chatgpt:acct-stable")
	}
}

func TestBuildAuthFileEntryExposesNonCodexStableAuthIdentity(t *testing.T) {
	t.Parallel()

	handler := NewHandler(&config.Config{}, "", nil)
	entry := handler.buildAuthFileEntry(&coreauth.Auth{
		ID:       "claude-user.json",
		Provider: "claude",
		FileName: "claude-user.json",
		Attributes: map[string]string{
			"path": t.TempDir() + "/claude-user.json",
		},
	})

	if entry == nil {
		t.Fatalf("buildAuthFileEntry returned nil")
	}
	if got := entry["auth_identity"]; got != "claude:file:claude-user.json" {
		t.Fatalf("auth_identity = %v, want %q", got, "claude:file:claude-user.json")
	}
}

func testManagementCodexJWT(t *testing.T, accountID string) string {
	t.Helper()

	header, err := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal JWT header: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
	if err != nil {
		t.Fatalf("marshal JWT payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
