package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
)

type claudeMessagesRequestState struct {
	request           ClaudeMessagesRequest
	anthropicVersion  string
	toolDefs          []ChatToolDef
	sanitizedMessages []ClaudeMessage
	budgetedMessages  []ClaudeMessage
	budgetedSystem    json.RawMessage
	prompt            string
	directOpts        directCodexRequestOptions
}

func (s *Server) decodeClaudeMessagesRequest(w http.ResponseWriter, r *http.Request, reqID string) (*claudeMessagesRequestState, bool) {
	anthropicVersion := claudeWireTranslator.NormalizeAnthropicVersion(r.Header.Get("anthropic-version"))
	if r.Method != http.MethodPost {
		respondClaudeErr(w, 405, "invalid_request_error", "method not allowed", reqID)
		return nil, false
	}
	if !s.isValidAPIKey(r) {
		respondClaudeErr(w, 401, "authentication_error", "invalid API key", reqID)
		return nil, false
	}
	w.Header().Set("anthropic-version", anthropicVersion)
	w.Header().Set("request-id", reqID)

	var req ClaudeMessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondClaudeErr(w, 400, "invalid_request_error", "invalid JSON body", reqID)
		return nil, false
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
		return nil, false
	}
	req.Model = s.resolveMappedModel(req.Model)
	toolDefs := claudeWireTranslator.MapTools(req.Tools)
	sessionKey := claudeWireTranslator.DeriveSessionKey(req, r)
	sanitizedMessages := claudeWireTranslator.Policy(s).SanitizeMessagesForPrompt(req.Messages, toolDefs, sessionKey)
	budgetedMessages, budgetedSystem := claudeWireTranslator.ApplyTokenBudgetGuard(sanitizedMessages, req.System)
	injectPrompt := s.shouldInjectDirectAPIPrompt()
	prompt := claudeWireTranslator.BuildPrompt(budgetedMessages, budgetedSystem, toolDefs, req.ToolChoice, injectPrompt)
	prompt = claudeWireTranslator.ApplyResponseDefaults(prompt)
	if strings.TrimSpace(prompt) == "" {
		respondClaudeErr(w, 400, "invalid_request_error", "messages are required", reqID)
		return nil, false
	}
	return &claudeMessagesRequestState{
		request:           req,
		anthropicVersion:  anthropicVersion,
		toolDefs:          toolDefs,
		sanitizedMessages: sanitizedMessages,
		budgetedMessages:  budgetedMessages,
		budgetedSystem:    budgetedSystem,
		prompt:            prompt,
		directOpts: directCodexRequestOptions{
			MaxOutputTokens: req.MaxTokens,
			StopSequences:   req.StopSequences,
			Tools:           toolDefs,
			ToolChoice:      req.ToolChoice,
			ClaudeProtocol:  true,
			AnthropicVer:    anthropicVersion,
		},
	}, true
}
