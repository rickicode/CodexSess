package service

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/store"
)

func (s *Service) UpsertCodingMemory(ctx context.Context, item store.MemoryItem) (store.MemoryItem, error) {
	if strings.TrimSpace(item.ID) == "" {
		item.ID = "mem_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	return s.Store.UpsertMemoryItem(ctx, item)
}

func (s *Service) ListCodingMemory(ctx context.Context, query store.MemoryQuery) ([]store.MemoryItem, error) {
	return s.Store.ListMemoryItems(ctx, query)
}
