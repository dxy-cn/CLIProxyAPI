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
	"github.com/router-for-me/CLIProxyAPI/v7/internal/apikeys"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

type fakeAPIKeyStore struct {
	records []apikeys.Record
}

func (s *fakeAPIKeyStore) ListAPIKeyRecords(context.Context) ([]apikeys.Record, error) {
	return append([]apikeys.Record(nil), s.records...), nil
}

func (s *fakeAPIKeyStore) ReplaceAPIKeyRecords(_ context.Context, records []apikeys.Record) ([]apikeys.Record, error) {
	s.records = append([]apikeys.Record(nil), records...)
	for i := range s.records {
		if s.records[i].ID == 0 {
			s.records[i].ID = int64(i + 1)
		}
	}
	return append([]apikeys.Record(nil), s.records...), nil
}

func (s *fakeAPIKeyStore) UpsertAPIKeyRecord(_ context.Context, record apikeys.Record) (apikeys.Record, error) {
	if record.ID != 0 {
		for i := range s.records {
			if s.records[i].ID == record.ID {
				s.records[i] = record
				return record, nil
			}
		}
	}
	for i := range s.records {
		if s.records[i].APIKey == record.APIKey {
			if record.ID == 0 {
				record.ID = s.records[i].ID
			}
			s.records[i] = record
			return record, nil
		}
	}
	if record.ID == 0 {
		record.ID = int64(len(s.records) + 1)
	}
	s.records = append(s.records, record)
	return record, nil
}

func (s *fakeAPIKeyStore) DeleteAPIKeyRecord(_ context.Context, id int64) error {
	for i := range s.records {
		if s.records[i].ID == id {
			s.records = append(s.records[:i], s.records[i+1:]...)
			return nil
		}
	}
	return nil
}

func (s *fakeAPIKeyStore) DeleteAPIKeyRecordByKey(_ context.Context, key string) error {
	for i := range s.records {
		if s.records[i].APIKey == key {
			s.records = append(s.records[:i], s.records[i+1:]...)
			return nil
		}
	}
	return nil
}

func TestPutAPIKeysUsesStoreAndRefreshesRuntimeConfig(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("routing:\n  strategy: account-bind\napi-keys:\n  - old-key\n"), 0o600); err != nil {
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
	if strings.Contains(string(data), "sk-java") || strings.Contains(string(data), "sk-go") {
		t.Fatalf("db-backed api keys leaked back into config yaml: %s", string(data))
	}
}

func TestPatchAPIKeysUsesStoreRecordPayload(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("routing:\n  strategy: account-bind\napi-keys:\n  - old-key\n"), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg := &config.Config{}
	h := NewHandler(cfg, configPath, nil)
	h.apiKeyStore = &fakeAPIKeyStore{
		records: []apikeys.Record{
			{ID: 1, APIKey: "sk-one", Name: "One"},
			{ID: 2, APIKey: "sk-two", Name: "Two"},
		},
	}

	body := `{"id":2,"index":1,"api-key":"sk-two-new","name":"Two New","auth_identity":"claude:account:dyf1269651709@gmail.com"}`
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/api-keys", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.PatchAPIKeys(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("PatchAPIKeys status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !reflect.DeepEqual([]string(cfg.APIKeys), []string{"sk-one", "sk-two-new"}) {
		t.Fatalf("cfg APIKeys = %#v", []string(cfg.APIKeys))
	}
	if got := cfg.APIKeyAuthIdentityBindings["sk-two-new"]; got != "claude:account:dyf1269651709@gmail.com" {
		t.Fatalf("auth identity binding = %q", got)
	}

	var payload struct {
		APIKeys []apikeys.Record `json:"api-keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response JSON invalid: %v", err)
	}
	if len(payload.APIKeys) != 2 || payload.APIKeys[1].ID != 2 || payload.APIKeys[1].Name != "Two New" {
		t.Fatalf("response records not returned: %#v", payload.APIKeys)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	if strings.Contains(string(data), "sk-two-new") {
		t.Fatalf("db-backed api key leaked back into config yaml: %s", string(data))
	}
}

func TestGetAPIKeysFallsBackToYAMLWhenStoreEmpty(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	initial := []byte(`
api-keys:
  - name: YAML Owner
    api-key: sk-yaml
    auth_identity: codex:chatgpt:acct-yaml
`)
	if err := os.WriteFile(configPath, initial, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	h := NewHandler(&config.Config{}, configPath, nil)
	h.apiKeyStore = &fakeAPIKeyStore{}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/api-keys", nil)
	h.GetAPIKeys(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("GetAPIKeys status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var payload struct {
		APIKeys []apikeys.Record `json:"api-keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response JSON invalid: %v", err)
	}
	if len(payload.APIKeys) != 1 {
		t.Fatalf("fallback records = %#v", payload.APIKeys)
	}
	got := payload.APIKeys[0]
	if got.APIKey != "sk-yaml" || got.Name != "YAML Owner" || got.AuthIdentity != "codex:chatgpt:acct-yaml" {
		t.Fatalf("fallback record = %#v", got)
	}
}

func TestGetAPIKeysMergesStoreWithMissingYAMLKeys(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
api-keys:
  - name: YAML Owner
    api-key: sk-yaml
  - name: YAML DB Owner
    api-key: sk-db
`), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	h := NewHandler(&config.Config{}, configPath, nil)
	h.apiKeyStore = &fakeAPIKeyStore{
		records: []apikeys.Record{{ID: 1, APIKey: "sk-db", Name: "DB Owner"}},
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/api-keys", nil)
	h.GetAPIKeys(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("GetAPIKeys status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var payload struct {
		APIKeys []apikeys.Record `json:"api-keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response JSON invalid: %v", err)
	}
	if len(payload.APIKeys) != 2 {
		t.Fatalf("merged records = %#v", payload.APIKeys)
	}
	if payload.APIKeys[0].APIKey != "sk-db" || payload.APIKeys[0].Name != "DB Owner" {
		t.Fatalf("store record not preferred: %#v", payload.APIKeys)
	}
	if payload.APIKeys[1].APIKey != "sk-yaml" || payload.APIKeys[1].Name != "YAML Owner" {
		t.Fatalf("yaml missing key not merged: %#v", payload.APIKeys)
	}
}

func TestConfigYAMLDoesNotExposeOrPersistAPIKeysWhenStoreBacked(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	initial := []byte("routing:\n  strategy: account-bind\napi-keys:\n  - old-yaml-key\n")
	if err := os.WriteFile(configPath, initial, 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	h := NewHandler(&config.Config{}, configPath, nil)
	h.apiKeyStore = &fakeAPIKeyStore{}

	getRec := httptest.NewRecorder()
	getCtx, _ := gin.CreateTestContext(getRec)
	getCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/config.yaml", nil)
	h.GetConfigYAML(getCtx)

	if getRec.Code != http.StatusOK {
		t.Fatalf("GetConfigYAML status = %d, want %d; body=%s", getRec.Code, http.StatusOK, getRec.Body.String())
	}
	if strings.Contains(getRec.Body.String(), "api-keys") || strings.Contains(getRec.Body.String(), "old-yaml-key") {
		t.Fatalf("db-backed config yaml exposed api keys: %s", getRec.Body.String())
	}

	putBody := "routing:\n  strategy: account-bind\napi-keys:\n  - new-yaml-key\n"
	putRec := httptest.NewRecorder()
	putCtx, _ := gin.CreateTestContext(putRec)
	putCtx.Request = httptest.NewRequest(http.MethodPut, "/v0/management/config.yaml", strings.NewReader(putBody))
	putCtx.Request.Header.Set(configHashHeader, getRec.Header().Get(configHashHeader))
	h.PutConfigYAML(putCtx)

	if putRec.Code != http.StatusOK {
		t.Fatalf("PutConfigYAML status = %d, want %d; body=%s", putRec.Code, http.StatusOK, putRec.Body.String())
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}
	if strings.Contains(string(data), "api-keys") || strings.Contains(string(data), "new-yaml-key") {
		t.Fatalf("db-backed config yaml persisted api keys: %s", string(data))
	}
}
