package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

var claudeWireTranslator = defaultClaudeTranslator

func writeClaudeExecutionSetupError(w http.ResponseWriter, err error, reqID string) {
	code, errType, msg := claudeWireTranslator.ClassifySetupError(err)
	respondClaudeErr(w, code, errType, msg, reqID)
}

func (s *Server) writeClaudeMessagesStreamResponse(w http.ResponseWriter, r *http.Request, reqID string, state *claudeMessagesRequestState, exec *proxyExecutionSession, status *int) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		*status = 500
		respondClaudeErr(w, 500, "api_error", "streaming not supported", reqID)
		return
	}

	writeSSE(w, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            reqID,
			"type":          "message",
			"role":          "assistant",
			"model":         state.request.Model,
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

	var (
		contentIdx   = 0
		streamedText strings.Builder
		streamMu     sync.Mutex
	)
	onDelta := func(delta string) error {
		if strings.TrimSpace(delta) == "" {
			return nil
		}
		streamMu.Lock()
		streamedText.WriteString(delta)
		streamMu.Unlock()
		return nil
	}

	result, err := s.executor.executeStream(r.Context(), exec, state.prompt, state.directOpts, onDelta, false)
	if err != nil {
		code, errType := claudeWireTranslator.ClassifyUpstreamError(err)
		*status = code
		writeSSE(w, "error", map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    errType,
				"message": err.Error(),
			},
		})
		flusher.Flush()
		return
	}
	usage := ClaudeMessagesUsage{InputTokens: result.InputTokens, OutputTokens: result.OutputTokens}
	normalized := claudeWireTranslator.NormalizeOutput(result.Text, state.toolDefs, result.ToolCalls)

	streamMu.Lock()
	finalText := strings.TrimSpace(streamedText.String())
	streamMu.Unlock()
	if finalText == "" {
		finalText = normalized.Text
	}
	finalText = claudeWireTranslator.SanitizeAssistantText(finalText)
	if finalText != "" {
		writeSSE(w, "content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": contentIdx,
			"content_block": map[string]any{
				"type": "text",
				"text": "",
			},
		})
		writeSSE(w, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": contentIdx,
			"delta": map[string]any{"type": "text_delta", "text": finalText},
		})
		writeSSE(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": contentIdx})
		flusher.Flush()
		contentIdx++
	}

	for _, call := range normalized.ToolCalls {
		toolInputJSON := strings.TrimSpace(call.Function.Arguments)
		if toolInputJSON == "" {
			toolInputJSON = "{}"
		}
		if !json.Valid([]byte(toolInputJSON)) {
			toolInputJSON = "{}"
		}
		writeSSE(w, "content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": contentIdx,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    strings.TrimSpace(call.ID),
				"name":  strings.TrimSpace(call.Function.Name),
				"input": map[string]any{},
			},
		})
		writeSSE(w, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": contentIdx,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": toolInputJSON},
		})
		writeSSE(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": contentIdx})
		flusher.Flush()
		contentIdx++
	}

	stopReason := "end_turn"
	if len(normalized.ToolCalls) > 0 {
		stopReason = "tool_use"
	}
	writeSSE(w, "message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
		},
	})
	writeSSE(w, "message_stop", map[string]any{"type": "message_stop"})
	flusher.Flush()
}

func writeClaudeMessagesJSONResponse(w http.ResponseWriter, reqID string, state *claudeMessagesRequestState, result proxyBackendResult) {
	normalized := claudeWireTranslator.NormalizeOutput(result.Text, state.toolDefs, result.ToolCalls)
	respondJSON(w, 200, ClaudeMessagesResponse{
		ID:         reqID,
		Type:       "message",
		Role:       "assistant",
		Model:      state.request.Model,
		Content:    normalized.Content,
		StopReason: normalized.StopReason,
		Usage: ClaudeMessagesUsage{
			InputTokens:  result.InputTokens,
			OutputTokens: result.OutputTokens,
		},
	})
}
