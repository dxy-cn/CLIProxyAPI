package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeys"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

type fakeAPIKeyStore struct {
	records      []apikeys.Record
	replaceCalls int
	upsertCalls  int
	deleteCalls  int
}

func (s *fakeAPIKeyStore) ListAPIKeyRecords(context.Context) ([]apikeys.Record, error) {
	return append([]apikeys.Record(nil), s.records...), nil
}

func (s *fakeAPIKeyStore) ReplaceAPIKeyRecords(_ context.Context, records []apikeys.Record) ([]apikeys.Record, error) {
	s.replaceCalls++
	s.records = append([]apikeys.Record(nil), records...)
	for i := range s.records {
		if s.records[i].ID == 0 {
			s.records[i].ID = int64(i + 1)
		}
	}
	return append([]apikeys.Record(nil), s.records...), nil
}

func (s *fakeAPIKeyStore) UpsertAPIKeyRecord(_ context.Context, record apikeys.Record) ([]apikeys.Record, error) {
	s.upsertCalls++
	record = apikeys.NormalizeRecord(record)
	for i := range s.records {
		if (record.ID != 0 && s.records[i].ID == record.ID) || s.records[i].APIKey == record.APIKey {
			if record.ID == 0 {
				record.ID = s.records[i].ID
			}
			s.records[i] = record
			return append([]apikeys.Record(nil), s.records...), nil
		}
	}
	if record.ID == 0 {
		record.ID = int64(len(s.records) + 1)
	}
	s.records = append(s.records, record)
	return append([]apikeys.Record(nil), s.records...), nil
}

func (s *fakeAPIKeyStore) DeleteAPIKeyRecord(_ context.Context, record apikeys.Record) ([]apikeys.Record, error) {
	s.deleteCalls++
	record = apikeys.NormalizeRecord(record)
	next := s.records[:0]
	for _, existing := range s.records {
		if record.ID != 0 && existing.ID == record.ID {
			continue
		}
		if record.APIKey != "" && existing.APIKey == record.APIKey {
			continue
		}
		next = append(next, existing)
	}
	s.records = next
	return append([]apikeys.Record(nil), s.records...), nil
}

func TestPutAPIKeysUsesStoreWithoutWritingYaml(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	originalConfig := []byte("routing:\n  strategy: account-bind\napi-keys:\n  - old-key\n")
	if err := os.WriteFile(configPath, originalConfig, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg := &config.Config{}
	store := &fakeAPIKeyStore{}
	h := NewHandler(cfg, configPath, nil)
	h.apiKeyStore = store
	var hookCalled bool
	h.SetConfigUpdateHook(func(updated *config.Config) {
		hookCalled = true
		if updated != cfg {
			t.Fatalf("hook config pointer changed")
		}
	})

	body := `[
		{"api-key":"sk-java","name":"Java owner","auth_identity":"codex:chatgpt:acct-java","tags":["Java","Tod","Java",""]},
		{"api-key":"sk-go","name":"Go owner","tags":["Go"]}
	]`
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/v0/management/api-keys", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.PutAPIKeys(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("PutAPIKeys status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !hookCalled {
		t.Fatal("expected config update hook to be called")
	}
	if !reflect.DeepEqual([]string(cfg.APIKeys), []string{"sk-java", "sk-go"}) {
		t.Fatalf("cfg APIKeys = %#v", []string(cfg.APIKeys))
	}
	if got := cfg.APIKeyAuthIdentityBindings["sk-java"]; got != "codex:chatgpt:acct-java" {
		t.Fatalf("auth identity binding = %q", got)
	}
	if !reflect.DeepEqual(store.records[0].Tags, []string{"Java", "Tod"}) {
		t.Fatalf("stored tags = %#v", store.records[0].Tags)
	}

	var payload struct {
		APIKeys []apikeys.Record `json:"api-keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response JSON invalid: %v", err)
	}
	if len(payload.APIKeys) != 2 || payload.APIKeys[0].ID == 0 {
		t.Fatalf("response records not returned with IDs: %#v", payload.APIKeys)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	if string(data) != string(originalConfig) {
		t.Fatalf("db-backed api key update wrote config yaml: %s", string(data))
	}
}

func TestPutAPIKeysWithoutStoreAcceptsRecordBindings(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("routing:\n  strategy: account-bind\napi-keys:\n  - old-key\n"), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg := &config.Config{}
	h := NewHandler(cfg, configPath, nil)
	var hookCalled bool
	h.SetConfigUpdateHook(func(updated *config.Config) {
		hookCalled = true
		if updated != cfg {
			t.Fatalf("hook config pointer changed")
		}
	})

	body := `[
		{"api-key":"sk-java","name":"Java owner","auth_identity":"codex:chatgpt:acct-java","tags":["Java"]},
		{"api-key":"sk-go","name":"Go owner"}
	]`
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/v0/management/api-keys", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.PutAPIKeys(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("PutAPIKeys status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !hookCalled {
		t.Fatal("expected config update hook to be called")
	}
	if !reflect.DeepEqual([]string(cfg.APIKeys), []string{"sk-java", "sk-go"}) {
		t.Fatalf("cfg APIKeys = %#v", []string(cfg.APIKeys))
	}
	if got := cfg.APIKeyAuthIdentityBindings["sk-java"]; got != "codex:chatgpt:acct-java" {
		t.Fatalf("auth identity binding = %q", got)
	}
	if _, ok := cfg.APIKeyAuthIdentityBindings["sk-go"]; ok {
		t.Fatalf("unexpected auth identity binding for sk-go")
	}

	var payload struct {
		APIKeys []apikeys.Record `json:"api-keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response JSON invalid: %v", err)
	}
	if len(payload.APIKeys) != 2 {
		t.Fatalf("response records len = %d, want 2", len(payload.APIKeys))
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "sk-java") || !strings.Contains(text, "sk-go") {
		t.Fatalf("api keys missing from config yaml: %s", text)
	}
}

func TestPatchAPIKeysUsesStoreSingleRecord(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	store := &fakeAPIKeyStore{records: []apikeys.Record{
		{ID: 1, APIKey: "sk-java", Name: "Java owner"},
		{ID: 2, APIKey: "sk-go", Name: "Go owner"},
	}}
	h := NewHandler(cfg, "", nil)
	h.apiKeyStore = store

	body := `{"id":2,"api-key":"sk-go","name":"Go owner","auth_identity":"codex:chatgpt:acct-go","tags":["Go","Ai"]}`
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/api-keys", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.PatchAPIKeys(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("PatchAPIKeys status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if store.replaceCalls != 0 {
		t.Fatalf("PatchAPIKeys called replace %d times, want 0", store.replaceCalls)
	}
	if store.upsertCalls != 1 {
		t.Fatalf("PatchAPIKeys upsert calls = %d, want 1", store.upsertCalls)
	}
	if len(store.records) != 2 {
		t.Fatalf("store records len = %d, want 2", len(store.records))
	}
	if !reflect.DeepEqual(store.records[1].Tags, []string{"Go", "Ai"}) {
		t.Fatalf("stored tags = %#v", store.records[1].Tags)
	}
	if got := cfg.APIKeyAuthIdentityBindings["sk-go"]; got != "codex:chatgpt:acct-go" {
		t.Fatalf("auth identity binding = %q", got)
	}
}

func TestDeleteAPIKeysUsesStoreSingleRecord(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	store := &fakeAPIKeyStore{records: []apikeys.Record{
		{ID: 1, APIKey: "sk-java", Name: "Java owner"},
		{ID: 2, APIKey: "sk-go", Name: "Go owner"},
	}}
	h := NewHandler(cfg, "", nil)
	h.apiKeyStore = store

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodDelete, "/v0/management/api-keys?id=1", nil)

	h.DeleteAPIKeys(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("DeleteAPIKeys status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if store.replaceCalls != 0 {
		t.Fatalf("DeleteAPIKeys called replace %d times, want 0", store.replaceCalls)
	}
	if store.deleteCalls != 1 {
		t.Fatalf("DeleteAPIKeys delete calls = %d, want 1", store.deleteCalls)
	}
	if len(store.records) != 1 || store.records[0].APIKey != "sk-go" {
		t.Fatalf("store records = %#v", store.records)
	}
}
