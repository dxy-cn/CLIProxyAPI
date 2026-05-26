package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeys"
)

const defaultAPIKeyTable = "api_key_store"

func (s *PostgresStore) ensureAPIKeySchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("postgres store: not initialized")
	}
	table := s.fullTableName(defaultAPIKeyTable)
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id BIGSERIAL PRIMARY KEY,
			api_key TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			auth_identity TEXT NOT NULL DEFAULT '',
			tags JSONB NOT NULL DEFAULT '[]'::jsonb,
			created_time TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_time TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (api_key)
		)
	`, table)); err != nil {
		return fmt.Errorf("postgres store: create api key table: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListAPIKeyRecords(ctx context.Context) ([]apikeys.Record, error) {
	query := fmt.Sprintf("SELECT id, api_key, name, auth_identity, COALESCE(tags, '[]'::jsonb), created_time, updated_time FROM %s ORDER BY id ASC", s.fullTableName(defaultAPIKeyTable))
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres store: list api keys: %w", err)
	}
	defer rows.Close()

	var records []apikeys.Record
	for rows.Next() {
		record, errScan := scanPostgresAPIKeyRecord(rows)
		if errScan != nil {
			return nil, errScan
		}
		records = append(records, record)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: iterate api keys: %w", err)
	}
	return records, nil
}

func (s *PostgresStore) ReplaceAPIKeyRecords(ctx context.Context, records []apikeys.Record) ([]apikeys.Record, error) {
	records = apikeys.NormalizeRecords(records)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("postgres store: begin api key replace: %w", err)
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", s.fullTableName(defaultAPIKeyTable))); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("postgres store: clear api keys: %w", err)
	}
	for _, record := range records {
		if _, err = insertPostgresAPIKeyRecordTx(ctx, tx, s.fullTableName(defaultAPIKeyTable), record); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("postgres store: commit api key replace: %w", err)
	}
	return s.ListAPIKeyRecords(ctx)
}

func insertPostgresAPIKeyRecordTx(ctx context.Context, tx *sql.Tx, table string, record apikeys.Record) (int64, error) {
	query := fmt.Sprintf(`
		INSERT INTO %s (api_key, name, auth_identity, tags)
		VALUES ($1, $2, $3, $4::jsonb)
		RETURNING id
	`, table)
	var id int64
	if err := tx.QueryRowContext(
		ctx,
		query,
		record.APIKey,
		record.Name,
		record.AuthIdentity,
		apiKeyTagsJSON(record.Tags),
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("postgres store: insert api key: %w", err)
	}
	return id, nil
}

type postgresAPIKeyScanner interface {
	Scan(dest ...any) error
}

func scanPostgresAPIKeyRecord(scanner postgresAPIKeyScanner) (apikeys.Record, error) {
	var (
		record      apikeys.Record
		tags        []byte
		createdTime time.Time
		updatedTime time.Time
	)
	if err := scanner.Scan(&record.ID, &record.APIKey, &record.Name, &record.AuthIdentity, &tags, &createdTime, &updatedTime); err != nil {
		return apikeys.Record{}, fmt.Errorf("postgres store: scan api key: %w", err)
	}
	record.Tags = parseAPIKeyTags(tags)
	record.CreatedTime = createdTime
	record.UpdatedTime = updatedTime
	return apikeys.NormalizeRecord(record), nil
}

func apiKeyTagsJSON(tags []string) string {
	data, err := json.Marshal(apikeys.NormalizeTags(tags))
	if err != nil {
		return "[]"
	}
	return string(data)
}

func parseAPIKeyTags(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	var tags []string
	if err := json.Unmarshal(data, &tags); err != nil {
		return nil
	}
	return apikeys.NormalizeTags(tags)
}
