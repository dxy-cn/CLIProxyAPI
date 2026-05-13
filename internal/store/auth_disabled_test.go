package store

import (
	"os"
	"path/filepath"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestAuthDisabledStateFromMetadata(t *testing.T) {
	disabled, status := authDisabledStateFromMetadata(map[string]any{"disabled": true})
	if !disabled {
		t.Fatal("disabled = false, want true")
	}
	if status != cliproxyauth.StatusDisabled {
		t.Fatalf("status = %q, want %q", status, cliproxyauth.StatusDisabled)
	}

	disabled, status = authDisabledStateFromMetadata(map[string]any{})
	if disabled {
		t.Fatal("disabled = true, want false")
	}
	if status != cliproxyauth.StatusActive {
		t.Fatalf("status = %q, want %q", status, cliproxyauth.StatusActive)
	}
}

func TestSyncAuthDisabledMetadata(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Disabled: true,
		Metadata: map[string]any{},
	}
	syncAuthDisabledMetadata(auth)
	if disabled, _ := auth.Metadata["disabled"].(bool); !disabled {
		t.Fatalf("metadata disabled = %#v, want true", auth.Metadata["disabled"])
	}

	auth.Disabled = false
	syncAuthDisabledMetadata(auth)
	if disabled, _ := auth.Metadata["disabled"].(bool); disabled {
		t.Fatalf("metadata disabled = %#v, want false", auth.Metadata["disabled"])
	}
}

func TestGitTokenStoreReadAuthFilePreservesDisabledMetadata(t *testing.T) {
	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "codex.json")
	if err := os.WriteFile(path, []byte(`{"type":"codex","disabled":true}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	store := NewGitTokenStore("", "", "", "")
	auth, err := store.readAuthFile(path, baseDir)
	if err != nil {
		t.Fatalf("readAuthFile: %v", err)
	}
	if auth == nil {
		t.Fatal("auth = nil")
	}
	if !auth.Disabled {
		t.Fatal("auth.Disabled = false, want true")
	}
	if auth.Status != cliproxyauth.StatusDisabled {
		t.Fatalf("auth.Status = %q, want %q", auth.Status, cliproxyauth.StatusDisabled)
	}
}

func TestObjectTokenStoreReadAuthFilePreservesDisabledMetadata(t *testing.T) {
	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "codex.json")
	if err := os.WriteFile(path, []byte(`{"type":"codex","disabled":true}`), 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	store := &ObjectTokenStore{}
	auth, err := store.readAuthFile(path, baseDir)
	if err != nil {
		t.Fatalf("readAuthFile: %v", err)
	}
	if auth == nil {
		t.Fatal("auth = nil")
	}
	if !auth.Disabled {
		t.Fatal("auth.Disabled = false, want true")
	}
	if auth.Status != cliproxyauth.StatusDisabled {
		t.Fatalf("auth.Status = %q, want %q", auth.Status, cliproxyauth.StatusDisabled)
	}
}
