package usage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveLocalUsageDBPath(t *testing.T) {
	authDir := filepath.Join(t.TempDir(), "auth")

	t.Setenv("MYSQLSTORE_LOCAL_PATH", filepath.Join(t.TempDir(), "mysqllocal"))
	got := resolveLocalUsageDBPath(authDir)
	want := filepath.Join(getEnvOrFatal(t, "MYSQLSTORE_LOCAL_PATH"), defaultLocalUsageFileName)
	if got != want {
		t.Fatalf("unexpected local db path: got %q want %q", got, want)
	}

	t.Setenv("MYSQLSTORE_LOCAL_PATH", filepath.Join(t.TempDir(), "custom.db"))
	got = resolveLocalUsageDBPath(authDir)
	want = getEnvOrFatal(t, "MYSQLSTORE_LOCAL_PATH")
	if got != want {
		t.Fatalf("unexpected db file path: got %q want %q", got, want)
	}

	t.Setenv("MYSQLSTORE_LOCAL_PATH", "")
	got = resolveLocalUsageDBPath(authDir)
	want = filepath.Join(authDir, defaultLocalUsageFileName)
	if got != want {
		t.Fatalf("unexpected fallback db path: got %q want %q", got, want)
	}
}

func TestNormalizeMySQLDSNForUsageEnablesParseTime(t *testing.T) {
	got, err := normalizeMySQLDSN("user:pass@tcp(localhost:3306)/cliproxy?charset=utf8mb4")
	if err != nil {
		t.Fatalf("normalizeMySQLDSN failed: %v", err)
	}
	if !strings.Contains(got, "parseTime=true") {
		t.Fatalf("expected parseTime=true in normalized DSN, got %q", got)
	}
	if strings.Contains(got, "parseTime=false") {
		t.Fatalf("normalized DSN must not keep parseTime=false: %q", got)
	}
}

func TestSQLiteUsageStoreReset(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "sqlite", "usage.db")

	store, err := newSQLiteUsageStoreAtPath(dbPath)
	if err != nil {
		t.Fatalf("newSQLiteUsageStoreAtPath failed: %v", err)
	}
	defer store.Close()

	err = store.Insert(ctx, UsageRecord{
		APIKey:      "api-1",
		Model:       "model-1",
		Source:      "source-1",
		AuthIndex:   "0",
		Failed:      false,
		RequestedAt: time.Now(),
		TotalTokens: 10,
	})
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	details, err := store.GetDetails(ctx, 0, 10)
	if err != nil {
		t.Fatalf("GetDetails before reset failed: %v", err)
	}
	if len(details) != 1 {
		t.Fatalf("unexpected detail count before reset: got %d want 1", len(details))
	}

	if err = store.Reset(ctx); err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	details, err = store.GetDetails(ctx, 0, 10)
	if err != nil {
		t.Fatalf("GetDetails after reset failed: %v", err)
	}
	if len(details) != 0 {
		t.Fatalf("unexpected detail count after reset: got %d want 0", len(details))
	}
}

func TestSQLiteUsageStoreAggregatedStatsClosesRowsBetweenQueries(t *testing.T) {
	store, err := newSQLiteUsageStoreAtPath(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatalf("newSQLiteUsageStoreAtPath failed: %v", err)
	}
	defer store.Close()
	store.db.SetMaxOpenConns(1)

	_, _, err = store.InsertBatch(context.Background(), []UsageRecord{
		{APIKey: "api-1", Model: "model-a", Source: "source-a", RequestedAt: time.Now(), TotalTokens: 10},
	})
	if err != nil {
		t.Fatalf("InsertBatch failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = store.GetAggregatedStats(ctx); err != nil {
		t.Fatalf("GetAggregatedStats with one DB connection failed: %v", err)
	}
}

func TestSQLiteUsageStoreEnsureSchemaSkipsCoveredSingleIndexes(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "sqlite", "usage.db")

	store, err := newSQLiteUsageStoreAtPath(dbPath)
	if err != nil {
		t.Fatalf("newSQLiteUsageStoreAtPath failed: %v", err)
	}
	defer store.Close()

	names, err := sqliteIndexNameSet(ctx, store, "usage_records")
	if err != nil {
		t.Fatalf("sqliteIndexNameSet failed: %v", err)
	}

	if _, ok := names["idx_usage_requested_at"]; ok {
		t.Fatalf("unexpected redundant index created: idx_usage_requested_at")
	}
	if _, ok := names["idx_usage_api_key"]; ok {
		t.Fatalf("unexpected redundant index created: idx_usage_api_key")
	}
	if _, ok := names["idx_usage_requested_at_id"]; !ok {
		t.Fatalf("expected composite index missing: idx_usage_requested_at_id")
	}
	if _, ok := names["idx_usage_api_model"]; !ok {
		t.Fatalf("expected composite index missing: idx_usage_api_model")
	}
}

func TestSQLiteUsageStoreEnsureSchemaDropsLegacyCoveredSingleIndexes(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "sqlite", "usage.db")

	store, err := newSQLiteUsageStoreAtPath(dbPath)
	if err != nil {
		t.Fatalf("newSQLiteUsageStoreAtPath failed: %v", err)
	}
	defer store.Close()

	legacyIndexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_usage_requested_at ON usage_records(requested_at DESC)",
		"CREATE INDEX IF NOT EXISTS idx_usage_api_key ON usage_records(api_key)",
	}
	for _, query := range legacyIndexes {
		if _, err = store.db.ExecContext(ctx, query); err != nil {
			t.Fatalf("create legacy index failed: %v", err)
		}
	}

	if err = store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema failed: %v", err)
	}

	names, err := sqliteIndexNameSet(ctx, store, "usage_records")
	if err != nil {
		t.Fatalf("sqliteIndexNameSet failed: %v", err)
	}

	if _, ok := names["idx_usage_requested_at"]; ok {
		t.Fatalf("legacy redundant index should be dropped: idx_usage_requested_at")
	}
	if _, ok := names["idx_usage_api_key"]; ok {
		t.Fatalf("legacy redundant index should be dropped: idx_usage_api_key")
	}
}

func sqliteIndexNameSet(ctx context.Context, store *sqliteUsageStore, tableName string) (map[string]struct{}, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT name
		FROM sqlite_master
		WHERE type='index' AND tbl_name = ?
	`, tableName)
	if err != nil {
		return nil, fmt.Errorf("query sqlite indexes: %w", err)
	}
	defer rows.Close()

	names := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan sqlite index name: %w", err)
		}
		names[name] = struct{}{}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite index names: %w", err)
	}
	return names, nil
}

func getEnvOrFatal(t *testing.T, key string) string {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		t.Fatalf("expected env %q to be set", key)
	}
	return value
}
