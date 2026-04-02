package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/store"
)

type ZoAPIKeyUpdate struct {
	ID     string
	Name   *string
	RawKey *string
	Active *bool
}

func (s *Service) CreateZoAPIKey(ctx context.Context, name string, rawKey string) (store.ZoAPIKey, error) {
	if strings.TrimSpace(rawKey) == "" {
		return store.ZoAPIKey{}, fmt.Errorf("zo api key secret is required")
	}
	if s.Crypto == nil || s.Store == nil {
		return store.ZoAPIKey{}, fmt.Errorf("zo api key service unavailable")
	}
	existing, _ := s.Store.ListZoAPIKeys(ctx)
	isFirst := len(existing) == 0
	id := "zo_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	enc, err := s.Crypto.Encrypt([]byte(rawKey))
	if err != nil {
		return store.ZoAPIKey{}, err
	}
	key := store.ZoAPIKey{
		ID:         id,
		Name:       strings.TrimSpace(name),
		Token:      enc,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
		LastUsedAt: time.Now().UTC(),
		Active:     isFirst,
	}
	if strings.TrimSpace(key.Name) == "" {
		key.Name = id
	}
	stored, err := s.Store.CreateZoAPIKey(ctx, key)
	if err != nil {
		return store.ZoAPIKey{}, err
	}
	if isFirst {
		_ = s.Store.SetActiveZoAPIKey(ctx, stored.ID)
	}
	return redactZoAPIKey(stored), nil
}

func (s *Service) UpdateZoAPIKey(ctx context.Context, update ZoAPIKeyUpdate) (store.ZoAPIKey, error) {
	id := strings.TrimSpace(update.ID)
	if id == "" {
		return store.ZoAPIKey{}, fmt.Errorf("zo api key id is required")
	}
	if s.Crypto == nil || s.Store == nil {
		return store.ZoAPIKey{}, fmt.Errorf("zo api key service unavailable")
	}
	existing, err := s.Store.GetZoAPIKey(ctx, id)
	if err != nil {
		return store.ZoAPIKey{}, err
	}
	if update.Name != nil {
		existing.Name = strings.TrimSpace(*update.Name)
	}
	if update.Active != nil {
		existing.Active = *update.Active
	}
	if update.RawKey != nil {
		raw := strings.TrimSpace(*update.RawKey)
		if raw == "" {
			return store.ZoAPIKey{}, fmt.Errorf("zo api key secret is required")
		}
		enc, err := s.Crypto.Encrypt([]byte(raw))
		if err != nil {
			return store.ZoAPIKey{}, err
		}
		existing.Token = enc
	}
	if strings.TrimSpace(existing.Name) == "" {
		existing.Name = existing.ID
	}
	existing.UpdatedAt = time.Now().UTC()
	stored, err := s.Store.UpdateZoAPIKey(ctx, existing)
	if err != nil {
		return store.ZoAPIKey{}, err
	}
	return redactZoAPIKey(stored), nil
}

func (s *Service) DeleteZoAPIKey(ctx context.Context, id string) error {
	if s.Store == nil {
		return fmt.Errorf("zo api key service unavailable")
	}
	keyID := strings.TrimSpace(id)
	if keyID == "" {
		return fmt.Errorf("zo api key id is required")
	}
	active, _ := s.Store.ActiveZoAPIKey(ctx)
	if err := s.Store.DeleteZoAPIKey(ctx, keyID); err != nil {
		return err
	}
	if strings.TrimSpace(active.ID) != "" && strings.TrimSpace(active.ID) == keyID {
		keys, err := s.Store.ListZoAPIKeys(ctx)
		if err == nil {
			for _, key := range keys {
				if strings.TrimSpace(key.ID) == "" || strings.TrimSpace(key.ID) == keyID {
					continue
				}
				_ = s.Store.SetActiveZoAPIKey(ctx, key.ID)
				break
			}
		}
	}
	return nil
}

func (s *Service) GetZoAPIKey(ctx context.Context, id string) (store.ZoAPIKey, error) {
	if s.Store == nil {
		return store.ZoAPIKey{}, fmt.Errorf("zo api key service unavailable")
	}
	key, err := s.Store.GetZoAPIKey(ctx, id)
	if err != nil {
		return store.ZoAPIKey{}, err
	}
	return redactZoAPIKey(key), nil
}

func (s *Service) ListZoAPIKeys(ctx context.Context) ([]store.ZoAPIKey, error) {
	if s.Store == nil {
		return nil, fmt.Errorf("zo api key service unavailable")
	}
	keys, err := s.Store.ListZoAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]store.ZoAPIKey, 0, len(keys))
	for _, key := range keys {
		out = append(out, redactZoAPIKey(key))
	}
	return out, nil
}

func (s *Service) ListZoAPIKeysWithUsage(ctx context.Context) ([]store.ZoAPIKeyWithUsage, error) {
	if s.Store == nil {
		return nil, fmt.Errorf("zo api key service unavailable")
	}
	items, err := s.Store.ListZoAPIKeysWithUsage(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]store.ZoAPIKeyWithUsage, 0, len(items))
	for _, item := range items {
		item.Key = redactZoAPIKey(item.Key)
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) ActivateZoAPIKey(ctx context.Context, id string) (store.ZoAPIKey, error) {
	if s.Store == nil {
		return store.ZoAPIKey{}, fmt.Errorf("zo api key service unavailable")
	}
	keyID := strings.TrimSpace(id)
	if keyID == "" {
		return store.ZoAPIKey{}, fmt.Errorf("zo api key id is required")
	}
	prev, _ := s.Store.ActiveZoAPIKey(ctx)
	if err := s.Store.SetActiveZoAPIKey(ctx, keyID); err != nil {
		return store.ZoAPIKey{}, err
	}
	next, err := s.Store.GetZoAPIKey(ctx, keyID)
	if err != nil {
		return store.ZoAPIKey{}, err
	}
	if strings.TrimSpace(prev.ID) != "" && strings.TrimSpace(prev.ID) != strings.TrimSpace(next.ID) {
		s.AddSystemLog(ctx, "zo_key_switch", "Zo API key activated", map[string]any{
			"from": strings.TrimSpace(prev.ID),
			"to":   strings.TrimSpace(next.ID),
		})
	}
	return redactZoAPIKey(next), nil
}

func (s *Service) SelectZoAPIKeyForRequest(ctx context.Context, strategy string) (store.ZoAPIKey, string, error) {
	if s.Store == nil || s.Crypto == nil {
		return store.ZoAPIKey{}, "", fmt.Errorf("zo api key service unavailable")
	}
	mode := strings.TrimSpace(strings.ToLower(strategy))
	if mode == "manual" {
		active, err := s.Store.ActiveZoAPIKey(ctx)
		if err != nil {
			return store.ZoAPIKey{}, "", fmt.Errorf("no active zo api key")
		}
		_ = s.Store.SetActiveZoAPIKey(ctx, active.ID)
		raw, err := s.Crypto.Decrypt(active.Token)
		if err != nil {
			return store.ZoAPIKey{}, "", err
		}
		return redactZoAPIKey(active), string(raw), nil
	}
	keys, err := s.Store.ListZoAPIKeys(ctx)
	if err != nil {
		return store.ZoAPIKey{}, "", err
	}
	if len(keys) == 0 {
		return store.ZoAPIKey{}, "", fmt.Errorf("no zo api keys configured")
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].LastUsedAt.Equal(keys[j].LastUsedAt) {
			if !keys[i].CreatedAt.Equal(keys[j].CreatedAt) {
				return keys[i].CreatedAt.Before(keys[j].CreatedAt)
			}
			return strings.TrimSpace(keys[i].ID) < strings.TrimSpace(keys[j].ID)
		}
		return keys[i].LastUsedAt.Before(keys[j].LastUsedAt)
	})
	selected := keys[0]
	_ = s.Store.SetActiveZoAPIKey(ctx, selected.ID)
	raw, err := s.Crypto.Decrypt(selected.Token)
	if err != nil {
		return store.ZoAPIKey{}, "", err
	}
	return redactZoAPIKey(selected), string(raw), nil
}

func (s *Service) ResolveZoAPIKey(ctx context.Context, id string) (store.ZoAPIKey, string, error) {
	if s.Crypto == nil || s.Store == nil {
		return store.ZoAPIKey{}, "", fmt.Errorf("zo api key service unavailable")
	}
	key, err := s.Store.GetZoAPIKey(ctx, id)
	if err != nil {
		return store.ZoAPIKey{}, "", err
	}
	raw, err := s.Crypto.Decrypt(key.Token)
	if err != nil {
		return store.ZoAPIKey{}, "", err
	}
	return redactZoAPIKey(key), string(raw), nil
}

func (s *Service) GetZoAPIKeyUsage(ctx context.Context, id string) (store.ZoAPIKeyUsage, error) {
	if s.Store == nil {
		return store.ZoAPIKeyUsage{}, fmt.Errorf("zo api key service unavailable")
	}
	return s.Store.GetZoAPIKeyUsage(ctx, id)
}

func (s *Service) IncrementZoAPIKeyUsage(ctx context.Context, id string, delta int64) (store.ZoAPIKeyUsage, error) {
	if s.Store == nil {
		return store.ZoAPIKeyUsage{}, fmt.Errorf("zo api key service unavailable")
	}
	return s.Store.IncrementZoAPIKeyUsage(ctx, id, delta)
}

func (s *Service) ResetZoAPIKeyUsage(ctx context.Context, id string) (store.ZoAPIKeyUsage, error) {
	if s.Store == nil {
		return store.ZoAPIKeyUsage{}, fmt.Errorf("zo api key service unavailable")
	}
	usage, err := s.Store.ResetZoAPIKeyUsage(ctx, id)
	if err == nil {
		s.AddSystemLog(ctx, "zo_key_usage_reset", "Zo API key usage reset", map[string]any{
			"id": strings.TrimSpace(id),
		})
	}
	return usage, err
}

func (s *Service) UpdateZoConversation(ctx context.Context, id string, conversationID string) error {
	if s.Store == nil {
		return fmt.Errorf("zo api key service unavailable")
	}
	conv := strings.TrimSpace(conversationID)
	if conv == "" {
		return nil
	}
	return s.Store.UpdateZoAPIKeyConversation(ctx, strings.TrimSpace(id), conv, time.Now().UTC())
}

func redactZoAPIKey(key store.ZoAPIKey) store.ZoAPIKey {
	key.Token = ""
	return key
}
