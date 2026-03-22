package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/config"
	"github.com/ricki/codexsess/internal/store"
)

func (s *Server) handleWebZoKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.respondZoKeysList(w, r)
		return
	case http.MethodPost:
		var req struct {
			Name   string `json:"name"`
			APIKey string `json:"api_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON")
			return
		}
		if strings.TrimSpace(req.APIKey) == "" {
			respondErr(w, 400, "bad_request", "api_key is required")
			return
		}
		if _, err := s.svc.CreateZoAPIKey(r.Context(), strings.TrimSpace(req.Name), strings.TrimSpace(req.APIKey)); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		s.respondZoKeysList(w, r)
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) respondZoKeysList(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.ListZoAPIKeysWithUsage(r.Context())
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	keys := make([]map[string]any, 0, len(items))
	for _, item := range items {
		rawMask := "-"
		if _, raw, err := s.svc.ResolveZoAPIKey(r.Context(), item.Key.ID); err == nil {
			if strings.TrimSpace(raw) != "" {
				rawMask = maskSecret(raw)
			}
		}
		keys = append(keys, map[string]any{
			"id":               item.Key.ID,
			"name":             item.Key.Name,
			"active":           item.Key.Active,
			"masked_key":       rawMask,
			"created_at":       item.Key.CreatedAt.UTC().Format(time.RFC3339),
			"updated_at":       item.Key.UpdatedAt.UTC().Format(time.RFC3339),
			"last_used_at":     item.Key.LastUsedAt.UTC().Format(time.RFC3339),
			"total_requests":   item.Usage.TotalRequests,
			"last_request_at":  formatNullableTime(item.Usage.LastRequestAt),
			"last_reset_at":    formatNullableTime(item.Usage.LastResetAt),
			"has_usage_metric": item.Usage.TotalRequests > 0,
		})
	}
	s.mu.RLock()
	strategy := config.NormalizeZoAPIStrategy(s.svc.Cfg.ZoAPIStrategy)
	s.mu.RUnlock()
	respondJSON(w, 200, map[string]any{
		"ok":       true,
		"keys":     keys,
		"total":    len(keys),
		"strategy": strategy,
	})
}

func (s *Server) handleWebZoKeyActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	if _, err := s.svc.ActivateZoAPIKey(r.Context(), strings.TrimSpace(req.ID)); err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleWebZoKeyDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	if err := s.svc.DeleteZoAPIKey(r.Context(), strings.TrimSpace(req.ID)); err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleWebZoKeyReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	if _, err := s.svc.ResetZoAPIKeyUsage(r.Context(), strings.TrimSpace(req.ID)); err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleWebZoKeyStrategy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Strategy string `json:"strategy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	strategy := config.NormalizeZoAPIStrategy(req.Strategy)
	s.mu.Lock()
	cfg := s.svc.Cfg
	before := cfg.ZoAPIStrategy
	cfg.ZoAPIStrategy = strategy
	s.svc.Cfg = cfg
	s.mu.Unlock()
	if err := s.saveSetting(r.Context(), store.SettingZoAPIStrategy, strategy); err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	if before != strategy {
		s.svc.AddSystemLog(r.Context(), "settings_change", "Settings updated", map[string]any{
			"changed": map[string]any{
				"zo_api_strategy": map[string]any{
					"from": before,
					"to":   strategy,
				},
			},
			"source": "ui",
		})
	}
	respondJSON(w, 200, map[string]any{"ok": true, "strategy": strategy})
}

func formatNullableTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
