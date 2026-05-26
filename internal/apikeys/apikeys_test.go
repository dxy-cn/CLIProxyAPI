package apikeys

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestApplyToConfigUsesDatabaseRecordsOnly(t *testing.T) {
	records := []Record{
		{APIKey: " sk-plain "},
		{APIKey: "sk-object", Name: "Alice", AuthIdentity: " codex:chatgpt:acct-1 ", Tags: []string{"Java", "", "Tod", "Java"}},
		{APIKey: "sk-camel", AuthIdentity: "codex:chatgpt:acct-2", Tags: []string{"Go"}},
	}

	records = NormalizeRecords(records)
	if len(records) != 3 {
		t.Fatalf("records len = %d, want 3: %#v", len(records), records)
	}
	if records[1].APIKey != "sk-object" || records[1].Name != "Alice" || records[1].AuthIdentity != "codex:chatgpt:acct-1" {
		t.Fatalf("object record not extracted correctly: %#v", records[1])
	}
	if !reflect.DeepEqual(records[1].Tags, []string{"Java", "Tod"}) {
		t.Fatalf("tags = %#v, want Java/Tod", records[1].Tags)
	}
	if !reflect.DeepEqual(records[2].Tags, []string{"Go"}) {
		t.Fatalf("scalar tags = %#v, want Go", records[2].Tags)
	}

	cfg := &config.Config{}
	ApplyToConfig(cfg, records)
	if !reflect.DeepEqual([]string(cfg.APIKeys), []string{"sk-plain", "sk-object", "sk-camel"}) {
		t.Fatalf("cfg APIKeys = %#v", []string(cfg.APIKeys))
	}
	wantBindings := map[string]string{
		"sk-object": "codex:chatgpt:acct-1",
		"sk-camel":  "codex:chatgpt:acct-2",
	}
	if !reflect.DeepEqual(cfg.APIKeyAuthIdentityBindings, wantBindings) {
		t.Fatalf("identity bindings = %#v, want %#v", cfg.APIKeyAuthIdentityBindings, wantBindings)
	}
}

func TestApplyStoreToConfigUsesStoreRecordsOnly(t *testing.T) {
	cfg := &config.Config{
		SDKConfig: config.SDKConfig{
			APIKeys: config.FlexAPIKeyList{"sk-yaml", "sk-db"},
			APIKeyAuthIdentityBindings: map[string]string{
				"sk-yaml": "codex:chatgpt:acct-yaml",
				"sk-db":   "codex:chatgpt:acct-yaml-db",
			},
		},
	}
	store := staticStore{
		records: []Record{{APIKey: "sk-db", AuthIdentity: "codex:chatgpt:acct-db"}},
	}
	if err := ApplyStoreToConfig(nil, cfg, store); err != nil {
		t.Fatalf("ApplyStoreToConfig returned error: %v", err)
	}
	if !reflect.DeepEqual([]string(cfg.APIKeys), []string{"sk-db"}) {
		t.Fatalf("cfg APIKeys = %#v", []string(cfg.APIKeys))
	}
	if got := cfg.APIKeyAuthIdentityBindings["sk-db"]; got != "codex:chatgpt:acct-db" {
		t.Fatalf("db identity binding = %q", got)
	}
}

func TestExtractYAMLRecordsReadsLegacyAPIKeys(t *testing.T) {
	data := []byte(`
api-keys:
  - sk-plain
  - name: Alice
    api-key: sk-alice
    auth_identity: codex:chatgpt:acct-alice
  - name: Bob
    key: sk-bob
    auth-identity: codex:chatgpt:acct-bob
`)

	records := ExtractYAMLRecords(data)
	if len(records) != 3 {
		t.Fatalf("records len = %d, want 3: %#v", len(records), records)
	}
	if records[0].APIKey != "sk-plain" {
		t.Fatalf("scalar key = %#v", records[0])
	}
	if records[1].APIKey != "sk-alice" || records[1].Name != "Alice" || records[1].AuthIdentity != "codex:chatgpt:acct-alice" {
		t.Fatalf("object record = %#v", records[1])
	}
	if records[2].APIKey != "sk-bob" || records[2].Name != "Bob" || records[2].AuthIdentity != "codex:chatgpt:acct-bob" {
		t.Fatalf("alternate object record = %#v", records[2])
	}
}

func TestStripYAMLConfigRemovesTopLevelAPIKeys(t *testing.T) {
	data := []byte("routing:\n  strategy: account-bind\napi-keys:\n  - sk-yaml\nrequest-log: true\n")

	stripped, err := StripYAMLConfig(data)
	if err != nil {
		t.Fatalf("StripYAMLConfig returned error: %v", err)
	}
	text := string(stripped)
	if strings.Contains(text, "api-keys") || strings.Contains(text, "sk-yaml") {
		t.Fatalf("stripped yaml still contains api keys: %s", text)
	}
	if !strings.Contains(text, "routing:") || !strings.Contains(text, "request-log: true") {
		t.Fatalf("stripped yaml lost non-key config: %s", text)
	}
}

type staticStore struct {
	records []Record
}

func (s staticStore) ListAPIKeyRecords(context.Context) ([]Record, error) {
	return append([]Record(nil), s.records...), nil
}

func (s staticStore) ReplaceAPIKeyRecords(context.Context, []Record) ([]Record, error) {
	return nil, nil
}

func (s staticStore) CreateAPIKeyRecord(context.Context, Record) (Record, error) {
	return Record{}, nil
}

func (s staticStore) UpsertAPIKeyRecord(context.Context, Record) (Record, error) {
	return Record{}, nil
}

func (s staticStore) DeleteAPIKeyRecord(context.Context, int64) error {
	return nil
}

func (s staticStore) DeleteAPIKeyRecordByKey(context.Context, string) error {
	return nil
}
