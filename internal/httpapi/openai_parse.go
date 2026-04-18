package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
)

type openAIChatRequestState struct {
	request        ChatCompletionsRequest
	prompt         string
	structuredSpec *structuredOutputSpec
	directOpts     directCodexRequestOptions
}

type openAIResponsesRequestState struct {
	request        ResponsesRequest
	prompt         string
	structuredSpec *structuredOutputSpec
	directOpts     directCodexRequestOptions
}

func (s *Server) decodeOpenAIChatRequest(w http.ResponseWriter, r *http.Request) (*openAIChatRequestState, bool) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return nil, false
	}
	if !s.isValidAPIKey(r) {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return nil, false
	}
	var req ChatCompletionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON body")
		return nil, false
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-5.2-codex"
	}
	req.Model = s.resolveMappedModel(req.Model)
	structuredSpec, err := openAIWireTranslator.NormalizeResponseFormat(req.ResponseFormat)
	if err != nil {
		respondErr(w, 400, "invalid_request_error", err.Error())
		return nil, false
	}
	injectPrompt := s.shouldInjectDirectAPIPrompt()
	normalized := openAIWireTranslator.BuildChatPrompt(req, injectPrompt)
	return &openAIChatRequestState{
		request:        req,
		prompt:         normalized.Prompt,
		structuredSpec: structuredSpec,
		directOpts:     normalized.DirectOpts,
	}, true
}

func (s *Server) decodeOpenAIResponsesRequest(w http.ResponseWriter, r *http.Request) (*openAIResponsesRequestState, bool) {
	if r.Method != http.MethodPost {
		respondErr(w, 405, "method_not_allowed", "method not allowed")
		return nil, false
	}
	if !s.isValidAPIKey(r) {
		respondErr(w, 401, "unauthorized", "invalid API key")
		return nil, false
	}
	var req ResponsesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErr(w, 400, "bad_request", "invalid JSON body")
		return nil, false
	}
	structuredSpec, err := openAIWireTranslator.NormalizeResponseFormat(nil)
	if req.Text != nil {
		structuredSpec, err = openAIWireTranslator.NormalizeResponseFormat(req.Text.Format)
	}
	if err != nil {
		respondErr(w, 400, "invalid_request_error", err.Error())
		return nil, false
	}
	if strings.TrimSpace(req.Model) == "" {
		req.Model = "gpt-5.2-codex"
	}
	req.Model = s.resolveMappedModel(req.Model)
	injectPrompt := s.shouldInjectDirectAPIPrompt()
	normalized := openAIWireTranslator.BuildResponsesPrompt(req, injectPrompt)
	if strings.TrimSpace(normalized.Prompt) == "" {
		respondErr(w, 400, "bad_request", "input is required")
		return nil, false
	}
	return &openAIResponsesRequestState{
		request:        req,
		prompt:         normalized.Prompt,
		structuredSpec: structuredSpec,
		directOpts:     normalized.DirectOpts,
	}, true
}
