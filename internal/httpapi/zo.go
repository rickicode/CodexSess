package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/config"
	"github.com/ricki/codexsess/internal/store"
)

const zoAskEndpoint = "https://api.zo.computer/zo/ask"
const zoConversationTTL = 30 * time.Minute

type zoAskRequest struct {
	Input          string          `json:"input"`
	ModelName      string          `json:"model_name,omitempty"`
	PersonaID      string          `json:"persona_id,omitempty"`
	ConversationID string          `json:"conversation_id,omitempty"`
	OutputFormat   json.RawMessage `json:"output_format,omitempty"`
	Stream         bool            `json:"stream,omitempty"`
}

type zoAskResponse struct {
	Output         any    `json:"output"`
	Response       any    `json:"response"`
	Text           any    `json:"text"`
	ConversationID string `json:"conversation_id"`
}

type zoAskResult struct {
	Text           string
	ConversationID string
}

func (s *Server) handleZoV1Root(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleZoModels(w, r)
		return
	case http.MethodPost:
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		var anyBody map[string]any
		if err := json.Unmarshal(body, &anyBody); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON body")
			return
		}
		if _, ok := anyBody["messages"]; ok {
			r.Body = io.NopCloser(bytes.NewReader(body))
			s.handleZoChatCompletions(w, r)
			return
		}
		respondErr(w, 400, "bad_request", "unsupported /zo/v1 payload, use /zo/v1/chat/completions")
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) handleZoNotSupported(w http.ResponseWriter, r *http.Request) {
	respondErr(w, http.StatusNotFound, "not_supported", "Zo proxy only supports /zo/v1/chat/completions")
}

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

func (s *Server) handleZoModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	key, rawKey, err := s.resolveZoKeyForRequest(r.Context())
	if err != nil {
		respondErr(w, 503, "zo_api_key_missing", err.Error())
		return
	}
	_, _ = s.svc.IncrementZoAPIKeyUsage(r.Context(), key.ID, 1)
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://api.zo.computer/models/available", nil)
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(rawKey))
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		respondErr(w, 502, "upstream_error", err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respondErr(w, 502, "upstream_error", fmt.Sprintf("zo api status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body))))
		return
	}
	var parsed struct {
		Models []struct {
			ModelName string `json:"model_name"`
			Vendor    string `json:"vendor"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		respondErr(w, 502, "upstream_error", "failed to parse zo models")
		return
	}
	out := ModelsResponse{
		Object: "list",
		Data:   make([]ModelInfo, 0, len(parsed.Models)),
	}
	for _, item := range parsed.Models {
		id := strings.TrimSpace(item.ModelName)
		if id == "" {
			continue
		}
		out.Data = append(out.Data, ModelInfo{
			ID:      id,
			Object:  "model",
			Created: time.Now().Unix(),
			OwnedBy: strings.TrimSpace(item.Vendor),
		})
	}
	respondJSON(w, 200, out)
}

func (s *Server) resolveZoKeyForRequest(ctx context.Context) (store.ZoAPIKey, string, error) {
	s.mu.RLock()
	strategy := config.NormalizeZoAPIStrategy(s.svc.Cfg.ZoAPIStrategy)
	s.mu.RUnlock()
	return s.svc.SelectZoAPIKeyForRequest(ctx, strategy)
}

func (s *Server) resolveZoKeyForClaudeMessages(ctx context.Context) (store.ZoAPIKey, string, error) {
	return s.resolveZoKeyForRequest(ctx)
}

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

func callZoAsk(ctx context.Context, rawKey string, req zoAskRequest) (zoAskResult, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return zoAskResult{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, zoAskEndpoint, bytes.NewReader(payload))
	if err != nil {
		return zoAskResult{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(rawKey))
	httpReq.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return zoAskResult{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zoAskResult{}, fmt.Errorf("zo api status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed zoAskResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return zoAskResult{}, err
	}
	text := firstNonEmpty(coerceAnyText(parsed.Output), coerceAnyText(parsed.Response), coerceAnyText(parsed.Text))
	if strings.TrimSpace(text) == "" {
		return zoAskResult{}, fmt.Errorf("empty response from zo api")
	}
	result := zoAskResult{
		Text:           text,
		ConversationID: strings.TrimSpace(parsed.ConversationID),
	}
	if result.ConversationID == "" {
		result.ConversationID = strings.TrimSpace(resp.Header.Get("x-conversation-id"))
	}
	if result.ConversationID == "" {
		result.ConversationID = strings.TrimSpace(resp.Header.Get("X-Conversation-Id"))
	}
	return result, nil
}

func isZoConversationBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "status=409") && strings.Contains(msg, "conversation is busy")
}

func callZoAskStream(ctx context.Context, rawKey string, req zoAskRequest, onDelta func(string) error) (zoAskResult, error) {
	req.Stream = true
	payload, err := json.Marshal(req)
	if err != nil {
		return zoAskResult{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, zoAskEndpoint, bytes.NewReader(payload))
	if err != nil {
		return zoAskResult{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(rawKey))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(httpReq)
	if err != nil {
		return zoAskResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
		return zoAskResult{}, fmt.Errorf("zo api status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 128*1024), 8*1024*1024)
	var (
		result     zoAskResult
		eventType  string
		dataLines  []string
		eventIsEnd bool
	)
	flushFrame := func() error {
		if len(dataLines) == 0 {
			eventType = ""
			return nil
		}
		payload := strings.TrimSpace(strings.Join(dataLines, "\n"))
		dataLines = dataLines[:0]
		if payload == "" || payload == "[DONE]" {
			eventType = ""
			return nil
		}
		var evt map[string]any
		if err := json.Unmarshal([]byte(payload), &evt); err != nil {
			eventType = ""
			return nil
		}
		if conv := strings.TrimSpace(coerceAnyText(evt["conversation_id"])); conv != "" {
			result.ConversationID = conv
		}
		if errMsg := strings.TrimSpace(coerceAnyText(evt["message"])); errMsg != "" && strings.Contains(strings.ToLower(eventType), "error") {
			return fmt.Errorf("zo api error: %s", errMsg)
		}
		delta := ""
		switch strings.ToLower(strings.TrimSpace(eventType)) {
		case "frontendmodelresponse":
			delta = coerceAnyText(evt["content"])
		case "end":
			if strings.TrimSpace(result.Text) == "" {
				delta = firstNonEmpty(coerceAnyText(evt["output"]), coerceAnyText(evt["content"]))
			}
		default:
			delta = firstNonEmpty(
				coerceAnyText(evt["delta"]),
				coerceAnyText(evt["output"]),
				coerceAnyText(evt["response"]),
				coerceAnyText(evt["text"]),
				coerceAnyText(evt["content"]),
			)
		}
		if delta != "" {
			result.Text += delta
			if onDelta != nil {
				if err := onDelta(delta); err != nil {
					return err
				}
			}
		}
		if strings.Contains(strings.ToLower(eventType), "end") {
			eventIsEnd = true
		}
		eventType = ""
		return nil
	}

	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			if err := flushFrame(); err != nil {
				return zoAskResult{}, err
			}
			if eventIsEnd {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := sc.Err(); err != nil {
		return zoAskResult{}, err
	}
	if err := flushFrame(); err != nil {
		return zoAskResult{}, err
	}
	if result.ConversationID == "" {
		result.ConversationID = strings.TrimSpace(resp.Header.Get("x-conversation-id"))
	}
	if result.ConversationID == "" {
		result.ConversationID = strings.TrimSpace(resp.Header.Get("X-Conversation-Id"))
	}
	if strings.TrimSpace(result.Text) == "" {
		return zoAskResult{}, fmt.Errorf("empty response from zo api")
	}
	return result, nil
}

func setZoConversationHeaders(w http.ResponseWriter, conversationID string) {
	conv := strings.TrimSpace(conversationID)
	if conv == "" || w == nil {
		return
	}
	w.Header().Set("x-conversation-id", conv)
	w.Header().Set("X-Zo-Conversation-Id", conv)
}
