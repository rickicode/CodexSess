package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/store"
)

func (s *Server) handleClaudeMessages(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	anthropicVersion := normalizeAnthropicVersion(r.Header.Get("anthropic-version"))
	if r.Method != http.MethodPost {
		respondClaudeErr(w, 405, "invalid_request_error", "method not allowed", reqID)
		return
	}
	if !s.isValidAPIKey(r) {
		respondClaudeErr(w, 401, "authentication_error", "invalid API key", reqID)
		return
	}
	w.Header().Set("anthropic-version", anthropicVersion)
	w.Header().Set("request-id", reqID)
	selector := ""
	var req ClaudeMessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondClaudeErr(w, 400, "invalid_request_error", "invalid JSON body", reqID)
		return
	}
	s.enforceClaudeCodexOnlyConfig()
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-5.2-codex"
	}
	if strings.Contains(strings.TrimSpace(req.Model), ":") {
		req.Model = "gpt-5.2-codex"
	}
	if req.MaxTokens <= 0 {
		respondClaudeErr(w, 400, "invalid_request_error", "max_tokens must be greater than 0", reqID)
		return
	}
	req.Model = s.resolveMappedModel(req.Model)
	toolDefs := mapClaudeToolsToChatTools(req.Tools)
	sessionKey := deriveClaudeSessionKey(req, r)
	sanitizedMessages := s.sanitizeClaudeMessagesForPrompt(req.Messages, toolDefs, sessionKey)
	budgetedMessages, budgetedSystem := applyClaudeTokenBudgetGuard(sanitizedMessages, req.System)
	injectPrompt := s.shouldInjectDirectAPIPrompt() && s.currentAPIMode() != "direct_api"
	prompt := promptFromClaudeMessagesWithSystemAndTools(budgetedMessages, budgetedSystem, nil, nil)
	if injectPrompt {
		prompt = promptFromClaudeMessagesWithSystemAndTools(budgetedMessages, budgetedSystem, toolDefs, req.ToolChoice)
	}
	prompt = applyClaudeResponseDefaults(prompt)
	directOpts := directCodexRequestOptions{
		MaxOutputTokens: req.MaxTokens,
		StopSequences:   req.StopSequences,
		Tools:           toolDefs,
		ToolChoice:      req.ToolChoice,
		ClaudeProtocol:  true,
		AnthropicVer:    anthropicVersion,
	}
	if strings.TrimSpace(prompt) == "" {
		respondClaudeErr(w, 400, "invalid_request_error", "messages are required", reqID)
		return
	}
	account, tk, err := s.resolveAPIAccountWithTokens(r.Context(), selector)
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		switch {
		case strings.Contains(msg, "not found"):
			respondClaudeErr(w, 404, "not_found_error", err.Error(), reqID)
		case strings.Contains(msg, "exhausted"):
			respondClaudeErr(w, 429, "rate_limit_error", "target account quota exhausted", reqID)
		default:
			respondClaudeErr(w, 500, "api_error", err.Error(), reqID)
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
				"model":         req.Model,
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
			streamRes    provider.ChatResult
			directRes    directAPIResult
			usage        ClaudeMessagesUsage
			toolCalls    []ChatToolCall
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

		if s.currentAPIMode() == "direct_api" {
			directRes, err = s.callDirectCodexResponsesAutoSwitch429(r.Context(), selector, &account, &tk, req.Model, prompt, directOpts, onDelta, false)
			if err != nil {
				code, errType := classifyDirectUpstreamClaudeError(err)
				status = code
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
			usage = ClaudeMessagesUsage{
				InputTokens:  directRes.InputTokens,
				OutputTokens: directRes.OutputTokens,
			}
			toolCalls, _ = resolveToolCalls(directRes.Text, toolDefs, directRes.ToolCalls)
			toolCalls = sanitizeClaudeClientToolCalls(toolCalls)
		} else {
			streamRes, err = s.svc.Codex.StreamChat(r.Context(), s.svc.APICodexHome(account.ID), req.Model, prompt, func(evt provider.ChatEvent) error {
				if evt.Type != "delta" {
					return nil
				}
				return onDelta(evt.Text)
			})
			if err != nil {
				status = 500
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
			usage = ClaudeMessagesUsage{
				InputTokens:  streamRes.InputTokens,
				OutputTokens: streamRes.OutputTokens,
			}
			toolCalls, _ = resolveToolCalls(streamRes.Text, toolDefs, mapProviderToolCalls(streamRes.ToolCalls))
			toolCalls = sanitizeClaudeClientToolCalls(toolCalls)
		}

		streamMu.Lock()
		finalText := strings.TrimSpace(streamedText.String())
		streamMu.Unlock()
		if finalText == "" {
			if s.currentAPIMode() == "direct_api" {
				finalText = strings.TrimSpace(directRes.Text)
			} else {
				finalText = strings.TrimSpace(streamRes.Text)
			}
		}
		finalText = sanitizeClaudeAssistantText(finalText)
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
			writeSSE(w, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": contentIdx,
			})
			flusher.Flush()
			contentIdx++
		}

		for _, call := range toolCalls {
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
					"type": "tool_use",
					"id":   strings.TrimSpace(call.ID),
					"name": strings.TrimSpace(call.Function.Name),
					// Claude SSE tool-use payload should be materialized via input_json_delta.
					"input": map[string]any{},
				},
			})
			writeSSE(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": contentIdx,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": toolInputJSON,
				},
			})
			writeSSE(w, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": contentIdx,
			})
			flusher.Flush()
			contentIdx++
		}

		stopReason := "end_turn"
		if len(toolCalls) > 0 {
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
		return
	}

	if s.currentAPIMode() == "direct_api" {
		res, err := s.callDirectCodexResponsesAutoSwitch429(r.Context(), selector, &account, &tk, req.Model, prompt, directOpts, nil, false)
		if err != nil {
			code, errType := classifyDirectUpstreamClaudeError(err)
			status = code
			respondClaudeErr(w, code, errType, err.Error(), reqID)
			return
		}
		sanitizedText := sanitizeClaudeAssistantText(res.Text)
		toolCalls, _ := resolveToolCalls(sanitizedText, toolDefs, res.ToolCalls)
		toolCalls = sanitizeClaudeClientToolCalls(toolCalls)
		content, stopReason := buildClaudeResponseContent(sanitizedText, toolCalls)
		resp := ClaudeMessagesResponse{
			ID:         reqID,
			Type:       "message",
			Role:       "assistant",
			Model:      req.Model,
			Content:    content,
			StopReason: stopReason,
			Usage: ClaudeMessagesUsage{
				InputTokens:  res.InputTokens,
				OutputTokens: res.OutputTokens,
			},
		}
		respondJSON(w, 200, resp)
		return
	}

	res, err := s.svc.Codex.Chat(r.Context(), s.svc.APICodexHome(account.ID), req.Model, prompt)
	if err != nil {
		status = 500
		respondClaudeErr(w, 500, "api_error", err.Error(), reqID)
		return
	}
	sanitizedText := sanitizeClaudeAssistantText(res.Text)
	toolCalls, _ := resolveToolCalls(sanitizedText, toolDefs, mapProviderToolCalls(res.ToolCalls))
	toolCalls = sanitizeClaudeClientToolCalls(toolCalls)
	content, stopReason := buildClaudeResponseContent(sanitizedText, toolCalls)
	resp := ClaudeMessagesResponse{
		ID:         reqID,
		Type:       "message",
		Role:       "assistant",
		Model:      req.Model,
		Content:    content,
		StopReason: stopReason,
		Usage: ClaudeMessagesUsage{
			InputTokens:  res.InputTokens,
			OutputTokens: res.OutputTokens,
		},
	}
	respondJSON(w, 200, resp)
}

func normalizeAnthropicVersion(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return "2023-06-01"
	}
	return trimmed
}

func respondClaudeErr(w http.ResponseWriter, code int, errType, msg, requestID string) {
	body := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    strings.TrimSpace(errType),
			"message": strings.TrimSpace(msg),
		},
	}
	if rid := strings.TrimSpace(requestID); rid != "" {
		body["request_id"] = rid
	}
	respondJSON(w, code, body)
}

func classifyDirectUpstreamError(err error) (int, string) {
	if err == nil {
		return 500, "upstream_error"
	}
	var httpErr *directAPIHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case 400:
			return 400, "bad_request"
		case 401:
			return 401, "unauthorized"
		case 403:
			return 403, "forbidden"
		case 404:
			return 404, "not_found"
		case 408:
			return 408, "timeout"
		case 409:
			return 409, "conflict"
		case 422:
			return 422, "unprocessable_entity"
		case 429:
			return 429, "quota_exhausted"
		default:
			if httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599 {
				return httpErr.StatusCode, "upstream_error"
			}
			if httpErr.StatusCode > 0 {
				return httpErr.StatusCode, "upstream_error"
			}
		}
	}
	return 500, "upstream_error"
}

func classifyDirectUpstreamClaudeError(err error) (int, string) {
	if err == nil {
		return 500, "api_error"
	}
	var httpErr *directAPIHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case 400:
			return 400, "invalid_request_error"
		case 401:
			return 401, "authentication_error"
		case 403:
			return 403, "permission_error"
		case 404:
			return 404, "not_found_error"
		case 408:
			return 408, "timeout_error"
		case 429:
			return 429, "rate_limit_error"
		default:
			if httpErr.StatusCode >= 500 && httpErr.StatusCode <= 599 {
				return httpErr.StatusCode, "api_error"
			}
			if httpErr.StatusCode > 0 {
				return httpErr.StatusCode, "api_error"
			}
		}
	}
	return 500, "api_error"
}

func mapClaudeToolsToChatTools(tools []ClaudeToolDef) []ChatToolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]ChatToolDef, 0, len(tools))
	for _, t := range tools {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			continue
		}
		if shouldBlockClaudeClientToolName(name) {
			continue
		}
		out = append(out, ChatToolDef{
			Type: "function",
			Function: ChatToolFunctionDef{
				Name:        name,
				Description: strings.TrimSpace(t.Description),
				Parameters:  t.InputSchema,
			},
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func shouldBlockClaudeClientToolName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	if !shouldBlockClaudeTaskTools() {
		return false
	}
	return strings.HasPrefix(lower, "task")
}

func shouldBlockClaudeTaskTools() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("CODEXSESS_CLAUDE_BLOCK_TASK_TOOLS")))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func sanitizeClaudeClientToolCalls(calls []ChatToolCall) []ChatToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ChatToolCall, 0, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			continue
		}
		if shouldBlockClaudeClientToolName(name) {
			continue
		}
		call.Function.Arguments = sanitizeClaudeToolCallArguments(name, call.Function.Arguments)
		out = append(out, call)
	}
	return out
}

func sanitizeClaudeToolCallArguments(name string, raw string) string {
	args := strings.TrimSpace(raw)
	if args == "" || !json.Valid([]byte(args)) {
		return raw
	}
	if strings.ToLower(strings.TrimSpace(name)) != "read" {
		return raw
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(args), &obj); err != nil || obj == nil {
		return raw
	}
	if pages := strings.TrimSpace(coerceAnyText(obj["pages"])); pages == "" {
		delete(obj, "pages")
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return string(b)
}

func buildClaudeResponseContent(text string, calls []ChatToolCall) ([]ClaudeContentBlock, string) {
	content := make([]ClaudeContentBlock, 0, len(calls)+1)
	trimmedText := strings.TrimSpace(text)
	if trimmedText != "" {
		content = append(content, ClaudeContentBlock{
			Type: "text",
			Text: trimmedText,
		})
	}
	for _, call := range calls {
		name := strings.TrimSpace(call.Function.Name)
		if name == "" {
			continue
		}
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			callID = "toolu_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		content = append(content, ClaudeContentBlock{
			Type:  "tool_use",
			ID:    callID,
			Name:  name,
			Input: parseToolArgumentsForClaude(call.Function.Arguments),
		})
	}
	if len(content) == 0 {
		content = append(content, ClaudeContentBlock{
			Type: "text",
			Text: "",
		})
		return content, "end_turn"
	}
	if len(calls) > 0 {
		return content, "tool_use"
	}
	return content, "end_turn"
}

func parseToolArgumentsForClaude(arguments string) any {
	raw := strings.TrimSpace(arguments)
	if raw == "" {
		return map[string]any{}
	}
	var out any
	if json.Unmarshal([]byte(raw), &out) == nil {
		return out
	}
	return map[string]any{"raw": raw}
}

func promptFromMessages(msgs []ChatMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "user"
		}
		sb.WriteString(role)
		if role == "tool" && strings.TrimSpace(m.ToolCallID) != "" {
			sb.WriteString("(")
			sb.WriteString(strings.TrimSpace(m.ToolCallID))
			sb.WriteString(")")
		}
		sb.WriteString(": ")
		sb.WriteString(extractOpenAIContentText(m.Content))
		if len(m.ToolCalls) > 0 {
			sb.WriteString("\nassistant_tool_calls: ")
			for i, tc := range m.ToolCalls {
				if i > 0 {
					sb.WriteString(" | ")
				}
				sb.WriteString(strings.TrimSpace(tc.Function.Name))
				sb.WriteString("(")
				sb.WriteString(strings.TrimSpace(tc.Function.Arguments))
				sb.WriteString(")")
			}
		}
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func extractOpenAIContentText(raw any) string {
	if raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		for _, it := range v {
			obj, ok := it.(map[string]any)
			if !ok {
				continue
			}
			t, _ := obj["type"].(string)
			if t != "" && t != "text" {
				continue
			}
			text, _ := obj["text"].(string)
			if strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	case map[string]any:
		text, _ := v["text"].(string)
		return strings.TrimSpace(text)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		var s string
		if err := json.Unmarshal(b, &s); err == nil {
			return strings.TrimSpace(s)
		}
		var items []map[string]any
		if err := json.Unmarshal(b, &items); err == nil {
			var parts []string
			for _, it := range items {
				t, _ := it["type"].(string)
				if t != "" && t != "text" {
					continue
				}
				text, _ := it["text"].(string)
				if strings.TrimSpace(text) != "" {
					parts = append(parts, strings.TrimSpace(text))
				}
			}
			return strings.TrimSpace(strings.Join(parts, "\n"))
		}
		return ""
	}
}

func promptFromMessagesWithTools(msgs []ChatMessage, tools []ChatToolDef, toolChoice json.RawMessage) string {
	base := promptFromMessages(msgs)
	return promptFromTextWithTools(base, tools, toolChoice)
}

func promptFromTextWithTools(base string, tools []ChatToolDef, toolChoice json.RawMessage) string {
	if len(tools) == 0 {
		return base
	}
	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n\nAVAILABLE_TOOLS_JSON:\n")
	sb.WriteString("[\n")
	for i, t := range tools {
		if i > 0 {
			sb.WriteString(",\n")
		}
		b, _ := json.Marshal(toolDefForPrompt(t))
		sb.WriteString(string(b))
	}
	sb.WriteString("\n]\n")
	if len(bytes.TrimSpace(toolChoice)) > 0 {
		sb.WriteString("TOOL_CHOICE_JSON:\n")
		sb.WriteString(strings.TrimSpace(string(toolChoice)))
		sb.WriteString("\n")
	}
	sb.WriteString("TOOL_OUTPUT_RULES:\n")
	sb.WriteString("- If a tool is required, respond with JSON only.\n")
	sb.WriteString("- JSON format must be exactly: {\"tool_calls\":[{\"name\":\"<tool_name>\",\"arguments\":{...}}]}.\n")
	sb.WriteString("- Do not wrap JSON in markdown fences.\n")
	sb.WriteString("- If no tool is needed, respond normally with plain assistant text.\n")
	return strings.TrimSpace(sb.String())
}

func promptFromClaudeMessages(msgs []ClaudeMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "user"
		}
		textParts, toolCallParts, toolResultParts := extractClaudeMessageParts(m.Content)
		sb.WriteString(role)
		sb.WriteString(": ")
		sb.WriteString(strings.TrimSpace(strings.Join(textParts, "\n")))
		if len(toolCallParts) > 0 {
			sb.WriteString("\nassistant_tool_calls: ")
			sb.WriteString(strings.Join(toolCallParts, " | "))
		}
		for _, tr := range toolResultParts {
			sb.WriteString("\n")
			sb.WriteString(tr)
		}
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func promptFromClaudeMessagesWithSystemAndTools(
	msgs []ClaudeMessage,
	system json.RawMessage,
	tools []ChatToolDef,
	toolChoice json.RawMessage,
) string {
	base := promptFromClaudeMessages(msgs)
	if systemText := extractClaudeSystemText(system); systemText != "" {
		if strings.TrimSpace(base) != "" {
			base = "system: " + systemText + "\n" + base
		} else {
			base = "system: " + systemText
		}
	}
	return promptFromTextWithTools(base, tools, toolChoice)
}

func (s *Server) sanitizeClaudeMessagesForPrompt(messages []ClaudeMessage, toolDefs []ChatToolDef, sessionKey string) []ClaudeMessage {
	if len(messages) == 0 {
		return messages
	}
	droppedToolUseIDs := map[string]struct{}{}
	out := make([]ClaudeMessage, 0, len(messages))
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		contentRaw := bytes.TrimSpace(msg.Content)
		if len(contentRaw) == 0 {
			continue
		}
		var items []map[string]any
		if err := json.Unmarshal(contentRaw, &items); err != nil {
			out = append(out, msg)
			continue
		}
		kept := make([]map[string]any, 0, len(items))
		for _, item := range items {
			typ := strings.ToLower(strings.TrimSpace(coerceAnyText(item["type"])))
			if typ == "" || typ == "text" {
				text := coerceAnyText(item["text"])
				cleaned, keep := sanitizeClaudePromptText(role, text)
				if !keep {
					continue
				}
				item["text"] = cleaned
			}
			if role == "assistant" && typ == "tool_use" {
				name := strings.TrimSpace(coerceAnyText(item["name"]))
				toolUseID := strings.TrimSpace(coerceAnyText(item["id"]))
				if strings.EqualFold(name, "skill") {
					if toolUseID != "" {
						droppedToolUseIDs[toolUseID] = struct{}{}
					}
					continue
				}
				args := coerceAnyJSON(item["input"])
				if def, ok := findToolDefByName(toolDefs, name); ok {
					missing := missingRequiredToolFields(def, args)
					if len(missing) > 0 {
						if toolUseID != "" {
							droppedToolUseIDs[toolUseID] = struct{}{}
						}
						s.rememberInvalidToolPattern(sessionKey, name, missing)
						continue
					}
				}
				if s.hasInvalidToolPattern(sessionKey, name, nil) {
					if toolUseID != "" {
						droppedToolUseIDs[toolUseID] = struct{}{}
					}
					continue
				}
			}
			if role == "user" && typ == "tool_result" {
				toolUseID := strings.TrimSpace(coerceAnyText(item["tool_use_id"]))
				if _, dropped := droppedToolUseIDs[toolUseID]; dropped {
					continue
				}
				cleanedResult, keep := sanitizeClaudeToolResultText(extractClaudeToolResultValue(item["content"]))
				if !keep {
					continue
				}
				item["content"] = cleanedResult
				content := strings.ToLower(strings.TrimSpace(cleanedResult))
				if strings.Contains(content, "required parameter") && strings.Contains(content, "missing") {
					continue
				}
			}
			kept = append(kept, item)
		}
		if len(kept) == 0 {
			continue
		}
		b, err := json.Marshal(kept)
		if err != nil {
			continue
		}
		out = append(out, ClaudeMessage{
			Role:    msg.Role,
			Content: json.RawMessage(b),
		})
	}
	if len(out) == 0 {
		return messages
	}
	return out
}

func sanitizeClaudePromptText(role, text string) (string, bool) {
	cleaned := stripSystemReminderBlocks(text)
	if strings.TrimSpace(cleaned) == "" {
		return "", false
	}
	if strings.EqualFold(strings.TrimSpace(role), "assistant") && isLikelyPolicyRefusalText(cleaned) {
		return "", false
	}
	return cleaned, true
}

func stripSystemReminderBlocks(text string) string {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	for {
		start := strings.Index(lower, "<system-reminder>")
		if start < 0 {
			break
		}
		endRel := strings.Index(lower[start:], "</system-reminder>")
		if endRel < 0 {
			raw = strings.TrimSpace(raw[:start])
			break
		}
		end := start + endRel + len("</system-reminder>")
		raw = strings.TrimSpace(raw[:start] + "\n" + raw[end:])
		lower = strings.ToLower(raw)
	}
	return strings.TrimSpace(raw)
}

func isLikelyPolicyRefusalText(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}
	patterns := []string{
		"maaf, saya tidak bisa membantu",
		"maaf, saya tidak dapat membantu",
		"i can't help with",
		"i cannot help with",
		"i'm sorry, i can't help",
		"berpotensi disalahgunakan",
		"could be misused",
	}
	for _, pattern := range patterns {
		if strings.Contains(normalized, pattern) {
			return true
		}
	}
	return false
}

func sanitizeClaudeAssistantText(text string) string {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ""
	}
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if shouldDropClaudeTraceLine(trimmed) {
			continue
		}
		out = append(out, trimmed)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func sanitizeClaudeToolResultText(text string) (string, bool) {
	cleaned := stripSystemReminderBlocks(text)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return "", false
	}
	if isNoisyToolResultText(cleaned) {
		return "", false
	}
	const maxToolResultChars = 2800
	if len(cleaned) > maxToolResultChars {
		cleaned = strings.TrimSpace(cleaned[:maxToolResultChars]) + "\n...[truncated]"
	}
	return cleaned, true
}

func isNoisyToolResultText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return true
	}
	if strings.Contains(lower, "invalid pages parameter") {
		return true
	}
	if strings.Contains(lower, "exceeds maximum allowed tokens") {
		return true
	}
	if strings.Contains(lower, "this is a read-only task") {
		return true
	}
	return false
}

func shouldDropClaudeTraceLine(line string) bool {
	normalized := strings.TrimSpace(line)
	lower := strings.ToLower(normalized)
	if lower == "" {
		return true
	}
	if strings.Contains(lower, "entered plan mode") {
		return true
	}
	if strings.Contains(lower, "successfully loaded skill") {
		return true
	}
	if strings.Contains(lower, "ctrl+b") {
		return true
	}
	if strings.HasPrefix(lower, "1 tasks (") || claudeTaskCountLinePattern.MatchString(lower) {
		return true
	}
	if strings.HasPrefix(lower, "◼ ") {
		return true
	}
	if strings.HasPrefix(lower, "● skill(") || strings.HasPrefix(lower, "skill(") {
		return true
	}
	if strings.HasPrefix(lower, "⎿") {
		return true
	}
	if claudeTraceCallLinePattern.MatchString(normalized) {
		return true
	}
	return false
}

func applyClaudeResponseDefaults(prompt string) string {
	base := strings.TrimSpace(prompt)
	if base == "" {
		return ""
	}
	defaults := strings.Join([]string{
		"system: Response defaults:",
		"- For broad analysis/debug requests, begin with a best-effort system analysis immediately.",
		"- Make reasonable assumptions and proceed; avoid asking scope-first questions unless truly blocked.",
		"- Prefer actionable findings first, then assumptions/open questions.",
	}, "\n")
	return strings.TrimSpace(defaults + "\n\n" + base)
}

func applyClaudeTokenBudgetGuard(messages []ClaudeMessage, system json.RawMessage) ([]ClaudeMessage, json.RawMessage) {
	msgs := cloneClaudeMessages(messages)
	sys := system
	softLimit := resolveClaudeTokenSoftLimit()
	hardLimit := resolveClaudeTokenHardLimit(softLimit)
	if len(msgs) == 0 {
		return msgs, sys
	}
	estimated := estimateClaudePromptTokens(msgs, sys)
	if estimated <= softLimit {
		return msgs, sys
	}

	// Stage 1: light trim, keep a generous recent history.
	msgs = trimClaudeMessagesTail(msgs, 24)
	estimated = estimateClaudePromptTokens(msgs, sys)
	if estimated <= hardLimit {
		return msgs, sys
	}

	// Stage 2: tighter history window.
	msgs = trimClaudeMessagesTail(msgs, 16)
	estimated = estimateClaudePromptTokens(msgs, sys)
	if estimated <= hardLimit {
		return msgs, sys
	}

	// Stage 3: shrink oversized system payload.
	sys = compressClaudeSystem(system, 2800)
	estimated = estimateClaudePromptTokens(msgs, sys)
	if estimated <= hardLimit {
		return msgs, sys
	}

	// Stage 4: final non-aggressive trim.
	msgs = trimClaudeMessagesTail(msgs, 12)
	sys = compressClaudeSystem(sys, 1800)
	return msgs, sys
}

func resolveClaudeTokenSoftLimit() int {
	raw := strings.TrimSpace(os.Getenv("CODEXSESS_CLAUDE_TOKEN_SOFT_LIMIT"))
	if raw == "" {
		return claudeTokenSoftLimitDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 4000 {
		return claudeTokenSoftLimitDefault
	}
	return n
}

func resolveClaudeTokenHardLimit(softLimit int) int {
	raw := strings.TrimSpace(os.Getenv("CODEXSESS_CLAUDE_TOKEN_HARD_LIMIT"))
	if raw == "" {
		if softLimit+4000 > claudeTokenHardLimitDefault {
			return softLimit + 4000
		}
		return claudeTokenHardLimitDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < softLimit+2000 {
		return maxInt(softLimit+2000, claudeTokenHardLimitDefault)
	}
	return n
}

func estimateClaudePromptTokens(messages []ClaudeMessage, system json.RawMessage) int {
	text := promptFromClaudeMessagesWithSystemAndTools(messages, system, nil, nil)
	if strings.TrimSpace(text) == "" {
		return 0
	}
	chars := len([]rune(text))
	return (chars + 3) / 4
}

func trimClaudeMessagesTail(messages []ClaudeMessage, keep int) []ClaudeMessage {
	if keep <= 0 || len(messages) <= keep {
		return cloneClaudeMessages(messages)
	}
	start := len(messages) - keep
	out := make([]ClaudeMessage, 0, keep)
	out = append(out, messages[start:]...)
	return out
}

func cloneClaudeMessages(messages []ClaudeMessage) []ClaudeMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]ClaudeMessage, 0, len(messages))
	out = append(out, messages...)
	return out
}

func compressClaudeSystem(system json.RawMessage, maxChars int) json.RawMessage {
	text := strings.TrimSpace(extractClaudeSystemText(system))
	if text == "" || maxChars <= 0 {
		return system
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return system
	}
	truncated := strings.TrimSpace(string(runes[:maxChars]))
	if truncated == "" {
		return system
	}
	b, err := json.Marshal(truncated + "\n...[system context truncated]")
	if err != nil {
		return system
	}
	return json.RawMessage(b)
}

func deriveClaudeSessionKey(req ClaudeMessagesRequest, r *http.Request) string {
	if metadataSession := extractSessionIDFromMetadata(req.Metadata); metadataSession != "" {
		return metadataSession
	}
	if fromHeader := strings.TrimSpace(r.Header.Get("x-claude-session-id")); fromHeader != "" {
		return fromHeader
	}
	ua := strings.TrimSpace(r.UserAgent())
	addr := strings.TrimSpace(r.RemoteAddr)
	if ua == "" && addr == "" {
		return "unknown"
	}
	return ua + "|" + addr
}

func extractSessionIDFromMetadata(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return ""
	}
	return extractSessionIDFromMetadataAny(data, 0)
}

func extractSessionIDFromMetadataAny(data any, depth int) string {
	if depth > 3 || data == nil {
		return ""
	}
	switch v := data.(type) {
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return ""
		}
		var nested any
		if err := json.Unmarshal([]byte(text), &nested); err != nil {
			return text
		}
		return extractSessionIDFromMetadataAny(nested, depth+1)
	case map[string]any:
		for _, key := range []string{"session_id", "sessionId"} {
			if id := strings.TrimSpace(coerceAnyText(v[key])); id != "" && id != "null" && id != "{}" {
				return id
			}
		}
		for _, key := range []string{"user_id", "userId"} {
			if id := extractSessionIDFromMetadataAny(v[key], depth+1); id != "" {
				return id
			}
		}
		for _, key := range []string{"metadata", "meta"} {
			if id := extractSessionIDFromMetadataAny(v[key], depth+1); id != "" {
				return id
			}
		}
		return ""
	default:
		return ""
	}
}

func (s *Server) rememberInvalidToolPattern(sessionKey string, name string, missing []string) {
	session := strings.TrimSpace(sessionKey)
	tool := strings.ToLower(strings.TrimSpace(name))
	if session == "" || tool == "" {
		return
	}
	now := time.Now()
	signature := invalidToolPatternSignature(tool, missing)
	s.invalidToolCacheMu.Lock()
	defer s.invalidToolCacheMu.Unlock()
	if s.invalidToolCache == nil {
		s.invalidToolCache = make(map[string]map[string]time.Time)
	}
	s.pruneInvalidToolCacheLocked(now)
	entry := s.invalidToolCache[session]
	if entry == nil {
		entry = make(map[string]time.Time)
		s.invalidToolCache[session] = entry
	}
	exp := now.Add(invalidToolCacheTTL)
	entry[signature] = exp
	entry[invalidToolPatternSignature(tool, nil)] = exp
}

func (s *Server) hasInvalidToolPattern(sessionKey string, name string, missing []string) bool {
	session := strings.TrimSpace(sessionKey)
	tool := strings.ToLower(strings.TrimSpace(name))
	if session == "" || tool == "" {
		return false
	}
	now := time.Now()
	signature := invalidToolPatternSignature(tool, missing)
	anySignature := invalidToolPatternSignature(tool, nil)
	s.invalidToolCacheMu.Lock()
	defer s.invalidToolCacheMu.Unlock()
	s.pruneInvalidToolCacheLocked(now)
	entry := s.invalidToolCache[session]
	if entry == nil {
		return false
	}
	if exp, ok := entry[signature]; ok && exp.After(now) {
		return true
	}
	if exp, ok := entry[anySignature]; ok && exp.After(now) {
		return true
	}
	return false
}

func invalidToolPatternSignature(tool string, missing []string) string {
	if len(missing) == 0 {
		return tool + "|any"
	}
	clean := make([]string, 0, len(missing))
	for _, item := range missing {
		field := strings.ToLower(strings.TrimSpace(item))
		if field != "" {
			clean = append(clean, field)
		}
	}
	if len(clean) == 0 {
		return tool + "|any"
	}
	sort.Strings(clean)
	return tool + "|" + strings.Join(clean, ",")
}

func (s *Server) pruneInvalidToolCacheLocked(now time.Time) {
	if s.invalidToolCache == nil {
		return
	}
	for session, entries := range s.invalidToolCache {
		for sig, exp := range entries {
			if !exp.After(now) {
				delete(entries, sig)
			}
		}
		if len(entries) == 0 {
			delete(s.invalidToolCache, session)
		}
	}
}

func extractClaudeSystemText(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	textParts, _, _ := extractClaudeMessageParts(raw)
	return strings.TrimSpace(strings.Join(textParts, "\n"))
}

func extractClaudeMessageParts(raw json.RawMessage) ([]string, []string, []string) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		text := strings.TrimSpace(asString)
		if text == "" {
			return nil, nil, nil
		}
		return []string{text}, nil, nil
	}

	var asItems []map[string]any
	if err := json.Unmarshal(raw, &asItems); err != nil {
		return nil, nil, nil
	}
	textParts := make([]string, 0, len(asItems))
	toolCalls := make([]string, 0, 2)
	toolResults := make([]string, 0, 2)
	for _, it := range asItems {
		typ := strings.ToLower(strings.TrimSpace(coerceAnyText(it["type"])))
		switch typ {
		case "", "text":
			if text := strings.TrimSpace(coerceAnyText(it["text"])); text != "" {
				textParts = append(textParts, text)
			}
		case "tool_use":
			name := strings.TrimSpace(coerceAnyText(it["name"]))
			if name == "" {
				continue
			}
			args := coerceAnyJSON(it["input"])
			toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", name, args))
		case "tool_result":
			toolUseID := strings.TrimSpace(coerceAnyText(it["tool_use_id"]))
			if toolUseID == "" {
				toolUseID = "unknown"
			}
			result := extractClaudeToolResultValue(it["content"])
			toolResults = append(toolResults, fmt.Sprintf("tool(%s): %s", toolUseID, result))
		}
	}
	return textParts, toolCalls, toolResults
}

func extractClaudeToolResultValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "{}"
	case string:
		trimmed := strings.TrimSpace(x)
		if trimmed == "" {
			return "{}"
		}
		return trimmed
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			t := strings.ToLower(strings.TrimSpace(coerceAnyText(obj["type"])))
			if t != "" && t != "text" {
				continue
			}
			text := strings.TrimSpace(coerceAnyText(obj["text"]))
			if text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
		b, _ := json.Marshal(x)
		return strings.TrimSpace(string(b))
	default:
		b, _ := json.Marshal(x)
		raw := strings.TrimSpace(string(b))
		if raw == "" {
			return "{}"
		}
		return raw
	}
}
