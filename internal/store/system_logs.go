package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type SystemLogEntry struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Message   string    `json:"message"`
	MetaJSON  string    `json:"meta_json"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) AddSystemLog(ctx context.Context, entry SystemLogEntry) error {
	if strings.TrimSpace(entry.ID) == "" {
		return fmt.Errorf("system log id required")
	}
	if strings.TrimSpace(entry.Kind) == "" {
		entry.Kind = "system"
	}
	if strings.TrimSpace(entry.Message) == "" {
		entry.Message = "(empty)"
	}
	if strings.TrimSpace(entry.MetaJSON) == "" {
		entry.MetaJSON = "{}"
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system_logs(id, kind, message, meta_json, created_at)
		VALUES(?, ?, ?, ?, ?)
	`, entry.ID, entry.Kind, entry.Message, entry.MetaJSON, entry.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) ListSystemLogs(ctx context.Context, limit int) ([]SystemLogEntry, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 50000 {
		limit = 50000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, message, meta_json, created_at
		FROM system_logs
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SystemLogEntry{}
	for rows.Next() {
		var item SystemLogEntry
		var created string
		if err := rows.Scan(&item.ID, &item.Kind, &item.Message, &item.MetaJSON, &created); err != nil {
			return nil, err
		}
		if ts, err := time.Parse(time.RFC3339, created); err == nil {
			item.CreatedAt = ts
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CountSystemLogs(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM system_logs`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) ClearSystemLogs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM system_logs`)
	return err
}

func (s *Store) PruneSystemLogs(ctx context.Context, maxRows int) error {
	if maxRows <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM system_logs
		WHERE id NOT IN (
			SELECT id FROM system_logs
			ORDER BY created_at DESC
			LIMIT ?
		)
	`, maxRows)
	return err
}

func SystemLogMetaJSON(meta map[string]any) string {
	if meta == nil {
		return "{}"
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(b)
}
