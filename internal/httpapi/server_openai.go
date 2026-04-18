package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
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
	reqID := "req_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	state, ok := s.decodeOpenAIChatRequest(w, r)
	if !ok {
		return
	}
	s.executeProxyProtocol(w, r, proxyPipeline{
		Plan: proxyProtocolPlan{
			RequestID:  reqID,
			Selector:   "",
			Model:      state.request.Model,
			Prompt:     state.prompt,
			DirectOpts: state.directOpts,
			Stream:     state.request.Stream,
		},
		Adapter: proxyProtocolAdapter{
			WriteSetupError: writeOpenAIExecutionSetupError,
			WriteStream: func(w http.ResponseWriter, r *http.Request, exec *proxyExecutionSession, status *int) {
				s.writeChatCompletionsStreamResponse(w, r, reqID, state, exec, status)
			},
			WriteJSON: func(w http.ResponseWriter, result proxyBackendResult, status *int) {
				writeChatCompletionsJSONResponse(w, reqID, state, result, status)
			},
			WriteError: func(w http.ResponseWriter, err error, status *int) {
				code, errType := openAIWireTranslator.ClassifyUpstreamError(err)
				*status = code
				respondErr(w, code, errType, err.Error())
			},
		},
	})
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	reqID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	state, ok := s.decodeOpenAIResponsesRequest(w, r)
	if !ok {
		return
	}
	s.executeProxyProtocol(w, r, proxyPipeline{
		Plan: proxyProtocolPlan{
			RequestID:  reqID,
			Selector:   "",
			Model:      state.request.Model,
			Prompt:     state.prompt,
			DirectOpts: state.directOpts,
			Stream:     state.request.Stream,
		},
		Adapter: proxyProtocolAdapter{
			WriteSetupError: writeOpenAIExecutionSetupError,
			WriteStream: func(w http.ResponseWriter, r *http.Request, exec *proxyExecutionSession, status *int) {
				s.writeResponsesStreamResponse(w, r, reqID, state, exec, status)
			},
			WriteJSON: func(w http.ResponseWriter, result proxyBackendResult, status *int) {
				writeResponsesJSONResponse(w, reqID, state, result, status)
			},
			WriteError: func(w http.ResponseWriter, err error, status *int) {
				code, errType := openAIWireTranslator.ClassifyUpstreamError(err)
				*status = code
				respondErr(w, code, errType, err.Error())
			},
		},
	})
}
