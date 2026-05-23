package apikeys

import (
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
