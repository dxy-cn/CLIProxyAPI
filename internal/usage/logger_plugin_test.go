package usage

import (
	"context"
	"fmt"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestRequestStatisticsRecordIncludesLatency(t *testing.T) {
	stats := NewRequestStatistics()
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "test-key",
		Model:       "gpt-5.4",
		RequestedAt: time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
		Latency:     1500 * time.Millisecond,
		Detail: coreusage.Detail{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
		},
	})

	snapshot := stats.Snapshot()
	details := snapshot.APIs["test-key"].Models["gpt-5.4"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
	if details[0].LatencyMs != 1500 {
		t.Fatalf("latency_ms = %d, want 1500", details[0].LatencyMs)
	}
}

func TestRequestStatisticsCapsRetainedDetails(t *testing.T) {
	stats := NewRequestStatistics()
	stats.maxDetails = 3
	start := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		stats.Record(context.Background(), coreusage.Record{
			APIKey:      "test-key",
			Model:       "gpt-5.4",
			RequestedAt: start.Add(time.Duration(i) * time.Second),
			Detail: coreusage.Detail{
				InputTokens: 1,
				TotalTokens: 1,
			},
		})
	}

	snapshot := stats.Snapshot()
	model := snapshot.APIs["test-key"].Models["gpt-5.4"]
	if model.TotalRequests != 5 {
		t.Fatalf("total requests = %d, want 5", model.TotalRequests)
	}
	if len(model.Details) != 3 {
		t.Fatalf("details len = %d, want 3", len(model.Details))
	}
	if got := model.Details[0].Timestamp; !got.Equal(start.Add(2 * time.Second)) {
		t.Fatalf("oldest retained timestamp = %s, want %s", got, start.Add(2*time.Second))
	}
}

func TestRequestStatisticsCapsRetainedDetailsAcrossModels(t *testing.T) {
	stats := NewRequestStatistics()
	stats.maxDetails = 3
	start := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	records := []struct {
		apiKey string
		model  string
	}{
		{"api-a", "model-a"},
		{"api-b", "model-b"},
		{"api-a", "model-a"},
		{"api-b", "model-b"},
		{"api-a", "model-a"},
	}
	for i, record := range records {
		stats.Record(context.Background(), coreusage.Record{
			APIKey:      record.apiKey,
			Model:       record.model,
			RequestedAt: start.Add(time.Duration(i) * time.Second),
			Detail: coreusage.Detail{
				InputTokens: 1,
				TotalTokens: 1,
			},
		})
	}

	snapshot := stats.Snapshot()
	totalDetails := 0
	for _, api := range snapshot.APIs {
		for _, model := range api.Models {
			totalDetails += len(model.Details)
		}
	}
	if totalDetails != 3 {
		t.Fatalf("retained details = %d, want 3", totalDetails)
	}
	if snapshot.TotalRequests != 5 {
		t.Fatalf("snapshot total requests = %d, want 5", snapshot.TotalRequests)
	}
}

func TestRequestStatisticsEvictsAggregateKeysWithOldDetails(t *testing.T) {
	stats := NewRequestStatistics()
	stats.maxDetails = 3
	start := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		stats.Record(context.Background(), coreusage.Record{
			APIKey:      fmt.Sprintf("api-%d", i),
			Model:       "gpt-5.4",
			RequestedAt: start.Add(time.Duration(i) * time.Second),
			Detail: coreusage.Detail{
				InputTokens: 1,
				TotalTokens: 1,
			},
		})
	}

	snapshot := stats.Snapshot()
	if len(snapshot.APIs) > 3 {
		t.Fatalf("api aggregate keys = %d, want at most 3", len(snapshot.APIs))
	}
	if _, exists := snapshot.APIs["api-0"]; exists {
		t.Fatal("old api aggregate key should be evicted with its last detail")
	}
	if _, exists := snapshot.APIs["api-4"]; !exists {
		t.Fatal("new api aggregate key should remain")
	}
	if snapshot.TotalRequests != 5 {
		t.Fatalf("snapshot total requests = %d, want 5", snapshot.TotalRequests)
	}
}

func TestRequestStatisticsDefaultKeepsOnlyRecentFiveThousandDetails(t *testing.T) {
	stats := NewRequestStatistics()
	start := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 5001; i++ {
		stats.Record(context.Background(), coreusage.Record{
			APIKey:      "test-key",
			Model:       "gpt-5.4",
			RequestedAt: start.Add(time.Duration(i) * time.Second),
			Detail: coreusage.Detail{
				InputTokens: 1,
				TotalTokens: 1,
			},
		})
	}

	snapshot := stats.Snapshot()
	model := snapshot.APIs["test-key"].Models["gpt-5.4"]
	if snapshot.TotalRequests != 5001 {
		t.Fatalf("snapshot total requests = %d, want 5001", snapshot.TotalRequests)
	}
	if model.TotalRequests != 5001 {
		t.Fatalf("model total requests = %d, want 5001", model.TotalRequests)
	}
	if len(model.Details) != 5000 {
		t.Fatalf("details len = %d, want 5000", len(model.Details))
	}
	if got := model.Details[0].Timestamp; !got.Equal(start.Add(time.Second)) {
		t.Fatalf("oldest retained timestamp = %s, want %s", got, start.Add(time.Second))
	}
}

func TestRequestStatisticsEvictionDoesNotLimitDatabasePersistence(t *testing.T) {
	store := newTestSQLiteUsageStore(t)
	t.Cleanup(func() { _ = store.Close() })

	plugin := &DatabasePlugin{
		store:  store,
		buffer: make([]UsageRecord, 0, 5001),
	}
	stats := NewRequestStatistics()
	start := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 5001; i++ {
		record := coreusage.Record{
			APIKey:      "test-key",
			Model:       "gpt-5.4",
			RequestedAt: start.Add(time.Duration(i) * time.Second),
			Detail: coreusage.Detail{
				InputTokens: 1,
				TotalTokens: 1,
			},
		}
		plugin.HandleUsage(context.Background(), record)
		stats.Record(context.Background(), record)
	}
	plugin.flush()

	dbStats, err := store.GetAggregatedStats(context.Background())
	if err != nil {
		t.Fatalf("GetAggregatedStats failed: %v", err)
	}
	if dbStats.TotalRequests != 5001 {
		t.Fatalf("database total requests = %d, want 5001", dbStats.TotalRequests)
	}

	model := stats.Snapshot().APIs["test-key"].Models["gpt-5.4"]
	if len(model.Details) != 5000 {
		t.Fatalf("memory details len = %d, want 5000", len(model.Details))
	}
}

func TestRequestStatisticsMergeSnapshotDedupIgnoresLatency(t *testing.T) {
	stats := NewRequestStatistics()
	timestamp := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	first := StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: timestamp,
							LatencyMs: 0,
							Source:    "user@example.com",
							AuthIndex: "0",
							Tokens: TokenStats{
								InputTokens:  10,
								OutputTokens: 20,
								TotalTokens:  30,
							},
						}},
					},
				},
			},
		},
	}
	second := StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: timestamp,
							LatencyMs: 2500,
							Source:    "user@example.com",
							AuthIndex: "0",
							Tokens: TokenStats{
								InputTokens:  10,
								OutputTokens: 20,
								TotalTokens:  30,
							},
						}},
					},
				},
			},
		},
	}

	result := stats.MergeSnapshot(first)
	if result.Added != 1 || result.Skipped != 0 {
		t.Fatalf("first merge = %+v, want added=1 skipped=0", result)
	}

	result = stats.MergeSnapshot(second)
	if result.Added != 0 || result.Skipped != 1 {
		t.Fatalf("second merge = %+v, want added=0 skipped=1", result)
	}

	snapshot := stats.Snapshot()
	details := snapshot.APIs["test-key"].Models["gpt-5.4"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
}
