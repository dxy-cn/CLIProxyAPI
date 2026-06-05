package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/usage"
)

func TestGetMonitorHourlyTokens_UsesRequestedRangeEndAsAnchor(t *testing.T) {
	gin.SetMode(gin.TestMode)

	anchor := time.Now().Local().Add(-24 * time.Hour).Truncate(time.Hour)
	slotTime := anchor.Add(-2 * time.Hour)
	start := anchor.Add(-5 * time.Hour)

	h := newMonitorTestHandler(
		testUsageRecord(slotTime.Add(10*time.Minute), "api-1", "model-a", "source-a", false, 1000, 180),
	)

	rr := executeMonitorRequest(
		h.GetMonitorHourlyTokens,
		"/monitor/hourly-tokens?hours=6&start_time="+url.QueryEscape(start.Format(time.RFC3339))+"&end_time="+url.QueryEscape(anchor.Format(time.RFC3339)),
	)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Hours       []string `json:"hours"`
		TotalTokens []int64  `json:"total_tokens"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	expectedLastSlot := anchor.Format("2006-01-02T15:04:05-07:00")
	if got := resp.Hours[len(resp.Hours)-1]; got != expectedLastSlot {
		t.Fatalf("unexpected slot anchor: got %s want %s", got, expectedLastSlot)
	}

	expectedSlot := slotTime.Format("2006-01-02T15:04:05-07:00")
	slotIndex := -1
	for i, slot := range resp.Hours {
		if slot == expectedSlot {
			slotIndex = i
			break
		}
	}
	if slotIndex < 0 {
		t.Fatalf("hour slot not found: %s", expectedSlot)
	}
	if resp.TotalTokens[slotIndex] != 30 {
		t.Fatalf("unexpected total tokens for slot: got %d want 30", resp.TotalTokens[slotIndex])
	}
}

func TestGetMonitorHourlyTokens_DatabasePluginIncludesCurrentPartialHour(t *testing.T) {
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

	partialHourRecord := time.Now().Local().Add(-time.Minute)
	anchor := partialHourRecord.Truncate(time.Hour)
	start := anchor.Add(-23 * time.Hour)
	end := partialHourRecord.Add(time.Minute)

	added, skipped, err := plugin.ImportRecords(usage.StatisticsSnapshot{
		APIs: map[string]usage.APISnapshot{
			"api-partial": {
				Models: map[string]usage.ModelSnapshot{
					"model-a": {
						Details: []usage.RequestDetail{
							{
								Timestamp: partialHourRecord,
								Source:    "source-a",
								Failed:    false,
								Tokens: usage.TokenStats{
									InputTokens:  10,
									OutputTokens: 20,
									TotalTokens:  30,
								},
							},
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ImportRecords failed: %v", err)
	}
	if added != 1 || skipped != 0 {
		t.Fatalf("unexpected import result: added=%d skipped=%d", added, skipped)
	}

	h := &Handler{usageStats: usage.NewRequestStatistics()}
	rr := executeMonitorRequest(
		h.GetMonitorHourlyTokens,
		"/monitor/hourly-tokens?hours=24&api=api-partial&start_time="+url.QueryEscape(start.Format(time.RFC3339))+"&end_time="+url.QueryEscape(end.Format(time.RFC3339)),
	)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Hours       []string `json:"hours"`
		TotalTokens []int64  `json:"total_tokens"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	expectedLastSlot := anchor.Format("2006-01-02T15:04:05-07:00")
	if got := resp.Hours[len(resp.Hours)-1]; got != expectedLastSlot {
		t.Fatalf("unexpected slot anchor: got %s want %s", got, expectedLastSlot)
	}
	if got := resp.TotalTokens[len(resp.TotalTokens)-1]; got != 30 {
		t.Fatalf("unexpected total tokens for current partial hour: got %d want 30", got)
	}
}
