package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/store"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

type structuredOutputSpec struct {
	Name   string
	Schema json.RawMessage
	Strict bool
}

func normalizeResponseFormat(format *ResponseFormat) (*structuredOutputSpec, error) {
	if format == nil {
		return nil, nil
	}
	typ := strings.TrimSpace(strings.ToLower(format.Type))
	if typ == "" {
		return nil, fmt.Errorf("response_format.type is required")
	}
	if typ != "json_schema" {
		return nil, fmt.Errorf("unsupported response_format.type: %s", typ)
	}
	name := strings.TrimSpace(format.Name)
	schema := bytes.TrimSpace(format.Schema)
	strictPtr := format.Strict
	if format.JSONSchema != nil {
		if name == "" {
			name = strings.TrimSpace(format.JSONSchema.Name)
		}
		if len(schema) == 0 {
			schema = bytes.TrimSpace(format.JSONSchema.Schema)
		}
		if strictPtr == nil {
			strictPtr = format.JSONSchema.Strict
		}
	}
	if len(schema) == 0 {
		return nil, fmt.Errorf("json_schema.schema is required")
	}
	strict := true
	if strictPtr != nil {
		strict = *strictPtr
	}
	return &structuredOutputSpec{
		Name:   name,
		Schema: schema,
		Strict: strict,
	}, nil
}

func responseFormatPayload(format *ResponseFormat) (map[string]any, error) {
	spec, err := normalizeResponseFormat(format)
	if err != nil || spec == nil {
		return nil, err
	}
	payload := map[string]any{
		"type":   "json_schema",
		"schema": json.RawMessage(spec.Schema),
		"strict": spec.Strict,
	}
	if spec.Name != "" {
		payload["name"] = spec.Name
	}
	return payload, nil
}

func validateStructuredOutput(spec *structuredOutputSpec, output string) error {
	if spec == nil {
		return nil
	}
	raw := strings.TrimSpace(output)
	if raw == "" {
		return fmt.Errorf("output is empty")
	}
	var data any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return fmt.Errorf("output is not valid JSON: %w", err)
	}
	if !spec.Strict {
		return nil
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", bytes.NewReader(spec.Schema)); err != nil {
		return fmt.Errorf("invalid json_schema: %w", err)
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("invalid json_schema: %w", err)
	}
	if err := schema.Validate(data); err != nil {
		return fmt.Errorf("output does not match json_schema: %w", err)
	}
	return nil
}

func (s *Server) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		path := strings.TrimSpace(r.URL.Path)
		if path == "" {
			path = "/"
		}

		// WebSocket upgrade paths should bypass response writer wrapping to avoid
		// hijack/upgrade incompatibilities in middleware wrappers.
		if path == "/api/coding/ws" {
			next.ServeHTTP(w, r)
			remote := strings.TrimSpace(r.RemoteAddr)
			if host, _, err := net.SplitHostPort(remote); err == nil && host != "" {
				remote = host
			}
			accountHint := firstNonEmpty(strings.TrimSpace(r.Header.Get("X-Codex-Account")), "-")
			apiAuth := classifyAuthSource(r)
			ua := firstNonEmpty(truncateForLog(strings.TrimSpace(r.UserAgent()), 72), "-")
			log.Printf(
				"[ACCESS] %-7s %-38s status=%3s latency=%4dms from=%s kind=%s auth=%s account=%s ua=%s",
				strings.ToUpper(strings.TrimSpace(r.Method)),
				path,
				"WS",
				time.Since(start).Milliseconds(),
				firstNonEmpty(remote, "-"),
				requestKind(path),
				apiAuth,
				accountHint,
				ua,
			)
			return
		}

		rec := &accessLogRecorder{
			ResponseWriter: w,
			status:         http.StatusOK,
		}
		next.ServeHTTP(rec, r)

		remote := strings.TrimSpace(r.RemoteAddr)
		if host, _, err := net.SplitHostPort(remote); err == nil && host != "" {
			remote = host
		}
		accountHint := firstNonEmpty(strings.TrimSpace(r.Header.Get("X-Codex-Account")), "-")
		apiAuth := classifyAuthSource(r)
		ua := firstNonEmpty(truncateForLog(strings.TrimSpace(r.UserAgent()), 72), "-")
		log.Printf(
			"[ACCESS] %-7s %-38s status=%3d latency=%4dms from=%s kind=%s auth=%s account=%s ua=%s",
			strings.ToUpper(strings.TrimSpace(r.Method)),
			path,
			rec.status,
			time.Since(start).Milliseconds(),
			firstNonEmpty(remote, "-"),
			requestKind(path),
			apiAuth,
			accountHint,
			ua,
		)
	})
}

func requestKind(path string) string {
	p := strings.TrimSpace(path)
	switch {
	case strings.HasPrefix(p, "/v1"), strings.HasPrefix(p, "/claude/v1"):
		return "proxy-api"
	case strings.HasPrefix(p, "/api/"):
		return "web-api"
	case strings.HasPrefix(p, "/auth/"):
		return "auth"
	case strings.HasPrefix(p, "/assets/"), strings.HasPrefix(p, "/sounds/"), p == "/favicon.png":
		return "asset"
	default:
		return "web-ui"
	}
}

func classifyAuthSource(r *http.Request) string {
	bearer := strings.TrimSpace(BearerToken(r.Header.Get("Authorization")))
	xAPIKey := strings.TrimSpace(r.Header.Get("x-api-key"))
	switch {
	case bearer != "":
		return "bearer:" + maskSecret(bearer)
	case xAPIKey != "":
		return "x-api-key:" + maskSecret(xAPIKey)
	default:
		return "none"
	}
}

func maskSecret(v string) string {
	s := strings.TrimSpace(v)
	if s == "" {
		return "-"
	}
	if len(s) <= 6 {
		return s[:1] + "***"
	}
	return s[:3] + "..." + s[len(s)-2:]
}

func ptrString(v string) *string {
	return &v
}

type accessLogRecorder struct {
	http.ResponseWriter
	status int
}

func (r *accessLogRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *accessLogRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(p)
}

func (r *accessLogRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *accessLogRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijacker unsupported")
	}
	return hj.Hijack()
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if !s.isValidAPIKey(r) {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return
	}
	now := time.Now().Unix()
	available := codexAvailableModels()
	data := make([]ModelInfo, 0, len(available))
	for _, id := range available {
		data = append(data, ModelInfo{ID: id, Object: "model", Created: now, OwnedBy: "codexsess"})
	}
	resp := ModelsResponse{
		Object: "list",
		Data:   data,
	}
	respondJSON(w, 200, resp)
}

func respondErr(w http.ResponseWriter, code int, errType, msg string) {
	normalizedType := strings.TrimSpace(errType)
	if normalizedType == "" {
		normalizedType = "error"
	}
	respondJSON(w, code, map[string]any{
		"error": map[string]any{
			"type":    normalizedType,
			"message": msg,
			"code":    normalizedType,
			"param":   nil,
		},
	})
}

func respondJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		x := strings.TrimSpace(v)
		if x != "" {
			return x
		}
	}
	return ""
}

func promptFromResponsesInput(raw json.RawMessage, tools []ChatToolDef, toolChoice json.RawMessage) string {
	base := extractResponsesInput(raw)
	return promptFromTextWithTools(base, tools, toolChoice)
}

func extractResponsesInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var asItems []map[string]any
	if err := json.Unmarshal(raw, &asItems); err == nil {
		var parts []string
		for _, it := range asItems {
			typ, _ := it["type"].(string)
			switch strings.TrimSpace(strings.ToLower(typ)) {
			case "function_call":
				callID, _ := it["call_id"].(string)
				name, _ := it["name"].(string)
				args := coerceAnyJSON(it["arguments"])
				line := "assistant_tool_calls: " + strings.TrimSpace(name) + "(" + args + ")"
				if strings.TrimSpace(callID) != "" {
					line += " [id=" + strings.TrimSpace(callID) + "]"
				}
				parts = append(parts, strings.TrimSpace(line))
				continue
			case "function_call_output":
				callID, _ := it["call_id"].(string)
				output := strings.TrimSpace(coerceAnyText(it["output"]))
				if output == "" {
					output = "{}"
				}
				label := "tool"
				if strings.TrimSpace(callID) != "" {
					label += "(" + strings.TrimSpace(callID) + ")"
				}
				parts = append(parts, label+": "+output)
				continue
			}
			if role, _ := it["role"].(string); role != "" {
				if content, ok := it["content"].(string); ok {
					parts = append(parts, role+": "+content)
					continue
				}
				if arr, ok := it["content"].([]any); ok {
					for _, c := range arr {
						obj, _ := c.(map[string]any)
						if text, _ := obj["text"].(string); text != "" {
							parts = append(parts, role+": "+text)
						}
					}
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

func coerceAnyText(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(x)
		return strings.TrimSpace(string(b))
	}
}

func coerceAnyJSON(v any) string {
	switch x := v.(type) {
	case nil:
		return "{}"
	case string:
		t := strings.TrimSpace(x)
		if t == "" {
			return "{}"
		}
		if json.Valid([]byte(t)) {
			return t
		}
		b, _ := json.Marshal(t)
		return string(b)
	default:
		b, _ := json.Marshal(x)
		if len(bytes.TrimSpace(b)) == 0 {
			return "{}"
		}
		return string(b)
	}
}

func streamChatCompletionText(w http.ResponseWriter, flusher http.Flusher, chunkID string, model string, text string, usage Usage, includeUsageChunk bool) {
	text = strings.TrimSpace(text)
	if text != "" {
		writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
			ID:      chunkID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{Role: "assistant", Content: text}}},
		})
	}
	streamChatCompletionFinalStop(w, flusher, chunkID, model, usage, includeUsageChunk)
}

func streamChatCompletionFinalStop(w http.ResponseWriter, flusher http.Flusher, chunkID string, model string, usage Usage, includeUsageChunk bool) {
	finalUsage := &usage
	if includeUsageChunk {
		finalUsage = nil
	}
	writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
		ID:      chunkID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{Role: "assistant"}, FinishReason: ptrString("stop")}},
		Usage:   finalUsage,
	})
	if includeUsageChunk {
		writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
			ID:      chunkID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []ChatChunkChoice{},
			Usage:   &usage,
		})
	}
	writeChatCompletionsDone(w, flusher)
}

func streamChatCompletionToolCalls(w http.ResponseWriter, flusher http.Flusher, chunkID string, model string, calls []ChatToolCall, usage Usage, includeUsageChunk bool) {
	writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
		ID:      chunkID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{Role: "assistant"}}},
	})
	for i, call := range calls {
		idx := i
		writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
			ID:      chunkID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []ChatChunkChoice{{
				Index: 0,
				Delta: ChatMessage{
					ToolCalls: []ChatToolCall{{
						Index: &idx,
						ID:    call.ID,
						Type:  "function",
						Function: ChatToolFunctionCall{
							Name: call.Function.Name,
						},
					}},
				},
			}},
		})
		if strings.TrimSpace(call.Function.Arguments) != "" {
			writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
				ID:      chunkID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   model,
				Choices: []ChatChunkChoice{{
					Index: 0,
					Delta: ChatMessage{
						ToolCalls: []ChatToolCall{{
							Index: &idx,
							Function: ChatToolFunctionCall{
								Arguments: call.Function.Arguments,
							},
						}},
					},
				}},
			})
		}
	}
	finalUsage := &usage
	if includeUsageChunk {
		finalUsage = nil
	}
	writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
		ID:      chunkID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{}, FinishReason: ptrString("tool_calls")}},
		Usage:   finalUsage,
	})
	if includeUsageChunk {
		writeChatCompletionsChunk(w, flusher, ChatCompletionsChunk{
			ID:      chunkID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []ChatChunkChoice{},
			Usage:   &usage,
		})
	}
	writeChatCompletionsDone(w, flusher)
}

func responsesMessageOutputItems(text string) []ResponsesItem {
	return []ResponsesItem{
		{
			Type:   "message",
			ID:     "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			Status: "completed",
			Role:   "assistant",
			Content: []ResponsesText{
				{Type: "output_text", Text: text, Annotations: []ResponsesRef{}},
			},
		},
	}
}

func responsesFunctionCallOutputItems(calls []ChatToolCall) []ResponsesItem {
	items := make([]ResponsesItem, 0, len(calls))
	for _, call := range calls {
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			callID = "call_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		items = append(items, ResponsesItem{
			Type:      "function_call",
			ID:        "fc_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			Status:    "completed",
			CallID:    callID,
			Name:      strings.TrimSpace(call.Function.Name),
			Arguments: strings.TrimSpace(call.Function.Arguments),
		})
	}
	return items
}

func streamResponsesText(
	emit func(string, map[string]any),
	reqID string,
	model string,
	text string,
	usage ResponsesUsage,
	createdAt int64,
) {
	text = strings.TrimSpace(text)
	textItemID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	if text != "" {
		emit("response.output_text.delta", map[string]any{
			"type":          "response.output_text.delta",
			"item_id":       textItemID,
			"output_index":  0,
			"content_index": 0,
			"delta":         text,
			"logprobs":      []any{},
		})
		emit("response.output_text.done", map[string]any{
			"type":          "response.output_text.done",
			"item_id":       textItemID,
			"output_index":  0,
			"content_index": 0,
			"text":          text,
			"logprobs":      []any{},
		})
	}
	outputItem := map[string]any{
		"type":   "message",
		"id":     textItemID,
		"status": "completed",
		"role":   "assistant",
		"content": []map[string]any{
			{"type": "output_text", "text": text, "annotations": []any{}},
		},
	}
	emit("response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": 0,
		"item":         outputItem,
	})
	completedEvent := map[string]any{
		"type": "response.completed",
		"response": buildResponseObject(reqID, model, "completed", []any{outputItem}, map[string]any{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
			"total_tokens":  usage.TotalTokens,
		}, createdAt),
	}
	emit("response.completed", completedEvent)
}

func streamResponsesFunctionCalls(
	emit func(string, map[string]any),
	reqID string,
	model string,
	calls []ChatToolCall,
	usage ResponsesUsage,
	createdAt int64,
) {
	output := responsesFunctionCallOutputItems(calls)
	for i, item := range output {
		emit("response.output_item.added", map[string]any{
			"type":         "response.output_item.added",
			"output_index": i,
			"item":         item,
		})
		if strings.TrimSpace(item.Arguments) != "" {
			emit("response.function_call_arguments.delta", map[string]any{
				"type":         "response.function_call_arguments.delta",
				"item_id":      item.ID,
				"output_index": i,
				"delta":        item.Arguments,
			})
			emit("response.function_call_arguments.done", map[string]any{
				"type":         "response.function_call_arguments.done",
				"item_id":      item.ID,
				"output_index": i,
				"arguments":    item.Arguments,
				"name":         item.Name,
			})
		}
		emit("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": i,
			"item":         item,
		})
	}
	completedEvent := map[string]any{
		"type": "response.completed",
		"response": buildResponseObject(reqID, model, "completed", anySlice(output), map[string]any{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
			"total_tokens":  usage.TotalTokens,
		}, createdAt),
	}
	emit("response.completed", completedEvent)
}

func writeChatCompletionsChunk(w http.ResponseWriter, flusher http.Flusher, chunk ChatCompletionsChunk) {
	b, _ := json.Marshal(chunk)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

func writeChatCompletionsDone(w http.ResponseWriter, flusher http.Flusher) {
	_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func writeSSE(w http.ResponseWriter, event string, payload any) {
	b, _ := json.Marshal(payload)
	if strings.TrimSpace(event) != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", event)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

func writeOpenAISSE(w http.ResponseWriter, payload any) {
	b, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

func startSSEKeepAlive(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, interval time.Duration) func() {
	if interval <= 0 {
		interval = 8 * time.Second
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				_, _ = fmt.Fprint(w, ": keep-alive\n\n")
				flusher.Flush()
			}
		}
	}()
	return func() {
		select {
		case <-stopCh:
		default:
			close(stopCh)
		}
		<-doneCh
	}
}

func resolveSSEKeepAliveInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("CODEXSESS_SSE_KEEPALIVE_SECONDS"))
	if raw == "" {
		return 8 * time.Second
	}
	sec, err := strconv.Atoi(raw)
	if err != nil {
		return 8 * time.Second
	}
	if sec < 2 {
		sec = 2
	}
	if sec > 30 {
		sec = 30
	}
	return time.Duration(sec) * time.Second
}

func anySlice[T any](items []T) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func buildResponseObject(reqID, model, status string, output []any, usage any, createdAt int64) map[string]any {
	now := createdAt
	if now <= 0 {
		now = time.Now().Unix()
	}
	completedAt := any(nil)
	if strings.TrimSpace(status) == "completed" {
		completedAt = now
	}
	return map[string]any{
		"id":                   reqID,
		"object":               "response",
		"created_at":           now,
		"output_text":          responseOutputText(output),
		"status":               firstNonEmpty(strings.TrimSpace(status), "completed"),
		"completed_at":         completedAt,
		"error":                nil,
		"incomplete_details":   nil,
		"instructions":         nil,
		"max_output_tokens":    nil,
		"model":                model,
		"output":               output,
		"parallel_tool_calls":  true,
		"previous_response_id": nil,
		"reasoning": map[string]any{
			"effort":  nil,
			"summary": nil,
		},
		"store":       true,
		"temperature": 1.0,
		"text": map[string]any{
			"format": map[string]any{"type": "text"},
		},
		"tool_choice": "auto",
		"tools":       []any{},
		"top_p":       1.0,
		"truncation":  "disabled",
		"usage":       usage,
		"user":        nil,
		"metadata":    map[string]any{},
	}
}

func responseOutputText(output []any) string {
	if len(output) == 0 {
		return ""
	}
	parts := make([]string, 0, len(output))
	for _, raw := range output {
		switch item := raw.(type) {
		case ResponsesItem:
			if strings.TrimSpace(strings.ToLower(item.Type)) != "message" {
				continue
			}
			for _, c := range item.Content {
				if strings.TrimSpace(strings.ToLower(c.Type)) != "output_text" {
					continue
				}
				if text := strings.TrimSpace(c.Text); text != "" {
					parts = append(parts, text)
				}
			}
		case map[string]any:
			if strings.TrimSpace(strings.ToLower(asString(item["type"]))) != "message" {
				continue
			}
			switch content := item["content"].(type) {
			case []any:
				for _, cRaw := range content {
					c, _ := cRaw.(map[string]any)
					if c == nil {
						continue
					}
					if strings.TrimSpace(strings.ToLower(asString(c["type"]))) != "output_text" {
						continue
					}
					if text := strings.TrimSpace(asString(c["text"])); text != "" {
						parts = append(parts, text)
					}
				}
			case []map[string]any:
				for _, c := range content {
					if strings.TrimSpace(strings.ToLower(asString(c["type"]))) != "output_text" {
						continue
					}
					if text := strings.TrimSpace(asString(c["text"])); text != "" {
						parts = append(parts, text)
					}
				}
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func setResolvedAccountHeaders(w http.ResponseWriter, account store.Account) {
	if w == nil {
		return
	}
	if rec, ok := w.(*trafficRecorder); ok {
		rec.accountID = strings.TrimSpace(account.ID)
		rec.accountEmail = strings.TrimSpace(account.Email)
		return
	}
}
