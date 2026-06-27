package management

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/apikeys"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/usage"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const apiKeyBalanceInterval = 5 * time.Hour

type apiKeyRebalanceResult struct {
	Status      string                      `json:"status"`
	Reason      string                      `json:"reason,omitempty"`
	Credentials int                         `json:"credentials"`
	Keys        int                         `json:"keys"`
	Changed     int                         `json:"changed"`
	Assignments []apiKeyRebalanceAssignment `json:"assignments,omitempty"`
}

type apiKeyRebalanceAssignment struct {
	APIKey           string `json:"api_key"`
	FromAuthIdentity string `json:"from_auth_identity,omitempty"`
	ToAuthIdentity   string `json:"to_auth_identity,omitempty"`
	TotalTokens      int64  `json:"total_tokens"`
	Changed          bool   `json:"changed"`
}

type apiKeyBalanceCredential struct {
	Identity string
}

type apiKeyBalanceParticipant struct {
	APIKey       string
	AuthIdentity string
	TotalTokens  int64
}

type apiKeyBalancePlan struct {
	APIKey       string
	FromIdentity string
	ToIdentity   string
	TotalTokens  int64
}

type apiKeyBalanceBucket struct {
	Identity string
	Tokens   int64
}

// RebalanceAPIKeys immediately redistributes API key auth_identity bindings across
// auth files that opted in with auto_balance=true.
func (h *Handler) RebalanceAPIKeys(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}
	result, err := h.rebalanceAPIKeyBindingsAt(c.Request.Context(), time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) startAPIKeyBalanceScanner(ctx context.Context, interval time.Duration) {
	if h == nil || interval <= 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		if h.apiKeyBalanceDone != nil {
			defer close(h.apiKeyBalanceDone)
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-h.apiKeyBalanceStop:
				return
			case <-ticker.C:
				h.runAPIKeyBalanceScan(ctx)
			}
		}
	}()
}

func (h *Handler) runAPIKeyBalanceScan(ctx context.Context) {
	if h == nil {
		return
	}
	result, err := h.rebalanceAPIKeyBindingsAt(ctx, time.Now())
	if err != nil {
		log.WithError(err).Warn("api key auto-balance failed")
		return
	}
	if result.Changed > 0 {
		log.WithFields(log.Fields{
			"changed":     result.Changed,
			"keys":        result.Keys,
			"credentials": result.Credentials,
		}).Info("api key auto-balance applied")
	}
}

func (h *Handler) rebalanceAPIKeyBindingsAt(ctx context.Context, now time.Time) (apiKeyRebalanceResult, error) {
	result := apiKeyRebalanceResult{Status: "skipped"}
	if h == nil || h.cfg == nil {
		result.Reason = "config unavailable"
		return result, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}

	h.apiKeyBalanceMu.Lock()
	defer h.apiKeyBalanceMu.Unlock()

	if strategy, ok := coreauth.NormalizeRoutingStrategy(h.cfg.Routing.Strategy); !ok || strategy != coreauth.RoutingStrategyAccountBind {
		result.Reason = "account-bind routing required"
		return result, nil
	}

	credentials := h.autoBalanceCredentials()
	result.Credentials = len(credentials)
	if len(credentials) < 2 {
		result.Reason = "at least two auto-balance credentials required"
		return result, nil
	}

	records, err := h.currentAPIKeyRecords(ctx)
	if err != nil {
		return result, fmt.Errorf("list api key records: %w", err)
	}
	records = apikeys.NormalizeRecords(records)
	if len(records) == 0 {
		result.Reason = "no api keys configured"
		return result, nil
	}

	eligible := make(map[string]struct{}, len(credentials))
	for _, credential := range credentials {
		eligible[credential.Identity] = struct{}{}
	}
	apiKeys := make([]string, 0, len(records))
	for _, record := range records {
		if strings.TrimSpace(record.APIKey) != "" {
			apiKeys = append(apiKeys, strings.TrimSpace(record.APIKey))
		}
	}
	tokenTotals := h.apiKeyTokenTotals(ctx, apiKeys, now)
	defaultIdentity := strings.TrimSpace(h.cfg.Routing.DefaultModelAccount)

	participants := make([]apiKeyBalanceParticipant, 0, len(records))
	for _, record := range records {
		apiKey := strings.TrimSpace(record.APIKey)
		if apiKey == "" {
			continue
		}
		identity := strings.TrimSpace(record.AuthIdentity)
		if identity == "" {
			identity = defaultIdentity
		}
		if _, ok := eligible[identity]; !ok {
			continue
		}
		participants = append(participants, apiKeyBalanceParticipant{
			APIKey:       apiKey,
			AuthIdentity: identity,
			TotalTokens:  tokenTotals[apiKey],
		})
	}
	result.Keys = len(participants)
	if len(participants) == 0 {
		result.Reason = "no api keys bound to auto-balance credentials"
		return result, nil
	}

	plans := planAPIKeyBalance(participants, credentials)
	if len(plans) == 0 {
		result.Reason = "no rebalance changes needed"
		return result, nil
	}

	recordByKey := make(map[string]int, len(records))
	for index, record := range records {
		recordByKey[strings.TrimSpace(record.APIKey)] = index
	}
	for _, plan := range plans {
		index, ok := recordByKey[plan.APIKey]
		if !ok {
			continue
		}
		records[index].AuthIdentity = plan.ToIdentity
		result.Assignments = append(result.Assignments, apiKeyRebalanceAssignment{
			APIKey:           util.HideAPIKey(plan.APIKey),
			FromAuthIdentity: plan.FromIdentity,
			ToAuthIdentity:   plan.ToIdentity,
			TotalTokens:      plan.TotalTokens,
			Changed:          plan.FromIdentity != plan.ToIdentity,
		})
		if plan.FromIdentity != plan.ToIdentity {
			result.Changed++
		}
	}
	if result.Changed == 0 {
		result.Status = "skipped"
		result.Reason = "no rebalance changes needed"
		return result, nil
	}

	if err := h.applyAPIKeyBalanceRecords(ctx, records, plans); err != nil {
		return result, err
	}
	result.Status = "ok"
	result.Reason = ""
	return result, nil
}

func (h *Handler) autoBalanceCredentials() []apiKeyBalanceCredential {
	if h == nil || h.authManager == nil {
		return nil
	}
	seen := make(map[string]struct{})
	credentials := make([]apiKeyBalanceCredential, 0)
	for _, auth := range h.authManager.List() {
		if auth == nil || auth.Disabled || auth.Unavailable || isRuntimeOnlyAuth(auth) {
			continue
		}
		if !authFileAutoBalance(auth) {
			continue
		}
		identity := strings.TrimSpace(auth.StableIdentity())
		if identity == "" {
			continue
		}
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		credentials = append(credentials, apiKeyBalanceCredential{Identity: identity})
	}
	sort.Slice(credentials, func(i, j int) bool {
		return credentials[i].Identity < credentials[j].Identity
	})
	return credentials
}

func planAPIKeyBalance(participants []apiKeyBalanceParticipant, credentials []apiKeyBalanceCredential) []apiKeyBalancePlan {
	if len(participants) == 0 || len(credentials) < 2 {
		return nil
	}
	buckets := make([]apiKeyBalanceBucket, 0, len(credentials))
	for _, credential := range credentials {
		identity := strings.TrimSpace(credential.Identity)
		if identity != "" {
			buckets = append(buckets, apiKeyBalanceBucket{Identity: identity})
		}
	}
	if len(buckets) < 2 {
		return nil
	}

	keys := append([]apiKeyBalanceParticipant(nil), participants...)
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].TotalTokens == keys[j].TotalTokens {
			return keys[i].APIKey < keys[j].APIKey
		}
		return keys[i].TotalTokens > keys[j].TotalTokens
	})

	plans := make([]apiKeyBalancePlan, 0, len(keys))
	for _, key := range keys {
		fromIdentity := strings.TrimSpace(key.AuthIdentity)
		if key.TotalTokens <= 0 {
			plans = append(plans, apiKeyBalancePlan{
				APIKey:       key.APIKey,
				FromIdentity: fromIdentity,
				ToIdentity:   fromIdentity,
				TotalTokens:  key.TotalTokens,
			})
			continue
		}
		sort.SliceStable(buckets, func(i, j int) bool {
			if buckets[i].Tokens == buckets[j].Tokens {
				return buckets[i].Identity < buckets[j].Identity
			}
			return buckets[i].Tokens < buckets[j].Tokens
		})
		target := &buckets[0]
		plans = append(plans, apiKeyBalancePlan{
			APIKey:       key.APIKey,
			FromIdentity: fromIdentity,
			ToIdentity:   target.Identity,
			TotalTokens:  key.TotalTokens,
		})
		target.Tokens += key.TotalTokens
	}
	return plans
}

func (h *Handler) apiKeyTokenTotals(ctx context.Context, apiKeys []string, now time.Time) map[string]int64 {
	totals := make(map[string]int64, len(apiKeys))
	keySet := make(map[string]struct{}, len(apiKeys))
	for _, key := range apiKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		totals[key] = 0
		keySet[key] = struct{}{}
	}
	if len(keySet) == 0 {
		return totals
	}

	start := now.Add(-apiKeyBalanceInterval)
	if dbPlugin := usage.GetDatabasePlugin(); dbPlugin != nil {
		rows, err := dbPlugin.QueryMonitorKeyTokenStats(ctx, usage.MonitorQueryFilter{
			APIKeys: apiKeys,
			Start:   &start,
			End:     &now,
		})
		if err == nil {
			for _, row := range rows {
				key := strings.TrimSpace(row.APIKey)
				if _, ok := keySet[key]; ok {
					totals[key] += row.TotalTokens
				}
			}
			return totals
		}
		log.WithError(err).Warn("api key auto-balance usage query failed, falling back to memory snapshot")
	}

	snapshot := h.usageSnapshot()
	for apiKey, api := range snapshot.APIs {
		if _, ok := keySet[apiKey]; !ok {
			continue
		}
		for _, model := range api.Models {
			for _, detail := range model.Details {
				if detail.Failed || detail.Timestamp.Before(start) || detail.Timestamp.After(now) {
					continue
				}
				total := detail.Tokens.TotalTokens
				if total == 0 {
					total = detail.Tokens.InputTokens + detail.Tokens.OutputTokens + detail.Tokens.ReasoningTokens
				}
				totals[apiKey] += total
			}
		}
	}
	return totals
}

func (h *Handler) applyAPIKeyBalanceRecords(ctx context.Context, records []apikeys.Record, plans []apiKeyBalancePlan) error {
	changed := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		if plan.FromIdentity != plan.ToIdentity {
			changed[plan.APIKey] = struct{}{}
		}
	}
	if len(changed) == 0 {
		return nil
	}

	if h.apiKeyStore != nil {
		var saved []apikeys.Record
		for _, record := range records {
			if _, ok := changed[strings.TrimSpace(record.APIKey)]; !ok {
				continue
			}
			nextSaved, err := h.apiKeyStore.UpsertAPIKeyRecord(ctx, record)
			if err != nil {
				return fmt.Errorf("save api key binding: %w", err)
			}
			saved = nextSaved
		}
		if len(saved) > 0 {
			apikeys.ApplyToConfig(h.cfg, saved)
			if h.configUpdateHook != nil {
				h.configUpdateHook(h.cfg)
			}
		}
		return nil
	}

	apikeys.ApplyToConfig(h.cfg, records)
	if h.configUpdateHook != nil {
		h.configUpdateHook(h.cfg)
	}
	configFilePath := strings.TrimSpace(h.configFilePath)
	if configFilePath == "" {
		return nil
	}
	if err := h.persistConfigOnly(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	for _, record := range records {
		if _, ok := changed[strings.TrimSpace(record.APIKey)]; !ok {
			continue
		}
		if err := config.SaveConfigPreserveCommentsUpdateAPIKeyAuthIdentity(configFilePath, record.APIKey, record.AuthIdentity); err != nil {
			return fmt.Errorf("save api key binding: %w", err)
		}
	}
	return nil
}
