package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *Store) CreateZoAPIKey(ctx context.Context, key ZoAPIKey) (ZoAPIKey, error) {
	if strings.TrimSpace(key.ID) == "" {
		return ZoAPIKey{}, fmt.Errorf("zo api key id is required")
	}
	if strings.TrimSpace(key.Name) == "" {
		key.Name = key.ID
	}
	if strings.TrimSpace(key.Token) == "" {
		return ZoAPIKey{}, fmt.Errorf("zo api key secret is required")
	}
	now := time.Now().UTC()
	if key.CreatedAt.IsZero() {
		key.CreatedAt = now
	}
	if key.UpdatedAt.IsZero() {
		key.UpdatedAt = now
	}
	if key.LastUsedAt.IsZero() {
		key.LastUsedAt = now
	}
	convUpdated := ""
	if key.ConversationUpdatedAt != nil {
		convUpdated = key.ConversationUpdatedAt.UTC().Format(time.RFC3339)
	}
	_, err := s.execWithRetry(ctx, `
		INSERT INTO zo_api_keys(id, name, key_secret, active, conversation_id, conversation_updated_at, created_at, updated_at, last_used_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, key.ID, key.Name, key.Token, boolToInt(key.Active), strings.TrimSpace(key.ConversationID), convUpdated, key.CreatedAt.Format(time.RFC3339), key.UpdatedAt.Format(time.RFC3339), key.LastUsedAt.Format(time.RFC3339))
	if err != nil {
		return ZoAPIKey{}, err
	}
	return s.GetZoAPIKey(ctx, key.ID)
}

func (s *Store) UpdateZoAPIKey(ctx context.Context, key ZoAPIKey) (ZoAPIKey, error) {
	if strings.TrimSpace(key.ID) == "" {
		return ZoAPIKey{}, fmt.Errorf("zo api key id is required")
	}
	if strings.TrimSpace(key.Name) == "" {
		key.Name = key.ID
	}
	if strings.TrimSpace(key.Token) == "" {
		return ZoAPIKey{}, fmt.Errorf("zo api key secret is required")
	}
	if key.UpdatedAt.IsZero() {
		key.UpdatedAt = time.Now().UTC()
	}
	if key.LastUsedAt.IsZero() {
		existing, err := s.GetZoAPIKey(ctx, key.ID)
		if err != nil {
			return ZoAPIKey{}, err
		}
		key.LastUsedAt = existing.LastUsedAt
	}
	_, err := s.execWithRetry(ctx, `
		UPDATE zo_api_keys
		SET name=?, key_secret=?, active=?, updated_at=?, last_used_at=?
		WHERE id=?
	`, key.Name, key.Token, boolToInt(key.Active), key.UpdatedAt.Format(time.RFC3339), key.LastUsedAt.Format(time.RFC3339), key.ID)
	if err != nil {
		return ZoAPIKey{}, err
	}
	return s.GetZoAPIKey(ctx, key.ID)
}

func (s *Store) DeleteZoAPIKey(ctx context.Context, id string) error {
	keyID := strings.TrimSpace(id)
	if keyID == "" {
		return fmt.Errorf("zo api key id is required")
	}
	tx, err := s.beginTxWithRetry(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := txExecWithRetry(ctx, tx, `DELETE FROM zo_api_key_usage WHERE key_id=?`, keyID); err != nil {
		return err
	}
	res, err := txExecWithRetry(ctx, tx, `DELETE FROM zo_api_keys WHERE id=?`, keyID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("zo api key not found")
	}
	return tx.Commit()
}

func (s *Store) GetZoAPIKey(ctx context.Context, id string) (ZoAPIKey, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, key_secret, active, conversation_id, conversation_updated_at, created_at, updated_at, last_used_at
		FROM zo_api_keys
		WHERE id=?
		LIMIT 1
	`, strings.TrimSpace(id))
	return scanZoAPIKey(row)
}

func (s *Store) ActiveZoAPIKey(ctx context.Context) (ZoAPIKey, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, key_secret, active, conversation_id, conversation_updated_at, created_at, updated_at, last_used_at
		FROM zo_api_keys
		WHERE active=1
		LIMIT 1
	`)
	return scanZoAPIKey(row)
}

func (s *Store) ListZoAPIKeys(ctx context.Context) ([]ZoAPIKey, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, key_secret, active, conversation_id, conversation_updated_at, created_at, updated_at, last_used_at
		FROM zo_api_keys
		ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ZoAPIKey
	for rows.Next() {
		var key ZoAPIKey
		var active int
		var createdAt, updatedAt, lastUsedAt string
		var conversationUpdatedAt sql.NullString
		if err := rows.Scan(&key.ID, &key.Name, &key.Token, &active, &key.ConversationID, &conversationUpdatedAt, &createdAt, &updatedAt, &lastUsedAt); err != nil {
			return nil, err
		}
		key.Active = active == 1
		key.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		key.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		key.LastUsedAt, _ = time.Parse(time.RFC3339, lastUsedAt)
		if conversationUpdatedAt.Valid && strings.TrimSpace(conversationUpdatedAt.String) != "" {
			if t, err := time.Parse(time.RFC3339, conversationUpdatedAt.String); err == nil {
				key.ConversationUpdatedAt = &t
			}
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

func (s *Store) ListZoAPIKeysWithUsage(ctx context.Context) ([]ZoAPIKeyWithUsage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT k.id, k.name, k.key_secret, k.active, k.conversation_id, k.conversation_updated_at,
		       k.created_at, k.updated_at, k.last_used_at,
		       u.usage_count, u.last_request_at, u.last_reset_at
		FROM zo_api_keys k
		LEFT JOIN zo_api_key_usage u ON u.key_id = k.id
		ORDER BY k.updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ZoAPIKeyWithUsage
	for rows.Next() {
		var key ZoAPIKey
		var usage ZoAPIKeyUsage
		var active int
		var createdAt, updatedAt, lastUsedAt string
		var conversationUpdatedAt sql.NullString
		var usageCount sql.NullInt64
		var lastRequest, lastReset sql.NullString
		if err := rows.Scan(
			&key.ID, &key.Name, &key.Token, &active, &key.ConversationID, &conversationUpdatedAt, &createdAt, &updatedAt, &lastUsedAt,
			&usageCount, &lastRequest, &lastReset,
		); err != nil {
			return nil, err
		}
		key.Active = active == 1
		key.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		key.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		key.LastUsedAt, _ = time.Parse(time.RFC3339, lastUsedAt)
		if conversationUpdatedAt.Valid && strings.TrimSpace(conversationUpdatedAt.String) != "" {
			if t, err := time.Parse(time.RFC3339, conversationUpdatedAt.String); err == nil {
				key.ConversationUpdatedAt = &t
			}
		}
		usage.KeyID = key.ID
		if usageCount.Valid {
			usage.TotalRequests = int(usageCount.Int64)
		}
		if lastRequest.Valid && strings.TrimSpace(lastRequest.String) != "" {
			if t, err := time.Parse(time.RFC3339, lastRequest.String); err == nil {
				usage.LastRequestAt = &t
			}
		}
		if lastReset.Valid && strings.TrimSpace(lastReset.String) != "" {
			if t, err := time.Parse(time.RFC3339, lastReset.String); err == nil {
				usage.LastResetAt = &t
			}
		}
		out = append(out, ZoAPIKeyWithUsage{Key: key, Usage: usage})
	}
	return out, rows.Err()
}

func (s *Store) SetActiveZoAPIKey(ctx context.Context, id string) error {
	keyID := strings.TrimSpace(id)
	if keyID == "" {
		return fmt.Errorf("zo api key id is required")
	}
	tx, err := s.beginTxWithRetry(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := txExecWithRetry(ctx, tx, `UPDATE zo_api_keys SET active=0`); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := txExecWithRetry(ctx, tx, `
		UPDATE zo_api_keys
		SET active=1, updated_at=?, last_used_at=?
		WHERE id=?
	`, now, now, keyID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("zo api key not found")
	}
	return tx.Commit()
}

func (s *Store) TouchZoAPIKeyLastUsed(ctx context.Context, id string) error {
	keyID := strings.TrimSpace(id)
	if keyID == "" {
		return fmt.Errorf("zo api key id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.execWithRetry(ctx, `
		UPDATE zo_api_keys
		SET updated_at=?, last_used_at=?
		WHERE id=?
	`, now, now, keyID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("zo api key not found")
	}
	return nil
}

func (s *Store) GetZoAPIKeyUsage(ctx context.Context, keyID string) (ZoAPIKeyUsage, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT key_id, usage_count, last_request_at, last_reset_at, updated_at
		FROM zo_api_key_usage
		WHERE key_id=?
	`, strings.TrimSpace(keyID))
	return scanZoAPIKeyUsage(row)
}

func (s *Store) UpdateZoAPIKeyConversation(ctx context.Context, keyID string, conversationID string, updatedAt time.Time) error {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return fmt.Errorf("zo api key id is required")
	}
	now := updatedAt.UTC().Format(time.RFC3339)
	res, err := s.execWithRetry(ctx, `
		UPDATE zo_api_keys
		SET conversation_id=?, conversation_updated_at=?, updated_at=?
		WHERE id=?
	`, strings.TrimSpace(conversationID), now, now, keyID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("zo api key not found")
	}
	return nil
}

func (s *Store) IncrementZoAPIKeyUsage(ctx context.Context, keyID string, delta int64) (ZoAPIKeyUsage, error) {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return ZoAPIKeyUsage{}, fmt.Errorf("zo api key id is required")
	}
	if delta <= 0 {
		delta = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.beginTxWithRetry(ctx)
	if err != nil {
		return ZoAPIKeyUsage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := txExecWithRetry(ctx, tx, `
		UPDATE zo_api_keys
		SET last_used_at=?, updated_at=?
		WHERE id=?
	`, now, now, keyID)
	if err != nil {
		return ZoAPIKeyUsage{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ZoAPIKeyUsage{}, fmt.Errorf("zo api key not found")
	}
	_, err = txExecWithRetry(ctx, tx, `
		INSERT INTO zo_api_key_usage(key_id, usage_count, last_request_at, last_reset_at, updated_at)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(key_id) DO UPDATE SET
			usage_count=usage_count + excluded.usage_count,
			last_request_at=excluded.last_request_at,
			updated_at=excluded.updated_at
	`, keyID, delta, now, now, now)
	if err != nil {
		return ZoAPIKeyUsage{}, err
	}
	if err := tx.Commit(); err != nil {
		return ZoAPIKeyUsage{}, err
	}
	return s.GetZoAPIKeyUsage(ctx, keyID)
}

func (s *Store) ResetZoAPIKeyUsage(ctx context.Context, keyID string) (ZoAPIKeyUsage, error) {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return ZoAPIKeyUsage{}, fmt.Errorf("zo api key id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.beginTxWithRetry(ctx)
	if err != nil {
		return ZoAPIKeyUsage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := txExecWithRetry(ctx, tx, `UPDATE zo_api_keys SET updated_at=? WHERE id=?`, now, keyID)
	if err != nil {
		return ZoAPIKeyUsage{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ZoAPIKeyUsage{}, fmt.Errorf("zo api key not found")
	}
	_, err = txExecWithRetry(ctx, tx, `
		INSERT INTO zo_api_key_usage(key_id, usage_count, last_request_at, last_reset_at, updated_at)
		VALUES(?, 0, '', ?, ?)
		ON CONFLICT(key_id) DO UPDATE SET
			usage_count=0,
			last_reset_at=excluded.last_reset_at,
			updated_at=excluded.updated_at
	`, keyID, now, now)
	if err != nil {
		return ZoAPIKeyUsage{}, err
	}
	if err := tx.Commit(); err != nil {
		return ZoAPIKeyUsage{}, err
	}
	return s.GetZoAPIKeyUsage(ctx, keyID)
}

func scanZoAPIKey(row *sql.Row) (ZoAPIKey, error) {
	var key ZoAPIKey
	var active int
	var createdAt, updatedAt, lastUsedAt string
	var conversationUpdatedAt sql.NullString
	if err := row.Scan(&key.ID, &key.Name, &key.Token, &active, &key.ConversationID, &conversationUpdatedAt, &createdAt, &updatedAt, &lastUsedAt); err != nil {
		if err == sql.ErrNoRows {
			return key, fmt.Errorf("zo api key not found")
		}
		return key, err
	}
	key.Active = active == 1
	key.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	key.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	key.LastUsedAt, _ = time.Parse(time.RFC3339, lastUsedAt)
	if conversationUpdatedAt.Valid && strings.TrimSpace(conversationUpdatedAt.String) != "" {
		if t, err := time.Parse(time.RFC3339, conversationUpdatedAt.String); err == nil {
			key.ConversationUpdatedAt = &t
		}
	}
	return key, nil
}

func scanZoAPIKeyUsage(row *sql.Row) (ZoAPIKeyUsage, error) {
	var usage ZoAPIKeyUsage
	var lastRequest, lastReset, updated string
	if err := row.Scan(&usage.KeyID, &usage.TotalRequests, &lastRequest, &lastReset, &updated); err != nil {
		if err == sql.ErrNoRows {
			return usage, fmt.Errorf("zo api key usage not found")
		}
		return usage, err
	}
	if strings.TrimSpace(lastRequest) != "" {
		if t, err := time.Parse(time.RFC3339, lastRequest); err == nil {
			usage.LastRequestAt = &t
		}
	}
	if strings.TrimSpace(lastReset) != "" {
		if t, err := time.Parse(time.RFC3339, lastReset); err == nil {
			usage.LastResetAt = &t
		}
	}
	return usage, nil
}
