package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ricki/codexsess/internal/config"
	"github.com/ricki/codexsess/internal/provider"
	"github.com/ricki/codexsess/internal/service"
	"github.com/ricki/codexsess/internal/store"
	"github.com/ricki/codexsess/internal/trafficlog"
	"github.com/ricki/codexsess/internal/webui"
)

type Server struct {
	svc      *service.Service
	apiKey   string
	bindAddr string
	traffic  *trafficlog.Logger
	mu       sync.RWMutex
}

func New(svc *service.Service, bindAddr string, apiKey string, traffic *trafficlog.Logger) *Server {
	return &Server{svc: svc, bindAddr: bindAddr, apiKey: apiKey, traffic: traffic}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/accounts", s.handleWebAccounts)
	mux.HandleFunc("/api/account/use", s.handleWebUseAccount)
	mux.HandleFunc("/api/account/remove", s.handleWebRemoveAccount)
	mux.HandleFunc("/api/account/import", s.handleWebImportAccount)
	mux.HandleFunc("/api/usage/refresh", s.handleWebRefreshUsage)
	mux.HandleFunc("/api/settings", s.handleWebSettings)
	mux.HandleFunc("/api/settings/api-key", s.handleWebUpdateAPIKey)
	mux.HandleFunc("/api/model-mappings", s.handleWebModelMappings)
	mux.HandleFunc("/api/logs", s.handleWebLogs)
	mux.HandleFunc("/api/auth/browser/start", s.handleWebBrowserStart)
	mux.HandleFunc("/api/auth/browser/cancel", s.handleWebBrowserCancel)
	mux.HandleFunc("/api/auth/browser/callback", s.handleWebBrowserCallback)
	mux.HandleFunc("/auth/callback", s.handleWebBrowserCallback)
	mux.HandleFunc("/api/auth/device/start", s.handleWebDeviceStart)
	mux.HandleFunc("/api/auth/device/poll", s.handleWebDevicePoll)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		respondJSON(w, 200, map[string]any{"ok": true})
	})
	mux.HandleFunc("/v1/models", s.withTrafficLog("openai", s.handleModels))
	mux.HandleFunc("/v1", s.withTrafficLog("openai", s.handleOpenAIV1Root))
	mux.HandleFunc("/v1/chat/completions", s.withTrafficLog("openai", s.handleChatCompletions))
	mux.HandleFunc("/v1/responses", s.withTrafficLog("openai", s.handleResponses))
	mux.HandleFunc("/v1/messages", s.withTrafficLog("claude", s.handleClaudeMessages))
	mux.HandleFunc("/claude/v1/messages", s.withTrafficLog("claude", s.handleClaudeMessages))
	mux.Handle("/", webui.Handler())
	srv := &http.Server{Addr: s.bindAddr, Handler: mux}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	return srv.ListenAndServe()
}

func (s *Server) handleWebAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	accounts, err := s.svc.ListAccounts(r.Context())
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	type webAccount struct {
		ID       string               `json:"id"`
		Email    string               `json:"email"`
		Alias    string               `json:"alias"`
		PlanType string               `json:"plan_type"`
		Active   bool                 `json:"active"`
		Usage    *store.UsageSnapshot `json:"usage,omitempty"`
	}
	resp := struct {
		Accounts []webAccount `json:"accounts"`
	}{}
	for _, a := range accounts {
		item := webAccount{
			ID:       a.ID,
			Email:    a.Email,
			Alias:    a.Alias,
			PlanType: a.PlanType,
			Active:   a.Active,
		}
		if u, err := s.svc.Store.GetUsage(r.Context(), a.ID); err == nil {
			ux := u
			item.Usage = &ux
		}
		resp.Accounts = append(resp.Accounts, item)
	}
	respondJSON(w, 200, resp)
}

func (s *Server) handleWebUseAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Selector string `json:"selector"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	acc, err := s.svc.UseAccount(r.Context(), req.Selector)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "account": map[string]any{"id": acc.ID, "email": acc.Email}})
}

func (s *Server) handleWebRemoveAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Selector string `json:"selector"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	if err := s.svc.RemoveAccount(r.Context(), req.Selector); err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleWebImportAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Path  string `json:"path"`
		Alias string `json:"alias"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	acc, err := s.svc.ImportTokenJSON(r.Context(), req.Path, req.Alias)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "account": map[string]any{"id": acc.ID, "email": acc.Email}})
}

func (s *Server) handleWebRefreshUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		Selector string `json:"selector"`
		All      bool   `json:"all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	if req.All {
		accounts, err := s.svc.ListAccounts(r.Context())
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		ok := 0
		for _, a := range accounts {
			if _, err := s.svc.RefreshUsage(r.Context(), a.ID); err == nil {
				ok++
			}
		}
		respondJSON(w, 200, map[string]any{"ok": true, "refreshed": ok, "total": len(accounts)})
		return
	}
	if strings.TrimSpace(req.Selector) == "" {
		respondErr(w, 400, "bad_request", "selector required")
		return
	}
	u, err := s.svc.RefreshUsage(r.Context(), req.Selector)
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "usage": u})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if BearerToken(r.Header.Get("Authorization")) != s.currentAPIKey() {
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

func (s *Server) handleOpenAIV1Root(w http.ResponseWriter, r *http.Request) {
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
	if BearerToken(r.Header.Get("Authorization")) != s.currentAPIKey() {
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
	prompt := promptFromMessages(req.Messages)
	account, _, err := s.svc.ResolveForRequest(r.Context(), selector)
	if err != nil {
		respondErr(w, 404, "account_not_found", err.Error())
		return
	}
	usage, usageErr := s.svc.Store.GetUsage(r.Context(), account.ID)
	if usageErr != nil {
		if snap, err := s.svc.RefreshUsage(r.Context(), account.ID); err == nil {
			usage = snap
			usageErr = nil
		}
	}
	if usageErr == nil {
		if usage.HourlyPct <= 0 || usage.WeeklyPct <= 0 {
			respondErr(w, 429, "quota_exhausted", "target account quota exhausted")
			return
		}
	}
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
			respondErr(w, 500, "internal_error", "streaming not supported")
			return
		}
		res, err := s.svc.Codex.StreamChat(r.Context(), account.CodexHome, req.Model, prompt, func(evt provider.ChatEvent) error {
			chunk := ChatCompletionsChunk{
				ID:      "chatcmpl-" + reqID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   req.Model,
				Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{Role: "assistant", Content: evt.Text}, FinishReason: ""}},
			}
			b, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
			return nil
		})
		if err != nil {
			status = 500
			_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"error":{"message":"`+escape(err.Error())+`"}}`)
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		final := ChatCompletionsChunk{
			ID:      "chatcmpl-" + reqID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []ChatChunkChoice{{Index: 0, Delta: ChatMessage{}, FinishReason: "stop"}},
			Usage:   &Usage{PromptTokens: res.InputTokens, CompletionTokens: res.OutputTokens, TotalTokens: res.InputTokens + res.OutputTokens},
		}
		b, _ := json.Marshal(final)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	res, err := s.svc.Codex.Chat(r.Context(), account.CodexHome, req.Model, prompt)
	if err != nil {
		status = 500
		respondErr(w, 500, "upstream_error", err.Error())
		return
	}
	resp := ChatCompletionsResponse{
		ID:      "chatcmpl-" + reqID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []ChatChoice{{Index: 0, Message: ChatMessage{Role: "assistant", Content: res.Text}, FinishReason: "stop"}},
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
	if BearerToken(r.Header.Get("Authorization")) != s.currentAPIKey() {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return
	}
	selector := ""
	var req ResponsesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-5.2-codex"
	}
	req.Model = s.resolveMappedModel(req.Model)
	inputText := extractResponsesInput(req.Input)
	if strings.TrimSpace(inputText) == "" {
		respondErr(w, 400, "bad_request", "input is required")
		return
	}
	account, _, err := s.svc.ResolveForRequest(r.Context(), selector)
	if err != nil {
		respondErr(w, 404, "account_not_found", err.Error())
		return
	}
	usage, usageErr := s.svc.Store.GetUsage(r.Context(), account.ID)
	if usageErr != nil {
		if snap, err := s.svc.RefreshUsage(r.Context(), account.ID); err == nil {
			usage = snap
			usageErr = nil
		}
	}
	if usageErr == nil {
		if usage.HourlyPct <= 0 || usage.WeeklyPct <= 0 {
			respondErr(w, 429, "quota_exhausted", "target account quota exhausted")
			return
		}
	}
	status := 200
	defer func() {
		_ = s.svc.Store.InsertAudit(r.Context(), store.AuditRecord{
			RequestID: reqID,
			AccountID: account.ID,
			Model:     req.Model,
			Stream:    false,
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
			respondErr(w, 500, "internal_error", "streaming not supported")
			return
		}
		createdEvent := map[string]any{
			"type": "response.created",
			"response": map[string]any{
				"id":     reqID,
				"object": "response",
				"model":  req.Model,
				"status": "in_progress",
			},
		}
		writeSSE(w, "response.created", createdEvent)
		flusher.Flush()

		result, err := s.svc.Codex.StreamChat(r.Context(), account.CodexHome, req.Model, inputText, func(evt provider.ChatEvent) error {
			deltaEvent := map[string]any{
				"type":        "response.output_text.delta",
				"response_id": reqID,
				"delta":       evt.Text,
			}
			writeSSE(w, "response.output_text.delta", deltaEvent)
			flusher.Flush()
			return nil
		})
		if err != nil {
			status = 500
			writeSSE(w, "error", map[string]any{
				"type": "error",
				"error": map[string]any{
					"type":    "upstream_error",
					"message": err.Error(),
				},
			})
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		completedEvent := map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id":     reqID,
				"object": "response",
				"model":  req.Model,
				"status": "completed",
				"output": []map[string]any{
					{
						"type":   "message",
						"id":     "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
						"status": "completed",
						"role":   "assistant",
						"content": []map[string]any{
							{"type": "output_text", "text": result.Text},
						},
					},
				},
				"usage": map[string]any{
					"input_tokens":  result.InputTokens,
					"output_tokens": result.OutputTokens,
					"total_tokens":  result.InputTokens + result.OutputTokens,
				},
			},
		}
		writeSSE(w, "response.completed", completedEvent)
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	result, err := s.svc.Codex.Chat(r.Context(), account.CodexHome, req.Model, inputText)
	if err != nil {
		status = 500
		respondErr(w, 500, "upstream_error", err.Error())
		return
	}
	resp := ResponsesResponse{
		ID:     reqID,
		Object: "response",
		Model:  req.Model,
		Output: []ResponsesItem{
			{
				Type:   "message",
				ID:     "msg_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
				Status: "completed",
				Role:   "assistant",
				Content: []ResponsesText{
					{Type: "output_text", Text: result.Text},
				},
			},
		},
		Usage: ResponsesUsage{
			InputTokens:  result.InputTokens,
			OutputTokens: result.OutputTokens,
			TotalTokens:  result.InputTokens + result.OutputTokens,
		},
	}
	respondJSON(w, 200, resp)
}

func (s *Server) handleClaudeMessages(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := "msg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	if !s.isValidAPIKey(r) {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return
	}
	selector := ""
	var req ClaudeMessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-5.2-codex"
	}
	req.Model = s.resolveMappedModel(req.Model)
	prompt := promptFromClaudeMessages(req.Messages)
	if strings.TrimSpace(prompt) == "" {
		respondErr(w, 400, "bad_request", "messages are required")
		return
	}
	account, _, err := s.svc.ResolveForRequest(r.Context(), selector)
	if err != nil {
		respondErr(w, 404, "account_not_found", err.Error())
		return
	}
	usage, usageErr := s.svc.Store.GetUsage(r.Context(), account.ID)
	if usageErr != nil {
		if snap, err := s.svc.RefreshUsage(r.Context(), account.ID); err == nil {
			usage = snap
			usageErr = nil
		}
	}
	if usageErr == nil {
		if usage.HourlyPct <= 0 || usage.WeeklyPct <= 0 {
			respondErr(w, 429, "quota_exhausted", "target account quota exhausted")
			return
		}
	}
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
			respondErr(w, 500, "internal_error", "streaming not supported")
			return
		}
		writeSSE(w, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":    reqID,
				"type":  "message",
				"role":  "assistant",
				"model": req.Model,
			},
		})
		flusher.Flush()
		_, err := s.svc.Codex.StreamChat(r.Context(), account.CodexHome, req.Model, prompt, func(evt provider.ChatEvent) error {
			writeSSE(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{"type": "text_delta", "text": evt.Text},
			})
			flusher.Flush()
			return nil
		})
		if err != nil {
			status = 500
			writeSSE(w, "error", map[string]any{"type": "error", "error": map[string]any{"message": err.Error()}})
			_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		writeSSE(w, "message_stop", map[string]any{"type": "message_stop"})
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}

	res, err := s.svc.Codex.Chat(r.Context(), account.CodexHome, req.Model, prompt)
	if err != nil {
		status = 500
		respondErr(w, 500, "upstream_error", err.Error())
		return
	}
	resp := ClaudeMessagesResponse{
		ID:         reqID,
		Type:       "message",
		Role:       "assistant",
		Model:      req.Model,
		Content:    []ClaudeContentBlock{{Type: "text", Text: res.Text}},
		StopReason: "end_turn",
		Usage: ClaudeMessagesUsage{
			InputTokens:  res.InputTokens,
			OutputTokens: res.OutputTokens,
		},
	}
	respondJSON(w, 200, resp)
}

func promptFromMessages(msgs []ChatMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "user"
		}
		sb.WriteString(role)
		sb.WriteString(": ")
		sb.WriteString(strings.TrimSpace(m.Content))
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func promptFromClaudeMessages(msgs []ClaudeMessage) string {
	var sb strings.Builder
	for _, m := range msgs {
		role := strings.TrimSpace(m.Role)
		if role == "" {
			role = "user"
		}
		sb.WriteString(role)
		sb.WriteString(": ")
		sb.WriteString(extractClaudeContentText(m.Content))
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}

func extractClaudeContentText(raw json.RawMessage) string {
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
			if t, _ := it["type"].(string); t != "" && t != "text" {
				continue
			}
			if text, _ := it["text"].(string); strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	return ""
}

func respondErr(w http.ResponseWriter, code int, errType, msg string) {
	respondJSON(w, code, map[string]any{"error": map[string]any{"type": errType, "message": msg}})
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

func writeSSE(w http.ResponseWriter, event string, payload any) {
	b, _ := json.Marshal(payload)
	if strings.TrimSpace(event) != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", event)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

func (s *Server) currentAPIKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.apiKey
}

func (s *Server) isValidAPIKey(r *http.Request) bool {
	key := s.currentAPIKey()
	if BearerToken(r.Header.Get("Authorization")) == key {
		return true
	}
	return strings.TrimSpace(r.Header.Get("x-api-key")) == key
}

func (s *Server) setAPIKey(v string) {
	s.mu.Lock()
	s.apiKey = v
	s.mu.Unlock()
}

func (s *Server) currentModelMappings() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[string]string{}
	for k, v := range s.svc.Cfg.ModelMappings {
		out[k] = v
	}
	return out
}

func (s *Server) resolveMappedModel(requested string) string {
	model := strings.TrimSpace(requested)
	if model == "" {
		return model
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if target, ok := s.svc.Cfg.ModelMappings[model]; ok && strings.TrimSpace(target) != "" {
		return strings.TrimSpace(target)
	}
	return model
}

func (s *Server) upsertModelMapping(alias, model string) error {
	cfg, err := config.LoadOrInit()
	if err != nil {
		return err
	}
	if cfg.ModelMappings == nil {
		cfg.ModelMappings = map[string]string{}
	}
	cfg.ModelMappings[strings.TrimSpace(alias)] = strings.TrimSpace(model)
	if err := config.Save(cfg); err != nil {
		return err
	}
	s.mu.Lock()
	s.svc.Cfg.ModelMappings = cfg.ModelMappings
	s.mu.Unlock()
	return nil
}

func (s *Server) deleteModelMapping(alias string) error {
	cfg, err := config.LoadOrInit()
	if err != nil {
		return err
	}
	if cfg.ModelMappings == nil {
		cfg.ModelMappings = map[string]string{}
	}
	delete(cfg.ModelMappings, strings.TrimSpace(alias))
	if err := config.Save(cfg); err != nil {
		return err
	}
	s.mu.Lock()
	s.svc.Cfg.ModelMappings = cfg.ModelMappings
	s.mu.Unlock()
	return nil
}

func codexAvailableModels() []string {
	return []string{
		"gpt-5.1-codex-max",
		"gpt-5.2",
		"gpt-5.2-codex",
		"gpt-5.3-codex",
		"gpt-5.4-mini",
		"gpt-5.4",
	}
}

func isValidCodexModel(model string) bool {
	m := strings.TrimSpace(model)
	if m == "" {
		return false
	}
	for _, v := range codexAvailableModels() {
		if v == m {
			return true
		}
	}
	return false
}

func (s *Server) handleWebSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	base := externalBaseURLFromRequest(r, s.bindAddr)
	modelMappings := s.currentModelMappings()
	respondJSON(w, 200, map[string]any{
		"api_key":              s.currentAPIKey(),
		"openai_endpoint":      strings.TrimRight(base, "/") + "/v1/chat/completions",
		"claude_endpoint":      strings.TrimRight(base, "/") + "/v1/messages",
		"openai_models_url":    strings.TrimRight(base, "/") + "/v1/models",
		"openai_chat_url":      strings.TrimRight(base, "/") + "/v1/chat/completions",
		"openai_responses_url": strings.TrimRight(base, "/") + "/v1/responses",
		"available_models":     codexAvailableModels(),
		"model_mappings":       modelMappings,
	})
}

func (s *Server) handleWebLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			if v > 1000 {
				v = 1000
			}
			limit = v
		}
	}
	if s.traffic == nil {
		respondJSON(w, 200, map[string]any{"ok": true, "lines": []string{}})
		return
	}
	lines, err := s.traffic.ReadTail(limit)
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "lines": lines})
}

func (s *Server) handleWebModelMappings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		respondJSON(w, 200, map[string]any{
			"ok":               true,
			"available_models": codexAvailableModels(),
			"mappings":         s.currentModelMappings(),
		})
		return
	case http.MethodPost:
		var req struct {
			Alias string `json:"alias"`
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondErr(w, 400, "bad_request", "invalid JSON")
			return
		}
		alias := strings.TrimSpace(req.Alias)
		model := strings.TrimSpace(req.Model)
		if alias == "" || model == "" {
			respondErr(w, 400, "bad_request", "alias and model are required")
			return
		}
		if !isValidCodexModel(model) {
			respondErr(w, 400, "bad_request", "invalid target model")
			return
		}
		if err := s.upsertModelMapping(alias, model); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true, "mappings": s.currentModelMappings()})
		return
	case http.MethodDelete:
		alias := strings.TrimSpace(r.URL.Query().Get("alias"))
		if alias == "" {
			respondErr(w, 400, "bad_request", "alias is required")
			return
		}
		if err := s.deleteModelMapping(alias); err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		respondJSON(w, 200, map[string]any{"ok": true, "mappings": s.currentModelMappings()})
		return
	default:
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
}

func (s *Server) withTrafficLog(protocol string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.traffic == nil {
			next(w, r)
			return
		}
		var bodyBytes []byte
		if r.Body != nil {
			bodyBytes, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}
		start := time.Now()
		rec := &trafficRecorder{
			ResponseWriter:    w,
			status:            http.StatusOK,
			responseBodyLimit: 4000,
		}
		next(rec, r)

		model, stream := detectTrafficModelAndStream(r.URL.Path, bodyBytes)
		remote := r.RemoteAddr
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			remote = host
		}
		responseBody := strings.TrimSpace(string(rec.responseBody))
		if rec.bodyTruncated {
			responseBody += "...(truncated)"
		}
		_ = s.traffic.Append(trafficlog.Entry{
			Timestamp:    time.Now().UTC(),
			Protocol:     protocol,
			Method:       r.Method,
			Path:         r.URL.Path,
			Status:       rec.status,
			LatencyMS:    time.Since(start).Milliseconds(),
			RemoteAddr:   strings.TrimSpace(remote),
			UserAgent:    strings.TrimSpace(r.UserAgent()),
			AccountHint:  strings.TrimSpace(r.Header.Get("X-Codex-Account")),
			Model:        model,
			Stream:       stream,
			RequestBody:  truncateForLog(string(bodyBytes), 2000),
			ResponseBody: responseBody,
		})
	}
}

type trafficRecorder struct {
	http.ResponseWriter
	status            int
	responseBody      []byte
	responseBodyLimit int
	bodyTruncated     bool
}

func (r *trafficRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *trafficRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.responseBodyLimit > 0 && !r.bodyTruncated {
		remaining := r.responseBodyLimit - len(r.responseBody)
		if remaining > 0 {
			if len(p) <= remaining {
				r.responseBody = append(r.responseBody, p...)
			} else {
				r.responseBody = append(r.responseBody, p[:remaining]...)
				r.bodyTruncated = true
			}
		} else {
			r.bodyTruncated = true
		}
	}
	return r.ResponseWriter.Write(p)
}

func detectTrafficModelAndStream(path string, body []byte) (string, bool) {
	switch strings.TrimSpace(path) {
	case "/v1/chat/completions":
		var req ChatCompletionsRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	case "/v1/responses":
		var req ResponsesRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	case "/v1/messages", "/claude/v1/messages":
		var req ClaudeMessagesRequest
		if err := json.Unmarshal(body, &req); err == nil {
			return strings.TrimSpace(req.Model), req.Stream
		}
	}
	return "", false
}

func truncateForLog(s string, n int) string {
	if n <= 0 {
		return ""
	}
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

func (s *Server) handleWebUpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		APIKey     string `json:"api_key"`
		Regenerate bool   `json:"regenerate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	newKey := strings.TrimSpace(req.APIKey)
	if req.Regenerate {
		k, err := randomProxyKey()
		if err != nil {
			respondErr(w, 500, "internal_error", err.Error())
			return
		}
		newKey = k
	}
	if newKey == "" {
		respondErr(w, 400, "bad_request", "api_key required")
		return
	}
	cfg, err := config.LoadOrInit()
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	cfg.ProxyAPIKey = newKey
	if err := config.Save(cfg); err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	s.svc.Cfg.ProxyAPIKey = newKey
	s.setAPIKey(newKey)
	respondJSON(w, 200, map[string]any{"ok": true, "api_key": newKey})
}

func (s *Server) handleWebBrowserStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	base := oauthBaseURLFromRequest(r)
	login, err := s.svc.StartBrowserLoginWeb(r.Context(), base, "")
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "login_id": login.LoginID, "auth_url": login.AuthURL})
}

func (s *Server) handleWebBrowserCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		LoginID string `json:"login_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.svc.CancelBrowserLoginWeb(strings.TrimSpace(req.LoginID))
	respondJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleWebBrowserCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	loginID := strings.TrimSpace(r.URL.Query().Get("login_id"))
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))

	var err error
	if loginID != "" {
		_, err = s.svc.CompleteBrowserLoginCode(r.Context(), loginID, code, state)
	} else {
		_, err = s.svc.CompleteBrowserLoginCodeByState(r.Context(), code, state)
	}
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("authentication failed: " + err.Error()))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<!doctype html><html><body style='font-family: sans-serif;background:#0f172a;color:#f8fafc;padding:20px'>Login success. You can close this tab and return to codexsess.</body></html>"))
}

func (s *Server) handleWebDeviceStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	login, err := s.svc.StartDeviceLogin(r.Context(), "")
	if err != nil {
		respondErr(w, 500, "internal_error", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{
		"ok": true,
		"login": map[string]any{
			"login_id":                  login.LoginID,
			"user_code":                 login.UserCode,
			"verification_uri":          login.VerificationURI,
			"verification_uri_complete": login.VerificationURIComplete,
			"interval_seconds":          login.IntervalSeconds,
			"expires_at":                login.ExpiresAt,
		},
	})
}

func (s *Server) handleWebDevicePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var req struct {
		LoginID string `json:"login_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON")
		return
	}
	result, err := s.svc.PollDeviceLogin(r.Context(), strings.TrimSpace(req.LoginID))
	if err != nil {
		respondErr(w, 400, "bad_request", err.Error())
		return
	}
	respondJSON(w, 200, map[string]any{"ok": true, "result": result})
}

func randomProxyKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "sk-codexsess-" + hex.EncodeToString(buf), nil
}

func oauthBaseURLFromRequest(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return scheme + "://localhost:3061"
	}
	port := ""
	if _, p, err := net.SplitHostPort(host); err == nil {
		port = p
	} else if i := strings.LastIndex(host, ":"); i > -1 {
		port = host[i+1:]
	}
	if strings.TrimSpace(port) == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return scheme + "://localhost:" + port
}

func externalBaseURLFromRequest(r *http.Request, bindAddr string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)
	if host != "" {
		return scheme + "://" + host
	}
	return scheme + "://" + bindAddr
}
