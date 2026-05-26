package apikeys

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"gopkg.in/yaml.v3"
)

var ErrDuplicateAPIKey = errors.New("api key already exists")

type Record struct {
	ID           int64     `json:"id"`
	APIKey       string    `json:"api-key"`
	Name         string    `json:"name,omitempty"`
	AuthIdentity string    `json:"auth_identity,omitempty"`
	Tags         []string  `json:"tags,omitempty"`
	CreatedTime  time.Time `json:"created_time,omitempty"`
	UpdatedTime  time.Time `json:"updated_time,omitempty"`
}

type Store interface {
	ListAPIKeyRecords(ctx context.Context) ([]Record, error)
	ReplaceAPIKeyRecords(ctx context.Context, records []Record) ([]Record, error)
	CreateAPIKeyRecord(ctx context.Context, record Record) (Record, error)
	UpsertAPIKeyRecord(ctx context.Context, record Record) (Record, error)
	DeleteAPIKeyRecord(ctx context.Context, id int64) error
	DeleteAPIKeyRecordByKey(ctx context.Context, key string) error
}

func StripYAMLConfig(data []byte) ([]byte, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return data, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return data, nil
	}
	doc := root.Content[0]
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i] != nil && doc.Content[i].Value == "api-keys" {
			doc.Content = append(doc.Content[:i], doc.Content[i+2:]...)
			i -= 2
		}
	}
	out, err := yaml.Marshal(&root)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func ExtractYAMLRecords(data []byte) []Record {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil || len(root.Content) == 0 {
		return nil
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i] == nil || doc.Content[i].Value != "api-keys" {
			continue
		}
		seq := doc.Content[i+1]
		if seq == nil || seq.Kind != yaml.SequenceNode {
			return nil
		}
		return NormalizeRecords(recordsFromYAMLSequence(seq))
	}
	return nil
}

func ApplyToConfig(cfg *config.Config, records []Record) {
	if cfg == nil {
		return
	}
	normalized := NormalizeRecords(records)
	keys := make([]string, 0, len(normalized))
	bindings := make(map[string]string, len(normalized))
	for _, record := range normalized {
		keys = append(keys, record.APIKey)
		if record.AuthIdentity != "" {
			bindings[record.APIKey] = record.AuthIdentity
		}
	}
	cfg.APIKeys = config.FlexAPIKeyList(keys)
	cfg.APIKeyAuthBindings = nil
	if len(bindings) == 0 {
		cfg.APIKeyAuthIdentityBindings = nil
	} else {
		cfg.APIKeyAuthIdentityBindings = bindings
	}
}

func ClearConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	cfg.APIKeys = nil
	cfg.APIKeyAuthBindings = nil
	cfg.APIKeyAuthIdentityBindings = nil
}

func ApplyStoreToConfig(ctx context.Context, cfg *config.Config, store Store) error {
	if cfg == nil || store == nil {
		return nil
	}
	records, err := store.ListAPIKeyRecords(ctx)
	if err != nil {
		return err
	}
	ApplyToConfig(cfg, records)
	return nil
}

func NormalizeRecords(records []Record) []Record {
	if len(records) == 0 {
		return nil
	}
	out := make([]Record, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		normalized := NormalizeRecord(record)
		if normalized.APIKey == "" {
			continue
		}
		if _, ok := seen[normalized.APIKey]; ok {
			continue
		}
		seen[normalized.APIKey] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func NormalizeRecord(record Record) Record {
	record.APIKey = strings.TrimSpace(record.APIKey)
	record.Name = strings.TrimSpace(record.Name)
	record.AuthIdentity = strings.TrimSpace(record.AuthIdentity)
	record.Tags = NormalizeTags(record.Tags)
	return record
}

func NormalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

func recordsFromYAMLSequence(seq *yaml.Node) []Record {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	records := make([]Record, 0, len(seq.Content))
	for _, item := range seq.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			records = append(records, Record{APIKey: item.Value})
		case yaml.MappingNode:
			records = append(records, recordFromYAMLMapping(item))
		}
	}
	return records
}

func recordFromYAMLMapping(node *yaml.Node) Record {
	if node == nil || node.Kind != yaml.MappingNode {
		return Record{}
	}
	record := Record{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		if key == nil || value == nil || key.Kind != yaml.ScalarNode || value.Kind != yaml.ScalarNode {
			continue
		}
		switch key.Value {
		case "api-key", "apiKey", "key", "Key":
			record.APIKey = value.Value
		case "name":
			record.Name = value.Value
		case "auth_identity", "auth-identity", "authIdentity":
			record.AuthIdentity = value.Value
		}
	}
	return record
}
