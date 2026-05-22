package apikeys

import (
	"context"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"gopkg.in/yaml.v3"
)

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
	UpsertAPIKeyRecord(ctx context.Context, record Record) (Record, error)
	DeleteAPIKeyRecord(ctx context.Context, id int64) error
	DeleteAPIKeyRecordByKey(ctx context.Context, key string) error
}

func ExtractYAMLRecords(data []byte) ([]Record, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, nil
	}
	doc := root.Content[0]
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i] == nil || doc.Content[i].Value != "api-keys" {
			continue
		}
		return recordsFromSequence(doc.Content[i+1]), nil
	}
	return nil, nil
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

func recordsFromSequence(seq *yaml.Node) []Record {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	records := make([]Record, 0, len(seq.Content))
	for _, item := range seq.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			record := NormalizeRecord(Record{APIKey: item.Value})
			if record.APIKey != "" {
				records = append(records, record)
			}
		case yaml.MappingNode:
			record := NormalizeRecord(Record{
				APIKey:       mappingScalar(item, "api-key", "apiKey", "key", "Key"),
				Name:         mappingScalar(item, "name"),
				AuthIdentity: mappingScalar(item, "auth_identity", "auth-identity", "authIdentity"),
				Tags:         mappingTags(item, "tags"),
			})
			if record.APIKey != "" {
				records = append(records, record)
			}
		}
	}
	return NormalizeRecords(records)
}

func mappingScalar(node *yaml.Node, names ...string) string {
	if node == nil || node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		if key == nil {
			continue
		}
		for _, name := range names {
			if key.Value == name {
				if value := node.Content[i+1]; value != nil && value.Kind == yaml.ScalarNode {
					return value.Value
				}
			}
		}
	}
	return ""
}

func mappingTags(node *yaml.Node, name string) []string {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		if key == nil || key.Value != name || value == nil {
			continue
		}
		switch value.Kind {
		case yaml.ScalarNode:
			return NormalizeTags([]string{value.Value})
		case yaml.SequenceNode:
			tags := make([]string, 0, len(value.Content))
			for _, item := range value.Content {
				if item != nil && item.Kind == yaml.ScalarNode {
					tags = append(tags, item.Value)
				}
			}
			return NormalizeTags(tags)
		}
	}
	return nil
}
