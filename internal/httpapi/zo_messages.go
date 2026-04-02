package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/store"
)

//nolint:unused // handler kept for Zo-specific route wiring
func (s *Server) handleZoMessages(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := "zoclaude_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if r.Method != http.MethodPost {
		respondClaudeErr(w, 405, "method_not_allowed", "method not allowed", reqID)
		return
	}
	if BearerToken(r.Header.Get("Authorization")) != s.currentAPIKey() {
		respondClaudeErr(w, 401, "unauthorized", "invalid API key", reqID)
		return
	}
	var req ClaudeMessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondClaudeErr(w, 400, "invalid_request_error", "invalid JSON body", reqID)
		return
	}
	forcedZoModel := s.currentClaudeCodeZoRoute()
	if forcedZoModel != "" {
		req.Model = forcedZoModel
	}
	modelLabel := strings.TrimSpace(req.Model)
	if modelLabel == "" {
		modelLabel = "zo-default"
	}
	toolDefs := mapClaudeToolsToChatTools(req.Tools)
	sanitizedMessages := s.sanitizeClaudeMessagesForPrompt(req.Messages, toolDefs, reqID)
	prompt := promptFromClaudeMessagesWithSystemAndTools(sanitizedMessages, req.System, toolDefs, req.ToolChoice)
	if strings.TrimSpace(prompt) == "" {
		respondClaudeErr(w, 400, "invalid_request_error", "messages are required", reqID)
		return
	}

	key, rawKey, err := s.resolveZoKeyForClaudeMessages(r.Context())
	if err != nil {
		respondClaudeErr(w, 503, "api_error", err.Error(), reqID)
		return
	}
	_, _ = s.svc.IncrementZoAPIKeyUsage(r.Context(), key.ID, 1)
	conversationID := s.resolveZoConversationID(r, key)

	zoReq := zoAskRequest{
		Input:          prompt,
		ModelName:      strings.TrimSpace(req.Model),
		ConversationID: conversationID,
	}

	status := 200
	defer func() {
		_ = s.svc.Store.InsertAudit(r.Context(), store.AuditRecord{
			RequestID: reqID,
			AccountID: key.ID,
			Model:     modelLabel,
			Stream:    req.Stream,
			Status:    status,
			LatencyMS: time.Since(start).Milliseconds(),
			CreatedAt: time.Now().UTC(),
		})
	}()

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			status = 500
			respondClaudeErr(w, 500, "api_error", "streaming not supported", reqID)
			return
		}
		writeSSE(w, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            reqID,
				"type":          "message",
				"role":          "assistant",
				"model":         modelLabel,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  0,
					"output_tokens": 0,
				},
			},
		})
		flusher.Flush()

		textStarted := false
		contentIdx := 0
		onDelta := func(delta string) error {
			if strings.TrimSpace(delta) == "" {
				return nil
			}
			if !textStarted {
				writeSSE(w, "content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": contentIdx,
					"content_block": map[string]any{
						"type": "text",
						"text": "",
					},
				})
				flusher.Flush()
				textStarted = true
			}
			writeSSE(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": contentIdx,
				"delta": map[string]any{"type": "text_delta", "text": delta},
			})
			flusher.Flush()
			return nil
		}

		result, err := callZoAskStream(r.Context(), rawKey, zoReq, onDelta)
		if err != nil && isZoConversationBusyError(err) {
			zoReq.ConversationID = ""
			result, err = callZoAskStream(r.Context(), rawKey, zoReq, onDelta)
		}
		if err != nil {
			status = 502
			writeSSE(w, "error", map[string]any{
				"type": "error",
				"error": map[string]any{
					"type":    "api_error",
					"message": err.Error(),
				},
			})
			flusher.Flush()
			return
		}
		setZoConversationHeaders(w, result.ConversationID)
		if conv := firstNonEmpty(result.ConversationID, conversationID); strings.TrimSpace(conv) != "" {
			_ = s.svc.UpdateZoConversation(r.Context(), key.ID, conv)
		}
		toolCalls, _ := resolveToolCalls(result.Text, toolDefs, nil)
		if textStarted {
			writeSSE(w, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": contentIdx,
			})
			flusher.Flush()
		}
		if len(toolCalls) > 0 {
			writeSSE(w, "message_delta", map[string]any{
				"type":        "message_delta",
				"delta":       map[string]any{"stop_reason": "tool_use"},
				"usage":       map[string]any{"input_tokens": 0, "output_tokens": 0},
				"stop_reason": "tool_use",
			})
			flusher.Flush()
			writeSSE(w, "message_stop", map[string]any{
				"type": "message_stop",
			})
			flusher.Flush()
			return
		}
		writeSSE(w, "message_delta", map[string]any{
			"type":        "message_delta",
			"delta":       map[string]any{"stop_reason": "end_turn"},
			"usage":       map[string]any{"input_tokens": 0, "output_tokens": 0},
			"stop_reason": "end_turn",
		})
		flusher.Flush()
		writeSSE(w, "message_stop", map[string]any{
			"type": "message_stop",
		})
		flusher.Flush()
		return
	}

	result, err := callZoAsk(r.Context(), rawKey, zoReq)
	if err != nil && isZoConversationBusyError(err) {
		zoReq.ConversationID = ""
		result, err = callZoAsk(r.Context(), rawKey, zoReq)
	}
	if err != nil {
		status = 502
		respondClaudeErr(w, 502, "api_error", err.Error(), reqID)
		return
	}
	setZoConversationHeaders(w, result.ConversationID)
	if conv := firstNonEmpty(result.ConversationID, conversationID); strings.TrimSpace(conv) != "" {
		_ = s.svc.UpdateZoConversation(r.Context(), key.ID, conv)
	}
	toolCalls, _ := resolveToolCalls(result.Text, toolDefs, nil)
	content, stopReason := buildClaudeResponseContent(result.Text, toolCalls)
	resp := ClaudeMessagesResponse{
		ID:         reqID,
		Type:       "message",
		Role:       "assistant",
		Model:      modelLabel,
		Content:    content,
		StopReason: stopReason,
		Usage:      ClaudeMessagesUsage{},
	}
	respondJSON(w, 200, resp)
}
