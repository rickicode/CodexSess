package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/store"
)

func (s *Server) handleZoChatCompletions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := "zochat_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if BearerToken(r.Header.Get("Authorization")) != s.currentAPIKey() {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return
	}

	var req ChatCompletionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON body")
		return
	}
	modelLabel := strings.TrimSpace(req.Model)
	if modelLabel == "" {
		modelLabel = "zo-default"
	}
	structuredSpec, err := normalizeResponseFormat(req.ResponseFormat)
	if err != nil {
		respondErr(w, 400, "invalid_request_error", err.Error())
		return
	}
	prompt := promptFromMessages(req.Messages)
	if len(req.Tools) > 0 {
		prompt = promptFromMessagesWithTools(req.Messages, req.Tools, req.ToolChoice)
	}
	if strings.TrimSpace(prompt) == "" {
		respondErr(w, 400, "bad_request", "messages are required")
		return
	}

	key, rawKey, err := s.resolveZoKeyForRequest(r.Context())
	if err != nil {
		respondErr(w, 503, "zo_api_key_missing", err.Error())
		return
	}
	_, _ = s.svc.IncrementZoAPIKeyUsage(r.Context(), key.ID, 1)
	conversationID := s.resolveZoConversationID(r, key)

	zoReq := zoAskRequest{
		Input:          prompt,
		ModelName:      strings.TrimSpace(req.Model),
		ConversationID: conversationID,
	}
	if structuredSpec != nil {
		zoReq.OutputFormat = bytes.TrimSpace(structuredSpec.Schema)
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
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			status = 500
			respondErr(w, 500, "internal_error", "streaming not supported")
			return
		}

		bufferedToolsMode := len(req.Tools) > 0 || structuredSpec != nil
		var streamedText strings.Builder
		stopKeepAlive := func() {}
		if bufferedToolsMode {
			stopKeepAlive = startSSEKeepAlive(r.Context(), w, flusher, resolveSSEKeepAliveInterval())
		}

		result, err := callZoAskStream(r.Context(), rawKey, zoReq, func(delta string) error {
			if bufferedToolsMode {
				streamedText.WriteString(delta)
				return nil
			}
			chunk := ChatCompletionsChunk{
				ID:      "chatcmpl-" + reqID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   modelLabel,
				Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{Role: "assistant", Content: delta}}},
			}
			writeChatCompletionsChunk(w, flusher, chunk)
			return nil
		})
		stopKeepAlive()
		if err != nil {
			status = 502
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"`+escape(err.Error())+`"}}`)
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		setZoConversationHeaders(w, result.ConversationID)
		if conv := firstNonEmpty(result.ConversationID, conversationID); strings.TrimSpace(conv) != "" {
			_ = s.svc.UpdateZoConversation(r.Context(), key.ID, conv)
		}
		usage := Usage{}
		if bufferedToolsMode {
			toolCalls, hasToolCalls := resolveToolCalls(result.Text, req.Tools, nil)
			if structuredSpec != nil {
				if hasToolCalls {
					_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"tool_calls not allowed when response_format is set","type":"invalid_response_format"}}`)
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				text := strings.TrimSpace(streamedText.String())
				if text == "" {
					text = result.Text
				}
				if err := validateStructuredOutput(structuredSpec, text); err != nil {
					_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"`+escape(err.Error())+`","type":"invalid_response_format"}}`)
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				streamChatCompletionText(w, flusher, "chatcmpl-"+reqID, modelLabel, text, usage, req.StreamOpts != nil && req.StreamOpts.IncludeUsage)
				return
			}
			if hasToolCalls {
				streamChatCompletionToolCalls(w, flusher, "chatcmpl-"+reqID, modelLabel, toolCalls, usage, req.StreamOpts != nil && req.StreamOpts.IncludeUsage)
				return
			}
			text := strings.TrimSpace(streamedText.String())
			if text == "" {
				text = result.Text
			}
			streamChatCompletionText(w, flusher, "chatcmpl-"+reqID, modelLabel, text, usage, req.StreamOpts != nil && req.StreamOpts.IncludeUsage)
			return
		}
		streamChatCompletionFinalStop(w, flusher, "chatcmpl-"+reqID, modelLabel, usage, req.StreamOpts != nil && req.StreamOpts.IncludeUsage)
		return
	}

	result, err := callZoAsk(r.Context(), rawKey, zoReq)
	if err != nil {
		status = 502
		respondErr(w, 502, "upstream_error", err.Error())
		return
	}
	setZoConversationHeaders(w, result.ConversationID)
	if conv := firstNonEmpty(result.ConversationID, conversationID); strings.TrimSpace(conv) != "" {
		_ = s.svc.UpdateZoConversation(r.Context(), key.ID, conv)
	}
	toolCalls, hasToolCalls := resolveToolCalls(result.Text, req.Tools, nil)
	if structuredSpec != nil {
		if hasToolCalls {
			respondErr(w, 400, "invalid_response_format", "tool_calls not allowed when response_format is set")
			return
		}
		if err := validateStructuredOutput(structuredSpec, result.Text); err != nil {
			respondErr(w, 400, "invalid_response_format", err.Error())
			return
		}
	}
	choice := ChatChoice{
		Index:        0,
		Message:      ChatMessage{Role: "assistant", Content: result.Text},
		FinishReason: "stop",
	}
	if hasToolCalls {
		choice.Message = ChatMessage{
			Role:      "assistant",
			Content:   "",
			ToolCalls: toolCalls,
		}
		choice.FinishReason = "tool_calls"
	}
	resp := ChatCompletionsResponse{
		ID:      "chatcmpl-" + reqID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelLabel,
		Choices: []ChatChoice{choice},
		Usage:   Usage{},
	}
	respondJSON(w, 200, resp)
}
