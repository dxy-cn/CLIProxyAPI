package usage

import (
	"context"
	"fmt"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

type fakeUsageStore struct {
	stats       AggregatedStats
	insertBatch func(context.Context, []UsageRecord) (int64, int64, error)
}

func (s *fakeUsageStore) Insert(context.Context, UsageRecord) error { return nil }

func (s *fakeUsageStore) InsertBatch(ctx context.Context, records []UsageRecord) (int64, int64, error) {
	if s.insertBatch != nil {
		return s.insertBatch(ctx, records)
	}
	return 0, 0, nil
}

func (s *fakeUsageStore) GetAggregatedStats(context.Context) (AggregatedStats, error) {
	return s.stats, nil
}

func (s *fakeUsageStore) GetDetails(context.Context, int, int) ([]DetailRecord, error) {
	return nil, nil
}

func (s *fakeUsageStore) DeleteOldRecords(context.Context, int) (int64, error) {
	return 0, nil
}

func (s *fakeUsageStore) EnsureSchema(context.Context) error { return nil }

func (s *fakeUsageStore) Close() error { return nil }

func TestGetCombinedSnapshot_StoreOnlySnapshotIgnoresMemory(t *testing.T) {
	oldStats := defaultRequestStatistics
	defer func() {
		defaultRequestStatistics = oldStats
	}()
	defaultRequestStatistics = NewRequestStatistics()
	SetStatisticsEnabled(true)

	defaultRequestStatistics.Record(context.Background(), coreusage.Record{
		APIKey:      "mem-api",
		Model:       "mem-model",
		RequestedAt: time.Now(),
		Detail: coreusage.Detail{
			TotalTokens: 99,
		},
	})

	now := time.Now().Add(-time.Hour)
	dbStats := AggregatedStats{
		TotalRequests: 3,
		SuccessCount:  2,
		FailureCount:  1,
		TotalTokens:   30,
		APIs: map[string]APIStats{
			"db-api": {
				TotalRequests: 3,
				TotalTokens:   30,
				Models: map[string]ModelStats{
					"db-model": {TotalRequests: 3, TotalTokens: 30},
				},
			},
		},
		RequestsByDay:  map[string]int64{"2026-02-07": 3},
		RequestsByHour: map[string]int64{"10": 3},
		TokensByDay:    map[string]int64{"2026-02-07": 30},
		TokensByHour:   map[string]int64{"10": 30},
		Details: []DetailRecord{
			{
				APIKey:      "db-api",
				Model:       "db-model",
				Source:      "db-source",
				AuthIndex:   "0",
				Failed:      false,
				RequestedAt: now,
				TotalTokens: 10,
			},
		},
	}

	plugin := &DatabasePlugin{
		store:             &fakeUsageStore{stats: dbStats},
		storeOnlySnapshot: true,
	}

	snapshot := plugin.GetCombinedSnapshot()
	if snapshot.TotalRequests != dbStats.TotalRequests {
		t.Fatalf("unexpected total requests: got %d want %d", snapshot.TotalRequests, dbStats.TotalRequests)
	}
	if snapshot.TotalTokens != dbStats.TotalTokens {
		t.Fatalf("unexpected total tokens: got %d want %d", snapshot.TotalTokens, dbStats.TotalTokens)
	}
	if _, exists := snapshot.APIs["mem-api"]; exists {
		t.Fatalf("memory api should not be merged when storeOnlySnapshot is true")
	}
	if _, exists := snapshot.APIs["db-api"]; !exists {
		t.Fatalf("db api missing in snapshot")
	}
}

func TestDatabasePluginHandleUsage_NormalizesTotalTokens(t *testing.T) {
	plugin := &DatabasePlugin{
		store:  &fakeUsageStore{},
		buffer: make([]UsageRecord, 0, 1),
	}

	plugin.HandleUsage(context.Background(), coreusage.Record{
		APIKey:      "api-a",
		Model:       "model-a",
		Source:      "source-a",
		AuthIndex:   "burn",
		RequestedAt: time.Now(),
		Detail: coreusage.Detail{
			InputTokens:  71,
			OutputTokens: 1,
			CachedTokens: 71,
			TotalTokens:  0,
		},
	})

	if len(plugin.buffer) != 1 {
		t.Fatalf("unexpected buffer size: got %d want 1", len(plugin.buffer))
	}
	if plugin.buffer[0].TotalTokens != 72 {
		t.Fatalf("unexpected normalized total tokens: got %d want 72", plugin.buffer[0].TotalTokens)
	}
}

func TestDatabasePluginHandleUsageDropsNewestWhenBufferHardLimitReached(t *testing.T) {
	plugin := &DatabasePlugin{
		store:   &fakeUsageStore{},
		buffer:  make([]UsageRecord, 0, defaultBufferSize),
		flushCh: make(chan struct{}, 1),
	}
	limit := defaultMaxBufferSize

	for i := 0; i < limit+1; i++ {
		plugin.HandleUsage(context.Background(), coreusage.Record{
			APIKey:      fmt.Sprintf("api-%d", i),
			Model:       fmt.Sprintf("model-%d", i),
			RequestedAt: time.Now(),
			Detail: coreusage.Detail{
				InputTokens: 1,
				TotalTokens: 1,
			},
		})
	}

	if len(plugin.buffer) != limit {
		t.Fatalf("buffer len = %d, want %d", len(plugin.buffer), limit)
	}
	if got := plugin.buffer[limit-1].Model; got != fmt.Sprintf("model-%d", limit-1) {
		t.Fatalf("last retained model = %q, want model-%d", got, limit-1)
	}
}

func TestDatabasePluginFlushUsesBoundedContext(t *testing.T) {
	sawDeadline := false
	store := &fakeUsageStore{
		insertBatch: func(ctx context.Context, records []UsageRecord) (int64, int64, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("InsertBatch context has no deadline")
			}
			sawDeadline = true
			return int64(len(records)), 0, nil
		},
	}
	plugin := &DatabasePlugin{
		store:  store,
		buffer: []UsageRecord{{APIKey: "api-a", Model: "model-a"}},
	}

	plugin.flush()

	if !sawDeadline {
		t.Fatal("InsertBatch was not called")
	}
}
