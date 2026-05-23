package management

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/apikeys"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"gopkg.in/yaml.v3"
)

type monitorYAMLRecord map[string]any

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

func collectMonitorAPIKeyNames(entries []any) map[string]string {
	if len(entries) == 0 {
		return nil
	}

	names := make(map[string]string)
	for _, entry := range entries {
		record := asMonitorYAMLRecord(entry)
		if record == nil {
			continue
		}
		apiKey := monitorYAMLString(record, "api-key", "apiKey", "key", "Key")
		name := monitorYAMLString(record, "name")
		if apiKey == "" || name == "" {
			continue
		}
		names[apiKey] = name
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

func collectMonitorAPIKeyConfig(entries []any) map[string]string {
	if len(entries) == 0 {
		return nil
	}

	keys := make(map[string]string)
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
		keys[apiKey] = monitorYAMLString(record, "name")
	}
	if len(keys) == 0 {
		return nil
	}
	return keys
}

func parseMonitorAPIKeyConfigMap(data []byte) map[string]string {
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
		if keys := collectMonitorAPIKeyConfig(providerEntries); len(keys) > 0 {
			return keys
		}
	}

	return collectMonitorAPIKeyConfig(topLevelEntries)
}

func parseMonitorAPIKeyNameMap(data []byte) map[string]string {
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
		if names := collectMonitorAPIKeyNames(providerEntries); len(names) > 0 {
			return names
		}
	}

	return collectMonitorAPIKeyNames(topLevelEntries)
}

func monitorAPIKeyRecordConfigMap(records []apikeys.Record, namesOnly bool) map[string]string {
	if len(records) == 0 {
		return nil
	}
	keys := make(map[string]string, len(records))
	for _, record := range records {
		apiKey := strings.TrimSpace(record.APIKey)
		if apiKey == "" {
			continue
		}
		name := strings.TrimSpace(record.Name)
		if namesOnly && name == "" {
			continue
		}
		keys[apiKey] = name
	}
	if len(keys) == 0 {
		return nil
	}
	return keys
}

func (h *Handler) monitorAPIKeyStoreConfigMap(ctx context.Context, namesOnly bool) map[string]string {
	if h == nil || h.apiKeyStore == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	records, err := h.apiKeyStore.ListAPIKeyRecords(ctx)
	if err != nil {
		return nil
	}
	return monitorAPIKeyRecordConfigMap(records, namesOnly)
}

func (h *Handler) monitorAPIKeyConfigMap(ctx context.Context) map[string]string {
	if h == nil {
		return nil
	}
	if keys := h.monitorAPIKeyStoreConfigMap(ctx, false); len(keys) > 0 {
		return keys
	}

	configFilePath := strings.TrimSpace(h.configFilePath)
	if configFilePath != "" {
		if data, err := os.ReadFile(configFilePath); err == nil {
			if keys := parseMonitorAPIKeyConfigMap(data); len(keys) > 0 {
				return keys
			}
		}
	}

	if h.cfg == nil || len(h.cfg.APIKeys) == 0 {
		return nil
	}
	keys := make(map[string]string, len(h.cfg.APIKeys))
	for _, apiKey := range h.cfg.APIKeys {
		if trimmed := strings.TrimSpace(apiKey); trimmed != "" {
			keys[trimmed] = ""
		}
	}
	return keys
}

func (h *Handler) monitorAPIKeyNameMap(ctx context.Context) map[string]string {
	if h == nil {
		return nil
	}
	if names := h.monitorAPIKeyStoreConfigMap(ctx, true); len(names) > 0 {
		return names
	}
	configFilePath := strings.TrimSpace(h.configFilePath)
	if configFilePath == "" {
		return nil
	}
	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return nil
	}
	return parseMonitorAPIKeyNameMap(data)
}

func (h *Handler) monitorAuthNoteMap() map[string]string {
	if h == nil || h.authManager == nil {
		return nil
	}
	auths := h.authManager.List()
	if len(auths) == 0 {
		return nil
	}
	notes := make(map[string]string, len(auths))
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		auth.EnsureIndex()
		index := strings.TrimSpace(auth.Index)
		if index == "" {
			continue
		}
		if label := strings.TrimSpace(quotaWarningAuthLabel(auth)); label != "" && label != "-" {
			notes[index] = label
		}
	}
	if len(notes) == 0 {
		return nil
	}
	return notes
}

func (h *Handler) PublicMonitorAPIKeyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := strings.TrimSpace(firstQuery(c, "api_key", "api", "api-key"))
		if apiKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing api_key"})
			c.Abort()
			return
		}

		if _, ok := h.monitorAPIKeyConfigMap(c.Request.Context())[apiKey]; !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
			c.Abort()
			return
		}

		c.Set(publicMonitorAPIKeyContextKey, apiKey)
		c.Next()
	}
}

func publicMonitorCurrentAPIKey(c *gin.Context) string {
	if c == nil {
		return ""
	}
	value, exists := c.Get(publicMonitorAPIKeyContextKey)
	if !exists {
		return ""
	}
	apiKey, _ := value.(string)
	return strings.TrimSpace(apiKey)
}

func monitorAPIKeyDisplayName(apiKey string, nameMap map[string]string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	if name := strings.TrimSpace(nameMap[apiKey]); name != "" {
		return name
	}
	return apiKey
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

	authIndex := ""
	if bindingMap != nil {
		authIndex = strings.TrimSpace(bindingMap[clientKey])
	}
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

func (h *Handler) monitorAPIKeysForBoundAuthIndex(authIndex, currentAPIKey string) []string {
	authIndex = strings.TrimSpace(authIndex)
	currentAPIKey = strings.TrimSpace(currentAPIKey)
	if authIndex == "" || h == nil || h.cfg == nil || h.authManager == nil {
		if currentAPIKey == "" {
			return nil
		}
		return []string{currentAPIKey}
	}

	auths := h.authManager.List()
	bindingMap, defaultAuthIndex := coreauth.ResolveBindingIndexes(
		auths,
		h.cfg.APIKeyAuthBindings,
		h.cfg.APIKeyAuthIdentityBindings,
		h.cfg.Routing.DefaultModelAccount,
	)

	candidates := make(map[string]struct{})
	if currentAPIKey != "" {
		candidates[currentAPIKey] = struct{}{}
	}
	for apiKey := range h.monitorAPIKeyConfigMap(context.Background()) {
		if trimmed := strings.TrimSpace(apiKey); trimmed != "" {
			candidates[trimmed] = struct{}{}
		}
	}
	for _, apiKey := range h.cfg.APIKeys {
		if trimmed := strings.TrimSpace(apiKey); trimmed != "" {
			candidates[trimmed] = struct{}{}
		}
	}
	for apiKey := range bindingMap {
		if trimmed := strings.TrimSpace(apiKey); trimmed != "" {
			candidates[trimmed] = struct{}{}
		}
	}

	keys := make([]string, 0, len(candidates))
	for apiKey := range candidates {
		resolvedIndex := strings.TrimSpace(bindingMap[apiKey])
		if resolvedIndex == "" {
			resolvedIndex = strings.TrimSpace(defaultAuthIndex)
		}
		if resolvedIndex == authIndex {
			keys = append(keys, apiKey)
		}
	}
	if len(keys) == 0 && currentAPIKey != "" {
		keys = append(keys, currentAPIKey)
	}
	sort.Strings(keys)
	return keys
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

func (h *Handler) buildMonitorRecordFilter(c *gin.Context, start, end *time.Time, status string) monitorRecordFilter {
	apiKey := firstQuery(c, "api", "api_key")
	ctx := context.Background()
	if c != nil {
		ctx = c.Request.Context()
		if value, exists := c.Get(publicMonitorAPIKeyContextKey); exists {
			if publicAPIKey, ok := value.(string); ok {
				apiKey = publicAPIKey
			}
		}
	}

	filter := monitorRecordFilter{
		APIKey:      apiKey,
		APIContains: firstQuery(c, "api_filter", "apiFilter", "api_like", "apiLike", "q"),
		Model:       firstQuery(c, "model"),
		Source:      firstQuery(c, "source", "channel"),
		Status:      status,
		Start:       start,
		End:         end,
	}
	filter.APIMatchedKeys = lookupMonitorAPIKeysByName(filter.APIContains, h.monitorAPIKeyNameMap(ctx))
	return filter
}
