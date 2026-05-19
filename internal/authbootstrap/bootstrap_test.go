package authbootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type memoryStore struct {
	items map[string]*coreauth.Auth
	saves int
}

func newMemoryStore(auths ...*coreauth.Auth) *memoryStore {
	store := &memoryStore{items: make(map[string]*coreauth.Auth)}
	for _, auth := range auths {
		store.items[auth.ID] = auth
	}
	return store
}

func (s *memoryStore) List(context.Context) ([]*coreauth.Auth, error) {
	out := make([]*coreauth.Auth, 0, len(s.items))
	for _, auth := range s.items {
		out = append(out, auth.Clone())
	}
	return out, nil
}

func (s *memoryStore) Save(_ context.Context, auth *coreauth.Auth) (string, error) {
	s.saves++
	s.items[auth.ID] = auth.Clone()
	return auth.ID, nil
}

func (s *memoryStore) Delete(_ context.Context, id string) error {
	delete(s.items, id)
	return nil
}

func TestImportSkipsExistingAuthByDefault(t *testing.T) {
	dir := t.TempDir()
	writeAuthFile(t, filepath.Join(dir, "claude.json"), map[string]any{
		"type":  "claude",
		"email": "new@example.com",
	})
	store := newMemoryStore(&coreauth.Auth{
		ID:       "claude.json",
		Provider: "claude",
		Metadata: map[string]any{"email": "old@example.com"},
	})

	result, err := Import(context.Background(), store, Options{Dir: dir})
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}
	if result.Imported != 0 || result.Skipped != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if store.saves != 0 {
		t.Fatalf("existing auth should not be overwritten by default, saves=%d", store.saves)
	}
}

func TestImportCopiesAuthIntoTargetStoreWhenOverwriteEnabled(t *testing.T) {
	dir := t.TempDir()
	writeAuthFile(t, filepath.Join(dir, "claude.json"), map[string]any{
		"type":      "claude",
		"email":     "new@example.com",
		"proxy_url": "http://proxy.local",
	})
	store := newMemoryStore(&coreauth.Auth{
		ID:       "claude.json",
		Provider: "claude",
		Metadata: map[string]any{"email": "old@example.com"},
	})

	result, err := Import(context.Background(), store, Options{Dir: dir, Overwrite: true})
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}
	if result.Imported != 1 || result.Skipped != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	saved := store.items["claude.json"]
	if saved == nil {
		t.Fatal("expected claude.json to be saved")
	}
	if saved.FileName != "claude.json" {
		t.Fatalf("expected target filename to stay relative, got %q", saved.FileName)
	}
	if got := saved.Metadata["email"]; got != "new@example.com" {
		t.Fatalf("expected new metadata to be saved, got %v", got)
	}
	if path := saved.Attributes["path"]; path != "" {
		t.Fatalf("bootstrap source path must not be persisted as target path, got %q", path)
	}
}

func writeAuthFile(t *testing.T, path string, payload map[string]any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal auth payload: %v", err)
	}
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write auth file: %v", err)
	}
}
