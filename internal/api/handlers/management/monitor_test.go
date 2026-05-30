package management

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestPerRequestTokPerSecUsesTotalLatency(t *testing.T) {
	got := perRequestTokPerSec(120, 1000)
	if got != 120 {
		t.Fatalf("tok/s = %v, want 120", got)
	}
}

func TestPerRequestTokPerSecMatchesCcLoadTotalDuration(t *testing.T) {
	cases := []struct {
		name         string
		outputTokens int64
		durationMs   int64
		want         float64
	}{
		{name: "streaming log", outputTokens: 957, durationMs: 21000, want: 45.57142857142857},
		{name: "non streaming log", outputTokens: 736, durationMs: 17000, want: 43.294117647058826},
		{name: "first byte equals duration", outputTokens: 100, durationMs: 3000, want: 33.333333333333336},
		{name: "zero output", outputTokens: 0, durationMs: 12000, want: 0},
		{name: "long first byte case still uses total duration", outputTokens: 437, durationMs: 19980, want: 21.87187187187187},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := perRequestTokPerSec(tc.outputTokens, tc.durationMs)
			if math.Abs(got-tc.want) > 0.000000001 {
				t.Fatalf("tok/s = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGetMonitorRequestLogs_TimeRangeAndPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)

	base := time.Date(2026, 2, 6, 12, 0, 0, 0, time.Local)
	h := newMonitorTestHandler(
		testUsageRecord(base.Add(-2*time.Hour), "api-1", "model-1", "source-1", false, 1000, 250),
		testUsageRecord(base.Add(-1*time.Hour), "api-1", "model-1", "source-1", true, 1200, 300),
		testUsageRecord(base.Add(-30*time.Minute), "api-1", "model-1", "source-1", false, 1500, 400),
		testUsageRecord(base.Add(-26*time.Hour), "api-2", "model-2", "source-2", false, 2000, 500),
	)

	path := "/monitor/request-logs?start_time=" + url.QueryEscape(base.Add(-2*time.Hour).Format(time.RFC3339)) +
		"&end_time=" + url.QueryEscape(base.Format(time.RFC3339)) + "&page=2&page_size=2"
	rr := executeMonitorRequest(h.GetMonitorRequestLogs, path)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Items []struct {
			Timestamp           time.Time `json:"timestamp"`
			APIKey              string    `json:"api_key"`
			Model               string    `json:"model"`
			Source              string    `json:"source"`
			Failed              bool      `json:"failed"`
			RequestCount        int64     `json:"request_count"`
			SuccessRate         float64   `json:"success_rate"`
			FirstTokenLatencyMs int64     `json:"first_token_latency_ms"`
			TotalDurationMs     int64     `json:"total_duration_ms"`
			TokensPerSecond     float64   `json:"tokens_per_second"`
			RecentRequests      []struct {
				Failed bool `json:"failed"`
			} `json:"recent_requests"`
		} `json:"items"`
		Page       int `json:"page"`
		PageSize   int `json:"page_size"`
		Total      int `json:"total"`
		TotalPages int `json:"total_pages"`
		Filters    struct {
			APIs    []string `json:"apis"`
			Models  []string `json:"models"`
			Sources []string `json:"sources"`
		} `json:"filters"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Total != 3 {
		t.Fatalf("unexpected total: got %d want 3", resp.Total)
	}
	if resp.TotalPages != 2 {
		t.Fatalf("unexpected total pages: got %d want 2", resp.TotalPages)
	}
	if resp.Page != 2 || resp.PageSize != 2 {
		t.Fatalf("unexpected page info: page=%d page_size=%d", resp.Page, resp.PageSize)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("unexpected items count: got %d want 1", len(resp.Items))
	}
	if !resp.Items[0].Timestamp.Equal(base.Add(-2 * time.Hour)) {
		t.Fatalf("unexpected item timestamp: got %s", resp.Items[0].Timestamp)
	}
	if resp.Items[0].APIKey != "api-1" || resp.Items[0].Model != "model-1" || resp.Items[0].Source != "source-1" {
		t.Fatalf("unexpected item content: %+v", resp.Items[0])
	}
	if resp.Items[0].RequestCount != 3 {
		t.Fatalf("unexpected request_count: got %d want 3", resp.Items[0].RequestCount)
	}
	if resp.Items[0].SuccessRate != 66.7 {
		t.Fatalf("unexpected success_rate: got %.1f want 66.7", resp.Items[0].SuccessRate)
	}
	if resp.Items[0].FirstTokenLatencyMs != 250 {
		t.Fatalf("unexpected first_token_latency_ms: got %d want 250", resp.Items[0].FirstTokenLatencyMs)
	}
	if resp.Items[0].TotalDurationMs != 1000 {
		t.Fatalf("unexpected total_duration_ms: got %d want 1000", resp.Items[0].TotalDurationMs)
	}
	if resp.Items[0].TokensPerSecond != 20 {
		t.Fatalf("unexpected tokens_per_second: got %.1f want 20.0", resp.Items[0].TokensPerSecond)
	}
	if len(resp.Items[0].RecentRequests) != 3 {
		t.Fatalf("unexpected recent_requests count: got %d want 3", len(resp.Items[0].RecentRequests))
	}

	assertStringSliceEqual(t, resp.Filters.APIs, []string{"api-1"})
	assertStringSliceEqual(t, resp.Filters.Models, []string{"model-1"})
	assertStringSliceEqual(t, resp.Filters.Sources, []string{"source-1"})
}

func TestGetMonitorRequestLogs_LimitsBrowsableRows(t *testing.T) {
	gin.SetMode(gin.TestMode)

	base := time.Date(2026, 2, 6, 12, 0, 0, 0, time.Local)
	records := make([]coreusage.Record, 0, 10001)
	for i := 0; i < 10001; i++ {
		records = append(records, testUsageRecord(base.Add(-time.Duration(i)*time.Second), "api-1", "model-1", "source-1", false, 1000, 250))
	}
	h := newMonitorTestHandler(records...)

	path := "/monitor/request-logs?start_time=" + url.QueryEscape(base.Add(-23*time.Hour).Format(time.RFC3339)) +
		"&end_time=" + url.QueryEscape(base.Format(time.RFC3339)) + "&page=999&page_size=20"
	rr := executeMonitorRequest(h.GetMonitorRequestLogs, path)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Items        []json.RawMessage `json:"items"`
		Page         int               `json:"page"`
		PageSize     int               `json:"page_size"`
		Total        int               `json:"total"`
		TotalPages   int               `json:"total_pages"`
		TotalLimited bool              `json:"total_limited"`
		TotalLimit   int               `json:"total_limit"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Total != 5000 || resp.TotalPages != 250 || resp.Page != 250 {
		t.Fatalf("unexpected capped pagination: total=%d totalPages=%d page=%d", resp.Total, resp.TotalPages, resp.Page)
	}
	if resp.TotalLimited || resp.TotalLimit != 10000 {
		t.Fatalf("expected limited response, got limited=%v limit=%d", resp.TotalLimited, resp.TotalLimit)
	}
	if len(resp.Items) != 20 {
		t.Fatalf("unexpected item count: got %d want 20", len(resp.Items))
	}
}

func TestGetMonitorChannelStats_StatusFilterAndAggregate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	base := time.Date(2026, 2, 6, 12, 0, 0, 0, time.Local)
	h := newMonitorTestHandler(
		testUsageRecord(base.Add(-2*time.Hour), "api-1", "model-a", "source-a", false, 1000, 200),
		testUsageRecord(base.Add(-90*time.Minute), "api-1", "model-a", "source-a", true, 1000, 200),
		testUsageRecord(base.Add(-70*time.Minute), "api-2", "model-b", "source-a", false, 1000, 200),
		testUsageRecord(base.Add(-60*time.Minute), "api-1", "model-a", "source-b", false, 1000, 200),
	)

	rr := executeMonitorRequest(h.GetMonitorChannelStats, "/monitor/channel-stats?status=failed")
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Items []struct {
			Source          string  `json:"source"`
			TotalRequests   int64   `json:"total_requests"`
			SuccessRequests int64   `json:"success_requests"`
			FailedRequests  int64   `json:"failed_requests"`
			SuccessRate     float64 `json:"success_rate"`
			Models          []struct {
				Model    string `json:"model"`
				Requests int64  `json:"requests"`
				Failed   int64  `json:"failed"`
			} `json:"models"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Total != 1 {
		t.Fatalf("unexpected total: got %d want 1", resp.Total)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("unexpected items count: got %d", len(resp.Items))
	}
	item := resp.Items[0]
	if item.Source != "source-a" {
		t.Fatalf("unexpected source: %s", item.Source)
	}
	if item.TotalRequests != 3 || item.SuccessRequests != 2 || item.FailedRequests != 1 {
		t.Fatalf("unexpected aggregate: %+v", item)
	}
	if item.SuccessRate != 66.7 {
		t.Fatalf("unexpected success rate: %.1f", item.SuccessRate)
	}
	if len(item.Models) != 2 {
		t.Fatalf("unexpected model count: %d", len(item.Models))
	}
}

func TestGetMonitorFailureAnalysis_OnlyFailedSources(t *testing.T) {
	gin.SetMode(gin.TestMode)

	base := time.Date(2026, 2, 6, 12, 0, 0, 0, time.Local)
	h := newMonitorTestHandler(
		testUsageRecord(base.Add(-4*time.Hour), "api-1", "model-a", "source-a", true, 1000, 200),
		testUsageRecord(base.Add(-3*time.Hour), "api-1", "model-b", "source-a", true, 1000, 200),
		testUsageRecord(base.Add(-2*time.Hour), "api-1", "model-b", "source-a", false, 1000, 200),
		testUsageRecord(base.Add(-90*time.Minute), "api-2", "model-c", "source-b", true, 1000, 200),
		testUsageRecord(base.Add(-1*time.Hour), "api-3", "model-c", "source-c", false, 1000, 200),
	)

	rr := executeMonitorRequest(h.GetMonitorFailureAnalysis, "/monitor/failure-analysis?limit=2")
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Items []struct {
			Source      string `json:"source"`
			FailedCount int64  `json:"failed_count"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Total != 2 {
		t.Fatalf("unexpected total: got %d want 2", resp.Total)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("unexpected items count: %d", len(resp.Items))
	}
	if resp.Items[0].Source != "source-a" || resp.Items[0].FailedCount != 2 {
		t.Fatalf("unexpected first item: %+v", resp.Items[0])
	}
	if resp.Items[1].Source != "source-b" || resp.Items[1].FailedCount != 1 {
		t.Fatalf("unexpected second item: %+v", resp.Items[1])
	}
}

func TestGetMonitorRequestLogs_InvalidTimeRange(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := newMonitorTestHandler(testUsageRecord(time.Now(), "api-1", "model-a", "source-a", false, 1000, 200))
	path := "/monitor/request-logs?start_time=2026-02-07T12:00:00Z&end_time=2026-02-06T12:00:00Z"
	rr := executeMonitorRequest(h.GetMonitorRequestLogs, path)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid time range") {
		t.Fatalf("unexpected error response: %s", rr.Body.String())
	}
}

func TestGetMonitorRequestLogs_ApiFilterContains(t *testing.T) {
	gin.SetMode(gin.TestMode)

	base := time.Date(2026, 2, 6, 12, 0, 0, 0, time.Local)
	h := newMonitorTestHandler(
		testUsageRecord(base.Add(-3*time.Hour), "abc-111", "model-a", "source-a", false, 1000, 200),
		testUsageRecord(base.Add(-2*time.Hour), "xyz-222", "model-a", "source-a", false, 1000, 200),
		testUsageRecord(base.Add(-1*time.Hour), "abc-333", "model-a", "source-a", true, 1000, 200),
	)

	rr := executeMonitorRequest(h.GetMonitorRequestLogs, "/monitor/request-logs?api_filter=abc")
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Total int `json:"total"`
		Items []struct {
			APIKey string `json:"api_key"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Total != 2 || len(resp.Items) != 2 {
		t.Fatalf("unexpected filtered total: total=%d items=%d", resp.Total, len(resp.Items))
	}
	for _, item := range resp.Items {
		if !strings.Contains(item.APIKey, "abc") {
			t.Fatalf("api_filter failed, got api_key=%s", item.APIKey)
		}
	}
}

func TestGetMonitorRequestLogs_ApiFilterMatchesConfiguredName(t *testing.T) {
	gin.SetMode(gin.TestMode)

	base := time.Date(2026, 2, 6, 12, 0, 0, 0, time.Local)
	h := newMonitorTestHandler(
		testUsageRecord(base.Add(-3*time.Hour), "abc-111", "model-a", "source-a", false, 1000, 200),
		testUsageRecord(base.Add(-2*time.Hour), "xyz-222", "model-a", "source-a", false, 1000, 200),
	)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configYAML := `
auth:
  providers:
    config-api-key:
      api-key-entries:
        - api-key: abc-111
          name: Alpha Team
        - api-key: xyz-222
          name: Beta Team
`
	if err := os.WriteFile(configPath, []byte(strings.TrimSpace(configYAML)), 0o600); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	h.configFilePath = configPath

	rr := executeMonitorRequest(h.GetMonitorRequestLogs, "/monitor/request-logs?api_filter=alpha")
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Total int `json:"total"`
		Items []struct {
			APIKey string `json:"api_key"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Total != 1 || len(resp.Items) != 1 {
		t.Fatalf("unexpected filtered total: total=%d items=%d", resp.Total, len(resp.Items))
	}
	if resp.Items[0].APIKey != "abc-111" {
		t.Fatalf("api_filter by name failed, got api_key=%s", resp.Items[0].APIKey)
	}
}

func TestGetMonitorRequestLogs_DatabasePluginPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	usage.CloseDatabasePlugin()
	t.Cleanup(usage.CloseDatabasePlugin)

	authDir := t.TempDir()
	if err := usage.InitDatabasePlugin(context.Background(), "", authDir); err != nil {
		t.Fatalf("InitDatabasePlugin failed: %v", err)
	}
	plugin := usage.GetDatabasePlugin()
	if plugin == nil {
		t.Fatalf("expected database plugin to be initialized")
	}

	base := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	added, skipped, err := plugin.ImportRecords(usage.StatisticsSnapshot{
		APIs: map[string]usage.APISnapshot{
			"api-db": {
				Models: map[string]usage.ModelSnapshot{
					"model-db": {
						Details: []usage.RequestDetail{
							{Timestamp: base.Add(-2 * time.Hour), Source: "source-db", Failed: false, Tokens: usage.TokenStats{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}},
							{Timestamp: base.Add(-1 * time.Hour), Source: "source-db", Failed: true, Tokens: usage.TokenStats{InputTokens: 3, OutputTokens: 4, TotalTokens: 7}},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ImportRecords failed: %v", err)
	}
	if added != 2 || skipped != 0 {
		t.Fatalf("unexpected import result: added=%d skipped=%d", added, skipped)
	}

	h := &Handler{usageStats: usage.NewRequestStatistics()}
	startQuery := url.QueryEscape(base.Add(-3 * time.Hour).Format(time.RFC3339))
	endQuery := url.QueryEscape(base.Add(1 * time.Hour).Format(time.RFC3339))
	rr := executeMonitorRequest(h.GetMonitorRequestLogs, "/monitor/request-logs?api=api-db&page=1&page_size=1&start_time="+startQuery+"&end_time="+endQuery)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Total int `json:"total"`
		Items []struct {
			APIKey string `json:"api_key"`
			Model  string `json:"model"`
			Source string `json:"source"`
			Failed bool   `json:"failed"`
		} `json:"items"`
	}
	if err = json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Total != 2 {
		t.Fatalf("unexpected total: got %d want 2", resp.Total)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("unexpected item count: got %d want 1", len(resp.Items))
	}
	if resp.Items[0].APIKey != "api-db" || resp.Items[0].Model != "model-db" || resp.Items[0].Source != "source-db" {
		t.Fatalf("unexpected item: %+v", resp.Items[0])
	}
	if !resp.Items[0].Failed {
		t.Fatalf("expected latest item to be failed")
	}
}

func newMonitorTestHandler(records ...coreusage.Record) *Handler {
	stats := usage.NewRequestStatistics()
	for _, record := range records {
		stats.Record(context.Background(), record)
	}
	return &Handler{usageStats: stats}
}

func testUsageRecord(ts time.Time, apiKey, model, source string, failed bool, latencyMs, firstTokenLatencyMs int64) coreusage.Record {
	return coreusage.Record{
		APIKey:            apiKey,
		Model:             model,
		Source:            source,
		RequestedAt:       ts,
		Latency:           time.Duration(latencyMs) * time.Millisecond,
		FirstTokenLatency: time.Duration(firstTokenLatencyMs) * time.Millisecond,
		Failed:            failed,
		Detail: coreusage.Detail{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
		},
	}
}

func executeMonitorRequest(handler func(*gin.Context), target string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rr)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	handler(c)
	return rr
}

func assertStringSliceEqual(t *testing.T, got, want []string) {
	t.Helper()
	gotCopy := append([]string(nil), got...)
	wantCopy := append([]string(nil), want...)
	sort.Strings(gotCopy)
	sort.Strings(wantCopy)
	if len(gotCopy) != len(wantCopy) {
		t.Fatalf("slice length mismatch: got=%v want=%v", gotCopy, wantCopy)
	}
	for i := range gotCopy {
		if gotCopy[i] != wantCopy[i] {
			t.Fatalf("slice mismatch: got=%v want=%v", gotCopy, wantCopy)
		}
	}
}

func TestGetMonitorServiceHealth_BasicBucketing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now()

	h := newMonitorTestHandler(
		// 30 minutes ago -> block index 670 (near the end)
		testUsageRecord(now.Add(-30*time.Minute), "api-1", "model-a", "source-a", false, 1000, 200),
		testUsageRecord(now.Add(-30*time.Minute), "api-1", "model-a", "source-a", true, 1000, 200),
		// 2 hours ago -> block index ~664
		testUsageRecord(now.Add(-2*time.Hour), "api-2", "model-b", "source-b", false, 1000, 200),
		// 8 days ago -> outside the window, should be excluded
		testUsageRecord(now.Add(-8*24*time.Hour), "api-3", "model-c", "source-c", false, 1000, 200),
	)

	rr := executeMonitorRequest(h.GetMonitorServiceHealth, "/monitor/service-health")
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Rows            int `json:"rows"`
		Cols            int `json:"cols"`
		BlockDurationMs int `json:"block_duration_ms"`
		Blocks          []struct {
			Success int64 `json:"success"`
			Failure int64 `json:"failure"`
		} `json:"blocks"`
		TotalSuccess int64   `json:"total_success"`
		TotalFailure int64   `json:"total_failure"`
		SuccessRate  float64 `json:"success_rate"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Rows != 7 {
		t.Fatalf("unexpected rows: got %d want 7", resp.Rows)
	}
	if resp.Cols != 96 {
		t.Fatalf("unexpected cols: got %d want 96", resp.Cols)
	}
	if resp.BlockDurationMs != 900000 {
		t.Fatalf("unexpected block_duration_ms: got %d want 900000", resp.BlockDurationMs)
	}
	if len(resp.Blocks) != 672 {
		t.Fatalf("unexpected blocks length: got %d want 672", len(resp.Blocks))
	}
	if resp.TotalSuccess != 2 {
		t.Fatalf("unexpected total_success: got %d want 2", resp.TotalSuccess)
	}
	if resp.TotalFailure != 1 {
		t.Fatalf("unexpected total_failure: got %d want 1", resp.TotalFailure)
	}

	// 8-day-old record should be excluded
	total := resp.TotalSuccess + resp.TotalFailure
	if total != 3 {
		t.Fatalf("unexpected total requests (success+failure): got %d want 3", total)
	}

	// Verify non-zero blocks exist
	nonZero := 0
	for _, b := range resp.Blocks {
		if b.Success > 0 || b.Failure > 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Fatal("expected at least one non-zero block")
	}
}

func TestGetMonitorServiceHealth_EmptySnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := newMonitorTestHandler() // no records

	rr := executeMonitorRequest(h.GetMonitorServiceHealth, "/monitor/service-health")
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Blocks       []struct{} `json:"blocks"`
		TotalSuccess int64      `json:"total_success"`
		TotalFailure int64      `json:"total_failure"`
		SuccessRate  float64    `json:"success_rate"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if len(resp.Blocks) != 672 {
		t.Fatalf("unexpected blocks length: got %d want 672", len(resp.Blocks))
	}
	if resp.TotalSuccess != 0 || resp.TotalFailure != 0 {
		t.Fatalf("expected zero totals, got success=%d failure=%d", resp.TotalSuccess, resp.TotalFailure)
	}
	if resp.SuccessRate != 0 {
		t.Fatalf("expected 0 success rate for empty data, got %f", resp.SuccessRate)
	}
}
