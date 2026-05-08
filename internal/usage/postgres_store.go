package usage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// pgUsageStore implements UsageStore using PostgreSQL.
type pgUsageStore struct {
	db     *sql.DB
	schema string
}

func newPgUsageStore(ctx context.Context, dsn, schema string) (*pgUsageStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("usage store: open postgres: %w", err)
	}
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("usage store: ping postgres: %w", err)
	}
	store := &pgUsageStore{db: db, schema: strings.TrimSpace(schema)}
	if err = store.EnsureSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *pgUsageStore) fullTableName(name string) string {
	if s.schema == "" {
		return pgQuoteIdentifier(name)
	}
	return pgQuoteIdentifier(s.schema) + "." + pgQuoteIdentifier(name)
}

func (s *pgUsageStore) fullIndexName(name string) string {
	if s.schema == "" {
		return pgQuoteIdentifier(name)
	}
	return pgQuoteIdentifier(s.schema) + "." + pgQuoteIdentifier(name)
}

func (s *pgUsageStore) EnsureSchema(ctx context.Context) error {
	if s.schema != "" {
		query := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pgQuoteIdentifier(s.schema))
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("usage store: create schema: %w", err)
		}
	}
	table := s.fullTableName("usage_records")
	createTable := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id                     BIGSERIAL PRIMARY KEY,
			api_key                TEXT NOT NULL,
			model                  TEXT NOT NULL,
			source                 TEXT,
			auth_index             TEXT,
			failed                 INTEGER NOT NULL DEFAULT 0,
			requested_at           TIMESTAMPTZ NOT NULL,
			input_tokens           BIGINT NOT NULL DEFAULT 0,
			output_tokens          BIGINT NOT NULL DEFAULT 0,
			reasoning_tokens       BIGINT NOT NULL DEFAULT 0,
			cached_tokens          BIGINT NOT NULL DEFAULT 0,
			total_tokens           BIGINT NOT NULL DEFAULT 0,
			first_token_latency_ms BIGINT NOT NULL DEFAULT 0
		)
	`, table)
	if _, err := s.db.ExecContext(ctx, createTable); err != nil {
		return fmt.Errorf("usage store: create table: %w", err)
	}

	// Create indexes for common query patterns
	indexes := []string{
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_usage_requested_at_id ON %s(requested_at DESC, id DESC)", table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_usage_api_model ON %s(api_key, model)", table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_usage_failed ON %s(failed) WHERE failed = 1", table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_usage_failed_requested_source ON %s(requested_at DESC, source) WHERE failed = 1", table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_usage_source_requested_id ON %s(source, requested_at DESC, id DESC)", table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_usage_source_model_requested_id ON %s(source, model, requested_at DESC, id DESC)", table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_usage_source_norm_requested ON %s((COALESCE(NULLIF(source, ''), 'unknown')), requested_at DESC)", table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_usage_source_norm_model_requested ON %s((COALESCE(NULLIF(source, ''), 'unknown')), model, requested_at DESC)", table),
	}
	for _, idx := range indexes {
		if _, err := s.db.ExecContext(ctx, idx); err != nil {
			return fmt.Errorf("usage store: create index: %w", err)
		}
	}
	legacyIndexes := []string{
		fmt.Sprintf("DROP INDEX IF EXISTS %s", s.fullIndexName("idx_usage_requested_at")),
		fmt.Sprintf("DROP INDEX IF EXISTS %s", s.fullIndexName("idx_usage_api_key")),
	}
	for _, dropStmt := range legacyIndexes {
		if _, err := s.db.ExecContext(ctx, dropStmt); err != nil {
			return fmt.Errorf("usage store: drop legacy index: %w", err)
		}
	}

	// Migration: add method/path columns for existing databases
	pgMigrations := []string{
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS method TEXT NOT NULL DEFAULT ''", table),
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS path TEXT NOT NULL DEFAULT ''", table),
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS latency_ms BIGINT NOT NULL DEFAULT 0", table),
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS first_token_latency_ms BIGINT NOT NULL DEFAULT 0", table),
	}
	for _, m := range pgMigrations {
		if _, err := s.db.ExecContext(ctx, m); err != nil {
			return fmt.Errorf("usage store: migration: %w", err)
		}
	}
	postMigrationIndexes := []string{
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_usage_method_requested_at ON %s(method, requested_at DESC)", table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_usage_path_requested_at ON %s(path, requested_at DESC)", table),
	}
	for _, idx := range postMigrationIndexes {
		if _, err := s.db.ExecContext(ctx, idx); err != nil {
			return fmt.Errorf("usage store: create post migration index: %w", err)
		}
	}

	return nil
}

func (s *pgUsageStore) Insert(ctx context.Context, record UsageRecord) error {
	table := s.fullTableName("usage_records")
	failed := 0
	if record.Failed {
		failed = 1
	}
	query := fmt.Sprintf(`
		INSERT INTO %s (api_key, model, source, auth_index, failed, requested_at,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens,
			method, path, latency_ms, first_token_latency_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`, table)
	_, err := s.db.ExecContext(ctx, query,
		record.APIKey,
		record.Model,
		record.Source,
		record.AuthIndex,
		failed,
		record.RequestedAt,
		record.InputTokens,
		record.OutputTokens,
		record.ReasoningTokens,
		record.CachedTokens,
		record.TotalTokens,
		record.Method,
		record.Path,
		record.LatencyMs,
		record.FirstTokenLatencyMs,
	)
	if err != nil {
		return fmt.Errorf("usage store: insert record: %w", err)
	}
	return nil
}

func (s *pgUsageStore) InsertBatch(ctx context.Context, records []UsageRecord) (added, skipped int64, err error) {
	if len(records) == 0 {
		return 0, 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("usage store: begin tx: %w", err)
	}
	defer tx.Rollback()

	table := s.fullTableName("usage_records")
	query := fmt.Sprintf(`
		INSERT INTO %s (api_key, model, source, auth_index, failed, requested_at,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens,
			method, path, latency_ms, first_token_latency_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`, table)

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return 0, 0, fmt.Errorf("usage store: prepare stmt: %w", err)
	}
	defer stmt.Close()

	for _, record := range records {
		failed := 0
		if record.Failed {
			failed = 1
		}
		_, execErr := stmt.ExecContext(ctx,
			record.APIKey,
			record.Model,
			record.Source,
			record.AuthIndex,
			failed,
			record.RequestedAt,
			record.InputTokens,
			record.OutputTokens,
			record.ReasoningTokens,
			record.CachedTokens,
			record.TotalTokens,
			record.Method,
			record.Path,
			record.LatencyMs,
			record.FirstTokenLatencyMs,
		)
		if execErr != nil {
			skipped++
			continue
		}
		added++
	}

	if err = tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("usage store: commit tx: %w", err)
	}
	return added, skipped, nil
}

func (s *pgUsageStore) ListRecordsAfterID(ctx context.Context, afterID int64, limit int) ([]UsageRecord, int64, error) {
	if limit <= 0 {
		limit = defaultMirrorSyncBatchSize
	}
	if limit > 50000 {
		limit = 50000
	}
	if afterID < 0 {
		afterID = 0
	}

	table := s.fullTableName("usage_records")
	query := fmt.Sprintf(`
		SELECT id, api_key, model, source, auth_index, failed, requested_at,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens,
			method, path, latency_ms, first_token_latency_ms
		FROM %s
		WHERE id > $1
		ORDER BY id ASC
		LIMIT $2
	`, table)

	rows, err := s.db.QueryContext(ctx, query, afterID, limit)
	if err != nil {
		return nil, afterID, fmt.Errorf("usage store: list records after id: %w", err)
	}
	defer rows.Close()

	records := make([]UsageRecord, 0, limit)
	lastID := afterID
	for rows.Next() {
		var (
			id     int64
			failed int
			record UsageRecord
		)
		if err = rows.Scan(
			&id,
			&record.APIKey,
			&record.Model,
			&record.Source,
			&record.AuthIndex,
			&failed,
			&record.RequestedAt,
			&record.InputTokens,
			&record.OutputTokens,
			&record.ReasoningTokens,
			&record.CachedTokens,
			&record.TotalTokens,
			&record.Method,
			&record.Path,
			&record.LatencyMs,
			&record.FirstTokenLatencyMs,
		); err != nil {
			return nil, afterID, fmt.Errorf("usage store: scan list records after id: %w", err)
		}
		record.Failed = failed != 0
		records = append(records, record)
		lastID = id
	}
	if err = rows.Err(); err != nil {
		return nil, afterID, fmt.Errorf("usage store: iterate list records after id: %w", err)
	}

	return records, lastID, nil
}

func (s *pgUsageStore) GetAggregatedStats(ctx context.Context) (AggregatedStats, error) {
	stats := AggregatedStats{
		APIs:           make(map[string]APIStats),
		RequestsByDay:  make(map[string]int64),
		RequestsByHour: make(map[string]int64),
		TokensByDay:    make(map[string]int64),
		TokensByHour:   make(map[string]int64),
	}
	table := s.fullTableName("usage_records")

	// Total stats
	queryTotal := fmt.Sprintf(`
		SELECT COUNT(*),
			SUM(CASE WHEN failed=0 THEN 1 ELSE 0 END),
			SUM(CASE WHEN failed=1 THEN 1 ELSE 0 END),
			COALESCE(SUM(total_tokens), 0)
		FROM %s
	`, table)
	if err := s.db.QueryRowContext(ctx, queryTotal).Scan(
		&stats.TotalRequests, &stats.SuccessCount, &stats.FailureCount, &stats.TotalTokens,
	); err != nil && err != sql.ErrNoRows {
		return stats, fmt.Errorf("usage store: query total stats: %w", err)
	}

	// By API key
	queryAPI := fmt.Sprintf(`
		SELECT api_key, COUNT(*), COALESCE(SUM(total_tokens), 0)
		FROM %s GROUP BY api_key
	`, table)
	rows, err := s.db.QueryContext(ctx, queryAPI)
	if err != nil {
		return stats, fmt.Errorf("usage store: query api stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var as APIStats
		if err = rows.Scan(&key, &as.TotalRequests, &as.TotalTokens); err != nil {
			return stats, fmt.Errorf("usage store: scan api stats: %w", err)
		}
		as.Models = make(map[string]ModelStats)
		stats.APIs[key] = as
	}

	// By API key + Model
	queryModel := fmt.Sprintf(`
		SELECT api_key, model, COUNT(*), COALESCE(SUM(total_tokens), 0)
		FROM %s GROUP BY api_key, model
	`, table)
	rows, err = s.db.QueryContext(ctx, queryModel)
	if err != nil {
		return stats, fmt.Errorf("usage store: query model stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var apiKey, model string
		var ms ModelStats
		if err = rows.Scan(&apiKey, &model, &ms.TotalRequests, &ms.TotalTokens); err != nil {
			return stats, fmt.Errorf("usage store: scan model stats: %w", err)
		}
		if api, ok := stats.APIs[apiKey]; ok {
			api.Models[model] = ms
			stats.APIs[apiKey] = api
		}
	}

	// By day
	queryDay := fmt.Sprintf(`
		SELECT TO_CHAR(requested_at, 'YYYY-MM-DD'), COUNT(*), COALESCE(SUM(total_tokens), 0)
		FROM %s GROUP BY TO_CHAR(requested_at, 'YYYY-MM-DD')
	`, table)
	rows, err = s.db.QueryContext(ctx, queryDay)
	if err != nil {
		return stats, fmt.Errorf("usage store: query day stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var day string
		var count, tokens int64
		if err = rows.Scan(&day, &count, &tokens); err != nil {
			return stats, fmt.Errorf("usage store: scan day stats: %w", err)
		}
		stats.RequestsByDay[day] = count
		stats.TokensByDay[day] = tokens
	}

	// By hour
	queryHour := fmt.Sprintf(`
		SELECT TO_CHAR(requested_at, 'HH24'), COUNT(*), COALESCE(SUM(total_tokens), 0)
		FROM %s GROUP BY TO_CHAR(requested_at, 'HH24')
	`, table)
	rows, err = s.db.QueryContext(ctx, queryHour)
	if err != nil {
		return stats, fmt.Errorf("usage store: query hour stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var hour string
		var count, tokens int64
		if err = rows.Scan(&hour, &count, &tokens); err != nil {
			return stats, fmt.Errorf("usage store: scan hour stats: %w", err)
		}
		stats.RequestsByHour[hour] = count
		stats.TokensByHour[hour] = tokens
	}

	// DetailCount only — full Details are available via GetDetails (paginated).
	stats.DetailCount = stats.TotalRequests

	return stats, nil
}

func (s *pgUsageStore) GetDetails(ctx context.Context, offset, limit int) ([]DetailRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}

	table := s.fullTableName("usage_records")
	query := fmt.Sprintf(`
		SELECT api_key, model, source, auth_index, failed, requested_at,
			input_tokens, output_tokens, reasoning_tokens, cached_tokens, total_tokens
		FROM %s ORDER BY requested_at DESC
		LIMIT $1 OFFSET $2
	`, table)

	rows, err := s.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("usage store: query details: %w", err)
	}
	defer rows.Close()

	var details []DetailRecord
	for rows.Next() {
		var detail DetailRecord
		var failed int
		if err = rows.Scan(
			&detail.APIKey, &detail.Model, &detail.Source, &detail.AuthIndex,
			&failed, &detail.RequestedAt,
			&detail.InputTokens, &detail.OutputTokens, &detail.ReasoningTokens,
			&detail.CachedTokens, &detail.TotalTokens,
		); err != nil {
			return nil, fmt.Errorf("usage store: scan detail: %w", err)
		}
		detail.Failed = (failed != 0)
		details = append(details, detail)
	}

	return details, nil
}

func (s *pgUsageStore) DeleteOldRecords(ctx context.Context, retentionDays int) (deleted int64, err error) {
	if retentionDays <= 0 {
		return 0, nil
	}
	table := s.fullTableName("usage_records")
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	query := fmt.Sprintf("DELETE FROM %s WHERE requested_at < $1", table)
	result, err := s.db.ExecContext(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("usage store: delete old records: %w", err)
	}
	deleted, _ = result.RowsAffected()
	return deleted, nil
}

func (s *pgUsageStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func pgQuoteIdentifier(identifier string) string {
	replaced := strings.ReplaceAll(identifier, "\"", "\"\"")
	return "\"" + replaced + "\""
}
