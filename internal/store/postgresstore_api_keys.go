package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/apikeys"
)

func (s *PostgresStore) ensureAPIKeySchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("postgres store: not initialized")
	}
	table := s.fullTableName(s.cfg.APIKeyTable)
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

func (s *PostgresStore) countAPIKeyRecords(ctx context.Context) (int, error) {
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", s.fullTableName(s.cfg.APIKeyTable))
	var count int
	if err := s.db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, fmt.Errorf("postgres store: count api keys: %w", err)
	}
	return count, nil
}

func (s *PostgresStore) ListAPIKeyRecords(ctx context.Context) ([]apikeys.Record, error) {
	query := fmt.Sprintf("SELECT id, api_key, name, auth_identity, COALESCE(tags, '[]'::jsonb), created_time, updated_time FROM %s ORDER BY id ASC", s.fullTableName(s.cfg.APIKeyTable))
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
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", s.fullTableName(s.cfg.APIKeyTable))); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("postgres store: clear api keys: %w", err)
	}
	for _, record := range records {
		if _, err = s.insertAPIKeyRecordTx(ctx, tx, record); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("postgres store: commit api key replace: %w", err)
	}
	return s.ListAPIKeyRecords(ctx)
}

func (s *PostgresStore) CreateAPIKeyRecord(ctx context.Context, record apikeys.Record) (apikeys.Record, error) {
	record = apikeys.NormalizeRecord(record)
	if record.APIKey == "" {
		return apikeys.Record{}, fmt.Errorf("postgres store: api key is required")
	}
	query := fmt.Sprintf(`
		INSERT INTO %s (api_key, name, auth_identity, tags)
		VALUES ($1, $2, $3, $4::jsonb)
		RETURNING id, api_key, name, auth_identity, COALESCE(tags, '[]'::jsonb), created_time, updated_time
	`, s.fullTableName(s.cfg.APIKeyTable))
	row := s.db.QueryRowContext(ctx, query, record.APIKey, record.Name, record.AuthIdentity, apiKeyTagsJSON(record.Tags))
	created, err := scanPostgresAPIKeyRecord(row)
	if err != nil {
		if isPostgresUniqueViolation(err) {
			return apikeys.Record{}, apikeys.ErrDuplicateAPIKey
		}
		return apikeys.Record{}, err
	}
	return created, nil
}

func (s *PostgresStore) UpsertAPIKeyRecord(ctx context.Context, record apikeys.Record) (apikeys.Record, error) {
	record = apikeys.NormalizeRecord(record)
	if record.APIKey == "" {
		return apikeys.Record{}, fmt.Errorf("postgres store: api key is required")
	}
	if record.ID > 0 {
		updated, err := s.updateAPIKeyRecordByID(ctx, record)
		if err == nil {
			return updated, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return apikeys.Record{}, err
		}
	}
	return s.insertOrUpdateAPIKeyRecord(ctx, record)
}

func (s *PostgresStore) DeleteAPIKeyRecord(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("postgres store: api key id is required")
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE id = $1", s.fullTableName(s.cfg.APIKeyTable))
	if _, err := s.db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("postgres store: delete api key: %w", err)
	}
	return nil
}

func (s *PostgresStore) DeleteAPIKeyRecordByKey(ctx context.Context, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("postgres store: api key is required")
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE api_key = $1", s.fullTableName(s.cfg.APIKeyTable))
	if _, err := s.db.ExecContext(ctx, query, key); err != nil {
		return fmt.Errorf("postgres store: delete api key by key: %w", err)
	}
	return nil
}

func (s *PostgresStore) insertOrUpdateAPIKeyRecord(ctx context.Context, record apikeys.Record) (apikeys.Record, error) {
	query := fmt.Sprintf(`
		INSERT INTO %s (api_key, name, auth_identity, tags)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (api_key) DO UPDATE SET
			api_key = EXCLUDED.api_key,
			name = EXCLUDED.name,
			auth_identity = EXCLUDED.auth_identity,
			tags = EXCLUDED.tags,
			updated_time = NOW()
		RETURNING id, api_key, name, auth_identity, COALESCE(tags, '[]'::jsonb), created_time, updated_time
	`, s.fullTableName(s.cfg.APIKeyTable))
	row := s.db.QueryRowContext(ctx, query, record.APIKey, record.Name, record.AuthIdentity, apiKeyTagsJSON(record.Tags))
	return scanPostgresAPIKeyRecord(row)
}

func (s *PostgresStore) insertAPIKeyRecordTx(ctx context.Context, tx *sql.Tx, record apikeys.Record) (int64, error) {
	query := fmt.Sprintf(`
		INSERT INTO %s (api_key, name, auth_identity, tags)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (api_key) DO UPDATE SET
			api_key = EXCLUDED.api_key,
			name = EXCLUDED.name,
			auth_identity = EXCLUDED.auth_identity,
			tags = EXCLUDED.tags,
			updated_time = NOW()
		RETURNING id
	`, s.fullTableName(s.cfg.APIKeyTable))
	var id int64
	if err := tx.QueryRowContext(ctx, query, record.APIKey, record.Name, record.AuthIdentity, apiKeyTagsJSON(record.Tags)).Scan(&id); err != nil {
		return 0, fmt.Errorf("postgres store: upsert api key: %w", err)
	}
	return id, nil
}

func (s *PostgresStore) updateAPIKeyRecordByID(ctx context.Context, record apikeys.Record) (apikeys.Record, error) {
	query := fmt.Sprintf(`
		UPDATE %s
		SET api_key = $1, name = $2, auth_identity = $3, tags = $4::jsonb, updated_time = NOW()
		WHERE id = $5
		RETURNING id, api_key, name, auth_identity, COALESCE(tags, '[]'::jsonb), created_time, updated_time
	`, s.fullTableName(s.cfg.APIKeyTable))
	row := s.db.QueryRowContext(ctx, query, record.APIKey, record.Name, record.AuthIdentity, apiKeyTagsJSON(record.Tags), record.ID)
	return scanPostgresAPIKeyRecord(row)
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

func isPostgresUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "SQLSTATE 23505") ||
		strings.Contains(err.Error(), "duplicate key value violates unique constraint")
}
