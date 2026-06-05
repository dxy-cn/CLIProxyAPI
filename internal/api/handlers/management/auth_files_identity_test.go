package management

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
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

func TestBuildAuthFileEntryExposesLastErrorContext(t *testing.T) {
	t.Parallel()

	errorAt := time.Date(2026, 5, 27, 14, 42, 0, 0, time.UTC)
	handler := NewHandler(&config.Config{}, "", nil)
	entry := handler.buildAuthFileEntry(&coreauth.Auth{
		ID:            "codex-auth",
		Provider:      "codex",
		FileName:      "codex.json",
		Status:        coreauth.StatusError,
		StatusMessage: "request failed",
		UpdatedAt:     errorAt,
		LastError: &coreauth.Error{
			Code:       "invalid_request_error",
			Message:    "No tool call found",
			HTTPStatus: http.StatusBadRequest,
		},
		Attributes: map[string]string{
			"path":    t.TempDir() + "/codex.json",
			"api_key": "sk-test-1234567890",
		},
	})

	if entry == nil {
		t.Fatalf("buildAuthFileEntry returned nil")
	}
	lastError, ok := entry["last_error"].(*coreauth.Error)
	if !ok {
		t.Fatalf("last_error = %T, want *coreauth.Error", entry["last_error"])
	}
	if lastError.Message != "No tool call found" {
		t.Fatalf("last_error.message = %q, want %q", lastError.Message, "No tool call found")
	}
	if got := entry["last_error_at"]; got != errorAt {
		t.Fatalf("last_error_at = %v, want %v", got, errorAt)
	}
	if got, want := entry["last_error_api_key"], util.HideAPIKey("sk-test-1234567890"); got != want {
		t.Fatalf("last_error_api_key = %v, want %q", got, want)
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
