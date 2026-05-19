package management

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

func TestGetMonitorKeyTokenStats_AggregatesByAPIKey(t *testing.T) {
	base := time.Date(2026, 4, 21, 12, 0, 0, 0, time.Local)
	h := newMonitorTestHandler(
		testUsageRecordWithAuthAndSource(base.Add(-4*time.Hour), "api-a", "auth-1", "burn-source", false),
		testUsageRecordWithAuthAndSource(base.Add(-3*time.Hour), "api-b", "auth-1", "burn-source", false),
		testUsageRecordWithAuthAndSource(base.Add(-2*time.Hour), "api-a", "auth-2", "burn-source", false),
		testUsageRecordWithAuth(base.Add(-90*time.Minute), "api-d", "auth-2", false),
		testUsageRecordWithAuth(base.Add(-60*time.Minute), "api-c", "auth-2", true),
		testUsageRecordWithAuth(base.Add(-30*time.Hour), "api-old", "auth-1", false),
	)

	startQuery := url.QueryEscape(base.Add(-6 * time.Hour).Format(time.RFC3339))
	endQuery := url.QueryEscape(base.Format(time.RFC3339))
	rr := executeMonitorRequest(h.GetMonitorKeyTokenStats, "/monitor/key-token-stats?start_time="+startQuery+"&end_time="+endQuery)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Total       int              `json:"total"`
		TotalTokens int64            `json:"total_tokens"`
		Account     map[string]int64 `json:"account_totals"`
		Items       []struct {
			APIKey            string           `json:"api_key"`
			AuthIndex         string           `json:"auth_index"`
			Requests          int64            `json:"requests"`
			TotalTokens       int64            `json:"total_tokens"`
			AccountTokens     int64            `json:"account_tokens"`
			AccountTokenShare float64          `json:"account_token_share"`
			TotalTokenShare   float64          `json:"total_token_share"`
			AuthTokens        map[string]int64 `json:"auth_tokens"`
			SourceTokens      map[string]int64 `json:"source_tokens"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if resp.Total != 4 {
		t.Fatalf("unexpected total: got %d want 4", resp.Total)
	}
	if resp.TotalTokens != 120 {
		t.Fatalf("unexpected total tokens: got %d want 120", resp.TotalTokens)
	}
	if resp.Account["auth-1"] != 60 || resp.Account["auth-2"] != 60 {
		t.Fatalf("unexpected account totals: %+v", resp.Account)
	}
	if len(resp.Items) != 4 {
		t.Fatalf("unexpected items count: got %d want 4", len(resp.Items))
	}

	first := resp.Items[0]
	if first.APIKey != "api-a" || first.AuthIndex != "auth-1" {
		t.Fatalf("unexpected first item identity: %+v", first)
	}
	if first.Requests != 2 || first.TotalTokens != 60 || first.AccountTokens != 60 {
		t.Fatalf("unexpected first aggregate: %+v", first)
	}
	if first.AccountTokenShare != 50 || first.TotalTokenShare != 50 {
		t.Fatalf("unexpected first shares: account=%.1f total=%.1f", first.AccountTokenShare, first.TotalTokenShare)
	}
	if first.AuthTokens["auth-1"] != 30 || first.AuthTokens["auth-2"] != 30 {
		t.Fatalf("unexpected first auth token breakdown: %+v", first.AuthTokens)
	}
	if first.SourceTokens["burn-source"] != 60 {
		t.Fatalf("unexpected first source token breakdown: %+v", first.SourceTokens)
	}
}

func testUsageRecordWithAuth(ts time.Time, apiKey, authIndex string, failed bool) coreusage.Record {
	record := testUsageRecord(ts, apiKey, "model-a", "source-a", failed, 1000, 200)
	record.AuthIndex = authIndex
	return record
}

func testUsageRecordWithAuthAndSource(ts time.Time, apiKey, authIndex, source string, failed bool) coreusage.Record {
	record := testUsageRecord(ts, apiKey, "model-a", source, failed, 1000, 200)
	record.AuthIndex = authIndex
	return record
}
