package management

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeys"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"gopkg.in/yaml.v3"
)

type monitorYAMLRecord map[string]any
type monitorAPIKeyRecord struct {
	APIKey       string
	Name         string
	AuthIdentity string
}

const publicMonitorAPIKeyContextKey = "public_monitor_api_key"

func asMonitorYAMLRecord(value any) monitorYAMLRecord {
	record, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return record
}

func monitorYAMLString(record monitorYAMLRecord, keys ...string) string {
	if record == nil {
		return ""
	}
	for _, key := range keys {
		value, ok := record[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok {
			trimmed := strings.TrimSpace(text)
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func monitorYAMLSlice(record monitorYAMLRecord, keys ...string) []any {
	if record == nil {
		return nil
	}
	for _, key := range keys {
		value, ok := record[key]
		if !ok {
			continue
		}
		if items, ok := value.([]any); ok {
			return items
		}
	}
	return nil
}

func collectMonitorAPIKeyRecords(entries []any) []monitorAPIKeyRecord {
	if len(entries) == 0 {
		return nil
	}

	records := make([]monitorAPIKeyRecord, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		record := asMonitorYAMLRecord(entry)
		apiKey := monitorYAMLString(record, "api-key", "apiKey", "key", "Key")
		if apiKey == "" {
			if text, ok := entry.(string); ok {
				apiKey = strings.TrimSpace(text)
			}
		}
		if apiKey == "" {
			continue
		}
		if _, exists := seen[apiKey]; exists {
			continue
		}
		seen[apiKey] = struct{}{}
		records = append(records, monitorAPIKeyRecord{
			APIKey:       apiKey,
			Name:         monitorYAMLString(record, "name"),
			AuthIdentity: monitorYAMLString(record, "auth_identity", "auth-identity", "authIdentity"),
		})
	}
	if len(records) == 0 {
		return nil
	}
	return records
}

func parseMonitorAPIKeyRecords(data []byte) []monitorAPIKeyRecord {
	if len(data) == 0 {
		return nil
	}

	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil
	}

	topLevelEntries := monitorYAMLSlice(root, "api-keys")
	authBlock := asMonitorYAMLRecord(root["auth"])
	providers := asMonitorYAMLRecord(authBlock["providers"])
	configAPIKeyProvider := asMonitorYAMLRecord(providers["config-api-key"])
	if configAPIKeyProvider != nil {
		providerEntries := monitorYAMLSlice(configAPIKeyProvider, "api-key-entries", "api-keys")
		if records := collectMonitorAPIKeyRecords(providerEntries); len(records) > 0 {
			return records
		}
	}
	return collectMonitorAPIKeyRecords(topLevelEntries)
}

func parseMonitorAPIKeyConfigMap(data []byte) map[string]string {
	records := parseMonitorAPIKeyRecords(data)
	if len(records) == 0 {
		return nil
	}
	keys := make(map[string]string, len(records))
	for _, record := range records {
		keys[record.APIKey] = record.Name
	}
	return keys
}

func parseMonitorAPIKeyNameMap(data []byte) map[string]string {
	records := parseMonitorAPIKeyRecords(data)
	if len(records) == 0 {
		return nil
	}
	names := make(map[string]string, len(records))
	for _, record := range records {
		if record.Name == "" {
			continue
		}
		names[record.APIKey] = record.Name
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

func normalizeMonitorAPIKeyRecords(records []monitorAPIKeyRecord) []monitorAPIKeyRecord {
	if len(records) == 0 {
		return nil
	}
	out := make([]monitorAPIKeyRecord, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		record.APIKey = strings.TrimSpace(record.APIKey)
		record.Name = strings.TrimSpace(record.Name)
		record.AuthIdentity = strings.TrimSpace(record.AuthIdentity)
		if record.APIKey == "" {
			continue
		}
		if _, exists := seen[record.APIKey]; exists {
			continue
		}
		seen[record.APIKey] = struct{}{}
		out = append(out, record)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func storeMonitorAPIKeyRecords(records []apikeys.Record) []monitorAPIKeyRecord {
	if len(records) == 0 {
		return nil
	}
	out := make([]monitorAPIKeyRecord, 0, len(records))
	for _, record := range records {
		out = append(out, monitorAPIKeyRecord{
			APIKey:       record.APIKey,
			Name:         record.Name,
			AuthIdentity: record.AuthIdentity,
		})
	}
	return normalizeMonitorAPIKeyRecords(out)
}

func (h *Handler) monitorAPIKeyRecords(ctx context.Context) []monitorAPIKeyRecord {
	if h == nil {
		return nil
	}
	if h.apiKeyStore != nil {
		records, err := h.apiKeyStore.ListAPIKeyRecords(ctx)
		if err == nil {
			if normalized := storeMonitorAPIKeyRecords(records); len(normalized) > 0 {
				return normalized
			}
		}
	}

	configFilePath := strings.TrimSpace(h.configFilePath)
	if configFilePath != "" {
		if data, err := os.ReadFile(configFilePath); err == nil {
			if records := normalizeMonitorAPIKeyRecords(parseMonitorAPIKeyRecords(data)); len(records) > 0 {
				return records
			}
		}
	}

	if h.cfg == nil || len(h.cfg.APIKeys) == 0 {
		return nil
	}
	out := make([]monitorAPIKeyRecord, 0, len(h.cfg.APIKeys))
	for _, apiKey := range h.cfg.APIKeys {
		trimmed := strings.TrimSpace(apiKey)
		if trimmed == "" {
			continue
		}
		out = append(out, monitorAPIKeyRecord{
			APIKey:       trimmed,
			AuthIdentity: strings.TrimSpace(h.cfg.APIKeyAuthIdentityBindings[trimmed]),
		})
	}
	return normalizeMonitorAPIKeyRecords(out)
}

func monitorAPIKeyNameMapFromRecords(records []monitorAPIKeyRecord) map[string]string {
	if len(records) == 0 {
		return nil
	}
	names := make(map[string]string, len(records))
	for _, record := range records {
		if record.Name == "" {
			continue
		}
		names[record.APIKey] = record.Name
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

func (h *Handler) monitorAPIKeyConfigMap() map[string]string {
	records := h.monitorAPIKeyRecords(context.Background())
	if len(records) == 0 {
		return nil
	}
	keys := make(map[string]string, len(records))
	for _, record := range records {
		keys[record.APIKey] = record.Name
	}
	return keys
}

func (h *Handler) monitorAPIKeyNameMap() map[string]string {
	return monitorAPIKeyNameMapFromRecords(h.monitorAPIKeyRecords(context.Background()))
}

func monitorAuthDisplayName(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if note := strings.TrimSpace(authAttribute(auth, "note")); note != "" {
		return note
	}
	if auth.Metadata != nil {
		if rawNote, ok := auth.Metadata["note"].(string); ok {
			if trimmed := strings.TrimSpace(rawNote); trimmed != "" {
				return trimmed
			}
		}
	}
	if label := strings.TrimSpace(auth.Label); label != "" {
		return label
	}
	if fileName := strings.TrimSpace(auth.FileName); fileName != "" {
		base := filepath.Base(fileName)
		return strings.TrimSuffix(base, filepath.Ext(base))
	}
	return strings.TrimSpace(auth.ID)
}

func (h *Handler) boundAuthForMonitorKey(clientKey string) *coreauth.Auth {
	clientKey = strings.TrimSpace(clientKey)
	if clientKey == "" || h == nil || h.cfg == nil || h.authManager == nil {
		return nil
	}

	strategy, _ := coreauth.NormalizeRoutingStrategy(h.cfg.Routing.Strategy)
	if strategy != coreauth.RoutingStrategyAccountBind {
		return nil
	}

	auths := h.authManager.List()
	bindingMap, defaultAuthIndex := coreauth.ResolveBindingIndexes(
		auths,
		h.cfg.APIKeyAuthBindings,
		h.cfg.APIKeyAuthIdentityBindings,
		h.cfg.Routing.DefaultModelAccount,
	)

	authIndex := strings.TrimSpace(bindingMap[clientKey])
	if authIndex == "" {
		authIndex = strings.TrimSpace(defaultAuthIndex)
	}
	if authIndex == "" {
		return nil
	}

	for _, auth := range auths {
		if auth == nil {
			continue
		}
		auth.EnsureIndex()
		if auth.Index == authIndex {
			return auth
		}
	}
	return nil
}

func (h *Handler) publicMonitorScopedAPIKeys(ctx context.Context, clientKey string) []string {
	clientKey = strings.TrimSpace(clientKey)
	if clientKey == "" || h == nil || h.cfg == nil || h.authManager == nil {
		return nil
	}

	strategy, _ := coreauth.NormalizeRoutingStrategy(h.cfg.Routing.Strategy)
	if strategy != coreauth.RoutingStrategyAccountBind {
		return nil
	}

	records := h.monitorAPIKeyRecords(ctx)
	if len(records) == 0 {
		return nil
	}

	auths := h.authManager.List()
	bindingMap, defaultAuthIndex := coreauth.ResolveBindingIndexes(
		auths,
		h.cfg.APIKeyAuthBindings,
		h.cfg.APIKeyAuthIdentityBindings,
		h.cfg.Routing.DefaultModelAccount,
	)

	currentAuthIndex := strings.TrimSpace(bindingMap[clientKey])
	if currentAuthIndex == "" {
		currentAuthIndex = strings.TrimSpace(defaultAuthIndex)
	}
	if currentAuthIndex == "" {
		return nil
	}

	keys := make([]string, 0, len(records))
	for _, record := range records {
		recordAuthIndex := strings.TrimSpace(bindingMap[record.APIKey])
		if recordAuthIndex == "" {
			recordAuthIndex = strings.TrimSpace(defaultAuthIndex)
		}
		if recordAuthIndex == currentAuthIndex {
			keys = append(keys, record.APIKey)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)
	return keys
}

func (h *Handler) PublicMonitorAPIKeyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := strings.TrimSpace(firstQuery(c, "api_key", "api", "api-key"))
		if apiKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing api_key"})
			c.Abort()
			return
		}

		if _, ok := h.monitorAPIKeyConfigMap()[apiKey]; !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
			c.Abort()
			return
		}

		c.Set(publicMonitorAPIKeyContextKey, apiKey)
		c.Next()
	}
}

func lookupMonitorAPIKeysByName(query string, nameMap map[string]string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" || len(nameMap) == 0 {
		return nil
	}

	matches := make([]string, 0)
	for apiKey, name := range nameMap {
		if strings.Contains(strings.ToLower(strings.TrimSpace(name)), query) {
			matches = append(matches, apiKey)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.Strings(matches)
	return matches
}

func shouldScopePublicMonitorToBoundCredential(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	return strings.HasSuffix(strings.TrimSpace(c.Request.URL.Path), "/key-token-stats")
}

func (h *Handler) buildMonitorRecordFilter(c *gin.Context, start, end *time.Time, status string) monitorRecordFilter {
	apiKey := firstQuery(c, "api", "api_key")
	var scopedAPIKeys []string
	if c != nil {
		if value, exists := c.Get(publicMonitorAPIKeyContextKey); exists {
			if publicAPIKey, ok := value.(string); ok {
				apiKey = publicAPIKey
				if shouldScopePublicMonitorToBoundCredential(c) {
					scopedAPIKeys = h.publicMonitorScopedAPIKeys(c.Request.Context(), publicAPIKey)
				}
			}
		}
	}

	filter := monitorRecordFilter{
		APIKey:      apiKey,
		APIKeys:     scopedAPIKeys,
		APIContains: firstQuery(c, "api_filter", "apiFilter", "api_like", "apiLike", "q"),
		Model:       firstQuery(c, "model"),
		Source:      firstQuery(c, "source", "channel"),
		Status:      status,
		Start:       start,
		End:         end,
	}
	if len(filter.APIKeys) > 0 {
		filter.APIKey = ""
	}
	filter.APIMatchedKeys = lookupMonitorAPIKeysByName(filter.APIContains, h.monitorAPIKeyNameMap())
	return filter
}
