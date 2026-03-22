package service

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/store"
)

func (s *Service) AddSystemLog(ctx context.Context, kind, message string, meta map[string]any) {
	if s == nil || s.Store == nil {
		return
	}
	entry := store.SystemLogEntry{
		ID:        uuid.NewString(),
		Kind:      strings.TrimSpace(kind),
		Message:   strings.TrimSpace(message),
		MetaJSON:  store.SystemLogMetaJSON(meta),
		CreatedAt: time.Now().UTC(),
	}
	_ = s.Store.AddSystemLog(ctx, entry)
	maxRows := s.Cfg.SystemLogMaxRows
	if maxRows < 0 {
		maxRows = 0
	}
	if maxRows > 0 {
		_ = s.Store.PruneSystemLogs(ctx, maxRows)
	}
}
