package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/store"
)

func (s *Server) handleOpenAIRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleModels(w, r)
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
			s.handleChatCompletions(w, r)
			return
		}
		if _, ok := anyBody["input"]; ok {
			r.Body = io.NopCloser(bytes.NewReader(body))
			s.handleResponses(w, r)
			return
		}
		respondErr(w, 400, "bad_request", "unsupported /v1 payload, use /v1/chat/completions or /v1/responses")
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := "req_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if !s.isValidAPIKey(r) {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return
	}
	selector := ""
	var req ChatCompletionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-5.2-codex"
	}
	req.Model = s.resolveMappedModel(req.Model)
	structuredSpec, err := normalizeResponseFormat(req.ResponseFormat)
	if err != nil {
		respondErr(w, 400, "invalid_request_error", err.Error())
		return
	}
	injectPrompt := s.shouldInjectDirectAPIPrompt() && s.currentAPIMode() != "direct_api"
	prompt := promptFromMessages(req.Messages)
	if injectPrompt {
		prompt = promptFromMessagesWithTools(req.Messages, req.Tools, req.ToolChoice)
	}
	directOpts := directCodexRequestOptions{
		Tools:      req.Tools,
		ToolChoice: req.ToolChoice,
		TextFormat: req.ResponseFormat,
	}
	account, tk, err := s.resolveAPIAccountWithTokens(r.Context(), selector)
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		switch {
		case strings.Contains(msg, "not found"):
			respondErr(w, 404, "account_not_found", err.Error())
		case strings.Contains(msg, "exhausted"):
			respondErr(w, 429, "quota_exhausted", "target account quota exhausted")
		default:
			respondErr(w, 500, "internal_error", err.Error())
		}
		return
	}
	setResolvedAccountHeaders(w, account)
	status := 200
	defer func() {
		_ = s.svc.Store.InsertAudit(r.Context(), store.AuditRecord{
			RequestID: reqID,
			AccountID: account.ID,
			Model:     req.Model,
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
		includeUsageChunk := req.StreamOpts != nil && req.StreamOpts.IncludeUsage
		if s.currentAPIMode() == "direct_api" {
			var streamedText strings.Builder
			stopKeepAlive := func() {}
			if bufferedToolsMode {
				stopKeepAlive = startSSEKeepAlive(r.Context(), w, flusher, resolveSSEKeepAliveInterval())
			}
			res, err := s.callDirectCodexResponsesAutoSwitch429(r.Context(), selector, &account, &tk, req.Model, prompt, directOpts, func(delta string) error {
				if bufferedToolsMode {
					streamedText.WriteString(delta)
					return nil
				}
				chunk := ChatCompletionsChunk{
					ID:      "chatcmpl-" + reqID,
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   req.Model,
					Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{Role: "assistant", Content: delta}}},
				}
				writeChatCompletionsChunk(w, flusher, chunk)
				return nil
			}, !bufferedToolsMode)
			stopKeepAlive()
			if err != nil {
				code, errType := classifyDirectUpstreamError(err)
				status = code
				_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"`+escape(err.Error())+`","type":"`+escape(errType)+`"}}`)
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			usage := Usage{PromptTokens: res.InputTokens, CompletionTokens: res.OutputTokens, TotalTokens: res.InputTokens + res.OutputTokens}
			if bufferedToolsMode {
				toolCalls, hasToolCalls := resolveToolCalls(res.Text, req.Tools, res.ToolCalls)
				if structuredSpec != nil {
					if hasToolCalls {
						status = 400
						_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"tool_calls not allowed when response_format is set","type":"invalid_response_format"}}`)
						_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
						flusher.Flush()
						return
					}
					text := strings.TrimSpace(streamedText.String())
					if text == "" {
						text = res.Text
					}
					if err := validateStructuredOutput(structuredSpec, text); err != nil {
						status = 400
						_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"`+escape(err.Error())+`","type":"invalid_response_format"}}`)
						_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
						flusher.Flush()
						return
					}
					streamChatCompletionText(w, flusher, "chatcmpl-"+reqID, req.Model, text, usage, includeUsageChunk)
					return
				}
				if hasToolCalls {
					streamChatCompletionToolCalls(w, flusher, "chatcmpl-"+reqID, req.Model, toolCalls, usage, includeUsageChunk)
					return
				}
				text := strings.TrimSpace(streamedText.String())
				if text == "" {
					text = res.Text
				}
				streamChatCompletionText(w, flusher, "chatcmpl-"+reqID, req.Model, text, usage, includeUsageChunk)
				return
			}
			streamChatCompletionFinalStop(w, flusher, "chatcmpl-"+reqID, req.Model, usage, includeUsageChunk)
			return
		}

		var streamedText strings.Builder
		stopKeepAlive := func() {}
		if bufferedToolsMode {
			stopKeepAlive = startSSEKeepAlive(r.Context(), w, flusher, resolveSSEKeepAliveInterval())
		}
		res, err := s.svc.Codex.StreamChat(r.Context(), s.svc.APICodexHome(account.ID), req.Model, prompt, func(evt provider.ChatEvent) error {
			if evt.Type != "delta" {
				return nil
			}
			if bufferedToolsMode {
				streamedText.WriteString(evt.Text)
				return nil
			}
			chunk := ChatCompletionsChunk{
				ID:      "chatcmpl-" + reqID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   req.Model,
				Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{Role: "assistant", Content: evt.Text}}},
			}
			writeChatCompletionsChunk(w, flusher, chunk)
			return nil
		})
		stopKeepAlive()
		if err != nil {
			status = 500
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"`+escape(err.Error())+`"}}`)
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		usage := Usage{PromptTokens: res.InputTokens, CompletionTokens: res.OutputTokens, TotalTokens: res.InputTokens + res.OutputTokens}
		if bufferedToolsMode {
			toolCalls, hasToolCalls := resolveToolCalls(res.Text, req.Tools, mapProviderToolCalls(res.ToolCalls))
			if structuredSpec != nil {
				if hasToolCalls {
					status = 400
					_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"tool_calls not allowed when response_format is set","type":"invalid_response_format"}}`)
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				text := strings.TrimSpace(streamedText.String())
				if text == "" {
					text = res.Text
				}
				if err := validateStructuredOutput(structuredSpec, text); err != nil {
					status = 400
					_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"`+escape(err.Error())+`","type":"invalid_response_format"}}`)
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				streamChatCompletionText(w, flusher, "chatcmpl-"+reqID, req.Model, text, usage, includeUsageChunk)
				return
			}
			if hasToolCalls {
				streamChatCompletionToolCalls(w, flusher, "chatcmpl-"+reqID, req.Model, toolCalls, usage, includeUsageChunk)
				return
			}
			text := strings.TrimSpace(streamedText.String())
			if text == "" {
				text = res.Text
			}
			streamChatCompletionText(w, flusher, "chatcmpl-"+reqID, req.Model, text, usage, includeUsageChunk)
			return
		}
		streamChatCompletionFinalStop(w, flusher, "chatcmpl-"+reqID, req.Model, usage, includeUsageChunk)
		return
	}

	if s.currentAPIMode() == "direct_api" {
		res, err := s.callDirectCodexResponsesAutoSwitch429(r.Context(), selector, &account, &tk, req.Model, prompt, directOpts, nil, false)
		if err != nil {
			status = 500
			code, errType := classifyDirectUpstreamError(err)
			status = code
			respondErr(w, code, errType, err.Error())
			return
		}
		toolCalls, hasToolCalls := resolveToolCalls(res.Text, req.Tools, res.ToolCalls)
		if structuredSpec != nil {
			if hasToolCalls {
				status = 400
				respondErr(w, 400, "invalid_response_format", "tool_calls not allowed when response_format is set")
				return
			}
			if err := validateStructuredOutput(structuredSpec, res.Text); err != nil {
				status = 400
				respondErr(w, 400, "invalid_response_format", err.Error())
				return
			}
		}
		choice := ChatChoice{
			Index:        0,
			Message:      ChatMessage{Role: "assistant", Content: res.Text},
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
			Model:   req.Model,
			Choices: []ChatChoice{choice},
			Usage:   Usage{PromptTokens: res.InputTokens, CompletionTokens: res.OutputTokens, TotalTokens: res.InputTokens + res.OutputTokens},
		}
		respondJSON(w, 200, resp)
		return
	}

	res, err := s.svc.Codex.Chat(r.Context(), s.svc.APICodexHome(account.ID), req.Model, prompt)
	if err != nil {
		status = 500
		respondErr(w, 500, "upstream_error", err.Error())
		return
	}
	toolCalls, hasToolCalls := resolveToolCalls(res.Text, req.Tools, mapProviderToolCalls(res.ToolCalls))
	if structuredSpec != nil {
		if hasToolCalls {
			status = 400
			respondErr(w, 400, "invalid_response_format", "tool_calls not allowed when response_format is set")
			return
		}
		if err := validateStructuredOutput(structuredSpec, res.Text); err != nil {
			status = 400
			respondErr(w, 400, "invalid_response_format", err.Error())
			return
		}
	}
	choice := ChatChoice{
		Index:        0,
		Message:      ChatMessage{Role: "assistant", Content: res.Text},
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
		Model:   req.Model,
		Choices: []ChatChoice{choice},
		Usage:   Usage{PromptTokens: res.InputTokens, CompletionTokens: res.OutputTokens, TotalTokens: res.InputTokens + res.OutputTokens},
	}
	respondJSON(w, 200, resp)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if !s.isValidAPIKey(r) {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return
	}
	selector := ""
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
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-5.2-codex"
	}
	req.Model = s.resolveMappedModel(req.Model)
	injectPrompt := s.shouldInjectDirectAPIPrompt() && s.currentAPIMode() != "direct_api"
	prompt := promptFromResponsesInput(req.Input, nil, nil)
	if injectPrompt {
		prompt = promptFromResponsesInput(req.Input, req.Tools, req.ToolChoice)
	}
	directOpts := directCodexRequestOptions{
		Tools:      req.Tools,
		ToolChoice: req.ToolChoice,
		TextFormat: nil,
	}
	if req.Text != nil {
		directOpts.TextFormat = req.Text.Format
	}
	if strings.TrimSpace(prompt) == "" {
		respondErr(w, 400, "bad_request", "input is required")
		return
	}
	account, tk, err := s.resolveAPIAccountWithTokens(r.Context(), selector)
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		switch {
		case strings.Contains(msg, "not found"):
			respondErr(w, 404, "account_not_found", err.Error())
		case strings.Contains(msg, "exhausted"):
			respondErr(w, 429, "quota_exhausted", "target account quota exhausted")
		default:
			respondErr(w, 500, "internal_error", err.Error())
		}
		return
	}
	setResolvedAccountHeaders(w, account)
	status := 200
	defer func() {
		_ = s.svc.Store.InsertAudit(r.Context(), store.AuditRecord{
			RequestID: reqID,
			AccountID: account.ID,
			Model:     req.Model,
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
		emit := func(event string, payload map[string]any) {
			if payload == nil {
				return
			}
			if _, exists := payload["sequence_number"]; !exists {
				seq++
				payload["sequence_number"] = seq
			}
			_ = event
			writeOpenAISSE(w, payload)
			flusher.Flush()
		}
		createdEvent := map[string]any{
			"type":     "response.created",
			"response": buildResponseObject(reqID, req.Model, "in_progress", []any{}, nil, responseCreatedAt),
		}
		emit("response.created", createdEvent)

		bufferedToolsMode := len(req.Tools) > 0 || structuredSpec != nil
		if s.currentAPIMode() == "direct_api" {
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
			result, err := s.callDirectCodexResponsesAutoSwitch429(r.Context(), selector, &account, &tk, req.Model, prompt, directOpts, func(delta string) error {
				if bufferedToolsMode {
					streamedText.WriteString(delta)
					return nil
				}
				streamedText.WriteString(delta)
				deltaEvent := map[string]any{
					"type":          "response.output_text.delta",
					"item_id":       textItemID,
					"output_index":  0,
					"content_index": 0,
					"delta":         delta,
					"logprobs":      []any{},
				}
				emit("response.output_text.delta", deltaEvent)
				return nil
			}, !bufferedToolsMode)
			stopKeepAlive()
			if err != nil {
				code, errType := classifyDirectUpstreamError(err)
				status = code
				emit("error", map[string]any{
					"type":    "error",
					"code":    errType,
					"message": err.Error(),
					"param":   nil,
				})
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			usage := ResponsesUsage{
				InputTokens:  result.InputTokens,
				OutputTokens: result.OutputTokens,
				TotalTokens:  result.InputTokens + result.OutputTokens,
			}
			if bufferedToolsMode {
				toolCalls, hasToolCalls := resolveToolCalls(result.Text, req.Tools, result.ToolCalls)
				if structuredSpec != nil {
					if hasToolCalls {
						status = 400
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
						status = 400
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
					streamResponsesText(emit, reqID, req.Model, text, usage, responseCreatedAt)
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				if hasToolCalls {
					streamResponsesFunctionCalls(emit, reqID, req.Model, toolCalls, usage, responseCreatedAt)
					_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					return
				}
				text := strings.TrimSpace(streamedText.String())
				if text == "" {
					text = strings.TrimSpace(result.Text)
				}
				streamResponsesText(emit, reqID, req.Model, text, usage, responseCreatedAt)
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
			completedEvent := map[string]any{
				"type": "response.completed",
				"response": buildResponseObject(reqID, req.Model, "completed", []any{outputItem}, map[string]any{
					"input_tokens":  usage.InputTokens,
					"output_tokens": usage.OutputTokens,
					"total_tokens":  usage.TotalTokens,
				}, responseCreatedAt),
			}
			emit("response.completed", completedEvent)
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}

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
		result, err := s.svc.Codex.StreamChat(r.Context(), s.svc.APICodexHome(account.ID), req.Model, prompt, func(evt provider.ChatEvent) error {
			if evt.Type != "delta" {
				return nil
			}
			if bufferedToolsMode {
				streamedText.WriteString(evt.Text)
				return nil
			}
			streamedText.WriteString(evt.Text)
			deltaEvent := map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       textItemID,
				"output_index":  0,
				"content_index": 0,
				"delta":         evt.Text,
				"logprobs":      []any{},
			}
			emit("response.output_text.delta", deltaEvent)
			return nil
		})
		stopKeepAlive()
		if err != nil {
			status = 500
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
		usage := ResponsesUsage{
			InputTokens:  result.InputTokens,
			OutputTokens: result.OutputTokens,
			TotalTokens:  result.InputTokens + result.OutputTokens,
		}
		if bufferedToolsMode {
			toolCalls, hasToolCalls := resolveToolCalls(result.Text, req.Tools, mapProviderToolCalls(result.ToolCalls))
			if structuredSpec != nil {
				if hasToolCalls {
					status = 400
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
					status = 400
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
				streamResponsesText(emit, reqID, req.Model, text, usage, responseCreatedAt)
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			if hasToolCalls {
				streamResponsesFunctionCalls(emit, reqID, req.Model, toolCalls, usage, responseCreatedAt)
				_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}
			text := strings.TrimSpace(streamedText.String())
			if text == "" {
				text = strings.TrimSpace(result.Text)
			}
			streamResponsesText(emit, reqID, req.Model, text, usage, responseCreatedAt)
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
		completedEvent := map[string]any{
			"type": "response.completed",
			"response": buildResponseObject(reqID, req.Model, "completed", []any{outputItem}, map[string]any{
				"input_tokens":  usage.InputTokens,
				"output_tokens": usage.OutputTokens,
				"total_tokens":  usage.TotalTokens,
			}, responseCreatedAt),
		}
		emit("response.completed", completedEvent)
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	if s.currentAPIMode() == "direct_api" {
		result, err := s.callDirectCodexResponsesAutoSwitch429(r.Context(), selector, &account, &tk, req.Model, prompt, directOpts, nil, false)
		if err != nil {
			code, errType := classifyDirectUpstreamError(err)
			status = code
			respondErr(w, code, errType, err.Error())
			return
		}
		toolCalls, hasToolCalls := resolveToolCalls(result.Text, req.Tools, result.ToolCalls)
		if structuredSpec != nil {
			if hasToolCalls {
				status = 400
				respondErr(w, 400, "invalid_response_format", "tool_calls not allowed when text.format is set")
				return
			}
			if err := validateStructuredOutput(structuredSpec, result.Text); err != nil {
				status = 400
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
		resp := ResponsesResponse{
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
			Model:              req.Model,
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
			Usage: ResponsesUsage{
				InputTokens:  result.InputTokens,
				OutputTokens: result.OutputTokens,
				TotalTokens:  result.InputTokens + result.OutputTokens,
			},
			User:     nil,
			Metadata: map[string]any{},
		}
		respondJSON(w, 200, resp)
		return
	}

	result, err := s.svc.Codex.Chat(r.Context(), s.svc.APICodexHome(account.ID), req.Model, prompt)
	if err != nil {
		status = 500
		respondErr(w, 500, "upstream_error", err.Error())
		return
	}
	toolCalls, hasToolCalls := resolveToolCalls(result.Text, req.Tools, mapProviderToolCalls(result.ToolCalls))
	if structuredSpec != nil {
		if hasToolCalls {
			status = 400
			respondErr(w, 400, "invalid_response_format", "tool_calls not allowed when text.format is set")
			return
		}
		if err := validateStructuredOutput(structuredSpec, result.Text); err != nil {
			status = 400
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
	resp := ResponsesResponse{
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
		Model:              req.Model,
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
		Usage: ResponsesUsage{
			InputTokens:  result.InputTokens,
			OutputTokens: result.OutputTokens,
			TotalTokens:  result.InputTokens + result.OutputTokens,
		},
		User:     nil,
		Metadata: map[string]any{},
	}
	respondJSON(w, 200, resp)
}
