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

//nolint:unused // handler kept for Zo-specific route wiring
func (s *Server) handleZoResponses(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := "zoresp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if BearerToken(r.Header.Get("Authorization")) != s.currentAPIKey() {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return
	}
	var req ResponsesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON body")
		return
	}
	structuredSpec, err := normalizeResponseFormat(nil)
	if req.Text != nil {
		structuredSpec, err = normalizeResponseFormat(req.Text.Format)
	}
	if err != nil {
		respondErr(w, 400, "invalid_request_error", err.Error())
		return
	}
	modelLabel := strings.TrimSpace(req.Model)
	if modelLabel == "" {
		modelLabel = "zo-default"
	}
	prompt := promptFromResponsesInput(req.Input, nil, nil)
	if len(req.Tools) > 0 {
		prompt = promptFromResponsesInput(req.Input, req.Tools, req.ToolChoice)
	}
	if strings.TrimSpace(prompt) == "" {
		respondErr(w, 400, "bad_request", "input is required")
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
		responseCreatedAt := time.Now().Unix()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			status = 500
			respondErr(w, 500, "internal_error", "streaming not supported")
			return
		}
		seq := 0
		emit := func(_ string, payload map[string]any) {
			if payload == nil {
				return
			}
			if _, exists := payload["sequence_number"]; !exists {
				seq++
				payload["sequence_number"] = seq
			}
			writeOpenAISSE(w, payload)
			flusher.Flush()
		}
		emit("response.created", map[string]any{
			"type":     "response.created",
			"response": buildResponseObject(reqID, modelLabel, "in_progress", []any{}, nil, responseCreatedAt),
		})

		bufferedToolsMode := len(req.Tools) > 0 || structuredSpec != nil
		var streamedText strings.Builder
		textItemID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		stopKeepAlive := func() {}
		if bufferedToolsMode {
			stopKeepAlive = startSSEKeepAlive(r.Context(), w, flusher, resolveSSEKeepAliveInterval())
		}
		if !bufferedToolsMode {
			emit("response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item": map[string]any{
					"type":    "message",
					"id":      textItemID,
					"status":  "in_progress",
					"role":    "assistant",
					"content": []any{},
				},
			})
		}
		result, err := callZoAskStream(r.Context(), rawKey, zoReq, func(delta string) error {
			if bufferedToolsMode {
				streamedText.WriteString(delta)
				return nil
			}
			streamedText.WriteString(delta)
			emit("response.output_text.delta", map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       textItemID,
				"output_index":  0,
				"content_index": 0,
				"delta":         delta,
				"logprobs":      []any{},
			})
			return nil
		})
		stopKeepAlive()
		if err != nil {
			status = 502
			emit("error", map[string]any{
				"type":    "error",
				"code":    "upstream_error",
				"message": err.Error(),
				"param":   nil,
			})
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		setZoConversationHeaders(w, result.ConversationID)
		if conv := firstNonEmpty(result.ConversationID, conversationID); strings.TrimSpace(conv) != "" {
			_ = s.svc.UpdateZoConversation(r.Context(), key.ID, conv)
		}
		usage := ResponsesUsage{}
		if bufferedToolsMode {
			toolCalls, hasToolCalls := resolveToolCalls(result.Text, req.Tools, nil)
			if structuredSpec != nil {
				if hasToolCalls {
					emit("error", map[string]any{
						"type":    "error",
						"code":    "invalid_response_format",
						"message": "tool_calls not allowed when text.format is set",
						"param":   nil,
					})
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				text := strings.TrimSpace(streamedText.String())
				if text == "" {
					text = strings.TrimSpace(result.Text)
				}
				if err := validateStructuredOutput(structuredSpec, text); err != nil {
					emit("error", map[string]any{
						"type":    "error",
						"code":    "invalid_response_format",
						"message": err.Error(),
						"param":   nil,
					})
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				streamResponsesText(emit, reqID, modelLabel, text, usage, responseCreatedAt)
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			if hasToolCalls {
				streamResponsesFunctionCalls(emit, reqID, modelLabel, toolCalls, usage, responseCreatedAt)
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			text := strings.TrimSpace(streamedText.String())
			if text == "" {
				text = strings.TrimSpace(result.Text)
			}
			streamResponsesText(emit, reqID, modelLabel, text, usage, responseCreatedAt)
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		finalText := strings.TrimSpace(result.Text)
		if finalText == "" {
			finalText = strings.TrimSpace(streamedText.String())
		}
		emit("response.output_text.done", map[string]any{
			"type":          "response.output_text.done",
			"item_id":       textItemID,
			"output_index":  0,
			"content_index": 0,
			"text":          finalText,
			"logprobs":      []any{},
		})
		outputItem := map[string]any{
			"type":   "message",
			"id":     textItemID,
			"status": "completed",
			"role":   "assistant",
			"content": []map[string]any{
				{"type": "output_text", "text": finalText, "annotations": []any{}},
			},
		}
		emit("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": 0,
			"item":         outputItem,
		})
		emit("response.completed", map[string]any{
			"type": "response.completed",
			"response": buildResponseObject(reqID, modelLabel, "completed", []any{outputItem}, map[string]any{
				"input_tokens":  usage.InputTokens,
				"output_tokens": usage.OutputTokens,
				"total_tokens":  usage.TotalTokens,
			}, responseCreatedAt),
		})
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
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
			respondErr(w, 400, "invalid_response_format", "tool_calls not allowed when text.format is set")
			return
		}
		if err := validateStructuredOutput(structuredSpec, result.Text); err != nil {
			respondErr(w, 400, "invalid_response_format", err.Error())
			return
		}
	}
	output := responsesMessageOutputItems(result.Text)
	outputText := strings.TrimSpace(result.Text)
	if hasToolCalls {
		output = responsesFunctionCallOutputItems(toolCalls)
		outputText = ""
	}
	completedAt := time.Now().Unix()
	textPayload := map[string]any{"format": map[string]any{"type": "text"}}
	if req.Text != nil && req.Text.Format != nil {
		if formatPayload, err := responseFormatPayload(req.Text.Format); err == nil && formatPayload != nil {
			textPayload = map[string]any{"format": formatPayload}
		}
	}
	response := ResponsesResponse{
		ID:                 reqID,
		Object:             "response",
		CreatedAt:          completedAt,
		OutputText:         outputText,
		Status:             "completed",
		CompletedAt:        &completedAt,
		Error:              nil,
		IncompleteDetails:  nil,
		Instructions:       nil,
		MaxOutputTokens:    nil,
		Model:              modelLabel,
		Output:             output,
		ParallelToolCalls:  true,
		PreviousResponseID: nil,
		Reasoning:          map[string]any{"effort": nil, "summary": nil},
		Store:              true,
		Temperature:        1.0,
		Text:               textPayload,
		ToolChoice:         "auto",
		Tools:              []any{},
		TopP:               1.0,
		Truncation:         "disabled",
		Usage:              ResponsesUsage{},
		User:               nil,
		Metadata:           map[string]any{},
	}
	respondJSON(w, 200, response)
}
