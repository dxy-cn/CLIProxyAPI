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
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func TestGetMonitorDailyModelTokens_SnapshotAggregatesByDayAndModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now().Local().AddDate(0, 0, -2)
	dayOne := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dayTwo := dayOne.AddDate(0, 0, 1)
	start := dayOne
	end := time.Date(dayTwo.Year(), dayTwo.Month(), dayTwo.Day(), 23, 59, 0, 0, dayTwo.Location())

	recordOne := testUsageRecord(dayOne.Add(2*time.Hour), "api-1", "model-a", "source-a", false, 1000, 200)
	recordOne.Detail = coreusage.Detail{
		InputTokens:  100,
		OutputTokens: 40,
		CachedTokens: 25,
		TotalTokens:  140,
	}
	recordTwo := testUsageRecord(dayTwo.Add(3*time.Hour), "api-1", "model-a", "source-a", false, 1000, 200)
	recordTwo.Detail = coreusage.Detail{
		InputTokens:  60,
		OutputTokens: 20,
		CachedTokens: 10,
		TotalTokens:  80,
	}
	recordThree := testUsageRecord(dayTwo.Add(5*time.Hour), "api-1", "model-b", "source-a", false, 1000, 200)
	recordThree.Detail = coreusage.Detail{
		InputTokens:  30,
		OutputTokens: 15,
		CachedTokens: 5,
		TotalTokens:  45,
	}
	recordFailed := testUsageRecord(dayTwo.Add(6*time.Hour), "api-1", "model-c", "source-a", true, 1000, 200)
	recordFailed.Detail = coreusage.Detail{
		InputTokens:  999,
		OutputTokens: 999,
		CachedTokens: 999,
		TotalTokens:  1998,
	}

	h := newMonitorTestHandler(recordOne, recordTwo, recordThree, recordFailed)
	rr := executeMonitorRequest(
		h.GetMonitorDailyModelTokens,
		"/monitor/daily-model-tokens?start_time="+url.QueryEscape(start.Format(time.RFC3339))+"&end_time="+url.QueryEscape(end.Format(time.RFC3339)),
	)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Slots        []string           `json:"slots"`
		Models       []string           `json:"models"`
		InputTokens  map[string][]int64 `json:"input_tokens"`
		OutputTokens map[string][]int64 `json:"output_tokens"`
		CachedTokens map[string][]int64 `json:"cached_tokens"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	expectedSlots := []string{dayOne.Format("2006-01-02"), dayTwo.Format("2006-01-02")}
	if len(resp.Slots) != len(expectedSlots) {
		t.Fatalf("unexpected slot count: got=%v want=%v", resp.Slots, expectedSlots)
	}
	for index, slot := range expectedSlots {
		if resp.Slots[index] != slot {
			t.Fatalf("unexpected slot at %d: got=%s want=%s", index, resp.Slots[index], slot)
		}
	}

	if len(resp.Models) != 2 || resp.Models[0] != "model-a" || resp.Models[1] != "model-b" {
		t.Fatalf("unexpected models order: %+v", resp.Models)
	}
	if got := resp.InputTokens["model-a"]; len(got) != 2 || got[0] != 100 || got[1] != 60 {
		t.Fatalf("unexpected model-a input tokens: %+v", got)
	}
	if got := resp.OutputTokens["model-a"]; len(got) != 2 || got[0] != 40 || got[1] != 20 {
		t.Fatalf("unexpected model-a output tokens: %+v", got)
	}
	if got := resp.CachedTokens["model-a"]; len(got) != 2 || got[0] != 25 || got[1] != 10 {
		t.Fatalf("unexpected model-a cached tokens: %+v", got)
	}
	if got := resp.InputTokens["model-b"]; len(got) != 2 || got[0] != 0 || got[1] != 30 {
		t.Fatalf("unexpected model-b input tokens: %+v", got)
	}
	if got := resp.OutputTokens["model-b"]; len(got) != 2 || got[0] != 0 || got[1] != 15 {
		t.Fatalf("unexpected model-b output tokens: %+v", got)
	}
	if got := resp.CachedTokens["model-b"]; len(got) != 2 || got[0] != 0 || got[1] != 5 {
		t.Fatalf("unexpected model-b cached tokens: %+v", got)
	}
	if _, exists := resp.InputTokens["model-c"]; exists {
		t.Fatalf("failed model should not be included: %+v", resp.InputTokens["model-c"])
	}
}

func TestGetMonitorHourlyModelTokens_DatabasePluginIncludesCurrentPartialHour(t *testing.T) {
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
									CachedTokens: 4,
									TotalTokens:  30,
								},
							},
						},
					},
					"model-b": {
						Details: []usage.RequestDetail{
							{
								Timestamp: partialHourRecord,
								Source:    "source-a",
								Failed:    false,
								Tokens: usage.TokenStats{
									InputTokens:  5,
									OutputTokens: 7,
									CachedTokens: 1,
									TotalTokens:  12,
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
	if added != 2 || skipped != 0 {
		t.Fatalf("unexpected import result: added=%d skipped=%d", added, skipped)
	}

	h := &Handler{usageStats: usage.NewRequestStatistics()}
	rr := executeMonitorRequest(
		h.GetMonitorHourlyModelTokens,
		"/monitor/hourly-model-tokens?hours=24&api=api-partial&start_time="+url.QueryEscape(start.Format(time.RFC3339))+"&end_time="+url.QueryEscape(end.Format(time.RFC3339)),
	)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Slots        []string           `json:"slots"`
		Models       []string           `json:"models"`
		InputTokens  map[string][]int64 `json:"input_tokens"`
		OutputTokens map[string][]int64 `json:"output_tokens"`
		CachedTokens map[string][]int64 `json:"cached_tokens"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	expectedLastSlot := anchor.Format("2006-01-02T15:04:05-07:00")
	if got := resp.Slots[len(resp.Slots)-1]; got != expectedLastSlot {
		t.Fatalf("unexpected slot anchor: got %s want %s", got, expectedLastSlot)
	}
	if len(resp.Models) != 2 || resp.Models[0] != "model-a" || resp.Models[1] != "model-b" {
		t.Fatalf("unexpected models order: %+v", resp.Models)
	}
	lastIndex := len(resp.Slots) - 1
	if got := resp.InputTokens["model-a"][lastIndex]; got != 10 {
		t.Fatalf("unexpected model-a input tokens for current partial hour: got %d want 10", got)
	}
	if got := resp.OutputTokens["model-a"][lastIndex]; got != 20 {
		t.Fatalf("unexpected model-a output tokens for current partial hour: got %d want 20", got)
	}
	if got := resp.CachedTokens["model-a"][lastIndex]; got != 4 {
		t.Fatalf("unexpected model-a cached tokens for current partial hour: got %d want 4", got)
	}
	if got := resp.InputTokens["model-b"][lastIndex]; got != 5 {
		t.Fatalf("unexpected model-b input tokens for current partial hour: got %d want 5", got)
	}
	if got := resp.OutputTokens["model-b"][lastIndex]; got != 7 {
		t.Fatalf("unexpected model-b output tokens for current partial hour: got %d want 7", got)
	}
	if got := resp.CachedTokens["model-b"][lastIndex]; got != 1 {
		t.Fatalf("unexpected model-b cached tokens for current partial hour: got %d want 1", got)
	}
}
