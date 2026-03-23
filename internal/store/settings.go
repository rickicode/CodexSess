package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	row := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key=? LIMIT 1`, strings.TrimSpace(key))
	var value string
	if err := row.Scan(&value); err != nil {
		return "", err
	}
	return value, nil
}

func (s *Store) SetSetting(ctx context.Context, key string, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.execWithRetry(ctx, `
		INSERT INTO app_settings(key, value, updated_at)
		VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET
			value=excluded.value,
			updated_at=excluded.updated_at
	`, strings.TrimSpace(key), value, now)
	return err
}

func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	_, err := s.execWithRetry(ctx, `DELETE FROM app_settings WHERE key=?`, strings.TrimSpace(key))
	return err
}

func (s *Store) ListSettingsByPrefix(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM app_settings WHERE key LIKE ?`, strings.TrimSpace(prefix)+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[strings.TrimSpace(key)] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) MustGetSetting(ctx context.Context, key string) (string, bool, error) {
	value, err := s.GetSetting(ctx, key)
	if err == nil {
		return value, true, nil
	}
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return "", false, err
}
