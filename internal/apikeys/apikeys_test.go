package apikeys

import (
	"reflect"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestExtractYAMLRecordsAndApplyToConfig(t *testing.T) {
	data := []byte(`
api-keys:
  - sk-plain
  - api-key: sk-object
    name: Alice
    auth_identity: codex:chatgpt:acct-1
    tags:
      - Java
      - ""
      - Tod
      - Java
  - apiKey: sk-camel
    authIdentity: codex:chatgpt:acct-2
    tags: Go
`)

	records, err := ExtractYAMLRecords(data)
	if err != nil {
		t.Fatalf("ExtractYAMLRecords returned error: %v", err)
	}
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
