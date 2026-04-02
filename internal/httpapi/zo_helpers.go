package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/ricki/codexsess/internal/config"
	"github.com/ricki/codexsess/internal/store"
)

func (s *Server) resolveZoKeyForRequest(ctx context.Context) (store.ZoAPIKey, string, error) {
	s.mu.RLock()
	strategy := config.NormalizeZoAPIStrategy(s.svc.Cfg.ZoAPIStrategy)
	s.mu.RUnlock()
	return s.svc.SelectZoAPIKeyForRequest(ctx, strategy)
}

//nolint:unused // used by Zo-specific handlers when route wiring is enabled
func (s *Server) resolveZoKeyForClaudeMessages(ctx context.Context) (store.ZoAPIKey, string, error) {
	return s.resolveZoKeyForRequest(ctx)
}

//nolint:unused // used by Zo-specific handlers when route wiring is enabled
func (s *Server) currentClaudeCodeZoRoute() string {
	return ""
}

func (s *Server) resolveZoConversationID(r *http.Request, key store.ZoAPIKey) string {
	if strings.TrimSpace(key.ConversationID) == "" || key.ConversationUpdatedAt == nil {
		return ""
	}
	updatedAt := key.ConversationUpdatedAt.UTC()
	if time.Since(updatedAt) > zoConversationTTL {
		return ""
	}
	return strings.TrimSpace(key.ConversationID)
}

func setZoConversationHeaders(w http.ResponseWriter, conversationID string) {
	conv := strings.TrimSpace(conversationID)
	if conv == "" || w == nil {
		return
	}
	w.Header().Set("x-conversation-id", conv)
	w.Header().Set("X-Zo-Conversation-Id", conv)
}
