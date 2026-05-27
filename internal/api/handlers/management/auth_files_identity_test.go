package management

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
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

func TestBuildAuthFileEntryExposesCostCenter(t *testing.T) {
	t.Parallel()

	handler := NewHandler(&config.Config{}, "", nil)
	entry := handler.buildAuthFileEntry(&coreauth.Auth{
		ID:       "codex-auth",
		Provider: "codex",
		FileName: "codex.json",
		Metadata: map[string]any{
			"cost_center": " ToD ",
		},
		Attributes: map[string]string{
			"path": t.TempDir() + "/codex.json",
		},
	})

	if entry == nil {
		t.Fatalf("buildAuthFileEntry returned nil")
	}
	if got := entry["cost_center"]; got != "ToD" {
		t.Fatalf("cost_center = %v, want %q", got, "ToD")
	}
}

func TestAuthFileJSONCostCenterReadsSnakeCase(t *testing.T) {
	t.Parallel()

	if got := authFileJSONCostCenter([]byte(`{"type":"codex","cost_center":" ToD "}`)); got != "ToD" {
		t.Fatalf("authFileJSONCostCenter = %q, want %q", got, "ToD")
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
