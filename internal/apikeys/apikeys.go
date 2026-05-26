package apikeys

import (
	"context"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
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

func ClearConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	cfg.APIKeys = nil
	cfg.APIKeyAuthBindings = nil
	cfg.APIKeyAuthIdentityBindings = nil
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
