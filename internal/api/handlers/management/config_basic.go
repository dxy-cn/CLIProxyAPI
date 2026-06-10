package management

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

const (
	latestReleaseURL       = "https://api.github.com/repos/router-for-me/CLIProxyAPI/releases/latest"
	latestReleaseUserAgent = "CLIProxyAPI"
	configHashHeader       = "X-Config-Hash"
)

func (h *Handler) GetConfig(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(200, gin.H{})
		return
	}
	c.JSON(200, new(*h.cfg))
}

type releaseInfo struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
}

// GetLatestVersion returns the latest release version from GitHub without downloading assets.
func (h *Handler) GetLatestVersion(c *gin.Context) {
	client := &http.Client{Timeout: 10 * time.Second}
	proxyURL := ""
	if h != nil && h.cfg != nil {
		proxyURL = strings.TrimSpace(h.cfg.ProxyURL)
	}
	if proxyURL != "" {
		sdkCfg := &sdkconfig.SDKConfig{ProxyURL: proxyURL}
		util.SetProxy(sdkCfg, client)
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "request_create_failed", "message": err.Error()})
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", latestReleaseUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "request_failed", "message": err.Error()})
		return
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.WithError(errClose).Debug("failed to close latest version response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		c.JSON(http.StatusBadGateway, gin.H{"error": "unexpected_status", "message": fmt.Sprintf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))})
		return
	}

	var info releaseInfo
	if errDecode := json.NewDecoder(resp.Body).Decode(&info); errDecode != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "decode_failed", "message": errDecode.Error()})
		return
	}

	version := strings.TrimSpace(info.TagName)
	if version == "" {
		version = strings.TrimSpace(info.Name)
	}
	if version == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "invalid_response", "message": "missing release version"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"latest-version": version})
}

func WriteConfig(path string, data []byte) error {
	data = config.NormalizeCommentIndentation(data)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, errWrite := f.Write(data); errWrite != nil {
		_ = f.Close()
		return errWrite
	}
	if errSync := f.Sync(); errSync != nil {
		_ = f.Close()
		return errSync
	}
	return f.Close()
}

func configContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func setConfigVersionHeaders(c *gin.Context, hash string) {
	if hash == "" {
		return
	}
	c.Header(configHashHeader, hash)
	c.Header("ETag", fmt.Sprintf("%q", hash))
}

func configPreconditionMatches(headerValue, currentHash string) bool {
	for _, part := range strings.Split(headerValue, ",") {
		value := strings.TrimSpace(part)
		value = strings.TrimPrefix(value, "W/")
		value = strings.Trim(value, `"`)
		if value == currentHash {
			return true
		}
	}
	return false
}

func requestedConfigHash(c *gin.Context) string {
	if value := strings.TrimSpace(c.GetHeader("If-Match")); value != "" {
		return value
	}
	return strings.TrimSpace(c.GetHeader(configHashHeader))
}

func parseModelPriceNumber(record map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		value, ok := record[key]
		if !ok || value == nil {
			continue
		}

		switch typed := value.(type) {
		case int:
			if typed >= 0 {
				return float64(typed), true
			}
		case int8:
			if typed >= 0 {
				return float64(typed), true
			}
		case int16:
			if typed >= 0 {
				return float64(typed), true
			}
		case int32:
			if typed >= 0 {
				return float64(typed), true
			}
		case int64:
			if typed >= 0 {
				return float64(typed), true
			}
		case uint:
			return float64(typed), true
		case uint8:
			return float64(typed), true
		case uint16:
			return float64(typed), true
		case uint32:
			return float64(typed), true
		case uint64:
			return float64(typed), true
		case float32:
			if typed >= 0 {
				return float64(typed), true
			}
		case float64:
			if typed >= 0 {
				return typed, true
			}
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
			if err == nil && parsed >= 0 {
				return parsed, true
			}
		}
	}
	return 0, false
}

func extractModelPrices(data []byte) (map[string]gin.H, error) {
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}

	rawModelPrices, ok := root["model-prices"]
	if !ok || rawModelPrices == nil {
		return map[string]gin.H{}, nil
	}

	modelPricesMap, ok := rawModelPrices.(map[string]any)
	if !ok {
		return map[string]gin.H{}, nil
	}

	result := make(map[string]gin.H, len(modelPricesMap))
	for rawModel, rawValue := range modelPricesMap {
		model := strings.TrimSpace(rawModel)
		if model == "" {
			continue
		}

		valueRecord, ok := rawValue.(map[string]any)
		if !ok {
			continue
		}

		mode := "token"
		if rawMode, ok := valueRecord["mode"].(string); ok && strings.EqualFold(strings.TrimSpace(rawMode), "call") {
			mode = "call"
		}

		prompt, hasPrompt := parseModelPriceNumber(valueRecord, "prompt", "input")
		completion, hasCompletion := parseModelPriceNumber(valueRecord, "completion", "output")
		cache, hasCache := parseModelPriceNumber(valueRecord, "cache")
		perCall, hasPerCall := parseModelPriceNumber(valueRecord, "perCall", "per-call", "per_call")

		if !hasCache {
			cache = prompt
		}
		if !hasPrompt && !hasCompletion && !hasCache && !hasPerCall {
			continue
		}

		entry := gin.H{
			"mode":       mode,
			"prompt":     prompt,
			"completion": completion,
			"cache":      cache,
		}
		if hasPerCall {
			entry["perCall"] = perCall
		}
		result[model] = entry
	}

	return result, nil
}

func (h *Handler) readModelPrices() (map[string]gin.H, error) {
	data, err := os.ReadFile(h.configFilePath)
	if err != nil {
		return nil, err
	}
	return extractModelPrices(data)
}

func (h *Handler) PutConfigYAML(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_yaml", "message": "cannot read request body"})
		return
	}
	var cfg config.Config
	if err = yaml.Unmarshal(body, &cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_yaml", "message": err.Error()})
		return
	}
	// Validate config using LoadConfigOptional with optional=false to enforce parsing
	tmpDir := filepath.Dir(h.configFilePath)
	tmpFile, err := os.CreateTemp(tmpDir, "config-validate-*.yaml")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "write_failed", "message": err.Error()})
		return
	}
	tempFile := tmpFile.Name()
	if _, errWrite := tmpFile.Write(body); errWrite != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tempFile)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "write_failed", "message": errWrite.Error()})
		return
	}
	if errClose := tmpFile.Close(); errClose != nil {
		_ = os.Remove(tempFile)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "write_failed", "message": errClose.Error()})
		return
	}
	defer func() {
		_ = os.Remove(tempFile)
	}()
	_, err = config.LoadConfigOptional(tempFile, false)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_config", "message": err.Error()})
		return
	}
	h.mu.Lock()
	currentData, err := os.ReadFile(h.configFilePath)
	if err != nil {
		h.mu.Unlock()
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "config file not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read_failed", "message": err.Error()})
		return
	}
	currentHash := configContentHash(currentData)
	setConfigVersionHeaders(c, currentHash)

	expectedHash := requestedConfigHash(c)
	if expectedHash == "" {
		h.mu.Unlock()
		c.JSON(http.StatusPreconditionRequired, gin.H{
			"error":   "config_version_required",
			"message": "config version is required; reload config before saving",
		})
		return
	}
	if !configPreconditionMatches(expectedHash, currentHash) {
		h.mu.Unlock()
		c.JSON(http.StatusConflict, gin.H{
			"error":        "config_conflict",
			"message":      "config has been modified; reload before saving",
			"current_hash": currentHash,
		})
		return
	}

	if errWrite := WriteConfig(h.configFilePath, body); errWrite != nil {
		h.mu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "write_failed", "message": errWrite.Error()})
		return
	}
	// Reload into handler to keep memory in sync
	newCfg, err := config.LoadConfig(h.configFilePath)
	if err != nil {
		h.mu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "reload_failed", "message": err.Error()})
		return
	}
	oldCfg := h.cfg
	h.cfg = newCfg
	dispatchQuotaWarnings := h.shouldDispatchQuotaWarningsAfterConfigChange(oldCfg, newCfg)
	h.mu.Unlock()
	if dispatchQuotaWarnings {
		go h.dispatchQuotaWarningsForCurrentCodexAuths(context.Background())
	}
	if updatedData, errRead := os.ReadFile(h.configFilePath); errRead == nil {
		setConfigVersionHeaders(c, configContentHash(updatedData))
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "changed": []string{"config"}})
}

// GetConfigYAML returns the raw config.yaml file bytes without re-encoding.
// It preserves comments and original formatting/styles.
func (h *Handler) GetConfigYAML(c *gin.Context) {
	data, err := os.ReadFile(h.configFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "config file not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read_failed", "message": err.Error()})
		return
	}
	setConfigVersionHeaders(c, configContentHash(data))
	c.Header("Content-Type", "application/yaml; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	// Write raw bytes as-is
	_, _ = c.Writer.Write(data)
}

func (h *Handler) GetModelPrices(c *gin.Context) {
	modelPrices, err := h.readModelPrices()
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "config file not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read_failed", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, modelPrices)
}

// Debug
func (h *Handler) GetDebug(c *gin.Context) { c.JSON(200, gin.H{"debug": h.cfg.Debug}) }
func (h *Handler) PutDebug(c *gin.Context) { h.updateBoolField(c, func(v bool) { h.cfg.Debug = v }) }

// UsageStatisticsEnabled
func (h *Handler) GetUsageStatisticsEnabled(c *gin.Context) {
	c.JSON(200, gin.H{"usage-statistics-enabled": h.cfg.UsageStatisticsEnabled})
}
func (h *Handler) PutUsageStatisticsEnabled(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.UsageStatisticsEnabled = v })
}

// UsageStatisticsEnabled
func (h *Handler) GetLoggingToFile(c *gin.Context) {
	c.JSON(200, gin.H{"logging-to-file": h.cfg.LoggingToFile})
}
func (h *Handler) PutLoggingToFile(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.LoggingToFile = v })
}

// LogsMaxTotalSizeMB
func (h *Handler) GetLogsMaxTotalSizeMB(c *gin.Context) {
	c.JSON(200, gin.H{"logs-max-total-size-mb": h.cfg.LogsMaxTotalSizeMB})
}
func (h *Handler) PutLogsMaxTotalSizeMB(c *gin.Context) {
	var body struct {
		Value *int `json:"value"`
	}
	if errBindJSON := c.ShouldBindJSON(&body); errBindJSON != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	value := *body.Value
	if value < 0 {
		value = 0
	}
	h.cfg.LogsMaxTotalSizeMB = value
	h.persist(c)
}

// ErrorLogsMaxFiles
func (h *Handler) GetErrorLogsMaxFiles(c *gin.Context) {
	c.JSON(200, gin.H{"error-logs-max-files": h.cfg.ErrorLogsMaxFiles})
}
func (h *Handler) PutErrorLogsMaxFiles(c *gin.Context) {
	var body struct {
		Value *int `json:"value"`
	}
	if errBindJSON := c.ShouldBindJSON(&body); errBindJSON != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	value := *body.Value
	if value < 0 {
		value = 10
	}
	h.cfg.ErrorLogsMaxFiles = value
	h.persist(c)
}

// Request log
func (h *Handler) GetRequestLog(c *gin.Context) { c.JSON(200, gin.H{"request-log": h.cfg.RequestLog}) }
func (h *Handler) PutRequestLog(c *gin.Context) {
	var body struct {
		Value *bool `json:"value"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	h.cfg.RequestLog = *body.Value
	if !h.persist(c) {
		return
	}
	if h.configUpdateHook != nil {
		h.configUpdateHook(h.cfg)
	}
}

// Websocket auth
func (h *Handler) GetWebsocketAuth(c *gin.Context) {
	c.JSON(200, gin.H{"ws-auth": h.cfg.WebsocketAuth})
}
func (h *Handler) PutWebsocketAuth(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.WebsocketAuth = v })
}

// Request retry
func (h *Handler) GetRequestRetry(c *gin.Context) {
	c.JSON(200, gin.H{"request-retry": h.cfg.RequestRetry})
}
func (h *Handler) PutRequestRetry(c *gin.Context) {
	h.updateIntField(c, func(v int) { h.cfg.RequestRetry = v })
}

// Max retry interval
func (h *Handler) GetMaxRetryInterval(c *gin.Context) {
	c.JSON(200, gin.H{"max-retry-interval": h.cfg.MaxRetryInterval})
}
func (h *Handler) PutMaxRetryInterval(c *gin.Context) {
	h.updateIntField(c, func(v int) { h.cfg.MaxRetryInterval = v })
}

// ForceModelPrefix
func (h *Handler) GetForceModelPrefix(c *gin.Context) {
	c.JSON(200, gin.H{"force-model-prefix": h.cfg.ForceModelPrefix})
}
func (h *Handler) PutForceModelPrefix(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.ForceModelPrefix = v })
}

func normalizeRoutingStrategy(strategy string) (string, bool) {
	return coreauth.NormalizeRoutingStrategy(strategy)
}

// RoutingStrategy
func (h *Handler) GetRoutingStrategy(c *gin.Context) {
	strategy, ok := normalizeRoutingStrategy(h.cfg.Routing.Strategy)
	if !ok {
		c.JSON(200, gin.H{"strategy": strings.TrimSpace(h.cfg.Routing.Strategy)})
		return
	}
	c.JSON(200, gin.H{"strategy": strategy})
}
func (h *Handler) PutRoutingStrategy(c *gin.Context) {
	var body struct {
		Value *string `json:"value"`
	}
	if errBindJSON := c.ShouldBindJSON(&body); errBindJSON != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	normalized, ok := normalizeRoutingStrategy(*body.Value)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid strategy"})
		return
	}
	h.cfg.Routing.Strategy = normalized
	h.persist(c)
}

// Proxy URL
func (h *Handler) GetProxyURL(c *gin.Context) { c.JSON(200, gin.H{"proxy-url": h.cfg.ProxyURL}) }
func (h *Handler) PutProxyURL(c *gin.Context) {
	h.updateStringField(c, func(v string) { h.cfg.ProxyURL = v })
}
func (h *Handler) DeleteProxyURL(c *gin.Context) {
	h.cfg.ProxyURL = ""
	h.persist(c)
}
