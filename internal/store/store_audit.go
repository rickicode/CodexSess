package store

import (
	"context"
	"time"
)

func (s *Store) InsertAudit(ctx context.Context, r AuditRecord) error {
	_, err := s.execWithRetry(ctx, `INSERT INTO request_audit(request_id,account_id,model,stream,status,latency_ms,created_at) VALUES(?,?,?,?,?,?,?)`, r.RequestID, r.AccountID, r.Model, boolToInt(r.Stream), r.Status, r.LatencyMS, r.CreatedAt.UTC().Format(time.RFC3339))
	return err
}
