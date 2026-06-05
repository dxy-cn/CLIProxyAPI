package management

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func TestGetMonitorModelDistribution_IncludesTokenBreakdown(t *testing.T) {
	gin.SetMode(gin.TestMode)

	base := time.Now().Local().Add(-90 * time.Minute)
	recordOne := testUsageRecord(base, "api-1", "model-a", "source-a", false, 1000, 200)
	recordOne.Detail = coreusage.Detail{
		InputTokens:     120,
		OutputTokens:    45,
		ReasoningTokens: 8,
		CachedTokens:    30,
		TotalTokens:     165,
	}
	recordTwo := testUsageRecord(base.Add(15*time.Minute), "api-1", "model-a", "source-a", false, 1000, 200)
	recordTwo.Detail = coreusage.Detail{
		InputTokens:     80,
		OutputTokens:    20,
		ReasoningTokens: 5,
		CachedTokens:    10,
		TotalTokens:     100,
	}
	recordFailed := testUsageRecord(base.Add(30*time.Minute), "api-1", "model-b", "source-a", true, 1000, 200)
	recordFailed.Detail = coreusage.Detail{
		InputTokens:  999,
		OutputTokens: 999,
		CachedTokens: 999,
		TotalTokens:  1998,
	}

	h := newMonitorTestHandler(recordOne, recordTwo, recordFailed)
	rr := executeMonitorRequest(h.GetMonitorModelDistribution, "/monitor/model-distribution?sort=tokens")
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Items []struct {
			Model           string `json:"model"`
			Requests        int64  `json:"requests"`
			Tokens          int64  `json:"tokens"`
			InputTokens     int64  `json:"input_tokens"`
			OutputTokens    int64  `json:"output_tokens"`
			ReasoningTokens int64  `json:"reasoning_tokens"`
			CachedTokens    int64  `json:"cached_tokens"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("unexpected items length: %+v", resp.Items)
	}

	if resp.Items[0].Model != "model-b" || resp.Items[0].Tokens != 1998 {
		t.Fatalf("unexpected top item ordering: %+v", resp.Items)
	}

	item := resp.Items[1]
	if item.Model != "model-a" {
		t.Fatalf("unexpected second item: %+v", item)
	}
	if item.Requests != 2 || item.Tokens != 265 {
		t.Fatalf("unexpected aggregate counts: %+v", item)
	}
	if item.InputTokens != 200 || item.OutputTokens != 65 || item.ReasoningTokens != 13 || item.CachedTokens != 40 {
		t.Fatalf("unexpected token breakdown: %+v", item)
	}
}
