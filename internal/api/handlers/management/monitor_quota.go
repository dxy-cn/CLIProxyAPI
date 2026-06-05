package management

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codex"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// GetPublicMonitorCodexQuota returns the Codex quota for the credential bound to
// the already-validated public monitor API key.
func (h *Handler) GetPublicMonitorCodexQuota(c *gin.Context) {
	apiKey := publicMonitorAPIKey(c)
	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing api_key"})
		return
	}

	auth := h.boundCodexAuthForMonitorKey(apiKey)
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no codex credential bound to api key"})
		return
	}

	statusCode, payload, err := h.fetchCodexQuotaPayload(c.Request.Context(), auth)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		c.JSON(http.StatusBadGateway, gin.H{
			"error":       "bound codex credential quota request failed",
			"status_code": statusCode,
		})
		return
	}
	if len(payload) == 0 {
		c.JSON(http.StatusBadGateway, gin.H{"error": "empty codex quota payload"})
		return
	}

	response := publicCodexQuotaResponse(payload, auth)
	if len(response) == 0 {
		c.JSON(http.StatusBadGateway, gin.H{"error": "codex quota payload missing public fields"})
		return
	}

	c.JSON(http.StatusOK, response)
}

func publicMonitorAPIKey(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if value, exists := c.Get(publicMonitorAPIKeyContextKey); exists {
		if apiKey, ok := value.(string); ok {
			if trimmed := strings.TrimSpace(apiKey); trimmed != "" {
				return trimmed
			}
		}
	}
	return strings.TrimSpace(firstQuery(c, "api_key", "api", "api-key"))
}

func (h *Handler) boundCodexAuthForMonitorKey(clientKey string) *coreauth.Auth {
	auth := h.boundAuthForMonitorKey(clientKey)
	if auth == nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
		return nil
	}
	return auth
}

func (h *Handler) fetchCodexQuotaPayload(ctx context.Context, auth *coreauth.Auth) (int, gin.H, error) {
	token, tokenErr := h.resolveTokenForAuth(ctx, auth)
	if tokenErr != nil {
		return 0, nil, fmt.Errorf("resolve codex token failed: %w", tokenErr)
	}
	if strings.TrimSpace(token) == "" {
		return 0, nil, fmt.Errorf("codex token not found")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexVerifyURL, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("build codex quota request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", codexVerifyUserAgent)
	if accountID := codexAccountIDForQuota(auth); accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}

	client := &http.Client{
		Transport: h.apiCallTransport(auth),
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request codex quota failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := readAPICallResponseBody(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read codex quota response failed: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp.StatusCode, nil, nil
	}

	var payload gin.H
	if err := json.Unmarshal(body, &payload); err != nil {
		return resp.StatusCode, nil, fmt.Errorf("decode codex quota response failed: %w", err)
	}
	return resp.StatusCode, payload, nil
}

func codexAccountIDForQuota(auth *coreauth.Auth) string {
	if accountID := extractCodexAccountID(auth); accountID != "" {
		return accountID
	}
	if auth != nil && auth.Metadata != nil {
		if value, ok := auth.Metadata["account_id"].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func codexPlanTypeForQuota(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if auth.Attributes != nil {
		if planType := strings.TrimSpace(auth.Attributes["plan_type"]); planType != "" {
			return planType
		}
	}
	if auth.Metadata == nil {
		return ""
	}
	idTokenRaw, ok := auth.Metadata["id_token"].(string)
	if !ok {
		return ""
	}
	claims, err := codex.ParseJWTToken(strings.TrimSpace(idTokenRaw))
	if err != nil || claims == nil {
		return ""
	}
	return strings.TrimSpace(claims.CodexAuthInfo.ChatgptPlanType)
}

func publicCodexQuotaResponse(payload gin.H, auth *coreauth.Auth) gin.H {
	response := gin.H{}
	if planType, ok := firstString(payload, "plan_type", "planType"); ok {
		response["plan_type"] = planType
	} else if planType := codexPlanTypeForQuota(auth); planType != "" {
		response["plan_type"] = planType
	}

	if rateLimit := publicCodexQuotaRateLimit(firstValue(payload, "rate_limit", "rateLimit")); len(rateLimit) > 0 {
		response["rate_limit"] = rateLimit
	}
	return response
}

func publicCodexQuotaRateLimit(raw any) gin.H {
	source := mapValue(raw)
	if source == nil {
		return nil
	}

	rateLimit := gin.H{}
	if value, ok := firstPresent(source, "allowed"); ok {
		rateLimit["allowed"] = value
	}
	if value, ok := firstPresent(source, "limit_reached", "limitReached"); ok {
		rateLimit["limit_reached"] = value
	}
	if window := publicCodexQuotaWindow(firstValue(source, "primary_window", "primaryWindow")); len(window) > 0 {
		rateLimit["primary_window"] = window
	}
	if window := publicCodexQuotaWindow(firstValue(source, "secondary_window", "secondaryWindow")); len(window) > 0 {
		rateLimit["secondary_window"] = window
	}
	return rateLimit
}

func publicCodexQuotaWindow(raw any) gin.H {
	source := mapValue(raw)
	if source == nil {
		return nil
	}

	window := gin.H{}
	copyFirstPresent(window, source, "used_percent", "used_percent", "usedPercent")
	copyFirstPresent(window, source, "limit_window_seconds", "limit_window_seconds", "limitWindowSeconds")
	copyFirstPresent(window, source, "reset_after_seconds", "reset_after_seconds", "resetAfterSeconds")
	copyFirstPresent(window, source, "reset_at", "reset_at", "resetAt")
	return window
}

func firstString(values map[string]any, keys ...string) (string, bool) {
	value, ok := firstPresent(values, keys...)
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

func firstValue(values map[string]any, keys ...string) any {
	value, _ := firstPresent(values, keys...)
	return value
}

func firstPresent(values map[string]any, keys ...string) (any, bool) {
	if values == nil {
		return nil, false
	}
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func copyFirstPresent(dst gin.H, source map[string]any, outputKey string, keys ...string) {
	if value, ok := firstPresent(source, keys...); ok {
		dst[outputKey] = value
	}
}

func mapValue(value any) map[string]any {
	switch typed := value.(type) {
	case gin.H:
		return map[string]any(typed)
	case map[string]any:
		return typed
	default:
		return nil
	}
}
