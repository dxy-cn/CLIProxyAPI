package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/apikeys"
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

func (s *PostgresStore) UpsertAPIKeyRecord(ctx context.Context, record apikeys.Record) ([]apikeys.Record, error) {
	record = apikeys.NormalizeRecord(record)
	if record.APIKey == "" {
		return nil, fmt.Errorf("postgres store: api key is required")
	}
	if record.ID > 0 {
		query := fmt.Sprintf(`
			UPDATE %s
			SET api_key = $1, name = $2, auth_identity = $3, tags = $4::jsonb, updated_time = NOW()
			WHERE id = $5
		`, s.fullTableName(defaultAPIKeyTable))
		result, err := s.db.ExecContext(ctx, query, record.APIKey, record.Name, record.AuthIdentity, apiKeyTagsJSON(record.Tags), record.ID)
		if err != nil {
			return nil, fmt.Errorf("postgres store: update api key: %w", err)
		}
		if affected, errRows := result.RowsAffected(); errRows == nil && affected > 0 {
			return s.ListAPIKeyRecords(ctx)
		}
	}
	query := fmt.Sprintf(`
		INSERT INTO %s (api_key, name, auth_identity, tags)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (api_key) DO UPDATE
		SET name = EXCLUDED.name,
		    auth_identity = EXCLUDED.auth_identity,
		    tags = EXCLUDED.tags,
		    updated_time = NOW()
	`, s.fullTableName(defaultAPIKeyTable))
	if _, err := s.db.ExecContext(ctx, query, record.APIKey, record.Name, record.AuthIdentity, apiKeyTagsJSON(record.Tags)); err != nil {
		return nil, fmt.Errorf("postgres store: upsert api key: %w", err)
	}
	return s.ListAPIKeyRecords(ctx)
}

func (s *PostgresStore) DeleteAPIKeyRecord(ctx context.Context, record apikeys.Record) ([]apikeys.Record, error) {
	record = apikeys.NormalizeRecord(record)
	switch {
	case record.ID > 0:
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = $1", s.fullTableName(defaultAPIKeyTable)), record.ID); err != nil {
			return nil, fmt.Errorf("postgres store: delete api key by id: %w", err)
		}
	case record.APIKey != "":
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE api_key = $1", s.fullTableName(defaultAPIKeyTable)), record.APIKey); err != nil {
			return nil, fmt.Errorf("postgres store: delete api key: %w", err)
		}
	default:
		return nil, fmt.Errorf("postgres store: api key id or value is required")
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
