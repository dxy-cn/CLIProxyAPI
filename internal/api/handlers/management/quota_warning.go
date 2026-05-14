package management

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	quotaWarningHTTPTimeout      = 5 * time.Second
	quotaWarningResetTimeLayout  = "2006-01-02 15:04"
	quotaWarningUnixMilliseconds = 1_000_000_000_000
	quotaWarningFiveHourSeconds  = 18000
)

type quotaWarningSender func(ctx context.Context, webhookURL string, content string) error

type quotaWarningQuotaFetcher func(ctx context.Context, auth *coreauth.Auth) (int, gin.H, error)

type quotaWarningWindow struct {
	ID          string
	Label       string
	Period      string
	Seconds     float64
	Remaining   float64
	Reset       string
	DedupeReset string
}

func (h *Handler) maybeSendCodexQuotaWarning(ctx context.Context, auth *coreauth.Auth, payload gin.H) {
	if h == nil || h.cfg == nil || auth == nil {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
		return
	}

	cfg := h.cfg.QuotaWarning
	webhookURL := strings.TrimSpace(cfg.WebhookURL)
	threshold := cfg.Threshold
	if webhookURL == "" || threshold <= 0 || threshold > 100 {
		return
	}
	if !isWeComRobotWebhook(webhookURL) {
		log.Warn("quota warning: ignored unsupported webhook url")
		return
	}

	window, ok := lowestCodexQuotaWindow(payload)
	if !ok || window.Remaining >= float64(threshold) {
		return
	}

	dedupeKey := h.quotaWarningDedupeKey(auth, window, threshold)
	h.quotaWarningMu.Lock()
	if h.quotaWarningSent == nil {
		h.quotaWarningSent = make(map[string]struct{})
	}
	if _, exists := h.quotaWarningSent[dedupeKey]; exists {
		h.quotaWarningMu.Unlock()
		return
	}
	h.quotaWarningSent[dedupeKey] = struct{}{}
	h.quotaWarningMu.Unlock()

	sender := h.quotaWarningSender
	if sender == nil {
		sender = sendWeComQuotaWarning
	}
	content := buildQuotaWarningContent(auth, window, threshold)
	if err := sender(ctx, webhookURL, content); err != nil {
		h.quotaWarningMu.Lock()
		delete(h.quotaWarningSent, dedupeKey)
		h.quotaWarningMu.Unlock()
		log.WithError(err).Warn("quota warning: send failed")
		return
	}
}

func (h *Handler) shouldDispatchQuotaWarningsAfterConfigChange(oldCfg *config.Config, newCfg *config.Config) bool {
	if h == nil || !quotaWarningConfigEnabled(newCfg) {
		return false
	}

	oldEnabled := quotaWarningConfigEnabled(oldCfg)
	oldThreshold := 0
	if oldCfg != nil {
		oldThreshold = oldCfg.QuotaWarning.Threshold
	}
	newThreshold := newCfg.QuotaWarning.Threshold
	if oldEnabled && oldThreshold == newThreshold {
		return false
	}

	h.quotaWarningMu.Lock()
	h.quotaWarningVersion++
	h.quotaWarningMu.Unlock()
	return true
}

func (h *Handler) dispatchQuotaWarningsForCurrentCodexAuths(ctx context.Context) {
	if h == nil {
		return
	}

	h.mu.Lock()
	cfg := h.cfg
	manager := h.authManager
	h.mu.Unlock()
	if !quotaWarningConfigEnabled(cfg) || manager == nil {
		return
	}

	fetcher := h.quotaWarningQuotaFetcher
	if fetcher == nil {
		fetcher = h.fetchCodexQuotaPayload
	}

	for _, auth := range manager.List() {
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
			continue
		}
		statusCode, payload, err := fetcher(ctx, auth)
		if err != nil {
			log.WithError(err).Warn("quota warning: fetch codex quota failed")
			continue
		}
		if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices || len(payload) == 0 {
			continue
		}
		h.maybeSendCodexQuotaWarning(ctx, auth, payload)
	}
}

func quotaWarningConfigEnabled(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	warning := cfg.QuotaWarning
	webhookURL := strings.TrimSpace(warning.WebhookURL)
	return webhookURL != "" &&
		warning.Threshold > 0 &&
		warning.Threshold <= 100 &&
		isWeComRobotWebhook(webhookURL)
}

func lowestCodexQuotaWindow(payload gin.H) (quotaWarningWindow, bool) {
	windows := collectCodexQuotaWindows(payload)
	if len(windows) == 0 {
		return quotaWarningWindow{}, false
	}

	var lowest quotaWarningWindow
	for _, window := range windows {
		if window.Seconds != quotaWarningFiveHourSeconds {
			continue
		}
		if lowest.ID == "" {
			lowest = window
			continue
		}
		if window.Remaining < lowest.Remaining {
			lowest = window
		}
	}
	if lowest.ID == "" {
		return quotaWarningWindow{}, false
	}
	return lowest, true
}

func collectCodexQuotaWindows(payload gin.H) []quotaWarningWindow {
	var windows []quotaWarningWindow

	addRateLimitWindows := func(prefix string, limitRaw any) {
		limit := mapValue(limitRaw)
		if limit == nil {
			return
		}
		limitReached := boolValue(firstValue(limit, "limit_reached", "limitReached"))
		allowedFalse := allowedIsFalse(firstValue(limit, "allowed"))
		addWindow := func(id, label string, windowRaw any) {
			window, ok := codexQuotaWarningWindow(id, label, windowRaw, limitReached, allowedFalse)
			if ok {
				windows = append(windows, window)
			}
		}
		addWindow(prefix+"-primary", prefix+" 5小时窗口", firstValue(limit, "primary_window", "primaryWindow"))
		addWindow(prefix+"-secondary", prefix+" 周窗口", firstValue(limit, "secondary_window", "secondaryWindow"))
	}

	addRateLimitWindows("代码", firstValue(payload, "rate_limit", "rateLimit"))
	addRateLimitWindows("代码审查", firstValue(payload, "code_review_rate_limit", "codeReviewRateLimit"))

	for index, itemRaw := range sliceValue(firstValue(payload, "additional_rate_limits", "additionalRateLimits")) {
		item := mapValue(itemRaw)
		if item == nil {
			continue
		}
		name := firstTrimmedString(item, "limit_name", "limitName", "metered_feature", "meteredFeature")
		if name == "" {
			name = fmt.Sprintf("附加额度%d", index+1)
		}
		addRateLimitWindows(name, firstValue(item, "rate_limit", "rateLimit"))
	}

	return windows
}

func codexQuotaWarningWindow(id string, label string, raw any, limitReached bool, allowedFalse bool) (quotaWarningWindow, bool) {
	window := mapValue(raw)
	if window == nil {
		return quotaWarningWindow{}, false
	}

	usedPercent, hasUsedPercent := numericValue(firstValue(window, "used_percent", "usedPercent"))
	if !hasUsedPercent && (limitReached || allowedFalse) {
		usedPercent = 100
		hasUsedPercent = true
	}
	if !hasUsedPercent {
		return quotaWarningWindow{}, false
	}

	remaining := 100 - usedPercent
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 100 {
		remaining = 100
	}

	reset := quotaWarningResetLabel(window)
	seconds, _ := numericValue(firstValue(window, "limit_window_seconds", "limitWindowSeconds"))
	return quotaWarningWindow{
		ID:          id,
		Label:       label,
		Period:      quotaWarningWindowPeriod(window),
		Seconds:     seconds,
		Remaining:   remaining,
		Reset:       reset,
		DedupeReset: quotaWarningResetKey(window, reset),
	}, true
}

func buildQuotaWarningContent(auth *coreauth.Auth, window quotaWarningWindow, threshold int) string {
	return strings.Join([]string{
		"### Token Pulse 额度预警",
		fmt.Sprintf("> 凭证: %s", quotaWarningAuthLabel(auth)),
		fmt.Sprintf("> %s限额: %s", window.Period, formatQuotaWarningPercent(window.Remaining)),
		fmt.Sprintf("> 重置时间: %s", emptyAsDash(window.Reset)),
	}, "\n")
}

func quotaWarningAuthLabel(auth *coreauth.Auth) string {
	if auth == nil {
		return "-"
	}
	if note := strings.TrimSpace(authAttribute(auth, "note")); note != "" {
		return note
	}
	if auth.Metadata != nil {
		if note, ok := auth.Metadata["note"].(string); ok {
			if note = strings.TrimSpace(note); note != "" {
				return note
			}
		}
	}
	if label := strings.TrimSpace(auth.Label); label != "" {
		return label
	}
	if _, account := auth.AccountInfo(); strings.TrimSpace(account) != "" {
		return strings.TrimSpace(account)
	}
	if index := strings.TrimSpace(auth.Index); index != "" {
		return index
	}
	if fileName := strings.TrimSpace(auth.FileName); fileName != "" {
		return fileName
	}
	if identity := strings.TrimSpace(auth.StableIdentity()); identity != "" {
		return identity
	}
	if id := strings.TrimSpace(auth.ID); id != "" {
		return id
	}
	return "-"
}

func (h *Handler) quotaWarningDedupeKey(auth *coreauth.Auth, window quotaWarningWindow, threshold int) string {
	identity := ""
	if auth != nil {
		identity = strings.TrimSpace(auth.StableIdentity())
		if identity == "" {
			identity = strings.TrimSpace(auth.Index)
		}
		if identity == "" {
			identity = strings.TrimSpace(auth.ID)
		}
		if identity == "" {
			identity = strings.TrimSpace(auth.FileName)
		}
	}
	version := int64(0)
	if h != nil {
		h.quotaWarningMu.Lock()
		version = h.quotaWarningVersion
		h.quotaWarningMu.Unlock()
	}
	return strings.Join([]string{identity, window.ID, window.DedupeReset, strconv.Itoa(threshold), strconv.FormatInt(version, 10)}, "|")
}

func sendWeComQuotaWarning(ctx context.Context, webhookURL string, content string) error {
	body, err := json.Marshal(gin.H{
		"msgtype": "markdown",
		"markdown": gin.H{
			"content": content,
		},
	})
	if err != nil {
		return fmt.Errorf("encode wecom message: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, quotaWarningHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build wecom request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post wecom webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("wecom webhook status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if len(respBody) > 0 && json.Unmarshal(respBody, &result) == nil && result.ErrCode != 0 {
		return fmt.Errorf("wecom webhook errcode %d: %s", result.ErrCode, strings.TrimSpace(result.ErrMsg))
	}
	return nil
}

func isWeComRobotWebhook(webhookURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(webhookURL))
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" &&
		strings.EqualFold(parsed.Host, "qyapi.weixin.qq.com") &&
		parsed.Path == "/cgi-bin/webhook/send" &&
		strings.TrimSpace(parsed.Query().Get("key")) != ""
}

func quotaWarningResetLabel(window map[string]any) string {
	if reset, ok := quotaWarningResetAtLabel(firstValue(window, "reset_at", "resetAt")); ok {
		return reset
	}
	if resetAfter, ok := numericValue(firstValue(window, "reset_after_seconds", "resetAfterSeconds")); ok {
		if resetAfter <= 0 {
			return time.Now().Local().Format(quotaWarningResetTimeLayout)
		}
		return time.Now().Add(time.Duration(resetAfter * float64(time.Second))).Local().Format(quotaWarningResetTimeLayout)
	}
	return "-"
}

func quotaWarningResetAtLabel(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	seconds, ok := numericValue(value)
	if !ok {
		reset := strings.TrimSpace(fmt.Sprint(value))
		return reset, reset != ""
	}
	if seconds >= quotaWarningUnixMilliseconds {
		seconds = seconds / 1000
	}
	return time.Unix(int64(seconds), 0).Local().Format(quotaWarningResetTimeLayout), true
}

func quotaWarningWindowPeriod(window map[string]any) string {
	seconds, ok := numericValue(firstValue(window, "limit_window_seconds", "limitWindowSeconds"))
	if !ok || seconds <= 0 {
		return ""
	}
	if seconds == 18000 {
		return "5小时"
	}
	if seconds == 604800 {
		return "周"
	}
	if seconds < 3600 {
		return fmt.Sprintf("%.0f分钟", seconds/60)
	}
	if seconds < 86400 {
		return fmt.Sprintf("%.0f小时", seconds/3600)
	}
	return fmt.Sprintf("%.0f天", seconds/86400)
}

func quotaWarningResetKey(window map[string]any, fallback string) string {
	if reset := firstStringish(window, "reset_at", "resetAt"); reset != "" {
		return reset
	}
	if resetAfter, ok := numericValue(firstValue(window, "reset_after_seconds", "resetAfterSeconds")); ok {
		resetBucket := time.Now().Add(time.Duration(resetAfter)*time.Second).Unix() / 300
		return fmt.Sprintf("reset-bucket:%d", resetBucket)
	}
	if seconds, ok := numericValue(firstValue(window, "limit_window_seconds", "limitWindowSeconds")); ok {
		return fmt.Sprintf("window:%.0f", seconds)
	}
	return fallback
}

func numericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case json.Number:
		v, err := typed.Float64()
		return v, err == nil
	case string:
		v, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return v, err == nil
	default:
		return 0, false
	}
}

func formatQuotaWarningPercent(value float64) string {
	if value == float64(int64(value)) {
		return fmt.Sprintf("%.0f%%", value)
	}
	return fmt.Sprintf("%.1f%%", value)
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func allowedIsFalse(value any) bool {
	switch typed := value.(type) {
	case bool:
		return !typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return err == nil && !parsed
	default:
		return false
	}
}

func firstTrimmedString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := firstPresent(values, key)
		if !ok {
			continue
		}
		if text, ok := value.(string); ok {
			if trimmed := strings.TrimSpace(text); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func firstStringish(values map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := firstPresent(values, key)
		if !ok {
			continue
		}
		if text, ok := value.(string); ok {
			if trimmed := strings.TrimSpace(text); trimmed != "" {
				return trimmed
			}
			continue
		}
		if number, ok := numericValue(value); ok {
			return fmt.Sprintf("%.0f", number)
		}
	}
	return ""
}

func sliceValue(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []gin.H:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func emptyAsDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}
