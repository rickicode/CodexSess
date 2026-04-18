package httpapi

import "net/http"

func (s *Server) registerProxyRoutes(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/v1/models", s.withTrafficLog("openai", s.handleModels))
	mux.HandleFunc("/v1", s.withTrafficLog("openai", s.handleOpenAIRoot))
	mux.HandleFunc("/v1/chat/completions", s.withTrafficLog("openai", s.handleChatCompletions))
	mux.HandleFunc("/v1/responses", s.withTrafficLog("openai", s.handleResponses))
	mux.HandleFunc("/v1/auth.json", s.handleAPIAuthJSON)
	mux.HandleFunc("/v1/usage", s.withTrafficLog("openai", s.handleAPIUsageStatus))
	mux.HandleFunc("/v1/messages", s.withTrafficLog("claude", s.handleClaudeMessages))
	mux.HandleFunc("/claude/v1/messages", s.withTrafficLog("claude", s.handleClaudeMessages))
}
