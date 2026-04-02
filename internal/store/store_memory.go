package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func normalizeMemoryText(v string) string {
	return strings.TrimSpace(v)
}

func clampMemoryConfidence(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func (s *Store) UpsertMemoryItem(ctx context.Context, item MemoryItem) (MemoryItem, error) {
	item.ID = normalizeMemoryText(item.ID)
	item.Scope = strings.ToLower(normalizeMemoryText(item.Scope))
	item.ScopeID = normalizeMemoryText(item.ScopeID)
	item.Kind = strings.ToLower(normalizeMemoryText(item.Kind))
	item.Key = normalizeMemoryText(item.Key)
	item.ValueJSON = strings.TrimSpace(item.ValueJSON)
	item.SourceType = strings.ToLower(normalizeMemoryText(item.SourceType))
	item.SourceRef = normalizeMemoryText(item.SourceRef)
	item.Confidence = clampMemoryConfidence(item.Confidence)
	if item.Scope == "" {
		return MemoryItem{}, fmt.Errorf("memory scope is required")
	}
	if item.Kind == "" {
		return MemoryItem{}, fmt.Errorf("memory kind is required")
	}
	if item.Key == "" {
		return MemoryItem{}, fmt.Errorf("memory key is required")
	}
	if item.ID == "" {
		return MemoryItem{}, fmt.Errorf("memory id is required")
	}
	if item.ValueJSON == "" {
		item.ValueJSON = "{}"
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	expiresAt := ""
	if item.ExpiresAt != nil && !item.ExpiresAt.IsZero() {
		expiresAt = item.ExpiresAt.UTC().Format(time.RFC3339)
	}
	_, err := s.execWithRetry(ctx, `
		INSERT INTO memory_items(
			id,scope,scope_id,kind,key,value_json,source_type,source_ref,verified,confidence,stale,created_at,updated_at,expires_at
		)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(scope, scope_id, kind, key) DO UPDATE SET
			value_json=excluded.value_json,
			source_type=excluded.source_type,
			source_ref=excluded.source_ref,
			verified=excluded.verified,
			confidence=excluded.confidence,
			stale=excluded.stale,
			updated_at=excluded.updated_at,
			expires_at=excluded.expires_at
	`, item.ID, item.Scope, item.ScopeID, item.Kind, item.Key, item.ValueJSON, item.SourceType, item.SourceRef, boolToInt(item.Verified), item.Confidence, boolToInt(item.Stale), item.CreatedAt.UTC().Format(time.RFC3339), item.UpdatedAt.UTC().Format(time.RFC3339), expiresAt)
	if err != nil {
		return MemoryItem{}, err
	}
	return s.GetMemoryItemByNaturalKey(ctx, item.Scope, item.ScopeID, item.Kind, item.Key)
}

func (s *Store) GetMemoryItemByNaturalKey(ctx context.Context, scope, scopeID, kind, key string) (MemoryItem, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id,scope,scope_id,kind,key,value_json,source_type,source_ref,verified,confidence,stale,created_at,updated_at,expires_at
		FROM memory_items
		WHERE scope=? AND scope_id=? AND kind=? AND key=?
		LIMIT 1
	`, strings.ToLower(normalizeMemoryText(scope)), normalizeMemoryText(scopeID), strings.ToLower(normalizeMemoryText(kind)), normalizeMemoryText(key))
	var item MemoryItem
	var verified, stale int
	var createdAt, updatedAt, expiresAt string
	if err := row.Scan(&item.ID, &item.Scope, &item.ScopeID, &item.Kind, &item.Key, &item.ValueJSON, &item.SourceType, &item.SourceRef, &verified, &item.Confidence, &stale, &createdAt, &updatedAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MemoryItem{}, fmt.Errorf("memory item not found")
		}
		return MemoryItem{}, err
	}
	item.Verified = verified != 0
	item.Stale = stale != 0
	item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	item.ExpiresAt = parseOptionalRFC3339(expiresAt)
	return item, nil
}

func (s *Store) ListMemoryItems(ctx context.Context, query MemoryQuery) ([]MemoryItem, error) {
	scope := strings.ToLower(normalizeMemoryText(query.Scope))
	scopeID := normalizeMemoryText(query.ScopeID)
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	args := make([]any, 0, 16)
	conditions := make([]string, 0, 10)
	if scope != "" {
		conditions = append(conditions, "scope=?")
		args = append(args, scope)
	}
	if scopeID != "" {
		conditions = append(conditions, "scope_id=?")
		args = append(args, scopeID)
	}
	kinds := make([]string, 0, len(query.Kinds))
	for _, kind := range query.Kinds {
		k := strings.ToLower(normalizeMemoryText(kind))
		if k == "" {
			continue
		}
		kinds = append(kinds, k)
	}
	if len(kinds) > 0 {
		placeholders := make([]string, 0, len(kinds))
		for _, k := range kinds {
			placeholders = append(placeholders, "?")
			args = append(args, k)
		}
		conditions = append(conditions, "kind IN ("+strings.Join(placeholders, ",")+")")
	}
	if query.VerifiedOnly {
		conditions = append(conditions, "verified=1")
	}
	if !query.IncludeStale {
		conditions = append(conditions, "stale=0")
	}

	var b strings.Builder
	b.WriteString(`SELECT id,scope,scope_id,kind,key,value_json,source_type,source_ref,verified,confidence,stale,created_at,updated_at,expires_at FROM memory_items`)
	if len(conditions) > 0 {
		b.WriteString(" WHERE ")
		b.WriteString(strings.Join(conditions, " AND "))
	}
	b.WriteString(" ORDER BY updated_at DESC, created_at DESC LIMIT ?")
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]MemoryItem, 0, limit)
	for rows.Next() {
		var item MemoryItem
		var verified, stale int
		var createdAt, updatedAt, expiresAt string
		if err := rows.Scan(&item.ID, &item.Scope, &item.ScopeID, &item.Kind, &item.Key, &item.ValueJSON, &item.SourceType, &item.SourceRef, &verified, &item.Confidence, &stale, &createdAt, &updatedAt, &expiresAt); err != nil {
			return nil, err
		}
		item.Verified = verified != 0
		item.Stale = stale != 0
		item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		item.ExpiresAt = parseOptionalRFC3339(expiresAt)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Store) DeleteMemoryItem(ctx context.Context, id string) error {
	memoryID := normalizeMemoryText(id)
	if memoryID == "" {
		return fmt.Errorf("memory id is required")
	}
	_, err := s.execWithRetry(ctx, `DELETE FROM memory_items WHERE id=?`, memoryID)
	return err
}

func (s *Store) DeleteMemoryItemsByScope(ctx context.Context, scope, scopeID string) error {
	nScope := strings.ToLower(normalizeMemoryText(scope))
	nScopeID := normalizeMemoryText(scopeID)
	if nScope == "" {
		return fmt.Errorf("memory scope is required")
	}
	if nScopeID == "" {
		return fmt.Errorf("memory scope_id is required")
	}
	_, err := s.execWithRetry(ctx, `DELETE FROM memory_items WHERE scope=? AND scope_id=?`, nScope, nScopeID)
	return err
}

func (s *Store) DeleteMemoryItemByNaturalKey(ctx context.Context, scope, scopeID, kind, key string) error {
	nScope := strings.ToLower(normalizeMemoryText(scope))
	nScopeID := normalizeMemoryText(scopeID)
	nKind := strings.ToLower(normalizeMemoryText(kind))
	nKey := normalizeMemoryText(key)
	if nScope == "" {
		return fmt.Errorf("memory scope is required")
	}
	if nScopeID == "" {
		return fmt.Errorf("memory scope_id is required")
	}
	if nKind == "" {
		return fmt.Errorf("memory kind is required")
	}
	if nKey == "" {
		return fmt.Errorf("memory key is required")
	}
	_, err := s.execWithRetry(ctx, `DELETE FROM memory_items WHERE scope=? AND scope_id=? AND kind=? AND key=?`, nScope, nScopeID, nKind, nKey)
	return err
}

func (s *Store) MarkMemoryItemStale(ctx context.Context, scope, scopeID, kind, key string, stale bool) error {
	nScope := strings.ToLower(normalizeMemoryText(scope))
	nScopeID := normalizeMemoryText(scopeID)
	nKind := strings.ToLower(normalizeMemoryText(kind))
	nKey := normalizeMemoryText(key)
	if nScope == "" {
		return fmt.Errorf("memory scope is required")
	}
	if nScopeID == "" {
		return fmt.Errorf("memory scope_id is required")
	}
	if nKind == "" {
		return fmt.Errorf("memory kind is required")
	}
	if nKey == "" {
		return fmt.Errorf("memory key is required")
	}
	_, err := s.execWithRetry(ctx, `
		UPDATE memory_items
		SET stale=?, updated_at=?
		WHERE scope=? AND scope_id=? AND kind=? AND key=?
	`, boolToInt(stale), time.Now().UTC().Format(time.RFC3339), nScope, nScopeID, nKind, nKey)
	return err
}
